package graph

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// newFixture: chain x→a→b→c→d plus e→c, and a non-calls edge (imports) that
// traversal filters must drop from callers/callees (but NOT from degree
// counts — Explain reports all edges).
func newFixture(t *testing.T) store.Backend {
	t.Helper()
	s, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(t.TempDir(), ".leankg", "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	els := make([]store.Element, 0, 6)
	for _, qn := range []string{"a", "b", "c", "d", "e", "x"} {
		els = append(els, store.Element{QualifiedName: qn, Name: qn, ElementType: "func", FilePath: qn + ".go"})
	}
	if err := s.UpsertElements(context.Background(), els); err != nil {
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
	if err := s.UpsertRelationships(context.Background(), rels); err != nil {
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

	hits, _, err := Impact(context.Background(), st, "a", 1)
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
	hits, _, err = Impact(context.Background(), st, "a", 5)
	if err != nil {
		t.Fatalf("Impact(a,5): %v", err)
	}
	if got, want := qns(hits), []string{"a", "x"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Impact(a,5) = %v, want %v", got, want)
	}

	// c: dependents b+e (d1), a (d2), x (d3).
	hits, _, _ = Impact(context.Background(), st, "c", 5)
	// depth-then-QN order: c(0), b+e(1), a(2), x(3)
	if got, want := qns(hits), []string{"c", "b", "e", "a", "x"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Impact(c,5) = %v, want %v", got, want)
	}

	if _, _, err := Impact(context.Background(), st, "nope", 2); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("Impact(unknown) err = %v, want ErrUnknownNode", err)
	}
}

func TestShortestPath(t *testing.T) {
	st := newFixture(t)

	path, _, err := ShortestPath(context.Background(), st, "a", "d", 5)
	if err != nil {
		t.Fatalf("ShortestPath(a,d): %v", err)
	}
	if got, want := path, []string{"a", "b", "c", "d"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ShortestPath(a,d) = %v, want %v", got, want)
	}

	// undirected view: reverse direction walks the same edges backwards
	path, _, err = ShortestPath(context.Background(), st, "d", "a", 5)
	if err != nil {
		t.Fatalf("ShortestPath(d,a): %v", err)
	}
	if got, want := path, []string{"d", "c", "b", "a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ShortestPath(d,a) = %v, want %v", got, want)
	}

	// undirected: a-b-c-e IS connected (e calls c)
	path, _, err = ShortestPath(context.Background(), st, "a", "e", 5)
	if err != nil {
		t.Fatalf("ShortestPath(a,e): %v", err)
	}
	if got, want := path, []string{"a", "b", "c", "e"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ShortestPath(a,e) = %v, want %v", got, want)
	}

	if _, _, err := ShortestPath(context.Background(), st, "nope", "a", 5); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("ShortestPath(unknown,a) err = %v, want ErrUnknownNode", err)
	}

	if path, _, err := ShortestPath(context.Background(), st, "a", "a", 5); err != nil || !reflect.DeepEqual(path, []string{"a"}) {
		t.Fatalf("self path: %v %v", path, err)
	}
}

func TestCallersCalleesFilterRelType(t *testing.T) {
	st := newFixture(t)
	callers, err := CallSites(context.Background(), st, []string{"b"}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	// only the "calls" edge counts; the imports edge must be filtered.
	if got, want := SiteEnds(callers), []string{"a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Callers(b) = %v, want %v", got, want)
	}
	callees, err := CallSites(context.Background(), st, []string{"b"}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := SiteEnds(callees), []string{"c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Callees(b) = %v, want %v", got, want)
	}
}

func TestContextAndExplain(t *testing.T) {
	st := newFixture(t)
	ctx, err := Context(context.Background(), st, "b", 10)
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
	ctx2, err := Context(context.Background(), st, "b", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(ctx2["incoming"].(map[string]any)["calls"].([]any)); got != 1 {
		t.Fatalf("Context(b,1) incoming[calls] len = %d, want 1 (capped)", got)
	}

	ex, err := Explain(context.Background(), st, "b")
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

// newSiteFixture: F references type T and constant K (signature and body
// uses, so references edges); G calls H; R references H only, from a signature
// mention (a references edge with no use line). Types and constants are
// non-callable seeds, functions are callable ones.
func newSiteFixture(t *testing.T) store.Backend {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenBackend(ctx, store.OpenOptions{DBPath: filepath.Join(t.TempDir(), ".leankg", "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := s.UpsertElements(ctx, []store.Element{
		{QualifiedName: "f.go::F", ElementType: "function", Name: "F", FilePath: "f.go", LineStart: 10},
		{QualifiedName: "g.go::G", ElementType: "function", Name: "G", FilePath: "g.go", LineStart: 20},
		{QualifiedName: "h.go::H", ElementType: "function", Name: "H", FilePath: "h.go", LineStart: 30},
		{QualifiedName: "r.go::R", ElementType: "function", Name: "R", FilePath: "r.go", LineStart: 40},
		{QualifiedName: "t.go::T", ElementType: "type", Name: "T", FilePath: "t.go", LineStart: 1},
		{QualifiedName: "k.go::K", ElementType: "constant", Name: "K", FilePath: "k.go", LineStart: 2},
	}); err != nil {
		t.Fatalf("upsert elements: %v", err)
	}
	if err := s.UpsertRelationships(ctx, []store.Relationship{
		{Source: "f.go::F", Target: "t.go::T", RelType: "references", Confidence: 0.5, Metadata: map[string]any{"line": float64(12)}},
		{Source: "f.go::F", Target: "k.go::K", RelType: "references", Confidence: 0.5, Metadata: map[string]any{"line": float64(13)}},
		{Source: "g.go::G", Target: "h.go::H", RelType: "calls", Confidence: 0.5, Metadata: map[string]any{"line": float64(22)}},
		{Source: "r.go::R", Target: "h.go::H", RelType: "references", Confidence: 0.5},
	}); err != nil {
		t.Fatalf("upsert relationships: %v", err)
	}
	return s
}

// A type seed's callers include the functions that reference it; a callable
// seed's callers stay call-only, so a reference-only user is not a caller.
func TestCallersOfTypeIncludeReferenceUsers(t *testing.T) {
	st := newSiteFixture(t)
	ctx := context.Background()

	typeCallers, err := CallSites(ctx, st, []string{"t.go::T"}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := SiteEnds(typeCallers), []string{"f.go::F"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Callers(T) = %v, want %v", got, want)
	}

	funcCallers, err := CallSites(ctx, st, []string{"h.go::H"}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := SiteEnds(funcCallers), []string{"g.go::G"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Callers(H) = %v, want %v (references-only user R must not appear)", got, want)
	}

	// Callees stay call-only: F's references to T and K are not callees.
	callees, err := CallSites(ctx, st, []string{"f.go::F"}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := SiteEnds(callees); len(got) != 0 {
		t.Fatalf("Callees(F) = %v, want none (references are not calls)", got)
	}
}

// A constant's site sits in the referencing function's file, at the line the
// reference edge carries in its metadata, and names its rel type.
func TestCallersOfConstantSiteFromReferenceEdge(t *testing.T) {
	st := newSiteFixture(t)
	sites, err := CallSites(context.Background(), st, []string{"k.go::K"}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []Site{{QN: "f.go::F", File: "f.go", Line: 13, Confidence: 0.5, RelType: "references"}}
	if !reflect.DeepEqual(sites, want) {
		t.Fatalf("Callers(K) sites = %+v, want %+v", sites, want)
	}
}

// An explicit rel_type wins over the per-seed default: calls on a type seed
// reads no edge, and references on a callable seed reads only the reference.
func TestCallersExplicitRelTypeWins(t *testing.T) {
	st := newSiteFixture(t)
	ctx := context.Background()

	none, err := CallSites(ctx, st, []string{"t.go::T"}, true, "calls")
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("Callers(T, rel=calls) = %+v, want none", none)
	}

	refs, err := CallSites(ctx, st, []string{"h.go::H"}, true, "references")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := SiteEnds(refs), []string{"r.go::R"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Callers(H, rel=references) = %v, want %v", got, want)
	}
}

// A callable seed's callers include a function that uses it as a value (a
// references edge with a use line, e.g. a method value passed as an argument),
// and a references edge with no line (a signature mention) is still excluded.
func TestCallersOfFunctionIncludeValueUses(t *testing.T) {
	st := newSiteFixture(t)
	ctx := context.Background()
	if err := st.UpsertRelationships(ctx, []store.Relationship{
		{Source: "f.go::F", Target: "h.go::H", RelType: "references", Confidence: 0.5, Metadata: map[string]any{"line": float64(14)}},
	}); err != nil {
		t.Fatalf("upsert relationships: %v", err)
	}

	sites, err := CallSites(ctx, st, []string{"h.go::H"}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := SiteEnds(sites), []string{"f.go::F", "g.go::G"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Callers(H) = %v, want %v (value use F and call G; signature mention R excluded)", got, want)
	}
	want := []Site{
		{QN: "f.go::F", File: "f.go", Line: 14, Confidence: 0.5, RelType: "references"},
		{QN: "g.go::G", File: "g.go", Line: 22, Confidence: 0.5, RelType: "calls"},
	}
	if !reflect.DeepEqual(sites, want) {
		t.Fatalf("Callers(H) sites = %+v, want %+v", sites, want)
	}
	if got, want := SiteRelTypes(sites), []string{"calls", "references"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Callers(H) rel_types = %v, want %v", got, want)
	}
}

// A method passed as a value from another file of its package (a method value
// in registerTools, server.go) is a references edge with a use line. The
// callers of the method seed must include it, and a signature mention of the
// same method (references with no line) must stay out.
func TestCallersOfMethodIncludeValueUseFromAnotherFile(t *testing.T) {
	st := newSiteFixture(t)
	ctx := context.Background()
	const (
		seed     = "internal/mcp/authenticate.go::Server.handleAuthenticate"
		register = "internal/mcp/server.go::Server.registerTools"
		sig      = "internal/mcp/authenticate.go::Server.handleAuthenticateSchema"
	)
	if err := st.UpsertElements(ctx, []store.Element{
		{QualifiedName: seed, ElementType: "method", Name: "handleAuthenticate", FilePath: "internal/mcp/authenticate.go", LineStart: 45},
		{QualifiedName: register, ElementType: "method", Name: "registerTools", FilePath: "internal/mcp/server.go", LineStart: 380},
		{QualifiedName: sig, ElementType: "method", Name: "handleAuthenticateSchema", FilePath: "internal/mcp/authenticate.go", LineStart: 60},
	}); err != nil {
		t.Fatalf("upsert elements: %v", err)
	}
	if err := st.UpsertRelationships(ctx, []store.Relationship{
		{Source: register, Target: seed, RelType: "references", Confidence: 0.4, Metadata: map[string]any{"line": float64(463), "resolution": "method-by-name"}},
		{Source: sig, Target: seed, RelType: "references", Confidence: 0.5},
	}); err != nil {
		t.Fatalf("upsert relationships: %v", err)
	}

	sites, err := CallSites(ctx, st, []string{seed}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []Site{{QN: register, File: "internal/mcp/server.go", Line: 463, Confidence: 0.4, RelType: "references"}}
	if !reflect.DeepEqual(sites, want) {
		t.Fatalf("Callers(handleAuthenticate) sites = %+v, want %+v", sites, want)
	}
}

// A type's own methods mentioning the type are not external users: references
// from a method whose receiver is the type are dropped, while references from
// another type's method, from an interface (a type element) and from a
// function are kept.
func TestCallersOfTypeExcludeOwnMethods(t *testing.T) {
	st := newSiteFixture(t)
	ctx := context.Background()
	if err := st.UpsertElements(ctx, []store.Element{
		{QualifiedName: "t.go::T.Clone", ElementType: "method", Name: "Clone", FilePath: "t.go", LineStart: 5},
		{QualifiedName: "u.go::U.Wrap", ElementType: "method", Name: "Wrap", FilePath: "u.go", LineStart: 7},
		{QualifiedName: "i.go::I", ElementType: "type", Name: "I", FilePath: "i.go", LineStart: 3},
	}); err != nil {
		t.Fatalf("upsert elements: %v", err)
	}
	if err := st.UpsertRelationships(ctx, []store.Relationship{
		{Source: "t.go::T.Clone", Target: "t.go::T", RelType: "references", Confidence: 0.5, Metadata: map[string]any{"line": float64(6)}},
		{Source: "u.go::U.Wrap", Target: "t.go::T", RelType: "references", Confidence: 0.5, Metadata: map[string]any{"line": float64(8)}},
		{Source: "i.go::I", Target: "t.go::T", RelType: "references", Confidence: 0.5},
	}); err != nil {
		t.Fatalf("upsert relationships: %v", err)
	}

	sites, err := CallSites(ctx, st, []string{"t.go::T"}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := SiteEnds(sites), []string{"f.go::F", "i.go::I", "u.go::U.Wrap"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Callers(T) = %v, want %v (T.Clone is the type's own method and must not appear)", got, want)
	}
}

// A Go method may be declared in another file of its type's package. Its
// references to the type are the type's own methods and must be dropped too.
func TestCallersOfTypeDropOtherFileMethodsInSameDirectory(t *testing.T) {
	st := newSiteFixture(t)
	ctx := context.Background()
	if err := st.UpsertElements(ctx, []store.Element{
		{QualifiedName: "m.go::T.M", ElementType: "method", Name: "M", FilePath: "m.go", LineStart: 5},
	}); err != nil {
		t.Fatalf("upsert elements: %v", err)
	}
	if err := st.UpsertRelationships(ctx, []store.Relationship{
		{Source: "m.go::T.M", Target: "t.go::T", RelType: "references", Confidence: 0.5, Metadata: map[string]any{"line": float64(6)}},
	}); err != nil {
		t.Fatalf("upsert relationships: %v", err)
	}

	sites, err := CallSites(ctx, st, []string{"t.go::T"}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := SiteEnds(sites), []string{"f.go::F"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Callers(T) = %v, want %v (T.M is declared in m.go, the type's own package directory, and must not appear)", got, want)
	}
}

// A type with the same name in another directory is another package's type, so
// its methods stay external callers.
func TestCallersOfSameNamedTypeInOtherDirectoryAreKept(t *testing.T) {
	st := newSiteFixture(t)
	ctx := context.Background()
	if err := st.UpsertElements(ctx, []store.Element{
		{QualifiedName: "pkg/x.go::T.N", ElementType: "method", Name: "N", FilePath: "pkg/x.go", LineStart: 5},
	}); err != nil {
		t.Fatalf("upsert elements: %v", err)
	}
	if err := st.UpsertRelationships(ctx, []store.Relationship{
		{Source: "pkg/x.go::T.N", Target: "t.go::T", RelType: "references", Confidence: 0.5, Metadata: map[string]any{"line": float64(6)}},
	}); err != nil {
		t.Fatalf("upsert relationships: %v", err)
	}

	sites, err := CallSites(ctx, st, []string{"t.go::T"}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := SiteEnds(sites), []string{"f.go::F", "pkg/x.go::T.N"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Callers(T) = %v, want %v (pkg/x.go::T.N is a different package's T and must stay)", got, want)
	}
}

// A call edge with no use line still makes its caller a caller, but there is no
// use line to list. The site must not take the caller's definition line
// (x.go::X starts at line 5) as a call line.
func TestCallersWithoutUseLineCountButListNoSite(t *testing.T) {
	st := newSiteFixture(t)
	ctx := context.Background()
	const caller = "x.go::X"
	if err := st.UpsertElements(ctx, []store.Element{
		{QualifiedName: caller, ElementType: "function", Name: "X", FilePath: "x.go", LineStart: 5},
	}); err != nil {
		t.Fatalf("upsert elements: %v", err)
	}
	if err := st.UpsertRelationships(ctx, []store.Relationship{
		{Source: caller, Target: "h.go::H", RelType: "calls", Confidence: 0.5},
	}); err != nil {
		t.Fatalf("upsert relationships: %v", err)
	}

	sites, err := CallSites(ctx, st, []string{"h.go::H"}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sites {
		if s.QN == caller && s.Line != noUseLine {
			t.Fatalf("CallSites gives %s line %d, want no use line (%d) for an edge with none", caller, s.Line, noUseLine)
		}
	}
	for _, s := range LinedSites(sites) {
		if s.QN == caller {
			t.Fatalf("LinedSites lists %s at line %d, want it left out of the site list", caller, s.Line)
		}
	}

	seeds := []string{"h.go::H"}
	bySeed, err := SitesBySeed(ctx, st, seeds, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := SiteEnds(UnionSites(seeds, bySeed)); !reflect.DeepEqual(got, []string{"g.go::G", caller}) {
		t.Fatalf("Callers(H) = %v, want %v (the caller counts without a use line)", got, []string{"g.go::G", caller})
	}
}

// A caller that uses a callee on several lines carries every use line in the
// edge's "lines" metadata (unsorted, with a repeat). CallSites emits one site per
// distinct line, all at the caller, in ascending line order.
func TestCallSitesOnePerCallLine(t *testing.T) {
	st := newSiteFixture(t)
	ctx := context.Background()
	if err := st.UpsertElements(ctx, []store.Element{
		{QualifiedName: "m.go::M", ElementType: "function", Name: "M", FilePath: "m.go", LineStart: 50},
	}); err != nil {
		t.Fatalf("upsert elements: %v", err)
	}
	if err := st.UpsertRelationships(ctx, []store.Relationship{
		{Source: "m.go::M", Target: "h.go::H", RelType: "calls", Confidence: 0.5,
			Metadata: map[string]any{"line": float64(40), "lines": []any{float64(40), float64(25), float64(31), float64(25)}}},
	}); err != nil {
		t.Fatalf("upsert relationships: %v", err)
	}

	sites, err := CallSites(ctx, st, []string{"h.go::H"}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []Site{
		{QN: "g.go::G", File: "g.go", Line: 22, Confidence: 0.5, RelType: "calls"},
		{QN: "m.go::M", File: "m.go", Line: 25, Confidence: 0.5, RelType: "calls"},
		{QN: "m.go::M", File: "m.go", Line: 31, Confidence: 0.5, RelType: "calls"},
		{QN: "m.go::M", File: "m.go", Line: 40, Confidence: 0.5, RelType: "calls"},
	}
	if !reflect.DeepEqual(sites, want) {
		t.Fatalf("Callers(H) sites = %+v, want one site per call line %+v", sites, want)
	}
	if got, want := SiteEnds(sites), []string{"g.go::G", "m.go::M"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Callers(H) = %v, want %v (callers stay unique by caller)", got, want)
	}
}

// Context on a package lists its importers as before. The summary splits them
// into non-test and test-only importers, and names the test-only ones. A package
// with no incoming imports carries neither key.
func TestContextSplitsTestOnlyImporters(t *testing.T) {
	st := newFixture(t)
	ctx := context.Background()
	if err := st.UpsertElements(ctx, []store.Element{
		{QualifiedName: "p/store", ElementType: "package", Name: "store", FilePath: "p/store", Language: "go"},
		{QualifiedName: "cmd/app", ElementType: "package", Name: "app", FilePath: "cmd/app", Language: "go"},
		{QualifiedName: "internal/mcp", ElementType: "package", Name: "mcp", FilePath: "internal/mcp", Language: "go"},
		{QualifiedName: "internal/doctor", ElementType: "package", Name: "doctor", FilePath: "internal/doctor", Language: "go"},
	}); err != nil {
		t.Fatalf("upsert elements: %v", err)
	}
	if err := st.UpsertRelationships(ctx, []store.Relationship{
		{Source: "cmd/app", Target: "p/store", RelType: "imports", Confidence: 1},
		{Source: "internal/mcp", Target: "p/store", RelType: "imports", Confidence: 1, Metadata: map[string]any{"test_only": true}},
		{Source: "internal/doctor", Target: "p/store", RelType: "imports", Confidence: 1, Metadata: map[string]any{"test_only": true}},
	}); err != nil {
		t.Fatalf("upsert relationships: %v", err)
	}

	out, err := Context(ctx, st, "p/store", 0)
	if err != nil {
		t.Fatal(err)
	}
	in := out["incoming"].(map[string]any)
	if got, want := in["imports"].([]any), []any{"cmd/app", "internal/doctor", "internal/mcp"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("incoming.imports = %v, want every importer %v", got, want)
	}
	if got, want := out["incoming_imports_summary"], map[string]int{"non_test": 1, "test_only": 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("incoming_imports_summary = %v, want %v", got, want)
	}
	if got, want := out["incoming_imports_test_only"], []string{"internal/doctor", "internal/mcp"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("incoming_imports_test_only = %v, want %v", got, want)
	}

	plain, err := Context(ctx, st, "a", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"incoming_imports_summary", "incoming_imports_test_only"} {
		if _, ok := plain[key]; ok {
			t.Fatalf("%s present on an element with no incoming imports: %v", key, plain[key])
		}
	}
}
