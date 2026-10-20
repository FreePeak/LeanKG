package core

import (
	"context"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// seedGraph wires the two-element call chain every graph verb in this file
// traverses: pkg/b.go::Caller calls pkg/a.go::Helper, which calls Leaf.
func seedGraph(t *testing.T, e *Engine) {
	t.Helper()
	if err := e.st.UpsertElements([]store.Element{
		{QualifiedName: "pkg/a.go::Helper", ElementType: "function", Name: "Helper", FilePath: "pkg/a.go", Language: "go"},
		{QualifiedName: "pkg/b.go::Caller", ElementType: "function", Name: "Caller", FilePath: "pkg/b.go", Language: "go"},
		{QualifiedName: "pkg/a.go::Leaf", ElementType: "function", Name: "Leaf", FilePath: "pkg/a.go", Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.UpsertRelationships([]store.Relationship{
		{Source: "pkg/b.go::Caller", Target: "pkg/a.go::Helper", RelType: "calls", Confidence: 1},
		{Source: "pkg/a.go::Helper", Target: "pkg/a.go::Leaf", RelType: "calls", Confidence: 1},
	}); err != nil {
		t.Fatal(err)
	}
}

// TestGraphVerbsAnswerAnUnknownSeed pins the agent-facing contract for a
// misspelled or not-yet-indexed seed: an ANSWER with recovery guidance, not a
// bare `graph: unknown node` error.
//
// The defect: graph.ErrUnknownNode was returned straight out of Query, so the
// whole tool call failed with a message that names neither the verb nor the
// seed nor a next step. An agent cannot tell "your symbol is not in the index"
// from "this graph verb is broken", and the guidance that every other empty
// result carries (graphEmptyGuidance) was unreachable on this path.
func TestGraphVerbsAnswerAnUnknownSeed(t *testing.T) {
	e, _ := newEngine(t)
	seedGraph(t, e)
	ctx := context.Background()

	for _, verb := range []string{"impact", "callers", "callees", "context", "explain"} {
		out, err := e.Query(ctx, QueryRequest{Action: verb, Query: "NoSuchSymbol"})
		if err != nil {
			t.Fatalf("%s on an unknown seed must answer, not error: %v", verb, err)
		}
		guidance, _ := out["guidance"].(string)
		if guidance == "" {
			t.Fatalf("%s on an unknown seed carried no guidance: %+v", verb, out)
		}
		if !strings.Contains(guidance, "NoSuchSymbol") {
			t.Fatalf("%s guidance must name the seed it could not resolve (got %q)", verb, guidance)
		}
	}
}

// TestGraphPathUnknownEndAnswersLikeShortestPath pins path's two ends: only the
// MISSING args.to is a caller error, while an unresolvable from/to is an
// answer. Today both ends share the same hard error, so an agent typo in the
// target cannot be told apart from a verb that is misconfigured.
func TestGraphPathUnknownEndAnswersLikeShortestPath(t *testing.T) {
	e, _ := newEngine(t)
	seedGraph(t, e)
	ctx := context.Background()

	// Missing args.to stays an error: the request is incomplete.
	if _, err := e.Query(ctx, QueryRequest{Action: "path", Query: "Helper"}); err == nil {
		t.Fatal("path without args.to must remain a hard error")
	}

	// A present but unresolvable `to` answers with the not-reachable shape.
	out, err := e.Query(ctx, QueryRequest{Action: "path", Query: "Helper", Args: map[string]any{"to": "NoSuchTarget"}})
	if err != nil {
		t.Fatalf("path with an unresolvable target must answer, not error: %v", err)
	}
	if out["reachable"] != false {
		t.Fatalf("reachable=%v, want false", out["reachable"])
	}
	guidance, _ := out["guidance"].(string)
	if guidance == "" {
		t.Fatalf("unreachable path must carry guidance: %+v", out)
	}
}
