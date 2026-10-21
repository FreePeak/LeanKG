package maintain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/gitobj/gittest"
	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestFleetPassReconcilesStoreWithHEAD is the regression for the store that
// never reconciled with the tree, seen from the place that owns the schedule:
// the hourly maintenance pass.
//
// The fleet pass swept orphans and vacuumed, but nothing compared the store
// against the repository's HEAD, so a store kept every element of a file
// HEAD no longer contained (1,397 paths and 13,006 elements on one measured
// project) until somebody re-indexed it by hand.
//
// This test drives the fleet pass itself (RunFleet), not the reconcile
// helper, so it covers the wiring: a tree without the reconcile keeps the
// stale content, and the test fails.
func TestFleetPassReconcilesStoreWithHEAD(t *testing.T) {
	gittest.SkipWithoutGit(t)
	dir := t.TempDir()
	gittest.Git(t, dir, "init")
	gittest.WriteFile(t, dir, "main.go", "package main\n\nfunc Main() {}\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-m", "first")

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
	if !hasElement(st, "main.go") {
		t.Fatal("fixture produced no elements for main.go")
	}

	// The tree moves: the file is deleted and the deletion committed.
	if err := os.Remove(filepath.Join(dir, "main.go")); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, dir, "add", "-A")
	gittest.Git(t, dir, "commit", "-m", "drop main.go")

	// One fleet pass: the same schedule the server runs hourly.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	RunFleet(ctx, func() []Target {
		return []Target{{Store: st, Label: "test"}}
	}, Options{}, time.Hour, 0)

	// The pass runs in the background; wait for it to have done its work.
	deadline := time.Now().Add(20 * time.Second)
	for hasElement(st, "main.go") {
		if time.Now().After(deadline) {
			t.Fatal("the fleet pass left the elements of a path HEAD no longer contains")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// hasElement reports whether the store holds an element of the given file.
func hasElement(st store.Backend, path string) bool {
	els, err := st.Elements()
	if err != nil {
		return false
	}
	for _, e := range els {
		if e.FilePath == path {
			return true
		}
	}
	return false
}
