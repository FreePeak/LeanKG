package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// initRepoWithCommit makes dir a real repository with one commit at a chosen
// timestamp, so LastCommitTimeAt has something exact to read.
func initRepoWithCommit(t *testing.T, dir string, at time.Time) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "checkout", "-q", "-b", "main")
	mustFile(t, filepath.Join(dir, "file.txt"), "hello\n")
	gitRun(t, dir, "add", ".")
	stamp := fmt.Sprintf("%d +0000", at.Unix())
	cmd := exec.Command("git", "commit", "-q", "-m", "init")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}

func TestIsGitRepoAtAcceptsDirectoryAndGitfile(t *testing.T) {
	dir := t.TempDir()
	if IsGitRepoAt(dir) {
		t.Fatal("a bare dir is not a repo")
	}
	// `.git` as a directory (a normal clone).
	mustFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n")
	if !IsGitRepoAt(dir) {
		t.Fatal(".git directory not detected")
	}
	// `.git` as a file (worktree / submodule gitfile).
	other := t.TempDir()
	mustFile(t, filepath.Join(other, ".git"), "gitdir: /elsewhere\n")
	if !IsGitRepoAt(other) {
		t.Fatal(".git file not detected")
	}
}

func TestWorkspaceHasGitContextNestedRepos(t *testing.T) {
	root := t.TempDir()
	if (Workspace{Root: root}).HasGitContext() {
		t.Fatal("an empty dir has no git context")
	}
	mustFile(t, filepath.Join(root, "svc", "api", ".git", "HEAD"), "ref: refs/heads/main\n")
	if !(Workspace{Root: root}).HasGitContext() {
		t.Fatal("a nested repo must count as git context")
	}
	// An injected answer wins over the probe.
	no := false
	if (Workspace{Root: root, HasGitContextResult: &no}).HasGitContext() {
		t.Fatal("the injected answer must win")
	}
}

func TestDiscoverNestedGitReposBoundsAndSkips(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"platform-food/be-food-order",
		"platform-mail/be-mailer",
	} {
		mustFile(t, filepath.Join(root, rel, ".git", "HEAD"), "ref: refs/heads/main\n")
	}
	// Skipped dirs are not descended into.
	for _, rel := range []string{
		"node_modules/dep",
		"vendor/dep",
		"target/debug",
		"dist/pkg",
		"build/pkg",
		".worktrees/wt",
		".claude/session",
		"browser-data/x",
	} {
		mustFile(t, filepath.Join(root, rel, ".git", "HEAD"), "x")
	}
	// Level 4 of the walk (inside the bound: `depth >= max` prunes at 5).
	mustFile(t, filepath.Join(root, "a", "b", "c", "d", ".git", "HEAD"), "x")
	// One level past the bound.
	mustFile(t, filepath.Join(root, "x", "y", "z", "u", "v", ".git", "HEAD"), "x")

	got := DiscoverNestedGitRepos(root)
	want := []string{
		filepath.Join(root, "a", "b", "c", "d"),
		filepath.Join(root, "platform-food", "be-food-order"),
		filepath.Join(root, "platform-mail", "be-mailer"),
	}
	if len(got) != len(want) {
		t.Fatalf("DiscoverNestedGitRepos = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DiscoverNestedGitRepos = %v, want %v", got, want)
		}
	}

	// A root that IS a repo yields exactly itself (no descent).
	repoRoot := t.TempDir()
	mustFile(t, filepath.Join(repoRoot, ".git", "HEAD"), "ref: refs/heads/main\n")
	mustFile(t, filepath.Join(repoRoot, "inner", ".git", "HEAD"), "x")
	if got := DiscoverNestedGitRepos(repoRoot); len(got) != 1 || got[0] != repoRoot {
		t.Fatalf("root repo discovery = %v", got)
	}
}

func TestLastCommitTimeAtReadsHeadTimestamp(t *testing.T) {
	hermeticGit(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	initRepoWithCommit(t, dir, at)

	got, err := LastCommitTimeAt(dir)
	if err != nil {
		t.Fatalf("LastCommitTimeAt: %v", err)
	}
	if got != at.Unix() {
		t.Fatalf("LastCommitTimeAt = %d, want %d", got, at.Unix())
	}
	// A non-repo dir is an error, not a zero.
	if _, err := LastCommitTimeAt(t.TempDir()); err == nil {
		t.Fatal("a non-repo dir must error")
	}
}

func TestWorkspaceLastCommitTimeUsesNewestNestedRepo(t *testing.T) {
	hermeticGit(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root := t.TempDir()
	older := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	initRepoWithCommit(t, filepath.Join(root, "svc-a"), older)
	initRepoWithCommit(t, filepath.Join(root, "svc-b"), newer)

	got := Workspace{Root: root}.LastCommitTime()
	if got != newer.Unix() {
		t.Fatalf("LastCommitTime = %d, want the newest nested commit %d", got, newer.Unix())
	}

	// A repo root short-circuits to its own commit.
	got = Workspace{Root: filepath.Join(root, "svc-a")}.LastCommitTime()
	if got != older.Unix() {
		t.Fatalf("root-repo LastCommitTime = %d, want %d", got, older.Unix())
	}

	// No repos at all: unknown (0), never a fabricated time.
	if got := (Workspace{Root: t.TempDir()}).LastCommitTime(); got != 0 {
		t.Fatalf("no-git LastCommitTime = %d, want 0", got)
	}
	// An injected answer wins.
	fixed := int64(42)
	if got := (Workspace{Root: root, LastCommitResult: &fixed}).LastCommitTime(); got != 42 {
		t.Fatalf("injected LastCommitTime = %d", got)
	}
}

func TestResolveReposUsesWorkspaceDiscoveryWithNestedRepos(t *testing.T) {
	isolateEnv(t)
	root := t.TempDir()
	mustFile(t, filepath.Join(root, "be", "svc", ".git", "HEAD"), "ref: refs/heads/main\n")
	t.Setenv("LEANKG_WORKSPACE_DIR", root)
	logf, _ := collectLogs()

	specs := ResolveRepos(logf)
	if len(specs) != 1 {
		t.Fatalf("specs = %+v", specs)
	}
	// The workspace and nested discovery agree on the repo set.
	nested := DiscoverNestedGitRepos(root)
	if len(nested) != 1 || specs[0].Dest != nested[0] {
		t.Fatalf("resolve/dest mismatch: %+v vs %v", specs, nested)
	}
	if !strings.HasSuffix(specs[0].Dest, filepath.Join("be", "svc")) {
		t.Fatalf("dest = %q", specs[0].Dest)
	}
}
