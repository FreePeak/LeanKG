package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/embed"
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
	st, err := store.OpenBackend(context.Background(), dir, "sqlite", "", "", store.RW)
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

// TestImportForeignRootRefused pins RS-02: any walk root other than the
// project root — sibling, ancestor, unrelated, relative climb — is refused
// with LEANKG_ERROR_INDEX_ROOT_MISMATCH and leaves the store untouched.
func TestImportForeignRootRefused(t *testing.T) {
	dir := writeProject(t)
	e := engineAt(t, dir)
	ctx := context.Background()
	if _, err := e.Import(ctx, ImportRequest{Action: "repo", Path: "."}); err != nil {
		t.Fatal(err)
	}
	before := count(t, e)
	sibling := t.TempDir()
	if err := os.WriteFile(filepath.Join(sibling, "x.go"), []byte("package x\n\nfunc X() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{sibling, filepath.Dir(dir), "..", "../..", filepath.Join(dir, "sub")} {
		for _, action := range []string{"repo", "dir"} {
			_, err := e.Import(ctx, ImportRequest{Action: action, Path: p})
			if err == nil || !strings.Contains(err.Error(), "LEANKG_ERROR_INDEX_ROOT_MISMATCH") {
				t.Fatalf("import %s %q: err=%v, want INDEX_ROOT_MISMATCH", action, p, err)
			}
			if got := count(t, e); got != before {
				t.Fatalf("refused import %s %q mutated the store: %d -> %d", action, p, before, got)
			}
		}
	}
	if _, err := e.Import(ctx, ImportRequest{Action: "dir", Path: dir}); err != nil {
		t.Fatalf("project root refused: %v", err)
	}
}

// TestDeletedCodeIsNeverASemanticHit pins RS-05: after a file is deleted and
// the project re-indexed, its symbols' vectors are gone and L3 never returns
// them. The validation run got the deleted symbol back as the rank-1 hit,
// with empty type/file/content, until `leankg gc`.
func TestDeletedCodeIsNeverASemanticHit(t *testing.T) {
	dir := writeProject(t)
	ghost := filepath.Join(dir, "ghost.go")
	if err := os.WriteFile(ghost, []byte("package p\n\nfunc RotateStagingCredentials() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := engineAt(t, dir)
	p := embed.Deterministic(64)
	e.SetEmbedder(QueryEmbedderFromProvider(p))
	ctx := context.Background()
	if _, err := e.Import(ctx, ImportRequest{Action: "repo", Path: "."}); err != nil {
		t.Fatal(err)
	}
	if _, err := embed.Run(ctx, e.st, p, "full"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ghost); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Import(ctx, ImportRequest{Action: "repo", Path: "."}); err != nil {
		t.Fatal(err)
	}
	vecs, _ := e.st.VectorCount(p.ModelID())
	if els := count(t, e); vecs != els {
		t.Fatalf("after re-index: %d vectors for %d elements (orphans left behind)", vecs, els)
	}
	out, err := e.Query(ctx, QueryRequest{Action: "semantic", Query: "RotateStagingCredentials", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hitsOf(out) {
		if strings.Contains(h["qualified_name"].(string), "ghost.go") {
			t.Fatalf("deleted symbol served as a semantic hit: %v", h)
		}
	}
}

// TestImportDocsRelativeAnchorsAtProject pins RS-08: `import docs path=docs`
// resolves against the project (it used to resolve against the server cwd
// and fail with ENOENT), keys files project-relative, and leaves .md records
// outside the walked directory alone.
func TestImportDocsRelativeAnchorsAtProject(t *testing.T) {
	dir := writeProject(t)
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "guide.md"), []byte("# Guide\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := engineAt(t, dir)
	ctx := context.Background()
	if _, err := e.Import(ctx, ImportRequest{Action: "repo", Path: "."}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(dir) })
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Import(ctx, ImportRequest{Action: "docs", Path: "docs"}); err != nil {
		t.Fatalf("import docs relative: %v", err)
	}
	files, _ := e.st.Files()
	got := map[string]bool{}
	for _, f := range files {
		got[f.Path] = true
	}
	if !got["docs/guide.md"] || !got["README.md"] || got["guide.md"] {
		t.Fatalf("file records after import docs = %v", got)
	}
	if _, err := e.Import(ctx, ImportRequest{Action: "docs", Path: "/etc"}); err == nil {
		t.Fatal("docs outside the project accepted")
	}
}

// TestPathNamesTheUnknownEndpoint: an unknown path endpoint is named, not
// reported as a bare "graph: unknown node".
func TestPathNamesTheUnknownEndpoint(t *testing.T) {
	dir := writeProject(t)
	e := engineAt(t, dir)
	ctx := context.Background()
	if _, err := e.Import(ctx, ImportRequest{Action: "repo", Path: "."}); err != nil {
		t.Fatal(err)
	}
	_, err := e.Query(ctx, QueryRequest{Action: "path", Query: "RootOne", Args: map[string]any{"to": "NoSuchThing"}})
	if err == nil || !strings.Contains(err.Error(), `to (args.to) endpoint "NoSuchThing"`) {
		t.Fatalf("err = %v, want the unknown 'to' endpoint named", err)
	}
}
