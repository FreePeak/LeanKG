package sources

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/index"
	"github.com/FreePeak/LeanKG/go/internal/langs"
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

func TestInjectAuthOnlyTouchesHTTPS(t *testing.T) {
	tests := []struct {
		url, auth, want string
	}{
		{"https://github.com/user/repo.git", "ghp_token123", "https://oauth2:ghp_token123@github.com/user/repo.git"},
		{"https://github.com/user/repo.git", "", "https://github.com/user/repo.git"},
		{"ssh://git@github.com/user/repo.git", "token", "ssh://git@github.com/user/repo.git"},
		{"git@github.com:user/repo.git", "token", "git@github.com:user/repo.git"},
	}
	for _, tc := range tests {
		if got := injectAuth(tc.url, tc.auth); got != tc.want {
			t.Errorf("injectAuth(%q, %q) = %q, want %q", tc.url, tc.auth, got, tc.want)
		}
	}
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
	if _, err := reg.Activate(got); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	files, err := index.SupportedFiles(got, reg)
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
