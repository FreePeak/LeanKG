package sources

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/langs"
)

// hermeticGit skips when git is unavailable (the source drives the binary) and
// pins the fixture environment so a user's global config cannot change the
// outcome. The library's own git invocations inherit these variables.
func hermeticGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH; the git source requires the git binary")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	for _, kv := range [][2]string{
		{"GIT_AUTHOR_NAME", "leankg test"},
		{"GIT_AUTHOR_EMAIL", "test@example.invalid"},
		{"GIT_COMMITTER_NAME", "leankg test"},
		{"GIT_COMMITTER_EMAIL", "test@example.invalid"},
	} {
		t.Setenv(kv[0], kv[1])
	}
}

// gitOut runs git in dir and fails the test on a non-zero exit.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newOriginRepo builds a real repository with one commit on branch main.
func newOriginRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitOut(t, dir, "init", "-q")
	gitOut(t, dir, "checkout", "-q", "-b", "main")
	commitFile(t, dir, "code.go", "package fixture\n")
	return dir
}

// commitFile commits one file and returns the new HEAD sha.
func commitFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "add", ".")
	gitOut(t, dir, "commit", "-q", "-m", "add "+name)
	return gitOut(t, dir, "rev-parse", "HEAD")
}

func TestGitSourceCloneThenFetch(t *testing.T) {
	hermeticGit(t)
	origin := newOriginRepo(t)
	staging := t.TempDir()
	progress := &recorder{}
	src := &GitSource{URL: "file://" + origin, RefName: "main"}

	got, err := src.SyncToLocal(context.Background(), staging, progress)
	if err != nil {
		t.Fatalf("SyncToLocal: %v", err)
	}
	if want := filepath.Join(staging, StagingDir(URI{Kind: KindGit, URL: src.URL})); got != want {
		t.Fatalf("sync dir = %q, want %q", got, want)
	}
	content, err := os.ReadFile(filepath.Join(got, "code.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "package fixture\n" {
		t.Fatalf("code.go = %q", content)
	}
	if info, err := os.Stat(filepath.Join(got, ".git")); err != nil || !info.IsDir() {
		t.Fatalf(".git in the synced tree: %v", err)
	}

	// Second sync takes the fetch/checkout path on the existing clone and
	// advances it to the new tip.
	head := commitFile(t, origin, "second.go", "package fixture\n\nconst Second = 1\n")
	progress.messages = nil
	got2, err := src.SyncToLocal(context.Background(), staging, progress)
	if err != nil {
		t.Fatalf("second SyncToLocal: %v", err)
	}
	if got2 != got {
		t.Fatalf("second sync dir = %q, want %q", got2, got)
	}
	if head2 := gitOut(t, got2, "rev-parse", "HEAD"); head2 != head {
		t.Fatalf("clone HEAD = %s, want %s", head2, head)
	}
	if !progress.contains("pulling main") {
		t.Fatalf("second sync reused no clone; progress = %q", progress.messages)
	}
}

func TestGitSourcePinsACommitSHA(t *testing.T) {
	hermeticGit(t)
	origin := newOriginRepo(t)
	sha := gitOut(t, origin, "rev-parse", "HEAD")

	// `clone --depth 1 --branch <sha>` cannot resolve a bare commit, so this
	// exercises the full-clone + checkout fallback.
	src := &GitSource{URL: "file://" + origin, RefName: sha}
	got, err := src.SyncToLocal(context.Background(), t.TempDir(), &recorder{})
	if err != nil {
		t.Fatalf("SyncToLocal: %v", err)
	}
	if head := gitOut(t, got, "rev-parse", "HEAD"); head != sha {
		t.Fatalf("HEAD = %s, want the pinned %s", head, sha)
	}
}

func TestGitSourceBadRefFails(t *testing.T) {
	hermeticGit(t)
	origin := newOriginRepo(t)
	src := &GitSource{URL: "file://" + origin, RefName: "no-such-ref"}

	_, err := src.SyncToLocal(context.Background(), t.TempDir(), &recorder{})
	if err == nil {
		t.Fatal("SyncToLocal with a missing ref succeeded")
	}
	if !strings.Contains(err.Error(), "git checkout no-such-ref failed") {
		t.Fatalf("error = %q, want the checkout failure to name the ref", err)
	}
}

func TestGitSourceMissingRemoteFails(t *testing.T) {
	hermeticGit(t)
	src := &GitSource{URL: "file:///nonexistent/leankg-test-repo", RefName: "main"}

	_, err := src.SyncToLocal(context.Background(), t.TempDir(), &recorder{})
	if err == nil {
		t.Fatal("SyncToLocal against a missing repository succeeded")
	}
	if !strings.Contains(err.Error(), "git clone fallback failed") {
		t.Fatalf("error = %q, want the clone failure", err)
	}
}

func TestGitSourceFingerprintTracksTheTip(t *testing.T) {
	hermeticGit(t)
	origin := newOriginRepo(t)
	first := gitOut(t, origin, "rev-parse", "HEAD")
	src := &GitSource{URL: "file://" + origin, RefName: "main"}

	fp, err := src.RemoteFingerprint(context.Background())
	if err != nil {
		t.Fatalf("RemoteFingerprint: %v", err)
	}
	if fp != first {
		t.Fatalf("fingerprint = %q, want the tip %q", fp, first)
	}

	second := commitFile(t, origin, "next.go", "package fixture\n\nconst Next = 2\n")
	fp2, err := src.RemoteFingerprint(context.Background())
	if err != nil {
		t.Fatalf("RemoteFingerprint after a commit: %v", err)
	}
	if fp2 != second || fp2 == fp {
		t.Fatalf("fingerprint = %q, want the new tip %q", fp2, second)
	}
}

func TestGitSourceFingerprintOfAMissingRefIsEmpty(t *testing.T) {
	hermeticGit(t)
	origin := newOriginRepo(t)
	src := &GitSource{URL: "file://" + origin, RefName: "no-such-ref"}

	fp, err := src.RemoteFingerprint(context.Background())
	if err != nil {
		t.Fatalf("RemoteFingerprint: %v", err)
	}
	if fp != "" {
		t.Fatalf("fingerprint = %q, want an empty fingerprint for an unresolvable ref", fp)
	}
}

func TestGitSourceMaterializeEphemeralFallsBackToAClone(t *testing.T) {
	hermeticGit(t)
	origin := newOriginRepo(t)
	// git archive --remote is not supported over the file transport, so the
	// shallow-clone fallback is the live path here (and the common path for
	// https remotes too).
	src := &GitSource{URL: "file://" + origin, RefName: "main"}

	got, err := src.MaterializeEphemeral(context.Background(), t.TempDir(), &recorder{})
	if err != nil {
		t.Fatalf("MaterializeEphemeral: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(got, "code.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "package fixture\n" {
		t.Fatalf("code.go = %q", content)
	}
}

// fakeBasicB64 is base64("oauth2:FAKETOKEN"), the value the Basic header carries.
const fakeBasicB64 = "b2F1dGgyOkZBS0VUT0tFTg=="

// envValues returns the last value of each KEY=VALUE pair, as exec applies them.
func envValues(env []string) map[string]string {
	out := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

func TestGitRemoteCarriesTheTokenOutOfBand(t *testing.T) {
	const clean = "https://example.invalid/group/repo.git"
	for _, in := range []string{clean, "https://oauth2:FAKETOKEN@example.invalid/group/repo.git"} {
		url, env := gitRemote(in, "FAKETOKEN")
		if url != clean {
			t.Errorf("gitRemote(%q) url = %q, want the clean %q", in, url, clean)
		}
		want := map[string]string{
			"GIT_CONFIG_COUNT":    "1",
			"GIT_CONFIG_KEY_0":    "http.https://example.invalid/.extraheader",
			"GIT_CONFIG_VALUE_0":  "Authorization: Basic " + fakeBasicB64,
			"GIT_TERMINAL_PROMPT": "0",
		}
		got := envValues(env)
		for k, v := range want {
			if got[k] != v {
				t.Errorf("gitRemote(%q) env %s = %q, want %q", in, k, got[k], v)
			}
		}
	}
}

func TestGitRemotePassesThroughWithoutACredential(t *testing.T) {
	tests := []struct{ name, url, token string }{
		{"empty token", "https://example.invalid/x.git", ""},
		{"ssh", "ssh://git@example.invalid/x.git", "FAKETOKEN"},
		{"scp-like", "git@example.invalid:x.git", "FAKETOKEN"},
		{"file", "file:///srv/git/x", "FAKETOKEN"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			url, env := gitRemote(tc.url, tc.token)
			if url != tc.url || env != nil {
				t.Fatalf("gitRemote(%q, %q) = (%q, %d env vars), want the URL unchanged and no env", tc.url, tc.token, url, len(env))
			}
		})
	}
}

// fakeGit puts a git stub first on PATH. Every call appends its argv and the
// credential environment to a log, so a test sees what the real command line
// and environment would carry, with no network. FAKE_GIT_ORIGIN is what
// `git config --get remote.origin.url` prints; FAKE_GIT_FAIL makes every call
// fail and echo FAKE_GIT_SECRET the way a careless git diagnostic would.
func fakeGit(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "git.log")
	script := `#!/bin/sh
for last; do :; done
{
  printf 'argv'
  for a in "$@"; do printf ' [%s]' "$a"; done
  printf ' | env count=%s key=%s value=%s prompt=%s\n' "$GIT_CONFIG_COUNT" "$GIT_CONFIG_KEY_0" "$GIT_CONFIG_VALUE_0" "$GIT_TERMINAL_PROMPT"
} >>"$FAKE_GIT_LOG"
if [ -n "$FAKE_GIT_FAIL" ]; then
  echo "fatal: rejected $GIT_CONFIG_VALUE_0 $FAKE_GIT_SECRET" >&2
  exit 128
fi
case "$1" in
  config) echo "$FAKE_GIT_ORIGIN" ;;
  clone) mkdir -p "$last" ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_GIT_LOG", logPath)
	t.Setenv("FAKE_GIT_ORIGIN", "")
	t.Setenv("FAKE_GIT_FAIL", "")
	t.Setenv("FAKE_GIT_SECRET", "")
	return logPath
}

// TestCredentialStaysOutOfArgvAndURL drives every network call through the
// fake git: the clone, the ls-remote, and the archive. None may carry the token
// or its Basic form in argv, and each must carry the header in its environment.
func TestCredentialStaysOutOfArgvAndURL(t *testing.T) {
	logPath := fakeGit(t)
	const clean = "https://example.invalid/group/repo.git"
	t.Setenv("FAKE_GIT_ORIGIN", clean)
	src := &GitSource{URL: clean, Auth: "FAKETOKEN", RefName: "main"}

	if _, err := src.SyncToLocal(context.Background(), t.TempDir(), &recorder{}); err != nil {
		t.Fatalf("SyncToLocal: %v", err)
	}
	if _, err := src.RemoteFingerprint(context.Background()); err != nil {
		t.Fatalf("RemoteFingerprint: %v", err)
	}
	if _, err := src.MaterializeEphemeral(context.Background(), t.TempDir(), &recorder{}); err != nil {
		t.Fatalf("MaterializeEphemeral: %v", err)
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, call := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		argv, env, _ := strings.Cut(call, " | env ")
		if strings.Contains(argv, "FAKETOKEN") || strings.Contains(argv, fakeBasicB64) {
			t.Errorf("argv carries the credential: %s", argv)
		}
		for _, verb := range []string{"clone", "ls-remote", "archive", "fetch"} {
			if !strings.HasPrefix(argv, "argv [") || !strings.Contains(argv, "["+verb+"]") {
				continue
			}
			seen[verb] = true
			if verb != "fetch" && !strings.Contains(argv, "["+clean+"]") {
				t.Errorf("%s argv does not name the clean URL: %s", verb, argv)
			}
			want := "count=1 key=http.https://example.invalid/.extraheader value=Authorization: Basic " + fakeBasicB64 + " prompt=0"
			if env != want {
				t.Errorf("%s env = %q, want %q", verb, env, want)
			}
		}
	}
	for _, verb := range []string{"clone", "ls-remote", "archive", "fetch"} {
		if !seen[verb] {
			t.Errorf("no %s call reached the fake git; the check is vacuous", verb)
		}
	}
}

// routeHTTPSTo points git's https transport for remote at a local repository,
// so the credential path runs against real git with no network. The remote
// URL that git stores stays the https one.
func routeHTTPSTo(t *testing.T, remote, localDir string) {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	body := "[url \"file://" + localDir + "\"]\n\tinsteadOf = " + remote + "\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
}

// assertCleanGitConfig fails when .git/config carries the token, its Basic
// form, or any userinfo, and returns the file for the evidence log.
func assertCleanGitConfig(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"FAKETOKEN", fakeBasicB64, "oauth2:", "@example.invalid"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf(".git/config carries %q:\n%s", secret, raw)
		}
	}
	return string(raw)
}

// The clone and the fetch run real git against a local repository reached
// through an https insteadOf rewrite. The stored remote must stay clean.
func TestCloneAndFetchLeaveNoCredentialInGitConfig(t *testing.T) {
	hermeticGit(t)
	origin := newOriginRepo(t)
	const remote = "https://example.invalid/repo.git"
	routeHTTPSTo(t, remote, origin)
	dest := filepath.Join(t.TempDir(), "clone")

	if err := CloneRepo(context.Background(), remote, "FAKETOKEN", dest, "main", &recorder{}); err != nil {
		t.Fatalf("CloneRepo: %v", err)
	}
	t.Logf(".git/config after clone:\n%s", assertCleanGitConfig(t, dest))

	// The environment must make real git see the header under the host scope.
	_, env := gitRemote(remote, "FAKETOKEN")
	cmd := exec.Command("git", "config", "--get", "http.https://example.invalid/.extraheader")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git config --get under the credential env: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "Authorization: Basic "+fakeBasicB64 {
		t.Fatalf("extraheader = %q", got)
	}

	commitFile(t, origin, "second.go", "package fixture\n")
	if err := FetchAndCheckout(context.Background(), dest, "FAKETOKEN", "main", &recorder{}); err != nil {
		t.Fatalf("FetchAndCheckout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "second.go")); err != nil {
		t.Fatalf("the fetched commit did not land: %v", err)
	}
	assertCleanGitConfig(t, dest)
}

func TestSyncedCloneIsIndexableAndHidesGit(t *testing.T) {
	hermeticGit(t)
	origin := newOriginRepo(t)
	src := &GitSource{URL: "file://" + origin, RefName: "main"}
	got, err := src.SyncToLocal(context.Background(), t.TempDir(), &recorder{})
	if err != nil {
		t.Fatalf("SyncToLocal: %v", err)
	}

	// The synced tree is handed to the indexer as-is: the walker sees the
	// sources and never descends into the clone's .git.
	reg := langs.DefaultRegistry()
	if _, err := reg.Activate(context.Background(), got); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	files, err := index.SupportedFiles(context.Background(), got, reg)
	if err != nil {
		t.Fatalf("SupportedFiles: %v", err)
	}
	found := false
	for _, f := range files {
		if strings.HasPrefix(f, ".git/") {
			t.Fatalf("walker returned the git metadata %q", f)
		}
		if f == "code.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("walker files = %v, want code.go", files)
	}
}

// CloneRepo is the exported runner the setup pipeline uses: it clones into the
// caller's directory (no staging layout) and checks the ref out.
func TestCloneRepoIntoCallerDirectory(t *testing.T) {
	hermeticGit(t)
	origin := newOriginRepo(t)
	dest := filepath.Join(t.TempDir(), "clone-target")
	progress := &recorder{}

	if err := CloneRepo(context.Background(), origin, "", dest, "main", progress); err != nil {
		t.Fatalf("CloneRepo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		t.Fatalf("no clone at %s: %v", dest, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "code.go")); err != nil {
		t.Fatalf("worktree not checked out: %v", err)
	}
}

// FetchAndCheckout is the exported refresh path for an existing clone: a new
// commit on the origin must land in the destination.
func TestFetchAndCheckoutAdvancesExistingClone(t *testing.T) {
	hermeticGit(t)
	origin := newOriginRepo(t)
	dest := filepath.Join(t.TempDir(), "clone-target")
	if err := CloneRepo(context.Background(), origin, "", dest, "main", &recorder{}); err != nil {
		t.Fatalf("CloneRepo: %v", err)
	}

	commitFile(t, origin, "second.go", "package fixture\n")
	if err := FetchAndCheckout(context.Background(), dest, "", "main", &recorder{}); err != nil {
		t.Fatalf("FetchAndCheckout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "second.go")); err != nil {
		t.Fatalf("the fetched commit did not land: %v", err)
	}
}

// A tag ref: the shallow branch clone serves it directly against a local
// remote, and the checkout must still land the tagged worktree. (The
// full-clone fallback arm is covered by the pre-existing SyncToLocal test on
// a bare-SHA ref.)
func TestCloneRepoChecksOutTagRef(t *testing.T) {
	hermeticGit(t)
	origin := newOriginRepo(t)
	gitOut(t, origin, "tag", "v1.0.0")
	dest := filepath.Join(t.TempDir(), "tag-clone")

	if err := CloneRepo(context.Background(), origin, "", dest, "v1.0.0", &recorder{}); err != nil {
		t.Fatalf("CloneRepo(tag): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "code.go")); err != nil {
		t.Fatalf("tag worktree not checked out: %v", err)
	}
}

// A checkout written by an older build stores the token in remote.origin.url.
// scrubOriginURL rewrites it to the clean URL without touching the network.
func TestScrubOriginURLRemovesStoredCredential(t *testing.T) {
	hermeticGit(t)
	dir := t.TempDir()
	gitOut(t, dir, "init", "-q")
	gitOut(t, dir, "remote", "add", "origin", "https://oauth2:FAKETOKEN@example.invalid/x.git")

	got, err := scrubOriginURL(context.Background(), dir, "FAKETOKEN")
	if err != nil {
		t.Fatalf("scrubOriginURL: %v", err)
	}
	const clean = "https://example.invalid/x.git"
	if got != clean {
		t.Fatalf("scrubOriginURL = %q, want %q", got, clean)
	}
	if stored := gitOut(t, dir, "config", "--get", "remote.origin.url"); stored != clean {
		t.Fatalf("stored origin = %q, want %q", stored, clean)
	}
	assertCleanGitConfig(t, dir)
}

// Remotes without an https credential are not rewritten.
func TestScrubOriginURLLeavesOtherRemotesAlone(t *testing.T) {
	for _, remote := range []string{"ssh://git@example.invalid/x.git", "git@example.invalid:x.git", "file:///srv/git/x"} {
		t.Run(remote, func(t *testing.T) {
			hermeticGit(t)
			dir := t.TempDir()
			gitOut(t, dir, "init", "-q")
			gitOut(t, dir, "remote", "add", "origin", remote)

			got, err := scrubOriginURL(context.Background(), dir, "")
			if err != nil {
				t.Fatalf("scrubOriginURL: %v", err)
			}
			if got != remote || gitOut(t, dir, "config", "--get", "remote.origin.url") != remote {
				t.Fatalf("scrubOriginURL(%q) = %q, want the remote unchanged", remote, got)
			}
		})
	}
}

// An existing checkout whose origin still holds the credential is cleaned
// before its fetch, so the fetch uses the clean remote and the token leaves
// .git/config.
func TestFetchAndCheckoutCleansLegacyCheckoutBeforeFetching(t *testing.T) {
	hermeticGit(t)
	origin := newOriginRepo(t)
	const remote = "https://example.invalid/repo.git"
	routeHTTPSTo(t, remote, origin)
	dest := filepath.Join(t.TempDir(), "clone")
	if err := CloneRepo(context.Background(), remote, "FAKETOKEN", dest, "main", &recorder{}); err != nil {
		t.Fatalf("CloneRepo: %v", err)
	}
	gitOut(t, dest, "remote", "set-url", "origin", "https://oauth2:FAKETOKEN@example.invalid/repo.git")

	commitFile(t, origin, "second.go", "package fixture\n")
	if err := FetchAndCheckout(context.Background(), dest, "FAKETOKEN", "main", &recorder{}); err != nil {
		t.Fatalf("FetchAndCheckout on a legacy checkout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "second.go")); err != nil {
		t.Fatalf("the fetched commit did not land: %v", err)
	}
	if stored := gitOut(t, dest, "config", "--get", "remote.origin.url"); stored != remote {
		t.Fatalf("stored origin = %q, want %q", stored, remote)
	}
	assertCleanGitConfig(t, dest)
}

// Git echoes what it was given. Every diagnostic built from its stderr must
// lose the token and its Basic form.
func TestGitDiagnosticsRedactTheCredential(t *testing.T) {
	stderr := "fatal: rejected Authorization: Basic " + fakeBasicB64 + " FAKETOKEN\n"
	cause := errors.New("exit status 128")
	for _, msg := range []string{
		gitError("git fetch failed", stderr, cause, "FAKETOKEN").Error(),
		firstLine(stderr, cause, "FAKETOKEN"),
	} {
		if strings.Contains(msg, "FAKETOKEN") || strings.Contains(msg, fakeBasicB64) {
			t.Fatalf("diagnostic carries the credential: %q", msg)
		}
		if !strings.Contains(msg, "[REDACTED]") {
			t.Fatalf("diagnostic was not redacted: %q", msg)
		}
	}
}

// With every git call failing and echoing the credential, the clone error and
// every progress line stay free of the token and its Basic form.
func TestCloneFailuresNeverLeakTheCredential(t *testing.T) {
	fakeGit(t)
	t.Setenv("FAKE_GIT_FAIL", "1")
	t.Setenv("FAKE_GIT_SECRET", "FAKETOKEN")
	progress := &recorder{}
	dest := filepath.Join(t.TempDir(), "clone")

	err := CloneRepo(context.Background(), "https://example.invalid/x.git", "FAKETOKEN", dest, "main", progress)
	if err == nil {
		t.Fatal("CloneRepo against a failing git succeeded")
	}
	all := append([]string{err.Error()}, progress.messages...)
	for _, msg := range all {
		if strings.Contains(msg, "FAKETOKEN") || strings.Contains(msg, fakeBasicB64) {
			t.Fatalf("message carries the credential: %q", msg)
		}
	}
	if !progress.contains("[REDACTED]") {
		t.Fatalf("the shallow-clone failure was not reported; progress = %q", progress.messages)
	}
}
