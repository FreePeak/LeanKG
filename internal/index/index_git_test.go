package index

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/gitobj/gittest"
	"github.com/FreePeak/LeanKG/internal/langs"
	"github.com/FreePeak/LeanKG/internal/store"
)

// gitIndexStore opens a migrated store outside any repository under test.
func gitIndexStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

// gitIndexRun runs one IndexDirWith pass over dir and fails the test on error.
func gitIndexRun(t *testing.T, st *store.Store, dir string) Result {
	t.Helper()
	res, err := IndexDirWith(context.Background(), st, dir, nil)
	if err != nil {
		t.Fatalf("IndexDirWith: %v", err)
	}
	return res
}

// gitIndexHas reports whether the store holds exactly one element with qn.
func gitIndexHas(t *testing.T, st *store.Store, qn string) bool {
	t.Helper()
	els, err := st.FindExact(qn)
	if err != nil {
		t.Fatal(err)
	}
	return len(els) == 1
}

// gitIndexFixture commits a.go, b.go, a .gitignore, and a nested repo recorded
// as a gitlink, then returns the root. Callers add the working-tree edits they
// need.
func gitIndexFixture(t *testing.T) string {
	t.Helper()
	gittest.SkipWithoutGit(t)
	root := t.TempDir()
	gittest.Git(t, root, "init", "-q")
	gittest.WriteFile(t, root, ".gitignore", "ignored.go\n")
	gittest.WriteFile(t, root, "a.go", "package a\n\nfunc Committed() {}\n")
	gittest.WriteFile(t, root, "b.go", "package b\n\nfunc Bee() {}\n")

	gittest.Git(t, root, "init", "-q", "nested")
	gittest.WriteFile(t, root, "nested/n.go", "package n\n\nfunc Nested() {}\n")
	gittest.Git(t, filepath.Join(root, "nested"), "add", "n.go")
	gittest.Git(t, filepath.Join(root, "nested"), "commit", "-q", "-m", "nested")

	gittest.Git(t, root, "add", ".gitignore", "a.go", "b.go", "nested")
	gittest.Git(t, root, "commit", "-q", "-m", "fixture")
	return root
}

// TestIndexGitRootIndexesHEADOnly: a repo root is indexed from HEAD, so
// uncommitted edits, untracked files, ignored files and gitlinked nested repos
// stay out of the store.
func TestIndexGitRootIndexesHEADOnly(t *testing.T) {
	root := gitIndexFixture(t)
	gittest.WriteFile(t, root, "a.go", "package a\n\nfunc Dirty() {}\n")
	gittest.WriteFile(t, root, "u.go", "package a\n\nfunc Untracked() {}\n")
	gittest.WriteFile(t, root, "ignored.go", "package a\n\nfunc Ignored() {}\n")
	st := gitIndexStore(t)

	gitIndexRun(t, st, root)

	if !gitIndexHas(t, st, "a.go::Committed") {
		t.Error("a.go::Committed missing: the committed content was not indexed")
	}
	for _, qn := range []string{"a.go::Dirty", "u.go::Untracked", "ignored.go::Ignored", "nested/n.go::Nested"} {
		if gitIndexHas(t, st, qn) {
			t.Errorf("%s indexed: the git root must index HEAD only", qn)
		}
	}
}

// TestIndexGitRootUnchangedRerunSkipsAll: a rerun over an unchanged HEAD skips
// every file by blob ID, even when the working copy of a committed file has
// been edited since the first run.
func TestIndexGitRootUnchangedRerunSkipsAll(t *testing.T) {
	root := gitIndexFixture(t)
	st := gitIndexStore(t)

	first := gitIndexRun(t, st, root)
	if first.Files != 2 {
		t.Fatalf("first run: Files = %d, want 2 (a.go, b.go)", first.Files)
	}

	gittest.WriteFile(t, root, "a.go", "package a\n\nfunc Dirty() {}\n")
	second := gitIndexRun(t, st, root)
	if second.Files != 0 || second.Skipped != first.Files {
		t.Fatalf("rerun: %+v, want Files=0 Skipped=%d", second, first.Files)
	}
	if !gitIndexHas(t, st, "a.go::Committed") {
		t.Error("a.go::Committed gone after rerun")
	}
}

// TestIndexGitRootNewCommitReextractsOnlyChanged: a new commit that edits one
// file re-extracts exactly that file. The other file has a dirty working copy
// that must not be read, because its blob ID is unchanged.
func TestIndexGitRootNewCommitReextractsOnlyChanged(t *testing.T) {
	root := gitIndexFixture(t)
	st := gitIndexStore(t)
	gitIndexRun(t, st, root)

	gittest.WriteFile(t, root, "a.go", "package a\n\nfunc Committed() {}\n\nfunc Added() {}\n")
	gittest.Git(t, root, "add", "a.go")
	gittest.Git(t, root, "commit", "-q", "-m", "edit a")
	gittest.WriteFile(t, root, "b.go", "package b\n\nfunc Bee() {}\n\nfunc Scratch() {}\n")

	res := gitIndexRun(t, st, root)
	if res.Files != 1 {
		t.Fatalf("after commit: Files = %d, want 1 (only a.go changed in HEAD)", res.Files)
	}
	if !gitIndexHas(t, st, "a.go::Added") {
		t.Error("a.go::Added missing after the commit")
	}
	if gitIndexHas(t, st, "b.go::Scratch") {
		t.Error("b.go::Scratch indexed: an unchanged blob must not be read from the working tree")
	}
}

// TestIndexGitRootRemovedInHEADIsDeleted: a file removed from HEAD is deleted
// from the store on the next run, and its elements go with it. The file stays
// on disk as untracked (git rm --cached), so only the HEAD source can see the
// removal.
func TestIndexGitRootRemovedInHEADIsDeleted(t *testing.T) {
	root := gitIndexFixture(t)
	st := gitIndexStore(t)
	gitIndexRun(t, st, root)
	if !gitIndexHas(t, st, "b.go::Bee") {
		t.Fatal("b.go::Bee not indexed on the first run")
	}

	gittest.Git(t, root, "rm", "-q", "--cached", "b.go")
	gittest.Git(t, root, "commit", "-q", "-m", "remove b")

	res := gitIndexRun(t, st, root)
	if res.DeletedFiles != 1 {
		t.Fatalf("DeletedFiles = %d, want 1", res.DeletedFiles)
	}
	if gitIndexHas(t, st, "b.go::Bee") {
		t.Error("b.go::Bee still indexed after b.go was removed from HEAD")
	}
}

// TestIndexGitRootAdoptsWalkStoreWithoutReextract: a store written by the walk
// (SHA-256 content hashes) adopts a git root that holds the same bytes without
// re-extracting: the content matches, so each record is re-keyed to its blob.
func TestIndexGitRootAdoptsWalkStoreWithoutReextract(t *testing.T) {
	gittest.SkipWithoutGit(t)
	dir := t.TempDir()
	gittest.WriteFile(t, dir, "a.go", "package a\n\nfunc A() {}\n")
	gittest.WriteFile(t, dir, "b.go", "package b\n\nfunc B() {}\n")
	st := gitIndexStore(t)

	if walked := gitIndexRun(t, st, dir); walked.Files != 2 {
		t.Fatalf("walk run: Files = %d, want 2", walked.Files)
	}

	gittest.Git(t, dir, "init", "-q")
	gittest.Git(t, dir, "add", "a.go", "b.go")
	gittest.Git(t, dir, "commit", "-q", "-m", "adopt")

	adopted := gitIndexRun(t, st, dir)
	if adopted.Files != 0 {
		t.Fatalf("adopting run: Files = %d, want 0 (identical bytes)", adopted.Files)
	}
	recs, err := st.Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("file records = %d, want 2", len(recs))
	}
	for _, rec := range recs {
		if !strings.HasPrefix(rec.ContentHash, "git:") {
			t.Errorf("%s ContentHash = %q, want a git: blob key", rec.Path, rec.ContentHash)
		}
	}
	if again := gitIndexRun(t, st, dir); again.Files != 0 || again.Skipped != 2 {
		t.Fatalf("third run: %+v, want Files=0 Skipped=2", again)
	}
}

// TestIndexUnbornRepoUsesWalk: a repository with no commit has no HEAD tree, so
// the indexer falls back to the filesystem walk.
func TestIndexUnbornRepoUsesWalk(t *testing.T) {
	gittest.SkipWithoutGit(t)
	dir := t.TempDir()
	gittest.Git(t, dir, "init", "-q")
	gittest.WriteFile(t, dir, "a.go", "package a\n\nfunc Working() {}\n")
	gittest.WriteFile(t, dir, "b.go", "package b\n\nfunc Other() {}\n")
	st := gitIndexStore(t)

	res := gitIndexRun(t, st, dir)
	if res.Files != 2 {
		t.Fatalf("Files = %d, want 2 from the working tree", res.Files)
	}
	if !gitIndexHas(t, st, "a.go::Working") {
		t.Error("a.go::Working missing: an unborn repo must be indexed from disk")
	}
}

// TestIndexRepoSubdirUsesWalk: indexing a subdirectory of a repository is not a
// repo root, so it reads the working tree, including uncommitted edits.
func TestIndexRepoSubdirUsesWalk(t *testing.T) {
	root := gitIndexFixture(t)
	gittest.WriteFile(t, root, "sub/s.go", "package s\n\nfunc Sub() {}\n")
	gittest.Git(t, root, "add", "sub/s.go")
	gittest.Git(t, root, "commit", "-q", "-m", "sub")
	gittest.WriteFile(t, root, "sub/s.go", "package s\n\nfunc SubEdited() {}\n")
	st := gitIndexStore(t)

	gitIndexRun(t, st, filepath.Join(root, "sub"))
	if !gitIndexHas(t, st, "s.go::SubEdited") {
		t.Error("s.go::SubEdited missing: a subdirectory of a repo must be walked from disk")
	}
}

// TestSupportedFilesGitRootListsHEAD: doctor's disk side of the freshness check
// lists what the git indexer takes, so the untracked u.go is not a supported
// file of a repository root with a commit.
func TestSupportedFilesGitRootListsHEAD(t *testing.T) {
	gittest.SkipWithoutGit(t)
	root := t.TempDir()
	gittest.Git(t, root, "init", "-q")
	gittest.WriteFile(t, root, "a.go", "package a\n")
	gittest.Git(t, root, "add", "a.go")
	gittest.Git(t, root, "commit", "-q", "-m", "init")
	gittest.WriteFile(t, root, "u.go", "package u\n")

	reg := langs.NewRegistry()
	if _, err := reg.Activate(root); err != nil {
		t.Fatal(err)
	}
	files, err := SupportedFiles(root, reg)
	if err != nil {
		t.Fatalf("SupportedFiles: %v", err)
	}
	if want := []string{"a.go"}; !slices.Equal(files, want) {
		t.Fatalf("SupportedFiles = %q, want %q (u.go is untracked)", files, want)
	}
}
