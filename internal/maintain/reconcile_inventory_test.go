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

// TestReconcileKeepsTheStoreFresh is the regression for the false staleness
// the reconcile introduced.
//
// Freshness is DERIVED: the inventory's seq is compared with the write
// watermark, and a difference reads "possibly_stale". A delete advances that
// watermark (DeleteByFile and DeleteFileRecord both bump it), so a store that
// had just been reconciled — made to MATCH its tree — reported
// "possibly_stale", and the fleet doctor's own hint was "re-index the stale
// projects". The store was not stale: the reconcile had just made it more
// correct than it was.
func TestReconcileKeepsTheStoreFresh(t *testing.T) {
	gittest.SkipWithoutGit(t)
	dir := gitRepo(t)
	st := openStore(t)
	ctx := context.Background()

	if _, err := index.IndexDir(ctx, st, dir); err != nil {
		t.Fatal(err)
	}
	// A second file, so deleting the first leaves the store with content: a
	// store with no elements is "cold", and the question here is freshness,
	// not emptiness.
	gittest.WriteFile(t, dir, "other.go", "package main\n\nfunc Other() {}\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-m", "second")
	if _, err := index.IndexDir(ctx, st, dir); err != nil {
		t.Fatal(err)
	}
	// The inventory is computed by the core layer (store.RefreshInventory),
	// which the serving process runs after an index; the index itself does
	// not write it. The baseline is that state: a store whose inventory
	// matches its watermark.
	if _, err := store.RefreshInventory(st); err != nil {
		t.Fatalf("refresh inventory: %v", err)
	}
	n, err := st.ElementCount()
	if err != nil {
		t.Fatalf("element count: %v", err)
	}
	if got := store.Freshness(st, n); got != store.FreshnessFresh {
		t.Fatalf("the fixture store is %q before the reconcile, want %q", got, store.FreshnessFresh)
	}

	// Delete the file from the repository and commit the deletion: HEAD no
	// longer contains it, so the reconcile has something to remove.
	if err := os.Remove(filepath.Join(dir, "main.go")); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, dir, "add", "-A")
	gittest.Git(t, dir, "commit", "-m", "drop main.go")

	res, err := ReconcileHead(ctx, st)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.DeletedFiles != 1 {
		t.Fatalf("reconcile = %+v, want 1 file removed", res)
	}
	n, err = st.ElementCount()
	if err != nil {
		t.Fatalf("element count: %v", err)
	}
	if got := store.Freshness(st, n); got != store.FreshnessFresh {
		t.Fatalf("the store reports %q after the reconcile, want %q — a reconciled store is not a stale one", got, store.FreshnessFresh)
	}
}

// TestReconcileRefreshesTheInventoryCounts: the inventory is the store's own
// summary of what it holds, so a reconcile that removed content must leave
// the summary matching the store.
func TestReconcileRefreshesTheInventoryCounts(t *testing.T) {
	gittest.SkipWithoutGit(t)
	dir := gitRepo(t)
	st := openStore(t)
	ctx := context.Background()

	if _, err := index.IndexDir(ctx, st, dir); err != nil {
		t.Fatal(err)
	}
	gittest.WriteFile(t, dir, "other.go", "package main\n\nfunc Other() {}\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-m", "second")
	if _, err := index.IndexDir(ctx, st, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RefreshInventory(st); err != nil {
		t.Fatalf("refresh inventory: %v", err)
	}
	before, err := st.LoadInventory()
	if err != nil {
		t.Fatalf("load inventory: %v", err)
	}
	if before == nil {
		t.Fatal("the index left no inventory")
	}
	if err := os.Remove(filepath.Join(dir, "main.go")); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, dir, "add", "-A")
	gittest.Git(t, dir, "commit", "-m", "drop main.go")

	if _, err := ReconcileHead(ctx, st); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	after, err := st.LoadInventory()
	if err != nil {
		t.Fatalf("load inventory: %v", err)
	}
	if after == nil {
		t.Fatal("the reconcile left no inventory")
	}
	n, err := st.ElementCount()
	if err != nil {
		t.Fatalf("element count: %v", err)
	}
	if after.TotalElements != n {
		t.Fatalf("the inventory says %d elements, the store holds %d", after.TotalElements, n)
	}
	if after.TotalElements >= before.TotalElements {
		t.Errorf("the inventory did not shrink: %d -> %d elements", before.TotalElements, after.TotalElements)
	}
}

// TestReconcileHealsAnInventoryLeftBehind: a store an EARLIER reconcile
// invalidated — its watermark moved, its summary did not — must be healed even
// when this pass has nothing to delete. That is the state the live fleet was
// in when this rule was written: two projects reported "possibly_stale"
// because the first pass of the wave deleted their content, and the fix that
// only refreshed the inventory on a delete would have left them saying so
// until somebody re-indexed them.
func TestReconcileHealsAnInventoryLeftBehind(t *testing.T) {
	gittest.SkipWithoutGit(t)
	dir := gitRepo(t)
	st := openStore(t)
	ctx := context.Background()

	if _, err := index.IndexDir(ctx, st, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RefreshInventory(st); err != nil {
		t.Fatalf("refresh inventory: %v", err)
	}
	n, err := st.ElementCount()
	if err != nil {
		t.Fatalf("element count: %v", err)
	}
	if got := store.Freshness(st, n); got != store.FreshnessFresh {
		t.Fatalf("the fixture store is %q, want %q", got, store.FreshnessFresh)
	}

	// Simulate the earlier reconcile: a delete advances the watermark and
	// leaves the inventory behind.
	if err := st.DeleteByFile("gone-long-ago.go"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	n, err = st.ElementCount()
	if err != nil {
		t.Fatalf("element count: %v", err)
	}
	if got := store.Freshness(st, n); got != store.FreshnessPossiblyStale {
		t.Fatalf("the fixture store is %q after the delete, want %q", got, store.FreshnessPossiblyStale)
	}

	// This pass has nothing to delete — HEAD still contains every stored
	// path — and the store must still be healed.
	res, err := ReconcileHead(ctx, st)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.DeletedFiles != 0 {
		t.Fatalf("reconcile = %+v, want nothing deleted", res)
	}
	if got := store.Freshness(st, n); got != store.FreshnessFresh {
		t.Fatalf("the store reports %q after the reconcile, want %q — the pass must heal the inventory it left behind", got, store.FreshnessFresh)
	}
}
