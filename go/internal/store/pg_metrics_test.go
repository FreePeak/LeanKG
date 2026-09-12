package store

import (
	"strconv"
	"strings"
	"testing"
)

// TestPGMetricsRoundTrip mirrors the sqlite round trip against PostgreSQL: every
// column survives the write/read cycle, both readouts see the row, and the
// migration created the ledger (gated behind LEANKG_TEST_PG_URL).
func TestPGMetricsRoundTrip(t *testing.T) {
	s := openPGTest(t)
	if _, err := s.UsageAggregates(0); err != nil {
		t.Fatalf("migration 011 must create context_metrics: %v", err)
	}

	anchor := dayAnchor()
	want := Metric{
		ToolName: "query", Timestamp: anchor + 30, ProjectPath: "/pg",
		InputTokens: 400, OutputTokens: 120, OutputElements: 7, ExecutionTimeMs: 42,
		BaselineTokens: 9000, BaselineLinesScanned: 3000, TokensSaved: 8480, SavingsPercent: 94.2,
		CorrectElements: 7, TotalExpected: 9, F1Score: 0.91,
		QueryPattern: "handle", QueryFile: "svc.go", QueryDepth: 3, Success: true,
	}
	if err := s.RecordMetric(want); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := s.RecordMetric(Metric{ToolName: "status", Timestamp: anchor + 40, ProjectPath: "/pg"}); err != nil {
		t.Fatalf("record bare row: %v", err)
	}

	got, err := scanMetric(s.pool.QueryRow(pgCtx,
		`SELECT `+metricReadCols+` FROM context_metrics WHERE tool_name = $1`, "query").Scan)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got != want {
		t.Fatalf("row mangled:\n got %+v\nwant %+v", got, want)
	}

	sum, err := s.MetricsSummary("", 30)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if sum.TotalInvocations != 2 || sum.TotalTokensSaved != 8480 || len(sum.ByTool) != 2 ||
		sum.ByTool[0].ToolName != "query" || sum.ByTool[0].AvgCorrectnessPercent != float64(7)/9*100 {
		t.Fatalf("summary = %+v", sum)
	}
	filtered, err := s.MetricsSummary("status", 30)
	if err != nil {
		t.Fatalf("filtered summary: %v", err)
	}
	if filtered.TotalInvocations != 1 || filtered.ByTool[0].ToolName != "status" {
		t.Fatalf("tool filter = %+v", filtered)
	}

	agg, err := s.UsageAggregates(0)
	if err != nil {
		t.Fatalf("aggregates: %v", err)
	}
	if agg.Calls != 2 || agg.SuccessfulCalls != 1 || agg.TokensSaved != 8480 ||
		len(agg.Days) != 1 || agg.Days[0].Day != dayLabel(anchor+30) ||
		len(agg.Patterns) != 1 || agg.Patterns[0].Pattern != "handle" {
		t.Fatalf("aggregates = %+v", agg)
	}
}

// TestPGMetricsRetentionVerbs mirrors the cleanup/reset semantics and their
// counts: the sweep takes soft-deleted and expired rows alike, and both verbs
// report what they removed.
func TestPGMetricsRetentionVerbs(t *testing.T) {
	s := openPGTest(t)
	anchor := dayAnchor()
	for _, m := range []Metric{
		{ToolName: "query", Timestamp: anchor - 40*SecondsPerDay},
		{ToolName: "query", Timestamp: anchor - 50*SecondsPerDay, IsDeleted: true},
		{ToolName: "query", Timestamp: anchor - SecondsPerDay, TokensSaved: 20},
	} {
		if err := s.RecordMetric(m); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	n, err := s.CleanupMetrics(30)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if n != 2 {
		t.Fatalf("cleaned = %d, want 2", n)
	}
	if n, err = s.CleanupMetrics(30); err != nil || n != 0 {
		t.Fatalf("second cleanup = %d, %v; want 0, nil", n, err)
	}

	agg, err := s.UsageAggregates(0)
	if err != nil {
		t.Fatalf("aggregates: %v", err)
	}
	if agg.Calls != 1 {
		t.Fatalf("ledger after cleanup = %+v, want 1 call", agg)
	}

	if n, err = s.ResetMetrics(); err != nil || n != 1 {
		t.Fatalf("reset = %d, %v; want 1, nil", n, err)
	}
	if n, err = s.ResetMetrics(); err != nil || n != 0 {
		t.Fatalf("second reset = %d, %v; want 0, nil", n, err)
	}
}

// TestPGMetricColumnArity is the dialect-side arity guard: the PostgreSQL INSERT
// is generated from the same column list the sqlite backend uses, so a drift
// between columns and values would surface here instead of at runtime.
func TestPGMetricColumnArity(t *testing.T) {
	stmt := `INSERT INTO context_metrics (` + metricWriteCols + `) VALUES (` +
		sqlPlaceholders(metricColumnCount, true) + `)`
	if got := len(metricValues(Metric{})); got != metricColumnCount {
		t.Fatalf("metricValues arity = %d, want %d", got, metricColumnCount)
	}
	if !strings.HasSuffix(strings.TrimSpace(stmt), "$"+strconv.Itoa(metricColumnCount)+")") {
		t.Fatalf("placeholder list does not end at $%d: %s", metricColumnCount, stmt)
	}
}
