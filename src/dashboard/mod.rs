//! H10 — `leankg dashboard`: engineering-visibility view over the
//! `context_metrics` ledger (FR-PLG-8 / PLG-8).
//!
//! Metrics: totals (calls, tokens, savings), per-tool usage, per-day trend,
//! top projects, and query patterns. Aggregations run as SINGLE grouped
//! queries through the backend run path (`GROUP BY tool_name`,
//! `(timestamp)/86400`, `project_path`) — never row-by-row in SQL; the pure
//! [`aggregate_rows`] reference implementation exists for tests and as the
//! ground truth the SQL path is cross-checked against.

use serde::Serialize;
use std::collections::BTreeMap;

/// Seconds per day — context_metrics timestamps are epoch seconds.
pub const SECS_PER_DAY: i64 = 86_400;

/// One raw metric row: the typed projection of `context_metrics` columns
/// the dashboard needs.
#[derive(Debug, Clone, PartialEq)]
pub struct MetricRow {
    pub tool_name: String,
    pub timestamp: i64,
    pub project_path: String,
    pub input_tokens: i64,
    pub output_tokens: i64,
    pub execution_time_ms: i64,
    pub tokens_saved: i64,
    pub savings_percent: f64,
    pub success: bool,
    pub query_pattern: Option<String>,
}

impl MetricRow {
    /// Convenience constructor for fixtures/tests.
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        tool_name: &str,
        timestamp: i64,
        project_path: &str,
        input_tokens: i64,
        output_tokens: i64,
        execution_time_ms: i64,
        tokens_saved: i64,
        savings_percent: f64,
        success: bool,
        query_pattern: Option<&str>,
    ) -> Self {
        Self {
            tool_name: tool_name.to_string(),
            timestamp,
            project_path: project_path.to_string(),
            input_tokens,
            output_tokens,
            execution_time_ms,
            tokens_saved,
            savings_percent,
            success,
            query_pattern: query_pattern.map(str::to_string),
        }
    }
}

/// Ledger-wide totals for the window.
#[derive(Debug, Clone, Serialize, Default, PartialEq)]
pub struct Totals {
    pub calls: u64,
    pub input_tokens: i64,
    pub output_tokens: i64,
    pub tokens_saved: i64,
    pub savings_percent_sum: f64,
    pub avg_savings_percent: f64,
    /// Fraction of calls with `success = true`, in `[0, 1]`.
    pub success_rate: f64,
}

/// Per-tool usage row (rendered sorted by `tokens_saved` desc).
#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct ToolUsage {
    pub tool: String,
    pub calls: u64,
    pub tokens_saved: i64,
    pub avg_ms: f64,
}

/// Per-day usage row (UTC day bucket).
#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct DayUsage {
    /// UTC date, `YYYY-MM-DD`.
    pub day: String,
    pub calls: u64,
    pub tokens_saved: i64,
}

/// Per-project usage row.
#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct ProjectUsage {
    pub project: String,
    pub calls: u64,
    pub tokens_saved: i64,
}

/// Query-pattern usage row (from `query_pattern`; empty when unset).
#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct PatternUsage {
    pub pattern: String,
    pub calls: u64,
    pub tokens_saved: i64,
}

/// Full dashboard payload. Empty ledger → zeroed totals + empty buckets;
/// text rendering shows the "no metrics yet" state.
#[derive(Debug, Clone, Serialize, Default, PartialEq)]
pub struct DashboardData {
    pub totals: Totals,
    pub by_tool: Vec<ToolUsage>,
    pub by_day: Vec<DayUsage>,
    pub by_project: Vec<ProjectUsage>,
    pub patterns: Vec<PatternUsage>,
}

/// Cap on listed rows per bucket section.
pub const TOP_N: usize = 10;

/// Project rows shown (`top 5` per FR-PLG-8).
pub const TOP_PROJECTS: usize = 5;

/// Assemble the dashboard from pre-aggregated bucket rows: sorts tools and
/// projects by `tokens_saved` desc (projects capped at 5 per FR-PLG-8),
/// days ascending by date, patterns by calls desc (capped).
pub fn build_dashboard(
    totals: Totals,
    mut tools: Vec<ToolUsage>,
    mut days: Vec<DayUsage>,
    mut projects: Vec<ProjectUsage>,
    mut patterns: Vec<PatternUsage>,
) -> DashboardData {
    tools.sort_by(|a, b| {
        b.tokens_saved
            .cmp(&a.tokens_saved)
            .then_with(|| a.tool.cmp(&b.tool))
    });
    tools.truncate(TOP_N);
    days.sort_by(|a, b| a.day.cmp(&b.day));
    projects.sort_by(|a, b| {
        b.tokens_saved
            .cmp(&a.tokens_saved)
            .then_with(|| a.project.cmp(&b.project))
    });
    projects.truncate(TOP_PROJECTS);
    patterns.sort_by(|a, b| {
        b.calls
            .cmp(&a.calls)
            .then_with(|| a.pattern.cmp(&b.pattern))
    });
    patterns.truncate(TOP_N);
    DashboardData {
        totals,
        by_tool: tools,
        by_day: days,
        by_project: projects,
        patterns,
    }
}

/// Reference aggregation over raw rows — the ground truth the grouped-SQL
/// path is integration-tested against. Pure; no DB involved.
pub fn aggregate_rows(rows: &[MetricRow]) -> DashboardData {
    let mut totals = Totals::default();
    let mut tools: BTreeMap<String, (u64, i64, i64)> = BTreeMap::new();
    let mut days: BTreeMap<i64, (u64, i64)> = BTreeMap::new();
    let mut projects: BTreeMap<String, (u64, i64)> = BTreeMap::new();
    let mut patterns: BTreeMap<String, (u64, i64)> = BTreeMap::new();

    for r in rows {
        totals.calls += 1;
        totals.input_tokens += r.input_tokens;
        totals.output_tokens += r.output_tokens;
        totals.tokens_saved += r.tokens_saved;
        totals.savings_percent_sum += r.savings_percent;
        if r.success {
            totals.success_rate += 1.0;
        }
        let tool = tools.entry(r.tool_name.clone()).or_default();
        *tool = (
            tool.0 + 1,
            tool.1 + r.tokens_saved,
            tool.2 + r.execution_time_ms,
        );
        let day = days
            .entry(r.timestamp.div_euclid(SECS_PER_DAY))
            .or_default();
        *day = (day.0 + 1, day.1 + r.tokens_saved);
        let proj = projects.entry(r.project_path.clone()).or_default();
        *proj = (proj.0 + 1, proj.1 + r.tokens_saved);
        if let Some(p) = &r.query_pattern {
            let pat = patterns.entry(p.clone()).or_default();
            *pat = (pat.0 + 1, pat.1 + r.tokens_saved);
        }
    }

    if totals.calls > 0 {
        let n = totals.calls as f64;
        totals.avg_savings_percent = totals.savings_percent_sum / n;
        totals.success_rate /= n;
    }

    let tool_rows = tools
        .into_iter()
        .map(|(tool, (calls, saved, ms_sum))| ToolUsage {
            avg_ms: ms_sum as f64 / calls as f64,
            tool,
            calls,
            tokens_saved: saved,
        })
        .collect();
    let day_rows = days
        .into_iter()
        .map(|(bucket, (calls, saved))| DayUsage {
            day: day_label(bucket),
            calls,
            tokens_saved: saved,
        })
        .collect();
    let project_rows = projects
        .into_iter()
        .map(|(project, (calls, saved))| ProjectUsage {
            project,
            calls,
            tokens_saved: saved,
        })
        .collect();
    let pattern_rows = patterns
        .into_iter()
        .map(|(pattern, (calls, saved))| PatternUsage {
            pattern,
            calls,
            tokens_saved: saved,
        })
        .collect();

    build_dashboard(totals, tool_rows, day_rows, project_rows, pattern_rows)
}

/// Epoch **days** since 1970-01-01 → `YYYY-MM-DD` (UTC).
pub fn day_label(epoch_day: i64) -> String {
    civil_from_days(epoch_day)
}

/// Howard Hinnant's civil_from_days algorithm (public domain).
fn civil_from_days(z: i64) -> String {
    let z = z + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let y = yoe + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = if m <= 2 { y + 1 } else { y };
    format!("{y:04}-{m:02}-{d:02}")
}

/// Render a normalized ASCII bar: `width` cells of `▇` proportional to
/// `value/max`. `max <= 0` or non-finite → empty bar. At least one cell
/// when `value > 0`.
pub fn bar(value: i64, max: i64, width: usize) -> String {
    if width == 0 || value <= 0 {
        return String::new();
    }
    if max <= 0 {
        return "▇".to_string();
    }
    let cells = ((value as i128 * width as i128) / max as i128) as usize;
    "▇".repeat(cells.clamp(1, width))
}

/// Render a positive integer with thin thousands separators (1 234 567).
fn thousands(n: i64) -> String {
    let digits = n.to_string();
    let mut out = String::with_capacity(digits.len() + digits.len() / 3);
    for (i, c) in digits.chars().enumerate() {
        if i > 0 && (digits.len() - i).is_multiple_of(3) {
            out.push(' ');
        }
        out.push(c);
    }
    out
}

/// Human-readable text rendering: aligned tables + `▇` bar charts.
/// Empty ledger renders "no metrics yet".
pub fn render_text(data: &DashboardData) -> String {
    let mut out = String::from("LeanKG usage dashboard — context_metrics\n");
    if data.totals.calls == 0 {
        out.push_str("No metrics yet — the context_metrics ledger is empty for this window.\n");
        return out;
    }
    let t = &data.totals;
    out.push_str(&format!(
        "\nTotal calls         : {}\n\
         Input tokens        : {}\n\
         Output tokens       : {}\n\
         Tokens saved        : {}\n\
         Avg savings percent : {:.1}%\n\
         Success rate        : {:.1}%\n",
        thousands(t.calls as i64),
        thousands(t.input_tokens),
        thousands(t.output_tokens),
        thousands(t.tokens_saved),
        t.avg_savings_percent,
        t.success_rate * 100.0,
    ));

    out.push_str("\nBy tool (sorted by tokens saved)\n");
    out.push_str(&format!(
        "{:<22} {:>7} {:>12} {:>10}\n",
        "TOOL", "CALLS", "SAVED", "AVG MS"
    ));
    for tool in &data.by_tool {
        out.push_str(&format!(
            "{:<22} {:>7} {:>12} {:>10.1}\n",
            tool.tool,
            thousands(tool.calls as i64),
            thousands(tool.tokens_saved),
            tool.avg_ms,
        ));
    }

    out.push_str("\nBy day (UTC)\n");
    let max_saved = data
        .by_day
        .iter()
        .map(|d| d.tokens_saved)
        .max()
        .unwrap_or(0);
    const BAR_WIDTH: usize = 24;
    out.push_str(&format!(
        "{:<12} {:>7} {:>12}  {}\n",
        "DAY",
        "CALLS",
        "SAVED",
        "▇".repeat(BAR_WIDTH)
    ));
    for day in &data.by_day {
        out.push_str(&format!(
            "{:<12} {:>7} {:>12}  {}\n",
            day.day,
            thousands(day.calls as i64),
            thousands(day.tokens_saved),
            bar(day.tokens_saved, max_saved, BAR_WIDTH),
        ));
    }

    out.push_str("\nBy project (top 5 by tokens saved)\n");
    out.push_str(&format!(
        "{:<40} {:>7} {:>12}\n",
        "PROJECT", "CALLS", "SAVED"
    ));
    for p in &data.by_project {
        out.push_str(&format!(
            "{:<40} {:>7} {:>12}\n",
            p.project,
            thousands(p.calls as i64),
            thousands(p.tokens_saved),
        ));
    }
    out
}

/// Structured JSON rendering (`{totals, by_tool, by_day, by_project, patterns}`).
pub fn render_json(data: &DashboardData) -> Result<String, serde_json::Error> {
    serde_json::to_string_pretty(data)
}

/// Bucket rows handed back by the backend seam
/// ([`crate::db::backend::DbBackend::query_usage_aggregates`]). All
/// aggregation happens in SINGLE grouped queries backend-side; these are
/// already-bucketed rows, never the raw ledger.
#[derive(Debug, Default)]
pub struct UsageAggregates {
    pub calls: u64,
    pub input_tokens: i64,
    pub output_tokens: i64,
    pub tokens_saved: i64,
    pub savings_percent_sum: f64,
    pub successful_calls: u64,
    pub tools: Vec<ToolUsage>,
    pub days: Vec<DayUsage>,
    pub projects: Vec<ProjectUsage>,
    /// `(pattern, calls, tokens_saved)` triples; rendered via [`PatternUsage`].
    pub patterns: Vec<(String, u64, i64)>,
}

/// Parse a `--since` window (`24h`, `7d`, `30d`, `2w`) into seconds.
/// Returns `None` for anything else.
pub fn parse_since(raw: &str) -> Option<i64> {
    let raw = raw.trim();
    let (n, secs) = match raw.strip_suffix('h') {
        Some(num) => (num, 3_600),
        None => match raw.strip_suffix('d') {
            Some(num) => (num, SECS_PER_DAY),
            None => (raw.strip_suffix('w')?, 7 * SECS_PER_DAY),
        },
    };
    n.parse::<i64>().ok().filter(|v| *v > 0).map(|v| v * secs)
}

/// Collect the dashboard over the ledger via SINGLE grouped queries through
/// the backend run path. `since_cutoff` filters `timestamp >= cutoff`
/// (epoch seconds); `None` = all time. `is_deleted = false` always.
pub fn collect(
    db: &crate::db::backend::SharedDb,
    since_cutoff: Option<i64>,
) -> Result<DashboardData, Box<dyn std::error::Error>> {
    let agg = db.query_usage_aggregates(since_cutoff)?;
    let mut totals = Totals {
        calls: agg.calls,
        input_tokens: agg.input_tokens,
        output_tokens: agg.output_tokens,
        tokens_saved: agg.tokens_saved,
        savings_percent_sum: agg.savings_percent_sum,
        avg_savings_percent: 0.0,
        success_rate: 0.0,
    };
    if totals.calls > 0 {
        let n = totals.calls as f64;
        totals.avg_savings_percent = totals.savings_percent_sum / n;
        totals.success_rate = agg.successful_calls as f64 / n;
    }
    let patterns = agg
        .patterns
        .into_iter()
        .map(|(pattern, calls, saved)| PatternUsage {
            pattern,
            calls,
            tokens_saved: saved,
        })
        .collect();
    Ok(build_dashboard(
        totals,
        agg.tools,
        agg.days,
        agg.projects,
        patterns,
    ))
}

// ---------------------------------------------------------------------------
// Tests (TDD RED — written before the real bodies)
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    /// Day 0 of the fixture window: a fixed UTC morning so day-bucketing is
    /// deterministic regardless of when tests run (2027-01-15T08:00Z).
    const DAY0: i64 = 1_800_000_000;

    fn row(tool: &str, ts: i64, proj: &str, saved: i64, pct: f64, ok: bool) -> MetricRow {
        MetricRow::new(
            tool,
            ts,
            proj,
            1_000,
            500,
            100,
            saved,
            pct,
            ok,
            Some("search_code(query=*)"),
        )
    }

    #[test]
    fn totals_math_is_correct() {
        let rows = vec![
            row("a", DAY0, "/p1", 100, 50.0, true),
            row("b", DAY0, "/p1", 200, 25.0, false),
            row("a", DAY0, "/p2", 300, 75.0, true),
        ];
        let d = aggregate_rows(&rows);
        assert_eq!(d.totals.calls, 3);
        assert_eq!(d.totals.input_tokens, 3_000);
        assert_eq!(d.totals.output_tokens, 1_500);
        assert_eq!(d.totals.tokens_saved, 600);
        assert!((d.totals.savings_percent_sum - 150.0).abs() < 1e-9);
        assert!((d.totals.avg_savings_percent - 50.0).abs() < 1e-9);
        assert!((d.totals.success_rate - 2.0 / 3.0).abs() < 1e-9);
    }

    #[test]
    fn empty_ledger_zeroes_totals_and_renders_no_metrics_yet() {
        let d = aggregate_rows(&[]);
        assert_eq!(d.totals.calls, 0);
        assert_eq!(d.totals.tokens_saved, 0);
        assert_eq!(d.totals.success_rate, 0.0);
        assert!(d.by_tool.is_empty());
        let txt = render_text(&d);
        assert!(
            txt.to_lowercase().contains("no metrics yet"),
            "empty state missing: {txt}"
        );
    }

    #[test]
    fn by_tool_sorted_by_tokens_saved_desc_with_avg_ms() {
        let mut small = row("small", DAY0, "/p", 10, 1.0, true);
        let mut mid = row("mid", DAY0, "/p", 100, 1.0, false);
        let mut big = row("big", DAY0, "/p", 900, 1.0, true);
        small.execution_time_ms = 100;
        mid.execution_time_ms = 200;
        big.execution_time_ms = 300;
        let d = aggregate_rows(&[small, mid, big]);
        let tools: Vec<&str> = d.by_tool.iter().map(|t| t.tool.as_str()).collect();
        assert_eq!(tools, vec!["big", "mid", "small"]);
        assert_eq!(d.by_tool[2].avg_ms, 100.0);
        assert_eq!(d.by_tool[0].calls, 1);
    }

    #[test]
    fn by_day_buckets_on_utc_date_boundaries() {
        // Two rows on DAY0's day, one on the next UTC day (+86400s).
        let rows = vec![
            row("a", DAY0, "/p", 10, 1.0, true),
            row("b", DAY0 + 3_600, "/p", 20, 1.0, true),
            row("c", DAY0 + SECS_PER_DAY, "/p", 40, 2.0, true),
        ];
        let d = aggregate_rows(&rows);
        assert_eq!(d.by_day.len(), 2);
        assert_eq!(d.by_day[0].calls, 2);
        assert_eq!(d.by_day[0].tokens_saved, 30);
        assert_eq!(d.by_day[1].calls, 1);
        assert_eq!(d.by_day[1].tokens_saved, 40);
    }

    #[test]
    fn day_label_formats_iso_utc() {
        assert_eq!(day_label(0), "1970-01-01");
        assert_eq!(day_label(19_000), "2022-01-08");
        // DAY0's own bucket: 2027-01-15.
        assert_eq!(day_label(DAY0.div_euclid(SECS_PER_DAY)), "2027-01-15");
    }

    #[test]
    fn by_project_top_five_by_saved() {
        let rows: Vec<MetricRow> = (0..7)
            .map(|i| row("t", DAY0, &format!("/proj-{i}"), (i + 1) * 100, 1.0, true))
            .collect();
        let d = aggregate_rows(&rows);
        assert_eq!(d.by_project.len(), 5, "top-5 cap");
        assert_eq!(d.by_project[0].project, "/proj-6");
        assert_eq!(d.by_project[4].project, "/proj-2");
        assert_eq!(d.by_project[0].tokens_saved, 700);
    }

    #[test]
    fn patterns_aggregate_only_populated_values() {
        let mut r1 = row("a", DAY0, "/p", 10, 1.0, true);
        r1.query_pattern = None;
        let rows = vec![
            r1,
            row("b", DAY0, "/p", 20, 1.0, true),
            row("c", DAY0, "/p", 30, 1.0, true),
        ];
        let d = aggregate_rows(&rows);
        assert_eq!(d.patterns.len(), 1);
        assert_eq!(d.patterns[0].pattern, "search_code(query=*)");
        assert_eq!(d.patterns[0].calls, 2);
        assert_eq!(d.patterns[0].tokens_saved, 50);
    }

    #[test]
    fn bars_scale_to_max() {
        assert_eq!(bar(10, 10, 4), "▇▇▇▇");
        assert_eq!(bar(0, 10, 4), "");
        assert_eq!(bar(5, 10, 4), "▇▇");
        // Positive value under a zero/absent max still shows one cell.
        assert_eq!(bar(3, 0, 4), "▇");
    }

    #[test]
    fn text_render_contains_tables_and_bars() {
        let rows = vec![
            row("alpha", DAY0, "/p1", 500, 90.0, true),
            row("beta", DAY0, "/p1", 100, 10.0, false),
            row("gamma", DAY0 + SECS_PER_DAY, "/p2", 250, 45.0, true),
        ];
        let d = aggregate_rows(&rows);
        let txt = render_text(&d);
        assert!(txt.contains("alpha"), "{txt}");
        assert!(txt.contains("beta"), "{txt}");
        assert!(txt.contains('▇'), "bar chart missing: {txt}");
        assert!(txt.contains("2027-"), "day table missing ISO dates: {txt}");
        assert!(txt.contains("/p1"), "{txt}");
    }

    #[test]
    fn json_serializes_full_structure() {
        let d = aggregate_rows(&[
            row("alpha", DAY0, "/p1", 500, 90.0, true),
            row("beta", DAY0 + SECS_PER_DAY, "/p2", 100, 10.0, false),
        ]);
        let json = render_json(&d).unwrap();
        let v: serde_json::Value = serde_json::from_str(&json).unwrap();
        assert!(v.get("totals").unwrap().get("calls").is_some());
        assert!(v.get("by_tool").unwrap().is_array());
        assert!(v.get("by_day").unwrap().is_array());
        assert!(v.get("by_project").unwrap().is_array());
        assert_eq!(
            v["totals"]["tokens_saved"].as_i64(),
            Some(600),
            "json: {json}"
        );
        assert_eq!(v["totals"]["success_rate"].as_f64(), Some(0.5));
    }

    #[test]
    fn build_dashboard_sorts_and_caps_like_reference() {
        let rows: Vec<MetricRow> = (0..7)
            .map(|i| row("t", DAY0, &format!("/proj-{i}"), (i + 1) * 100, 1.0, true))
            .collect();
        let reference = aggregate_rows(&rows);
        let built = build_dashboard(
            reference.totals.clone(),
            reference.by_tool.clone(),
            reference.by_day.clone(),
            // Feed unsorted to prove sorting happens in build_dashboard.
            {
                let mut p = reference.by_project.clone();
                p.reverse();
                p
            },
            reference.patterns.clone(),
        );
        assert_eq!(built.by_project, reference.by_project);
        assert_eq!(built.by_project.len(), 5);
    }

    #[test]
    fn parse_since_windows() {
        assert_eq!(parse_since("24h"), Some(86_400));
        assert_eq!(parse_since("7d"), Some(7 * 86_400));
        assert_eq!(parse_since("30d"), Some(30 * 86_400));
        assert_eq!(parse_since("2w"), Some(14 * 86_400));
        assert_eq!(parse_since("0d"), None, "non-positive rejected");
        assert_eq!(parse_since("-3h"), None);
        assert_eq!(parse_since("7x"), None);
        assert_eq!(parse_since(""), None);
        assert_eq!(parse_since("d"), None);
    }
}
