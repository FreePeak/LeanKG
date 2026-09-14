package export

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// newFixture opens a migrated RW store and loads the fixture; it mirrors the
// helper used by internal/graph tests.
func newFixture(t *testing.T, els []store.Element, rels []store.Relationship) store.Backend {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := s.UpsertElements(els); err != nil {
		t.Fatalf("upsert elements: %v", err)
	}
	if err := s.UpsertRelationships(rels); err != nil {
		t.Fatalf("upsert relationships: %v", err)
	}
	return s
}

func el(qn, name, elType, file string, line int) store.Element {
	return store.Element{
		QualifiedName: qn,
		ElementType:   elType,
		Name:          name,
		FilePath:      file,
		LineStart:     line,
		LineEnd:       line + 5,
		Language:      "rust",
	}
}

func rel(src, tgt, relType string) store.Relationship {
	return store.Relationship{Source: src, Target: tgt, RelType: relType, Confidence: 1.0}
}

// testFixElements is the markdown fixture store shape: two files, one hub.
var testFixElements = []store.Element{
	el("src/app.rs::hub", "hub", "function", "src/app.rs", 1),
	el("src/app.rs::leaf", "leaf", "function", "src/app.rs", 10),
	el("src/lib.rs::init", "init", "function", "src/lib.rs", 1),
	el("src/lib.rs", "lib.rs", "file", "src/lib.rs", 0),
}

var testFixRels = []store.Relationship{
	rel("src/app.rs::hub", "src/app.rs::leaf", "calls"),
	rel("src/lib.rs::init", "src/app.rs::leaf", "calls"),
	rel("src/app.rs::hub", "src/lib.rs::init", "imports"),
	// Dangling endpoints exist in real graphs; the edge filter drops them.
	rel("x", "y", "references"),
}

func TestDedupeQualifiedName(t *testing.T) {
	els := []store.Element{
		el("a.rs::f", "f", "function", "a.rs", 1),
		el("a.rs::f", "f", "function", "a.rs", 1),
		el("b.rs::g", "g", "function", "b.rs", 1),
	}
	var rels []store.Relationship
	if dedupeAndFilter(&els, &rels, 100) {
		t.Fatal("nothing should be truncated below budget")
	}
	if len(els) != 2 {
		t.Fatalf("duplicate qualified_name must be removed, got %d", len(els))
	}
	if els[0].QualifiedName != "a.rs::f" || els[1].QualifiedName != "b.rs::g" {
		t.Fatalf("wrong survivors: %q, %q", els[0].QualifiedName, els[1].QualifiedName)
	}
}

func TestEdgeFilterAlwaysRetainsConnected(t *testing.T) {
	els := []store.Element{
		el("a.rs::f", "f", "function", "a.rs", 1),
		el("b.rs::g", "g", "function", "b.rs", 1),
	}
	rels := []store.Relationship{
		rel("a.rs::f", "b.rs::g", "calls"),
		rel("a.rs::f", "c.rs::h", "calls"),
	}
	dedupeAndFilter(&els, &rels, 100)
	if len(rels) != 1 {
		t.Fatalf("orphan edge must be stripped, got %d", len(rels))
	}
	if rels[0].Target != "b.rs::g" {
		t.Fatalf("wrong surviving edge: %+v", rels[0])
	}
}

func TestTruncationBudget(t *testing.T) {
	els := make([]store.Element, 0, 10)
	for i := 0; i < 10; i++ {
		qn := fmt.Sprintf("n%d.rs::f", i)
		els = append(els, el(qn, "f", "function", fmt.Sprintf("n%d.rs", i), 1))
	}
	rels := []store.Relationship{rel("n0.rs::f", "n1.rs::f", "calls")}
	if !dedupeAndFilter(&els, &rels, 3) {
		t.Fatal("budget below the element count must report truncation")
	}
	if len(els) != 3 {
		t.Fatalf("must truncate to budget, got %d", len(els))
	}
	// Degree-first cut: the two connected nodes survive the budget cut.
	if els[0].QualifiedName != "n0.rs::f" || els[1].QualifiedName != "n1.rs::f" {
		t.Fatalf("connected nodes must rank first: %q %q", els[0].QualifiedName, els[1].QualifiedName)
	}
}

func TestSelectFullGraph(t *testing.T) {
	st := newFixture(t, testFixElements, testFixRels)
	els, rels, meta, err := Select(st, Options{})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if meta.ScopeDesc != "full graph" {
		t.Fatalf("scope desc: %q", meta.ScopeDesc)
	}
	if meta.MaxNodes != DefaultMaxNodes {
		t.Fatalf("default budget: %d", meta.MaxNodes)
	}
	if len(els) != 4 {
		t.Fatalf("elements: %d", len(els))
	}
	// The dangling x→y edge is dropped because neither endpoint is present.
	if len(rels) != 3 {
		t.Fatalf("edges after endpoint filter: %d", len(rels))
	}
	if meta.Truncated {
		t.Fatal("full graph must not report truncation")
	}
}

func TestSelectPathPrefix(t *testing.T) {
	st := newFixture(t, testFixElements, testFixRels)
	els, rels, meta, err := Select(st, Options{Path: "src/app"})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if meta.ScopeDesc != "path:src/app" {
		t.Fatalf("scope desc: %q", meta.ScopeDesc)
	}
	if len(els) != 2 {
		t.Fatalf("path-scoped elements: %d (%v)", len(els), els)
	}
	// hub→leaf stays; the init→leaf call has its source outside the scope.
	if len(rels) != 1 || rels[0].Source != "src/app.rs::hub" {
		t.Fatalf("path-scoped edges: %+v", rels)
	}
}

func TestSelectPathPrefixNormalizesDotSlash(t *testing.T) {
	st := newFixture(t, testFixElements, testFixRels)
	els, _, _, err := Select(st, Options{Path: "./src/app"})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(els) != 2 {
		t.Fatalf("./-prefixed scope must normalize: %d elements", len(els))
	}
}

func TestSelectFileScoped(t *testing.T) {
	els := []store.Element{
		el("src/app.rs::hub", "hub", "function", "src/app.rs", 1),
		el("src/lib.rs", "lib.rs", "file", "src/lib.rs", 0),
		el("src/lib.rs::init", "init", "function", "src/lib.rs", 1),
	}
	rels := []store.Relationship{
		rel("src/lib.rs", "src/lib.rs::init", "contains"),
		rel("src/lib.rs::init", "src/app.rs::hub", "calls"),
	}
	st := newFixture(t, els, rels)

	gotEls, gotRels, meta, err := Select(st, Options{File: "src/lib.rs", Depth: 2})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if meta.ScopeDesc != "file:src/lib.rs (depth 2)" {
		t.Fatalf("scope desc: %q", meta.ScopeDesc)
	}
	// The walk follows the file element's outgoing edge and then stops: the
	// target (a function QN, not a file) has no outgoing edges of its own.
	if len(gotEls) != 2 {
		t.Fatalf("file-scoped elements: %d (%v)", len(gotEls), gotEls)
	}
	if len(gotRels) != 1 || gotRels[0].Source != "src/lib.rs" {
		t.Fatalf("file-scoped edges: %+v", gotRels)
	}
	for _, e := range gotEls {
		if e.FilePath != "src/lib.rs" {
			t.Fatalf("element outside seed file: %+v", e)
		}
	}
}

func TestSelectCommunityScope(t *testing.T) {
	st := newFixture(t, testFixElements, testFixRels)
	resolver := func(id string) (members []string, label string, ok bool) {
		if id != "c1" {
			return nil, "", false
		}
		return []string{"src/app.rs::hub", "src/app.rs::leaf"}, "app", true
	}
	els, rels, meta, err := Select(st, Options{Community: "c1", Clusters: resolver})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if meta.ScopeDesc != "community:c1" || len(els) != 2 || len(rels) != 1 {
		t.Fatalf("community scope: meta=%+v els=%d rels=%d", meta, len(els), len(rels))
	}

	if _, _, _, err := Select(st, Options{Community: "nope", Clusters: resolver}); err == nil {
		t.Fatal("unknown community must be an error, not an empty export")
	}
	if _, _, _, err := Select(st, Options{Community: "c1"}); err == nil {
		t.Fatal("community scope without a resolver must be an error")
	}
}

func TestClusterResolverMatchesIDThenLabel(t *testing.T) {
	assignments := map[string]string{
		"src/app.rs::hub":  "c1",
		"src/app.rs::leaf": "c1",
		"src/lib.rs::init": "c2",
	}
	labels := map[string]string{"c1": "app", "c2": "lib"}
	resolve := ClusterResolver(assignments, labels)

	members, label, ok := resolve("c1")
	if !ok || label != "app" {
		t.Fatalf("id lookup: %v %q %v", members, label, ok)
	}
	if len(members) != 2 || members[0] != "src/app.rs::hub" || members[1] != "src/app.rs::leaf" {
		t.Fatalf("members must be sorted: %v", members)
	}

	if _, label, ok := resolve("lib"); !ok || label != "lib" {
		t.Fatalf("label lookup: %q %v", label, ok)
	}
	if _, _, ok := resolve("missing"); ok {
		t.Fatal("unknown community must resolve to ok=false")
	}
}

func TestAllDoesNotFilter(t *testing.T) {
	st := newFixture(t, testFixElements, testFixRels)
	els, rels, err := All(st)
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(els) != 4 {
		t.Fatalf("elements: %d", len(els))
	}
	// Unlike Select, the unscoped reader keeps the dangling x→y edge — that is
	// exactly what the legacy full-graph dot/mermaid path streamed.
	if len(rels) != 4 {
		t.Fatalf("relationships: %d", len(rels))
	}
}
