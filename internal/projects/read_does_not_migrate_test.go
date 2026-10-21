package projects

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// pre017Project builds a project whose store is the REAL pre-017 shape:
// every sqlite migration up to 015 applied and 017 NOT applied — the schema
// a store has when the last writer was a binary that predated the text-cap
// column.
//
// The fixture starts as a migrated store and is taken back to 015 the way an
// older writer's store looks: the 017 column gone, the 017 ledger row gone.
// A read must leave it exactly as it is.
func pre017Project(t *testing.T) string {
	t.Helper()
	dir := canonical(t.TempDir())
	path := filepath.Join(dir, ".leankg", "leankg.db")
	st, err := store.Open(path, store.RW)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open the store file directly: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`ALTER TABLE emb_stamp DROP COLUMN text_cap`); err != nil {
		t.Fatalf("take the store back to 015: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = 17`); err != nil {
		t.Fatalf("drop the 017 ledger row: %v", err)
	}
	return dir
}

// TestRoutedReadDoesNotMigrateTheStore pins that serving a routed project
// does not rewrite the store it serves.
//
// The defect: opening a routed project Migrate()d its store, so one query
// against a project whose store was one migration behind silently rewrote
// that store's schema — and the rewrite happened in the READ path, where
// nobody asked for a write. The schema is the writer's to maintain: a reader
// that migrates on open makes the drift invisible, and the fleet doctor then
// reports a version that depends on who looked first.
func TestRoutedReadDoesNotMigrateTheStore(t *testing.T) {
	t.Setenv("LEANKG_DB_PATH", "")
	dir := pre017Project(t)
	base := canonical(t.TempDir())
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}

	r := NewRouter(base, Config{
		ExtraDirs:  []string{dir},
		Mode:       store.RW,
		EngineName: store.EngineSQLite,
	})
	p, err := r.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("open the routed project: %v", err)
	}
	applied, err := store.AppliedMigrations(p.Store)
	if err != nil {
		t.Fatalf("applied migrations: %v", err)
	}
	for _, v := range applied {
		if v == 17 {
			t.Fatalf("serving a project migrated the store it served: schema_migrations now holds version 17 (%v)", applied)
		}
	}
	// The column migration 017 adds is still missing: the store was served,
	// not rewritten.
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, ".leankg", "leankg.db"))
	if err != nil {
		t.Fatalf("open the store file directly: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`SELECT text_cap FROM emb_stamp`); err == nil {
		t.Fatal("the store was migrated by a read: emb_stamp.text_cap exists")
	}
}

// TestRoutedProjectWithoutAStoreIsRefused pins that a project nobody indexed
// is refused, not created.
//
// The defect: opening a routed project opened its store read-write, which
// CREATES the file — so a single query against a project nobody had indexed
// left a store (and a schema) behind, and the project then looked indexed
// forever. A read must not create the thing it reads.
func TestRoutedProjectWithoutAStoreIsRefused(t *testing.T) {
	t.Setenv("LEANKG_DB_PATH", "")
	dir := canonical(t.TempDir())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := canonical(t.TempDir())
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}

	r := NewRouter(base, Config{
		ExtraDirs:  []string{dir},
		Mode:       store.RW,
		EngineName: store.EngineSQLite,
	})
	_, err := r.Open(context.Background(), dir)
	if err == nil {
		t.Fatal("opening a project with no store must be refused")
	}
	if !strings.Contains(err.Error(), "not indexed") {
		t.Fatalf("the refusal must name the missing store: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".leankg", "leankg.db")); !os.IsNotExist(statErr) {
		t.Fatalf("a refused project must not leave a store behind: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".leankg")); !os.IsNotExist(statErr) {
		t.Fatalf("a refused project must not leave a .leankg directory behind: %v", statErr)
	}
}
