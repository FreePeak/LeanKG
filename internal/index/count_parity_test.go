//go:build !tstree

package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// newParityStore opens a migrated store under its own temp directory.
func newParityStore(t *testing.T) store.Backend {
	t.Helper()
	dbDir := t.TempDir()
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(dbDir, "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

// writeTree writes files (repo-relative path -> content) under a fresh dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// danglingEdges counts stored edges whose source or target is not a stored
// element: the orphans `leankg status` counts inside relationships.
func danglingEdges(t *testing.T, st store.Backend) int {
	t.Helper()
	ctx := context.Background()
	els, err := st.Elements(ctx)
	if err != nil {
		t.Fatal(err)
	}
	live := make(map[string]bool, len(els))
	for _, e := range els {
		live[e.QualifiedName] = true
	}
	rels, err := st.RelationshipsAll(ctx, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range rels {
		if !live[r.Source] || !live[r.Target] {
			n++
		}
	}
	return n
}

// TestIndexCountsMatchStoreForSameNamedElements: two `init` functions in one
// file share a qualified name. The store keeps one row, so the summary must
// report the rows that were stored, not the two that were attempted.
func TestIndexCountsMatchStoreForSameNamedElements(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"dup.go": "package dup\n\nfunc init() {}\n\nfunc init() {}\n\nfunc Load() {}\n",
	})
	st := newParityStore(t)
	res := index(t, st, dir)

	stored, err := st.ElementCount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Elements != stored {
		t.Fatalf("res.Elements = %d, store ElementCount = %d: a same-named element was dropped on upsert", res.Elements, stored)
	}
	mustFind(t, st, "dup.go::init")
	mustFind(t, st, "dup.go::init (2)")
}

// TestGoASTElementsGetUniqueQualifiedNames: the go/parser tier emits a const
// and a func with the same name, plus two init funcs, all in one file. Each
// must keep its own row, so the summary equals the store count and every
// spelling is findable. The parser accepts this file; the compiler would not.
func TestGoASTElementsGetUniqueQualifiedNames(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"ast.go": "package ast\n\nconst Name = 1\n\nfunc Name() {}\n\nfunc init() {}\n\nfunc init() {}\n",
	})
	st := newParityStore(t)
	res := index(t, st, dir)

	stored, err := st.ElementCount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Elements != stored {
		t.Fatalf("res.Elements = %d, store ElementCount = %d", res.Elements, stored)
	}
	for _, qn := range []string{"ast.go::Name", "ast.go::Name (2)", "ast.go::init", "ast.go::init (2)"} {
		mustFind(t, st, qn)
	}
}

// TestDeletedFileLeavesNoDanglingIncomingEdges: a.go calls into b.go. Removing
// b.go must remove the edge that pointed INTO it, so the store matches a fresh
// index of the files that remain.
func TestDeletedFileLeavesNoDanglingIncomingEdges(t *testing.T) {
	files := map[string]string{
		"a.go": "package app\n\nfunc Caller() {\n\tHelperFn()\n}\n",
		"b.go": "package app\n\nfunc HelperFn() {}\n",
	}
	dir := writeTree(t, files)
	st := newParityStore(t)
	index(t, st, dir)
	if n, _ := st.RelationshipCount(context.Background()); n == 0 {
		t.Fatal("setup: a.go -> b.go call edge was not indexed")
	}

	if err := os.Remove(filepath.Join(dir, "b.go")); err != nil {
		t.Fatal(err)
	}
	index(t, st, dir)

	fresh := newParityStore(t)
	index(t, fresh, writeTree(t, map[string]string{"a.go": files["a.go"]}))
	want, _ := fresh.RelationshipCount(context.Background())
	got, _ := st.RelationshipCount(context.Background())
	if got != want {
		t.Fatalf("RelationshipCount after delete = %d, fresh index of the rest = %d", got, want)
	}
	if n := danglingEdges(t, st); n != 0 {
		t.Fatalf("dangling edges after delete = %d, want 0", n)
	}
}

// TestRoomDaoFixtureLeavesNoOrphans: the Room DAO fixture (entity with a
// tableName, DAO querying that table) must index without orphan edges.
func TestRoomDaoFixtureLeavesNoOrphans(t *testing.T) {
	src := mustReadFixture(t, "AppDatabase.kt")
	dir := writeTree(t, map[string]string{"AppDatabase.kt": string(src)})
	st := newParityStore(t)
	index(t, st, dir)
	if n := danglingEdges(t, st); n != 0 {
		t.Fatalf("orphan edges after a full index of the Room fixture = %d, want 0", n)
	}
}

// TestIncrementalReindexMatchesFullIndexCounts: after one file changes and
// another is added, the incremental re-index holds the same counts as a full
// index of the final tree.
func TestIncrementalReindexMatchesFullIndexCounts(t *testing.T) {
	v1 := map[string]string{
		"a.go": "package app\n\nfunc Caller() {\n\tHelperFn()\n}\n",
		"b.go": "package app\n\nfunc HelperFn() {}\n\nfunc init() {}\n",
	}
	v2 := map[string]string{
		"a.go": v1["a.go"],
		"b.go": "package app\n\nfunc HelperFn() {}\n\nfunc init() {}\n\nfunc init() {}\n",
		"c.go": "package app\n\nfunc Other() {\n\tHelperFn()\n}\n",
	}
	dir := writeTree(t, v1)
	st := newParityStore(t)
	index(t, st, dir)
	for rel, body := range v2 {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	index(t, st, dir)

	full := newParityStore(t)
	index(t, full, writeTree(t, v2))
	ctx := context.Background()
	gotEls, _ := st.ElementCount(ctx)
	wantEls, _ := full.ElementCount(ctx)
	gotRels, _ := st.RelationshipCount(ctx)
	wantRels, _ := full.RelationshipCount(ctx)
	if gotEls != wantEls || gotRels != wantRels {
		t.Fatalf("incremental elements/relationships = %d/%d, full index = %d/%d", gotEls, gotRels, wantEls, wantRels)
	}
	if n := danglingEdges(t, st); n != 0 {
		t.Fatalf("dangling edges after incremental re-index = %d, want 0", n)
	}
}

// TestUniqueQNsSuffixesRepeatsInOrder pins the suffix scheme: the first
// spelling of a name is kept, each repeat takes the lowest free " (n)" with
// n >= 2, and a spelling already taken is skipped.
func TestUniqueQNsSuffixesRepeatsInOrder(t *testing.T) {
	got := uniqueQNs(map[string]bool{}, []string{"f.go::init", "f.go::init", "f.go::init", "f.go::Load"})
	want := []string{"f.go::init", "f.go::init (2)", "f.go::init (3)", "f.go::Load"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("uniqueQNs = %v, want %v", got, want)
		}
	}
	taken := map[string]bool{"f.go::Load (2)": true}
	got = uniqueQNs(taken, []string{"f.go::Load", "f.go::Load"})
	if got[0] != "f.go::Load" || got[1] != "f.go::Load (3)" {
		t.Fatalf("uniqueQNs with a taken spelling = %v, want [Load, Load (3)]", got)
	}
}
