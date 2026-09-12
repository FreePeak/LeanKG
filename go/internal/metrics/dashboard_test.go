package metrics

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// TestParseWindow pins the dashboard window parser (Rust dashboard::parse_since):
// "<n>h" | "<n>d" | "<n>w" with a strictly positive count, surrounding
// whitespace ignored, and a bare number rejected — unlike `metrics --since`.
func TestParseWindow(t *testing.T) {
	for raw, want := range map[string]int64{
		"24h":  24 * 3_600,
		"7d":   7 * store.SecondsPerDay,
		"30d":  30 * store.SecondsPerDay,
		"2w":   14 * store.SecondsPerDay,
		" 7d ": 7 * store.SecondsPerDay,
	} {
		got, ok := ParseWindow(raw)
		if !ok || got != want {
			t.Fatalf("ParseWindow(%q) = %d, %v; want %d, true", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"7", "0d", "-1d", "7m", "d", "", "abc", "1 d"} {
		if got, ok := ParseWindow(raw); ok {
			t.Fatalf("ParseWindow(%q) must be rejected, got %d", raw, got)
		}
	}
}

// TestDashboardTextShape pins the byte-exact text rendering: the totals block,
// the tool table, the per-day `▇` chart and the project table (Rust
// dashboard::render_text, including its column widths).
func TestDashboardTextShape(t *testing.T) {
	b := openTestBackend(t)
	anchor := dayAnchor()
	record(t, b,
		store.Metric{ToolName: "query", Timestamp: anchor + 10, ProjectPath: "/a", InputTokens: 1000,
			OutputTokens: 200, ExecutionTimeMs: 30, TokensSaved: 500, SavingsPercent: 50,
			Success: true, QueryPattern: "handle"},
		store.Metric{ToolName: "query", Timestamp: anchor + 20, ProjectPath: "/a", InputTokens: 2000,
			OutputTokens: 300, ExecutionTimeMs: 10, TokensSaved: 1500, SavingsPercent: 75,
			Success: true, QueryPattern: "handle"},
		store.Metric{ToolName: "status", Timestamp: anchor - store.SecondsPerDay + 5, ProjectPath: "/b",
			InputTokens: 50, OutputTokens: 20, ExecutionTimeMs: 5, TokensSaved: 40,
			SavingsPercent: 80},
	)

	got, err := Dashboard(b, DashboardOptions{})
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	want := strings.NewReplacer(
		"@@OLDER@@", utcDay(anchor-store.SecondsPerDay),
		"@@TODAY@@", utcDay(anchor),
	).Replace(`LeanKG usage dashboard — context_metrics

Total calls         : 3
Input tokens        : 3 050
Output tokens       : 520
Tokens saved        : 2 040
Avg savings percent : 68.3%
Success rate        : 66.7%

By tool (sorted by tokens saved)
TOOL                     CALLS        SAVED     AVG MS
query                        2        2 000       20.0
status                       1           40        5.0

By day (UTC)
DAY            CALLS        SAVED  ▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇
@@OLDER@@         1           40  ▇
@@TODAY@@         2        2 000  ▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇

By project (top 5 by tokens saved)
PROJECT                                    CALLS        SAVED
/a                                             2        2 000
/b                                             1           40
`)
	if got != want {
		t.Fatalf("dashboard text mismatch:\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestDashboardJSONShape pins the structured payload Rust's web dashboard
// consumes: the five top-level sections in order, the totals keys (including
// savings_percent_sum, which the text rendering never shows) and empty buckets
// as [] rather than null.
func TestDashboardJSONShape(t *testing.T) {
	b := openTestBackend(t)
	anchor := dayAnchor()
	record(t, b, store.Metric{ToolName: "query", Timestamp: anchor + 10, ProjectPath: "/a",
		TokensSaved: 60, SavingsPercent: 60, Success: true, QueryPattern: "handle"})

	got, err := Dashboard(b, DashboardOptions{Format: "json"})
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	var payload struct {
		Totals struct {
			Calls             int64   `json:"calls"`
			InputTokens       int64   `json:"input_tokens"`
			OutputTokens      int64   `json:"output_tokens"`
			TokensSaved       int64   `json:"tokens_saved"`
			SavingsPercentSum float64 `json:"savings_percent_sum"`
			AvgSavingsPercent float64 `json:"avg_savings_percent"`
			SuccessRate       float64 `json:"success_rate"`
		} `json:"totals"`
		ByTool    []map[string]any `json:"by_tool"`
		ByDay     []map[string]any `json:"by_day"`
		ByProject []map[string]any `json:"by_project"`
		Patterns  []map[string]any `json:"patterns"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("dashboard JSON does not parse: %v\n%s", err, got)
	}
	if payload.Totals.Calls != 1 || payload.Totals.TokensSaved != 60 ||
		payload.Totals.SavingsPercentSum != 60 || payload.Totals.AvgSavingsPercent != 60 ||
		payload.Totals.SuccessRate != 1 {
		t.Fatalf("totals payload = %s", got)
	}
	if len(payload.ByTool) != 1 || payload.ByTool[0]["tool"] != "query" ||
		payload.ByTool[0]["tokens_saved"] != float64(60) || payload.ByTool[0]["avg_ms"] != float64(0) {
		t.Fatalf("by_tool payload = %s", got)
	}
	if len(payload.ByDay) != 1 || payload.ByDay[0]["day"] != utcDay(anchor) {
		t.Fatalf("by_day payload = %s", got)
	}
	if len(payload.ByProject) != 1 || payload.ByProject[0]["project"] != "/a" {
		t.Fatalf("by_project payload = %s", got)
	}
	if len(payload.Patterns) != 1 || payload.Patterns[0]["pattern"] != "handle" ||
		payload.Patterns[0]["calls"] != float64(1) {
		t.Fatalf("patterns payload = %s", got)
	}
	// Section order is part of the payload contract (Rust's serde order).
	last := -1
	for _, key := range []string{`"totals"`, `"by_tool"`, `"by_day"`, `"by_project"`, `"patterns"`} {
		at := strings.Index(got, key)
		if at <= last {
			t.Fatalf("section %s out of order:\n%s", key, got)
		}
		last = at
	}

	// An empty ledger still renders every section as an array.
	empty, err := Dashboard(openTestBackend(t), DashboardOptions{Format: "json"})
	if err != nil {
		t.Fatalf("empty dashboard: %v", err)
	}
	for _, want := range []string{`"by_tool": []`, `"by_day": []`, `"by_project": []`, `"patterns": []`} {
		if !strings.Contains(empty, want) {
			t.Fatalf("empty payload must carry %s: %s", want, empty)
		}
	}
}

// TestDashboardEmptyLedgerText pins the empty state (Rust render_text's early
// return) and that the empty ledger is not an error.
func TestDashboardEmptyLedgerText(t *testing.T) {
	got, err := Dashboard(openTestBackend(t), DashboardOptions{})
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	want := "LeanKG usage dashboard — context_metrics\n" +
		"No metrics yet — the context_metrics ledger is empty for this window.\n"
	if got != want {
		t.Fatalf("empty dashboard text = %q, want %q", got, want)
	}
}

// TestDashboardCapsAndOrder pins build_dashboard: tools by tokens saved desc
// (name ascending on a tie) capped at 10, days ascending, projects by tokens
// saved desc capped at 5, patterns by calls desc capped at 10.
func TestDashboardCapsAndOrder(t *testing.T) {
	b := openTestBackend(t)
	anchor := dayAnchor()
	// 12 tools; "same_a"/"same_b" tie so the tie-break is observable.
	for i := 0; i < 10; i++ {
		record(t, b, store.Metric{ToolName: fmt.Sprintf("tool%02d", i), Timestamp: anchor + int64(i),
			ProjectPath: fmt.Sprintf("/p%d", i), TokensSaved: int64(1000 - i), Success: true})
	}
	record(t, b,
		store.Metric{ToolName: "same_b", Timestamp: anchor + 20, ProjectPath: "/p0", TokensSaved: 995, Success: true},
		store.Metric{ToolName: "same_a", Timestamp: anchor + 21, ProjectPath: "/p0", TokensSaved: 995, Success: true},
		store.Metric{ToolName: "old", Timestamp: anchor - 5*store.SecondsPerDay, ProjectPath: "/old", TokensSaved: 1},
	)

	out, err := Dashboard(b, DashboardOptions{Format: "json"})
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	var payload struct {
		ByTool    []store.ToolUsage    `json:"by_tool"`
		ByDay     []store.DayUsage     `json:"by_day"`
		ByProject []store.ProjectUsage `json:"by_project"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(payload.ByTool) != 10 {
		t.Fatalf("by_tool cap = %d rows, want 10", len(payload.ByTool))
	}
	if payload.ByTool[0].Tool != "tool00" {
		t.Fatalf("by_tool[0] = %+v, want the highest saver", payload.ByTool[0])
	}
	if payload.ByTool[5].Tool != "same_a" || payload.ByTool[6].Tool != "same_b" {
		t.Fatalf("ties must order by tool name ascending: %+v", payload.ByTool[4:8])
	}
	if len(payload.ByProject) != 5 {
		t.Fatalf("by_project cap = %d rows, want 5", len(payload.ByProject))
	}
	if len(payload.ByDay) != 2 || payload.ByDay[0].Day >= payload.ByDay[1].Day {
		t.Fatalf("by_day must be chronological: %+v", payload.ByDay)
	}

	// Pattern cap: 12 distinct patterns, one row each.
	b2 := openTestBackend(t)
	for i := 0; i < 12; i++ {
		record(t, b2, store.Metric{ToolName: "query", Timestamp: anchor + int64(i),
			QueryPattern: fmt.Sprintf("pattern%02d", i), Success: true})
	}
	patterns, err := Dashboard(b2, DashboardOptions{Format: "json"})
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	var patPayload struct {
		Patterns []store.PatternUsage `json:"patterns"`
	}
	if err := json.Unmarshal([]byte(patterns), &patPayload); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(patPayload.Patterns) != 10 {
		t.Fatalf("patterns cap = %d rows, want 10", len(patPayload.Patterns))
	}
}

// TestDashboardWindowAndErrors pins that --since windows the read and that an
// invalid window or format is an error (Rust exits non-zero for both).
func TestDashboardWindowAndErrors(t *testing.T) {
	b := openTestBackend(t)
	anchor := dayAnchor()
	record(t, b,
		store.Metric{ToolName: "query", Timestamp: anchor + 10, ProjectPath: "/a", TokensSaved: 10, Success: true},
		store.Metric{ToolName: "query", Timestamp: anchor - 3*store.SecondsPerDay, ProjectPath: "/a",
			TokensSaved: 20, Success: true},
	)

	var payload struct {
		Totals struct {
			Calls int64 `json:"calls"`
		} `json:"totals"`
	}
	out, err := Dashboard(b, DashboardOptions{Since: "1d", Format: "json"})
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if payload.Totals.Calls != 1 {
		t.Fatalf("--since 1d calls = %d, want 1", payload.Totals.Calls)
	}

	if _, err := Dashboard(b, DashboardOptions{Since: "7"}); err == nil ||
		!strings.Contains(err.Error(), "invalid --since window") {
		t.Fatalf("bare --since must be rejected, got %v", err)
	}
	if _, err := Dashboard(b, DashboardOptions{Format: "yaml"}); err == nil ||
		!strings.Contains(err.Error(), `unknown --format value "yaml"`) {
		t.Fatalf("unknown --format must be rejected, got %v", err)
	}
}

// TestBarAndThousands pin the two rendering helpers against the Rust shapes,
// including the clamp to at least one cell and the negative-count grouping.
func TestBarAndThousands(t *testing.T) {
	for _, tc := range []struct {
		value, max int64
		want       string
	}{
		{0, 100, ""},
		{-5, 100, ""},
		{5, 0, "▇"},
		{5, -1, "▇"},
		{100, 100, strings.Repeat("▇", 24)},
		{200, 100, strings.Repeat("▇", 24)},
		{25, 100, strings.Repeat("▇", 6)},
		{1, 1000, "▇"}, // rounds to zero cells, clamped up to one
	} {
		if got := bar(tc.value, tc.max, 24); got != tc.want {
			t.Fatalf("bar(%d, %d) = %q, want %q", tc.value, tc.max, got, tc.want)
		}
	}
	if got := bar(5, 5, 0); got != "" {
		t.Fatalf("bar with width 0 = %q, want empty", got)
	}

	for n, want := range map[int64]string{
		0:       "0",
		999:     "999",
		1000:    "1 000",
		1234567: "1 234 567",
		-1234:   "-1 234",
		-100:    "- 100", // the sign takes part in the grouping, exactly like Rust
	} {
		if got := thousands(n); got != want {
			t.Fatalf("thousands(%d) = %q, want %q", n, got, want)
		}
	}
}
