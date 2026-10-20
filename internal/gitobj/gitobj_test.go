package gitobj

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/FreePeak/LeanKG/internal/gitobj/gittest"
)

// newFixtureRepo builds the repository the tests read: committed regular files,
// a symlink, a space-named file, and a nested repo recorded as a gitlink. Then it
// makes uncommitted edits that HEAD reads must ignore.
func newFixtureRepo(t *testing.T) string {
	t.Helper()
	gittest.SkipWithoutGit(t)
	root := t.TempDir()
	gittest.Git(t, root, "init", "-q")
	gittest.WriteFile(t, root, "a.go", "package a\n")
	gittest.WriteFile(t, root, "dir/b.py", "print('b')\n")
	gittest.WriteFile(t, root, "sp ace.go", "package sp\n")
	gittest.WriteFile(t, root, ".gitignore", "ignored.go\n")
	if err := os.Symlink("a.go", filepath.Join(root, "link.go")); err != nil {
		t.Fatal(err)
	}

	nested := filepath.Join(root, "nested")
	gittest.Git(t, root, "init", "-q", "nested")
	gittest.WriteFile(t, nested, "n.go", "package n\n")
	gittest.Git(t, nested, "add", "n.go")
	gittest.Git(t, nested, "commit", "-q", "-m", "nested")

	gittest.Git(t, root, "add", ".gitignore", "a.go", "dir/b.py", "sp ace.go", "link.go", "nested")
	gittest.Git(t, root, "commit", "-q", "-m", "fixture")

	gittest.WriteFile(t, root, "a.go", "package a // dirty\n")
	gittest.WriteFile(t, root, "untracked.go", "package u\n")
	gittest.WriteFile(t, root, "ignored.go", "package i\n")
	return root
}

func TestListHEADCommittedRegularFilesOnly(t *testing.T) {
	root := newFixtureRepo(t)

	blobs, err := ListHEAD(context.Background(), root)
	if err != nil {
		t.Fatalf("ListHEAD: %v", err)
	}

	var paths []string
	byPath := map[string]Blob{}
	for _, b := range blobs {
		paths = append(paths, b.Path)
		byPath[b.Path] = b
	}
	slices.Sort(paths)
	want := []string{".gitignore", "a.go", "dir/b.py", "sp ace.go"}
	if !slices.Equal(paths, want) {
		t.Fatalf("paths = %q, want %q", paths, want)
	}

	a := byPath["a.go"]
	if wantSize := int64(len("package a\n")); a.Size != wantSize {
		t.Fatalf("a.go Size = %d, want %d", a.Size, wantSize)
	}
	if wantOID := gittest.Git(t, root, "rev-parse", "HEAD:a.go"); a.OID != wantOID {
		t.Fatalf("a.go OID = %s, want %s", a.OID, wantOID)
	}
}

func TestReaderReadsCommittedContent(t *testing.T) {
	root := newFixtureRepo(t)
	aOID := gittest.Git(t, root, "rev-parse", "HEAD:a.go")
	bOID := gittest.Git(t, root, "rev-parse", "HEAD:dir/b.py")

	r, err := NewReader(context.Background(), root)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	got, err := r.Read(aOID)
	if err != nil {
		t.Fatalf("Read(a.go): %v", err)
	}
	if string(got) != "package a\n" {
		t.Fatalf("Read(a.go) = %q, want the committed %q (not the dirty working copy)", got, "package a\n")
	}

	got, err = r.Read(bOID)
	if err != nil {
		t.Fatalf("Read(dir/b.py) after a.go: %v", err)
	}
	if want := "print('b')\n"; string(got) != want {
		t.Fatalf("Read(dir/b.py) = %q, want %q", got, want)
	}

	if _, err := r.Read("0000000000000000000000000000000000000000"); err == nil {
		t.Fatal("Read(missing oid) returned no error")
	}

	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestListHEADUnbornHEAD(t *testing.T) {
	gittest.SkipWithoutGit(t)
	root := t.TempDir()
	gittest.Git(t, root, "init", "-q")
	gittest.WriteFile(t, root, "f.go", "package f\n")

	_, err := ListHEAD(context.Background(), root)
	if !errors.Is(err, ErrNoHEAD) {
		t.Fatalf("ListHEAD on unborn HEAD: err = %v, want ErrNoHEAD", err)
	}
}

func TestIsRepoRoot(t *testing.T) {
	root := newFixtureRepo(t)
	ctx := context.Background()

	if !IsRepoRoot(ctx, root) {
		t.Errorf("IsRepoRoot(repo root) = false, want true")
	}
	if IsRepoRoot(ctx, filepath.Join(root, "dir")) {
		t.Errorf("IsRepoRoot(subdirectory of repo) = true, want false")
	}
	if IsRepoRoot(ctx, t.TempDir()) {
		t.Errorf("IsRepoRoot(non-git directory) = true, want false")
	}
	// The same root reached through a symlink must still match. On macOS
	// TempDir sits under /var, a symlink to /private/var.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if !IsRepoRoot(ctx, link) {
		t.Errorf("IsRepoRoot(symlink to repo root) = false, want true")
	}
}

func TestIsRepoRootRelativeDir(t *testing.T) {
	root := newFixtureRepo(t)
	t.Chdir(root)

	if !IsRepoRoot(context.Background(), ".") {
		t.Errorf("IsRepoRoot(\".\") in repo root = false, want true")
	}
}

// TestCommittedGitDir pins the git dir a watcher reads HEAD moves from: a
// repository root with a commit reports its own git dir, an unborn repository
// reports none, and a linked worktree (its .git is a file) reports its own git
// dir under the common repository's worktrees/.
func TestCommittedGitDir(t *testing.T) {
	gittest.SkipWithoutGit(t)
	ctx := context.Background()

	t.Run("repo root with commit", func(t *testing.T) {
		root := t.TempDir()
		gittest.Git(t, root, "init", "-q")
		gittest.WriteFile(t, root, "a.go", "package a\n")
		gittest.Git(t, root, "add", "a.go")
		gittest.Git(t, root, "commit", "-q", "-m", "init")

		dir, ok, err := CommittedGitDir(ctx, root)
		if err != nil || !ok {
			t.Fatalf("CommittedGitDir = (%q, %v, %v), want ok", dir, ok, err)
		}
		if want := gittest.Git(t, root, "rev-parse", "--absolute-git-dir"); dir != want {
			t.Fatalf("dir = %q, want %q", dir, want)
		}
	})

	t.Run("unborn repository", func(t *testing.T) {
		root := t.TempDir()
		gittest.Git(t, root, "init", "-q")

		dir, ok, err := CommittedGitDir(ctx, root)
		if err != nil || ok {
			t.Fatalf("CommittedGitDir = (%q, %v, %v), want not ok and no error", dir, ok, err)
		}
	})

	t.Run("linked worktree", func(t *testing.T) {
		root := t.TempDir()
		gittest.Git(t, root, "init", "-q")
		gittest.WriteFile(t, root, "a.go", "package a\n")
		gittest.Git(t, root, "add", "a.go")
		gittest.Git(t, root, "commit", "-q", "-m", "init")
		wt := filepath.Join(t.TempDir(), "wt")
		gittest.Git(t, root, "worktree", "add", "-q", wt)

		dir, ok, err := CommittedGitDir(ctx, wt)
		if err != nil || !ok {
			t.Fatalf("CommittedGitDir = (%q, %v, %v), want ok", dir, ok, err)
		}
		if want := gittest.Git(t, wt, "rev-parse", "--absolute-git-dir"); dir != want {
			t.Fatalf("dir = %q, want the worktree's own git dir %q", dir, want)
		}
		if filepath.Base(filepath.Dir(dir)) != "worktrees" || filepath.Base(dir) != "wt" {
			t.Fatalf("dir = %q, want .../worktrees/wt", dir)
		}
	})
}
