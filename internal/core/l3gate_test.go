package core

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/ontology"
	"github.com/FreePeak/LeanKG/internal/store"
)

// countingEmbedder records how many times EmbedQuery is called so a test can
// prove the Noul L3 gate prevented a semantic call. Embeds the other
// QueryEmbedder methods so SetEmbedder accepts it.
type countingEmbedder struct {
	QueryEmbedder
	n atomic.Int64
}

func (c *countingEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	c.n.Add(1)
	return c.QueryEmbedder.EmbedQuery(ctx, text)
}

// TestL3GateSkipsSemanticOnLowOntologyConfidence is the FR-TYPE-01
// acceptance: when OntologyConfidence is in (0, MinConfidence), the ladder
// must not call the embedder. The existing TestL3GateSkipsWhenConfidence…
// only unit-tests ScoreLevel boundaries and never exercises Query — so the
// gate could be unplugged and that test would still pass.
//
// Seed: one ontology node whose description shares a single meaningful word
// with a 4-word prose query → MatchScore = 0.25 (partial 1/4 * 0.3) →
// OntologyConfidence = 0.25, which is the band the gate exists to catch.
func TestL3GateSkipsSemanticOnLowOntologyConfidence(t *testing.T) {
	e, _ := newEngine(t)
	// Code elements so the store is not L0-cold and L1/L2 can still answer.
	code := []store.Element{
		{QualifiedName: "pkg/a.go::Alpha", ElementType: "function", Name: "Alpha", FilePath: "pkg/a.go", Language: "go", Content: "func Alpha() {}"},
		{QualifiedName: "pkg/b.go::Beta", ElementType: "function", Name: "Beta", FilePath: "pkg/b.go", Language: "go", Content: "func Beta() {}"},
	}
	// Ontology node: description contains "refund" only. A query of four
	// meaningful words of which one is "refund" scores 0.25.
	onto := store.Element{
		QualifiedName: "local:default:domain_entity:payments:v1",
		ElementType:   ontology.TypeDomainEntity,
		Name:          "Payments",
		FilePath:      "ontology://local:default:domain_entity:payments:v1",
		Language:      "ontology",
		Metadata:      map[string]any{"description": "handles a refund against the card network", "aliases": []any{}, "ontology_layer": "domain"},
	}
	if err := e.st.UpsertElements(append(code, onto)); err != nil {
		t.Fatal(err)
	}
	p := embed.Deterministic(16)
	if _, err := embed.Run(context.Background(), e.st, p, "full"); err != nil {
		t.Fatal(err)
	}
	counter := &countingEmbedder{QueryEmbedder: QueryEmbedderFromProvider(p)}
	e.SetEmbedder(counter)

	// Sanity: the confidence the gate will see is the one we designed for.
	conf, err := ontology.OntologyConfidence(e.st, "how does refund settle overnight")
	if err != nil {
		t.Fatal(err)
	}
	if conf <= 0 || conf >= ontology.MinConfidence {
		t.Fatalf("fixture confidence = %v, want (0, %v) so the gate is the path under test", conf, ontology.MinConfidence)
	}

	out, err := e.Query(context.Background(), QueryRequest{Query: "how does refund settle overnight"})
	if err != nil {
		t.Fatal(err)
	}
	if n := counter.n.Load(); n != 0 {
		t.Fatalf("embedder called %d times — the L3 gate failed to hold the semantic rung back", n)
	}
	r, _ := out["retrieval"].(map[string]any)
	if r == nil {
		t.Fatalf("no retrieval block: %v", out)
	}
	// L3 was skipped; the ladder either answered from L2 or returned an empty
	// L2 with the gate's degrade. Either way the rung is not L3.
	if r["rung"] == "L3" {
		t.Fatalf("rung = L3 despite conf=%v < MinConfidence — gate did not fire: %v", conf, r)
	}
}

// TestL3GateAllowsSemanticWhenConfidenceIsZero pins the complementary
// branch: conf == 0 (no ontology match) must NOT hold L3 back — that is the
// happy path for a store with no ontology loaded, which is the common case.
func TestL3GateAllowsSemanticWhenConfidenceIsZero(t *testing.T) {
	e, _ := newEngine(t)
	els := []store.Element{
		{QualifiedName: "pkg/w.go::Widget", ElementType: "function", Name: "Widget", FilePath: "pkg/w.go", Language: "go",
			Content: "// Widget draws the frame\nfunc Widget() {}"},
	}
	if err := e.st.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
	p := embed.Deterministic(16)
	if _, err := embed.Run(context.Background(), e.st, p, "full"); err != nil {
		t.Fatal(err)
	}
	counter := &countingEmbedder{QueryEmbedder: QueryEmbedderFromProvider(p)}
	e.SetEmbedder(counter)

	conf, err := ontology.OntologyConfidence(e.st, "how does the widget frame get drawn")
	if err != nil {
		t.Fatal(err)
	}
	if conf != 0 {
		t.Fatalf("fixture confidence = %v, want 0 (no ontology nodes)", conf)
	}

	out, err := e.Query(context.Background(), QueryRequest{Query: "how does the widget frame get drawn"})
	if err != nil {
		t.Fatal(err)
	}
	if n := counter.n.Load(); n == 0 {
		t.Fatal("embedder never called — conf==0 must allow L3 (the no-ontology happy path)")
	}
	r := out["retrieval"].(map[string]any)
	if r["rung"] != "L3" {
		// L2 is acceptable only when L3 is unreachable; with a wired embedder
		// and conf==0 the prose path must take L3.
		t.Fatalf("rung = %v, want L3 (conf==0, embedder wired, prose query); reason=%v", r["rung"], r["reason"])
	}
}

// Keep the original ScoreLevel boundary test but rename its claim so it no
// longer pretends to exercise Query. The Query path is covered above.
func TestScoreLevelBoundaries(t *testing.T) {
	if got := ontology.ScoreLevel(0.25); got != ontology.MatchNone {
		t.Errorf("ScoreLevel(0.25) = %v, want MatchNone", got)
	}
	if got := ontology.ScoreLevel(0.299); got != ontology.MatchNone {
		t.Errorf("ScoreLevel(0.299) = %v, want MatchNone", got)
	}
	if got := ontology.ScoreLevel(ontology.MinConfidence); got != ontology.MatchPartial {
		t.Errorf("ScoreLevel(MinConfidence) = %v, want MatchPartial", got)
	}
	if got := ontology.ScoreLevel(0.3); got != ontology.MatchPartial {
		t.Errorf("ScoreLevel(0.3) = %v, want MatchPartial", got)
	}
	// The old name is kept as an alias so anything grepping for it still finds
	// a test; the body lives here.
	_ = strings.TrimSpace
}
