package core

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/graph"
	"github.com/FreePeak/LeanKG/internal/store"
)

// seedRungs indexes one element and embeds it, so the L3 rung finds a stamped
// collection that matches the live provider.
func seedRungs(t *testing.T, e *Engine) embed.Provider {
	t.Helper()
	if err := e.st.UpsertElements([]store.Element{{
		QualifiedName: "pkg/a.go::Alpha", ElementType: "function", Name: "Alpha",
		FilePath: "pkg/a.go", Language: "go", Content: "func Alpha() {}",
	}}); err != nil {
		t.Fatal(err)
	}
	p := embed.Deterministic(64)
	if _, err := embed.Run(context.Background(), e.st, p, "full"); err != nil {
		t.Fatal(err)
	}
	e.SetEmbedder(p)
	return p
}

// TestRungsReturnCancelledCtx: a cancelled caller ctx ends every rung with
// ctx.Err(), never with an empty or degraded answer.
func TestRungsReturnCancelledCtx(t *testing.T) {
	e, _ := newEngine(t)
	seedRungs(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resp := func() map[string]any { return map[string]any{} }

	if _, err := e.rungExact(ctx, "Alpha", 10, resp(), true, store.ElementFilter{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("rungExact err = %v, want context.Canceled", err)
	}
	if _, err := e.rungFuzzy(ctx, "Alpha", 10, resp(), true, store.ElementFilter{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("rungFuzzy err = %v, want context.Canceled", err)
	}
	if _, err := e.rungSemantic(ctx, "alpha function", 10, resp(), true, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("rungSemantic err = %v, want context.Canceled", err)
	}
	if _, err := e.Query(ctx, QueryRequest{Query: "Alpha"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ladder err = %v, want context.Canceled", err)
	}
}

// cancelOnEmbed cancels the caller's ctx and then fails, like a provider call
// interrupted by a disconnect. The failure must surface as cancellation, not
// as a degrade to L2.
type cancelOnEmbed struct {
	embed.Provider
	cancel context.CancelFunc
}

func (c cancelOnEmbed) Embed(context.Context, embed.TextKind, []string) ([][]float32, error) {
	c.cancel()
	return nil, errors.New("provider connection reset")
}

func TestSemanticRungCancelDuringEmbedIsNotDegrade(t *testing.T) {
	e, _ := newEngine(t)
	p := seedRungs(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.SetEmbedder(cancelOnEmbed{Provider: p, cancel: cancel})

	out, err := e.rungSemantic(ctx, "alpha function", 10, map[string]any{}, true, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v (out=%v), want context.Canceled: a cancelled embed is not a provider failure", err, out)
	}
}

// TestGraphVerbReportsBudgetAndDepth: a hub with more dependents than the
// node budget answers with the budget, the effective depth and truncated:true.
func TestGraphVerbReportsBudgetAndDepth(t *testing.T) {
	e, _ := newEngine(t)
	els := []store.Element{{QualifiedName: "hub", ElementType: "function", Name: "hub", FilePath: "h.go", Language: "go"}}
	var rels []store.Relationship
	for i := range graph.NodeBudget + 1 {
		qn := fmt.Sprintf("caller%05d", i)
		els = append(els, store.Element{QualifiedName: qn, ElementType: "function", Name: qn, FilePath: "c.go", Language: "go"})
		rels = append(rels, store.Relationship{Source: qn, Target: "hub", RelType: "calls", Confidence: 0.9})
	}
	if err := e.st.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
	if err := e.st.UpsertRelationships(rels); err != nil {
		t.Fatal(err)
	}

	out, err := e.Query(context.Background(), QueryRequest{Action: "impact", Query: "hub", Args: map[string]any{"depth": 1}})
	if err != nil {
		t.Fatal(err)
	}
	hits, _ := out["hits"].([]graph.Hit)
	if len(hits) != graph.NodeBudget {
		t.Fatalf("len(hits) = %d, want the budget %d", len(hits), graph.NodeBudget)
	}
	if out["truncated"] != true {
		t.Fatalf("truncated = %v, want true", out["truncated"])
	}
	if out["depth"] != 1 {
		t.Fatalf("depth = %v, want the effective depth 1", out["depth"])
	}
}

// TestPathReportsTruncatedNotUnreachable: a search cut short by the budget
// says so; it must not claim the target is unreachable.
func TestPathReportsTruncatedNotUnreachable(t *testing.T) {
	e, _ := newEngine(t)
	els := []store.Element{{QualifiedName: "src", ElementType: "function", Name: "src", FilePath: "s.go", Language: "go"},
		{QualifiedName: "t", ElementType: "function", Name: "t", FilePath: "t.go", Language: "go"}}
	var rels []store.Relationship
	for i := range graph.NodeBudget + 1 {
		qn := fmt.Sprintf("f%05d", i)
		els = append(els, store.Element{QualifiedName: qn, ElementType: "function", Name: qn, FilePath: "f.go", Language: "go"})
		rels = append(rels, store.Relationship{Source: "src", Target: qn, RelType: "calls", Confidence: 0.9})
	}
	rels = append(rels, store.Relationship{Source: fmt.Sprintf("f%05d", graph.NodeBudget), Target: "t", RelType: "calls", Confidence: 0.9})
	if err := e.st.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
	if err := e.st.UpsertRelationships(rels); err != nil {
		t.Fatal(err)
	}

	out, err := e.Query(context.Background(), QueryRequest{Action: "path", Query: "src", Args: map[string]any{"to": "t"}})
	if err != nil {
		t.Fatal(err)
	}
	if out["truncated"] != true {
		t.Fatalf("truncated = %v, want true", out["truncated"])
	}
	if _, said := out["reachable"]; said {
		t.Fatalf("reachable = %v with truncated: true; the answer is unknown, so reachable must be omitted", out["reachable"])
	}
}
