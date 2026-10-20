package core

import (
	"context"
	"testing"

	"github.com/FreePeak/LeanKG/internal/graph"
)

// TestEveryGraphVerbNamesItsResult pins one shape rule across the six graph
// verbs: the key holding the result carries the verb's name.
//
// The defect: impact answered under "hits", the same key the LADDER uses for
// element search. An agent cannot tell "these are the 21 nodes that depend on
// this symbol" from "these are the 21 elements that matched a text query" —
// and the two have completely different shapes, so the plausible reading is
// the wrong one. Every other verb already names itself (callers, callees,
// path, context, explain), so impact was the single inconsistency.
//
// The alias is kept alongside, not instead: `hits` is what the search ladder
// and the goldens speak, and an agent that has learned one must not be broken
// by the other. The test asserts the named key is present, because the alias
// is a compatibility affordance, not a contract.
func TestEveryGraphVerbNamesItsResult(t *testing.T) {
	e, _ := newEngine(t)
	seedGraph(t, e)
	ctx := context.Background()

	for _, tc := range []struct{ verb, key string }{
		{"impact", "impact"},
		{"callers", "callers"},
		{"callees", "callees"},
		{"context", "element"},
		{"explain", "element"},
	} {
		out, err := e.Query(ctx, QueryRequest{Action: tc.verb, Query: "Helper", Limit: 5})
		if err != nil {
			t.Fatalf("%s: %v", tc.verb, err)
		}
		if _, ok := out[tc.key]; !ok {
			t.Errorf("%s answered under %q (keys: %v) — each verb names its result", tc.verb, tc.key, keysOf(out))
		}
	}
	// path names its own key too.
	out, err := e.Query(ctx, QueryRequest{
		Action: "path", Query: "Caller", Args: map[string]any{"to": "Leaf", "depth": 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["path"]; !ok {
		t.Errorf("path answered under %v, want a path key", keysOf(out))
	}
}

// TestImpactKeepsTheHitsAlias pins the compatibility half: the ladder's own
// consumers (the goldens, an agent that learned `hits`) must keep working.
func TestImpactKeepsTheHitsAlias(t *testing.T) {
	e, _ := newEngine(t)
	seedGraph(t, e)
	out, err := e.Query(context.Background(), QueryRequest{Action: "impact", Query: "Helper", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	// Both keys carry the SAME list (the traversal's own graph.Hit shape, not
	// the ladder's element shape) — an alias, not a re-shape.
	named, ok := out["impact"].([]graph.Hit)
	if !ok {
		t.Fatalf("impact key is %T, want []graph.Hit (keys: %v)", out["impact"], keysOf(out))
	}
	alias, ok := out["hits"].([]graph.Hit)
	if !ok {
		t.Fatalf("impact must keep the hits alias for existing consumers (keys: %v)", keysOf(out))
	}
	if len(named) != len(alias) || (len(named) > 0 && named[0].QN != alias[0].QN) {
		t.Fatalf("the two keys must hold the same list, got %d and %d entries", len(named), len(alias))
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
