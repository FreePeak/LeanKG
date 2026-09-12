package budget

import "encoding/json"

// TokenBudget caps a JSON response by tool name, ported from
// src/mcp/token_budget.rs. One token is approximated as 4 serialized bytes,
// the same heuristic the Rust engine used.
//
// TokenBudget is a namespace for its methods rather than a struct, matching the
// Rust `pub struct TokenBudget;` unit type.
type TokenBudget struct{}

// TokenCharsPerToken is the serialized-bytes-per-token approximation.
const TokenCharsPerToken = 4

// CountTokens approximates the token cost of a JSON value.
func (TokenBudget) CountTokens(v any) int { return countTokens(v) }

// ToolBudget maps a tool (legacy MCP tool name or the equivalent Go query
// action) to its response token cap. A cap of 0 means uncapped.
type ToolBudget struct {
	MaxTokens int
	Actions   []string
}

// ToolBudgets is the tool -> budget table, in presentation order.
//
// The first thirteen rows are the Rust `max_tokens_for_tool` arms (legacy MCP
// tool names; unknown names get DefaultMaxTokens). The tables then extend the
// same map to the surface this Go engine actually serves: the 3-tool MCP
// registry routes work through the query tool's action names
// (core.QueryRequest.Action), so `semantic_search` is reached as action
// `semantic` and must not silently fall back to the 1000-token default.
var ToolBudgets = []ToolBudget{
	{MaxTokens: 800, Actions: []string{"get_service_context"}},
	{MaxTokens: 6000, Actions: []string{"get_impact_radius", "impact"}},
	{MaxTokens: 2000, Actions: []string{"query_incidents"}},
	{MaxTokens: 2000, Actions: []string{"find_env_conflicts"}},
	{MaxTokens: 2000, Actions: []string{"trace_call_chain"}},
	{MaxTokens: 2000, Actions: []string{"semantic_search", "semantic"}},
	{MaxTokens: 4000, Actions: []string{"kg_context", "context"}},
	{MaxTokens: 4000, Actions: []string{"kg_trace_workflow"}},
	{MaxTokens: 4000, Actions: []string{"kg_ontology_status", "ontology"}},
	{MaxTokens: 4000, Actions: []string{"get_clusters"}},
	{MaxTokens: 4000, Actions: []string{"get_doc_tree"}},
	{MaxTokens: 4000, Actions: []string{"get_code_tree"}},
	{MaxTokens: 4000, Actions: []string{"get_call_graph", "callers", "callees"}},
	{MaxTokens: 4000, Actions: []string{"search_code", "search", "exact", "element", "fuzzy", "pattern"}},
	{MaxTokens: 2000, Actions: []string{"query_graph"}},
	{MaxTokens: 2000, Actions: []string{"get_dependencies"}},
	{MaxTokens: 2000, Actions: []string{"get_dependents"}},
	// Go actions beyond the Rust table.
	{MaxTokens: 2000, Actions: []string{"status"}},
	{MaxTokens: 2000, Actions: []string{"memory"}},
	{MaxTokens: 2000, Actions: []string{"session"}},
	{MaxTokens: 2000, Actions: []string{"path"}},
	{MaxTokens: 2000, Actions: []string{"languages"}},
	{MaxTokens: 4000, Actions: []string{"explain"}},
	{MaxTokens: 4000, Actions: []string{"lsp"}},
	// Envelope tool names are unbounded: they carry the caller's chosen action,
	// and the action name is what the table caps. Leaving them unlisted would
	// silently hold every router call to the 1000-token default and re-truncate
	// an already-compliant action response.
	{MaxTokens: 0, Actions: []string{"query", "import"}},
}

// DefaultMaxTokens is the cap for tools absent from ToolBudgets.
const DefaultMaxTokens = 1000

// MaxTokensForTool resolves the cap for a tool name (legacy MCP name or Go
// query action). Unknown names fall back to DefaultMaxTokens; uncapped tools
// (the router envelopes) return 0.
func (TokenBudget) MaxTokensForTool(tool string) int {
	for _, tb := range ToolBudgets {
		for _, a := range tb.Actions {
			if a == tool {
				return tb.MaxTokens
			}
		}
	}
	return DefaultMaxTokens
}

// CappedTools returns every capped tool name, in table order.
func (TokenBudget) CappedTools() []string {
	var out []string
	for _, tb := range ToolBudgets {
		if tb.MaxTokens <= 0 {
			continue
		}
		out = append(out, tb.Actions...)
	}
	return out
}

// BudgetMarkerKey is the response key that carries the budget report.
const BudgetMarkerKey = "_token_budget"

// TokenReport is the value stored under BudgetMarkerKey.
type TokenReport struct {
	Max                int  `json:"max"`
	Actual             int  `json:"actual"`
	PreTruncationToken int  `json:"pre_truncation_tokens"`
	Truncated          bool `json:"truncated"`
}

// Stats is the savings accounting for one Apply call: how many tokens the
// response carried before enforcement, how many it carries after, and whether
// the payload was structurally trimmed to get there.
type Stats struct {
	Tool               string
	Max                int
	PreTruncationToken int
	Actual             int
	Truncated          bool
}

// SavedTokens is the token delta between the pre-truncation response and the
// enforced one (0 when nothing was dropped).
func (s Stats) SavedTokens() int {
	if s.PreTruncationToken <= s.Actual {
		return 0
	}
	return s.PreTruncationToken - s.Actual
}

// SavedPercent is the percentage of the pre-truncation response that
// enforcement removed (0 when nothing was dropped).
func (s Stats) SavedPercent() float64 {
	if s.PreTruncationToken <= 0 || s.PreTruncationToken <= s.Actual {
		return 0
	}
	return float64(s.SavedTokens()) / float64(s.PreTruncationToken) * 100
}

// Apply enforces the tool's token cap on a JSON response.
//
// Under budget the value is returned untouched (no marker). Over budget the
// payload is structurally trimmed (arrays keep their longest fitting prefix,
// objects shed non-payload keys), the post-truncation size is recorded as
// `actual` (issue #300: never the pre-truncation count) and the report is
// attached as a `_token_budget` object. A non-object response is trimmed
// recursively but cannot carry the marker. Tools mapped to 0 are uncapped and
// pass through untouched.
func (TokenBudget) Apply(v any, tool string) (any, Stats) {
	maxTokens := TokenBudget{}.MaxTokensForTool(tool)
	stats := Stats{Tool: tool, Max: maxTokens}
	stats.PreTruncationToken = countTokens(v)
	if maxTokens <= 0 || stats.PreTruncationToken <= maxTokens {
		stats.Actual = stats.PreTruncationToken
		return v, stats
	}

	budget := maxTokens
	if _, isObject := v.(map[string]any); isObject {
		// Reserve room for the report itself so the delivered payload honors
		// the cap instead of the Rust original's pre-marker measurement.
		if reserve := markerReserveTokens(); budget > reserve {
			budget -= reserve
		}
	}
	truncated := truncateValue(&v, budget)
	stats.Truncated = truncated

	obj, isObject := v.(map[string]any)
	if !isObject {
		stats.Actual = countTokens(v)
		return v, stats
	}
	// `actual` is the size the caller actually receives, report included. The
	// count is fixed-pointed: digits in `actual` shift the report's own width.
	stats.Actual = countTokens(obj)
	for i := 0; i < 2; i++ {
		obj[BudgetMarkerKey] = TokenReport{
			Max:                maxTokens,
			Actual:             stats.Actual,
			PreTruncationToken: stats.PreTruncationToken,
			Truncated:          truncated,
		}
		if counted := countTokens(obj); counted != stats.Actual {
			stats.Actual = counted
			continue
		}
		break
	}
	return obj, stats
}

// markerReserveTokens is the space held back for the largest possible
// `_token_budget` report (widest counts plus key overhead).
func markerReserveTokens() int {
	probe := map[string]any{BudgetMarkerKey: TokenReport{
		Max: 99999999, Actual: 99999999, PreTruncationToken: 99999999, Truncated: true,
	}}
	return countTokens(probe) + 1
}

// countTokens approximates the token cost of a JSON value or subtree.
func countTokens(v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(b) / TokenCharsPerToken
}

// truncateValue returns true when anything inside v was dropped, writing the
// trimmed value back through the pointer.
func truncateValue(v *any, maxTokens int) bool {
	if countTokens(*v) <= maxTokens {
		return false
	}
	switch typed := (*v).(type) {
	case []any:
		kept, dropped := truncateArray(typed, maxTokens)
		*v = kept
		return dropped
	case map[string]any:
		return truncateObject(typed, maxTokens)
	default:
		return false
	}
}

// itemBytes is the exact compact serialized size of one value.
func itemBytes(v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(b)
}

// entryBytes is the exact serialized size of `"key":value` plus its trailing
// comma slot.
func entryBytes(key string, v any) int {
	kb, err := json.Marshal(key)
	if err != nil {
		kb = []byte(`"` + key + `"`)
	}
	return len(kb) + 1 + itemBytes(v) + 1
}

// objectBytes is the exact serialized size of a compact JSON object.
func objectBytes(obj map[string]any) int {
	if len(obj) == 0 {
		return 2 // "{}"
	}
	entries := 0
	for k, v := range obj {
		entries += entryBytes(k, v)
	}
	return 2 + entries - 1 // drop the last comma slot
}

// truncateArray keeps the longest leading prefix of arr that fits the byte
// budget and reports whether items were dropped. When the whole array fits on
// its own, the over-budget part is the parent object and nothing is dropped
// here.
//
// One pass, O(n): per-item sizes are summed once and the prefix is scanned
// (Rust R2b perf fix — the old shape re-serialized the array once per dropped
// item and wedged the server on 24k-finding responses).
func truncateArray(arr []any, maxTokens int) ([]any, bool) {
	budget := maxTokens * TokenCharsPerToken
	cum := make([]int, 0, len(arr))
	acc := 2 // "["
	for _, item := range arr {
		acc += itemBytes(item) + 1
		cum = append(cum, acc)
	}
	keep := 0
	for keep < len(cum) && cum[keep] <= budget {
		keep++
	}
	if keep == len(arr) {
		return arr, false
	}
	if keep < 1 {
		keep = 1 // never hand back a silently emptied payload
	}
	return arr[:keep:keep], true
}

// protectedKeys are never dropped during object truncation: they carry the
// query identity and the primary payload, so the response keeps its shape.
var protectedKeys = map[string]bool{
	"service": true, "env": true, "query": true, "file": true,
	"function": true, "element": true, "id": true, "results": true,
	"incidents": true, "conflicts": true, "calls": true, "called_by": true,
	"open_incidents": true, "recent_incidents": true, "count": true,
	// Primary payload keys of full-scan tools must survive so the response
	// keeps its shape after truncation:
	"findings": true, "relationships": true, "elements": true,
}

// truncateObject recursively trims children, then removes non-protected keys
// (sorted, so behavior is deterministic — Go map order is randomized) until the
// object fits.
//
// ponytail: objectBytes is recomputed after each removal, so a pathological
// object with thousands of removable keys is O(k*n) instead of the Rust O(n)
// running total. Real payloads carry ~a dozen removable keys; if a mega-object
// ever shows up, carry a running byte total like the Rust original.
func truncateObject(obj map[string]any, maxTokens int) bool {
	truncated := false
	for _, key := range sortedKeys(obj) {
		child := obj[key]
		if child == nil {
			continue
		}
		if truncateValue(&child, maxTokens) {
			obj[key] = child // write the trimmed child back; a range copy would drop it
			truncated = true
		}
	}

	removable := make([]string, 0, len(obj))
	for k := range obj {
		if !protectedKeys[k] && k != BudgetMarkerKey {
			removable = append(removable, k)
		}
	}
	sortStrings(removable)

	budget := maxTokens * TokenCharsPerToken
	for _, key := range removable {
		if objectBytes(obj) <= budget {
			break
		}
		delete(obj, key)
		truncated = true
	}
	return truncated
}

// sortedKeys returns obj's keys in sorted order.
func sortedKeys(obj map[string]any) []string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}

// sortStrings is a tiny insertion sort: key slices are at most the object
// arity and normally near-sorted, and this keeps the package's imports to
// encoding/json alone.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
