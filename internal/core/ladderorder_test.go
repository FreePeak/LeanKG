package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestLadderPrefersSemanticOverWeakKeyword pins the router's rung ORDER, which
// the PRD states as the product decision behind FR-ZCP-03: "one tool, many
// rungs … L3 vectors → ANN + rerank + traverse; L2 no vectors → FTS/trigram
// fuzzy + ontology". L3 is the BETTER rung, so when the collection is present
// and stamped, an intent-shaped query must reach it.
//
// The defect: the router returned as soon as L2 produced ANY hit, so on an
// embedded project every non-identifier question stopped at keyword search and
// the vector collection was never consulted. Live on this repository: the
// question "how are search results ranked and fused" answered with three
// archive-report headings (the only elements containing those words) while L3
// on the same query put internal/store/pg_fts.go::FusedHit — the function that
// actually implements the answer — first. Keyword noise is exactly what a
// semantic tier exists to outrank.
//
// The fix keeps both properties: L1 still wins outright on an exact identifier,
// and a genuinely empty keyword result still falls through to L3 (the existing
// degrade path), but a weak keyword hit no longer short-circuits a live vector
// rung. Pinned here on vectors whose similarity is decisive, so the assertion
// is about the ROUTE and not about a provider's ranking quality.
func TestLadderPrefersSemanticOverWeakKeyword(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	p := embed.Deterministic(16)

	// The implementation the question is really about, and a keyword-noise
	// element that happens to contain every word of the question.
	impl := store.Element{
		QualifiedName: "store/rank.go::Rank", ElementType: "function", Name: "Rank",
		FilePath: "store/rank.go", Language: "go",
		Content: "rank fuses candidate lists into one ordered result",
	}
	// The decoy is keyword noise ONLY: its NAME deliberately shares nothing
	// with the question, because a decoy named after the question's own word
	// stops being a keyword-noise fixture and becomes a name-collision one —
	// and the name arm (0.6 weight) would then win it, which says nothing about
	// the L2→L3 route this test exists to pin.
	noise := store.Element{
		QualifiedName: "docs/report.md::Q3Numbers", ElementType: "section", Name: "Q3Numbers",
		FilePath: "docs/report.md", Language: "md",
		Content: "search results ranked and fused during the reporting run",
	}
	if err := st.UpsertElements([]store.Element{impl, noise}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteStamp(embed.StampOf(p)); err != nil {
		t.Fatal(err)
	}
	// Store the QUERY-kind embedding of the question as the implementation's
	// vector, so L3's top hit is the implementation with similarity 1.
	qvec, err := p.Embed(context.Background(), embed.Query, []string{"how are results ranked and fused"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertVectors(p.ModelID(), []store.VectorRow{{QualifiedName: impl.QualifiedName, Vec: qvec[0]}}); err != nil {
		t.Fatal(err)
	}

	e := New(st, nil, QueryEmbedderFromProvider(p))
	const question = "how are results ranked and fused"

	// The question is keyword-heavy, so L2 does return something — that is
	// the precondition this defect needed; assert it, so the test cannot
	// silently become an empty-L2 case.
	out, err := e.Query(context.Background(), QueryRequest{Action: "fuzzy", Query: question})
	if err != nil {
		t.Fatal(err)
	}
	if len(hitsOf(out)) == 0 {
		t.Fatal("precondition: the keyword rung must return something for this fixture, else the test proves nothing")
	}

	out, err = e.Query(context.Background(), QueryRequest{Query: question})
	if err != nil {
		t.Fatal(err)
	}
	if r := out["retrieval"].(map[string]any); r["rung"] != "L3" {
		t.Fatalf("a keyword hit must not short-circuit a live vector rung: got %v (hits %v)", r, out["hits"])
	}
	if got := hitsOf(out); len(got) == 0 || got[0]["qualified_name"] != impl.QualifiedName {
		t.Fatalf("L3 top hit = %v, want the implementation element", out["hits"])
	}

	// L1 still wins outright: an exact identifier must never pay for an
	// embedding call.
	out, err = e.Query(context.Background(), QueryRequest{Query: "Rank"})
	if err != nil {
		t.Fatal(err)
	}
	if r := out["retrieval"].(map[string]any); r["rung"] != "L1" {
		t.Fatalf("exact identifier must stay L1, got %v", r)
	}
}
