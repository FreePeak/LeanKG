package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// filterSearcher is the part of an engine the CX-16 conformance check needs:
// seed elements and vectors, then run the vector and hybrid searches with a
// filter that excludes more rows than the limit can absorb.
type filterSearcher interface {
	WriteStamp(ModelStamp) error
	UpsertElements([]Element) error
	UpsertVectors(modelID string, rows []VectorRow) error
	SearchVectors(ctx context.Context, q VectorQuery) ([]VectorSearchHit, error)
	Hybrid(ctx context.Context, q HybridQuery) ([]HybridHit, Arm, error)
}

// checkFilterBeforeLimit seeds 80 test fixtures that are closer to the query
// than 5 production elements, then asserts that a filter excluding the
// fixtures still returns the full limit. A filter applied after LIMIT sees only
// the fixtures and returns fewer hits than asked for.
//
// The fixture names sort before the production names and carry more keyword
// weight, so every keyword arm ranks fixtures first too.
func checkFilterBeforeLimit(t *testing.T, s filterSearcher, wantKeywordArms ...Arm) {
	t.Helper()
	const modelID = "cx16-model"
	if err := s.WriteStamp(ModelStamp{ModelID: modelID, Revision: "r1", Dimensions: 4, Distance: "cosine", Provider: "unit"}); err != nil {
		t.Fatal(err)
	}
	var els []Element
	var rows []VectorRow
	for i := 0; i < 80; i++ {
		qn := fmt.Sprintf("aaa.widget%02d", i)
		els = append(els, Element{QualifiedName: qn, ElementType: "function", Name: "widget",
			FilePath: fmt.Sprintf("testdata/widget_%02d.go", i), Language: "go",
			Content: "widget widget widget fixture"})
		rows = append(rows, VectorRow{QualifiedName: qn, Vec: []float32{1, 0.001 * float32(i), 0, 0}})
	}
	for i := 0; i < 5; i++ {
		qn := fmt.Sprintf("zzz.widget%d", i)
		els = append(els, Element{QualifiedName: qn, ElementType: "function", Name: "widget",
			FilePath: fmt.Sprintf("pkg/widget%d.go", i), Language: "go", Content: "widget"})
		rows = append(rows, VectorRow{QualifiedName: qn, Vec: []float32{0.2, 1, 0, 0}})
	}
	if err := s.UpsertElements(els); err != nil {
		t.Fatalf("upsert elements: %v", err)
	}
	if err := s.UpsertVectors(modelID, rows); err != nil {
		t.Fatalf("upsert vectors: %v", err)
	}
	noFixtures := ElementFilter{ExcludePathContains: []string{"testdata"}}
	assertProduction := func(label string, names []string, want int) {
		t.Helper()
		if len(names) != want {
			t.Fatalf("%s: %d hits %v, want exactly %d", label, len(names), names, want)
		}
		for _, n := range names {
			if !strings.HasPrefix(n, "pkg/") {
				t.Fatalf("%s: fixture %s leaked through the filter", label, n)
			}
		}
	}

	vec, err := s.SearchVectors(context.Background(), VectorQuery{ModelID: modelID, Vec: []float32{1, 0, 0, 0}, K: 3, Filter: noFixtures})
	if err != nil {
		t.Fatalf("SearchVectors: %v", err)
	}
	assertProduction("vector arm", hitFiles(vec), 3)

	// Keyword-only: no vector, so the keyword arm alone must fill the limit.
	kw, arm, err := s.Hybrid(context.Background(), HybridQuery{ModelID: modelID, Text: "widget", Limit: 2,
		Filter: noFixtures, Weights: DefaultFuseWeights()})
	if err != nil {
		t.Fatalf("Hybrid keyword-only: %v", err)
	}
	if !slices.Contains(wantKeywordArms, arm) {
		t.Fatalf("Hybrid keyword-only arm = %q; want one of %v", arm, wantKeywordArms)
	}
	assertProduction("keyword arm", hitFiles(kw), 2)

	// Fused: vector and keyword arms together, filter on both.
	fused, _, err := s.Hybrid(context.Background(), HybridQuery{ModelID: modelID, Text: "widget",
		Vec: []float32{1, 0, 0, 0}, Limit: 3, Filter: noFixtures, Weights: DefaultFuseWeights()})
	if err != nil {
		t.Fatalf("Hybrid fused: %v", err)
	}
	assertProduction("fused", hitFiles(fused), 3)
}

// hybridSearch runs Hybrid with the store's default fusion weights, the weights
// the plain search has always used.
func hybridSearch(s interface {
	Hybrid(context.Context, HybridQuery) ([]HybridHit, Arm, error)
}, modelID, text string, vec []float32, limit int) ([]HybridHit, Arm, error) {
	return s.Hybrid(context.Background(), HybridQuery{ModelID: modelID, Text: text, Vec: vec, Limit: limit,
		Weights: DefaultFuseWeights()})
}

// armsOf names the arms that ranked any of hits, in fusion order: the
// provenance the L3 reason reports.
func armsOf(hits []HybridHit) []Arm {
	var out []Arm
	for _, arm := range []Arm{ArmVector, ArmFTS5, ArmTSVector, ArmTrigram, ArmILIKE, ArmName} {
		for _, h := range hits {
			if _, ok := h.Ranks[arm]; ok {
				out = append(out, arm)
				break
			}
		}
	}
	return out
}

func hitFiles(hits any) []string {
	var out []string
	switch h := hits.(type) {
	case []VectorSearchHit:
		for _, x := range h {
			out = append(out, x.Element.FilePath)
		}
	case []HybridHit:
		for _, x := range h {
			out = append(out, x.Element.FilePath)
		}
	}
	return out
}

// SQLite: the vector arm already filters inside its scan; the keyword arm
// (FTS5) must push the filter into its WHERE so the limit is not spent on
// fixtures.
func TestFilterBeforeLimitSQLite(t *testing.T) {
	checkFilterBeforeLimit(t, openTestStore(t), ArmFTS5)
}

// PostgreSQL: the vector arm over-fetches and filters in Go, so 80 excluded
// rows starve it; the tsvector arm (or the trigram/ILIKE arm when tsvector
// finds nothing) must push the filter into its SQL.
func TestPGFilterBeforeLimit(t *testing.T) {
	checkFilterBeforeLimit(t, openPGTest(t), ArmTSVector, ArmTrigram, ArmILIKE)
}
