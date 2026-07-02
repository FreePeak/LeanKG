use crate::db;
use crate::graph;
use crate::ontology;
// Unified A/B test benchmark: LeanKG tools vs manual grep/find equivalents.
// Measures latency, token usage, and result counts. Saves comparison report.

use serde::Serialize;
use std::path::Path;
use std::process::Command;
use std::time::Instant;

#[derive(Debug, Clone, Serialize)]
struct AbQueryResult {
    tool: String,
    query: String,
    variant_a: VariantResult,
    variant_b: VariantResult,
    latency_ratio: f64,
    token_diff: i64,
    result_diff: i64,
    winner: String,
}

#[derive(Debug, Clone, Serialize)]
struct VariantResult {
    latency_ms: f64,
    output_bytes: usize,
    output_tokens: usize,
    result_count: usize,
    success: bool,
    error: Option<String>,
}

#[derive(Debug, Serialize)]
struct AbReport {
    name: String,
    project: String,
    timestamp: String,
    summary: AbSummary,
    results: Vec<AbQueryResult>,
}

#[derive(Debug, Serialize)]
struct AbSummary {
    total_queries: usize,
    a_total_ms: f64,
    b_total_ms: f64,
    a_avg_ms: f64,
    b_avg_ms: f64,
    avg_speedup: f64,
    a_total_tokens: usize,
    b_total_tokens: usize,
    a_total_results: usize,
    b_total_results: usize,
    a_wins: usize,
    b_wins: usize,
}

fn estimate_tokens(bytes: usize) -> usize {
    bytes / 4
}

fn run_shell(cmd: &str, project: &str) -> (String, String, f64) {
    let start = Instant::now();
    let output = Command::new("sh")
        .arg("-c")
        .arg(cmd)
        .current_dir(project)
        .output();
    let ms = start.elapsed().as_secs_f64() * 1000.0;
    match output {
        Ok(o) => (
            String::from_utf8_lossy(&o.stdout).to_string(),
            String::from_utf8_lossy(&o.stderr).to_string(),
            ms,
        ),
        Err(e) => (String::new(), e.to_string(), ms),
    }
}

pub fn run(project_path: &str) -> Result<(), Box<dyn std::error::Error>> {
    let db_path = Path::new(project_path).join(".leankg");
    let has_db = db_path.exists();
    if !has_db {
        eprintln!(
            "No .leankg at {}, running without DB tools",
            db_path.display()
        );
    }
    let db = if has_db {
        Some(db::schema::init_db(&db_path)?)
    } else {
        None
    };
    let graph_engine = db.as_ref().map(|d| graph::GraphEngine::new(d.clone()));
    let oq = db
        .as_ref()
        .map(|d| ontology::OntologyQueryEngine::new(d.clone()));

    let ts = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);

    println!("A/B Test: LeanKG vs Manual\n  Project: {}\n", project_path);

    let cases = &[
        (
            "search_code",
            "search_by_name(CodeElement)",
            "grep -r --include='*.rs' -l 'CodeElement' src/ | wc -l",
        ),
        (
            "search_code",
            "search_by_name(OntologyQueryEngine)",
            "grep -r --include='*.rs' -l 'OntologyQueryEngine' src/ | wc -l",
        ),
        (
            "search_code",
            "search_by_name(GraphEngine)",
            "grep -r --include='*.rs' -l 'GraphEngine' src/ | wc -l",
        ),
        (
            "find_function",
            "find_function(get_ontology_context)",
            "grep -rn --include='*.rs' 'fn get_ontology_context' src/ | wc -l",
        ),
        (
            "find_function",
            "find_function(concept_search)",
            "grep -rn --include='*.rs' 'fn concept_search' src/ | wc -l",
        ),
        (
            "find_function",
            "find_function(search_by_name_typed)",
            "grep -rn --include='*.rs' 'fn search_by_name_typed' src/ | wc -l",
        ),
        (
            "search_code",
            "search_by_name(benchmark)",
            "grep -r --include='*.rs' -l 'benchmark' src/ | wc -l",
        ),
        (
            "search_code",
            "search_by_name(tool_bench)",
            "grep -r --include='*.rs' 'tool_bench' src/ | wc -l",
        ),
        (
            "search_code",
            "search_by_name(ab_test)",
            "grep -r --include='*.rs' 'ab_test' src/ | wc -l",
        ),
        (
            "search_code",
            "search_by_name(OntologyCommand)",
            "grep -r --include='*.rs' -l 'OntologyCommand' src/ | wc -l",
        ),
    ];

    use serde_json::json;
    let mut results: Vec<AbQueryResult> = Vec::new();
    let (mut a_tot_ms, mut b_tot_ms, mut a_tot_tok, mut b_tot_tok, mut a_tot_res, mut b_tot_res) =
        (0.0_f64, 0.0_f64, 0usize, 0usize, 0usize, 0usize);
    let (mut a_wins, mut b_wins) = (0usize, 0usize);

    for (idx, (tool, description, shell_cmd)) in cases.iter().enumerate() {
        println!(
            "  [{}/{}] {} -- {}",
            idx + 1,
            cases.len(),
            tool,
            description
        );

        let (a_lat, a_out, a_count, a_ok) = if has_db {
            run_lean_kg(&graph_engine, tool, description)
        } else {
            (0.0, String::new(), 0, false)
        };
        let a_tok = estimate_tokens(a_out.len());
        println!(
            "    A (LeanKG):   {:>8.1} ms  {:>6} results  {:>6} tokens",
            a_lat, a_count, a_tok
        );

        let (b_out, b_err, b_lat) = run_shell(shell_cmd, project_path);
        let b_ok = b_err.is_empty();
        let b_count = b_out.trim().parse::<usize>().unwrap_or(0);
        let b_tok = estimate_tokens(b_out.len());
        println!(
            "    B (Manual):   {:>8.1} ms  {:>6} results  {:>6} tokens",
            b_lat, b_count, b_tok
        );

        let ratio = if a_lat > 0.0 { b_lat / a_lat } else { 0.0 };
        let token_diff = a_tok as i64 - b_tok as i64;
        let result_diff = a_count as i64 - b_count as i64;
        let winner = if a_lat < b_lat { "LeanKG" } else { "Manual" };
        let speedup = if ratio > 1.0 {
            ratio
        } else if ratio > 0.0 {
            1.0 / ratio
        } else {
            0.0
        };
        println!(
            "    => LeanKG {:.1}x {} | token diff: {} | result diff: {}",
            speedup,
            if a_lat < b_lat { "faster" } else { "slower" },
            token_diff,
            result_diff
        );

        a_tot_ms += a_lat;
        b_tot_ms += b_lat;
        a_tot_tok += a_tok;
        b_tot_tok += b_tok;
        a_tot_res += a_count;
        b_tot_res += b_count;
        if a_lat < b_lat {
            a_wins += 1;
        } else {
            b_wins += 1;
        }

        results.push(AbQueryResult {
            tool: tool.to_string(),
            query: description.to_string(),
            variant_a: VariantResult {
                latency_ms: a_lat,
                output_bytes: a_out.len(),
                output_tokens: a_tok,
                result_count: a_count,
                success: a_ok,
                error: None,
            },
            variant_b: VariantResult {
                latency_ms: b_lat,
                output_bytes: b_out.len(),
                output_tokens: b_tok,
                result_count: b_count,
                success: b_ok,
                error: if b_ok { None } else { Some(b_err) },
            },
            latency_ratio: ratio,
            token_diff,
            result_diff,
            winner: winner.to_string(),
        });
    }

    let n = results.len() as f64;
    let avg_speedup = if n > 0.0 {
        b_tot_ms / if a_tot_ms > 0.0 { a_tot_ms } else { 1.0 }
    } else {
        0.0
    };

    println!("\n ============================================");
    println!("  A/B Test Summary");
    println!(" ============================================");
    println!(
        "  {:<18} {:>10} {:>10} {:>10}",
        "Metric", "A: LeanKG", "B: Manual", "Delta"
    );
    println!(
        "  {:<18} {:>10.1} {:>10.1} {:>10.1}",
        "Total ms",
        a_tot_ms,
        b_tot_ms,
        a_tot_ms - b_tot_ms
    );
    println!(
        "  {:<18} {:>10.1} {:>10.1} {:>10.1}",
        "Avg ms",
        a_tot_ms / n,
        b_tot_ms / n,
        (a_tot_ms - b_tot_ms) / n
    );
    println!(
        "  {:<18} {:>10} {:>10} {:>10}",
        "Total tokens",
        a_tot_tok,
        b_tot_tok,
        a_tot_tok as i64 - b_tot_tok as i64
    );
    println!(
        "  {:<18} {:>10} {:>10} {:>10}",
        "Total results",
        a_tot_res,
        b_tot_res,
        a_tot_res as i64 - b_tot_res as i64
    );
    println!("  {:<18} {:>10} {:>10}", "Wins", a_wins, b_wins);
    println!("  {:<18} {:>10.1}x", "Avg speedup", avg_speedup);
    println!(" ============================================");

    let report = AbReport {
        name: "LeanKG A/B Test".into(),
        project: project_path.to_string(),
        timestamp: ts.to_string(),
        summary: AbSummary {
            total_queries: results.len(),
            a_total_ms: a_tot_ms,
            b_total_ms: b_tot_ms,
            a_avg_ms: a_tot_ms / n,
            b_avg_ms: b_tot_ms / n,
            avg_speedup,
            a_total_tokens: a_tot_tok,
            b_total_tokens: b_tot_tok,
            a_total_results: a_tot_res,
            b_total_results: b_tot_res,
            a_wins,
            b_wins,
        },
        results,
    };

    let out_dir = Path::new("benchmark/results");
    std::fs::create_dir_all(out_dir)?;
    let jp = out_dir.join(format!("ab-test-{}.json", ts));
    std::fs::write(&jp, serde_json::to_string_pretty(&report)?)?;
    println!("\nReport saved: {}", jp.display());
    Ok(())
}

fn run_lean_kg(
    graph: &Option<graph::GraphEngine>,
    tool: &str,
    desc: &str,
) -> (f64, String, usize, bool) {
    let start = Instant::now();
    let (out, count) = match (tool, graph) {
        ("search_code", Some(g)) => {
            let q = extract_arg(desc);
            let els = g.search_by_name_typed(&q, None, 50).unwrap_or_default();
            let n = els.len();
            (format!("{} results", n), n)
        }
        ("find_function", Some(g)) => {
            let q = extract_arg(desc);
            let els = g
                .search_by_name_typed(&q, Some("function"), 50)
                .unwrap_or_default();
            let n = els.len();
            (format!("{} functions", n), n)
        }
        _ => ("unknown".to_string(), 0),
    };
    let ms = start.elapsed().as_secs_f64() * 1000.0;
    (ms, out, count, true)
}

fn extract_arg(desc: &str) -> String {
    desc.split('(')
        .nth(1)
        .unwrap_or("")
        .split(')')
        .next()
        .unwrap_or("")
        .to_string()
}
