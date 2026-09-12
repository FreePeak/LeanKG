// Git source (Rust src/sources/git.rs): clone/fetch a repository into the
// staging directory, poll the remote tip without cloning, and materialize a
// tree without a persistent clone.
//
// Every git invocation is argv-only (no shell) and bounded: `--depth 1` keeps
// the clone shallow, the ref is pinned, and each call gets a deadline from
// LEANKG_GIT_TIMEOUT seconds (default 10 minutes) so an unreachable host fails
// instead of hanging the index/watch loop. The Rust reference had no timeout.
package sources

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// defaultGitTimeout bounds one git invocation when LEANKG_GIT_TIMEOUT is unset.
const defaultGitTimeout = 10 * time.Minute

// GitSource is a `git+<transport url>` source.
type GitSource struct {
	URL     string
	Auth    string
	RefName string
}

// Name implements Source.
func (g *GitSource) Name() string { return "git" }

// SyncToLocal clones the repository on first use and fetches/checkouts the ref
// afterwards (Rust git.rs sync_to_local).
func (g *GitSource) SyncToLocal(ctx context.Context, stagingRoot string, progress ProgressReporter) (string, error) {
	localDir, err := stagingPath(stagingRoot, g.uri())
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(filepath.Join(localDir, ".git")); statErr == nil && info.IsDir() {
		report(progress, fmt.Sprintf("git repo exists at %s, pulling %s...", localDir, g.RefName))
		if err := fetchAndCheckout(ctx, localDir, g.RefName, progress); err != nil {
			return "", err
		}
		return localDir, nil
	}
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return "", err
	}
	report(progress, fmt.Sprintf("cloning %s (ref: %s)...", g.URL, g.RefName))
	if err := cloneRepo(ctx, injectAuth(g.URL, g.Auth), localDir, g.RefName, progress); err != nil {
		return "", err
	}
	return localDir, nil
}

// RemoteFingerprint polls the remote tip SHA with `git ls-remote` (no clone).
// An empty result means the ref could not be resolved (a bare SHA ref, or a
// remote that does not advertise it) and yields "" — Rust returned Ok(None).
func (g *GitSource) RemoteFingerprint(ctx context.Context) (string, error) {
	stdout, stderr, err := runGit(ctx, "", "ls-remote", injectAuth(g.URL, g.Auth), g.RefName)
	if err != nil {
		return "", gitError("git ls-remote failed", stderr, err)
	}
	fields := strings.Fields(stdout)
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], nil
}

// MaterializeEphemeral tries `git archive --remote` (no clone) and falls back
// to a shallow clone, matching Rust git.rs materialize_ephemeral.
func (g *GitSource) MaterializeEphemeral(ctx context.Context, stagingRoot string, progress ProgressReporter) (string, error) {
	localDir, err := stagingPath(stagingRoot, g.uri())
	if err != nil {
		return "", err
	}
	fetchURL := injectAuth(g.URL, g.Auth)

	report(progress, fmt.Sprintf("downloading archive for %s @ %s...", g.URL, g.RefName))
	if err := archiveExtract(ctx, fetchURL, g.RefName, localDir, progress); err == nil {
		return localDir, nil
	}

	report(progress, "archive download failed; falling back to shallow clone")
	if err := os.RemoveAll(localDir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return "", err
	}
	if err := cloneRepo(ctx, fetchURL, localDir, g.RefName, progress); err != nil {
		return "", err
	}
	return localDir, nil
}

func (g *GitSource) uri() URI { return URI{Kind: KindGit, URL: g.URL} }

// injectAuth injects a token into an https URL as `oauth2:<token>@host`
// (Rust maybe_inject_auth). Empty auth, and every non-https URL, pass through
// unchanged.
func injectAuth(url, auth string) string {
	if auth == "" {
		return url
	}
	if rest, ok := strings.CutPrefix(url, "https://"); ok {
		return "https://oauth2:" + auth + "@" + rest
	}
	return url
}

// cloneRepo does a shallow, branch-pinned clone and falls back to a full clone
// plus checkout when the ref is not a branch tip (a tag or a bare SHA).
func cloneRepo(ctx context.Context, url, dir, refName string, progress ProgressReporter) error {
	report(progress, fmt.Sprintf("git clone --depth 1 --branch %s ...", refName))
	if _, stderr, err := runGit(ctx, "", "clone", "--depth", "1", "--branch", refName, url, dir); err != nil {
		// Fallback: clone the default branch, then check the ref out.
		// Rust reported only the fallback's stderr; keep the shallow failure
		// in the progress stream so a real cause is still visible.
		report(progress, "shallow clone failed ("+firstLine(stderr, err)+"), trying full clone + checkout...")
		if _, stderr2, err2 := runGit(ctx, "", "clone", url, dir); err2 != nil {
			return gitError("git clone fallback failed", stderr2, err2)
		}
	}
	return fetchAndCheckout(ctx, dir, refName, progress)
}

// fetchAndCheckout fetches every remote ref, checks the requested ref out
// (branch, then origin/<ref>), and fast-forwards an existing branch. The
// pull/merge/reset tail is non-fatal in Rust and stays non-fatal here.
func fetchAndCheckout(ctx context.Context, repoDir, refName string, progress ProgressReporter) error {
	if _, stderr, err := runGit(ctx, repoDir, "fetch", "--all", "--prune"); err != nil {
		return gitError("git fetch failed", stderr, err)
	}

	report(progress, "git checkout "+refName+" ...")
	if _, checkoutStderr, err := runGit(ctx, repoDir, "checkout", refName); err != nil {
		remoteRef := "origin/" + refName
		report(progress, "trying remote ref "+remoteRef+" ...")
		if _, remoteStderr, err2 := runGit(ctx, repoDir, "checkout", "-b", refName, remoteRef); err2 != nil {
			return fmt.Errorf("git checkout %s failed: %s (tried %s: %s)",
				refName, strings.TrimSpace(checkoutStderr), remoteRef, strings.TrimSpace(remoteStderr))
		}
	}

	// A shallow depth can block a fast-forward pull: unshallow, then
	// fast-forward, then hard-reset to the remote tracking branch.
	_, pullStderr, pullErr := runGit(ctx, repoDir, "pull", "--ff-only")
	if pullErr == nil {
		report(progress, "pulled latest")
		return nil
	}
	report(progress, "git pull skipped ("+firstLine(pullStderr, pullErr)+"), attempting fetch --unshallow + reset...")
	if _, _, err := runGit(ctx, repoDir, "fetch", "--unshallow"); err != nil {
		report(progress, "fetch --unshallow failed; keeping the existing worktree")
		return nil
	}
	if _, _, err := runGit(ctx, repoDir, "merge", "--ff-only", "origin/"+refName); err == nil {
		report(progress, "fast-forward after unshallow")
		return nil
	}
	report(progress, "reset to origin/HEAD as fallback")
	if _, _, err := runGit(ctx, repoDir, "reset", "--hard", "origin/"+refName); err == nil {
		report(progress, "reset to origin/HEAD OK")
	}
	return nil
}

// archiveExtract streams `git archive --remote <url> <ref> --format=tar` into
// dest. It fails when the remote does not support the archive protocol (the
// usual case for GitHub over https), which makes the caller fall back to a
// shallow clone. Rust piped into the system tar; the archive/tar stdlib reader
// removes the external-tar dependency and lets entry paths be validated.
func archiveExtract(ctx context.Context, url, refName, dest string, progress ProgressReporter) error {
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("remove old staging: %w", err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}

	archCtx, cancel := gitContext(ctx)
	defer cancel()
	cmd := exec.CommandContext(archCtx, "git", "archive", "--remote", url, refName, "--format=tar")
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return gitError("git archive --remote failed", "", err)
	}
	if err := cmd.Start(); err != nil {
		return gitError("git archive --remote spawn failed", stderr.String(), err)
	}
	extractErr := extractTar(pipe, dest)
	waitErr := cmd.Wait()
	if waitErr != nil || extractErr != nil {
		_ = os.RemoveAll(dest)
		if waitErr != nil {
			return gitError("git archive failed", stderr.String(), waitErr)
		}
		return fmt.Errorf("tar extraction failed: %w", extractErr)
	}
	report(progress, "archive extracted to "+dest)
	return nil
}

// extractTar writes a tar stream into dest, refusing entries that would land
// outside it (guard: the archive comes from a remote, so its paths are
// untrusted).
func extractTar(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if name == "." {
			continue
		}
		target := filepath.Join(dest, name)
		if name == ".." || filepath.IsAbs(name) || !strings.HasPrefix(target, filepath.Clean(dest)+string(filepath.Separator)) {
			return fmt.Errorf("tar entry %q escapes the destination", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			// git archive emits directories and regular files for a tree.
		}
	}
}

// runGit runs one git command with argv (never a shell) under the git
// deadline and returns stdout/stderr as text.
func runGit(ctx context.Context, dir string, args ...string) (stdout, stderr string, err error) {
	ctx, cancel := gitContext(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err = cmd.Run()
	return out.String(), errBuf.String(), err
}

// gitContext applies the per-invocation git deadline to a caller context.
func gitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if d := gitTimeout(); d > 0 {
		return context.WithTimeout(ctx, d)
	}
	return context.WithCancel(ctx)
}

// gitTimeout is the per-invocation git deadline: LEANKG_GIT_TIMEOUT seconds,
// or defaultGitTimeout. A non-positive value disables the deadline.
func gitTimeout() time.Duration {
	if v, ok := os.LookupEnv("LEANKG_GIT_TIMEOUT"); ok {
		if secs, err := strconv.Atoi(v); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	return defaultGitTimeout
}

// gitError renders the git diagnostic the way the Rust code did: the command
// context plus git's stderr, or the exec failure when git produced none.
func gitError(what, stderr string, err error) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		return fmt.Errorf("%s: %w", what, err)
	}
	return fmt.Errorf("%s: %s", what, msg)
}

// firstLine condenses a git diagnostic for a progress line.
func firstLine(stderr string, err error) string {
	msg := strings.TrimSpace(stderr)
	if msg == "" && err != nil {
		msg = err.Error()
	}
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	if msg == "" {
		msg = "no output"
	}
	return msg
}
