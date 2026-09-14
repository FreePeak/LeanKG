// Package metrics renders the persisted usage ledger (context_metrics) for the
// `leankg metrics` and `leankg dashboard` verbs.
//
// Ported from the Rust engine:
//   - metrics.go  — main.rs show_metrics + seed_test_metrics over
//     db::get_metrics_summary, i.e. the context-savings report.
//   - dashboard.go — dashboard/mod.rs (H10 / FR-PLG-8): the usage buckets the
//     `leankg dashboard` verb renders as text tables or JSON.
//
// The package owns flag semantics and rendering only; the folds live in
// internal/store so both storage backends report identical numbers.
package metrics

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// DefaultRetentionDays is the window both verbs fall back to (Rust's
// unwrap_or(30) in show_metrics and dashboard::collect's None = all time).
const DefaultRetentionDays = 30

// seedProjectPath is the project_path the Rust seed wrote.
const seedProjectPath = "/test"

// Options are the `leankg metrics` flags (Rust cli::CLICommand::Metrics).
//
// Since, Tool, Session and Retention carry Rust's Option semantics with the Go
// zero value as "unset": Since "" = fall back to Retention (then to
// DefaultRetentionDays), Tool "" = every tool, Retention 0 = unset. A
// `--retention 0` (purge everything) is therefore not expressible from the CLI,
// which is the one flag value the port cannot carry; Rust's own default was 30.
type Options struct {
	Since     string // window, "<n>d" or a bare "<n>" days
	Tool      string // tool filter ("" = all)
	JSON      bool
	Session   bool
	Reset     bool
	Retention int // days; 0 = unset
	Cleanup   bool
	Seed      bool
}

// Show renders the `leankg metrics` output (Rust show_metrics): reset, cleanup,
// seed or the summary, in that precedence. `--seed` wins outright — Rust ran it
// in the CLI arm before show_metrics, so `--seed --reset` seeds and returns.
//
// The returned string is exactly what the verb prints, trailing newline
// included, which keeps the verb's stdout byte-identical to Rust's.
func Show(b store.Backend, opts Options) (string, error) {
	if opts.Seed {
		return seed(b)
	}
	// Rust checked reset before cleanup, so `--reset --cleanup` resets.
	if opts.Reset {
		n, err := b.ResetMetrics()
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Reset %d metric record(s).\n", n), nil
	}
	if opts.Cleanup {
		// Rust's cleanup path read --retention only; --since did NOT scope it.
		days := opts.Retention
		if days == 0 {
			days = DefaultRetentionDays
		}
		n, err := b.CleanupMetrics(days)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Cleaned up %d old metric record(s) (retention: %d days).\n", n, days), nil
	}

	days := DefaultRetentionDays
	switch {
	case opts.Since != "":
		days = parseSinceDays(opts.Since)
	case opts.Retention != 0:
		days = opts.Retention
	}
	sum, err := b.MetricsSummary(opts.Tool, days)
	if err != nil {
		return "", err
	}

	if opts.JSON {
		buf, err := json.MarshalIndent(sum, "", "  ")
		if err != nil {
			return "", fmt.Errorf("metrics: encode summary: %w", err)
		}
		return string(buf) + "\n", nil
	}

	var out strings.Builder
	out.WriteString("=== LeanKG Context Metrics ===\n\n")
	fmt.Fprintf(&out, "Total Savings: %d tokens across %d calls\n", sum.TotalTokensSaved, sum.TotalInvocations)
	fmt.Fprintf(&out, "Average Savings: %.1f%% (positive only)\n", sum.AverageSavingsPercent)
	fmt.Fprintf(&out, "Average Correctness: %.1f%%\n", sum.AverageCorrectnessPercent)
	fmt.Fprintf(&out, "Retention: %d days\n", sum.RetentionDays)

	if len(sum.ByTool) > 0 {
		out.WriteString("\nBy Tool:\n")
		for _, tm := range sum.ByTool {
			// Rust printed the per-tool savings with no decimal (%.0f) and the
			// correctness with one (%.1f).
			fmt.Fprintf(&out, "  %s: %d calls, %.0f%% save, %.1f%% correct\n",
				tm.ToolName, tm.Calls, tm.AvgSavingsPercent, tm.AvgCorrectnessPercent)
		}
	}
	if len(sum.ByDay) > 0 {
		out.WriteString("\nBy Day:\n")
		for _, dm := range sum.ByDay {
			// Two spaces after the date: Rust's format string.
			fmt.Fprintf(&out, "  %s:  %d calls, %.1f%% correct\n", dm.Date, dm.Calls, dm.Correctness)
		}
	}
	if opts.Session {
		// Ported verbatim: Rust's --session branch printed this placeholder,
		// and the Go engine has no session-scoped ledger read to substitute.
		out.WriteString("\nSession: Showing current session metrics not yet implemented\n")
	}
	return out.String(), nil
}

// parseSinceDays ports show_metrics' window parsing: "<n>d" or a bare "<n>"
// number of days, anything unparseable is DefaultRetentionDays (Rust's
// unwrap_or(30)). The number is parsed as i32 because Rust's flag is i32, so an
// out-of-range value falls back instead of silently becoming a huge window.
func parseSinceDays(raw string) int {
	num := raw
	if trimmed, ok := strings.CutSuffix(raw, "d"); ok {
		num = trimmed
	}
	days, err := strconv.ParseInt(num, 10, 32)
	if err != nil {
		return DefaultRetentionDays
	}
	return int(days)
}

// seed writes the five fixed rows of the Rust seed (main.rs seed_test_metrics)
// and returns its per-row output. Timestamps are relative to now so the rows
// land inside any sane retention window.
func seed(b store.Backend) (string, error) {
	now := time.Now().Unix()
	rows := []struct {
		id, tool                                    string
		age, in, out, elems, ms, base, lines, saved int64
		pct                                         float64
	}{
		{"seed1", "search_code", 100, 150, 45, 12, 25, 12000, 5000, 11955, 99.6},
		{"seed2", "get_context", 90, 200, 35, 8, 18, 8000, 3200, 7965, 99.6},
		{"seed3", "find_function", 80, 80, 28, 5, 12, 6000, 2400, 5972, 99.5},
		{"seed4", "search_code", 70, 120, 52, 15, 30, 14000, 5800, 13948, 99.6},
		{"seed5", "get_impact_radius", 60, 300, 180, 25, 45, 25000, 10000, 24820, 99.3},
	}

	var out strings.Builder
	for _, r := range rows {
		m := store.Metric{
			ToolName:             r.tool,
			Timestamp:            now - r.age,
			ProjectPath:          seedProjectPath,
			InputTokens:          r.in,
			OutputTokens:         r.out,
			OutputElements:       r.elems,
			ExecutionTimeMs:      r.ms,
			BaselineTokens:       r.base,
			BaselineLinesScanned: r.lines,
			TokensSaved:          r.saved,
			SavingsPercent:       r.pct,
			CorrectElements:      r.elems,
			TotalExpected:        r.elems + 2,
			F1Score:              0.85,
			QueryPattern:         "name",
			QueryFile:            "src/*.rs", // Rust's seed literal, kept for parity
			QueryDepth:           2,
			Success:              true,
		}
		if err := b.RecordMetric(m); err != nil {
			return "", err
		}
		fmt.Fprintf(&out, "Seeded metric: %s (%s)\n", r.id, r.tool)
	}
	fmt.Fprintf(&out, "Seeded %d test metrics\n", len(rows))
	return out.String(), nil
}

// sortToolsBySaved is the dashboard's tool ordering: tokens saved descending,
// tool name ascending as the tie-break.
func sortToolsBySaved(tools []store.ToolUsage) {
	sort.Slice(tools, func(i, j int) bool {
		if tools[i].TokensSaved != tools[j].TokensSaved {
			return tools[i].TokensSaved > tools[j].TokensSaved
		}
		return tools[i].Tool < tools[j].Tool
	})
}

// sortProjectsBySaved is the dashboard's project ordering (same rule as tools).
func sortProjectsBySaved(projects []store.ProjectUsage) {
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].TokensSaved != projects[j].TokensSaved {
			return projects[i].TokensSaved > projects[j].TokensSaved
		}
		return projects[i].Project < projects[j].Project
	})
}

// sortPatternsByCalls is the dashboard's pattern ordering: calls descending,
// pattern ascending as the tie-break.
func sortPatternsByCalls(patterns []store.PatternUsage) {
	sort.Slice(patterns, func(i, j int) bool {
		if patterns[i].Calls != patterns[j].Calls {
			return patterns[i].Calls > patterns[j].Calls
		}
		return patterns[i].Pattern < patterns[j].Pattern
	})
}

// sortDaysAscending keeps the day buckets chronological (the labels are
// YYYY-MM-DD, so lexical order is chronological order).
func sortDaysAscending(days []store.DayUsage) {
	sort.Slice(days, func(i, j int) bool { return days[i].Day < days[j].Day })
}

// firstN returns at most n rows of a bucket list (Rust's truncate).
func firstN[T any](rows []T, n int) []T {
	if len(rows) > n {
		return rows[:n]
	}
	return rows
}
