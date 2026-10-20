package budget

import (
	"encoding/json"
	"maps"
	"slices"
)

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
	{MaxTokens: 2000, Actions: []string{"semantic_search"}},
	// The Go semantic rung answers the same hit shape as search/fuzzy, so it
	// shares their cap (RS-04: at 2000 a ten-hit answer lost every hit).
	{MaxTokens: 4000, Actions: []string{"semantic"}},
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
	// Reader-mode compression: the caller chose the mode (full, map,
	// signatures…), so the answer IS the file text; under the 1000-token
	// default a full read of any file over ~4 KB lost its content.
	{MaxTokens: 8000, Actions: []string{"read", "compress"}},
	// A portfolio answer is the union of up to MaxRepos children's own results
	// (issue #376), so its natural size is N times a single-project response.
	// Under the 1000-token default a two-project fan-out was truncated to its
	// summary with zero hits carried — the fleet read is useless without the
	// children, so it gets the widest cap on the table.
	{MaxTokens: 12000, Actions: []string{"portfolio"}},
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
	Max                int                 `json:"max"`
	Actual             int                 `json:"actual"`
	PreTruncationToken int                 `json:"pre_truncation_tokens"`
	Truncated          bool                `json:"truncated"`
	Trimmed            map[string]TrimInfo `json:"trimmed,omitempty"`
	Dropped            []string            `json:"dropped,omitempty"`
	Hint               string              `json:"hint,omitempty"`
}

// TrimInfo reports one list cut by the budget: how many items the caller
// received out of how many the engine produced.
type TrimInfo struct {
	Returned  int `json:"returned"`
	Available int `json:"available"`
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
// Under budget the value is returned untouched (no marker). Over budget it is
// SHRUNK, never emptied (RS-04): string fields inside list items are capped
// progressively, then lists lose tail items (never below one), then long
// strings are cut, and only as a last resort are non-envelope scalar/object
// keys removed. Envelope keys (envelopeKeys) and lists are never removed. The
// post-shrink size is recorded as `actual` (issue #300: never the
// pre-truncation count) and the report — including which lists were trimmed
// from how many items — is attached as `_token_budget`.
//
// The previous shape protected keys by name, a list ported from the Rust
// engine (`results`); the Go engine answers with `hits`, `result`,
// `memories`, …, so an over-budget semantic answer lost `hits` altogether and
// a full `import read` lost `content`.
func (TokenBudget) Apply(v any, tool string) (any, Stats) {
	maxTokens := TokenBudget{}.MaxTokensForTool(tool)
	stats := Stats{Tool: tool, Max: maxTokens}
	stats.PreTruncationToken = countTokens(v)
	if maxTokens <= 0 || stats.PreTruncationToken <= maxTokens {
		stats.Actual = stats.PreTruncationToken
		return v, stats
	}

	// Engine answers carry typed values ([]map[string]any, structs); shrink
	// reasons over generic JSON shapes, so normalize first. A typed hit slice
	// was not recognized as a list and was dropped whole (RS-04 golden).
	v = normalize(v)
	budget := maxTokens
	if _, isObject := v.(map[string]any); isObject {
		// Reserve room for the report itself so the delivered payload honors
		// the cap instead of the Rust original's pre-marker measurement.
		if reserve := markerReserveTokens(); budget > reserve {
			budget -= reserve
		}
	}
	rep := &shrinkReport{}
	v = shrink(v, budget*TokenCharsPerToken, rep, "")
	stats.Truncated = true

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
			Truncated:          true,
			Trimmed:            rep.trimmed,
			Dropped:            rep.dropped,
			Hint:               budgetHint,
		}
		if counted := countTokens(obj); counted != stats.Actual {
			stats.Actual = counted
			continue
		}
		break
	}
	return obj, stats
}

// normalize round-trips v through JSON into generic maps, slices and
// scalars. On failure v is returned as is.
func normalize(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return v
	}
	return out
}

// budgetHint tells an agent how to get the rest of a trimmed answer.
const budgetHint = "response shrunk to fit the token budget; lower limit or narrow the query to see whole items"

// markerReserveTokens is the space held back for the largest possible
// `_token_budget` report (widest counts plus key overhead).
func markerReserveTokens() int {
	probe := map[string]any{BudgetMarkerKey: TokenReport{
		Max: 99999999, Actual: 99999999, PreTruncationToken: 99999999, Truncated: true,
		Trimmed: map[string]TrimInfo{
			"requirements": {Returned: 99999, Available: 99999},
			"memories":     {Returned: 99999, Available: 99999},
		},
		Dropped: []string{"elements_by_type", "last_embed_run"},
		Hint:    budgetHint,
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

// envelopeKeys carry an answer's identity and provenance; shrinking may
// shorten them but never removes them.
var envelopeKeys = map[string]bool{
	"query": true, "retrieval": true, "freshness": true, "guidance": true,
	"action": true, "resolved_query": true, "resolved_to": true, "reachable": true,
	"command": true, "path": true, "mode": true, "limit": true, "count": true,
	"tool": true, "cmd": true, "service": true, "env": true, "id": true,
	"session_id": true, "node_id": true, "bank": true, "banks": true,
}

// itemStringCaps are the successive rune caps applied to string fields of
// list items before any item is dropped: a shorter snippet per hit beats
// fewer hits.
var itemStringCaps = []int{400, 200, 120, 60}

// minChildBytes is the smallest budget worth shrinking a child into; below
// it the sibling keys alone exceed the budget and deletion is the only lever.
const minChildBytes = 32

// shrinkReport records what Apply removed.
type shrinkReport struct {
	trimmed map[string]TrimInfo
	dropped []string
}

// shrink returns v reduced to at most budget serialized bytes where possible.
// key names v inside its parent (for the trim report).
func shrink(v any, budget int, rep *shrinkReport, key string) any {
	if itemBytes(v) <= budget {
		return v
	}
	switch t := v.(type) {
	case string:
		return cutString(t, budget)
	case []any:
		return shrinkArray(t, budget, rep, key)
	case map[string]any:
		return shrinkObject(t, budget, rep)
	default:
		return v
	}
}

func shrinkArray(arr []any, budget int, rep *shrinkReport, key string) []any {
	avail := len(arr)
	for _, cap := range itemStringCaps {
		if itemBytes(arr) <= budget {
			return arr
		}
		arr = capItemStrings(arr, cap)
	}
	if itemBytes(arr) <= budget {
		return arr
	}
	acc := 2 // "[" + "]"
	keep := 0
	for _, item := range arr {
		acc += itemBytes(item) + 1
		if acc > budget {
			break
		}
		keep++
	}
	if keep < 1 {
		// One item cannot fit any budget: keep it (never a silently emptied
		// payload) and shrink the item itself.
		keep = 1
		arr = []any{shrink(arr[0], budget-2, rep, key)}
	}
	if keep < avail {
		if rep.trimmed == nil {
			rep.trimmed = map[string]TrimInfo{}
		}
		name := key
		if name == "" {
			name = "(root)"
		}
		rep.trimmed[name] = TrimInfo{Returned: keep, Available: avail}
	}
	return arr[:keep:keep]
}

// capItemStrings returns a copy of arr whose string items, and string fields
// of object items, are cut to at most n runes.
func capItemStrings(arr []any, n int) []any {
	out := make([]any, len(arr))
	for i, item := range arr {
		switch t := item.(type) {
		case string:
			out[i] = cutRunes(t, n)
		case map[string]any:
			m := make(map[string]any, len(t))
			for k, fv := range t {
				if fs, ok := fv.(string); ok && !envelopeKeys[k] {
					m[k] = cutRunes(fs, n)
				} else {
					m[k] = fv
				}
			}
			out[i] = m
		default:
			out[i] = item
		}
	}
	return out
}

func shrinkObject(obj map[string]any, budget int, rep *shrinkReport) map[string]any {
	out := make(map[string]any, len(obj))
	for k, v := range obj {
		out[k] = v
	}
	isList := func(v any) bool { _, ok := v.([]any); return ok }
	// Lists are the payload (hits, memories, requirements…). Spend the cut on
	// everything else first: non-envelope scalars and objects (objects are
	// shrunk recursively, so a nested result keeps its own lists), then the
	// lists, then — only if nothing else is left — envelope strings.
	phases := []func(string, any) bool{
		func(k string, v any) bool { return !envelopeKeys[k] && !isList(v) && k != BudgetMarkerKey },
		func(k string, v any) bool { return isList(v) },
		func(k string, v any) bool { return k != BudgetMarkerKey },
	}
	for _, eligible := range phases {
		for range 64 {
			size := objectBytes(out)
			if size <= budget {
				return out
			}
			k := largestKey(out, eligible)
			if k == "" || itemBytes(out[k]) <= minChildBytes {
				break
			}
			childSize := itemBytes(out[k])
			room := budget - (size - childSize)
			if room >= minChildBytes {
				if shrunk := shrink(out[k], room, rep, k); itemBytes(shrunk) < childSize {
					out[k] = shrunk
					continue
				}
			}
			if !envelopeKeys[k] && !isList(out[k]) {
				// No room left for it at all: drop the non-payload key.
				delete(out, k)
				rep.dropped = append(rep.dropped, k)
				continue
			}
			// A list or envelope key never goes; squeeze it to its minimum.
			out[k] = shrink(out[k], minChildBytes, rep, k)
			break
		}
	}
	return out
}

// largestKey returns the key with the largest serialized value among those
// accepted by ok (ties broken by name, so the result is deterministic).
func largestKey(obj map[string]any, ok func(string, any) bool) string {
	best, bestSize := "", -1
	for _, k := range slices.Sorted(maps.Keys(obj)) {
		if !ok(k, obj[k]) {
			continue
		}
		if sz := itemBytes(obj[k]); sz > bestSize {
			best, bestSize = k, sz
		}
	}
	return best
}

// truncMark ends every string the budget cut.
const truncMark = "…"

func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + truncMark
}

// cutString cuts s so its JSON encoding fits budget bytes (escapes counted).
func cutString(s string, budget int) string {
	r := []rune(s)
	lo, hi := 0, len(r)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if itemBytes(string(r[:mid])+truncMark) <= budget {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return string(r[:lo]) + truncMark
}
