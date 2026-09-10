package graph

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// newFixture: chain x→a→b→c→d plus e→c, and a non-calls edge (imports) that
// traversal filters must drop from callers/callees (but NOT from degree
// counts — Explain reports all edges).
func newFixture(t *testing.T) store.Backend {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	els := make([]store.Element, 0, 6)
	for _, qn := range []string{"a", "b", "c", "d", "e", "x"} {
		els = append(els, store.Element{QualifiedName: qn, Name: qn, ElementType: "func", FilePath: qn + ".go"})
	}
	if err := s.UpsertElements(els); err != nil {
		t.Fatalf("upsert elements: %v", err)
	}
	rels := []store.Relationship{
		{Source: "x", Target: "a", RelType: "calls", Confidence: 0.9},
		{Source: "a", Target: "b", RelType: "calls", Confidence: 0.9},
		{Source: "b", Target: "c", RelType: "calls", Confidence: 0.9},
		{Source: "c", Target: "d", RelType: "calls", Confidence: 0.9},
		{Source: "e", Target: "c", RelType: "calls", Confidence: 0.8},
		// non-calls edge: caller/callee filters must drop it; degree counts keep it
		{Source: "a", Target: "b", RelType: "imports", Confidence: 0.5},
	}
	if err := s.UpsertRelationships(rels); err != nil {
		t.Fatalf("upsert relationships: %v", err)
	}
	return s
}

func qns(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.QN
	}
	return out
}

// Impact = blast radius over INCOMING edges (who depends on X): changing a
// can break x (x calls a). a→b is a→b outgoing — b does NOT depend on a.
// Result order is depth-then-QN (BFS level order, sorted within each level).
func TestImpact(t *testing.T) {
	st := newFixture(t)

	hits, err := Impact(st, "a", 1)
	if err != nil {
		t.Fatalf("Impact(a,1): %v", err)
	}
	if got, want := qns(hits), []string{"a", "x"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Impact(a,1) = %v, want %v", got, want)
	}
	if hits[0].Depth != 0 || hits[1].Depth != 1 {
		t.Fatalf("depths = %d,%d, want 0,1", hits[0].Depth, hits[1].Depth)
	}

	// deeper traversal adds nothing: a has exactly one dependent.
	hits, err = Impact(st, "a", 5)
	if err != nil {
		t.Fatalf("Impact(a,5): %v", err)
	}
	if got, want := qns(hits), []string{"a", "x"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Impact(a,5) = %v, want %v", got, want)
	}

	// c: dependents b+e (d1), a (d2), x (d3).
	hits, _ = Impact(st, "c", 5)
	// depth-then-QN order: c(0), b+e(1), a(2), x(3)
	if got, want := qns(hits), []string{"c", "b", "e", "a", "x"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Impact(c,5) = %v, want %v", got, want)
	}

	if _, err := Impact(st, "nope", 2); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("Impact(unknown) err = %v, want ErrUnknownNode", err)
	}
}

func TestShortestPath(t *testing.T) {
	st := newFixture(t)

	path, err := ShortestPath(st, "a", "d", 5)
	if err != nil {
		t.Fatalf("ShortestPath(a,d): %v", err)
	}
	if got, want := path, []string{"a", "b", "c", "d"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ShortestPath(a,d) = %v, want %v", got, want)
	}

	// undirected view: reverse direction walks the same edges backwards
	path, err = ShortestPath(st, "d", "a", 5)
	if err != nil {
		t.Fatalf("ShortestPath(d,a): %v", err)
	}
	if got, want := path, []string{"d", "c", "b", "a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ShortestPath(d,a) = %v, want %v", got, want)
	}

	// undirected: a-b-c-e IS connected (e calls c)
	path, err = ShortestPath(st, "a", "e", 5)
	if err != nil {
		t.Fatalf("ShortestPath(a,e): %v", err)
	}
	if got, want := path, []string{"a", "b", "c", "e"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ShortestPath(a,e) = %v, want %v", got, want)
	}

	if _, err := ShortestPath(st, "nope", "a", 5); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("ShortestPath(unknown,a) err = %v, want ErrUnknownNode", err)
	}

	if path, err := ShortestPath(st, "a", "a", 5); err != nil || !reflect.DeepEqual(path, []string{"a"}) {
		t.Fatalf("self path: %v %v", path, err)
	}
}

func TestCallersCalleesFilterRelType(t *testing.T) {
	st := newFixture(t)
	callers, err := Callers(st, "b")
	if err != nil {
		t.Fatal(err)
	}
	// only the "calls" edge counts; the imports edge must be filtered.
	if got, want := callers, []string{"a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Callers(b) = %v, want %v", got, want)
	}
	callees, err := Callees(st, "b")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := callees, []string{"c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Callees(b) = %v, want %v", got, want)
	}
}

func TestContextAndExplain(t *testing.T) {
	st := newFixture(t)
	ctx, err := Context(st, "b", 10)
	if err != nil {
		t.Fatal(err)
	}
	if ctx["element"] == nil {
		t.Fatal("context must include the element")
	}
	// grouped lists are JSON-ready: map[string]any holding []any of QNs.
	assertList := func(m map[string]any, group, rel string, want []string) {
		g, ok := m[group].(map[string]any)
		if !ok {
			t.Fatalf("%s not a map: %T", group, m[group])
		}
		var got []string
		for _, v := range g[rel].([]any) {
			got = append(got, v.(string))
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s/%s = %v, want %v", group, rel, got, want)
		}
	}
	assertList(ctx, "outgoing", "calls", []string{"c"})
	assertList(ctx, "incoming", "calls", []string{"a"})
	assertList(ctx, "incoming", "imports", []string{"a"})

	// limit caps each grouped list (b has 2 distinct incoming callers).
	ctx2, err := Context(st, "b", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(ctx2["incoming"].(map[string]any)["calls"].([]any)); got != 1 {
		t.Fatalf("Context(b,1) incoming[calls] len = %d, want 1 (capped)", got)
	}

	ex, err := Explain(st, "b")
	if err != nil {
		t.Fatal(err)
	}
	// b has 2 incoming (calls + imports) and 1 outgoing.
	if ex["in_degree"].(int) != 2 || ex["out_degree"].(int) != 1 {
		t.Fatalf("explain degrees: %v", ex)
	}
	if ex["in_by_type"].(map[string]int)["calls"] != 1 || ex["in_by_type"].(map[string]int)["imports"] != 1 {
		t.Fatalf("in_by_type: %v", ex["in_by_type"])
	}
}

func TestDepthClamp(t *testing.T) {
	if clampDepth(0) != 2 || clampDepth(-3) != 2 || clampDepth(99) != 5 {
		t.Fatal("clamp broken")
	}
}
