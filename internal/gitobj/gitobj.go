// Package gitobj reads a repository's HEAD commit through git's object
// database instead of the working tree: the committed file list (ListHEAD) and
// committed blob contents (Reader). It shells out to the git binary with
// GIT_OPTIONAL_LOCKS=0, so it is read-only and never takes index.lock.
package gitobj

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrNoHEAD is returned when the repository has no commit yet (unborn HEAD).
var ErrNoHEAD = errors.New("gitobj: repository has no commit at HEAD")

// Blob is one regular file in the HEAD tree.
type Blob struct {
	Path string // slash-separated, relative to the repository root
	OID  string // git object id, hex
	Size int64  // blob size in bytes
}

// IsRepoRoot reports whether dir is the top-level directory of a git work tree.
// A subdirectory of a repository, or a non-git directory, is false.
func IsRepoRoot(ctx context.Context, dir string) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, _, err := runGit(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	// Resolve symlinks on both sides, so /var and /private/var compare equal.
	// Abs first: --show-toplevel is absolute, and a relative dir (".") would
	// otherwise never compare equal to it.
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	want, errDir := filepath.EvalSymlinks(abs)
	got, errTop := filepath.EvalSymlinks(strings.TrimSuffix(string(out), "\n"))
	return errDir == nil && errTop == nil && want == got
}

// ListHEAD lists the regular-file blobs (modes 100644 and 100755) of HEAD's full
// tree. Symlinks and gitlinks (submodules) are excluded. An unborn HEAD returns
// ErrNoHEAD.
func ListHEAD(ctx context.Context, root string) ([]Blob, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	out, stderr, err := runGit(ctx, root, "ls-tree", "-r", "-l", "-z", "--full-tree", "HEAD")
	if err != nil {
		if headUnborn(ctx, root) {
			return nil, ErrNoHEAD
		}
		return nil, gitErr("git ls-tree HEAD failed", stderr, err)
	}
	return parseLsTree(string(out))
}

// HeadBlobs returns HEAD's blobs when root is a repository's top-level directory
// with a commit. ok is false for anything else (a subdirectory, a directory
// without its own .git, or an unborn HEAD): the caller reads the disk then.
func HeadBlobs(ctx context.Context, root string) (blobs []Blob, ok bool, err error) {
	if !IsRepoRoot(ctx, root) {
		return nil, false, nil
	}
	blobs, err = ListHEAD(ctx, root)
	if errors.Is(err, ErrNoHEAD) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return blobs, true, nil
}

// CommittedGitDir returns the absolute git directory of the repository whose
// top-level directory is root, when that repository has a commit. A linked
// worktree yields its own gitdir, where its HEAD and logs/HEAD live. ok is false
// for anything HeadBlobs would also refuse.
func CommittedGitDir(ctx context.Context, root string) (dir string, ok bool, err error) {
	if !IsRepoRoot(ctx, root) || headUnborn(ctx, root) {
		return "", false, nil
	}
	out, stderr, err := runGit(ctx, root, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", false, gitErr("git rev-parse --absolute-git-dir failed", stderr, err)
	}
	return strings.TrimSpace(string(out)), true, nil
}

// headUnborn reports whether HEAD names no object yet. Only the failure path
// asks: `rev-parse --verify --quiet` exits 1 for a missing ref and 128 when dir
// is not a repository, so exit 1 alone means unborn. Locale does not matter.
func headUnborn(ctx context.Context, root string) bool {
	_, _, err := runGit(ctx, root, "rev-parse", "--verify", "--quiet", "HEAD")
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == 1
}

// parseLsTree reads `git ls-tree -r -l -z` output: one NUL-terminated record per
// entry, "<mode> SP <type> SP <oid> SP <size> TAB <path>". The metadata has no
// tabs and the size is space-padded, so the record splits on the first TAB and
// on runs of spaces. Only regular files (100644, 100755) are kept.
func parseLsTree(out string) ([]Blob, error) {
	var blobs []Blob
	for rec := range strings.SplitSeq(out, "\x00") {
		if rec == "" {
			continue
		}
		meta, path, ok := strings.Cut(rec, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 4 {
			return nil, fmt.Errorf("gitobj: malformed ls-tree record %q", rec)
		}
		if mode := fields[0]; mode != "100644" && mode != "100755" {
			continue
		}
		size, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("gitobj: ls-tree size for %q: %w", path, err)
		}
		blobs = append(blobs, Blob{Path: path, OID: fields[2], Size: size})
	}
	return blobs, nil
}

// probeTimeout bounds the cheap rev-parse probe, so the hook path cannot hang
// on a network filesystem. listTimeout bounds the full-tree ls-tree walk.
const (
	probeTimeout = 30 * time.Second
	listTimeout  = 10 * time.Minute
)

// runGit runs git -C dir args with the read-only environment. It returns stdout
// and stderr separately; the caller applies any deadline through ctx.
func runGit(ctx context.Context, dir string, args ...string) ([]byte, string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	return out.Bytes(), errBuf.String(), err
}

// gitEnv is the process environment plus GIT_OPTIONAL_LOCKS=0, so a read never
// refreshes or locks the index.
func gitEnv() []string {
	return append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
}

// gitErr renders a git failure with git's stderr, or the exec error when git
// printed nothing (the same shape as internal/sources).
func gitErr(what, stderr string, err error) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		return fmt.Errorf("gitobj: %s: %w", what, err)
	}
	return fmt.Errorf("gitobj: %s: %s", what, msg)
}

// Reader streams blob contents through one long-lived `git cat-file --batch`
// process. It is not safe for concurrent use.
type Reader struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *bufio.Reader
}

// NewReader starts `git cat-file --batch` in root. The process lives until
// Close is called or ctx is cancelled.
func NewReader(ctx context.Context, root string) (*Reader, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "cat-file", "--batch")
	cmd.Env = gitEnv()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("gitobj: cat-file stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("gitobj: cat-file stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("gitobj: start cat-file: %w", err)
	}
	return &Reader{cmd: cmd, stdin: stdin, out: bufio.NewReader(stdout)}, nil
}

// Read returns the full contents of the object oid. A missing or ambiguous
// object is an error. A protocol error leaves the stream unusable; Close it.
func (r *Reader) Read(oid string) ([]byte, error) {
	if oid == "" || strings.ContainsAny(oid, " \t\r\n") {
		return nil, fmt.Errorf("gitobj: invalid object id %q", oid)
	}
	if _, err := io.WriteString(r.stdin, oid+"\n"); err != nil {
		return nil, fmt.Errorf("gitobj: request %s: %w", oid, err)
	}
	header, err := r.out.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("gitobj: header for %s: %w", oid, err)
	}
	fields := strings.Fields(header)
	if len(fields) == 2 && (fields[1] == "missing" || fields[1] == "ambiguous") {
		return nil, fmt.Errorf("gitobj: object %s is %s", oid, fields[1])
	}
	if len(fields) != 3 {
		return nil, fmt.Errorf("gitobj: malformed cat-file header %q", header)
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 {
		return nil, fmt.Errorf("gitobj: cat-file size in header %q", header)
	}
	body := make([]byte, size+1) // the contents, then the LF cat-file appends
	if _, err := io.ReadFull(r.out, body); err != nil {
		return nil, fmt.Errorf("gitobj: contents of %s: %w", oid, err)
	}
	if body[size] != '\n' {
		return nil, fmt.Errorf("gitobj: contents of %s not LF-terminated", oid)
	}
	return body[:size], nil
}

// Close stops the cat-file process by closing its stdin, then returns its exit
// error, if any.
func (r *Reader) Close() error {
	_ = r.stdin.Close()
	if err := r.cmd.Wait(); err != nil {
		return fmt.Errorf("gitobj: cat-file --batch: %w", err)
	}
	return nil
}
