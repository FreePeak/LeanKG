package budget

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// withEnv sets an env var for the duration of a test. These tests must not run
// in parallel: LEANKG_TOOL_BUDGET_OFF is process-wide (the Rust suite needed an
// explicit mutex for the same reason).
func withEnv(t *testing.T, key, value string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("setenv %s: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestGuardDisabledByEnv(t *testing.T) {
	withEnv(t, "LEANKG_TOOL_BUDGET_OFF", "1")
	g := WithCaps("test", 0, 0, 1)
	g.Tick()
	g.Tick()
	if err := g.Check(); err != nil {
		t.Fatalf("disabled guard aborted: %v", err)
	}
	if g.Exhausted() {
		t.Fatal("disabled guard reported exhausted")
	}
}

func TestGuardIterationCapAborts(t *testing.T) {
	withEnv(t, "LEANKG_TOOL_BUDGET_OFF", "0")
	g := WithCaps("test", NoCap, NoCap, 3)
	g.Tick()
	g.Tick()
	if err := g.Check(); err != nil {
		t.Fatalf("under cap: %v", err)
	}
	g.Tick()
	err := g.Check()
	var be *BudgetExceeded
	if !errors.As(err, &be) {
		t.Fatalf("want BudgetExceeded, got %v", err)
	}
	if be.Reason != ReasonIterations || be.Count != 3 || be.Cap != 3 {
		t.Fatalf("unexpected breach %+v", be)
	}
	if !g.AlreadyReported() {
		t.Fatal("breach did not set AlreadyReported")
	}
}

func TestGuardTimeoutAborts(t *testing.T) {
	withEnv(t, "LEANKG_TOOL_BUDGET_OFF", "0")
	// 0-second cap: any elapsed >= cap triggers (Rust test shape).
	g := WithCaps("test", 0, NoCap, 0)
	time.Sleep(1100 * time.Millisecond)
	var be *BudgetExceeded
	if !errors.As(g.Check(), &be) || be.Reason != ReasonTimeout {
		t.Fatalf("want timeout breach, got %v", g.Check())
	}
	if be.CapSecs != 0 || be.Tool != "test" {
		t.Fatalf("unexpected breach %+v", be)
	}
	if !strings.Contains(be.Error(), "budget timeout") {
		t.Fatalf("message lost the timeout clause: %s", be.Error())
	}
}

func TestGuardUnlimitedNeverAborts(t *testing.T) {
	withEnv(t, "LEANKG_TOOL_BUDGET_OFF", "0")
	g := Unlimited("test")
	for i := 0; i < 10_000; i++ {
		g.Tick()
	}
	if err := g.Check(); err != nil {
		t.Fatalf("unlimited guard aborted: %v", err)
	}
}

func TestGuardIterOnlyHasNoTimeOrRSS(t *testing.T) {
	withEnv(t, "LEANKG_TOOL_BUDGET_OFF", "0")
	g := IterOnly("test", 2)
	if err := g.Check(); err != nil {
		t.Fatalf("fresh guard: %v", err)
	}
	g.Tick()
	if err := g.Check(); err != nil {
		t.Fatalf("under cap: %v", err)
	}
	g.Tick()
	if !g.Exhausted() {
		t.Fatal("iter-only guard should be exhausted at the cap")
	}
	if g.Check() == nil {
		t.Fatal("iter-only guard should abort at the cap")
	}
}

func TestGuardMemoryCapAborts(t *testing.T) {
	withEnv(t, "LEANKG_TOOL_BUDGET_OFF", "0")
	restore := RSSMbFn
	RSSMbFn = func() (uint64, error) { return 900, nil }
	t.Cleanup(func() { RSSMbFn = restore })

	g := WithCaps("export", NoCap, 128, 0)
	var be *BudgetExceeded
	if !errors.As(g.Check(), &be) || be.Reason != ReasonMemory {
		t.Fatalf("want memory breach, got %v", g.Check())
	}
	if be.RSSMb != 900 || be.CapMb != 128 {
		t.Fatalf("unexpected breach %+v", be)
	}
	if !strings.Contains(be.Error(), "LEANKG_MAX_RSS_MB") {
		t.Fatalf("message must name the fix env var: %s", be.Error())
	}
}

func TestGuardMemoryProbeErrorIsNotABreach(t *testing.T) {
	withEnv(t, "LEANKG_TOOL_BUDGET_OFF", "0")
	restore := RSSMbFn
	RSSMbFn = func() (uint64, error) { return 0, errors.New("no probe") }
	t.Cleanup(func() { RSSMbFn = restore })

	if err := WithCaps("test", NoCap, 1, 0).Check(); err != nil {
		t.Fatalf("unreadable RSS must not abort: %v", err)
	}
}

func TestGuardElapsedGrows(t *testing.T) {
	g := ForTool("test")
	e1 := g.Elapsed()
	time.Sleep(5 * time.Millisecond)
	if g.Elapsed() < e1 {
		t.Fatal("elapsed went backwards")
	}
}

func TestDefaultRSSMbAndTimeoutFromEnv(t *testing.T) {
	withEnv(t, "LEANKG_MAX_RSS_MB", "512")
	if got := DefaultRSSMb(); got != 512 {
		t.Fatalf("LEANKG_MAX_RSS_MB: got %d, want 512", got)
	}
	withEnv(t, "LEANKG_TOOL_TIMEOUT_SECS", "7")
	if got := defaultTimeoutSecs(); got != 7 {
		t.Fatalf("LEANKG_TOOL_TIMEOUT_SECS: got %d, want 7", got)
	}
	withEnv(t, "LEANKG_MAX_RSS_MB", "not-a-number")
	if got := DefaultRSSMb(); got != 4096 {
		t.Fatalf("garbage env must fall back to 4096, got %d", got)
	}
}

func TestCurrentRSSMbIsPlausible(t *testing.T) {
	rss, err := CurrentRSSMb()
	if err != nil {
		t.Skipf("no RSS probe on this platform: %v", err)
	}
	if rss == 0 || rss > 1<<20 {
		t.Fatalf("implausible RSS reading: %d MiB", rss)
	}
}

func TestMaxTokensForToolTable(t *testing.T) {
	// Rust table arms (verbatim) plus the Go action mapping must both resolve.
	cases := map[string]int{
		"get_service_context": 800,
		"semantic_search":     2000,
		"kg_context":          4000,
		"get_impact_radius":   6000,
		"unknown_tool":        DefaultMaxTokens,
		// Go query actions routed through the 3-tool registry.
		"semantic": 2000,
		"impact":   6000,
		"search":   4000,
		"context":  4000,
		"callers":  4000,
		"path":     2000,
		"status":   2000,
		"":         DefaultMaxTokens,
		// Envelope tool names carry no cap of their own.
		"query":  0,
		"import": 0,
	}
	for tool, want := range cases {
		if got := (TokenBudget{}).MaxTokensForTool(tool); got != want {
			t.Errorf("MaxTokensForTool(%q) = %d, want %d", tool, got, want)
		}
	}
}

func TestToolBudgetTableIsWellFormed(t *testing.T) {
	seen := map[string]int{}
	for _, tb := range ToolBudgets {
		if tb.MaxTokens < 0 {
			t.Errorf("budget row %v has a negative cap", tb.Actions)
		}
		if len(tb.Actions) == 0 {
			t.Errorf("budget row with cap %d has no tool names", tb.MaxTokens)
		}
		for _, a := range tb.Actions {
			if a == "" {
				t.Errorf("empty tool name in row %v", tb.Actions)
				continue
			}
			if prev, dup := seen[a]; dup {
				t.Errorf("tool %q mapped twice (%d and %d)", a, prev, tb.MaxTokens)
			}
			seen[a] = tb.MaxTokens
		}
	}
	// Envelope tools must be uncapped (their action carries the real cap).
	envelopes := map[string]bool{"query": true, "import": true, "get": true, "set": true}
	for name, cap := range seen {
		if envelopes[name] && cap != 0 {
			t.Errorf("envelope tool %q must be uncapped, got %d", name, cap)
		}
		if cap == 0 && !envelopes[name] {
			t.Errorf("tool %q is uncapped but is not an envelope name", name)
		}
	}
}

func TestCountTokens(t *testing.T) {
	v := map[string]any{"key": "value"}
	if got := (TokenBudget{}).CountTokens(v); got <= 0 {
		t.Fatalf("CountTokens = %d, want > 0", got)
	}
	// 4 bytes per token.
	b, _ := json.Marshal(v)
	if got, want := (TokenBudget{}).CountTokens(v), len(b)/4; got != want {
		t.Fatalf("CountTokens = %d, want %d", got, want)
	}
}

func TestApplyUnderBudgetLeavesPayloadUntouched(t *testing.T) {
	v := map[string]any{"small": "data"}
	out, stats := (TokenBudget{}).Apply(v, "semantic_search")
	obj, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("payload identity changed: %T", out)
	}
	if _, marked := obj[BudgetMarkerKey]; marked {
		t.Fatal("under-budget response must not carry the marker")
	}
	if stats.Truncated || stats.SavedTokens() != 0 {
		t.Fatalf("under-budget stats: %+v", stats)
	}
}

// TestApplyUncappedToolPassesThrough pins the envelope contract: the router
// tool names carry no cap, so an oversized envelope response is never trimmed
// (the action name inside it is what gets capped).
func TestApplyUncappedToolPassesThrough(t *testing.T) {
	v := map[string]any{
		"query": strings.Repeat("x", 40_000),
		"debug": strings.Repeat("y", 40_000),
	}
	out, stats := (TokenBudget{}).Apply(v, "query")
	obj, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("payload identity changed: %T", out)
	}
	if _, marked := obj[BudgetMarkerKey]; marked {
		t.Fatal("uncapped tool must not carry the marker")
	}
	if _, kept := obj["debug"]; !kept {
		t.Fatal("uncapped tool must not drop keys")
	}
	if stats.Max != 0 || stats.Truncated || stats.SavedTokens() != 0 {
		t.Fatalf("uncapped stats: %+v", stats)
	}
}

func TestApplyTruncatesArrayAndKeepsPrefix(t *testing.T) {
	items := make([]any, 20)
	for i := range items {
		items[i] = map[string]any{"id": "1", "data": strings.Repeat("x", 500)}
	}
	v := map[string]any{"results": items}

	out, stats := (TokenBudget{}).Apply(v, "semantic_search")
	obj, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("payload is not an object: %T", out)
	}
	results, ok := obj["results"].([]any)
	if !ok {
		t.Fatalf("results key lost: %T", obj["results"])
	}
	if len(results) == 0 || len(results) >= len(items) {
		t.Fatalf("results kept %d of %d items, want a non-empty proper prefix", len(results), len(items))
	}
	// The kept prefix is the LEADING items.
	if first, _ := results[0].(map[string]any); first["id"] != "1" {
		t.Fatalf("prefix does not start at the first item: %v", results[0])
	}

	marker, ok := obj[BudgetMarkerKey].(TokenReport)
	if !ok {
		t.Fatalf("marker missing or wrong type: %#v", obj[BudgetMarkerKey])
	}
	if !marker.Truncated {
		t.Fatal("marker must report truncated=true")
	}
	if marker.Max != 2000 {
		t.Fatalf("marker max = %d, want 2000", marker.Max)
	}
	// Regression #300: actual is the POST-truncation size and must fit the cap.
	if marker.Actual > marker.Max {
		t.Fatalf("actual (%d) must be <= max (%d) when truncated", marker.Actual, marker.Max)
	}
	if marker.PreTruncationToken <= marker.Max {
		t.Fatalf("pre_truncation_tokens (%d) must record the original oversize payload", marker.PreTruncationToken)
	}
	if !stats.Truncated || stats.SavedTokens() <= 0 || stats.SavedPercent() <= 0 {
		t.Fatalf("savings accounting empty: %+v", stats)
	}
}

func TestApplyPreservesPrimaryPayloadKeys(t *testing.T) {
	items := make([]any, 20)
	for i := range items {
		items[i] = map[string]any{"id": "1", "data": strings.Repeat("x", 500)}
	}
	v := map[string]any{
		"query":   "service lookup",
		"results": items,
		"debug":   strings.Repeat("x", 5000),
	}
	out, _ := (TokenBudget{}).Apply(v, "semantic_search")
	obj := out.(map[string]any)
	if _, ok := obj["query"]; !ok {
		t.Fatal("protected key 'query' was dropped")
	}
	if _, ok := obj["results"]; !ok {
		t.Fatal("protected key 'results' was dropped")
	}
	if _, ok := obj["debug"]; ok {
		t.Fatal("non-protected 'debug' should have been dropped to make room")
	}
}

func TestApplyAlwaysLeavesAtLeastOneItem(t *testing.T) {
	// One huge item cannot fit any budget: the array must still keep it rather
	// than hand back a silently emptied payload.
	items := []any{map[string]any{"id": "1", "data": strings.Repeat("x", 20000)}}
	v := map[string]any{"results": items}
	out, _ := (TokenBudget{}).Apply(v, "search_code")
	obj := out.(map[string]any)
	kept, ok := obj["results"].([]any)
	if !ok || len(kept) != 1 {
		t.Fatalf("oversized single item was dropped: %#v", obj["results"])
	}
}

func TestApplyMarkerOnTinyBudget(t *testing.T) {
	// A cap smaller than the marker reserve must not panic or hand back a
	// negative budget.
	v := map[string]any{"elements": []any{strings.Repeat("x", 4000)}, "noise": strings.Repeat("y", 4000)}
	out, stats := (TokenBudget{}).Apply(v, "get_service_context") // 800-token cap
	if stats.Tool != "get_service_context" || stats.Actual <= 0 {
		t.Fatalf("stats: %+v", stats)
	}
	obj, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("not an object: %T", out)
	}
	if _, ok := obj[BudgetMarkerKey]; !ok {
		t.Fatal("oversized response must carry the marker")
	}
	if _, ok := obj["elements"]; !ok {
		t.Fatal("primary payload key 'elements' must survive")
	}
}
func TestApplyNonObjectResponseTruncatesWithoutMarker(t *testing.T) {
	// No marker is ever attached, so the payload must be trimmed against the
	// FULL cap (the reserve only exists to make room for the report).
	arr := make([]any, 50)
	for i := range arr {
		arr[i] = strings.Repeat("x", 400) // ~10k serialized bytes => ~5000 tokens > 4000
	}
	out, stats := (TokenBudget{}).Apply(arr, "search_code")
	kept, ok := out.([]any)
	if !ok {
		t.Fatalf("array payload changed type: %T", out)
	}
	if len(kept) >= len(arr) || len(kept) == 0 {
		t.Fatalf("kept %d of %d items", len(kept), len(arr))
	}
	if !stats.Truncated || stats.Actual > stats.Max {
		t.Fatalf("stats: %+v (actual must fit the cap)", stats)
	}
}

func TestApplyIsDeterministicAcrossRepeatedCalls(t *testing.T) {
	build := func() map[string]any {
		items := make([]any, 20)
		for i := range items {
			items[i] = map[string]any{"id": "1", "data": strings.Repeat("x", 500)}
		}
		return map[string]any{"query": "q", "results": items, "debug": strings.Repeat("x", 5000)}
	}
	first := ""
	for i := 0; i < 8; i++ {
		b, err := json.Marshal(build())
		if err != nil {
			t.Fatal(err)
		}
		out, _ := (TokenBudget{}).Apply(build(), "semantic_search")
		got, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		if first == "" {
			first = string(got)
			continue
		}
		if string(got) != first {
			t.Fatalf("truncation is not deterministic:\nfirst: %s\n  now: %s", first, got)
		}
		_ = b
	}
}
