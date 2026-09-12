// The H10 / FR-PLG-8 usage dashboard over context_metrics: grouped totals per
// tool, per UTC day, per project and per query pattern (Rust src/dashboard/mod.rs
// + main.rs run_dashboard). Text is the aligned-table rendering with `▇` bars,
// JSON is the structured payload the web dashboard consumes.
//
// Difference from Rust: the Rust verb pinned PostgreSQL (its bucket queries
// existed only on the PG backend); here the buckets come from store.Backend, so
// sqlite — the default engine — serves the dashboard too.
package metrics

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

const (
	// topTools is the bucket cap for tools and patterns (Rust dashboard::TOP_N).
	topTools = 10
	// topProjects is the project-row cap (FR-PLG-8 says top 5).
	topProjects = 5
	// barWidth is the `▇` chart width in the text rendering.
	barWidth = 24
)

// DashboardOptions are the `leankg dashboard` flags (Rust run_dashboard).
type DashboardOptions struct {
	// Since is a "<n>h" | "<n>d" | "<n>w" window; "" = all time.
	Since string
	// Format is "text" (default when empty) or "json".
	Format string
}

// Totals is the ledger-wide totals for the window. Every row counts here
// (negative savings included), unlike the `leankg metrics` report.
type Totals struct {
	Calls             int64   `json:"calls"`
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	TokensSaved       int64   `json:"tokens_saved"`
	SavingsPercentSum float64 `json:"savings_percent_sum"`
	AvgSavingsPercent float64 `json:"avg_savings_percent"`
	SuccessRate       float64 `json:"success_rate"`
}

// DashboardData is the full dashboard payload, in Rust's serialized shape.
type DashboardData struct {
	Totals    Totals               `json:"totals"`
	ByTool    []store.ToolUsage    `json:"by_tool"`
	ByDay     []store.DayUsage     `json:"by_day"`
	ByProject []store.ProjectUsage `json:"by_project"`
	Patterns  []store.PatternUsage `json:"patterns"`
}

// ParseWindow parses a dashboard time window ("24h", "7d", "30d", "2w") into
// seconds (Rust dashboard::parse_since). Unlike `leankg metrics --since`, a bare
// number is NOT a valid window and neither is a zero/negative count.
func ParseWindow(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	var unit int64
	var num string
	switch {
	case strings.HasSuffix(raw, "h"):
		unit, num = 3_600, strings.TrimSuffix(raw, "h")
	case strings.HasSuffix(raw, "d"):
		unit, num = store.SecondsPerDay, strings.TrimSuffix(raw, "d")
	case strings.HasSuffix(raw, "w"):
		unit, num = 7*store.SecondsPerDay, strings.TrimSuffix(raw, "w")
	default:
		return 0, false
	}
	n, err := parsePositiveInt(num)
	if err != nil {
		return 0, false
	}
	return n * unit, true
}

// Dashboard renders the usage buckets for the window: text tables by default,
// pretty JSON with Format "json". An invalid window or format is an error
// (Rust run_dashboard exits non-zero for both).
func Dashboard(b store.Backend, opts DashboardOptions) (string, error) {
	var cutoff int64
	if opts.Since != "" {
		window, ok := ParseWindow(opts.Since)
		if !ok {
			return "", fmt.Errorf("invalid --since window (expected e.g. 24h, 7d, 30d, 2w)")
		}
		cutoff = time.Now().Unix() - window
	}
	agg, err := b.UsageAggregates(cutoff)
	if err != nil {
		return "", err
	}
	data := buildDashboard(agg)

	switch opts.Format {
	case "", "text":
		return renderDashboardText(data), nil
	case "json":
		buf, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return "", fmt.Errorf("dashboard: encode buckets: %w", err)
		}
		return string(buf) + "\n", nil
	default:
		return "", fmt.Errorf("unknown --format value %q; pass --format text (default) or --format json", opts.Format)
	}
}

// buildDashboard turns the raw buckets into the rendered shape: totals with
// averages, tools and projects ordered by tokens saved (name ascending on a
// tie) and capped, days chronological, patterns by calls. Rust's
// dashboard::build_dashboard, including the caps.
func buildDashboard(agg store.UsageAggregates) DashboardData {
	totals := Totals{
		Calls:             agg.Calls,
		InputTokens:       agg.InputTokens,
		OutputTokens:      agg.OutputTokens,
		TokensSaved:       agg.TokensSaved,
		SavingsPercentSum: agg.SavingsPercentSum,
	}
	if agg.Calls > 0 {
		totals.AvgSavingsPercent = agg.SavingsPercentSum / float64(agg.Calls)
		totals.SuccessRate = float64(agg.SuccessfulCalls) / float64(agg.Calls)
	}

	tools := nonNil(agg.Tools)
	sortToolsBySaved(tools)
	tools = firstN(tools, topTools)

	days := nonNil(agg.Days)
	sortDaysAscending(days)

	projects := nonNil(agg.Projects)
	sortProjectsBySaved(projects)
	projects = firstN(projects, topProjects)

	patterns := nonNil(agg.Patterns)
	sortPatternsByCalls(patterns)
	patterns = firstN(patterns, topTools)

	return DashboardData{Totals: totals, ByTool: tools, ByDay: days, ByProject: projects, Patterns: patterns}
}

// renderDashboardText is Rust dashboard::render_text: aligned tables plus a
// per-day `▇` bar chart, and an explicit empty-ledger line. Query patterns are
// JSON-only in Rust too, so they stay out of the text rendering.
func renderDashboardText(data DashboardData) string {
	var out strings.Builder
	out.WriteString("LeanKG usage dashboard — context_metrics\n")
	if data.Totals.Calls == 0 {
		out.WriteString("No metrics yet — the context_metrics ledger is empty for this window.\n")
		return out.String()
	}
	t := data.Totals
	fmt.Fprintf(&out, "\nTotal calls         : %s\nInput tokens        : %s\nOutput tokens       : %s\n"+
		"Tokens saved        : %s\nAvg savings percent : %.1f%%\nSuccess rate        : %.1f%%\n",
		thousands(t.Calls), thousands(t.InputTokens), thousands(t.OutputTokens),
		thousands(t.TokensSaved), t.AvgSavingsPercent, t.SuccessRate*100)

	out.WriteString("\nBy tool (sorted by tokens saved)\n")
	fmt.Fprintf(&out, "%-22s %7s %12s %10s\n", "TOOL", "CALLS", "SAVED", "AVG MS")
	for _, tool := range data.ByTool {
		fmt.Fprintf(&out, "%-22s %7s %12s %10.1f\n",
			tool.Tool, thousands(tool.Calls), thousands(tool.TokensSaved), tool.AvgMS)
	}

	out.WriteString("\nBy day (UTC)\n")
	maxSaved := int64(0)
	for _, day := range data.ByDay {
		if day.TokensSaved > maxSaved {
			maxSaved = day.TokensSaved
		}
	}
	fmt.Fprintf(&out, "%-12s %7s %12s  %s\n", "DAY", "CALLS", "SAVED", strings.Repeat("▇", barWidth))
	for _, day := range data.ByDay {
		fmt.Fprintf(&out, "%-12s %7s %12s  %s\n",
			day.Day, thousands(day.Calls), thousands(day.TokensSaved), bar(day.TokensSaved, maxSaved, barWidth))
	}

	out.WriteString("\nBy project (top 5 by tokens saved)\n")
	fmt.Fprintf(&out, "%-40s %7s %12s\n", "PROJECT", "CALLS", "SAVED")
	for _, p := range data.ByProject {
		fmt.Fprintf(&out, "%-40s %7s %12s\n", p.Project, thousands(p.Calls), thousands(p.TokensSaved))
	}
	return out.String()
}

// bar renders a normalized ASCII bar: width cells of `▇` proportional to
// value/max. A non-positive value renders empty, max <= 0 renders one cell, and
// a positive value always gets at least one cell (Rust dashboard::bar).
func bar(value, max int64, width int) string {
	if width == 0 || value <= 0 {
		return ""
	}
	if max <= 0 {
		return "▇"
	}
	if value >= max {
		return strings.Repeat("▇", width)
	}
	cells := int(value * int64(width) / max)
	if cells < 1 {
		cells = 1
	}
	if cells > width {
		cells = width
	}
	return strings.Repeat("▇", cells)
}

// thousands renders an integer with a thin thousands separator, i.e. a plain
// space every three digits from the right (Rust dashboard::thousands; the sign
// of a negative count takes part in the grouping there too, so it does here).
func thousands(n int64) string {
	digits := fmt.Sprintf("%d", n)
	var out strings.Builder
	out.Grow(len(digits) + len(digits)/3)
	for i := 0; i < len(digits); i++ {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out.WriteByte(' ')
		}
		out.WriteByte(digits[i])
	}
	return out.String()
}

// nonNil normalizes a nil slice so an empty bucket serializes as [] and not
// null (Rust's Vec always serializes as an array).
func nonNil[T any](rows []T) []T {
	if rows == nil {
		return []T{}
	}
	return rows
}

// parsePositiveInt parses a strictly positive int64 quantity (Rust's
// `parse::<i64>().ok().filter(|v| *v > 0)`, so "0d" and "-1d" are rejected).
func parsePositiveInt(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid window quantity %q", s)
	}
	return n, nil
}
