package compress

import (
	"fmt"
	"math"
	"testing"
)

func elementSlice(n int) []any {
	out := make([]any, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, map[string]any{
			"qualified_name": fmt.Sprintf("func%d", i),
			"name":           fmt.Sprintf("func%d", i),
			"type":           "function",
		})
	}
	return out
}

func TestCompressImpactRadius(t *testing.T) {
	c := NewResponseCompressor()
	response := map[string]any{
		"start_file":               "src/main.rs",
		"max_depth":                3,
		"elements":                 elementSlice(50),
		"elements_with_confidence": elementSlice(50),
	}
	compressed := c.CompressImpactRadius(response)

	if _, ok := compressed["total_affected"]; !ok {
		t.Fatalf("compressed response missing total_affected: %+v", compressed)
	}
	if compressed["total_affected"] != 50 {
		t.Errorf("total_affected = %v, want 50", compressed["total_affected"])
	}
	top, ok := compressed["top_elements"].([]any)
	if !ok || len(top) != 20 {
		t.Errorf("top_elements = %v, want 20 entries", compressed["top_elements"])
	}
	if _, has := compressed["elements_summary"]; has {
		t.Error("when the response carries full elements, the summary must be omitted (Rust quirk)")
	}
}

func TestCompressImpactRadiusWithoutFullElements(t *testing.T) {
	c := NewResponseCompressor()
	response := map[string]any{
		"start_file":               "src/main.rs",
		"max_depth":                3,
		"elements_with_confidence": elementSlice(5),
	}
	compressed := c.CompressImpactRadius(response)
	if compressed["elements_summary"] != "5 elements total" {
		t.Errorf("elements_summary = %v, want %q", compressed["elements_summary"], "5 elements total")
	}
	if top := compressed["top_elements"].([]any); len(top) != 5 {
		t.Errorf("top_elements = %v, want all 5", compressed["top_elements"])
	}
}

func TestCompressSearchCode(t *testing.T) {
	c := NewResponseCompressor()
	response := map[string]any{"elements": elementSlice(30)}
	compressed := c.CompressSearchCode(response)

	if compressed["total_matches"] != 30 {
		t.Errorf("total_matches = %v, want 30", compressed["total_matches"])
	}
	if compressed["elements_summary"] != "Showing 20 of 30 elements" {
		t.Errorf("elements_summary = %v", compressed["elements_summary"])
	}
	if top := compressed["top_elements"].([]any); len(top) != 20 {
		t.Errorf("top_elements = %v, want 20", len(top))
	}

	small := c.CompressSearchCode(map[string]any{"elements": elementSlice(3)})
	if small["elements_summary"] != "3 elements" {
		t.Errorf("small summary = %v, want %q", small["elements_summary"], "3 elements")
	}
}

func TestCompressCallGraph(t *testing.T) {
	c := NewResponseCompressor()
	response := map[string]any{
		"function": "main",
		"callers":  elementSlice(25),
		"callees":  elementSlice(3),
	}
	compressed := c.CompressCallGraph(response)
	if compressed["caller_count"] != 25 || compressed["callee_count"] != 3 {
		t.Errorf("counts = %v/%v, want 25/3", compressed["caller_count"], compressed["callee_count"])
	}
	if callers := compressed["callers"].([]any); len(callers) != 20 {
		t.Errorf("callers = %d, want 20", len(callers))
	}
	if callees := compressed["callees"].([]any); len(callees) != 3 {
		t.Errorf("callees = %d, want 3", len(callees))
	}

	// Missing lists stay null (not empty arrays) and count as zero.
	missing := c.CompressCallGraph(map[string]any{"function": "orphan"})
	if missing["caller_count"] != 0 || missing["callee_count"] != 0 {
		t.Errorf("missing counts = %v/%v, want 0/0", missing["caller_count"], missing["callee_count"])
	}
	if missing["callers"] != nil || missing["callees"] != nil {
		t.Errorf("missing lists should be null, got %v/%v", missing["callers"], missing["callees"])
	}
}

func TestCompressNavGraph(t *testing.T) {
	c := NewResponseCompressor()
	response := map[string]any{
		"elements":      elementSlice(30),
		"relationships": []any{"a->b"},
	}
	compressed := c.CompressNavGraph(response)
	if compressed["count"] != 30 {
		t.Errorf("count = %v, want 30", compressed["count"])
	}
	if elements := compressed["elements"].([]any); len(elements) != 20 {
		t.Errorf("elements = %d, want 20", len(elements))
	}
	if rels := compressed["relationships"].([]any); len(rels) != 1 {
		t.Errorf("relationships should pass through, got %v", compressed["relationships"])
	}

	empty := c.CompressNavGraph(map[string]any{})
	if rels, ok := empty["relationships"].([]any); !ok || len(rels) != 0 {
		t.Errorf("missing relationships should default to [], got %v", empty["relationships"])
	}
}

func TestCompressDependenciesAndDependents(t *testing.T) {
	c := NewResponseCompressor()
	deps := c.CompressDependencies(map[string]any{"dependencies": elementSlice(25)})
	if deps["total"] != 25 {
		t.Errorf("total = %v, want 25", deps["total"])
	}
	if deps["_compression_note"] != "Showing 20 of 25. Use get_dependencies with compress=true for full results" {
		t.Errorf("note = %v", deps["_compression_note"])
	}

	small := c.CompressDependencies(map[string]any{"dependencies": elementSlice(2)})
	if small["_compression_note"] != "Use get_dependencies with compress=true for full results" {
		t.Errorf("small note = %v", small["_compression_note"])
	}

	dependents := c.CompressDependents(map[string]any{"dependents": elementSlice(25)})
	if dependents["total"] != 25 || dependents["_compression_note"] != "Showing 20 of 25. Use get_dependents with compress=true for full results" {
		t.Errorf("dependents = %+v", dependents)
	}
}

func TestCompressContext(t *testing.T) {
	c := NewResponseCompressor()
	response := map[string]any{
		"elements": elementSlice(30),
		"file":     "src/lib.rs",
	}
	compressed := c.CompressContext(response)
	if compressed["total_elements"] != 30 {
		t.Errorf("total_elements = %v, want 30", compressed["total_elements"])
	}
	if compressed["file"] != "src/lib.rs" {
		t.Errorf("file = %v, want passthrough", compressed["file"])
	}
	if compressed["elements_summary"] != "Showing 20 of 30 elements" {
		t.Errorf("elements_summary = %v", compressed["elements_summary"])
	}
}

func TestResponseCompressorDisabled(t *testing.T) {
	c := NewResponseCompressor().WithCompression(false)
	response := map[string]any{"elements": elementSlice(30)}
	compressed := c.CompressSearchCode(response)
	if len(compressed) != len(response) {
		t.Errorf("disabled compressor must pass responses through, got %+v", compressed)
	}
}

func TestResponseCompressorMaxElements(t *testing.T) {
	c := NewResponseCompressor().WithMaxElements(5)
	compressed := c.CompressSearchCode(map[string]any{"elements": elementSlice(30)})
	if top := compressed["top_elements"].([]any); len(top) != 5 {
		t.Errorf("top_elements = %d, want 5", len(top))
	}
	if compressed["elements_summary"] != "Showing 5 of 30 elements" {
		t.Errorf("elements_summary = %v", compressed["elements_summary"])
	}
}

func TestCompressByTool(t *testing.T) {
	c := NewResponseCompressor()
	response := map[string]any{"elements": elementSlice(30)}
	if got := c.CompressByTool("search_code", response); got["total_matches"] != 30 {
		t.Errorf("search_code dispatch failed: %+v", got)
	}
	if got := c.CompressByTool("unknown_tool", response); len(got) != len(response) {
		t.Errorf("unknown tool must pass through, got %+v", got)
	}
}

func TestResponseEstimateSavings(t *testing.T) {
	c := NewResponseCompressor()
	original := map[string]any{"elements": elementSlice(100)}
	compressed := map[string]any{"top_elements": elementSlice(1)}
	stats := c.EstimateSavings(original, compressed)
	if stats.OriginalTokens <= stats.CompressedTokens {
		t.Errorf("stats = %+v, want original > compressed", stats)
	}
	if math.Abs(stats.SavingsPercent-float64(stats.OriginalTokens-stats.CompressedTokens)/float64(stats.OriginalTokens)*100) > 1e-9 {
		t.Errorf("savings percent inconsistent with token counts: %+v", stats)
	}
	// "{}" serializes to 2 bytes -> 0 tokens at 4 chars/token.
	if got := c.EstimateSavings(map[string]any{}, map[string]any{}); got.SavingsPercent != 0 || got.OriginalTokens != 0 {
		t.Errorf("empty objects should have no tokens; got %+v", got)
	}
}
