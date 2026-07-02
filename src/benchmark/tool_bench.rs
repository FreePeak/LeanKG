use crate::db;
use crate::graph;
use crate::ontology;
use serde::{Deserialize, Serialize};
use std::path::Path;
use std::time::Instant;

#[derive(Debug, Deserialize)]
struct OntologyToolsYaml {
    name: String,
    description: String,
    tools: Vec<ToolDef>,
}
#[derive(Debug, Clone, Deserialize)]
struct ToolDef {
    name: String,
    description: String,
    queries: Vec<QueryDef>,
}
#[derive(Debug, Clone, Deserialize)]
struct QueryDef {
    id: String,
    #[serde(default)]
    query: String,
    #[serde(default)]
    env: String,
    #[serde(default)]
    element_type: Option<String>,
    #[serde(default)]
    expected: serde_json::Value,
}
#[derive(Debug, Serialize)]
struct ToolBenchReport {
    name: String,
    description: String,
    project: String,
    timestamp: String,
    summary: SummaryRow,
    tool_results: Vec<ToolResult>,
}
#[derive(Debug, Serialize)]
struct SummaryRow {
    total_queries: usize,
    passed: usize,
    failed: usize,
    total_latency_ms: f64,
    avg_latency_ms: f64,
    max_latency_ms: f64,
}
#[derive(Debug, Serialize)]
struct ToolResult {
    tool: String,
    description: String,
    queries: Vec<QueryResult>,
    tool_avg_ms: f64,
    tool_max_ms: f64,
    tool_passed: usize,
    tool_failed: usize,
}
#[derive(Debug, Serialize)]
struct QueryResult {
    id: String,
    #[serde(default)]
    query: String,
    passed: bool,
    latency_ms: f64,
    result_summary: serde_json::Value,
    failures: Vec<String>,
}

macro_rules! expect {
    ($e:expr, $f:expr, $($arg:tt)*) => { if !($e) { $f.push(format!($($arg)*)); } };
}

fn e_i64(v: &serde_json::Value, key: &str) -> i64 {
    v.get(key).and_then(|x| x.as_i64()).unwrap_or(0)
}
fn e_str<'a>(v: &'a serde_json::Value, key: &str) -> Option<&'a str> {
    v.get(key).and_then(|x| x.as_str())
}
fn e_bool(v: &serde_json::Value, key: &str) -> Option<bool> {
    v.get(key).and_then(|x| x.as_bool())
}

pub fn run(project_path: &str) -> Result<(), Box<dyn std::error::Error>> {
    let db_path = Path::new(project_path).join(".leankg");
    if !db_path.exists() {
        return Err(format!("no .leankg at {}", db_path.display()).into());
    }
    let yaml_path = Path::new("benchmark/prompts/ontology-tools.yaml");
    let yaml: OntologyToolsYaml = serde_yaml::from_str(&std::fs::read_to_string(yaml_path)?)?;
    let db = db::schema::init_db(&db_path)?;
    let graph_engine = graph::GraphEngine::new(db.clone());
    if graph_engine.all_elements().unwrap_or_default().is_empty() {
        return Err("Empty DB. Run index first.".into());
    }
    let oq = ontology::OntologyQueryEngine::new(db);
    let ts = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);

    println!("Ontology Tools Benchmark\n  Project: {}\n", project_path);
    let mut all_results: Vec<ToolResult> = Vec::new();
    let (mut total_q, mut total_p, mut total_f) = (0usize, 0usize, 0usize);
    let mut all_lat: Vec<f64> = Vec::new();
    use serde_json::json;

    for tool in &yaml.tools {
        println!("  -- {} --", tool.name);
        let mut qs: Vec<QueryResult> = Vec::new();
        let mut tl: Vec<f64> = Vec::new();
        let (mut tp, mut tf) = (0usize, 0usize);
        for q in &tool.queries {
            let start = Instant::now();
            let (out, fails) = match tool.name.as_str() {
                "concept_search" => run_cs(&oq, &q.query, &q.env, &q.expected),
                "kg_context" => run_kc(&oq, &q.query, &q.env, &q.expected),
                "kg_concept_map" => run_km(&oq, &q.query, &q.env, &q.expected),
                "kg_trace_workflow" => run_tw(&oq, &q.query, &q.env, &q.expected),
                "semantic_search" => run_ss(&graph_engine, &q.query, &q.expected),
                "search_code" => run_sc(
                    &graph_engine,
                    &q.query,
                    q.element_type.as_deref(),
                    &q.expected,
                ),
                "query_file" => run_qf(&graph_engine, &q.query, &q.expected),
                "find_function" => run_ff(&graph_engine, &q.query, &q.expected),
                "kg_ontology_status" => run_os(&oq, &q.expected),
                _ => (json!({}), vec!["unknown tool".into()]),
            };
            let ms = start.elapsed().as_secs_f64() * 1000.0;
            tl.push(ms);
            let passed = fails.is_empty();
            println!(
                "    {:30} {:>8.1} ms  {}",
                q.id,
                ms,
                if passed { "PASS" } else { "FAIL" }
            );
            for f in &fails {
                println!("      FAIL: {}", f);
            }
            qs.push(QueryResult {
                id: q.id.clone(),
                query: q.query.clone(),
                passed,
                latency_ms: ms,
                result_summary: json!({"output": out}),
                failures: fails,
            });
            if passed {
                tp += 1;
            } else {
                tf += 1;
            }
            total_q += 1;
        }
        let avg = if tl.is_empty() {
            0.0
        } else {
            tl.iter().sum::<f64>() / tl.len() as f64
        };
        let max = tl.iter().copied().fold(0.0_f64, f64::max);
        println!(
            "    avg: {:.1} ms  max: {:.1} ms  passed: {}/{}\n",
            avg,
            max,
            tp,
            tp + tf
        );
        all_lat.extend(tl);
        total_p += tp;
        total_f += tf;
        all_results.push(ToolResult {
            tool: tool.name.clone(),
            description: tool.description.clone(),
            queries: qs,
            tool_avg_ms: avg,
            tool_max_ms: max,
            tool_passed: tp,
            tool_failed: tf,
        });
    }

    let tot = all_lat.iter().sum::<f64>();
    let lat_avg = if all_lat.is_empty() {
        0.0
    } else {
        tot / all_lat.len() as f64
    };
    let lat_max = all_lat.iter().copied().fold(0.0_f64, f64::max);
    println!("==========================================");
    println!(
        "  Total: {} passed: {} failed: {}",
        total_q, total_p, total_f
    );
    println!(
        "  Latency: total={:.1}ms avg={:.1}ms max={:.1}ms",
        tot, lat_avg, lat_max
    );
    println!("==========================================");

    let report = ToolBenchReport {
        name: yaml.name,
        description: yaml.description,
        project: project_path.to_string(),
        timestamp: ts.to_string(),
        summary: SummaryRow {
            total_queries: total_q,
            passed: total_p,
            failed: total_f,
            total_latency_ms: tot,
            avg_latency_ms: lat_avg,
            max_latency_ms: lat_max,
        },
        tool_results: all_results,
    };
    let out_dir = Path::new("benchmark/results");
    std::fs::create_dir_all(out_dir)?;
    let jp = out_dir.join(format!("ontology-tools-{}.json", ts));
    std::fs::write(&jp, serde_json::to_string_pretty(&report)?)?;
    println!("\nReport saved: {}", jp.display());
    let tp = out_dir.join(format!("ontology-tools-{}.txt", ts));
    let mut txt = format!("Ontology Tools Benchmark | Project: {}\n", project_path);
    txt.push_str(&format!(
        "{} queries, {} passed, {} failed\n",
        total_q, total_p, total_f
    ));
    txt.push_str(&format!(
        "Latency: total={:.1}ms avg={:.1}ms max={:.1}ms\n\n",
        tot, lat_avg, lat_max
    ));
    txt.push_str(&format!(
        "{:<22} {:>8} {:>8} {:>8} {:>8}\n",
        "Tool", "Queries", "P", "F", "Avg ms"
    ));
    for r in &report.tool_results {
        txt.push_str(&format!(
            "{:<22} {:>8} {:>8} {:>8} {:>8.1}\n",
            r.tool,
            r.queries.len(),
            r.tool_passed,
            r.tool_failed,
            r.tool_avg_ms
        ));
    }
    std::fs::write(&tp, &txt)?;
    println!("Text saved: {}", tp.display());
    Ok(())
}

fn run_cs(
    engine: &ontology::OntologyQueryEngine,
    q: &str,
    env: &str,
    exp: &serde_json::Value,
) -> (serde_json::Value, Vec<String>) {
    use serde_json::json;
    let mut f = Vec::new();
    match engine.concept_search(q, env, 20) {
        Ok(r) => {
            expect!(
                r.concept_match_count >= e_i64(exp, "min_concepts") as usize,
                f,
                "concepts {} < {}",
                r.concept_match_count,
                e_i64(exp, "min_concepts")
            );
            expect!(
                r.linked_code_count >= e_i64(exp, "min_linked_code") as usize,
                f,
                "linked {} < {}",
                r.linked_code_count,
                e_i64(exp, "min_linked_code")
            );
            if let Some(v) = e_bool(exp, "fallback_used") {
                expect!(r.fallback_used == v, f, "fallback_used mismatch");
            }
            if let Some(n) = e_str(exp, "top_concept_contains") {
                expect!(
                    r.matched_concepts
                        .first()
                        .is_some_and(|c| c.name.contains(n)),
                    f,
                    "top concept missing '{}'",
                    n
                );
            }
            (
                json!({"concepts": r.concept_match_count, "linked": r.linked_code_count, "refs": r.code_ref_count}),
                f,
            )
        }
        Err(e) => (
            json!({"error": format!("{}", e)}),
            vec![format!("error: {}", e)],
        ),
    }
}

fn run_kc(
    engine: &ontology::OntologyQueryEngine,
    q: &str,
    env: &str,
    exp: &serde_json::Value,
) -> (serde_json::Value, Vec<String>) {
    use serde_json::json;
    let mut f = Vec::new();
    match engine.get_ontology_context(q, env, 2) {
        Ok(r) => {
            expect!(
                r.matched_ontology_nodes.len() >= e_i64(exp, "min_nodes") as usize,
                f,
                "nodes {} < {}",
                r.matched_ontology_nodes.len(),
                e_i64(exp, "min_nodes")
            );
            expect!(
                r.expanded_code_context.len() >= e_i64(exp, "min_code_elements") as usize,
                f,
                "code {} < {}",
                r.expanded_code_context.len(),
                e_i64(exp, "min_code_elements")
            );
            if let Some(n) = e_str(exp, "top_concept_contains") {
                expect!(
                    r.matched_ontology_nodes
                        .first()
                        .is_some_and(|c| c.name.contains(n)),
                    f,
                    "top node missing '{}'",
                    n
                );
            }
            (
                json!({"nodes": r.matched_ontology_nodes.len(), "code": r.expanded_code_context.len(), "confidence": r.confidence}),
                f,
            )
        }
        Err(e) => (
            json!({"error": format!("{}", e)}),
            vec![format!("error: {}", e)],
        ),
    }
}

fn run_km(
    engine: &ontology::OntologyQueryEngine,
    q: &str,
    env: &str,
    exp: &serde_json::Value,
) -> (serde_json::Value, Vec<String>) {
    use serde_json::json;
    let mut f = Vec::new();
    match engine.search_ontology_nodes(q, env, 2) {
        Ok(nodes) => {
            expect!(
                nodes.len() >= e_i64(exp, "min_nodes") as usize,
                f,
                "nodes {} < {}",
                nodes.len(),
                e_i64(exp, "min_nodes")
            );
            if let Some(n) = e_str(exp, "node_contains") {
                expect!(
                    nodes.iter().any(|x| x.name.contains(n)),
                    f,
                    "no node contains '{}'",
                    n
                );
            }
            (json!({"nodes": nodes.len()}), f)
        }
        Err(e) => (
            json!({"error": format!("{}", e)}),
            vec![format!("error: {}", e)],
        ),
    }
}

fn run_tw(
    engine: &ontology::OntologyQueryEngine,
    q: &str,
    env: &str,
    exp: &serde_json::Value,
) -> (serde_json::Value, Vec<String>) {
    use serde_json::json;
    let mut f = Vec::new();
    match engine.trace_workflow(q, env) {
        Ok(steps) => {
            expect!(
                steps.len() >= e_i64(exp, "min_steps") as usize,
                f,
                "steps {} < {}",
                steps.len(),
                e_i64(exp, "min_steps")
            );
            if let Some(n) = e_str(exp, "step_contains") {
                expect!(
                    steps.iter().any(|s| s.name.contains(n)),
                    f,
                    "no step contains '{}'",
                    n
                );
            }
            (json!({"steps": steps.len()}), f)
        }
        Err(e) => (
            json!({"error": format!("{}", e)}),
            vec![format!("error: {}", e)],
        ),
    }
}

fn run_ss(
    graph: &graph::GraphEngine,
    q: &str,
    exp: &serde_json::Value,
) -> (serde_json::Value, Vec<String>) {
    use serde_json::json;
    let mut f = Vec::new();
    let els = graph.search_by_name_typed(q, None, 20).unwrap_or_default();
    expect!(
        els.len() >= e_i64(exp, "min_results") as usize,
        f,
        "results {} < {}",
        els.len(),
        e_i64(exp, "min_results")
    );
    (json!({"results": els.len()}), f)
}

fn run_sc(
    graph: &graph::GraphEngine,
    q: &str,
    et: Option<&str>,
    exp: &serde_json::Value,
) -> (serde_json::Value, Vec<String>) {
    use serde_json::json;
    let mut f = Vec::new();
    let els = graph.search_by_name_typed(q, et, 50).unwrap_or_default();
    expect!(
        els.len() >= e_i64(exp, "min_results") as usize,
        f,
        "results {} < {}",
        els.len(),
        e_i64(exp, "min_results")
    );
    (json!({"results": els.len()}), f)
}

fn run_qf(
    graph: &graph::GraphEngine,
    file: &str,
    exp: &serde_json::Value,
) -> (serde_json::Value, Vec<String>) {
    use serde_json::json;
    let mut f = Vec::new();
    let els = graph.get_elements_by_file(file).unwrap_or_default();
    expect!(
        els.len() >= e_i64(exp, "min_results") as usize,
        f,
        "results {} < {}",
        els.len(),
        e_i64(exp, "min_results")
    );
    (json!({"results": els.len()}), f)
}

fn run_ff(
    graph: &graph::GraphEngine,
    name: &str,
    exp: &serde_json::Value,
) -> (serde_json::Value, Vec<String>) {
    use serde_json::json;
    let mut f = Vec::new();
    let els = graph
        .search_by_name_typed(name, Some("function"), 50)
        .unwrap_or_default();
    expect!(
        els.len() >= e_i64(exp, "min_results") as usize,
        f,
        "results {} < {}",
        els.len(),
        e_i64(exp, "min_results")
    );
    (json!({"results": els.len()}), f)
}

fn run_os(
    engine: &ontology::OntologyQueryEngine,
    exp: &serde_json::Value,
) -> (serde_json::Value, Vec<String>) {
    use serde_json::json;
    let mut f = Vec::new();
    match engine.get_ontology_status() {
        Ok(s) => {
            if e_bool(exp, "has_counts").unwrap_or(false) {
                let t: usize = s.concept_counts.values().sum();
                expect!(t > 0, f, "concept count total == 0");
            }
            (
                json!({"concept_counts": s.concept_counts, "procedural_counts": s.procedural_counts, "aliases": s.total_aliases}),
                f,
            )
        }
        Err(e) => (
            json!({"error": format!("{}", e)}),
            vec![format!("error: {}", e)],
        ),
    }
}
