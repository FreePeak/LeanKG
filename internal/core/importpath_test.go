package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// writeProject creates a two-level codebase: one file at the root and one in a
// subdirectory, so a walk rooted at the subdirectory is observably smaller than
// a walk rooted at the project.
func writeProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "root.go"), []byte("package p\n\nfunc RootOne() {}\nfunc RootTwo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "leaf.go"), []byte("package sub\n\nfunc Leaf() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func engineAt(t *testing.T, dir string) *Engine {
	t.Helper()
	st, err := store.OpenBackend(context.Background(), dir, "sqlite", "", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	e := New(st, nil, nil)
	e.SetProjectDir(dir)
	return e
}

func count(t *testing.T, e *Engine) int {
	t.Helper()
	n, err := e.st.ElementCount()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestImportRelativePathAnchorsAtProject is the regression for the dogfood
// incident: `import {action:"repo", path:"."}` resolved "." against the SERVER
// process's working directory, so a daemon started elsewhere indexed the wrong
// tree and the store reconcile deleted everything the walk missed. "." must
// mean the project, and an unchanged re-index must delete nothing.
func TestImportRelativePathAnchorsAtProject(t *testing.T) {
	dir := writeProject(t)
	e := engineAt(t, dir)
	ctx := context.Background()

	if _, err := e.Import(ctx, ImportRequest{Action: "repo", Path: dir}); err != nil {
		t.Fatalf("baseline index: %v", err)
	}
	full := count(t, e)
	if full < 3 {
		t.Fatalf("baseline indexed %d elements, want the 3 symbols in the tree", full)
	}

	// Now the same content addressed as "." from a completely unrelated cwd.
	t.Cleanup(func() { os.Chdir(dir) })
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	out, err := e.Import(ctx, ImportRequest{Action: "repo", Path: "."})
	if err != nil {
		t.Fatalf(`import "." : %v`, err)
	}
	if got := count(t, e); got != full {
		t.Fatalf(`import "." from a foreign cwd changed the element count %d -> %d (it indexed the cwd, not the project)`, full, got)
	}
	idx, _ := out["indexed"].(map[string]any)
	if d, ok := idx["deleted_files"]; ok {
		t.Fatalf(`re-indexing an unchanged tree deleted %v files; idx=%v`, d, idx)
	}
}

// TestImportSubtreeRefused pins the guard: a walk root that is a strict
// subdirectory of the project would read every outside file as deleted, so it is
// refused instead of attempted.
func TestImportSubtreeRefused(t *testing.T) {
	dir := writeProject(t)
	e := engineAt(t, dir)
	ctx := context.Background()
	if _, err := e.Import(ctx, ImportRequest{Action: "dir", Path: dir}); err != nil {
		t.Fatal(err)
	}
	before := count(t, e)
	_, err := e.Import(ctx, ImportRequest{Action: "dir", Path: filepath.Join(dir, "sub")})
	if err == nil {
		t.Fatalf("indexing a subtree was accepted; it deleted %d -> %d", before, count(t, e))
	}
	if count(t, e) != before {
		t.Fatalf("the refused import still mutated the store: %d -> %d", before, count(t, e))
	}
}
