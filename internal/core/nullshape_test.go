package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// findNulls lists the JSON paths whose value is null.
func findNulls(v any, path string, out *[]string) {
	switch t := v.(type) {
	case nil:
		*out = append(*out, path)
	case map[string]any:
		for k, c := range t {
			findNulls(c, path+"."+k, out)
		}
	case []any:
		for i, c := range t {
			findNulls(c, fmt.Sprintf("%s[%d]", path, i), out)
		}
	}
}

// scalarOptional are fields whose null means "unknown", not "none" (Rust
// Option<String> parity in the service profile).
var scalarOptional = map[string]bool{
	"repo_url": true, "version": true, "language": true, "last_incident": true,
	"team": true, "on_call": true,
}

// listNulls drops the paths whose last segment is a known optional scalar.
func listNulls(paths []string) []string {
	var out []string
	for _, p := range paths {
		last := p[strings.LastIndex(p, ".")+1:]
		if !scalarOptional[last] {
			out = append(out, p)
		}
	}
	return out
}

// TestEmptyResultsAreNeverNull pins RS-09: an empty answer is `[]`, never
// `null` — a client doing len(hits) crashed on memory search's `"hits": null`.
func TestEmptyResultsAreNeverNull(t *testing.T) {
	e, _ := newEngine(t)
	e.SetProjectDir(t.TempDir())
	ctx := context.Background()
	calls := []QueryRequest{
		{Action: "memory", Query: "nothing-matches-this"},
		{Action: "memory", Query: "nothing", Args: map[string]any{"command": "session_recall"}},
		{Action: "callers", Query: "NoSuchSymbol"},
		{Action: "callees", Query: "NoSuchSymbol"},
		{Action: "impact", Query: "NoSuchSymbol"},
		{Action: "ontology", Query: "x", Args: map[string]any{"cmd": "matches"}},
		{Action: "ontology", Query: "x", Args: map[string]any{"cmd": "trace"}},
		{Action: "ontology", Query: "x", Args: map[string]any{"cmd": "feature_flow"}},
		{Action: "ontology", Query: "x", Args: map[string]any{"cmd": "traceability"}},
		{Action: "ontology", Query: "x", Args: map[string]any{"cmd": "concept_search"}},
		{Action: "prd", Query: "FR-NONE"},
		{Action: "incidents", Query: "x", Args: map[string]any{"service": "svc"}},
		{Action: "env_conflicts", Query: "x", Args: map[string]any{"service": "svc"}},
		{Action: "service_context", Query: "x", Args: map[string]any{"service": "svc", "env": "local"}},
		{Action: "exact", Query: "NoSuchSymbol"},
		{Action: "fuzzy", Query: "nosuchsymbol"},
		{Action: "semantic", Query: "nosuchsymbol"},
		{Action: "session", Query: "nosess", Args: map[string]any{"command": "canvas"}},
	}
	var bad []string
	for _, c := range calls {
		out, err := e.Query(ctx, c)
		if err != nil {
			continue // an error is an answer too; only shapes are pinned here
		}
		raw, _ := json.Marshal(out)
		var v any
		_ = json.Unmarshal(raw, &v)
		var nulls []string
		findNulls(v, "", &nulls)
		nulls = listNulls(nulls)
		for _, n := range nulls {
			bad = append(bad, fmt.Sprintf("%s/%s: %s", c.Action, c.Args["cmd"], n))
		}
	}
	if len(bad) > 0 {
		t.Fatalf("null values in empty answers:\n%s", strings.Join(bad, "\n"))
	}
}
