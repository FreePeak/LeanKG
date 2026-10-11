package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// TestOpenStoreResolvesStandalonePath is FR-P2's documented flow, which was
// broken: `LEANKG_DB_PATH=<p> leankg index .` then serving against the same
// path. `serve` resolved the standalone path, but ten other verbs passed a
// bare "" and silently read <cwd>/.leankg/leankg.db, so the corpus was split
// in two and every verb but serve answered from an empty store.
//
// Verified live before the fix: with <cwd>/.leankg moved aside,
// `leankg status` reported "read-only open of missing store
// <cwd>/.leankg/leankg.db" with LEANKG_DB_PATH set — the variable was never
// read on that path.
func TestOpenStoreResolvesStandalonePath(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "standalone.db")
	t.Setenv("LEANKG_DB_PATH", dbPath)

	st, err := openStore(context.Background(), dir, "", store.RW)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("the standalone store was not created at the configured path: %v", err)
	}
}

// TestOpenStoreExplicitPathWins: a caller naming a store is never overridden
// by ambient config.
func TestOpenStoreExplicitPathWins(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "explicit.db")
	t.Setenv("LEANKG_DB_PATH", filepath.Join(dir, "env.db"))

	st, err := openStore(context.Background(), dir, explicit, store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(explicit); err != nil {
		t.Fatalf("explicit path was not used: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "env.db")); !os.IsNotExist(err) {
		t.Fatal("the env path was used even though an explicit path was given")
	}
}

// TestOpenStoreProjectScopedUnchanged: with no standalone path configured the
// store stays at <project>/.leankg/leankg.db — the fix must not move anyone's
// existing store.
func TestOpenStoreProjectScopedUnchanged(t *testing.T) {
	dir := t.TempDir()
	os.Unsetenv("LEANKG_DB_PATH")

	st, err := openStore(context.Background(), dir, "", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".leankg", "leankg.db")); err != nil {
		t.Fatalf("project-scoped store not created: %v", err)
	}
}
