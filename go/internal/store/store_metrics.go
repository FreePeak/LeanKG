// Context-metrics ledger: the persisted usage record of every served tool
// call. Ported from the Rust engine's Cozo `context_metrics` table — the DDL
// (src/db/pg/schema.sql), the recording path (src/db/mod.rs record_metric), the
// `leankg metrics` report (get_metrics_summary / cleanup_old_metrics /
// reset_metrics) and the H10/FR-PLG-8 usage buckets (src/dashboard/mod.rs over
// the grouped queries in src/db/backend.rs).
//
// Two readouts sit on this table and they answer different questions:
//
//   - MetricsSummary (`leankg metrics`): context savings. Savings are
//     POSITIVE-ONLY (a call that saved nothing contributes to no average) and
//     correctness is the mean of correct/total over rows that carry a total.
//     The per-tool average savings divides by ALL of the tool's calls — Rust's
//     fold, quirk included.
//   - UsageAggregates (the `leankg dashboard` seam): raw usage totals, where
//     every row counts (negative savings included), so the two must not be
//     unified.
//
// Both collapse one windowed row read per backend through the shared folds
// below, so sqlite and postgres report identical buckets. Rust split the same
// work differently (Cozo script -> Rust fold for the report; grouped SQL over
// PostgreSQL only for the dashboard); the Go engine runs the dashboard seam on
// BOTH backends, which is a deliberate extension.
//
// ponytail: the window read materializes rows instead of grouping in SQL.
// Ceiling: a multi-million-row window pays for the row transfer. Upgrade path:
// per-dialect GROUP BY queries behind these same two methods, cross-checked
// against summarizeMetrics/aggregateUsage.
package store

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SecondsPerDay is the epoch-second width of one UTC day. context_metrics
// timestamps are epoch seconds and every bucketing path divides by this.
const SecondsPerDay int64 = 86_400

// Metric is one context_metrics row: one served tool call.
//
// Rust declared six nullable columns (correct_elements, total_expected,
// f1_score, query_pattern, query_file, query_depth) for "not measured /
// not applicable". Readers all treated NULL as the zero value, so the Go port
// carries plain fields and normalizes on read (see metricReadCols); a writer
// that has nothing to report leaves them at their zero value. The columns stay
// nullable, so Rust-era rows still read correctly.
type Metric struct {
	ToolName             string  `json:"tool_name"`
	Timestamp            int64   `json:"timestamp"` // epoch seconds
	ProjectPath          string  `json:"project_path"`
	InputTokens          int64   `json:"input_tokens"`
	OutputTokens         int64   `json:"output_tokens"`
	OutputElements       int64   `json:"output_elements"`
	ExecutionTimeMs      int64   `json:"execution_time_ms"`
	BaselineTokens       int64   `json:"baseline_tokens"`
	BaselineLinesScanned int64   `json:"baseline_lines_scanned"`
	TokensSaved          int64   `json:"tokens_saved"`
	SavingsPercent       float64 `json:"savings_percent"`
	CorrectElements      int64   `json:"correct_elements"`
	TotalExpected        int64   `json:"total_expected"`
	F1Score              float64 `json:"f1_score"`
	QueryPattern         string  `json:"query_pattern"`
	QueryFile            string  `json:"query_file"`
	QueryDepth           int64   `json:"query_depth"`
	Success              bool    `json:"success"`
	IsDeleted            bool    `json:"is_deleted"`
}

// MetricSummary is the `leankg metrics` report (Rust models::MetricsSummary).
// Field names and order are the serialized contract: the CLI prints this
// struct as JSON verbatim.
type MetricSummary struct {
	TotalInvocations          int64          `json:"total_invocations"`
	TotalTokensSaved          int64          `json:"total_tokens_saved"`
	AverageSavingsPercent     float64        `json:"average_savings_percent"`
	AverageCorrectnessPercent float64        `json:"average_correctness_percent"`
	RetentionDays             int            `json:"retention_days"`
	ByTool                    []ToolMetrics  `json:"by_tool"`
	ByDay                     []DailyMetrics `json:"by_day"`
}

// ToolMetrics is one per-tool row of MetricSummary.
type ToolMetrics struct {
	ToolName              string  `json:"tool_name"`
	Calls                 int64   `json:"calls"`
	AvgSavingsPercent     float64 `json:"avg_savings_percent"`
	AvgCorrectnessPercent float64 `json:"avg_correctness_percent"`
	TotalSaved            int64   `json:"total_saved"`
}

// DailyMetrics is one UTC-day row of MetricSummary.
type DailyMetrics struct {
	Date        string  `json:"date"`
	Calls       int64   `json:"calls"`
	Savings     int64   `json:"savings"`
	Correctness float64 `json:"correctness"`
}

// UsageAggregates is the H10/FR-PLG-8 usage-bucket seam: already-grouped rows
// over one window, never the raw ledger (Rust dashboard::UsageAggregates).
// Buckets come back key-ascending so callers get a deterministic order; the
// dashboard re-sorts for display.
type UsageAggregates struct {
	Calls             int64
	InputTokens       int64
	OutputTokens      int64
	TokensSaved       int64
	SavingsPercentSum float64
	SuccessfulCalls   int64
	Tools             []ToolUsage
	Days              []DayUsage
	Projects          []ProjectUsage
	Patterns          []PatternUsage
}

// ToolUsage is one per-tool bucket: calls, tokens saved and mean latency.
type ToolUsage struct {
	Tool        string  `json:"tool"`
	Calls       int64   `json:"calls"`
	TokensSaved int64   `json:"tokens_saved"`
	AvgMS       float64 `json:"avg_ms"`
}

// DayUsage is one UTC-day bucket, labelled YYYY-MM-DD.
type DayUsage struct {
	Day         string `json:"day"`
	Calls       int64  `json:"calls"`
	TokensSaved int64  `json:"tokens_saved"`
}

// ProjectUsage is one project_path bucket.
type ProjectUsage struct {
	Project     string `json:"project"`
	Calls       int64  `json:"calls"`
	TokensSaved int64  `json:"tokens_saved"`
}

// PatternUsage is one query_pattern bucket (rows without a pattern are
// excluded, as in Rust's `query_pattern IS NOT NULL`).
type PatternUsage struct {
	Pattern     string `json:"pattern"`
	Calls       int64  `json:"calls"`
	TokensSaved int64  `json:"tokens_saved"`
}

// metricWriteCols is the INSERT column list; metricReadCols is the same row
// with the NULL -> zero normalization every reader applied. They are separate
// because COALESCE is legal in a SELECT list and not in a column list.
const metricWriteCols = `tool_name, timestamp, project_path, input_tokens, output_tokens,
	output_elements, execution_time_ms, baseline_tokens, baseline_lines_scanned, tokens_saved,
	savings_percent, correct_elements, total_expected, f1_score, query_pattern, query_file,
	query_depth, success, is_deleted`

const metricReadCols = `tool_name, timestamp, project_path, input_tokens, output_tokens,
	output_elements, execution_time_ms, baseline_tokens, baseline_lines_scanned, tokens_saved,
	savings_percent, COALESCE(correct_elements, 0), COALESCE(total_expected, 0),
	COALESCE(f1_score, 0), COALESCE(query_pattern, ''), COALESCE(query_file, ''),
	COALESCE(query_depth, 0), success, is_deleted`

// metricColumnCount is the arity of one ledger row. Both dialects' placeholder
// lists are generated from it (sqlPlaceholders) and metricValues is pinned to
// it by a test, so a column added to one half cannot silently mis-bind.
var metricColumnCount = len(strings.Split(metricWriteCols, ","))

// metricValues renders m in metricWriteCols order.
func metricValues(m Metric) []any {
	return []any{
		m.ToolName, m.Timestamp, m.ProjectPath, m.InputTokens, m.OutputTokens,
		m.OutputElements, m.ExecutionTimeMs, m.BaselineTokens, m.BaselineLinesScanned,
		m.TokensSaved, m.SavingsPercent, m.CorrectElements, m.TotalExpected, m.F1Score,
		m.QueryPattern, m.QueryFile, m.QueryDepth, m.Success, m.IsDeleted,
	}
}

// sqlPlaceholders renders n bind placeholders for the two dialects:
// "?, ?, ?" or "$1, $2, $3".
func sqlPlaceholders(n int, dollar bool) string {
	parts := make([]string, n)
	for i := range parts {
		if dollar {
			parts[i] = "$" + strconv.Itoa(i+1)
		} else {
			parts[i] = "?"
		}
	}
	return strings.Join(parts, ", ")
}

// scanMetric scans one row of metricReadCols through either backend's Scan.
func scanMetric(scan func(dest ...any) error) (Metric, error) {
	var m Metric
	if err := scan(&m.ToolName, &m.Timestamp, &m.ProjectPath, &m.InputTokens, &m.OutputTokens,
		&m.OutputElements, &m.ExecutionTimeMs, &m.BaselineTokens, &m.BaselineLinesScanned,
		&m.TokensSaved, &m.SavingsPercent, &m.CorrectElements, &m.TotalExpected, &m.F1Score,
		&m.QueryPattern, &m.QueryFile, &m.QueryDepth, &m.Success, &m.IsDeleted); err != nil {
		return Metric{}, fmt.Errorf("store: scan metric: %w", err)
	}
	return m, nil
}

// scanMetrics drains a metrics window; the caller owns closing rows.
func scanMetrics(rows versionRows) ([]Metric, error) {
	var out []Metric
	for rows.Next() {
		m, err := scanMetric(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: metrics window: %w", err)
	}
	return out, nil
}

// metricsCutoff is the epoch-second lower bound of a retention window.
func metricsCutoff(retentionDays int) int64 {
	return time.Now().Unix() - int64(retentionDays)*SecondsPerDay
}

// RecordMetric appends one context_metrics row (Rust db::record_metric).
//
// A read-only backend no-ops with a nil error: Rust skipped the write on the
// reader-role connection rather than turning every served tool call into a bogus
// database error.
func (s *Store) RecordMetric(m Metric) error {
	if s.mode == RO {
		return nil
	}
	if _, err := s.db.Exec(`INSERT INTO context_metrics (`+metricWriteCols+`) VALUES (`+
		sqlPlaceholders(metricColumnCount, false)+`)`, metricValues(m)...); err != nil {
		return fmt.Errorf("store: record metric %s: %w", m.ToolName, err)
	}
	return nil
}

// metricRows reads one live window: rows at/after sinceCutoff, the tool filter
// applied when non-empty, soft-deleted rows always excluded, ascending by
// (timestamp, tool_name) so every fold below is reproducible.
func (s *Store) metricRows(tool string, sinceCutoff int64) ([]Metric, error) {
	q := `SELECT ` + metricReadCols + ` FROM context_metrics WHERE is_deleted = 0 AND timestamp >= ?`
	args := []any{sinceCutoff}
	if tool != "" {
		q += ` AND tool_name = ?`
		args = append(args, tool)
	}
	q += ` ORDER BY timestamp, tool_name`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: metrics window: %w", err)
	}
	defer rows.Close()
	return scanMetrics(rows)
}

// MetricsSummary reports the last retentionDays of the ledger, optionally for
// one tool (empty = every tool).
func (s *Store) MetricsSummary(tool string, retentionDays int) (MetricSummary, error) {
	rows, err := s.metricRows(tool, metricsCutoff(retentionDays))
	if err != nil {
		return MetricSummary{}, err
	}
	return summarizeMetrics(rows, retentionDays), nil
}

// UsageAggregates returns the H10/FR-PLG-8 buckets. sinceCutoff == 0 means
// "all time": the filter is `timestamp >= sinceCutoff` and epoch-second stamps
// are never negative.
func (s *Store) UsageAggregates(sinceCutoff int64) (UsageAggregates, error) {
	rows, err := s.metricRows("", sinceCutoff)
	if err != nil {
		return UsageAggregates{}, err
	}
	return aggregateUsage(rows), nil
}

// CleanupMetrics deletes rows older than retentionDays and reports how many
// went (Rust db::cleanup_old_metrics). Soft-deleted rows are purged too:
// cleanup is a retention sweep, not a view filter.
func (s *Store) CleanupMetrics(retentionDays int) (int64, error) {
	cutoff := metricsCutoff(retentionDays)
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM context_metrics WHERE timestamp < ?`, cutoff).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count metrics before cleanup: %w", err)
	}
	if n == 0 {
		return 0, nil
	}
	if _, err := s.db.Exec(`DELETE FROM context_metrics WHERE timestamp < ?`, cutoff); err != nil {
		return 0, fmt.Errorf("store: cleanup metrics: %w", err)
	}
	return n, nil
}

// ResetMetrics deletes the whole ledger and reports how many rows went
// (Rust db::reset_metrics).
func (s *Store) ResetMetrics() (int64, error) {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM context_metrics`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count metrics for reset: %w", err)
	}
	if n == 0 {
		return 0, nil
	}
	if _, err := s.db.Exec(`DELETE FROM context_metrics`); err != nil {
		return 0, fmt.Errorf("store: reset metrics: %w", err)
	}
	return n, nil
}

// summarizeMetrics folds a live window into the report shape. The rules are
// Rust db::get_metrics_summary's, quirks included:
//
//   - total_tokens_saved and average_savings_percent count only rows with
//     tokens_saved > 0;
//   - average_correctness_percent averages correct/total*100 over rows with
//     total_expected > 0;
//   - per-tool avg_savings_percent divides the summed percentage by ALL of the
//     tool's calls, while avg_correctness_percent divides by the tool's
//     correctness-bearing calls. The two denominators genuinely differ in Rust.
//
// by_day is the one deliberate extension: Rust declared the field and rendered
// a "By Day" section for it but never filled it (its branch was unreachable
// dead code). It is filled here from the same window, with the same
// positive-only savings rule and the same mean-correctness rule, bucketed by
// UTC day and sorted ascending.
func summarizeMetrics(rows []Metric, retentionDays int) MetricSummary {
	sum := MetricSummary{
		RetentionDays: retentionDays,
		ByTool:        []ToolMetrics{},
		ByDay:         []DailyMetrics{},
	}
	type toolFold struct {
		calls, totalSaved, correctCalls int64
		sumPct, sumCorrect              float64
	}
	type dayFold struct {
		calls, savings, correctCalls int64
		sumCorrect                   float64
	}
	tools := map[string]*toolFold{}
	days := map[string]*dayFold{}

	var sumPct float64
	var positiveCalls int64
	var sumCorrect float64
	var correctCalls int64

	for _, r := range rows {
		name := r.ToolName
		if name == "" {
			name = "unknown" // Rust: row[0].get_str().unwrap_or("unknown")
		}
		sum.TotalInvocations++
		saved := r.TokensSaved
		if saved > 0 {
			sum.TotalTokensSaved += saved
			sumPct += r.SavingsPercent
			positiveCalls++
		}
		hasCorrectness := r.TotalExpected > 0
		var pct float64
		if hasCorrectness {
			pct = float64(r.CorrectElements) / float64(r.TotalExpected) * 100
			sumCorrect += pct
			correctCalls++
		}

		t := tools[name]
		if t == nil {
			t = &toolFold{}
			tools[name] = t
		}
		t.calls++
		if saved > 0 {
			t.totalSaved += saved
		}
		t.sumPct += r.SavingsPercent
		if hasCorrectness {
			t.sumCorrect += pct
			t.correctCalls++
		}

		day := dayLabel(r.Timestamp)
		d := days[day]
		if d == nil {
			d = &dayFold{}
			days[day] = d
		}
		d.calls++
		if saved > 0 {
			d.savings += saved
		}
		if hasCorrectness {
			d.sumCorrect += pct
			d.correctCalls++
		}
	}

	if positiveCalls > 0 {
		sum.AverageSavingsPercent = sumPct / float64(positiveCalls)
	}
	if correctCalls > 0 {
		sum.AverageCorrectnessPercent = sumCorrect / float64(correctCalls)
	}
	for _, name := range sortedKeys(tools) {
		t := tools[name]
		tm := ToolMetrics{ToolName: name, Calls: t.calls, TotalSaved: t.totalSaved}
		if t.calls > 0 {
			tm.AvgSavingsPercent = t.sumPct / float64(t.calls)
		}
		if t.correctCalls > 0 {
			tm.AvgCorrectnessPercent = t.sumCorrect / float64(t.correctCalls)
		}
		sum.ByTool = append(sum.ByTool, tm)
	}
	for _, day := range sortedKeys(days) {
		d := days[day]
		dm := DailyMetrics{Date: day, Calls: d.calls, Savings: d.savings}
		if d.correctCalls > 0 {
			dm.Correctness = d.sumCorrect / float64(d.correctCalls)
		}
		sum.ByDay = append(sum.ByDay, dm)
	}
	return sum
}

// aggregateUsage folds a live window into the usage buckets (Rust
// dashboard::aggregate_rows). Unlike the report, every row counts: totals sum
// all tokens_saved, and avg_savings_percent/success_rate divide the sums by
// the call count.
func aggregateUsage(rows []Metric) UsageAggregates {
	agg := UsageAggregates{
		Tools:    []ToolUsage{},
		Days:     []DayUsage{},
		Projects: []ProjectUsage{},
		Patterns: []PatternUsage{},
	}
	type fold struct {
		calls, saved, ms int64
	}
	type dayFold struct {
		calls, saved int64
	}
	tools := map[string]*fold{}
	days := map[int64]*dayFold{}
	projects := map[string]*fold{}
	patterns := map[string]*fold{}

	for _, r := range rows {
		agg.Calls++
		agg.InputTokens += r.InputTokens
		agg.OutputTokens += r.OutputTokens
		agg.TokensSaved += r.TokensSaved
		agg.SavingsPercentSum += r.SavingsPercent
		if r.Success {
			agg.SuccessfulCalls++
		}

		t := tools[r.ToolName]
		if t == nil {
			t = &fold{}
			tools[r.ToolName] = t
		}
		t.calls++
		t.saved += r.TokensSaved
		t.ms += r.ExecutionTimeMs

		bucket := floorDiv(r.Timestamp, SecondsPerDay)
		d := days[bucket]
		if d == nil {
			d = &dayFold{}
			days[bucket] = d
		}
		d.calls++
		d.saved += r.TokensSaved

		p := projects[r.ProjectPath]
		if p == nil {
			p = &fold{}
			projects[r.ProjectPath] = p
		}
		p.calls++
		p.saved += r.TokensSaved

		if r.QueryPattern != "" {
			q := patterns[r.QueryPattern]
			if q == nil {
				q = &fold{}
				patterns[r.QueryPattern] = q
			}
			q.calls++
			q.saved += r.TokensSaved
		}
	}

	for _, tool := range sortedKeys(tools) {
		t := tools[tool]
		agg.Tools = append(agg.Tools, ToolUsage{
			Tool:        tool,
			Calls:       t.calls,
			TokensSaved: t.saved,
			AvgMS:       float64(t.ms) / float64(max64(t.calls, 1)),
		})
	}
	for _, bucket := range sortedIntKeys(days) {
		d := days[bucket]
		agg.Days = append(agg.Days, DayUsage{
			Day:         dayLabelFromDay(bucket),
			Calls:       d.calls,
			TokensSaved: d.saved,
		})
	}
	for _, project := range sortedKeys(projects) {
		p := projects[project]
		agg.Projects = append(agg.Projects, ProjectUsage{
			Project:     project,
			Calls:       p.calls,
			TokensSaved: p.saved,
		})
	}
	for _, pattern := range sortedKeys(patterns) {
		q := patterns[pattern]
		agg.Patterns = append(agg.Patterns, PatternUsage{
			Pattern:     pattern,
			Calls:       q.calls,
			TokensSaved: q.saved,
		})
	}
	return agg
}

// dayLabel renders an epoch-second stamp's UTC day as YYYY-MM-DD. Rust bucketed
// with div_euclid(timestamp, 86400) — integer division, so the day boundary is
// UTC regardless of session timezone — and labelled via civil_from_days; the
// Go port floors the same way, so pre-1970 stamps bucket identically.
func dayLabel(ts int64) string {
	return dayLabelFromDay(floorDiv(ts, SecondsPerDay))
}

// dayLabelFromDay renders an epoch-day bucket as YYYY-MM-DD (UTC).
func dayLabelFromDay(epochDay int64) string {
	return time.Unix(epochDay*SecondsPerDay, 0).UTC().Format("2006-01-02")
}

// floorDiv is floor division (Rust's div_euclid); Go's / truncates toward zero.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// sortedKeys returns a string-keyed map's keys ascending.
func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedIntKeys returns an int64-keyed map's keys ascending.
func sortedIntKeys[T any](m map[int64]T) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
