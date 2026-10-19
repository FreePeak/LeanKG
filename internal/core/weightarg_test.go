package core

import (
	"context"
	"strings"
	"testing"
)

// TestQueryWeightIsValidated pins the defect measured live on the 298-project
// self-host 2026-10-11: `args.weight` was accepted in EVERY shape and silently
// ignored — the same class as #484's scope before it was wired.
//
//	weight=3,1     accepted; retrieval.weight absent; ranking identical
//	weight=1,0     accepted; retrieval.weight absent
//	weight=0,0     accepted; retrieval.weight absent
//	weight=banana  accepted; retrieval.weight absent
//	weight=3       accepted; retrieval.weight absent
//
// A caller who sets a weight believes it set one. `banana` and `0,0` are not
// merely unapplied, they are unreadable: 0,0 ranks nothing, and a typo must
// fail the same way an unknown scope does.
func TestQueryWeightIsValidated(t *testing.T) {
	e := engineAt(t, t.TempDir())

	for _, tc := range []struct{ weight, want string }{
		{"banana", "vector"},
		{"3", "vector>,<keyword"},
		{"0,0", "0,0"},
		{"-1,2", "non-negative"},
	} {
		_, err := e.Query(context.Background(), QueryRequest{Query: "rank", Args: map[string]any{"weight": tc.weight}})
		if err == nil {
			t.Errorf("weight %q was accepted; want a refusal naming the shape", tc.weight)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("weight %q: refusal %q does not name %q", tc.weight, err.Error(), tc.want)
		}
	}
	// A well-formed weight is accepted, and an UNSET one stays the default
	// (no behaviour change for every existing caller).
	for _, ok := range []string{"", "3,1", "1,0", "0,1"} {
		if _, err := e.Query(context.Background(), QueryRequest{Query: "rank", Args: map[string]any{"weight": ok}}); err != nil {
			t.Errorf("weight %q was refused: %v", ok, err)
		}
	}
}

// TestWeightIsNotClaimedByARungThatDoesNotUseIt is the correction to the first
// version of this feature, measured on the branch itself: with the weight
// reported unconditionally, an L0 answer ("no elements indexed") claimed
// weight=3,1 and an L2 degrade would have too — while only L3 fuses two arms
// to scale. An answer that names a setting it did not apply is the same defect
// as #491's advice that named a flag which did not work.
func TestWeightIsNotClaimedByARungThatDoesNotUseIt(t *testing.T) {
	e := engineAt(t, t.TempDir()) // empty store, no embedder
	out, err := e.Query(context.Background(), QueryRequest{
		Query: "rank", Args: map[string]any{"weight": "3,1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := out["retrieval"].(map[string]any)
	if r == nil {
		t.Fatal("no retrieval block")
	}
	if r["rung"] != "L3" {
		if w, claimed := r["weight"]; claimed {
			t.Errorf("a %v answer claims weight=%v; only L3 applies a weight", r["rung"], w)
		}
	}
}

// TestParseFusionWeightRejectsNonFinite pins that NaN and the infinities are
// refused. strconv.ParseFloat accepts all of them, and NaN passes a plain
// `v < 0` check, so a non-finite weight would otherwise reach fusion.
func TestParseFusionWeightRejectsNonFinite(t *testing.T) {
	for _, bad := range []string{"NaN,1", "1,NaN", "+Inf,1", "1,-Inf", "Inf,Inf"} {
		if _, err := parseFusionWeight(bad); err == nil {
			t.Errorf("weight %q was accepted; want a refusal", bad)
		}
	}
	for _, ok := range []string{"3,1", "1,1", "1,0", "0,1"} {
		if _, err := parseFusionWeight(ok); err != nil {
			t.Errorf("weight %q was refused: %v", ok, err)
		}
	}
	if _, err := parseFusionWeight("0,0"); err == nil {
		t.Errorf("weight 0,0 was accepted; want a refusal")
	}
}
