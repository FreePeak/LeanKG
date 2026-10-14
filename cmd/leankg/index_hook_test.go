package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/flock"
	"github.com/FreePeak/LeanKG/internal/gitobj/gittest"
)

// hookEnv pins the environment a hook-mode index run sees: the big-repo limit
// under test. The embed provider is set package-wide in TestMain.
func hookEnv(t *testing.T, maxFiles string) {
	t.Helper()
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")
	t.Setenv("LEANKG_HOOK_MAX_FILES", maxFiles)
}

// writeThreeGoFiles puts three one-function Go files at the root of dir.
func writeThreeGoFiles(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		gittest.WriteFile(t, dir, name, "package p\n\nfunc "+strings.ToUpper(name[:1])+"() int { return 1 }\n")
	}
}

// hookRepo is a fresh git repository whose HEAD commits three Go files.
func hookRepo(t *testing.T) string {
	t.Helper()
	gittest.SkipWithoutGit(t)
	dir := t.TempDir()
	gittest.Git(t, dir, "init", "-q")
	writeThreeGoFiles(t, dir)
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// TestIndexHookSkipsBigGitRepo: a git repo whose HEAD holds more indexable files
// than LEANKG_HOOK_MAX_FILES is skipped by the SessionStart hook. The skip exits
// 0, names the manual command, and happens before any .leankg is created.
func TestIndexHookSkipsBigGitRepo(t *testing.T) {
	hookEnv(t, "2")
	repo := hookRepo(t)

	stdout, stderr, code := runCLIIn(t, repo, "index", "--hook", repo)
	if code != 0 {
		t.Fatalf("hook skip must exit 0, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	for _, want := range []string{"3 indexable files", "run it manually: leankg index"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".leankg")); !os.IsNotExist(err) {
		t.Fatalf("skipped hook must create no .leankg (stat err: %v)", err)
	}
}

// TestIndexHookIndexesSmallGitRepo: under the limit the hook indexes the repo
// from HEAD as a manual run would.
func TestIndexHookIndexesSmallGitRepo(t *testing.T) {
	hookEnv(t, "10")
	repo := hookRepo(t)

	stdout, stderr, code := runCLIIn(t, repo, "index", "--hook", repo)
	if code != 0 {
		t.Fatalf("hook index exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "files=3") {
		t.Fatalf("hook must index the 3 committed files:\n%s", stdout)
	}
	requireFile(t, filepath.Join(repo, ".leankg", "leankg.db"))
}

// TestIndexHookScansNonGitDirAsNormal: the gate applies only to a git repo root
// with a commit. A plain directory is scanned from disk whatever the limit.
func TestIndexHookScansNonGitDirAsNormal(t *testing.T) {
	hookEnv(t, "2")
	dir := t.TempDir()
	writeThreeGoFiles(t, dir)

	stdout, stderr, code := runCLIIn(t, dir, "index", "--hook", dir)
	if code != 0 {
		t.Fatalf("hook index of a plain dir exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "files=3") {
		t.Fatalf("plain dir must be scanned as normal (files=3):\n%s", stdout)
	}
}

// TestIndexManualIgnoresHookLimit: the limit gates the hook only. A manual
// `leankg index` of the same over-limit repo indexes it.
func TestIndexManualIgnoresHookLimit(t *testing.T) {
	hookEnv(t, "2")
	repo := hookRepo(t)

	stdout, stderr, code := runCLIIn(t, repo, "index", repo)
	if code != 0 {
		t.Fatalf("manual index exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "files=3") {
		t.Fatalf("manual index must ignore LEANKG_HOOK_MAX_FILES (files=3):\n%s", stdout)
	}
}

// TestIndexHookSkipsWhenIndexRunning: while another index holds the store's
// lock, the hook exits 0 at once and indexes nothing, so the session start
// never queues behind a running index.
func TestIndexHookSkipsWhenIndexRunning(t *testing.T) {
	hookEnv(t, "10")
	repo := hookRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".leankg"), 0o755); err != nil {
		t.Fatal(err)
	}
	unlock, err := flock.TryLock(filepath.Join(repo, ".leankg", "index.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	stdout, stderr, code := runCLIIn(t, repo, "index", "--hook", repo)
	if code != 0 {
		t.Fatalf("hook skip must exit 0, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "another index of this store is running") {
		t.Fatalf("stdout missing the busy-skip line:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(repo, ".leankg", "leankg.db")); !os.IsNotExist(err) {
		t.Fatalf("busy hook must not open or create the store (stat err: %v)", err)
	}
}
