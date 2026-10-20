package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/budget"
	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestBudgetNeverDropsPrimaryPayload is the RS-04 golden: REAL engine answers
// for every hit-returning route, at limits that overflow every cap, go through
// the same enforceBudget the MCP handlers use. The primary list must survive
// non-empty, the envelope keys must survive, and the delivered size must fit
// the cap. The validation run found pinned semantic answers with no `hits`
// and a full `import read` with no `content`.
func TestBudgetNeverDropsPrimaryPayload(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
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
	if err := st.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
	p := embed.Deterministic(384)
	if _, err := embed.Run(context.Background(), st, p, "full"); err != nil {
		t.Fatal(err)
	}
	e := core.New(st, nil, core.QueryEmbedderFromProvider(p))
	e.SetProjectDir(dir)

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
			shaped := enforceBudget(out, r.action)
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
	raw, _ := json.Marshal(enforceBudget(out, "read"))
	var obj map[string]any
	_ = json.Unmarshal(raw, &obj)
	if c, _ := obj["content"].(string); len(c) < 1000 {
		t.Fatalf("full read lost its content (len %d); keys=%v", len(c), keys(obj))
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
