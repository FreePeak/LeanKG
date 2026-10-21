package maintain

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/gitobj/gittest"
	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/store"
)

// gitRepo builds a git repository with one committed Go file and returns its
// directory.
func gitRepo(t *testing.T) string {
	t.Helper()
	gittest.SkipWithoutGit(t)
	dir := t.TempDir()
	gittest.Git(t, dir, "init")
	gittest.WriteFile(t, dir, "main.go", "package main\n\nfunc Main() {}\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-m", "first")
	return dir
}

// openStore opens a migrated store in a scratch directory (NOT inside the
// project: a store under the project root would be walked by the index).
func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

// elementsOf returns the elements whose file path is path.
func elementsOf(t *testing.T, st store.Backend, path string) []store.Element {
	t.Helper()
	all, err := st.Elements()
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Element
	for _, e := range all {
		if e.FilePath == path {
			out = append(out, e)
		}
	}
	return out
}

// TestReconcileHeadRemovesPathDeletedFromHead is the regression for the store
// that never reconciled with the tree: a file deleted from HEAD left its
// elements, its relationships and its vectors in the store forever.
func TestReconcileHeadRemovesPathDeletedFromHead(t *testing.T) {
	gittest.SkipWithoutGit(t)
	dir := gitRepo(t)
	st := openStore(t)

	if _, err := index.IndexDir(context.Background(), st, dir); err != nil {
		t.Fatal(err)
	}
	if els := elementsOf(t, st, "main.go"); len(els) == 0 {
		t.Fatal("index produced no elements for main.go; the fixture is wrong")
	}
	// A vector for one of the file's elements: the reconcile must take it
	// with the element, or the L3 arm answers with vectors whose content no
	// longer exists.
	els := elementsOf(t, st, "main.go")
	if err := st.WriteStamp(store.ModelStamp{
		ModelID: "m-1", Revision: "r1", Dimensions: 4, Distance: "cosine", Provider: "test",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertVectors("m-1", []store.VectorRow{
		{QualifiedName: els[0].QualifiedName, Vec: []float32{1, 0, 0, 0}},
	}); err != nil {
		t.Fatal(err)
	}

	// Delete the file and commit the deletion: HEAD no longer contains it.
	if err := os.Remove(filepath.Join(dir, "main.go")); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, dir, "add", "-A")
	gittest.Git(t, dir, "commit", "-m", "drop main.go")

	res, err := ReconcileHead(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if res.DeletedFiles != 1 {
		t.Fatalf("DeletedFiles = %d; want 1 (the path HEAD dropped)", res.DeletedFiles)
	}
	if els := elementsOf(t, st, "main.go"); len(els) != 0 {
		t.Fatalf("elements of the deleted path remain: %d", len(els))
	}
	files, err := st.Files()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Path == "main.go" {
			t.Fatal("the deleted path still has a file record")
		}
	}
	if n, err := st.VectorCount("m-1"); err != nil {
		t.Fatal(err)
	} else if n != 0 {
		t.Fatalf("VectorCount = %d; want 0 (the deleted path's vectors are orphans now)", n)
	}
}

// TestReconcileHeadKeepsPathStillInHead is the safety case: a path HEAD
// still contains is never deleted, whatever the store holds for it.
func TestReconcileHeadKeepsPathStillInHead(t *testing.T) {
	gittest.SkipWithoutGit(t)
	dir := gitRepo(t)
	st := openStore(t)

	if _, err := index.IndexDir(context.Background(), st, dir); err != nil {
		t.Fatal(err)
	}
	// A second commit that does not touch main.go.
	gittest.WriteFile(t, dir, "other.go", "package main\n\nfunc Other() {}\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-m", "add other")

	res, err := ReconcileHead(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if res.DeletedFiles != 0 {
		t.Fatalf("DeletedFiles = %d; want 0 (HEAD still contains main.go)", res.DeletedFiles)
	}
	if els := elementsOf(t, st, "main.go"); len(els) == 0 {
		t.Fatal("the elements of a path HEAD still contains were deleted")
	}
}

// TestReconcileHeadLeavesFilesystemStoreAlone is the safety case that
// matters most: a store that was NOT built from git objects must never be
// pruned against a git tree. Its content hashes are SHA-256 digests, not
// "git:<oid>", so nothing about it can be compared with HEAD.
func TestReconcileHeadLeavesFilesystemStoreAlone(t *testing.T) {
	gittest.SkipWithoutGit(t)
	st := openStore(t)

	// Index a plain directory (not a git repo): the store's records carry
	// SHA-256 content hashes.
	plain := t.TempDir()
	if err := os.WriteFile(filepath.Join(plain, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := index.IndexDir(context.Background(), st, plain); err != nil {
		t.Fatal(err)
	}
	if els := elementsOf(t, st, "a.go"); len(els) == 0 {
		t.Fatal("fixture produced no elements")
	}

	// A git repository whose HEAD does not contain a.go: the reconcile must
	// still refuse to delete it, because the store was not built from git.
	other := t.TempDir()
	gittest.Git(t, other, "init")
	gittest.WriteFile(t, other, "b.go", "package b\n\nfunc B() {}\n")
	gittest.Git(t, other, "add", ".")
	gittest.Git(t, other, "commit", "-m", "first")

	// Point the store at the git repo WITHOUT re-indexing: the store's own
	// records stay filesystem-scanned (SHA-256 hashes).
	if err := st.KVSet(store.IndexKVNamespace, store.RootKVKey, other); err != nil {
		t.Fatal(err)
	}

	res, err := ReconcileHead(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if res.DeletedFiles != 0 {
		t.Fatalf("DeletedFiles = %d; want 0 (a filesystem-scanned store must never be pruned)", res.DeletedFiles)
	}
	if els := elementsOf(t, st, "a.go"); len(els) == 0 {
		t.Fatal("a filesystem-scanned store lost its elements to the reconcile")
	}
}

// TestReconcileHeadNoRepositoryDeletesNothing: a project that is not a git
// repository at all is left untouched.
func TestReconcileHeadNoRepositoryDeletesNothing(t *testing.T) {
	plain := t.TempDir()
	if err := os.WriteFile(filepath.Join(plain, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := openStore(t)
	if _, err := index.IndexDir(context.Background(), st, plain); err != nil {
		t.Fatal(err)
	}
	res, err := ReconcileHead(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if res.DeletedFiles != 0 {
		t.Fatalf("DeletedFiles = %d; want 0 (no repository, no HEAD)", res.DeletedFiles)
	}
	if els := elementsOf(t, st, "a.go"); len(els) == 0 {
		t.Fatal("a non-git project lost its elements")
	}
}

// TestReconcileHeadUnbornHEADDeletesNothing: a repository with no commit yet
// has no HEAD to compare against, so nothing may be deleted.
func TestReconcileHeadUnbornHEADDeletesNothing(t *testing.T) {
	gittest.SkipWithoutGit(t)
	dir := t.TempDir()
	gittest.Git(t, dir, "init")
	gittest.WriteFile(t, dir, "main.go", "package main\n\nfunc Main() {}\n")
	st := openStore(t)
	if _, err := index.IndexDir(context.Background(), st, dir); err != nil {
		t.Fatal(err)
	}
	res, err := ReconcileHead(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if res.DeletedFiles != 0 {
		t.Fatalf("DeletedFiles = %d; want 0 (unborn HEAD)", res.DeletedFiles)
	}
}
