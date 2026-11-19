package core

import (
	"context"
	"reflect"
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
