package core

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

const (
	storeReplace  = "internal/store/store.go::Store.ReplaceFileRelationships"
	pgReplace     = "internal/store/pgstore.go::PGStore.ReplaceFileRelationships"
	indexFileCall = "internal/index/index.go::IndexFile"
	indexPGCall   = "internal/index/pgindex.go::IndexPG"
)

// seedSameNamedMethods wires two same-named methods on different types, each
// with one distinct caller. callMeta is the metadata on the IndexFile edge.
func seedSameNamedMethods(t *testing.T, e *Engine, callMeta map[string]any) {
	t.Helper()
	ctx := context.Background()
	if err := e.st.UpsertElements(ctx, []store.Element{
		{QualifiedName: storeReplace, ElementType: "method", Name: "ReplaceFileRelationships", FilePath: "internal/store/store.go", LineStart: 10, Language: "go"},
		{QualifiedName: pgReplace, ElementType: "method", Name: "ReplaceFileRelationships", FilePath: "internal/store/pgstore.go", LineStart: 20, Language: "go"},
		{QualifiedName: indexFileCall, ElementType: "function", Name: "IndexFile", FilePath: "internal/index/index.go", LineStart: 40, Language: "go"},
		{QualifiedName: indexPGCall, ElementType: "function", Name: "IndexPG", FilePath: "internal/index/pgindex.go", LineStart: 55, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.UpsertRelationships(ctx, []store.Relationship{
		{Source: indexFileCall, Target: storeReplace, RelType: "calls", Confidence: 1, Metadata: callMeta},
		{Source: indexPGCall, Target: pgReplace, RelType: "calls", Confidence: 1},
	}); err != nil {
		t.Fatal(err)
	}
}

// TestCallersUnionsSameNamedMethods pins the bare-name seed: a name shared by
// Store.X and PGStore.X resolves to both, and callers answers for each.
//
// The defect: resolveGraphSeed kept only the shortest qualified name, so the
// PGStore caller was dropped and the agent needed a second query per type.
func TestCallersUnionsSameNamedMethods(t *testing.T) {
	e, _ := newEngine(t)
	seedSameNamedMethods(t, e, nil)

	out, err := e.Query(context.Background(), QueryRequest{Action: "callers", Query: "ReplaceFileRelationships"})
	if err != nil {
		t.Fatal(err)
	}
	wantSeeds := []string{storeReplace, pgReplace}
	if got := out["seeds"]; !reflect.DeepEqual(got, wantSeeds) {
		t.Errorf("seeds = %v, want %v", got, wantSeeds)
	}
	wantCallers := []string{indexFileCall, indexPGCall}
	if got := out["callers"]; !reflect.DeepEqual(got, wantCallers) {
		t.Errorf("callers = %v, want both same-named methods' callers %v", got, wantCallers)
	}
	wantSites := []string{
		"internal/index/index.go:40 " + indexFileCall + " 1",
		"internal/index/pgindex.go:55 " + indexPGCall + " 1",
	}
	if got := out["sites"]; !reflect.DeepEqual(got, wantSites) {
		t.Errorf("sites = %v, want %v", got, wantSites)
	}
}

// TestCallersQualifiedSeedResolvesOneSeed pins that a qualified name is not
// widened: it still resolves to exactly the one element it names.
func TestCallersQualifiedSeedResolvesOneSeed(t *testing.T) {
	e, _ := newEngine(t)
	seedSameNamedMethods(t, e, nil)

	out, err := e.Query(context.Background(), QueryRequest{Action: "callers", Query: storeReplace})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := out["seeds"], []string{storeReplace}; !reflect.DeepEqual(got, want) {
		t.Errorf("seeds = %v, want %v", got, want)
	}
	if got, want := out["callers"], []string{indexFileCall}; !reflect.DeepEqual(got, want) {
		t.Errorf("callers = %v, want %v", got, want)
	}
}

// TestCallersSiteLineFromEdgeMetadata pins the call-site line: when a calls
// edge carries metadata "line", the site uses it instead of the caller's
// line_start.
func TestCallersSiteLineFromEdgeMetadata(t *testing.T) {
	e, _ := newEngine(t)
	seedSameNamedMethods(t, e, map[string]any{"line": float64(47)})

	out, err := e.Query(context.Background(), QueryRequest{Action: "callers", Query: storeReplace})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"internal/index/index.go:47 " + indexFileCall + " 1"}
	if got := out["sites"]; !reflect.DeepEqual(got, want) {
		t.Errorf("sites = %v, want %v", got, want)
	}
}

// TestCalleesSitesAtTheSeedFile pins the mirror for callees: the call happens
// inside the seed, so a site sits in the seed's file, at the seed's line_start
// unless edge metadata says otherwise.
func TestCalleesSitesAtTheSeedFile(t *testing.T) {
	e, _ := newEngine(t)
	seedSameNamedMethods(t, e, nil)

	out, err := e.Query(context.Background(), QueryRequest{Action: "callees", Query: indexFileCall})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := out["callees"], []string{storeReplace}; !reflect.DeepEqual(got, want) {
		t.Errorf("callees = %v, want %v", got, want)
	}
	want := []string{"internal/index/index.go:40 " + storeReplace + " 1"}
	if got := out["sites"]; !reflect.DeepEqual(got, want) {
		t.Errorf("sites = %v, want %v", got, want)
	}
}

// seedRefGraph wires a type T referenced by F (a references edge with a call
// site line), a function H called by G, and R which only references H.
func seedRefGraph(t *testing.T, e *Engine) {
	t.Helper()
	ctx := context.Background()
	if err := e.st.UpsertElements(ctx, []store.Element{
		{QualifiedName: "internal/graph/graph.go::Impact", ElementType: "function", Name: "Impact", FilePath: "internal/graph/graph.go", LineStart: 80, Language: "go"},
		{QualifiedName: "internal/graph/graph.go::Bounds", ElementType: "type", Name: "Bounds", FilePath: "internal/graph/graph.go", LineStart: 20, Language: "go"},
		{QualifiedName: "internal/core/core.go::Run", ElementType: "function", Name: "Run", FilePath: "internal/core/core.go", LineStart: 5, Language: "go"},
		{QualifiedName: "internal/core/core.go::Helper", ElementType: "function", Name: "Helper", FilePath: "internal/core/core.go", LineStart: 30, Language: "go"},
		{QualifiedName: "internal/core/core.go::Reader", ElementType: "function", Name: "Reader", FilePath: "internal/core/core.go", LineStart: 60, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.UpsertRelationships(ctx, []store.Relationship{
		{Source: "internal/graph/graph.go::Impact", Target: "internal/graph/graph.go::Bounds", RelType: "references", Confidence: 0.5, Metadata: map[string]any{"line": float64(94)}},
		{Source: "internal/core/core.go::Run", Target: "internal/core/core.go::Helper", RelType: "calls", Confidence: 0.5, Metadata: map[string]any{"line": float64(7)}},
		{Source: "internal/core/core.go::Reader", Target: "internal/core/core.go::Helper", RelType: "references", Confidence: 0.5},
	}); err != nil {
		t.Fatal(err)
	}
}

// TestCallersOfTypeReportReferenceSites pins that callers of a type answer
// with the functions that reference it, each site at the reference line, and
// that rel_types names the relationship the sites were read through.
func TestCallersOfTypeReportReferenceSites(t *testing.T) {
	e, _ := newEngine(t)
	seedRefGraph(t, e)

	out, err := e.Query(context.Background(), QueryRequest{Action: "callers", Query: "internal/graph/graph.go::Bounds"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := out["callers"], []string{"internal/graph/graph.go::Impact"}; !reflect.DeepEqual(got, want) {
		t.Errorf("callers = %v, want %v", got, want)
	}
	if got, want := out["sites"], []string{"internal/graph/graph.go:94 internal/graph/graph.go::Impact 0.5"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sites = %v, want %v", got, want)
	}
	if got, want := out["rel_types"], []string{"references"}; !reflect.DeepEqual(got, want) {
		t.Errorf("rel_types = %v, want %v", got, want)
	}
}

// TestCallersOfFunctionStayCallOnly pins that a callable seed keeps the
// call-graph answer: a reference-only user is not a caller.
func TestCallersOfFunctionStayCallOnly(t *testing.T) {
	e, _ := newEngine(t)
	seedRefGraph(t, e)

	out, err := e.Query(context.Background(), QueryRequest{Action: "callers", Query: "internal/core/core.go::Helper"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := out["callers"], []string{"internal/core/core.go::Run"}; !reflect.DeepEqual(got, want) {
		t.Errorf("callers = %v, want %v", got, want)
	}
	if got, want := out["rel_types"], []string{"calls"}; !reflect.DeepEqual(got, want) {
		t.Errorf("rel_types = %v, want %v", got, want)
	}
}

// TestCallersExplicitRelTypeWinsOverDefault pins that an explicit rel_type arg
// replaces the per-seed default: calls on a type seed is empty.
func TestCallersExplicitRelTypeWinsOverDefault(t *testing.T) {
	e, _ := newEngine(t)
	seedRefGraph(t, e)

	out, err := e.Query(context.Background(), QueryRequest{
		Action: "callers", Query: "internal/graph/graph.go::Bounds", Args: map[string]any{"rel_type": "calls"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := out["callers"].([]string); len(got) != 0 {
		t.Errorf("callers with rel_type=calls = %v, want none", got)
	}
}

// fanIn wires n distinct callers, each in its own production file, to target.
// It returns the caller qualified names in sorted order.
func fanIn(t *testing.T, e *Engine, target, name, file string, callers int, prefix string) []string {
	t.Helper()
	ctx := context.Background()
	var els []store.Element
	var rels []store.Relationship
	var qns []string
	for i := 0; i < callers; i++ {
		qn := fmt.Sprintf("%s%d.go::%s%d", prefix, i, name, i)
		qns = append(qns, qn)
		els = append(els, store.Element{QualifiedName: qn, ElementType: "function", Name: fmt.Sprintf("%s%d", name, i), FilePath: fmt.Sprintf("%s%d.go", prefix, i), LineStart: 10 + i, Language: "go"})
		rels = append(rels, store.Relationship{Source: qn, Target: target, RelType: "calls", Confidence: 1})
	}
	if err := e.st.UpsertElements(ctx, append(els, store.Element{QualifiedName: target, ElementType: "function", Name: name, FilePath: file, LineStart: 1, Language: "go"})); err != nil {
		t.Fatal(err)
	}
	if err := e.st.UpsertRelationships(ctx, rels); err != nil {
		t.Fatal(err)
	}
	return qns
}

// TestCallersSitesPerCallLine pins the lines contract through Query: an edge
// that carries every call-site line yields one site per line, at the caller.
func TestCallersSitesPerCallLine(t *testing.T) {
	e, _ := newEngine(t)
	seedSameNamedMethods(t, e, map[string]any{"lines": []any{float64(51), float64(47)}})

	out, err := e.Query(context.Background(), QueryRequest{Action: "callers", Query: storeReplace})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"internal/index/index.go:47 " + indexFileCall + " 1", "internal/index/index.go:51 " + indexFileCall + " 1"}
	if got := out["sites"]; !reflect.DeepEqual(got, want) {
		t.Errorf("sites = %v, want %v", got, want)
	}
	if got, want := out["callers"], []string{indexFileCall}; !reflect.DeepEqual(got, want) {
		t.Errorf("callers = %v, want %v (unique by caller)", got, want)
	}
}

// TestCallersUnknownSeedAnswersWithDidYouMean pins the unknown-seed answer: a
// seed that names no indexed element is an answer with resolved=false, the
// candidates found by name on its last segment, and guidance naming the seed.
// The seed here has the right name under the wrong path and type prefix.
func TestCallersUnknownSeedAnswersWithDidYouMean(t *testing.T) {
	e, _ := newEngine(t)
	seedSameNamedMethods(t, e, nil)

	for _, verb := range []string{"callers", "callees"} {
		out, err := e.Query(context.Background(), QueryRequest{Action: verb, Query: "internal/store/store.go::ReplaceFileRelationships"})
		if err != nil {
			t.Fatalf("%s on an unknown seed must answer, not error: %v", verb, err)
		}
		if got := out["resolved"]; got != false {
			t.Errorf("%s resolved = %v, want false", verb, got)
		}
		if verb == "callers" {
			if got, want := out["did_you_mean"], []string{storeReplace, pgReplace}; !reflect.DeepEqual(got, want) {
				t.Errorf("did_you_mean = %v, want %v", got, want)
			}
		}
		guidance, _ := out["guidance"].(string)
		if !strings.Contains(guidance, "internal/store/store.go::ReplaceFileRelationships") {
			t.Errorf("%s guidance must name the unresolved seed, got %q", verb, guidance)
		}
	}
}

// TestCallersKnownSeedWithoutCallersIsResolved pins that a seed which resolves
// and has no callers is a different answer from an unknown seed: resolved stays
// true, no did_you_mean is offered, and the empty-list guidance is kept.
func TestCallersKnownSeedWithoutCallersIsResolved(t *testing.T) {
	e, _ := newEngine(t)
	seedSameNamedMethods(t, e, nil)

	out, err := e.Query(context.Background(), QueryRequest{Action: "callers", Query: indexFileCall})
	if err != nil {
		t.Fatal(err)
	}
	if got := out["resolved"]; got != true {
		t.Errorf("resolved = %v, want true for a known seed", got)
	}
	if _, ok := out["did_you_mean"]; ok {
		t.Errorf("did_you_mean offered for a resolved seed: %v", out["did_you_mean"])
	}
	if got, _ := out["guidance"].(string); !strings.HasPrefix(got, "No callers for ") {
		t.Errorf("known seed with no callers guidance = %q, want the empty-list guidance", got)
	}
}

// TestCallersCapSitesWithTotal pins the cap: 120 callers answer with the
// default 50 sites, total_sites 120 and truncated true, while every caller
// stays listed. An explicit limit replaces the default.
func TestCallersCapSitesWithTotal(t *testing.T) {
	e, _ := newEngine(t)
	fanIn(t, e, "lib/t.go::Target", "Target", "lib/t.go", 120, "svc/c")

	out, err := e.Query(context.Background(), QueryRequest{Action: "callers", Query: "lib/t.go::Target"})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(out["sites"].([]string)); got != 50 {
		t.Errorf("sites = %d, want the default cap 50", got)
	}
	if got := out["total_sites"]; got != 120 {
		t.Errorf("total_sites = %v, want 120", got)
	}
	if got := out["truncated"]; got != true {
		t.Errorf("truncated = %v, want true", got)
	}
	if got := len(out["callers"].([]string)); got != 120 {
		t.Errorf("callers = %d, want all 120 kept", got)
	}

	out, err = e.Query(context.Background(), QueryRequest{Action: "callers", Query: "lib/t.go::Target", Limit: 7})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(out["sites"].([]string)); got != 7 {
		t.Errorf("sites with limit 7 = %d, want 7", got)
	}
}

// TestCallersProductionSitesFirst pins the truncation order: test-file callers
// sort after production callers even though their qualified names sort first.
func TestCallersProductionSitesFirst(t *testing.T) {
	e, _ := newEngine(t)
	fanIn(t, e, "lib/t.go::Target", "Target", "lib/t.go", 10, "zz/prod")
	ctx := context.Background()
	var els []store.Element
	var rels []store.Relationship
	for i := 0; i < 3; i++ {
		qn := fmt.Sprintf("aa/x_test.go::TestTarget%d", i)
		els = append(els, store.Element{QualifiedName: qn, ElementType: "function", Name: fmt.Sprintf("TestTarget%d", i), FilePath: "aa/x_test.go", LineStart: 5 + i, Language: "go"})
		rels = append(rels, store.Relationship{Source: qn, Target: "lib/t.go::Target", RelType: "calls", Confidence: 1})
	}
	if err := e.st.UpsertElements(ctx, els); err != nil {
		t.Fatal(err)
	}
	if err := e.st.UpsertRelationships(ctx, rels); err != nil {
		t.Fatal(err)
	}

	out, err := e.Query(ctx, QueryRequest{Action: "callers", Query: "lib/t.go::Target", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range out["sites"].([]string) {
		if strings.Contains(s, "_test.go") {
			t.Fatalf("a test-file caller took a production site slot: %q", s)
		}
	}
	if got := out["total_sites"]; got != 13 {
		t.Errorf("total_sites = %v, want 13", got)
	}
}

// TestCallersBareNameCountsPerSeed pins the per-seed counts of a bare name that
// resolves to four callables, and the guidance that names the top three seeds
// by caller count when there are more than three.
func TestCallersBareNameCountsPerSeed(t *testing.T) {
	e, _ := newEngine(t)
	counts := map[string]int{"a": 5, "b": 3, "c": 2, "d": 1}
	for _, pkg := range []string{"a", "b", "c", "d"} {
		fanIn(t, e, pkg+"/run.go::Run", "Run", pkg+"/run.go", counts[pkg], "src"+pkg)
	}

	out, err := e.Query(context.Background(), QueryRequest{Action: "callers", Query: "Run"})
	if err != nil {
		t.Fatal(err)
	}
	wantCounts := map[string]int{"a/run.go::Run": 5, "b/run.go::Run": 3, "c/run.go::Run": 2, "d/run.go::Run": 1}
	if got := out["seed_counts"]; !reflect.DeepEqual(got, wantCounts) {
		t.Errorf("seed_counts = %v, want %v", got, wantCounts)
	}
	guidance, _ := out["guidance"].(string)
	for _, want := range []string{"a/run.go::Run (5)", "b/run.go::Run (3)", "c/run.go::Run (2)"} {
		if !strings.Contains(guidance, want) {
			t.Errorf("guidance must name the top three seeds by caller count (missing %q): %q", want, guidance)
		}
	}
	if strings.Contains(guidance, "d/run.go::Run (1)") {
		t.Errorf("guidance names a fourth seed: %q", guidance)
	}
}

// TestCallersUnknownSeedFuzzyCandidates pins the fuzzy rung of did_you_mean: a
// seed one edit away from an indexed name (a typo, or a dropped letter) is
// offered that element, while a seed close to nothing offers no candidate.
func TestCallersUnknownSeedFuzzyCandidates(t *testing.T) {
	e, _ := newEngine(t)
	seedSameNamedMethods(t, e, nil)

	out, err := e.Query(context.Background(), QueryRequest{Action: "callers", Query: "internal/store/store.go::ReplaceFileRelationship"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := out["did_you_mean"], []string{storeReplace, pgReplace}; !reflect.DeepEqual(got, want) {
		t.Errorf("did_you_mean for a one-letter-short seed = %v, want %v", got, want)
	}

	out, err = e.Query(context.Background(), QueryRequest{Action: "callers", Query: "internal/x.go::Zzzz"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := out["did_you_mean"].([]string); len(got) != 0 {
		t.Errorf("did_you_mean for an unrelated seed = %v, want none", got)
	}
}

// TestCallersGuidanceNamesOnlySeedsWithCallers pins that the seed lead in the
// guidance never names a seed with no callers: a bare name with one caller
// seed and two seeds that have none recommends only the seed that has callers.
func TestCallersGuidanceNamesOnlySeedsWithCallers(t *testing.T) {
	e, _ := newEngine(t)
	fanIn(t, e, "a/run.go::Run", "Run", "a/run.go", 2, "srca")
	fanIn(t, e, "b/run.go::Run", "Run", "b/run.go", 0, "srcb")
	fanIn(t, e, "c/run.go::Run", "Run", "c/run.go", 0, "srcc")
	fanIn(t, e, "d/run.go::Run", "Run", "d/run.go", 0, "srcd")

	out, err := e.Query(context.Background(), QueryRequest{Action: "callers", Query: "Run"})
	if err != nil {
		t.Fatal(err)
	}
	guidance, _ := out["guidance"].(string)
	if !strings.Contains(guidance, "a/run.go::Run (2)") {
		t.Errorf("guidance must name the seed with callers: %q", guidance)
	}
	for _, zero := range []string{"b/run.go::Run (0)", "c/run.go::Run (0)", "d/run.go::Run (0)"} {
		if strings.Contains(guidance, zero) {
			t.Errorf("guidance names a seed with no callers %q: %q", zero, guidance)
		}
	}
}
