package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/gitobj/gittest"
	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/store"
)

// gitRepoWithFile builds a git repository with one committed Go file and
// returns its directory.
func gitRepoWithFile(t *testing.T) string {
	t.Helper()
	gittest.SkipWithoutGit(t)
	dir := t.TempDir()
	gittest.Git(t, dir, "init")
	gittest.WriteFile(t, dir, "main.go", "package main\n\nfunc Main() {}\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-m", "first")
	return dir
}

// headSyncOf reads the head_sync field from a status payload.
func headSyncOf(t *testing.T, out map[string]any) string {
	t.Helper()
	v, ok := out["head_sync"]
	if !ok {
		t.Fatalf("status payload has no head_sync field; keys: %v", keysOf(out))
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("head_sync = %v (%T); want a string", v, v)
	}
	return s
}

// TestStatusHeadSyncTellsTheTreeMoved is the regression for the freshness
// blind spot: `freshness` compares two counters inside the SAME database, so
// it can never notice that the repository moved on — every store reported
// "fresh", including one 5.3 days and 59 % behind HEAD.
//
// head_sync answers the question freshness cannot: does the store still
// mirror the tree it was indexed from?
func TestStatusHeadSyncTellsTheTreeMoved(t *testing.T) {
	gittest.SkipWithoutGit(t)
	dir := gitRepoWithFile(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := index.IndexDir(context.Background(), st, dir); err != nil {
		t.Fatal(err)
	}
	e := New(st, nil, nil)

	// The store was indexed from the current HEAD: the tree has not moved.
	out, err := e.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := headSyncOf(t, out); got != store.HeadSyncMatch {
		t.Fatalf("head_sync = %q; want %q right after indexing", got, store.HeadSyncMatch)
	}

	// The tree moves: a commit that deletes the indexed file. The store
	// still holds the old content, and the status must say so.
	if err := os.Remove(filepath.Join(dir, "main.go")); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, dir, "add", "-A")
	gittest.Git(t, dir, "commit", "-m", "drop main.go")

	out, err = e.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := headSyncOf(t, out); got != store.HeadSyncStale {
		t.Fatalf("head_sync = %q; want %q after the tree moved", got, store.HeadSyncStale)
	}
	// The old freshness label is NOT touched by this: it keeps its meaning
	// (two counters in one database), and head_sync is the new signal.
	if _, ok := out["freshness"]; !ok {
		t.Fatal("status payload lost its freshness field")
	}

	// Re-indexing brings the store back to the current HEAD.
	if _, err := index.IndexDir(context.Background(), st, dir); err != nil {
		t.Fatal(err)
	}
	out, err = e.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := headSyncOf(t, out); got != store.HeadSyncMatch {
		t.Fatalf("head_sync = %q; want %q after re-indexing", got, store.HeadSyncMatch)
	}
}

// TestStatusHeadSyncUnknownWithoutGit: a store that was not built from a git
// tree has no head to compare, so the signal is "unknown" — never a
// freshness claim that was not proven.
func TestStatusHeadSyncUnknownWithoutGit(t *testing.T) {
	plain := t.TempDir()
	if err := os.WriteFile(filepath.Join(plain, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := index.IndexDir(context.Background(), st, plain); err != nil {
		t.Fatal(err)
	}
	out, err := New(st, nil, nil).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := headSyncOf(t, out); got != store.HeadSyncUnknown {
		t.Fatalf("head_sync = %q; want %q for a store that was not git-indexed", got, store.HeadSyncUnknown)
	}
}
