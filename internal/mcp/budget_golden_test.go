package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/budget"
	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestBudgetNeverDropsPrimaryPayload is the RS-04 golden: REAL engine answers
// for every hit-returning route, at limits that overflow every cap, go through
// the same core.EnforceBudget the MCP and REST handlers use. The primary list must survive
// non-empty, the envelope keys must survive, and the delivered size must fit
// the cap. The validation run found pinned semantic answers with no `hits`
// and a full `import read` with no `content`.
func TestBudgetNeverDropsPrimaryPayload(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(dir, ".leankg", "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("widget pipeline renders the frame and flushes buffers ", 20) // ~1.1 KB
	var els []store.Element
	for i := range 300 {
		els = append(els, store.Element{
			QualifiedName: fmt.Sprintf("pkg/f%03d.go::Widget", i), ElementType: "function",
			Name: "Widget", FilePath: fmt.Sprintf("pkg/f%03d.go", i), Language: "go",
			Content: fmt.Sprintf("func Widget%d() {\n// %s\n}", i, body),
		})
	}
	if err := st.UpsertElements(context.Background(), els); err != nil {
		t.Fatal(err)
	}
	p := embed.Deterministic(384)
	if _, err := embed.Run(context.Background(), st, p, "full"); err != nil {
		t.Fatal(err)
	}
	e := core.New(st, core.Options{Embedder: p, ProjectDir: dir})

	routes := []struct{ action, query, list string }{
		{"", "Widget", "hits"},                         // ladder, L1 (300 exact matches)
		{"", "widget pipeline renders frames", "hits"}, // ladder, prose
		{"search", "widget pipeline", "hits"},
		{"exact", "Widget", "hits"},
		{"fuzzy", "widget pipeline", "hits"},
		{"semantic", "render the widget frame", "hits"},
	}
	for _, r := range routes {
		for _, limit := range []int{10, 20, 100, 100000} {
			out, err := e.Query(context.Background(), core.QueryRequest{Action: r.action, Query: r.query, Limit: limit})
			if err != nil {
				t.Fatalf("%q/%d: %v", r.action, limit, err)
			}
			shaped, _ := core.EnforceBudget(out, r.action, 0)
			raw, _ := json.Marshal(shaped)
			var obj map[string]any
			if err := json.Unmarshal(raw, &obj); err != nil {
				t.Fatalf("%q/%d: not an object: %s", r.action, limit, raw)
			}
			list, _ := obj[r.list].([]any)
			if len(list) == 0 {
				t.Fatalf("action %q limit %d: primary list %q dropped or empty; keys=%v", r.action, limit, r.list, keys(obj))
			}
			for _, k := range []string{"query", "retrieval", "freshness"} {
				if _, ok := obj[k]; !ok {
					t.Fatalf("action %q limit %d: envelope key %q dropped", r.action, limit, k)
				}
			}
			key := r.action
			if key == "" {
				key = "search"
			}
			if max := (budget.TokenBudget{}).MaxTokensForTool(key); max > 0 && len(raw)/budget.TokenCharsPerToken > max {
				t.Fatalf("action %q limit %d: %d tokens delivered over the %d cap", r.action, limit, len(raw)/budget.TokenCharsPerToken, max)
			}
		}
	}

	// A full read of a large file keeps its (cut) content.
	big := strings.Repeat("package big\n// a long comment line for the reader budget test\n", 2000)
	if err := os.WriteFile(filepath.Join(dir, "big.go"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := e.Import(context.Background(), core.ImportRequest{Action: "read", Path: "big.go", Args: map[string]any{"mode": "full"}})
	if err != nil {
		t.Fatal(err)
	}
	shaped, _ := core.EnforceBudget(out, "read", 0)
	raw, _ := json.Marshal(shaped)
	var obj map[string]any
	_ = json.Unmarshal(raw, &obj)
	if c, _ := obj["content"].(string); len(c) < 1000 {
		t.Fatalf("full read lost its content (len %d); keys=%v", len(c), keys(obj))
	}
}

// TestBudgetKeepsGraphProvenance pins a graph verb answer through the budget
// every transport applies at the action's table cap: the rung that answered
// (graph), its reason and the freshness survive, so the envelope an agent
// reads still says where the answer came from. The cap is not forced low on
// purpose: a squeeze that exhausts the top-level keys drops retrieval.reason
// (internal/budget/tokens.go, shrinkObject), and no test pins that case yet.
func TestBudgetKeepsGraphProvenance(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(dir, ".leankg", "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertElements(context.Background(), []store.Element{
		{QualifiedName: "src/alpha.go::Alpha", ElementType: "function", Name: "Alpha", FilePath: "src/alpha.go", Language: "go"},
		{QualifiedName: "src/beta.go::Beta", ElementType: "function", Name: "Beta", FilePath: "src/beta.go", Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertRelationships(context.Background(), []store.Relationship{
		{Source: "src/alpha.go::Alpha", Target: "src/beta.go::Beta", RelType: "calls", Confidence: 1},
	}); err != nil {
		t.Fatal(err)
	}
	e := core.New(st, core.Options{ProjectDir: dir})
	out, err := e.Query(context.Background(), core.QueryRequest{Action: "callees", Query: "src/alpha.go::Alpha"})
	if err != nil {
		t.Fatal(err)
	}
	shaped, _ := core.EnforceBudget(out, "callees", 0)
	raw, _ := json.Marshal(shaped)
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("not an object: %s", raw)
	}
	r, _ := obj["retrieval"].(map[string]any)
	if r["rung"] != "graph" || r["reason"] != "callees from seed src/alpha.go::Alpha" {
		t.Fatalf("retrieval after budget = %v; want graph / callees from seed src/alpha.go::Alpha", r)
	}
	if obj["freshness"] == nil {
		t.Fatalf("freshness dropped by the budget: keys=%v", keys(obj))
	}

	// The same answer squeezed below what it needs: the cap must really cut it,
	// and the access block still says who asked and which project answered.
	squeezed, stats := core.EnforceBudget(out, "callees", 5)
	if !stats.Truncated {
		t.Fatalf("cap 5 did not squeeze the answer; the check below would prove nothing")
	}
	raw, _ = json.Marshal(squeezed)
	obj = map[string]any{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("squeezed answer not an object: %s", raw)
	}
	want := map[string]any{"role": "admin", "via": "local", "restricted": false, "project": filepath.Base(dir)}
	if a, _ := obj["access"].(map[string]any); !reflect.DeepEqual(a, want) {
		t.Fatalf("access after squeeze = %v; want %v", obj["access"], want)
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
