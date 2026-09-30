package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestL3WeightArgIsAnEscapeHatch pins the contract wave 14 measured before
// building: `args.weight` lets a caller who KNOWS its question population pick
// the fusion weighting, and is exactly today's behaviour when unset.
//
// Why a hatch rather than a better default. Waves 12 and 13 measured the
// trade across four cells — two corpora, two label protocols — and found no
// single weighting that is right for all of them:
//
//	                        corpus1/behaviour  corpus1/doc  corpus2/behaviour  corpus2/doc
//	weight (1,1) — shipped            16/137        49/84         13/94         92/102
//	weight (3,1)                      30/137        48/84         21/94         88/102
//	keyword arm weight 0              59/137        40/84         55/94         84/102
//
// A keyword arm that cannot corroborate contributes nothing measurable to the
// fused order (wave 13's `agree_gate` was byte-identical to vector-only in every
// cell), so on behaviour-shaped questions — which name a concept and contain
// none of the document's words — the right move is to drop it; on
// keyword-shaped questions it is the arm carrying the answer. Which population
// a deployment serves is a PRODUCT decision the engine cannot infer from a
// query, so the honest lever is the same shape as `args.scope`: a named
// argument the caller sets when it knows, reported back so the answer is
// interpretable, and inert when absent.
//
// Format: `args.weight` is `"<vector>,<keyword>"` (both positive), the same
// convention the retrieval bench's `--sweep` prints, so a number measured
// offline can be pasted in. Anything else is an ERROR, never a silent default:
// a typo in a tuning knob that quietly falls back is the class of defect waves
// 1-11 exist to remove.
func TestL3WeightArgIsAnEscapeHatch(t *testing.T) {
	// Unset is exactly today's behaviour.
	if got, err := parseFusionWeight(""); err != nil || !got.ok || got.vector != 1 || got.keyword != 1 {
		t.Fatalf("an unset weight must be (1,1) — today's behaviour — got %+v err=%v", got, err)
	}
	// Set, it parses and applies.
	w, err := parseFusionWeight("3,1")
	if err != nil || !w.ok || w.vector != 3 || w.keyword != 1 {
		t.Fatalf("weight \"3,1\" must parse to (3,1), got %+v err=%v", w, err)
	}
	// A keyword weight of 0 is the wave-13 finding: drop the arm entirely.
	z, err := parseFusionWeight("1,0")
	if err != nil || !z.ok || z.vector != 1 || z.keyword != 0 {
		t.Fatalf("weight \"1,0\" must parse to (1,0) — drop the keyword arm — got %+v err=%v", z, err)
	}
	// A fractional weight is legitimate — the bench prints 1.5 and 2.5 — so the
	// parser accepts any non-negative pair, and a whitespace-only value is
	// treated as UNSET (an empty argument is an absent argument, not a typo).
	if got, err := parseFusionWeight("  "); err != nil || got.vector != 1 || got.keyword != 1 || got.raw != "" {
		t.Errorf("whitespace is unset, got %+v err=%v", got, err)
	}
	if got, err := parseFusionWeight("1.5,2.5"); err != nil || got.vector != 1.5 || got.keyword != 2.5 {
		t.Errorf("a fractional weight must parse, got %+v err=%v", got, err)
	}

	// Everything else is refused, loudly.
	for _, bad := range []string{"3", "3,1,2", "a,b", "3,", ",1", "-1,1", "0,0", "1,-2", "1e999"} {
		if _, err := parseFusionWeight(bad); err == nil {
			t.Errorf("weight %q must be an error, not a silent default", bad)
		}
	}
	// And the engine actually honours it: a keyword arm dropped by weight 0 must
	// not contribute. This needs a wired provider and a stamped collection, or
	// the ladder degrades to L2 and never reads a weight at all.
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	prov := embed.Deterministic(16)
	if err := st.WriteStamp(embed.StampOf(prov)); err != nil {
		t.Fatal(err)
	}
	e := New(st, nil, QueryEmbedderFromProvider(prov))
	e.SetProjectDir(dir)
	ctx := context.Background()
	for _, el := range []store.Element{
		{QualifiedName: "pkg/a.go::Alpha", ElementType: "function", Name: "Alpha", FilePath: "pkg/a.go", Language: "go", Content: "alpha body"},
		{QualifiedName: "pkg/b.go::Beta", ElementType: "function", Name: "Beta", FilePath: "pkg/b.go", Language: "go", Content: "keyword only body"},
	} {
		if err := e.st.UpsertElements([]store.Element{el}); err != nil {
			t.Fatal(err)
		}
		// The vector arm needs a vector: without one the query never reaches L3
		// and the test would be asserting the L2 path under an L3 name.
		qv, err := prov.Embed(ctx, embed.Document, []string{el.Content})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.st.UpsertVectors(prov.ModelID(), []store.VectorRow{{QualifiedName: el.QualifiedName, Vec: qv[0]}}); err != nil {
			t.Fatal(err)
		}
	}
	plain, err := e.Query(ctx, QueryRequest{Query: "keyword", Args: map[string]any{"weight": "1,1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(hitsOf(plain)) == 0 {
		t.Fatal("precondition: the keyword query must return something")
	}
	dropped, err := e.Query(ctx, QueryRequest{Query: "keyword", Args: map[string]any{"weight": "1,0"}})
	if err != nil {
		t.Fatalf("weight 1,0 must be accepted: %v", err)
	}
	r := dropped["retrieval"].(map[string]any)
	if r["weight"] != "1,0" {
		t.Errorf("a weighted answer must report its weight, got %v", r["weight"])
	}
	for _, h := range hitsOf(dropped) {
		rk, _ := h["ranks"].(map[string]int)
		if _, ok := rk["tsvector"]; ok {
			t.Errorf("with keyword weight 0 no hit may carry a tsvector rank: %v", h)
		}
	}
	// An unparseable weight is refused rather than ignored.
	if _, err := e.Query(ctx, QueryRequest{Query: "x", Args: map[string]any{"weight": "nonsense"}}); err == nil {
		t.Error("weight \"nonsense\" must be an error — a silent fallback on a tuning knob is the defect class this loop exists to remove")
	}
}
