package metrics

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// openTestBackend opens a migrated sqlite store in a temp dir (the default
// engine, and where these readouts must work without PostgreSQL).
func openTestBackend(t *testing.T) store.Backend {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

// dayAnchor is the start of the current UTC day: fixtures are anchored to it so
// day buckets stay deterministic whatever time the test runs.
func dayAnchor() int64 { return time.Now().Unix() / store.SecondsPerDay * store.SecondsPerDay }

// utcDay renders an epoch-second stamp as the YYYY-MM-DD bucket label.
func utcDay(ts int64) string { return time.Unix(ts, 0).UTC().Format("2006-01-02") }

// record writes rows into the ledger and fails the test on error.
func record(t *testing.T, b store.Backend, rows ...store.Metric) {
	t.Helper()
	for _, r := range rows {
		if err := b.RecordMetric(r); err != nil {
			t.Fatalf("record %s: %v", r.ToolName, err)
		}
	}
}

// TestShowTextReport pins the report's byte-exact text shape (Rust show_metrics
// println sequence): header block, By Tool rows, the By Day section with its
// double space after the date, and the --session placeholder.
func TestShowTextReport(t *testing.T) {
	b := openTestBackend(t)
	anchor := dayAnchor()
	record(t, b,
		store.Metric{ToolName: "query", Timestamp: anchor + 100, TokensSaved: 100, SavingsPercent: 90,
			CorrectElements: 8, TotalExpected: 10, Success: true},
		store.Metric{ToolName: "search", Timestamp: anchor + 200, TokensSaved: 300, SavingsPercent: 95,
			CorrectElements: 9, TotalExpected: 10, Success: true},
	)

	got, err := Show(b, Options{Session: true})
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	want := strings.ReplaceAll(`=== LeanKG Context Metrics ===

Total Savings: 400 tokens across 2 calls
Average Savings: 92.5% (positive only)
Average Correctness: 85.0%
Retention: 30 days

By Tool:
  query: 1 calls, 90% save, 80.0% correct
  search: 1 calls, 95% save, 90.0% correct

By Day:
  @DAY@:  2 calls, 85.0% correct

Session: Showing current session metrics not yet implemented
`, "@DAY@", utcDay(anchor))
	if got != want {
		t.Fatalf("report text mismatch:\n got:\n%s\nwant:\n%s", got, want)
	}

	// Without --session the placeholder line is absent (Rust printed it from the
	// session branch, not unconditionally).
	plain, err := Show(b, Options{})
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if strings.Contains(plain, "Session:") {
		t.Fatalf("session line must be gated on --session:\n%s", plain)
	}
}

// TestShowJSONReportsTheRustShape pins the --json payload: Rust's field names in
// Rust's order, empty buckets as [] (never null). Numbers that Rust's serde
// wrote as 0.0 are written as 0 by encoding/json — same JSON number, different
// spelling; see the package report.
func TestShowJSONReportsTheRustShape(t *testing.T) {
	b := openTestBackend(t)
	got, err := Show(b, Options{JSON: true})
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	want := `{
  "total_invocations": 0,
  "total_tokens_saved": 0,
  "average_savings_percent": 0,
  "average_correctness_percent": 0,
  "retention_days": 30,
  "by_tool": [],
  "by_day": []
}
`
	if got != want {
		t.Fatalf("empty-ledger JSON mismatch:\n got:%s\nwant:%s", got, want)
	}

	anchor := dayAnchor()
	record(t, b, store.Metric{ToolName: "query", Timestamp: anchor + 10, TokensSaved: 100,
		SavingsPercent: 90, CorrectElements: 8, TotalExpected: 10, Success: true})

	got, err = Show(b, Options{JSON: true})
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	var sum struct {
		TotalInvocations          int64            `json:"total_invocations"`
		TotalTokensSaved          int64            `json:"total_tokens_saved"`
		AverageSavingsPercent     float64          `json:"average_savings_percent"`
		AverageCorrectnessPercent float64          `json:"average_correctness_percent"`
		RetentionDays             int              `json:"retention_days"`
		ByTool                    []map[string]any `json:"by_tool"`
		ByDay                     []map[string]any `json:"by_day"`
	}
	if err := json.Unmarshal([]byte(got), &sum); err != nil {
		t.Fatalf("summary JSON does not parse: %v\n%s", err, got)
	}
	if sum.TotalInvocations != 1 || sum.TotalTokensSaved != 100 || sum.AverageSavingsPercent != 90 ||
		sum.AverageCorrectnessPercent != 80 || sum.RetentionDays != 30 {
		t.Fatalf("summary payload = %s", got)
	}
	if len(sum.ByTool) != 1 || sum.ByTool[0]["tool_name"] != "query" || sum.ByTool[0]["calls"] != float64(1) ||
		sum.ByTool[0]["total_saved"] != float64(100) {
		t.Fatalf("by_tool payload = %s", got)
	}
	if len(sum.ByDay) != 1 || sum.ByDay[0]["date"] != utcDay(anchor) || sum.ByDay[0]["savings"] != float64(100) {
		t.Fatalf("by_day payload = %s", got)
	}
	// --json prints JSON only, never the text header.
	if strings.Contains(got, "===") {
		t.Fatalf("--json must not print the text report:\n%s", got)
	}
}

// TestShowWindowAndToolFilters pins the window resolution: --since ("<n>d" or a
// bare "<n>") outranks --retention, an unparseable --since falls back to 30
// days, the retention window is echoed back, and --tool scopes the report.
func TestShowWindowAndToolFilters(t *testing.T) {
	b := openTestBackend(t)
	anchor := dayAnchor()
	record(t, b,
		store.Metric{ToolName: "query", Timestamp: anchor + 100, TokensSaved: 10},
		store.Metric{ToolName: "status", Timestamp: anchor - 3*store.SecondsPerDay + 50, TokensSaved: 20},
	)

	invocations := func(opts Options) int64 {
		t.Helper()
		out, err := Show(b, opts)
		if err != nil {
			t.Fatalf("show %+v: %v", opts, err)
		}
		var sum struct {
			TotalInvocations int64 `json:"total_invocations"`
			RetentionDays    int   `json:"retention_days"`
		}
		if err := json.Unmarshal([]byte(out), &sum); err != nil {
			t.Fatalf("parse: %v", err)
		}
		return sum.TotalInvocations
	}
	retention := func(opts Options) int {
		t.Helper()
		out, err := Show(b, Options{JSON: true, Since: opts.Since, Retention: opts.Retention})
		if err != nil {
			t.Fatalf("show: %v", err)
		}
		var sum struct {
			RetentionDays int `json:"retention_days"`
		}
		if err := json.Unmarshal([]byte(out), &sum); err != nil {
			t.Fatalf("parse: %v", err)
		}
		return sum.RetentionDays
	}

	if got := invocations(Options{JSON: true, Since: "1d"}); got != 1 {
		t.Fatalf("--since 1d = %d invocations, want 1", got)
	}
	if got := invocations(Options{JSON: true, Since: "7d"}); got != 2 {
		t.Fatalf("--since 7d = %d invocations, want 2", got)
	}
	if got := invocations(Options{JSON: true, Since: "7"}); got != 2 {
		t.Fatalf("bare --since 7 = %d invocations, want 2", got)
	}
	if got := invocations(Options{JSON: true, Since: "bogus"}); got != 2 {
		t.Fatalf("unparseable --since must fall back to 30 days, got %d invocations", got)
	}
	if got := invocations(Options{JSON: true, Retention: 2}); got != 1 {
		t.Fatalf("--retention 2 = %d invocations, want 1", got)
	}
	// Rust let --since win over --retention.
	if got := invocations(Options{JSON: true, Since: "7d", Retention: 2}); got != 2 {
		t.Fatalf("--since 7d --retention 2 = %d invocations, want --since to win", got)
	}
	if got := retention(Options{Since: "7d", Retention: 2}); got != 7 {
		t.Fatalf("retention_days = %d, want 7", got)
	}
	if got := retention(Options{}); got != DefaultRetentionDays {
		t.Fatalf("default retention_days = %d, want %d", got, DefaultRetentionDays)
	}

	if got := invocations(Options{JSON: true, Tool: "query"}); got != 1 {
		t.Fatalf("--tool query = %d invocations, want 1", got)
	}
	out, err := Show(b, Options{JSON: true, Tool: "query"})
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(out, `"tool_name": "query"`) || strings.Contains(out, `"tool_name": "status"`) {
		t.Fatalf("--tool must scope by_tool too:\n%s", out)
	}
}

// TestShowCleanupAndReset pins the destructive verbs' semantics and their exact
// reporting: cleanup sweeps by --retention only (Rust ignored --since on that
// path), reset empties the ledger, and both report the rows removed.
func TestShowCleanupAndReset(t *testing.T) {
	b := openTestBackend(t)
	now := time.Now().Unix()
	record(t, b,
		store.Metric{ToolName: "query", Timestamp: now - 40*store.SecondsPerDay},
		store.Metric{ToolName: "query", Timestamp: now - 3*store.SecondsPerDay},
	)

	// --since 1d must NOT narrow the cleanup window: the sweep uses --retention
	// (30 by default), so only the 40-day-old row goes. Honouring --since would
	// have removed nothing.
	out, err := Show(b, Options{Cleanup: true, Since: "1d"})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if want := "Cleaned up 1 old metric record(s) (retention: 30 days).\n"; out != want {
		t.Fatalf("cleanup output = %q, want %q", out, want)
	}

	// Re-record the expired row, then sweep with an explicit 2-day retention:
	// both remaining rows are older than that.
	record(t, b, store.Metric{ToolName: "query", Timestamp: now - 40*store.SecondsPerDay})
	out, err = Show(b, Options{Cleanup: true, Retention: 2})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if want := "Cleaned up 2 old metric record(s) (retention: 2 days).\n"; out != want {
		t.Fatalf("cleanup output = %q, want %q", out, want)
	}
	// --cleanup without --retention falls back to the 30-day default.
	record(t, b, store.Metric{ToolName: "query", Timestamp: now - 40*store.SecondsPerDay})
	out, err = Show(b, Options{Cleanup: true})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if want := "Cleaned up 1 old metric record(s) (retention: 30 days).\n"; out != want {
		t.Fatalf("default cleanup output = %q, want %q", out, want)
	}
	// Ledger is empty now, so reset reports zero.
	out, err = Show(b, Options{Reset: true})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if want := "Reset 0 metric record(s).\n"; out != want {
		t.Fatalf("reset output = %q, want %q", out, want)
	}

	record(t, b, store.Metric{ToolName: "query", Timestamp: now, TokensSaved: 5})
	// --reset outranks --cleanup (Rust checked reset first).
	out, err = Show(b, Options{Reset: true, Cleanup: true, Retention: 1})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if want := "Reset 1 metric record(s).\n"; out != want {
		t.Fatalf("reset output = %q, want %q", out, want)
	}
	if got, err := b.MetricsSummary("", DefaultRetentionDays); err != nil || got.TotalInvocations != 0 {
		t.Fatalf("ledger after reset: %+v, %v", got, err)
	}
}

// TestSeedWritesTheRustFixture pins --seed: the five Rust fixture rows land in
// the ledger (same tools, same savings), the per-row output matches, and --seed
// outranks --reset, exactly like Rust's CLI arm.
func TestSeedWritesTheRustFixture(t *testing.T) {
	b := openTestBackend(t)
	out, err := Show(b, Options{Seed: true, Reset: true})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	want := "Seeded metric: seed1 (search_code)\nSeeded metric: seed2 (get_context)\n" +
		"Seeded metric: seed3 (find_function)\nSeeded metric: seed4 (search_code)\n" +
		"Seeded metric: seed5 (get_impact_radius)\nSeeded 5 test metrics\n"
	if out != want {
		t.Fatalf("seed output = %q, want %q", out, want)
	}

	sum, err := b.MetricsSummary("", DefaultRetentionDays)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if sum.TotalInvocations != 5 || sum.TotalTokensSaved != 64660 {
		t.Fatalf("seeded summary = %+v", sum)
	}
	if len(sum.ByTool) != 4 || sum.ByTool[0].ToolName != "find_function" {
		t.Fatalf("seeded by_tool = %+v", sum.ByTool)
	}
	var dayCalls int64
	for _, d := range sum.ByDay {
		dayCalls += d.Calls
	}
	if dayCalls != 5 {
		t.Fatalf("seeded by_day must cover all 5 rows: %+v", sum.ByDay)
	}

	report, err := Show(b, Options{})
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	for _, line := range []string{
		"Total Savings: 64660 tokens across 5 calls\n",
		"Average Savings: 99.5% (positive only)\n",
		"Average Correctness: 83.6%\n",
	} {
		if !strings.Contains(report, line) {
			t.Fatalf("report missing %q:\n%s", line, report)
		}
	}
}

// TestParseSinceDays pins the window parser: a "<n>d" suffix or a bare number
// of days, anything else (including an out-of-i32-range value) falls back to 30,
// and negative windows stay negative so they select nothing (Rust parsed i32).
func TestParseSinceDays(t *testing.T) {
	for raw, want := range map[string]int{
		"7d":          7,
		"7":           7,
		"30d":         30,
		"1":           1,
		"-3d":         -3,
		"0d":          0,
		"30D":         DefaultRetentionDays,
		"d":           DefaultRetentionDays,
		"":            DefaultRetentionDays,
		"7 days":      DefaultRetentionDays,
		"99999999999": DefaultRetentionDays,
	} {
		if got := parseSinceDays(raw); got != want {
			t.Fatalf("parseSinceDays(%q) = %d, want %d", raw, got, want)
		}
	}
}
