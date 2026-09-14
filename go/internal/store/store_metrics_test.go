package store

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// dayAnchor is the start of the current UTC day: fixtures are anchored to it so
// day buckets are deterministic no matter when the test runs.
func dayAnchor() int64 { return time.Now().Unix() / SecondsPerDay * SecondsPerDay }

// countMetrics reads the raw ledger size (every row, soft-deleted included).
func countMetrics(t *testing.T, s *Store) int64 {
	t.Helper()
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM context_metrics`).Scan(&n); err != nil {
		t.Fatalf("count metrics: %v", err)
	}
	return n
}

// TestMetricRecordAndReadRoundTrip pins that every ledger column survives a
// write/read cycle: typed Metric in, the same row out through the backend's
// read path, and the readout methods seeing it (Rust record_metric ->
// get_metrics_summary).
func TestMetricRecordAndReadRoundTrip(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().Unix()
	want := Metric{
		ToolName: "query", Timestamp: now - 30, ProjectPath: "/proj",
		InputTokens: 400, OutputTokens: 120, OutputElements: 7, ExecutionTimeMs: 42,
		BaselineTokens: 9000, BaselineLinesScanned: 3000, TokensSaved: 8480, SavingsPercent: 94.2,
		CorrectElements: 7, TotalExpected: 9, F1Score: 0.91,
		QueryPattern: "handle", QueryFile: "svc.go", QueryDepth: 3, Success: true,
	}
	if err := s.RecordMetric(want); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := s.RecordMetric(Metric{ToolName: "status", Timestamp: now - 10, ProjectPath: "/proj", Success: true}); err != nil {
		t.Fatalf("record bare row: %v", err)
	}
	if n := countMetrics(t, s); n != 2 {
		t.Fatalf("ledger size = %d, want 2", n)
	}

	got, err := scanMetric(s.db.QueryRow(
		`SELECT `+metricReadCols+` FROM context_metrics WHERE tool_name = ?`, "query").Scan)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got != want {
		t.Fatalf("row mangled:\n got %+v\nwant %+v", got, want)
	}

	// The unset optional columns of the bare row read as zero values, which is
	// what every reader treats as "not measured" (Rust's unwrap_or(0)).
	bare, err := scanMetric(s.db.QueryRow(
		`SELECT `+metricReadCols+` FROM context_metrics WHERE tool_name = ?`, "status").Scan)
	if err != nil {
		t.Fatalf("read bare row: %v", err)
	}
	if bare.CorrectElements != 0 || bare.TotalExpected != 0 || bare.F1Score != 0 ||
		bare.QueryPattern != "" || bare.QueryFile != "" || bare.QueryDepth != 0 {
		t.Fatalf("unset optionals must read as zero values: %+v", bare)
	}

	sum, err := s.MetricsSummary("", 30)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if sum.TotalInvocations != 2 || sum.TotalTokensSaved != 8480 {
		t.Fatalf("summary over both rows: %+v", sum)
	}
	sum, err = s.MetricsSummary("status", 30)
	if err != nil {
		t.Fatalf("filtered summary: %v", err)
	}
	if sum.TotalInvocations != 1 || sum.TotalTokensSaved != 0 || len(sum.ByTool) != 1 ||
		sum.ByTool[0].ToolName != "status" {
		t.Fatalf("tool filter summary: %+v", sum)
	}
}

// TestMetricsSummaryBuckets pins the report's fold rules, quirks included:
// positive-only savings totals, positive-only savings average, correctness
// averaged over rows carrying a total, the per-tool savings denominator being
// ALL of the tool's calls, soft-deleted rows excluded, and the retention window
// cutting the rest.
func TestMetricsSummaryBuckets(t *testing.T) {
	s := openTestStore(t)
	anchor := dayAnchor()
	rows := []Metric{
		// Today, tool query: 8/10 correct, saves 100.
		{ToolName: "query", Timestamp: anchor + 100, SavingsPercent: 90, TokensSaved: 100,
			CorrectElements: 8, TotalExpected: 10, Success: true},
		// Today, tool query: saves nothing and has negative savings percent, no
		// correctness — it counts as a call but not as a savings data point.
		{ToolName: "query", Timestamp: anchor + 200, SavingsPercent: -5.5, Success: true},
		// Today, tool search: 9/10 correct, saves 300.
		{ToolName: "search", Timestamp: anchor + 300, SavingsPercent: 95, TokensSaved: 300,
			CorrectElements: 9, TotalExpected: 10, Success: true},
		// Three days ago, tool search: 10/10 correct, saves 200.
		{ToolName: "search", Timestamp: anchor - 3*SecondsPerDay + 500, SavingsPercent: 80,
			TokensSaved: 200, CorrectElements: 10, TotalExpected: 10, Success: true},
		// Soft-deleted: never counted.
		{ToolName: "query", Timestamp: anchor + 400, SavingsPercent: 99, TokensSaved: 99999,
			IsDeleted: true},
		// Older than the retention window: excluded.
		{ToolName: "query", Timestamp: anchor - 40*SecondsPerDay, SavingsPercent: 99,
			TokensSaved: 50000, CorrectElements: 1, TotalExpected: 1},
	}
	for _, r := range rows[:5] {
		if err := s.RecordMetric(r); err != nil {
			t.Fatalf("record %s: %v", r.ToolName, err)
		}
	}

	sum, err := s.MetricsSummary("", 30)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if sum.TotalInvocations != 4 {
		t.Fatalf("total_invocations = %d, want 4 (soft-deleted and out-of-window rows excluded)", sum.TotalInvocations)
	}
	if sum.TotalTokensSaved != 600 {
		t.Fatalf("total_tokens_saved = %d, want 600 (positive rows only)", sum.TotalTokensSaved)
	}
	if want := (90.0 + 95.0 + 80.0) / 3; sum.AverageSavingsPercent != want {
		t.Fatalf("average_savings_percent = %v, want %v", sum.AverageSavingsPercent, want)
	}
	if want := (80.0 + 90.0 + 100.0) / 3; sum.AverageCorrectnessPercent != want {
		t.Fatalf("average_correctness_percent = %v, want %v", sum.AverageCorrectnessPercent, want)
	}
	if sum.RetentionDays != 30 {
		t.Fatalf("retention_days = %d, want 30", sum.RetentionDays)
	}

	// Tools are key-ascending.
	if len(sum.ByTool) != 2 || sum.ByTool[0].ToolName != "query" || sum.ByTool[1].ToolName != "search" {
		t.Fatalf("by_tool = %+v, want query then search", sum.ByTool)
	}
	q := sum.ByTool[0]
	if q.Calls != 2 || q.TotalSaved != 100 {
		t.Fatalf("query bucket = %+v", q)
	}
	// Rust divides the summed per-tool percentage by ALL callers of that tool,
	// so the -5.5 call drags the average down even though it saved nothing.
	if want := (90.0 + -5.5) / 2; q.AvgSavingsPercent != want {
		t.Fatalf("query avg_savings_percent = %v, want %v", q.AvgSavingsPercent, want)
	}
	if q.AvgCorrectnessPercent != 80 {
		t.Fatalf("query avg_correctness_percent = %v, want 80", q.AvgCorrectnessPercent)
	}
	search := sum.ByTool[1]
	if search.Calls != 2 || search.TotalSaved != 500 || search.AvgSavingsPercent != 87.5 || search.AvgCorrectnessPercent != 95 {
		t.Fatalf("search bucket = %+v", search)
	}

	// Days are chronological, with the same positive-only savings rule.
	if len(sum.ByDay) != 2 {
		t.Fatalf("by_day = %+v, want 2 buckets", sum.ByDay)
	}
	today := dayLabel(anchor)
	older := dayLabel(anchor - 3*SecondsPerDay)
	if sum.ByDay[0].Date != older || sum.ByDay[1].Date != today {
		t.Fatalf("by_day order = %+v, want %s then %s", sum.ByDay, older, today)
	}
	if sum.ByDay[1].Calls != 3 || sum.ByDay[1].Savings != 400 {
		t.Fatalf("today bucket = %+v, want 3 calls / 400 saved", sum.ByDay[1])
	}
	if want := (80.0 + 90.0) / 2; sum.ByDay[1].Correctness != want {
		t.Fatalf("today correctness = %v, want %v", sum.ByDay[1].Correctness, want)
	}
	if sum.ByDay[0].Calls != 1 || sum.ByDay[0].Savings != 200 || sum.ByDay[0].Correctness != 100 {
		t.Fatalf("older bucket = %+v", sum.ByDay[0])
	}

	// The retention window is a real filter: 1 day keeps only today's rows.
	todayOnly, err := s.MetricsSummary("", 1)
	if err != nil {
		t.Fatalf("1-day summary: %v", err)
	}
	if todayOnly.TotalInvocations != 3 || len(todayOnly.ByDay) != 1 {
		t.Fatalf("1-day window = %+v", todayOnly)
	}
}

// TestUsageAggregatesBuckets pins the H10/FR-PLG-8 buckets: every row counts
// (negative savings included), tools carry a mean latency, days are UTC day
// buckets labelled YYYY-MM-DD, projects are grouped, rows without a query
// pattern are not a pattern bucket, and a cutoff windows the read.
func TestUsageAggregatesBuckets(t *testing.T) {
	s := openTestStore(t)
	anchor := dayAnchor()
	rows := []Metric{
		{ToolName: "query", Timestamp: anchor + 10, ProjectPath: "/a", InputTokens: 100, OutputTokens: 40,
			ExecutionTimeMs: 30, TokensSaved: 60, SavingsPercent: 60, Success: true, QueryPattern: "handle"},
		{ToolName: "query", Timestamp: anchor + 20, ProjectPath: "/a", InputTokens: 200, OutputTokens: 60,
			ExecutionTimeMs: 10, TokensSaved: -20, SavingsPercent: -20, Success: false, QueryPattern: "handle"},
		{ToolName: "status", Timestamp: anchor - SecondsPerDay + 30, ProjectPath: "/b", InputTokens: 50,
			OutputTokens: 20, ExecutionTimeMs: 5, TokensSaved: 40, SavingsPercent: 70, Success: true},
		{ToolName: "query", Timestamp: anchor + 40, ProjectPath: "/a", TokensSaved: 1, IsDeleted: true},
	}
	for _, r := range rows {
		if err := s.RecordMetric(r); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	agg, err := s.UsageAggregates(0)
	if err != nil {
		t.Fatalf("aggregates: %v", err)
	}
	if agg.Calls != 3 || agg.SuccessfulCalls != 2 {
		t.Fatalf("calls/success = %d/%d, want 3/2 (soft-deleted excluded)", agg.Calls, agg.SuccessfulCalls)
	}
	if agg.InputTokens != 350 || agg.OutputTokens != 120 {
		t.Fatalf("token totals = %d/%d, want 350/120", agg.InputTokens, agg.OutputTokens)
	}
	// The dashboard totals include the negative row: raw usage, not savings.
	if agg.TokensSaved != 80 || agg.SavingsPercentSum != 110 {
		t.Fatalf("saved/percent sum = %d/%v, want 80/110", agg.TokensSaved, agg.SavingsPercentSum)
	}

	if len(agg.Tools) != 2 || agg.Tools[0].Tool != "query" || agg.Tools[1].Tool != "status" {
		t.Fatalf("tools = %+v, want query then status", agg.Tools)
	}
	if agg.Tools[0].Calls != 2 || agg.Tools[0].TokensSaved != 40 || agg.Tools[0].AvgMS != 20 {
		t.Fatalf("query bucket = %+v", agg.Tools[0])
	}
	if agg.Tools[1].Calls != 1 || agg.Tools[1].AvgMS != 5 {
		t.Fatalf("status bucket = %+v", agg.Tools[1])
	}

	if len(agg.Days) != 2 {
		t.Fatalf("days = %+v, want 2 buckets", agg.Days)
	}
	older := dayLabel(anchor - SecondsPerDay)
	today := dayLabel(anchor)
	if agg.Days[0].Day != older || agg.Days[1].Day != today {
		t.Fatalf("day buckets = %+v, want %s then %s", agg.Days, older, today)
	}
	if agg.Days[1].Calls != 2 || agg.Days[1].TokensSaved != 40 {
		t.Fatalf("today bucket = %+v", agg.Days[1])
	}

	if len(agg.Projects) != 2 || agg.Projects[0].Project != "/a" || agg.Projects[0].Calls != 2 ||
		agg.Projects[0].TokensSaved != 40 {
		t.Fatalf("projects = %+v", agg.Projects)
	}
	if agg.Projects[1].Project != "/b" || agg.Projects[1].Calls != 1 {
		t.Fatalf("projects = %+v", agg.Projects)
	}

	// Only rows that named a pattern, and both of them share one bucket.
	if len(agg.Patterns) != 1 || agg.Patterns[0].Pattern != "handle" ||
		agg.Patterns[0].Calls != 2 || agg.Patterns[0].TokensSaved != 40 {
		t.Fatalf("patterns = %+v", agg.Patterns)
	}

	// A cutoff excludes the previous day's row.
	windowed, err := s.UsageAggregates(anchor)
	if err != nil {
		t.Fatalf("windowed aggregates: %v", err)
	}
	if windowed.Calls != 2 || len(windowed.Days) != 1 || len(windowed.Tools) != 1 ||
		windowed.Tools[0].Tool != "query" {
		t.Fatalf("windowed aggregates = %+v", windowed)
	}
}

// TestMetricsCleanupRetention pins the retention sweep: rows older than the
// window go (soft-deleted ones included — cleanup is not a view filter), the
// count is the number removed, newer rows stay, and a second sweep is a no-op.
func TestMetricsCleanupRetention(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().Unix()
	old := Metric{ToolName: "query", Timestamp: now - 40*SecondsPerDay, TokensSaved: 10}
	olderSoftDeleted := Metric{ToolName: "query", Timestamp: now - 50*SecondsPerDay, IsDeleted: true}
	fresh := Metric{ToolName: "query", Timestamp: now - SecondsPerDay, TokensSaved: 20}
	for _, m := range []Metric{old, olderSoftDeleted, fresh} {
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
	if got := countMetrics(t, s); got != 1 {
		t.Fatalf("ledger size after cleanup = %d, want 1", got)
	}
	if n, err = s.CleanupMetrics(30); err != nil || n != 0 {
		t.Fatalf("second cleanup = %d, %v; want 0, nil", n, err)
	}

	// A zero-day retention window sweeps everything older than now.
	if n, err = s.CleanupMetrics(0); err != nil || n != 1 {
		t.Fatalf("zero-day cleanup = %d, %v; want 1, nil", n, err)
	}
	if got := countMetrics(t, s); got != 0 {
		t.Fatalf("ledger size after full sweep = %d, want 0", got)
	}
}

// TestMetricsResetAll pins reset: the whole ledger goes (soft-deleted rows
// included), the count is reported, and a second reset is a no-op.
func TestMetricsResetAll(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().Unix()
	for i := 0; i < 3; i++ {
		if err := s.RecordMetric(Metric{ToolName: "query", Timestamp: now - int64(i), IsDeleted: i == 0}); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	n, err := s.ResetMetrics()
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if n != 3 {
		t.Fatalf("reset = %d, want 3", n)
	}
	if got := countMetrics(t, s); got != 0 {
		t.Fatalf("ledger size after reset = %d, want 0", got)
	}
	if n, err = s.ResetMetrics(); err != nil || n != 0 {
		t.Fatalf("second reset = %d, %v; want 0, nil", n, err)
	}
}

// TestRecordMetricReadOnlyNoOp pins Rust's is_read_only guard: the reader role
// records nothing and must not turn every served call into a write error.
func TestRecordMetricReadOnlyNoOp(t *testing.T) {
	dir := t.TempDir()
	rw, err := Open(filepath.Join(dir, ".leankg", "leankg.db"), RW)
	if err != nil {
		t.Fatalf("open rw: %v", err)
	}
	if err := rw.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := rw.Close(); err != nil {
		t.Fatalf("close rw: %v", err)
	}

	ro, err := Open(filepath.Join(dir, ".leankg", "leankg.db"), RO)
	if err != nil {
		t.Fatalf("open ro: %v", err)
	}
	t.Cleanup(func() { ro.Close() })
	if err := ro.RecordMetric(Metric{ToolName: "query", Timestamp: time.Now().Unix()}); err != nil {
		t.Fatalf("read-only record must no-op, got %v", err)
	}
	if n := countMetrics(t, ro); n != 0 {
		t.Fatalf("read-only store recorded %d rows, want 0", n)
	}
	// Reads still work on the reader role.
	if _, err := ro.MetricsSummary("", 30); err != nil {
		t.Fatalf("read-only summary: %v", err)
	}
	if _, err := ro.UsageAggregates(0); err != nil {
		t.Fatalf("read-only aggregates: %v", err)
	}
}

// TestMetricsMigrationIsAdditiveAndIdempotent pins migration 011 on a store the
// previous layout left behind: the ledger table is absent until Migrate runs,
// becomes usable in place, keeps its rows across a second Migrate, and the
// migration ledger stays ascending and unique.
func TestMetricsMigrationIsAdditiveAndIdempotent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	// A store whose ledger claims every migration BEFORE 011.
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("legacy ledger: %v", err)
	}
	for _, m := range migrations {
		if m.version >= 11 {
			continue
		}
		if _, err := s.db.Exec(
			`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, '')`,
			m.version, m.name); err != nil {
			t.Fatalf("record legacy migration %d: %v", m.version, err)
		}
	}
	if _, err := s.db.Exec(`SELECT COUNT(*) FROM context_metrics`); err == nil {
		t.Fatal("context_metrics must not exist before migration 011")
	}

	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate 011: %v", err)
	}
	if err := s.RecordMetric(Metric{ToolName: "query", Timestamp: time.Now().Unix(), TokensSaved: 5}); err != nil {
		t.Fatalf("record after upgrade: %v", err)
	}

	// Re-running the migration is a no-op and keeps the rows.
	if err := s.Migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if n := countMetrics(t, s); n != 1 {
		t.Fatalf("ledger size after second migrate = %d, want 1", n)
	}

	applied, err := AppliedMigrations(s)
	if err != nil {
		t.Fatalf("applied migrations: %v", err)
	}
	seen := map[int]bool{}
	for i, v := range applied {
		if seen[v] {
			t.Fatalf("duplicate migration version %d in ledger %v", v, applied)
		}
		seen[v] = true
		if i > 0 && v <= applied[i-1] {
			t.Fatalf("migration ledger not ascending: %v", applied)
		}
	}
	if !seen[11] {
		t.Fatalf("migration 011 missing from ledger %v", applied)
	}
}

// TestMetricColumnArityAndCoverage pins the write/read column lists against
// each other and against metricValues: a column added to one half without the
// other cannot silently mis-bind or drop a field.
func TestMetricColumnArityAndCoverage(t *testing.T) {
	cols := strings.Split(metricWriteCols, ",")
	if len(cols) != metricColumnCount {
		t.Fatalf("metricColumnCount = %d, want %d", metricColumnCount, len(cols))
	}
	if got := len(metricValues(Metric{})); got != metricColumnCount {
		t.Fatalf("metricValues arity = %d, want %d", got, metricColumnCount)
	}
	for _, c := range cols {
		name := strings.TrimSpace(c)
		if !strings.Contains(metricReadCols, name) {
			t.Fatalf("column %q is written but never read (add it to metricReadCols)", name)
		}
	}
	if strings.Count(sqlPlaceholders(metricColumnCount, false), "?") != metricColumnCount {
		t.Fatal("sqlite placeholder list is short")
	}
	if !strings.HasSuffix(sqlPlaceholders(metricColumnCount, true), "$"+strconv.Itoa(metricColumnCount)) {
		t.Fatalf("postgres placeholder list must end at $%d", metricColumnCount)
	}
}

// TestDayLabelAndFloorDiv pins the UTC day bucketing: epoch division is
// floored (Rust div_euclid), which the pre-1970 stamps prove, and the label is
// the plain ISO date (the values Rust's dashboard tests assert).
func TestDayLabelAndFloorDiv(t *testing.T) {
	if got := dayLabel(0); got != "1970-01-01" {
		t.Fatalf("dayLabel(0) = %q", got)
	}
	if got := dayLabel(19_000 * SecondsPerDay); got != "2022-01-08" {
		t.Fatalf("dayLabel(19000 days) = %q", got)
	}
	if got := dayLabel(-1); got != "1969-12-31" {
		t.Fatalf("dayLabel(-1) = %q, want the floored day", got)
	}
	if got := floorDiv(-1, SecondsPerDay); got != -1 {
		t.Fatalf("floorDiv(-1, 86400) = %d, want -1", got)
	}
	if got := floorDiv(-SecondsPerDay, SecondsPerDay); got != -1 {
		t.Fatalf("floorDiv(-86400, 86400) = %d, want -1", got)
	}
}

// TestMigration011DdlCoversEveryLedgerColumn pins both dialects' migration 011
// against the column list the record/read paths bind: a column renamed or
// forgotten in one DDL would otherwise only surface at runtime (and the
// PostgreSQL path is gated behind LEANKG_TEST_PG_URL, so it would surface late).
func TestMigration011DdlCoversEveryLedgerColumn(t *testing.T) {
	ddls := map[string]string{}
	for _, m := range migrations {
		if m.version == 11 {
			ddls["sqlite"] = m.ddl
		}
	}
	for _, m := range pgMigrations {
		if m.version == 11 {
			ddls["postgres"] = m.ddl
		}
	}
	for dialect, ddl := range ddls {
		if ddl == "" {
			t.Fatalf("%s migration 011 is missing", dialect)
		}
		if !strings.Contains(ddl, "CREATE TABLE IF NOT EXISTS context_metrics") {
			t.Fatalf("%s migration 011 does not create context_metrics", dialect)
		}
		for _, col := range strings.Split(metricWriteCols, ",") {
			name := strings.TrimSpace(col)
			if !regexp.MustCompile(`(?m)^\s*` + name + `\s`).MatchString(ddl) {
				t.Fatalf("%s migration 011 does not declare column %q", dialect, name)
			}
		}
		for _, index := range []string{
			"idx_context_metrics_tool_name", "idx_context_metrics_timestamp", "idx_context_metrics_project_path",
		} {
			if !strings.Contains(ddl, index) {
				t.Fatalf("%s migration 011 is missing index %s", dialect, index)
			}
		}
	}
}
