package core

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// seedGraph wires the two-element call chain every graph verb in this file
// traverses: pkg/b.go::Caller calls pkg/a.go::Helper, which calls Leaf.
func seedGraph(t *testing.T, e *Engine) {
	t.Helper()
	if err := e.st.UpsertElements(context.Background(), []store.Element{
		{QualifiedName: "pkg/a.go::Helper", ElementType: "function", Name: "Helper", FilePath: "pkg/a.go", Language: "go"},
		{QualifiedName: "pkg/b.go::Caller", ElementType: "function", Name: "Caller", FilePath: "pkg/b.go", Language: "go"},
		{QualifiedName: "pkg/a.go::Leaf", ElementType: "function", Name: "Leaf", FilePath: "pkg/a.go", Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.UpsertRelationships(context.Background(), []store.Relationship{
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

const adminVerifyQN = "internal/auth/admin.go::VerifyAdminLogin"

// seedAliasGraph wires the callable the package-qualified and path-suffix seeds
// name, one caller of it, a method on a type of the same package, and two
// functions in two different directories both named store.
func seedAliasGraph(t *testing.T, e *Engine) {
	t.Helper()
	ctx := context.Background()
	if err := e.st.UpsertElements(ctx, []store.Element{
		{QualifiedName: adminVerifyQN, ElementType: "function", Name: "VerifyAdminLogin", FilePath: "internal/auth/admin.go", LineStart: 12, Language: "go"},
		{QualifiedName: "internal/auth/admin.go::Store.Check", ElementType: "method", Name: "Check", FilePath: "internal/auth/admin.go", LineStart: 40, Language: "go"},
		{QualifiedName: "internal/api/login.go::Login", ElementType: "function", Name: "Login", FilePath: "internal/api/login.go", LineStart: 30, Language: "go"},
		{QualifiedName: "a/store/x.go::Open", ElementType: "function", Name: "Open", FilePath: "a/store/x.go", LineStart: 5, Language: "go"},
		{QualifiedName: "b/store/y.go::Open", ElementType: "function", Name: "Open", FilePath: "b/store/y.go", LineStart: 5, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.UpsertRelationships(ctx, []store.Relationship{
		{Source: "internal/api/login.go::Login", Target: adminVerifyQN, RelType: "calls", Confidence: 1},
	}); err != nil {
		t.Fatal(err)
	}
}

// TestCallersResolvesPackageAndPathSeeds pins the two alias forms: a package
// qualified name (auth.VerifyAdminLogin, auth.Store.Check) and a path suffix
// (auth/admin.go::VerifyAdminLogin, admin.go::VerifyAdminLogin) resolve to the
// one element they name, and the answer reports both the written seed
// (resolved_from) and the qualified name it resolved to (resolved_query).
func TestCallersResolvesPackageAndPathSeeds(t *testing.T) {
	cases := []struct {
		seed string
		want string
	}{
		{"auth.VerifyAdminLogin", adminVerifyQN},
		{"auth/admin.go::VerifyAdminLogin", adminVerifyQN},
		{"admin.go::VerifyAdminLogin", adminVerifyQN},
		{"auth.Store.Check", "internal/auth/admin.go::Store.Check"},
	}
	for _, tc := range cases {
		t.Run(tc.seed, func(t *testing.T) {
			e, _ := newEngine(t)
			seedAliasGraph(t, e)
			out, err := e.Query(context.Background(), QueryRequest{Action: "callers", Query: tc.seed})
			if err != nil {
				t.Fatal(err)
			}
			if got := out["resolved"]; got != true {
				t.Fatalf("resolved = %v, want true for seed %q (out %+v)", got, tc.seed, out)
			}
			if got, want := out["seeds"], []string{tc.want}; !reflect.DeepEqual(got, want) {
				t.Errorf("seeds = %v, want %v", got, want)
			}
			if got := out["resolved_from"]; got != tc.seed {
				t.Errorf("resolved_from = %v, want the written seed %q", got, tc.seed)
			}
			if got := out["resolved_query"]; got != tc.want {
				t.Errorf("resolved_query = %v, want %q", got, tc.want)
			}
		})
	}
	e, _ := newEngine(t)
	seedAliasGraph(t, e)
	out, err := e.Query(context.Background(), QueryRequest{Action: "callers", Query: "auth.VerifyAdminLogin"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := out["callers"], []string{"internal/api/login.go::Login"}; !reflect.DeepEqual(got, want) {
		t.Errorf("callers = %v, want %v", got, want)
	}
}

// TestCallersAliasSeedMustMatchTheDirectory pins that the package form checks
// the file's directory, not only the name: api.VerifyAdminLogin names no
// element, and a path suffix must end at a path-segment boundary (min.go does
// not name admin.go).
func TestCallersAliasSeedMustMatchTheDirectory(t *testing.T) {
	for _, seed := range []string{"api.VerifyAdminLogin", "min.go::VerifyAdminLogin"} {
		e, _ := newEngine(t)
		seedAliasGraph(t, e)
		out, err := e.Query(context.Background(), QueryRequest{Action: "callers", Query: seed})
		if err != nil {
			t.Fatal(err)
		}
		if got := out["resolved"]; got != false {
			t.Errorf("seed %q resolved = %v, want false", seed, got)
		}
		if _, ok := out["resolved_from"]; ok {
			t.Errorf("seed %q: resolved_from set on an unresolved seed", seed)
		}
	}
}

// TestCallersAmbiguousPackageSeedReturnsEveryCandidate pins that a package
// name shared by two directories answers with every candidate as a seed, and
// guidance that names them so the agent can retry with a qualified name.
func TestCallersAmbiguousPackageSeedReturnsEveryCandidate(t *testing.T) {
	e, _ := newEngine(t)
	seedAliasGraph(t, e)
	out, err := e.Query(context.Background(), QueryRequest{Action: "callers", Query: "store.Open"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a/store/x.go::Open", "b/store/y.go::Open"}
	if got := out["seeds"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("seeds = %v, want every candidate %v", got, want)
	}
	guidance, _ := out["guidance"].(string)
	for _, qn := range want {
		if !strings.Contains(guidance, qn) {
			t.Errorf("guidance must name candidate %q, got %q", qn, guidance)
		}
	}
}

// TestCallersUnknownAliasSeedKeepsDidYouMean pins that a package-qualified seed
// that names nothing still answers unresolved, with the did_you_mean candidates.
func TestCallersUnknownAliasSeedKeepsDidYouMean(t *testing.T) {
	e, _ := newEngine(t)
	seedAliasGraph(t, e)
	out, err := e.Query(context.Background(), QueryRequest{Action: "callers", Query: "auth.VerifyAdminLogn"})
	if err != nil {
		t.Fatal(err)
	}
	if got := out["resolved"]; got != false {
		t.Fatalf("resolved = %v, want false", got)
	}
	if got, _ := out["did_you_mean"].([]string); len(got) == 0 || got[0] != adminVerifyQN {
		t.Errorf("did_you_mean = %v, want it to offer %q first", out["did_you_mean"], adminVerifyQN)
	}
}
