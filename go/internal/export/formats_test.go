package export

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

func TestJSONLayout(t *testing.T) {
	els := []store.Element{
		el("a.rs::f", "f", "function", "a.rs", 1),
	}
	rels := []store.Relationship{
		rel("a.rs::f", "b.rs::g", "calls"),
	}
	raw, err := JSON(els, rels, "9.9.9", 1700000000)
	if err != nil {
		t.Fatalf("json: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	meta := doc["metadata"].(map[string]any)
	if meta["generator"] != "leankg" || meta["version"] != "9.9.9" {
		t.Fatalf("metadata: %+v", meta)
	}
	if meta["node_count"] != float64(1) || meta["edge_count"] != float64(1) {
		t.Fatalf("counts: %+v", meta)
	}
	nodes := doc["nodes"].([]any)
	n := nodes[0].(map[string]any)
	if n["id"] != "a.rs::f" || n["type"] != "function" || n["language"] != "rust" {
		t.Fatalf("node: %+v", n)
	}
	lines := n["lines"].([]any)
	if lines[0] != float64(1) || lines[1] != float64(6) {
		t.Fatalf("lines: %v", lines)
	}
	edges := doc["edges"].([]any)
	e := edges[0].(map[string]any)
	if e["source"] != "a.rs::f" || e["target"] != "b.rs::g" || e["type"] != "calls" {
		t.Fatalf("edge: %+v", e)
	}

	// serde_json::to_string_pretty is alphabetical + 2-space indent.
	if !strings.Contains(string(raw), "  \"edges\": [") || !strings.Contains(string(raw), "  \"metadata\": {") {
		t.Fatalf("key order/indent mismatch:\n%s", raw)
	}
	if strings.HasSuffix(string(raw), "\n") {
		t.Fatal("no trailing newline in the Rust format")
	}
}

func TestWriteJSONStreamingShape(t *testing.T) {
	st := newFixture(t, testFixElements, testFixRels)

	var sb strings.Builder
	if err := WriteJSONStreaming(&sb, st); err != nil {
		t.Fatalf("streaming: %v", err)
	}
	raw := sb.String()

	if !strings.HasPrefix(raw, "{\n  \"version\": 1,\n  \"kind\": \"leankg.graph.streaming\",\n  \"elements\": [\n") {
		t.Fatalf("header mismatch:\n%.200s", raw)
	}
	if !strings.Contains(raw, "\n  ],\n  \"relationships\": [\n") {
		t.Fatal("section separator missing")
	}
	if !strings.HasSuffix(raw, "\n  ]\n}\n") {
		t.Fatalf("footer mismatch: %q", raw[len(raw)-40:])
	}

	var doc struct {
		Version       int `json:"version"`
		Kind          string
		Elements      []map[string]any
		Relationships []map[string]any
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	if doc.Version != 1 || doc.Kind != "leankg.graph.streaming" {
		t.Fatalf("header fields: %+v", doc)
	}
	if len(doc.Elements) != 4 || len(doc.Relationships) != 4 {
		t.Fatalf("counts: %d elements, %d rels", len(doc.Elements), len(doc.Relationships))
	}
	// The Rust format string emitted a fixed key order per record.
	if !strings.Contains(raw, `"qualified_name":"src/app.rs::hub"`) {
		t.Fatalf("element record layout:\n%s", raw)
	}
	if !strings.Contains(raw, `"cluster_id":null`) || !strings.Contains(raw, `"cluster_label":null`) {
		t.Fatalf("cluster keys must be present as null:\n%s", raw)
	}
	if !strings.Contains(raw, `"metadata":null`) {
		t.Fatalf("metadata null default:\n%s", raw)
	}
}

func TestDotLayout(t *testing.T) {
	els := []store.Element{
		el("src/a.rs::f", "f", "function", "src/a.rs", 1),
		el("src/b.rs::g", "g", "function", "src/b.rs", 1),
	}
	rels := []store.Relationship{rel("src/a.rs::f", "src/b.rs::g", "calls")}

	dot := Dot(els, rels)
	if !strings.HasPrefix(dot, "digraph LeanKG {\n  rankdir=LR;\n") {
		t.Fatalf("header: %q", dot)
	}
	if !strings.Contains(dot, "  subgraph cluster_src_a_rs {\n    label=\"src/a.rs\";\n    style=dashed;\n    color=gray;\n") {
		t.Fatalf("subgraph block:\n%s", dot)
	}
	if !strings.Contains(dot, "    src_a_rs__f [label=\"f (function)\"];\n") {
		t.Fatalf("node line:\n%s", dot)
	}
	if !strings.Contains(dot, "  src_a_rs__f -> src_b_rs__g [label=\"calls\"];\n") {
		t.Fatalf("edge line:\n%s", dot)
	}
	if !strings.HasSuffix(dot, "}\n") {
		t.Fatalf("footer: %q", dot)
	}
	// Files render in path order regardless of insertion order.
	if strings.Index(dot, "src/a.rs") > strings.Index(dot, "src/b.rs") {
		t.Fatalf("subgraphs must be sorted by file:\n%s", dot)
	}
}

func TestMermaidLayout(t *testing.T) {
	rels := []store.Relationship{rel("src/a.rs::do_work", "src/b.rs::run", "calls")}
	m := Mermaid(rels)
	want := "graph LR\n    src_a_rs__do_work[\"do_work\"] -->|calls| src_b_rs__run[\"run\"]\n"
	if m != want {
		t.Fatalf("mermaid:\n got: %q\nwant: %q", m, want)
	}
}
