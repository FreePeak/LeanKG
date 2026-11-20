package core

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/graph"
	"github.com/FreePeak/LeanKG/internal/store"
)

const (
	iwSpec     = "internal/store/roles.go::IndexWriter.UpsertElements"
	prdSpec    = "internal/prdindex/prdindex.go::prdStore.UpsertElements"
	lspSpec    = "internal/lsp/enrich.go::enrichStore.UpsertElements"
	storeImpl  = "internal/store/elements.go::Store.UpsertElements"
	pgImpl     = "internal/store/store_pg.go::PGStore.UpsertElements"
	convoCall  = "internal/convo/convo.go::IndexItems"
	prdCall    = "internal/prdindex/prdindex.go::Index"
	lspCall    = "internal/lsp/enrich.go::Bridge.Enrich"
	testIface  = "internal/store/store_test.go::TestIface"
	testDirect = "internal/store/store_test.go::TestUpsert"
	mdCaller   = "docs/design.md::Upsert"
)

// seedIfaceGraph wires one interface method reached through three local
// interface specs (IndexWriter, prdStore, enrichStore) and two concrete
// implementers. Each caller links to its own spec (interface-spec) and to the
// implementers it may dispatch to (interface-dispatch). TestUpsert calls
// Store.UpsertElements directly. mdCaller is a document that reaches only the
// IndexWriter spec.
func seedIfaceGraph(t *testing.T, e *Engine) {
	t.Helper()
	ctx := context.Background()
	spec := map[string]any{"interface_spec": true}
	if err := e.st.UpsertElements(ctx, []store.Element{
		{QualifiedName: iwSpec, ElementType: "method", Name: "UpsertElements", FilePath: "internal/store/roles.go", LineStart: 10, Language: "go", Metadata: spec},
		{QualifiedName: prdSpec, ElementType: "method", Name: "UpsertElements", FilePath: "internal/prdindex/prdindex.go", LineStart: 11, Language: "go", Metadata: spec},
		{QualifiedName: lspSpec, ElementType: "method", Name: "UpsertElements", FilePath: "internal/lsp/enrich.go", LineStart: 12, Language: "go", Metadata: spec},
		{QualifiedName: storeImpl, ElementType: "method", Name: "UpsertElements", FilePath: "internal/store/elements.go", LineStart: 20, Language: "go"},
		{QualifiedName: pgImpl, ElementType: "method", Name: "UpsertElements", FilePath: "internal/store/store_pg.go", LineStart: 30, Language: "go"},
		{QualifiedName: convoCall, ElementType: "function", Name: "IndexItems", FilePath: "internal/convo/convo.go", LineStart: 5, Language: "go"},
		{QualifiedName: prdCall, ElementType: "function", Name: "Index", FilePath: "internal/prdindex/prdindex.go", LineStart: 5, Language: "go"},
		{QualifiedName: lspCall, ElementType: "method", Name: "Enrich", FilePath: "internal/lsp/enrich.go", LineStart: 5, Language: "go"},
		{QualifiedName: testIface, ElementType: "function", Name: "TestIface", FilePath: "internal/store/store_test.go", LineStart: 40, Language: "go"},
		{QualifiedName: testDirect, ElementType: "function", Name: "TestUpsert", FilePath: "internal/store/store_test.go", LineStart: 60, Language: "go"},
		{QualifiedName: mdCaller, ElementType: "section", Name: "Upsert", FilePath: "docs/design.md", LineStart: 3},
	}); err != nil {
		t.Fatal(err)
	}
	via := func(resolution string, line float64) map[string]any {
		return map[string]any{"resolution": resolution, "line": line}
	}
	if err := e.st.UpsertRelationships(context.Background(), []store.Relationship{
		{Source: convoCall, Target: iwSpec, RelType: "calls", Confidence: 0.3, Metadata: via("interface-spec", 215)},
		{Source: convoCall, Target: storeImpl, RelType: "calls", Confidence: 0.3, Metadata: via("interface-dispatch", 215)},
		{Source: convoCall, Target: pgImpl, RelType: "calls", Confidence: 0.3, Metadata: via("interface-dispatch", 215)},
		{Source: prdCall, Target: prdSpec, RelType: "calls", Confidence: 0.3, Metadata: via("interface-spec", 446)},
		{Source: prdCall, Target: storeImpl, RelType: "calls", Confidence: 0.3, Metadata: via("interface-dispatch", 446)},
		{Source: prdCall, Target: pgImpl, RelType: "calls", Confidence: 0.3, Metadata: via("interface-dispatch", 446)},
		{Source: lspCall, Target: lspSpec, RelType: "calls", Confidence: 0.3, Metadata: via("interface-spec", 88)},
		{Source: lspCall, Target: storeImpl, RelType: "calls", Confidence: 0.3, Metadata: via("interface-dispatch", 88)},
		{Source: lspCall, Target: pgImpl, RelType: "calls", Confidence: 0.3, Metadata: via("interface-dispatch", 88)},
		{Source: testIface, Target: iwSpec, RelType: "calls", Confidence: 0.3, Metadata: via("interface-spec", 44)},
		{Source: testIface, Target: storeImpl, RelType: "calls", Confidence: 0.3, Metadata: via("interface-dispatch", 44)},
		{Source: testIface, Target: pgImpl, RelType: "calls", Confidence: 0.3, Metadata: via("interface-dispatch", 44)},
		{Source: testDirect, Target: storeImpl, RelType: "calls", Confidence: 0.4, Metadata: via("method-by-name", 62)},
		{Source: mdCaller, Target: iwSpec, RelType: "calls", Confidence: 0.3, Metadata: via("interface-spec", 3)},
	}); err != nil {
		t.Fatal(err)
	}
}

func callersOf(t *testing.T, e *Engine, qn string, args map[string]any) map[string]any {
	t.Helper()
	out, err := e.Query(context.Background(), QueryRequest{Action: "callers", Query: qn, Args: args})
	if err != nil {
		t.Fatalf("callers %s: %v", qn, err)
	}
	return out
}

// TestCallersOfSpecUnionsItsImplementerFamily pins B1: callers of an interface
// spec reach every caller that dispatches to the same implementers, through
// any local spec of the name, and the seeds say which expansion was used.
func TestCallersOfSpecUnionsItsImplementerFamily(t *testing.T) {
	e, _ := newEngine(t)
	seedIfaceGraph(t, e)

	out := callersOf(t, e, iwSpec, nil)
	wantSeeds := []string{iwSpec, storeImpl, pgImpl, lspSpec, prdSpec}
	if got := out["seeds"]; !reflect.DeepEqual(got, wantSeeds) {
		t.Errorf("seeds = %v, want %v", got, wantSeeds)
	}
	wantCallers := []string{mdCaller, convoCall, lspCall, prdCall, testIface, testDirect}
	if got := out["callers"]; !reflect.DeepEqual(got, wantCallers) {
		t.Errorf("callers = %v, want %v", got, wantCallers)
	}
	counts, _ := out["seed_counts"].(map[string]int)
	if counts[iwSpec] != 3 || counts[storeImpl] != 5 || counts[lspSpec] != 1 || counts[prdSpec] != 1 {
		t.Errorf("seed_counts = %v, want iw=3 store=5 lsp=1 prd=1", counts)
	}
}

// TestCallersOfConcreteSeedIsNotWidened pins that a concrete method stays one
// seed: its callers are the ones linked to it, and no spec's own callers join.
func TestCallersOfConcreteSeedIsNotWidened(t *testing.T) {
	e, _ := newEngine(t)
	seedIfaceGraph(t, e)

	out := callersOf(t, e, storeImpl, nil)
	if got, want := out["seeds"], []string{storeImpl}; !reflect.DeepEqual(got, want) {
		t.Errorf("seeds = %v, want %v", got, want)
	}
	wantCallers := []string{convoCall, lspCall, prdCall, testIface, testDirect}
	if got := out["callers"]; !reflect.DeepEqual(got, wantCallers) {
		t.Errorf("callers = %v, want %v", got, wantCallers)
	}
}

// TestShapeElementFlagsInterfaceSpecs pins B2: a spec element is marked, and the
// flag stays on a wide (non-compact) hit, because it changes what the element is.
func TestShapeElementFlagsInterfaceSpecs(t *testing.T) {
	spec := store.Element{QualifiedName: iwSpec, ElementType: "method", Name: "UpsertElements", Metadata: map[string]any{"interface_spec": true}}
	impl := store.Element{QualifiedName: storeImpl, ElementType: "method", Name: "UpsertElements"}
	for _, limit := range []int{compactHitLimit, 10} {
		if got := shapeElement(spec, limit)["interface_spec"]; got != true {
			t.Errorf("limit %d: spec interface_spec = %v, want true", limit, got)
		}
		if _, ok := shapeElement(impl, limit)["interface_spec"]; ok {
			t.Errorf("limit %d: implementer carries interface_spec, want the key omitted", limit)
		}
	}
}

// TestExactHitsSeparateSpecsFromImplementers pins B2 end to end: an exact
// search reports the spec elements as specs and the implementers without the flag.
func TestExactHitsSeparateSpecsFromImplementers(t *testing.T) {
	e, _ := newEngine(t)
	seedIfaceGraph(t, e)

	out, err := e.Query(context.Background(), QueryRequest{Action: "exact", Query: "UpsertElements"})
	if err != nil {
		t.Fatal(err)
	}
	flags := map[string]any{}
	hits, _ := out["hits"].([]map[string]any)
	for _, h := range hits {
		flags[h["qualified_name"].(string)] = h["interface_spec"]
	}
	for _, spec := range []string{iwSpec, prdSpec, lspSpec} {
		if flags[spec] != true {
			t.Errorf("%s interface_spec = %v, want true", spec, flags[spec])
		}
	}
	if v, ok := flags[storeImpl]; !ok || v != nil {
		t.Errorf("%s carries interface_spec = %v, want the key omitted", storeImpl, v)
	}
}

// TestCallersScopeProdDropsTestCallers pins B3: scope=prod removes the test
// callers, echoes the applied scope, and reports how many sites it dropped.
func TestCallersScopeProdDropsTestCallers(t *testing.T) {
	e, _ := newEngine(t)
	seedIfaceGraph(t, e)

	all := callersOf(t, e, iwSpec, nil)
	prod := callersOf(t, e, iwSpec, map[string]any{"scope": "prod"})
	if got, want := prod["scope"], "prod"; got != want {
		t.Errorf("scope = %v, want %v", got, want)
	}
	wantCallers := []string{mdCaller, convoCall, lspCall, prdCall}
	if got := prod["callers"]; !reflect.DeepEqual(got, wantCallers) {
		t.Errorf("prod callers = %v, want %v", got, wantCallers)
	}
	allSites, _ := all["total_sites"].(int)
	prodSites, _ := prod["total_sites"].(int)
	dropped, _ := prod["filtered_out"].(int)
	if prodSites >= allSites || dropped != allSites-prodSites || dropped == 0 {
		t.Errorf("total_sites %d -> %d, filtered_out = %d; want filtered_out = the difference", allSites, prodSites, dropped)
	}
}

// TestCallersScopeCodeDropsDocumentCallers pins that scope=code is the other
// half: a document caller goes, the code callers stay.
func TestCallersScopeCodeDropsDocumentCallers(t *testing.T) {
	e, _ := newEngine(t)
	seedIfaceGraph(t, e)

	code := callersOf(t, e, iwSpec, map[string]any{"scope": "code"})
	if got, want := code["callers"], []string{convoCall, lspCall, prdCall, testIface, testDirect}; !reflect.DeepEqual(got, want) {
		t.Errorf("code callers = %v, want %v", got, want)
	}
}

// TestCallersScopeRejectsUnknownValue pins that a bogus scope gets the search
// path's error text, not a silent full answer.
func TestCallersScopeRejectsUnknownValue(t *testing.T) {
	e, _ := newEngine(t)
	seedIfaceGraph(t, e)

	_, err := e.Query(context.Background(), QueryRequest{Action: "callers", Query: iwSpec, Args: map[string]any{"scope": "bogus"}})
	want := `unknown scope "bogus" (valid: all, code, prod)`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want it to contain %q", err, want)
	}
}

// TestCallersDefaultScopeIsAll pins (iv): with no scope the answer says all
// and drops nothing.
func TestCallersDefaultScopeIsAll(t *testing.T) {
	e, _ := newEngine(t)
	seedIfaceGraph(t, e)

	out := callersOf(t, e, iwSpec, nil)
	if got, want := out["scope"], "all"; got != want {
		t.Errorf("scope = %v, want %v", got, want)
	}
	if got, want := out["filtered_out"], 0; got != want {
		t.Errorf("filtered_out = %v, want %v", got, want)
	}
}

// TestImpactScopeProdDropsTestHits pins B3 for impact: scope=prod keeps test
// files out of the blast radius and says how many nodes it dropped.
func TestImpactScopeProdDropsTestHits(t *testing.T) {
	e, _ := newEngine(t)
	seedIfaceGraph(t, e)

	out, err := e.Query(context.Background(), QueryRequest{Action: "impact", Query: storeImpl, Args: map[string]any{"scope": "prod"}})
	if err != nil {
		t.Fatal(err)
	}
	hits, _ := out["hits"].([]graph.Hit)
	for _, h := range hits {
		if file, _, _ := strings.Cut(h.QN, "::"); isTestPath(file) {
			t.Errorf("prod impact kept test hit %s", h.QN)
		}
	}
	if got, want := out["scope"], "prod"; got != want {
		t.Errorf("scope = %v, want %v", got, want)
	}
	if dropped, _ := out["filtered_out"].(int); dropped == 0 {
		t.Errorf("filtered_out = 0, want the test hits counted")
	}
}
