package compress

import (
	"encoding/json"
	"fmt"
)

// CompressionStats reports the token impact of a response compression (Rust
// response::CompressionStats).
type CompressionStats struct {
	OriginalTokens   int     `json:"original_tokens"`
	CompressedTokens int     `json:"compressed_tokens"`
	SavingsPercent   float64 `json:"savings_percent"`
}

// ResponseCompressor shapes query responses down to a bounded element count
// (Rust response::ResponseCompressor).
type ResponseCompressor struct {
	maxElements     int
	maxDepth        int
	compressEnabled bool
}

// NewResponseCompressor builds the default compressor (20 elements, depth 3,
// enabled).
func NewResponseCompressor() *ResponseCompressor {
	return &ResponseCompressor{maxElements: 20, maxDepth: 3, compressEnabled: true}
}

// WithMaxElements sets the element cap.
func (c *ResponseCompressor) WithMaxElements(max int) *ResponseCompressor {
	c.maxElements = max
	return c
}

// WithMaxDepth sets the recorded max depth.
func (c *ResponseCompressor) WithMaxDepth(max int) *ResponseCompressor {
	c.maxDepth = max
	return c
}

// WithCompression toggles compression; disabled returns responses untouched.
func (c *ResponseCompressor) WithCompression(enabled bool) *ResponseCompressor {
	c.compressEnabled = enabled
	return c
}

// MaxElements returns the configured element cap.
func (c *ResponseCompressor) MaxElements() int { return c.maxElements }

// CompressByTool dispatches a tool response to its shaper (Rust
// handler.rs maybe_compress tool match); unknown tools pass through.
func (c *ResponseCompressor) CompressByTool(tool string, response map[string]any) map[string]any {
	switch tool {
	case "get_impact_radius":
		return c.CompressImpactRadius(response)
	case "get_call_graph":
		return c.CompressCallGraph(response)
	case "search_code":
		return c.CompressSearchCode(response)
	case "get_nav_graph":
		return c.CompressNavGraph(response)
	case "get_dependencies":
		return c.CompressDependencies(response)
	case "get_dependents":
		return c.CompressDependents(response)
	case "get_context":
		return c.CompressContext(response)
	default:
		return response
	}
}

// CompressImpactRadius trims elements_with_confidence (Rust
// ResponseCompressor::compress_impact_radius).
func (c *ResponseCompressor) CompressImpactRadius(response map[string]any) map[string]any {
	if !c.compressEnabled {
		return response
	}
	elements := jsonArray(response, "elements_with_confidence")
	totalCount := len(elements)
	topElements := topN(elements, c.maxElements)

	note := "Use get_impact_radius with compress=true for full results"
	if _, hasElements := response["elements"]; hasElements {
		return map[string]any{
			"start_file":        response["start_file"],
			"max_depth":         response["max_depth"],
			"total_affected":    totalCount,
			"top_elements":      topElements,
			"_compression_note": note,
		}
	}
	return map[string]any{
		"start_file":        response["start_file"],
		"max_depth":         response["max_depth"],
		"total_affected":    totalCount,
		"elements_summary":  fmt.Sprintf("%d elements total", totalCount),
		"top_elements":      topElements,
		"_compression_note": note,
	}
}

// CompressCallGraph trims callers/callees (Rust
// ResponseCompressor::compress_call_graph).
func (c *ResponseCompressor) CompressCallGraph(response map[string]any) map[string]any {
	if !c.compressEnabled {
		return response
	}
	callers := jsonArray(response, "callers")
	callees := jsonArray(response, "callees")

	var callersValue, calleesValue any
	if callers != nil {
		callersValue = topN(callers, c.maxElements)
	}
	if callees != nil {
		calleesValue = topN(callees, c.maxElements)
	}

	return map[string]any{
		"function":          response["function"],
		"caller_count":      len(callers),
		"callee_count":      len(callees),
		"callers":           callersValue,
		"callees":           calleesValue,
		"_compression_note": "Use get_call_graph with compress=true for full results",
	}
}

// CompressSearchCode trims elements (Rust
// ResponseCompressor::compress_search_code).
func (c *ResponseCompressor) CompressSearchCode(response map[string]any) map[string]any {
	if !c.compressEnabled {
		return response
	}
	elements := jsonArray(response, "elements")
	totalCount := len(elements)
	topElements := topN(elements, c.maxElements)

	summary := fmt.Sprintf("%d elements", totalCount)
	if totalCount > c.maxElements {
		summary = fmt.Sprintf("Showing %d of %d elements", c.maxElements, totalCount)
	}
	return map[string]any{
		"total_matches":     totalCount,
		"elements_summary":  summary,
		"top_elements":      topElements,
		"_compression_note": "Use search_code with compress=true for full results",
	}
}

// CompressNavGraph trims elements, passing relationships through (Rust
// ResponseCompressor::compress_nav_graph).
func (c *ResponseCompressor) CompressNavGraph(response map[string]any) map[string]any {
	if !c.compressEnabled {
		return response
	}
	elements := jsonArray(response, "elements")
	top := topN(elements, c.maxElements)

	relationships := response["relationships"]
	if relationships == nil {
		relationships = []any{}
	}
	return map[string]any{
		"count":             len(elements),
		"elements":          top,
		"relationships":     relationships,
		"_compression_note": "Use get_nav_graph with full file path for complete results",
	}
}

// CompressDependencies trims dependencies (Rust
// ResponseCompressor::compress_dependencies).
func (c *ResponseCompressor) CompressDependencies(response map[string]any) map[string]any {
	if !c.compressEnabled {
		return response
	}
	deps := jsonArray(response, "dependencies")
	total := len(deps)
	top := topN(deps, c.maxElements)

	// Faithful port: the Rust message hardcodes "20" regardless of the
	// configured max_elements.
	note := "Use get_dependencies with compress=true for full results"
	if total > c.maxElements {
		note = fmt.Sprintf("Showing 20 of %d. Use get_dependencies with compress=true for full results", total)
	}
	return map[string]any{
		"total":             total,
		"dependencies":      top,
		"_compression_note": note,
	}
}

// CompressDependents trims dependents (Rust
// ResponseCompressor::compress_dependents).
func (c *ResponseCompressor) CompressDependents(response map[string]any) map[string]any {
	if !c.compressEnabled {
		return response
	}
	deps := jsonArray(response, "dependents")
	total := len(deps)
	top := topN(deps, c.maxElements)

	note := "Use get_dependents with compress=true for full results"
	if total > c.maxElements {
		note = fmt.Sprintf("Showing 20 of %d. Use get_dependents with compress=true for full results", total)
	}
	return map[string]any{
		"total":             total,
		"dependents":        top,
		"_compression_note": note,
	}
}

// CompressContext trims context elements (Rust
// ResponseCompressor::compress_context).
func (c *ResponseCompressor) CompressContext(response map[string]any) map[string]any {
	if !c.compressEnabled {
		return response
	}
	elements := jsonArray(response, "elements")
	totalCount := len(elements)
	topElements := topN(elements, c.maxElements)

	summary := fmt.Sprintf("%d elements", totalCount)
	if totalCount > c.maxElements {
		summary = fmt.Sprintf("Showing %d of %d elements", c.maxElements, totalCount)
	}
	return map[string]any{
		"total_elements":    totalCount,
		"elements_summary":  summary,
		"top_elements":      topElements,
		"file":              response["file"],
		"_compression_note": "Use get_context with compress=true for full results",
	}
}

// EstimateSavings reports the token delta between two JSON payloads (Rust
// ResponseCompressor::estimate_savings).
func (c *ResponseCompressor) EstimateSavings(original, compressed any) CompressionStats {
	originalBytes, _ := json.Marshal(original)
	compressedBytes, _ := json.Marshal(compressed)
	originalTokens := len(originalBytes) / 4
	compressedTokens := len(compressedBytes) / 4

	savingsPercent := 0.0
	if originalTokens > 0 {
		savingsPercent = float64(originalTokens-compressedTokens) / float64(originalTokens) * 100.0
	}
	return CompressionStats{
		OriginalTokens:   originalTokens,
		CompressedTokens: compressedTokens,
		SavingsPercent:   savingsPercent,
	}
}

// jsonArray returns a JSON array field, or nil when absent/not an array.
func jsonArray(m map[string]any, key string) []any {
	if m == nil {
		return nil
	}
	if v, ok := m[key].([]any); ok {
		return v
	}
	return nil
}

func topN(list []any, n int) []any {
	if list == nil {
		return nil
	}
	if len(list) > n {
		return list[:n:n]
	}
	return list
}
