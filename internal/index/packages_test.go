//go:build !tstree

package index

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"slices"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// mustPackage asserts the package element of directory dir: one element per Go
// directory, typed package, keyed by the module-relative path, file_path is the
// directory, name is its last element, and it has no line range.
func mustPackage(t *testing.T, st store.Backend, dir string) {
	t.Helper()
	el := mustFind(t, st, dir)
	if el.ElementType != "package" || el.FilePath != dir || el.Name != path.Base(dir) || el.LineStart != 0 || el.LineEnd != 0 {
		t.Fatalf("package element %q = type %q file %q name %q lines %d-%d", dir, el.ElementType, el.FilePath, el.Name, el.LineStart, el.LineEnd)
	}
}

// assertNoPackage fails when a package element named qn is still stored.
func assertNoPackage(t *testing.T, st store.Backend, qn string) {
	t.Helper()
	els, err := st.FindExact(context.Background(), qn)
	if err != nil {
		t.Fatal(err)
	}
	if len(els) != 0 {
		t.Fatalf("FindExact(%q): got %d elements, want 0", qn, len(els))
	}
}

// threePackages is the fixture of the package tests: cmd/app imports internal/mcp
// and internal/store, internal/mcp imports internal/store, and internal/store
// imports only the standard library and an external module.
var threePackages = map[string]string{
	"cmd/app/main.go":         "package main\n\nimport (\n\t\"fmt\"\n\n\t\"github.com/acme/app/internal/mcp\"\n\t\"github.com/acme/app/internal/store\"\n)\n\nfunc main() {\n\tfmt.Println(store.Open(), mcp.Serve())\n}\n",
	"internal/mcp/server.go":  "package mcp\n\nimport \"github.com/acme/app/internal/store\"\n\nfunc Serve() string { return store.Open() }\n",
	"internal/store/store.go": "package store\n\nimport (\n\t\"strings\"\n\n\t\"github.com/external/lib\"\n)\n\nfunc Open() string { return strings.ToUpper(lib.Name) }\n",
}

// TestGoPackagesAndImportsEdges (i): one package element per Go directory, and
// the module-internal imports edges in both directions.
func TestGoPackagesAndImportsEdges(t *testing.T) {
	dir := writeTree(t, threePackages)
	st := newParityStore(t)
	index(t, st, dir)

	for _, pkg := range []string{"cmd/app", "internal/mcp", "internal/store"} {
		mustPackage(t, st, pkg)
	}
	if got, want := importEnds(t, st, "internal/store", false), []string{"cmd/app", "internal/mcp"}; !slices.Equal(got, want) {
		t.Fatalf("importers of internal/store = %v, want %v", got, want)
	}
	if got, want := importEnds(t, st, "cmd/app", true), []string{"internal/mcp", "internal/store"}; !slices.Equal(got, want) {
		t.Fatalf("dependencies of cmd/app = %v, want %v", got, want)
	}
	if got, want := importEnds(t, st, "internal/mcp", true), []string{"internal/store"}; !slices.Equal(got, want) {
		t.Fatalf("dependencies of internal/mcp = %v, want %v", got, want)
	}
}

// TestGoImportsIgnoreStdlibAndExternal (ii): a package whose imports are all
// stdlib or external gets no imports edge, and testdata is not a package.
func TestGoImportsIgnoreStdlibAndExternal(t *testing.T) {
	files := map[string]string{
		"internal/store/store.go":       threePackages["internal/store/store.go"],
		"testdata/fixture/fixture.go":   "package fixture\n\nimport \"github.com/acme/app/internal/store\"\n\nvar _ = store.Open\n",
		"internal/external/external.go": "package external\n\nimport (\n\t\"fmt\"\n\n\t\"github.com/external/lib\"\n)\n\nvar _ = fmt.Sprint(lib.Name)\n",
	}
	dir := writeTree(t, files)
	st := newParityStore(t)
	index(t, st, dir)

	if got := importEnds(t, st, "internal/store", true); len(got) != 0 {
		t.Fatalf("dependencies of internal/store = %v, want none (stdlib and external only)", got)
	}
	if got := importEnds(t, st, "internal/external", true); len(got) != 0 {
		t.Fatalf("dependencies of internal/external = %v, want none", got)
	}
	assertNoElement(t, st, "testdata/fixture")
	if got := importEnds(t, st, "internal/store", false); len(got) != 0 {
		t.Fatalf("testdata imports must not create edges into internal/store, got sources %v", got)
	}
}

// TestGoImportsTestOnlyFlag (iii): an import that only _test.go files name is
// flagged test_only; an import that a non-test file also names is not; and an
// external test package importing its own directory gets no self edge.
func TestGoImportsTestOnlyFlag(t *testing.T) {
	files := map[string]string{
		"cmd/app/main.go":                  threePackages["cmd/app/main.go"],
		"cmd/app/app_test.go":              "package main\n\nimport \"github.com/acme/app/internal/mcp\"\n\nvar _ = mcp.Serve\n",
		"internal/mcp/server.go":           threePackages["internal/mcp/server.go"],
		"internal/store/store.go":          threePackages["internal/store/store.go"],
		"internal/store/store_ext_test.go": "package store_test\n\nimport \"github.com/acme/app/internal/store\"\n\nvar _ = store.Open\n",
		"internal/fixture/fixture_test.go": "package fixture_test\n\nimport \"github.com/acme/app/internal/store\"\n\nvar _ = store.Open\n",
	}
	dir := writeTree(t, files)
	st := newParityStore(t)
	index(t, st, dir)

	fixture, ok := importEdge(t, st, "internal/fixture", "internal/store")
	if !ok {
		t.Fatal("internal/fixture -> internal/store edge missing (tests-only package)")
	}
	if fixture.Metadata["test_only"] != true {
		t.Fatalf("internal/fixture -> internal/store metadata = %v, want test_only true", fixture.Metadata)
	}
	mixed, ok := importEdge(t, st, "cmd/app", "internal/mcp")
	if !ok {
		t.Fatal("cmd/app -> internal/mcp edge missing")
	}
	if v, set := mixed.Metadata["test_only"]; set && v == true {
		t.Fatalf("cmd/app -> internal/mcp is flagged test_only although main.go imports it: %v", mixed.Metadata)
	}
	if _, self := importEdge(t, st, "internal/store", "internal/store"); self {
		t.Fatal("internal/store has a self imports edge from its external test package")
	}
}

// TestGoPackageRemovedWithItsLastFile (iv): deleting a package's last Go file
// removes its element, its edges and the edges that name it, so the incremental
// store matches a fresh index of what remains.
func TestGoPackageRemovedWithItsLastFile(t *testing.T) {
	files := map[string]string{}
	for k, v := range threePackages {
		files[k] = v
	}
	files["internal/fixture/fixture_test.go"] = "package fixture_test\n\nimport \"github.com/acme/app/internal/store\"\n\nvar _ = store.Open\n"
	files["cmd/app/main.go"] = "package main\n\nimport (\n\t\"fmt\"\n\n\t\"github.com/acme/app/internal/fixture\"\n\t\"github.com/acme/app/internal/mcp\"\n\t\"github.com/acme/app/internal/store\"\n)\n\nfunc main() {\n\tfmt.Println(store.Open(), mcp.Serve(), fixture.Name)\n}\n"
	dir := writeTree(t, files)
	st := newParityStore(t)
	index(t, st, dir)
	mustPackage(t, st, "internal/fixture")

	if err := os.Remove(filepath.Join(dir, "internal/fixture/fixture_test.go")); err != nil {
		t.Fatal(err)
	}
	index(t, st, dir)

	assertNoPackage(t, st, "internal/fixture")
	if n := danglingEdges(t, st); n != 0 {
		t.Fatalf("dangling edges after the package was removed = %d, want 0", n)
	}
	if got, want := importEnds(t, st, "cmd/app", true), []string{"internal/mcp", "internal/store"}; !slices.Equal(got, want) {
		t.Fatalf("dependencies of cmd/app after removal = %v, want %v", got, want)
	}

	delete(files, "internal/fixture/fixture_test.go")
	fresh := newParityStore(t)
	index(t, fresh, writeTree(t, withoutFixture(files)))
	assertSameCounts(t, st, fresh)
}

// TestGoIncrementalFileKeepsOtherPackageEdges (v): re-indexing one changed file
// replaces only its own package's imports edges; the other packages' edges are
// the same rows as before.
func TestGoIncrementalFileKeepsOtherPackageEdges(t *testing.T) {
	dir := writeTree(t, threePackages)
	st := newParityStore(t)
	index(t, st, dir)
	before := importEnds(t, st, "internal/mcp", true)
	beforeIn := importEnds(t, st, "internal/store", false)

	if err := os.WriteFile(filepath.Join(dir, "cmd/app/main.go"), []byte(threePackages["cmd/app/main.go"]+"\nfunc extra() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	index(t, st, dir)

	if got := importEnds(t, st, "internal/mcp", true); !slices.Equal(got, before) {
		t.Fatalf("dependencies of internal/mcp changed on a cmd/app edit: %v, was %v", got, before)
	}
	if got := importEnds(t, st, "internal/store", false); !slices.Equal(got, beforeIn) {
		t.Fatalf("importers of internal/store changed on a cmd/app edit: %v, was %v", got, beforeIn)
	}
}

// TestGoPackageCountsMatchFreshIndex (vi): the summary of a fresh index equals
// the store counts, package elements and imports edges included.
func TestGoPackageCountsMatchFreshIndex(t *testing.T) {
	st := newParityStore(t)
	res := index(t, st, writeTree(t, threePackages))
	ctx := context.Background()
	els, err := st.ElementCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rels, err := st.RelationshipCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Elements != els || res.Relationships != rels {
		t.Fatalf("summary elements/relationships = %d/%d, store = %d/%d", res.Elements, res.Relationships, els, rels)
	}
	if n := danglingEdges(t, st); n != 0 {
		t.Fatalf("dangling edges = %d, want 0", n)
	}
}

// TestGoNewPackageGetsImportersOnIncrementalRun (vii): a package created in a run
// is named by an importer that the run does not change; the importer's edge
// appears, as a fresh index would have it.
func TestGoNewPackageGetsImportersOnIncrementalRun(t *testing.T) {
	// An unused import keeps the test to the imports edge: a qualified call would
	// add a call edge, which an unchanged importer does not get back (crosspkg.go).
	v1 := map[string]string{"cmd/app/main.go": "package main\n\nimport \"github.com/acme/app/internal/mcp\"\n\nfunc main() {}\n"}
	dir := writeTree(t, v1)
	st := newParityStore(t)
	index(t, st, dir)

	if err := os.MkdirAll(filepath.Join(dir, "internal/mcp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal/mcp/server.go"), []byte(threePackages["internal/mcp/server.go"]), 0o644); err != nil {
		t.Fatal(err)
	}
	index(t, st, dir)

	if got, want := importEnds(t, st, "cmd/app", true), []string{"internal/mcp"}; !slices.Equal(got, want) {
		t.Fatalf("dependencies of cmd/app = %v, want %v", got, want)
	}
	fresh := newParityStore(t)
	index(t, fresh, writeTree(t, map[string]string{
		"cmd/app/main.go":        v1["cmd/app/main.go"],
		"internal/mcp/server.go": threePackages["internal/mcp/server.go"],
	}))
	assertSameCounts(t, st, fresh)
}

// withoutFixture drops the fixture package's files from a tree.
func withoutFixture(files map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range files {
		if path.Dir(k) == "internal/fixture" {
			continue
		}
		out[k] = v
	}
	return out
}

// assertSameCounts fails unless st and fresh hold the same element and edge counts.
func assertSameCounts(t *testing.T, st, fresh store.Backend) {
	t.Helper()
	ctx := context.Background()
	gotEls, _ := st.ElementCount(ctx)
	wantEls, _ := fresh.ElementCount(ctx)
	gotRels, _ := st.RelationshipCount(ctx)
	wantRels, _ := fresh.RelationshipCount(ctx)
	if gotEls != wantEls || gotRels != wantRels {
		t.Fatalf("incremental elements/relationships = %d/%d, fresh index = %d/%d", gotEls, gotRels, wantEls, wantRels)
	}
	if n := danglingEdges(t, st); n != 0 {
		t.Fatalf("dangling edges = %d, want 0", n)
	}
}

// TestGoDeletedFileDropsItsImportsFromThePackage (viii): a package that loses one
// of its Go files, and keeps the others, drops the imports only that file named.
// None of its remaining files changes, so the edge goes through the package pass
// (refreshPackages) and not the directory loop.
func TestGoDeletedFileDropsItsImportsFromThePackage(t *testing.T) {
	files := map[string]string{
		"cmd/app/main.go":         threePackages["cmd/app/main.go"],
		"internal/mcp/server.go":  "package mcp\n\nfunc Serve() string { return \"\" }\n",
		"internal/mcp/extra.go":   "package mcp\n\nimport \"github.com/acme/app/internal/store\"\n\nvar _ = store.Open\n",
		"internal/store/store.go": threePackages["internal/store/store.go"],
	}
	dir := writeTree(t, files)
	st := newParityStore(t)
	index(t, st, dir)
	if got, want := importEnds(t, st, "internal/mcp", true), []string{"internal/store"}; !slices.Equal(got, want) {
		t.Fatalf("setup: dependencies of internal/mcp = %v, want %v", got, want)
	}

	if err := os.Remove(filepath.Join(dir, "internal/mcp/extra.go")); err != nil {
		t.Fatal(err)
	}
	index(t, st, dir)

	if got := importEnds(t, st, "internal/mcp", true); len(got) != 0 {
		t.Fatalf("dependencies of internal/mcp after its importing file was deleted = %v, want none", got)
	}
	mustPackage(t, st, "internal/mcp")
	if n := danglingEdges(t, st); n != 0 {
		t.Fatalf("dangling edges = %d, want 0", n)
	}
}
