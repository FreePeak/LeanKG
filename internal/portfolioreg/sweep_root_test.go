package portfolioreg

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/store"
)

// newStoreWithRoot creates a real store for dir and records root as the
// index root it was built from.
//
// The root is planted through index.RebaseRoot — the indexer's OWN writer —
// so this test pins the kv coordinates the sweep reads: if internal/index
// ever moves its root key, the sweep stops finding it and this test fails
// instead of the detection silently going blind.
func newStoreWithRoot(t *testing.T, dir, root string) {
	t.Helper()
	// LEANKG_DB_PATH would point every project at ONE shared store, which
	// makes the "is this store mine?" question meaningless.
	t.Setenv(store.StandalonePathEnv, "")
	dbPath := filepath.Join(dir, ".leankg", "leankg.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dbPath, store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := index.RebaseRoot(st, root); err != nil {
		t.Fatal(err)
	}
}

// TestSweepReportsRootMismatch is the E.2 regression.
//
// The store-present test used to be a pure ENOENT probe, so a store whose
// RECORDED ROOT no longer matches the directory it sits in counted as
// present: dead weight that no sweep, doctor or route could see. A store
// like that can never be repaired either — the index root guard refuses to
// walk a store from a different root, because every path in a store is
// relative to the root it was built from.
//
// The sweep must REPORT it (never delete it, never silently forget it).
func TestSweepReportsRootMismatch(t *testing.T) {
	o := Options{DBPath: filepath.Join(t.TempDir(), "portfolio.db")}
	ctx := context.Background()

	// The project directory, and a DIFFERENT directory the store claims to
	// have been indexed from (the "the checkout moved" case).
	dir := t.TempDir()
	other := t.TempDir()
	newStoreWithRoot(t, dir, other)

	if _, err := Register(ctx, o, dir, "moved", 12, 3, nil); err != nil {
		t.Fatal(err)
	}
	dirCanon, err := Canonical(dir)
	if err != nil {
		t.Fatal(err)
	}
	otherCanon, err := Canonical(other)
	if err != nil {
		t.Fatal(err)
	}

	// The verdict for that directory is a root mismatch...
	st, err := CheckStore(ctx, o, dirCanon)
	if err != nil {
		t.Fatalf("check store: %v", err)
	}
	if st.Reason != StoreRootMismatch {
		t.Fatalf("reason = %q, want %q (recorded root %q vs dir %q)",
			st.Reason, StoreRootMismatch, st.RecordedRoot, dirCanon)
	}
	if st.RecordedRoot != otherCanon {
		t.Errorf("recorded root = %q, want %q", st.RecordedRoot, otherCanon)
	}

	// ...the sweep reports it...
	rep, err := AutoSweep(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.RootMismatch) != 1 {
		t.Fatalf("sweep reported %d root mismatches, want 1: %+v", len(rep.RootMismatch), rep.RootMismatch)
	}
	if rep.RootMismatch[0].Dir != dirCanon || rep.RootMismatch[0].RecordedRoot != otherCanon {
		t.Errorf("mismatch = %+v, want dir %q recorded root %q", rep.RootMismatch[0], dirCanon, otherCanon)
	}
	if rep.RootMismatch[0].Reason != StoreRootMismatch {
		t.Errorf("reason = %q, want %q", rep.RootMismatch[0].Reason, StoreRootMismatch)
	}

	// ...and the row and the store BOTH survive: the directory is live, and
	// a sweep never deletes a file.
	rows, err := Projects(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Dir != dirCanon {
		t.Fatalf("rows = %+v, want the live project kept", rows)
	}
	if _, err := os.Stat(filepath.Join(dirCanon, ".leankg", "leankg.db")); err != nil {
		t.Errorf("the sweep deleted the store file: %v", err)
	}
}

// TestSweepRootCheckIgnoresHealthyStores is the other half of E.2: the
// detection must not cry wolf. A store whose recorded root IS its own
// directory is healthy, and a store that records no root at all (one built
// before the root guard existed) is UNKNOWN, which is not a mismatch.
func TestSweepRootCheckIgnoresHealthyStores(t *testing.T) {
	o := Options{DBPath: filepath.Join(t.TempDir(), "portfolio.db")}
	ctx := context.Background()

	healthy := t.TempDir()
	newStoreWithRoot(t, healthy, healthy) // records its own root

	noRoot := t.TempDir() // a store with no recorded root at all
	dbPath := filepath.Join(noRoot, ".leankg", "leankg.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dbPath, store.RW)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{healthy, noRoot} {
		if _, err := Register(ctx, o, dir, "p", 1, 1, nil); err != nil {
			t.Fatal(err)
		}
		verdict, err := CheckStore(ctx, o, dir)
		if err != nil {
			t.Fatalf("check %s: %v", dir, err)
		}
		if verdict.Reason != StoreOK {
			t.Errorf("%s: reason = %q, want %q", dir, verdict.Reason, StoreOK)
		}
	}
	rep, err := AutoSweep(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.RootMismatch) != 0 {
		t.Errorf("sweep reported %d mismatches on healthy stores: %+v", len(rep.RootMismatch), rep.RootMismatch)
	}
	if rep.Kept != 2 {
		t.Errorf("sweep kept %d rows, want 2", rep.Kept)
	}
}

// TestSweepKeepsLiveProjectWithoutStore is the E.3 regression.
//
// THE RULE: a registry row's life is bound to the project DIRECTORY, not to
// the store. A registered project whose directory is still a live project
// keeps its row even when it has no store yet (registered, never indexed —
// a legitimate state), and a row whose directory is gone goes.
//
// The old registry had no automatic path at all, so both halves of that
// rule were only ever applied by hand.
func TestSweepKeepsLiveProjectWithoutStore(t *testing.T) {
	o := Options{DBPath: filepath.Join(t.TempDir(), "portfolio.db")}
	ctx := context.Background()

	// A live project directory with NO store: registered, never indexed.
	live := t.TempDir()
	// A project directory that is gone.
	gone := filepath.Join(t.TempDir(), "gone")
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(ctx, o, live, "live", 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(ctx, o, gone, "gone", 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	liveCanon, err := Canonical(live)
	if err != nil {
		t.Fatal(err)
	}
	goneCanon, err := Canonical(gone)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	// A live directory with no store is NOT a store-less claim that the
	// sweep may drop: it is a live project the fleet may index later.
	verdict, err := CheckStore(ctx, o, liveCanon)
	if err != nil {
		t.Fatalf("check store: %v", err)
	}
	if verdict.Reason != StoreNoStore {
		t.Errorf("reason = %q, want %q", verdict.Reason, StoreNoStore)
	}

	rep, err := AutoSweep(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.NoStore) != 1 || rep.NoStore[0] != liveCanon {
		t.Errorf("no-store rows = %v, want [%s]", rep.NoStore, liveCanon)
	}
	if len(rep.Removed) != 1 || rep.Removed[0] != goneCanon {
		t.Errorf("removed = %v, want [%s]", rep.Removed, goneCanon)
	}
	rows, err := Projects(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Dir != liveCanon {
		t.Fatalf("rows = %+v, want only the live project kept", rows)
	}
}

// TestCheckStoreIgnoresASharedStore pins the shared-store exemption: a
// store that lives OUTSIDE the project (LEANKG_DB_PATH, or a leankg.yaml
// standalone_db_path) is one file for many projects, so its recorded root
// names the project that owns it — not this one. Comparing the two would
// report every other project as a root mismatch, which is exactly the cry
// wolf the root check must not do.
//
// The Postgres half matters too: the PG engine keeps ONE SCHEMA PER
// PROJECT on a shared server and ignores LEANKG_DB_PATH entirely, so a PG
// project's recorded root is always its own and the exemption must not
// apply to it.
func TestCheckStoreIgnoresASharedStore(t *testing.T) {
	shared := filepath.Join(t.TempDir(), "shared.db")
	if err := os.WriteFile(shared, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	o := Options{DBPath: filepath.Join(t.TempDir(), "portfolio.db")}

	// A store built from a DIFFERENT project, shared with this one.
	other := t.TempDir()
	st, err := store.Open(shared, store.RW)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := index.RebaseRoot(st, other); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv(store.StandalonePathEnv, shared)
	verdict, err := CheckStore(context.Background(), o, dir)
	if err != nil {
		t.Fatalf("check store: %v", err)
	}
	if verdict.Reason != StoreOK {
		t.Errorf("shared store: reason = %q, want %q (a shared store's root is its owner's)", verdict.Reason, StoreOK)
	}

	// The Postgres half: the PG engine keeps one schema per project and
	// IGNORES LEANKG_DB_PATH, so its projects' recorded roots are always
	// their own — the exemption must not swallow them.
	if storeIsShared(store.EnginePostgres, dir) {
		t.Errorf("postgres project reported as a shared store: its recorded root is its own")
	}
	if !storeIsShared(store.EngineSQLite, dir) {
		t.Errorf("sqlite project with LEANKG_DB_PATH set reported as NOT shared")
	}
}
