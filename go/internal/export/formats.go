package export

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Version metadata written by JSON: the Rust exporters stamped
// env!("CARGO_PKG_VERSION"); the Go caller passes its own engine version.
const generator = "leankg"

// JSON renders the scoped export payload (Rust main.rs export_json):
// metadata + nodes + edges. exportedAtUnix is the caller's clock.
//
// Object keys come out alphabetically ordered, matching the Rust bytes:
// serde_json's Map is a BTreeMap (the Rust crate builds without
// preserve_order) and Go's encoding/json sorts map keys.
func JSON(els []store.Element, rels []store.Relationship, version string, exportedAtUnix int64) ([]byte, error) {
	nodes := make([]map[string]any, 0, len(els))
	for _, e := range els {
		nodes = append(nodes, map[string]any{
			"id":       e.QualifiedName,
			"type":     e.ElementType,
			"name":     e.Name,
			"file":     e.FilePath,
			"lines":    []int{e.LineStart, e.LineEnd},
			"language": e.Language,
		})
	}
	// Confidence renders through encoding/json's shortest-round-trip float
	// formatting: equal to serde_json/ryu for values in [0,1], unlike serde's
	// always-fractional "1.0" for whole numbers.
	edges := make([]map[string]any, 0, len(rels))
	for _, r := range rels {
		edges = append(edges, map[string]any{
			"source":     r.Source,
			"target":     r.Target,
			"type":       r.RelType,
			"confidence": r.Confidence,
		})
	}
	doc := map[string]any{
		"metadata": map[string]any{
			"generator":        generator,
			"version":          version,
			"exported_at_unix": exportedAtUnix,
			"node_count":       len(els),
			"edge_count":       len(rels),
		},
		"nodes": nodes,
		"edges": edges,
	}
	return marshalIndent(doc)
}

// streamElement is the full-graph streaming record (Rust
// export_json_streaming). cluster_id / cluster_label are always null: the Go
// store has no cluster column (the Rust engine could precompute them). The
// keys and their order are the Rust format string's.
type streamElement struct {
	QualifiedName   string  `json:"qualified_name"`
	ElementType     string  `json:"element_type"`
	Name            string  `json:"name"`
	FilePath        string  `json:"file_path"`
	LineStart       int     `json:"line_start"`
	LineEnd         int     `json:"line_end"`
	Language        string  `json:"language"`
	ClusterID       *string `json:"cluster_id"`
	ClusterLabel    *string `json:"cluster_label"`
	ParentQualified *string `json:"parent_qualified"`
	Metadata        any     `json:"metadata"`
}

type streamRelationship struct {
	Source     string  `json:"source_qualified"`
	Target     string  `json:"target_qualified"`
	RelType    string  `json:"rel_type"`
	Confidence float64 `json:"confidence"`
	Metadata   any     `json:"metadata"`
}

// WriteJSONStreaming ports export_json_streaming: the unwrapped full-graph
// JSON (`{"version":1,"kind":"leankg.graph.streaming",...}`), written element
// by element so peak memory stays O(1) instead of materializing the whole
// document. Rust's budget guard (crate::budget) has no Go equivalent, so the
// "truncated" flag can never be set here.
func WriteJSONStreaming(w io.Writer, st store.Backend) error {
	els, err := st.Elements()
	if err != nil {
		return err
	}
	rels, err := st.RelationshipsAll(maxRelationships)
	if err != nil {
		return err
	}

	out := bufio.NewWriter(w)
	line := func(format string, args ...any) error {
		_, werr := fmt.Fprintf(out, format, args...)
		return werr
	}
	if err := line("{\n  \"version\": 1,\n  \"kind\": \"leankg.graph.streaming\",\n  \"elements\": [\n"); err != nil {
		return err
	}
	for i, e := range els {
		if i > 0 {
			if err := line(",\n"); err != nil {
				return err
			}
		}
		raw, err := marshalJSON(streamElement{
			QualifiedName:   e.QualifiedName,
			ElementType:     e.ElementType,
			Name:            e.Name,
			FilePath:        e.FilePath,
			LineStart:       e.LineStart,
			LineEnd:         e.LineEnd,
			Language:        e.Language,
			ParentQualified: optional(e.ParentQualified),
			Metadata:        metadataOrNull(e.Metadata),
		})
		if err != nil {
			return err
		}
		if err := line("    %s", raw); err != nil {
			return err
		}
	}
	if err := line("\n  ],\n  \"relationships\": [\n"); err != nil {
		return err
	}
	for i, r := range rels {
		if i > 0 {
			if err := line(",\n"); err != nil {
				return err
			}
		}
		raw, err := marshalJSON(streamRelationship{
			Source:     r.Source,
			Target:     r.Target,
			RelType:    r.RelType,
			Confidence: r.Confidence,
			Metadata:   metadataOrNull(r.Metadata),
		})
		if err != nil {
			return err
		}
		if err := line("    %s", raw); err != nil {
			return err
		}
	}
	if err := line("\n  ]\n}\n"); err != nil {
		return err
	}
	return out.Flush()
}

// Dot renders the Graphviz digraph (Rust main.rs export_dot): nodes grouped
// into one subgraph per file (files sorted by path) followed by the edges.
// The Rust writer applied no escaping, so neither does this one.
func Dot(els []store.Element, rels []store.Relationship) string {
	var b strings.Builder
	b.WriteString("digraph LeanKG {\n  rankdir=LR;\n  node [shape=box, style=rounded, fontname=\"Helvetica\"];\n  edge [fontname=\"Helvetica\", fontsize=10];\n\n")

	byFile := make(map[string][]store.Element)
	files := make([]string, 0)
	for _, e := range els {
		if _, ok := byFile[e.FilePath]; !ok {
			files = append(files, e.FilePath)
		}
		byFile[e.FilePath] = append(byFile[e.FilePath], e)
	}
	sort.Strings(files)

	for _, file := range files {
		fmt.Fprintf(&b, "  subgraph cluster_%s {\n    label=\"%s\";\n    style=dashed;\n    color=gray;\n",
			dotID(file), file)
		for _, e := range byFile[file] {
			fmt.Fprintf(&b, "    %s [label=\"%s (%s)\"];\n", dotID(e.QualifiedName), e.Name, e.ElementType)
		}
		b.WriteString("  }\n\n")
	}
	for _, r := range rels {
		fmt.Fprintf(&b, "  %s -> %s [label=\"%s\"];\n", dotID(r.Source), dotID(r.Target), r.RelType)
	}
	b.WriteString("}\n")
	return b.String()
}

// Mermaid renders the `graph LR` diagram (Rust main.rs export_mermaid): one
// line per relationship, labelled with the short (post-`::`) symbol names.
func Mermaid(rels []store.Relationship) string {
	var b strings.Builder
	b.WriteString("graph LR\n")
	for _, r := range rels {
		fmt.Fprintf(&b, "    %s[\"%s\"] -->|%s| %s[\"%s\"]\n",
			dotID(r.Source), shortName(r.Source), r.RelType, dotID(r.Target), shortName(r.Target))
	}
	return b.String()
}

// dotID is the Rust dot/mermaid identifier sanitizer (same replacement chain,
// same order).
func dotID(s string) string {
	r := strings.NewReplacer("::", "__", "/", "_", ".", "_", "-", "_", " ", "_")
	return r.Replace(s)
}

func shortName(qn string) string {
	if i := strings.LastIndex(qn, "::"); i >= 0 {
		return qn[i+2:]
	}
	return qn
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func metadataOrNull(m map[string]any) any {
	if len(m) == 0 {
		return nil
	}
	return m
}

// marshalIndent is marshalJSON plus the serde_json::to_string_pretty layout
// (two-space indent, no trailing newline).
func marshalIndent(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// marshalJSON is the compact single-line form (no HTML escaping, so the bytes
// match serde_json).
func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
