package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// TestImportMigratesTheStoreItWritesTo pins that the WRITE path owns the
// schema.
//
// The server no longer migrates a store when it opens it — a read must not
// rewrite the store it reads — so an import into a store whose schema was
// behind has to migrate it first. Without that, the import dies with "no such
// table: code_elements" and the operator cannot tell whether the store or
// the import is at fault.
func TestImportMigratesTheStoreItWritesTo(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	// No Migrate() on purpose: the store has no schema at all, and the write
	// path is the only thing that can bring it up to date.

	e := New(st, nil, nil)
	e.SetProjectDir(dir)
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := e.Import(context.Background(), ImportRequest{Action: "dir", Path: "."}); err != nil {
		t.Fatalf("import into a store with no schema: %v", err)
	}
	applied, err := store.AppliedMigrations(st)
	if err != nil {
		t.Fatalf("applied migrations: %v", err)
	}
	plan := store.Migrations()
	if len(plan) == 0 {
		t.Fatal("no migration plan")
	}
	want := plan[len(plan)-1].Version
	if len(applied) == 0 || applied[len(applied)-1] != want {
		t.Fatalf("the write path must leave the store at version %d, applied %v", want, applied)
	}
}
