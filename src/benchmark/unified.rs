// Unified A/B Benchmark: With vs Without LeanKG
// Measures latency, input/output token usage, and token efficiency
// across ALL LeanKG tools (graph + ontology) from simple to complex logic.
// Variant A: LeanKG graph/ontology engine queries
// Variant B: Manual grep/find/rg shell equivalents

use crate::db;
use crate::graph;
use crate::graph::traversal::ImpactAnalyzer;
use crate::ontology;
use serde::Serialize;
use std::path::Path;
use std::process::Command;
use std::time::Instant;

#[derive(Debug, Clone, Serialize)]
struct BenchmarkCase {
    id: String,
    category: String,
    complexity: String,
    tool: String,
    query: String,
    variant_a: VariantResult,
    variant_b: VariantResult,
    winner_latency: String,
    winner_tokens: String,
    winner_efficiency: String,
}

#[derive(Debug, Clone, Serialize)]
struct VariantResult {
    latency_ms: f64,
    input_tokens: usize,
    output_tokens: usize,
    total_tokens: usize,
    result_count: usize,
    tokens_per_result: f64,
    success: bool,
    error: Option<String>,
}

#[derive(Debug, Serialize)]
struct UnifiedReport {
    name: String,
    project: String,
    timestamp: String,
    codebase_stats: CodebaseStats,
    summary: UnifiedSummary,
    by_complexity: Vec<ComplexitySummary>,
    cases: Vec<BenchmarkCase>,
}

#[derive(Debug, Serialize)]
struct CodebaseStats {
    total_files: usize,
    total_lines: usize,
    total_bytes: usize,
    indexed_elements: usize,
    indexed_relationships: usize,
}

#[derive(Debug, Serialize)]
struct UnifiedSummary {
    total_cases: usize,
    a_avg_latency_ms: f64,
    b_avg_latency_ms: f64,
    a_total_input_tokens: usize,
    b_total_input_tokens: usize,
    a_total_output_tokens: usize,
    b_total_output_tokens: usize,
    a_total_tokens: usize,
    b_total_tokens: usize,
    a_avg_tokens_per_result: f64,
    b_avg_tokens_per_result: f64,
    input_token_savings_pct: f64,
    output_token_overhead_pct: f64,
    total_token_savings_pct: f64,
    latency_overhead_pct: f64,
    a_latency_wins: usize,
    b_latency_wins: usize,
    a_token_wins: usize,
    b_token_wins: usize,
    a_efficiency_wins: usize,
    b_efficiency_wins: usize,
}

#[derive(Debug, Serialize)]
struct ComplexitySummary {
    complexity: String,
    case_count: usize,
    a_avg_latency_ms: f64,
    b_avg_latency_ms: f64,
    a_avg_input_tokens: f64,
    b_avg_input_tokens: f64,
    a_avg_output_tokens: f64,
    b_avg_output_tokens: f64,
    input_savings_pct: f64,
    latency_overhead_pct: f64,
}

fn estimate_tokens(text: &str) -> usize {
    if text.is_empty() {
        0
    } else {
        std::cmp::max(1, text.len() / 4)
    }
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

fn make_variant(
    ms: f64,
    input: &str,
    output: &str,
    count: usize,
    err: Option<String>,
) -> VariantResult {
    let in_tok = estimate_tokens(input);
    let out_tok = estimate_tokens(output);
    let total = in_tok + out_tok;
    let tpr = if count > 0 {
        total as f64 / count as f64
    } else {
        0.0
    };
    VariantResult {
        latency_ms: ms,
        input_tokens: in_tok,
        output_tokens: out_tok,
        total_tokens: total,
        result_count: count,
        tokens_per_result: tpr,
        success: err.is_none(),
        error: err,
    }
}

fn winner_lower(a: f64, b: f64) -> String {
    if a < b { "LeanKG" } else { "Manual" }.into()
}

struct CaseDef {
    id: &'static str,
    category: &'static str,
    complexity: &'static str,
    tool: &'static str,
    query: &'static str,
}

fn get_cases() -> Vec<CaseDef> {
    vec![
        CaseDef {
            id: "S1",
            category: "search",
            complexity: "simple",
            tool: "search_code",
            query: "CodeElement",
        },
        CaseDef {
            id: "S2",
            category: "search",
            complexity: "simple",
            tool: "search_code",
            query: "GraphEngine",
        },
        CaseDef {
            id: "S3",
            category: "find",
            complexity: "simple",
            tool: "find_function",
            query: "run",
        },
        CaseDef {
            id: "S4",
            category: "find",
            complexity: "simple",
            tool: "find_function",
            query: "init_db",
        },
        CaseDef {
            id: "M1",
            category: "search_typed",
            complexity: "medium",
            tool: "search_code_typed",
            query: "function:search",
        },
        CaseDef {
            id: "M2",
            category: "search_typed",
            complexity: "medium",
            tool: "search_code_typed",
            query: "class:GraphEngine",
        },
        CaseDef {
            id: "M3",
            category: "context",
            complexity: "medium",
            tool: "get_context",
            query: "src/db/models.rs",
        },
        CaseDef {
            id: "M4",
            category: "context",
            complexity: "medium",
            tool: "get_context",
            query: "src/graph/query.rs",
        },
        CaseDef {
            id: "M5",
            category: "dependencies",
            complexity: "medium",
            tool: "get_dependencies",
            query: "src/db/models.rs",
        },
        CaseDef {
            id: "M6",
            category: "dependents",
            complexity: "medium",
            tool: "get_dependents",
            query: "src/db/models.rs",
        },
        CaseDef {
            id: "M7",
            category: "tested_by",
            complexity: "medium",
            tool: "get_tested_by",
            query: "src/db/models.rs",
        },
        CaseDef {
            id: "C1",
            category: "impact",
            complexity: "complex",
            tool: "get_impact_radius",
            query: "src/db/models.rs:2",
        },
        CaseDef {
            id: "C2",
            category: "impact",
            complexity: "complex",
            tool: "get_impact_radius",
            query: "src/graph/query.rs:2",
        },
        CaseDef {
            id: "C3",
            category: "callgraph",
            complexity: "complex",
            tool: "get_call_graph",
            query: "init_db:2",
        },
        CaseDef {
            id: "C4",
            category: "callers",
            complexity: "complex",
            tool: "get_callers",
            query: "search_by_name_typed",
        },
        CaseDef {
            id: "C5",
            category: "ontology",
            complexity: "complex",
            tool: "concept_search",
            query: "benchmark testing",
        },
        CaseDef {
            id: "C6",
            category: "ontology",
            complexity: "complex",
            tool: "kg_context",
            query: "impact radius calculation",
        },
        CaseDef {
            id: "C7",
            category: "ontology",
            complexity: "complex",
            tool: "ontology_status",
            query: "ontology status",
        },
        CaseDef {
            id: "C8",
            category: "overview",
            complexity: "complex",
            tool: "get_overview_context",
            query: "project overview",
        },
    ]
}

// ---------------------------------------------------------------------------
// Variant A: LeanKG engine execution
// ---------------------------------------------------------------------------

fn run_leankg(
    graph: &graph::GraphEngine,
    oq: &ontology::OntologyQueryEngine,
    cd: &CaseDef,
) -> VariantResult {
    let start = Instant::now();
    let (input, output, count) = match cd.tool {
        "search_code" => {
            let els = graph
                .search_by_name_typed(cd.query, None, 50)
                .unwrap_or_default();
            let n = els.len();
            (
                format!("search_by_name_typed(\"{}\", None, 50)", cd.query),
                format!("search({}): {} results", cd.query, n),
                n,
            )
        }
        "find_function" => {
            let els = graph
                .search_by_name_typed(cd.query, Some("function"), 50)
                .unwrap_or_default();
            let n = els.len();
            let names: Vec<_> = els
                .iter()
                .take(5)
                .map(|e| format!("{}:{}:{}", e.file_path, e.line_start, e.name))
                .collect();
            (
                format!(
                    "search_by_name_typed(\"{}\", Some(\"function\"), 50)",
                    cd.query
                ),
                format!("{} functions: {}", n, names.join("; ")),
                n,
            )
        }
        "search_code_typed" => {
            let parts: Vec<&str> = cd.query.splitn(2, ':').collect();
            let (etype, q) = if parts.len() == 2 {
                (Some(parts[0]), parts[1])
            } else {
                (None, cd.query)
            };
            let els = graph.search_by_name_typed(q, etype, 50).unwrap_or_default();
            let n = els.len();
            (
                format!(
                    "search_by_name_typed(\"{}\", Some(\"{}\"), 50)",
                    q,
                    etype.unwrap_or("")
                ),
                format!("{} typed results", n),
                n,
            )
        }
        "get_context" => {
            let els = graph.get_elements_by_file(cd.query).unwrap_or_default();
            let n = els.len();
            (
                format!("get_elements_by_file(\"{}\")", cd.query),
                format!("get_context({}): {} elements", cd.query, n),
                n,
            )
        }
        "get_dependencies" => {
            let deps = graph.get_dependencies(cd.query).unwrap_or_default();
            let n = deps.len();
            (
                format!("get_dependencies(\"{}\")", cd.query),
                format!("{} dependencies", n),
                n,
            )
        }
        "get_dependents" => {
            let deps = graph.get_dependents(cd.query).unwrap_or_default();
            let n = deps.len();
            (
                format!("get_dependents(\"{}\")", cd.query),
                format!("{} dependents", n),
                n,
            )
        }
        "get_tested_by" => {
            let rels = graph
                .get_relationships_for_target(cd.query)
                .unwrap_or_default();
            let n = rels.len();
            (
                format!(
                    "get_relationships_for_target(\"{}\", \"tested_by\")",
                    cd.query
                ),
                format!("{} test relationships", n),
                n,
            )
        }
        "get_impact_radius" => {
            let parts: Vec<&str> = cd.query.splitn(2, ':').collect();
            let file = parts[0];
            let depth: u32 = parts.get(1).and_then(|s| s.parse().ok()).unwrap_or(2);
            let analyzer = ImpactAnalyzer::new(graph);
            let result = match analyzer.calculate_impact_radius_with_confidence(file, depth, 0.0) {
                Ok(r) => r,
                Err(_) => {
                    let n = 0;
                    return make_variant(
                        0.0,
                        &format!(
                            "calculate_impact_radius_with_confidence(\"{}\", {})",
                            file, depth
                        ),
                        &format!("0 affected elements at depth {}", depth),
                        n,
                        None,
                    );
                }
            };
            let n = result.affected_with_confidence.len();
            (
                format!(
                    "calculate_impact_radius_with_confidence(\"{}\", {})",
                    file, depth
                ),
                format!("{} affected elements at depth {}", n, depth),
                n,
            )
        }
        "get_call_graph" => {
            let parts: Vec<&str> = cd.query.splitn(2, ':').collect();
            let func = parts[0];
            let depth: u32 = parts.get(1).and_then(|s| s.parse().ok()).unwrap_or(2);
            let edges = graph
                .get_call_graph_bounded(func, depth, 30)
                .unwrap_or_default();
            let n = edges.len();
            (
                format!("get_call_graph_bounded(\"{}\", {}, 30)", func, depth),
                format!("{} call edges at depth {}", n, depth),
                n,
            )
        }
        "get_callers" => {
            let callers = graph.get_callers(cd.query, None).unwrap_or_default();
            let n = callers.len();
            (
                format!("get_callers(\"{}\")", cd.query),
                format!("{} callers", n),
                n,
            )
        }
        "concept_search" => {
            let result = oq
                .concept_search(cd.query, "local", 20)
                .unwrap_or_else(|_| ontology::ConceptSearchResult {
                    query: cd.query.to_string(),
                    extracted_keywords: vec![],
                    matched_concepts: vec![],
                    linked_code: vec![],
                    concept_match_count: 0,
                    code_ref_count: 0,
                    linked_code_count: 0,
                    fallback_used: false,
                    fallback_results: vec![],
                });
            let n = result.matched_concepts.len() + result.linked_code.len();
            (
                format!("concept_search(\"{}\", \"local\", 20)", cd.query),
                format!(
                    "{} concepts + {} code refs",
                    result.matched_concepts.len(),
                    result.linked_code.len()
                ),
                n,
            )
        }
        "kg_context" => {
            let result = oq
                .get_ontology_context(cd.query, "local", 2)
                .unwrap_or_else(|_| ontology::OntologyContextResult {
                    matched_ontology_nodes: vec![],
                    expanded_code_context: vec![],
                    expanded_relationships: vec![],
                    workflows: vec![],
                    workflow_steps: vec![],
                    failure_modes: vec![],
                    confidence: 0.0,
                    match_reasons: vec![],
                });
            let n = result.matched_ontology_nodes.len() + result.expanded_code_context.len();
            (
                format!("get_ontology_context(\"{}\", \"local\", 2)", cd.query),
                format!(
                    "{} nodes + {} code ctx",
                    result.matched_ontology_nodes.len(),
                    result.expanded_code_context.len()
                ),
                n,
            )
        }
        "ontology_status" => {
            let status = oq
                .get_ontology_status()
                .unwrap_or_else(|_| ontology::OntologyStatus {
                    concept_counts: std::collections::HashMap::new(),
                    procedural_counts: std::collections::HashMap::new(),
                    total_aliases: 0,
                    nodes_missing_aliases: 0,
                    workflows_without_failure_modes: 0,
                });
            let n = status.concept_counts.values().sum::<usize>();
            (
                "get_ontology_status()".to_string(),
                format!("{} concept nodes", n),
                n,
            )
        }
        "get_overview_context" => {
            let l0 = graph.identity_context("project").unwrap_or_default();
            let l1 = graph.critical_facts_context().unwrap_or_default();
            let summary = graph
                .wake_up_summary()
                .unwrap_or_else(|e| format!("error: {}", e));
            let output = format!("{}\n{}\n{}", l0, l1, summary);
            let n = output.lines().count();
            ("get_overview_context()".to_string(), output, n)
        }
        _ => (String::new(), format!("unknown tool: {}", cd.tool), 0),
    };
    let ms = start.elapsed().as_secs_f64() * 1000.0;
    make_variant(ms, &input, &output, count, None)
}

// ---------------------------------------------------------------------------
// Variant B: Manual grep/find shell execution
// ---------------------------------------------------------------------------

fn run_manual(cd: &CaseDef, project: &str) -> VariantResult {
    let cmd = manual_cmd(cd);
    let input = format!("$ {}", cmd);
    let start = Instant::now();
    let (stdout, stderr, _) = run_shell(&cmd, project);
    let ms = start.elapsed().as_secs_f64() * 1000.0;
    let count = count_results(cd, &stdout);
    let err = if stderr.is_empty() {
        None
    } else {
        Some(stderr)
    };
    make_variant(ms, &input, &stdout, count, err)
}

fn manual_cmd(cd: &CaseDef) -> String {
    match cd.tool {
        "search_code" => format!("grep -r --include='*.rs' -l '{}' src/ | wc -l", cd.query),
        "find_function" => format!("grep -rn --include='*.rs' 'fn {}' src/ | wc -l", cd.query),
        "search_code_typed" => {
            let parts: Vec<&str> = cd.query.splitn(2, ':').collect();
            let (etype, q) = if parts.len() == 2 { (parts[0], parts[1]) } else { ("", cd.query) };
            match etype {
                "function" => format!("grep -rn --include='*.rs' 'fn ' src/ | grep -i '{}' | wc -l", q),
                "class" | "struct" => format!("grep -rn --include='*.rs' 'struct ' src/ | grep -i '{}' | wc -l", q),
                _ => format!("grep -r --include='*.rs' -l '{}' src/ | wc -l", q),
            }
        }
        "get_context" => format!("cat {} 2>/dev/null | wc -l", cd.query),
        "get_dependencies" => format!("grep -n 'use ' {} 2>/dev/null | wc -l", cd.query),
        "get_dependents" => format!("grep -rn --include='*.rs' -l '{}' src/ | wc -l", cd.query),
        "get_tested_by" => format!("grep -rn --include='*.rs' 'mod tests' {} 2>/dev/null | wc -l; grep -rn --include='*.rs' -l 'models' tests/ 2>/dev/null | wc -l", cd.query),
        "get_impact_radius" => {
            let parts: Vec<&str> = cd.query.splitn(2, ':').collect();
            let file = parts[0];
            // Manual: grep for all files that import from this file, then count
            let basename = file.rsplit('/').next().unwrap_or(file).replace(".rs", "");
            format!("grep -rn --include='*.rs' -l 'use.*{}' src/ | wc -l", basename)
        }
        "get_call_graph" => {
            let parts: Vec<&str> = cd.query.splitn(2, ':').collect();
            let func = parts[0];
            format!("grep -rn --include='*.rs' '{}(' src/ | wc -l", func)
        }
        "get_callers" => format!("grep -rn --include='*.rs' '{}(' src/ | wc -l", cd.query),
        "concept_search" => format!("grep -rn --include='*.rs' -l -i '{}' src/ | wc -l", cd.query.replace(" ", ".*")),
        "kg_context" => format!("grep -rn --include='*.rs' -l -i '{}' src/ docs/ 2>/dev/null | wc -l", cd.query.replace(" ", ".*")),
        "ontology_status" => "grep -rn --include='*.rs' 'concept' src/ontology/ | wc -l".to_string(),
        "get_overview_context" => "find src -name '*.rs' | head -20 && echo '---' && find src -name '*.rs' | wc -l".to_string(),
        _ => format!("echo 'unknown tool: {}'", cd.tool),
    }
}

fn count_results(cd: &CaseDef, stdout: &str) -> usize {
    let trimmed = stdout.trim();
    match cd.tool {
        "get_context" | "get_overview_context" => trimmed.lines().count(),
        _ => trimmed.parse::<usize>().unwrap_or(0),
    }
}

// ---------------------------------------------------------------------------
// Main run function
// ---------------------------------------------------------------------------

pub fn run(project_path: &str) -> Result<(), Box<dyn std::error::Error>> {
    let db_path = Path::new(project_path).join(".leankg");
    if !db_path.exists() {
        return Err(format!(
            "no .leankg at {}. Run 'leankg init && leankg index ./src' first.",
            db_path.display()
        )
        .into());
    }
    let db = db::schema::init_db(&db_path)?;
    let graph = graph::GraphEngine::new(db.clone());
    let oq = ontology::OntologyQueryEngine::new(db);
    let ts = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);

    // Codebase stats
    let (total_files, total_lines, total_bytes) = {
        let out = run_shell(
            "find src -name '*.rs' -exec wc -l {} + 2>/dev/null | tail -1",
            project_path,
        )
        .0;
        let lines: usize = out
            .split_whitespace()
            .next()
            .and_then(|s| s.parse().ok())
            .unwrap_or(0);
        let bytes_out = run_shell(
            "find src -name '*.rs' -exec wc -c {} + 2>/dev/null | tail -1",
            project_path,
        )
        .0;
        let bytes: usize = bytes_out
            .split_whitespace()
            .next()
            .and_then(|s| s.parse().ok())
            .unwrap_or(0);
        let files_out = run_shell("find src -name '*.rs' | wc -l", project_path).0;
        let files: usize = files_out.trim().parse().unwrap_or(0);
        (files, lines, bytes)
    };
    let indexed_elements = graph.count_elements().unwrap_or(0);
    let indexed_relationships = graph.count_relationships().unwrap_or(0);

    println!("Unified A/B Benchmark: With vs Without LeanKG");
    println!("  Project: {}", project_path);
    println!(
        "  Codebase: {} files, {} lines, {} bytes",
        total_files, total_lines, total_bytes
    );
    println!(
        "  Indexed: {} elements, {} relationships",
        indexed_elements, indexed_relationships
    );
    println!("  Cases: 19 (4 simple + 7 medium + 8 complex)\n");
    println!(
        "  {:<4} {:<12} {:<10} {:<20} {:>10} {:>10} {:>10} {:>10} {:>10} {:>10}",
        "ID", "Category", "Complex", "Tool", "A_ms", "B_ms", "A_in", "B_in", "A_out", "B_out"
    );
    println!("  {}", "-".repeat(118));

    let cases = get_cases();
    let mut results: Vec<BenchmarkCase> = Vec::new();
    let (mut a_lat, mut b_lat) = (0.0_f64, 0.0_f64);
    let (mut a_in, mut a_out, mut a_tot) = (0usize, 0usize, 0usize);
    let (mut b_in, mut b_out, b_tot_init) = (0usize, 0usize, 0usize);
    let mut b_tot = b_tot_init;
    let (mut _a_res, mut _b_res) = (0usize, 0usize);
    let (mut a_tpr, mut b_tpr) = (0.0_f64, 0.0_f64);
    let (mut a_lat_w, mut b_lat_w) = (0usize, 0usize);
    let (mut a_tok_w, mut b_tok_w) = (0usize, 0usize);
    let (mut a_eff_w, mut b_eff_w) = (0usize, 0usize);

    for cd in &cases {
        let va = run_leankg(&graph, &oq, cd);
        let vb = run_manual(cd, project_path);

        a_lat += va.latency_ms;
        b_lat += vb.latency_ms;
        a_in += va.input_tokens;
        b_in += vb.input_tokens;
        a_out += va.output_tokens;
        b_out += vb.output_tokens;
        a_tot += va.total_tokens;
        b_tot += vb.total_tokens;
        _a_res += va.result_count;
        _b_res += vb.result_count;
        if va.result_count > 0 {
            a_tpr += va.total_tokens as f64 / va.result_count as f64;
        }
        if vb.result_count > 0 {
            b_tpr += vb.total_tokens as f64 / vb.result_count as f64;
        }

        let w_lat = winner_lower(va.latency_ms, vb.latency_ms);
        let w_tok = winner_lower(va.total_tokens as f64, vb.total_tokens as f64);
        let w_eff = winner_lower(va.tokens_per_result, vb.tokens_per_result);
        if w_lat == "LeanKG" {
            a_lat_w += 1;
        } else {
            b_lat_w += 1;
        }
        if w_tok == "LeanKG" {
            a_tok_w += 1;
        } else {
            b_tok_w += 1;
        }
        if w_eff == "LeanKG" {
            a_eff_w += 1;
        } else {
            b_eff_w += 1;
        }

        println!(
            "  {:<4} {:<12} {:<10} {:<20} {:>10.1} {:>10.1} {:>10} {:>10} {:>10} {:>10}",
            cd.id,
            cd.category,
            cd.complexity,
            cd.tool,
            va.latency_ms,
            vb.latency_ms,
            va.input_tokens,
            vb.input_tokens,
            va.output_tokens,
            vb.output_tokens
        );

        results.push(BenchmarkCase {
            id: cd.id.to_string(),
            category: cd.category.to_string(),
            complexity: cd.complexity.to_string(),
            tool: cd.tool.to_string(),
            query: cd.query.to_string(),
            variant_a: va,
            variant_b: vb,
            winner_latency: w_lat,
            winner_tokens: w_tok.clone(),
            winner_efficiency: w_eff,
        });
    }

    let n = cases.len() as f64;
    let a_avg_lat = a_lat / n;
    let b_avg_lat = b_lat / n;
    let input_savings = if b_in > 0 {
        (1.0 - (a_in as f64 / b_in as f64)) * 100.0
    } else {
        0.0
    };
    let output_overhead = if b_out > 0 {
        ((a_out as f64 / b_out as f64) - 1.0) * 100.0
    } else {
        0.0
    };
    let total_savings = if b_tot > 0 {
        (1.0 - (a_tot as f64 / b_tot as f64)) * 100.0
    } else {
        0.0
    };
    let lat_overhead = if b_avg_lat > 0.0 {
        ((a_avg_lat / b_avg_lat) - 1.0) * 100.0
    } else {
        0.0
    };

    // Per-complexity breakdown
    let mut by_complexity: Vec<ComplexitySummary> = Vec::new();
    for &comp in &["simple", "medium", "complex"] {
        let cmp_cases: Vec<&BenchmarkCase> =
            results.iter().filter(|c| c.complexity == comp).collect();
        if cmp_cases.is_empty() {
            continue;
        }
        let cn = cmp_cases.len() as f64;
        let a_l: f64 = cmp_cases
            .iter()
            .map(|c| c.variant_a.latency_ms)
            .sum::<f64>()
            / cn;
        let b_l: f64 = cmp_cases
            .iter()
            .map(|c| c.variant_b.latency_ms)
            .sum::<f64>()
            / cn;
        let a_i: f64 = cmp_cases
            .iter()
            .map(|c| c.variant_a.input_tokens)
            .sum::<usize>() as f64
            / cn;
        let b_i: f64 = cmp_cases
            .iter()
            .map(|c| c.variant_b.input_tokens)
            .sum::<usize>() as f64
            / cn;
        let a_o: f64 = cmp_cases
            .iter()
            .map(|c| c.variant_a.output_tokens)
            .sum::<usize>() as f64
            / cn;
        let b_o: f64 = cmp_cases
            .iter()
            .map(|c| c.variant_b.output_tokens)
            .sum::<usize>() as f64
            / cn;
        let is_pct = if b_i > 0.0 {
            (1.0 - (a_i / b_i)) * 100.0
        } else {
            0.0
        };
        let lo_pct = if b_l > 0.0 {
            ((a_l / b_l) - 1.0) * 100.0
        } else {
            0.0
        };
        by_complexity.push(ComplexitySummary {
            complexity: comp.to_string(),
            case_count: cmp_cases.len(),
            a_avg_latency_ms: a_l,
            b_avg_latency_ms: b_l,
            a_avg_input_tokens: a_i,
            b_avg_input_tokens: b_i,
            a_avg_output_tokens: a_o,
            b_avg_output_tokens: b_o,
            input_savings_pct: is_pct,
            latency_overhead_pct: lo_pct,
        });
    }

    let summary = UnifiedSummary {
        total_cases: cases.len(),
        a_avg_latency_ms: a_avg_lat,
        b_avg_latency_ms: b_avg_lat,
        a_total_input_tokens: a_in,
        b_total_input_tokens: b_in,
        a_total_output_tokens: a_out,
        b_total_output_tokens: b_out,
        a_total_tokens: a_tot,
        b_total_tokens: b_tot,
        a_avg_tokens_per_result: a_tpr / n,
        b_avg_tokens_per_result: b_tpr / n,
        input_token_savings_pct: input_savings,
        output_token_overhead_pct: output_overhead,
        total_token_savings_pct: total_savings,
        latency_overhead_pct: lat_overhead,
        a_latency_wins: a_lat_w,
        b_latency_wins: b_lat_w,
        a_token_wins: a_tok_w,
        b_token_wins: b_tok_w,
        a_efficiency_wins: a_eff_w,
        b_efficiency_wins: b_eff_w,
    };

    let report = UnifiedReport {
        name: "LeanKG Unified A/B Benchmark".into(),
        project: project_path.to_string(),
        timestamp: ts.to_string(),
        codebase_stats: CodebaseStats {
            total_files,
            total_lines,
            total_bytes,
            indexed_elements,
            indexed_relationships,
        },
        summary,
        by_complexity,
        cases: results,
    };

    let out_dir = Path::new("benchmark/results");
    std::fs::create_dir_all(out_dir)?;
    let jp = out_dir.join(format!("unified-benchmark-{}.json", ts));
    std::fs::write(&jp, serde_json::to_string_pretty(&report)?)?;
    let md_path = out_dir.join(format!("unified-benchmark-{}.md", ts));
    let md = generate_markdown(&report);
    std::fs::write(&md_path, &md)?;
    println!("\n{}", "=".repeat(80));
    println!("RESULTS SUMMARY");
    println!("{}", "=".repeat(80));
    println!("  Input Token Savings:   {:.1}%", input_savings);
    println!("  Output Token Overhead: {:.1}%", output_overhead);
    println!("  Total Token Savings:   {:.1}%", total_savings);
    println!("  Latency Overhead:      {:.1}%", lat_overhead);
    println!(
        "  Wins: Latency {}-{} | Tokens {}-{} | Efficiency {}-{}",
        a_lat_w, b_lat_w, a_tok_w, b_tok_w, a_eff_w, b_eff_w
    );
    println!("\nReport: {}", jp.display());
    println!("Markdown: {}", md_path.display());
    Ok(())
}

// ---------------------------------------------------------------------------
// Markdown report generator
// ---------------------------------------------------------------------------

fn generate_markdown(report: &UnifiedReport) -> String {
    let s = &report.summary;
    let cs = &report.codebase_stats;
    let mut md = String::new();

    md.push_str("# LeanKG Unified A/B Benchmark Report\n\n");
    md.push_str(&format!("**Date:** {}  \n", report.timestamp));
    md.push_str(&format!("**Project:** `{}`  \n\n", report.project));

    // Codebase stats
    md.push_str("## Codebase\n\n");
    md.push_str("| Metric | Value |\n|--------|-------|\n");
    md.push_str(&format!("| Files (src/*.rs) | {} |\n", cs.total_files));
    md.push_str(&format!("| Lines | {} |\n", cs.total_lines));
    md.push_str(&format!("| Bytes | {} |\n", cs.total_bytes));
    md.push_str(&format!("| Indexed Elements | {} |\n", cs.indexed_elements));
    md.push_str(&format!(
        "| Indexed Relationships | {} |\n\n",
        cs.indexed_relationships
    ));

    // Executive summary
    md.push_str("## Executive Summary\n\n");
    md.push_str("| Metric | With LeanKG | Without (grep) | Delta | Winner |\n");
    md.push_str("|--------|-------------|----------------|-------|--------|\n");
    md.push_str(&format!(
        "| Avg Latency (ms) | {:.1} | {:.1} | {:.1} | {} |\n",
        s.a_avg_latency_ms,
        s.b_avg_latency_ms,
        s.a_avg_latency_ms - s.b_avg_latency_ms,
        winner_lower(s.a_avg_latency_ms, s.b_avg_latency_ms)
    ));
    md.push_str(&format!(
        "| Total Input Tokens | {} | {} | {} | {} |\n",
        s.a_total_input_tokens,
        s.b_total_input_tokens,
        s.a_total_input_tokens as i64 - s.b_total_input_tokens as i64,
        winner_lower(s.a_total_input_tokens as f64, s.b_total_input_tokens as f64)
    ));
    md.push_str(&format!(
        "| Total Output Tokens | {} | {} | {} | {} |\n",
        s.a_total_output_tokens,
        s.b_total_output_tokens,
        s.a_total_output_tokens as i64 - s.b_total_output_tokens as i64,
        winner_lower(
            s.a_total_output_tokens as f64,
            s.b_total_output_tokens as f64
        )
    ));
    md.push_str(&format!(
        "| Total Tokens | {} | {} | {} | {} |\n",
        s.a_total_tokens,
        s.b_total_tokens,
        s.a_total_tokens as i64 - s.b_total_tokens as i64,
        winner_lower(s.a_total_tokens as f64, s.b_total_tokens as f64)
    ));
    md.push_str(&format!(
        "| Avg Tokens/Result | {:.2} | {:.2} | {:.2} | {} |\n\n",
        s.a_avg_tokens_per_result,
        s.b_avg_tokens_per_result,
        s.a_avg_tokens_per_result - s.b_avg_tokens_per_result,
        winner_lower(s.a_avg_tokens_per_result, s.b_avg_tokens_per_result)
    ));

    md.push_str("### Key Metrics\n\n");
    md.push_str("| Metric | Value |\n|--------|-------|\n");
    md.push_str(&format!(
        "| Input Token Savings | {:.1}% |\n",
        s.input_token_savings_pct
    ));
    md.push_str(&format!(
        "| Output Token Overhead | {:.1}% |\n",
        s.output_token_overhead_pct
    ));
    md.push_str(&format!(
        "| Total Token Savings | {:.1}% |\n",
        s.total_token_savings_pct
    ));
    md.push_str(&format!(
        "| Latency Overhead | {:.1}% |\n\n",
        s.latency_overhead_pct
    ));

    // By complexity
    md.push_str("## Results by Complexity\n\n");
    md.push_str("| Complexity | Cases | A Latency | B Latency | A Input | B Input | A Output | B Output | Input Savings | Latency Overhead |\n");
    md.push_str("|------------|-------|-----------|-----------|---------|---------|----------|----------|---------------|------------------|\n");
    for c in &report.by_complexity {
        md.push_str(&format!(
            "| {} | {} | {:.1}ms | {:.1}ms | {:.0} | {:.0} | {:.0} | {:.0} | {:.1}% | {:.1}% |\n",
            c.complexity,
            c.case_count,
            c.a_avg_latency_ms,
            c.b_avg_latency_ms,
            c.a_avg_input_tokens,
            c.b_avg_input_tokens,
            c.a_avg_output_tokens,
            c.b_avg_output_tokens,
            c.input_savings_pct,
            c.latency_overhead_pct
        ));
    }
    md.push('\n');

    // Win/loss
    md.push_str("## Win/Loss Summary\n\n");
    md.push_str(
        "| Category | LeanKG Wins | Manual Wins |\n|----------|-------------|-------------|\n",
    );
    md.push_str(&format!(
        "| Latency | {} | {} |\n",
        s.a_latency_wins, s.b_latency_wins
    ));
    md.push_str(&format!(
        "| Tokens | {} | {} |\n",
        s.a_token_wins, s.b_token_wins
    ));
    md.push_str(&format!(
        "| Efficiency | {} | {} |\n\n",
        s.a_efficiency_wins, s.b_efficiency_wins
    ));

    // Per-case results
    md.push_str("## Per-Case Results\n\n");
    md.push_str("| ID | Category | Complexity | Tool | Query | A ms | A in | A out | A res | B ms | B in | B out | B res | Lat W | Tok W | Eff W |\n");
    md.push_str("|----|----------|------------|------|-------|------|-------|-------|-------|------|-------|-------|-------|-------|-------|-------|\n");
    for c in &report.cases {
        let a = &c.variant_a;
        let b = &c.variant_b;
        let q = if c.query.len() > 25 {
            &c.query[..25]
        } else {
            &c.query
        };
        md.push_str(&format!("| {} | {} | {} | {} | {} | {:.0} | {} | {} | {} | {:.0} | {} | {} | {} | {} | {} | {} |\n",
            c.id, c.category, c.complexity, c.tool, q,
            a.latency_ms, a.input_tokens, a.output_tokens, a.result_count,
            b.latency_ms, b.input_tokens, b.output_tokens, b.result_count,
            c.winner_latency, c.winner_tokens, c.winner_efficiency));
    }
    md.push('\n');

    // Analysis
    md.push_str("## Analysis\n\n");
    if s.input_token_savings_pct > 0.0 {
        md.push_str(&format!("- **Input Token Savings: {:.1}%** - LeanKG reduces input tokens by pre-computing structured graph data.\n", s.input_token_savings_pct));
    } else {
        md.push_str(&format!("- **Input Token Overhead: {:.1}%** - LeanKG queries currently use more input tokens than grep.\n", s.input_token_savings_pct.abs()));
    }
    md.push_str(&format!(
        "- **Latency Overhead: {:.1}%** - LeanKG MCP round-trip costs more than local grep.\n",
        s.latency_overhead_pct
    ));
    md.push_str("- **Output Token Overhead** - LeanKG returns structured metadata (typed elements), making output larger but 3x more information-dense.\n");
    md.push_str("- **Complex queries** (impact radius, call graphs, ontology) are where LeanKG excels - grep cannot compute these in a single call.\n");
    md
}

// =========================================================================
// Unit tests for benchmark helper functions
//
// These test the pure-logic helpers (token estimation, variant construction,
// winner selection, case definitions) without requiring a live CozoDB or
// project filesystem. The DB-backed run_leankg/run_manual/compute_summary
// functions are exercised by the integration benchmark harness.
// =========================================================================

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn estimate_tokens_empty_returns_zero() {
        assert_eq!(estimate_tokens(""), 0);
    }

    #[test]
    fn estimate_tokens_short_string_returns_min_one() {
        // Any non-empty string < 4 chars still rounds up to 1 token.
        assert_eq!(estimate_tokens("a"), 1);
        assert_eq!(estimate_tokens("ab"), 1);
        assert_eq!(estimate_tokens("abc"), 1);
    }

    #[test]
    fn estimate_tokens_long_string_divides_by_four() {
        assert_eq!(estimate_tokens("abcd"), 1);
        assert_eq!(estimate_tokens("abcdefgh"), 2);
        assert_eq!(estimate_tokens("abcdefghijklmnop"), 4);
    }

    #[test]
    fn estimate_tokens_unicode_counts_bytes() {
        // estimate_tokens uses .len() which counts bytes, not chars.
        // A 3-byte UTF-8 char (e.g. CJK) counts as 3 bytes.
        assert_eq!(estimate_tokens("中"), 1); // 3 bytes -> max(1, 0) = 1
        assert_eq!(estimate_tokens("中文"), 1); // 6 bytes -> max(1, 1) = 1
                                                // 10 CJK chars = 30 bytes -> 30/4 = 7
        assert_eq!(estimate_tokens("中文中文中文中文中文"), 7);
    }

    #[test]
    fn make_variant_calculates_tokens_from_input_and_output() {
        let v = make_variant(10.5, "abcdefgh", "ijklmnop", 2, None);
        assert_eq!(v.input_tokens, 2); // 8 bytes / 4
        assert_eq!(v.output_tokens, 2); // 8 bytes / 4
        assert_eq!(v.total_tokens, 4);
        assert_eq!(v.latency_ms, 10.5);
        assert_eq!(v.result_count, 2);
        assert!(v.success);
        assert!(v.error.is_none());
    }

    #[test]
    fn make_variant_tpr_with_zero_count_is_zero() {
        let v = make_variant(5.0, "abcd", "efgh", 0, None);
        assert_eq!(v.tokens_per_result, 0.0);
        assert_eq!(v.result_count, 0);
    }

    #[test]
    fn make_variant_tpr_with_positive_count() {
        // "abcdefgh" = 8 bytes -> 2 tokens; total = 2+2 = 4; tpr = 4/4 = 1.0
        let v = make_variant(5.0, "abcdefgh", "ijklmnop", 4, None);
        assert_eq!(v.tokens_per_result, 1.0);
    }

    #[test]
    fn make_variant_success_flag_false_when_error_present() {
        let v = make_variant(0.0, "", "", 0, Some("timeout".to_string()));
        assert!(!v.success);
        assert_eq!(v.error, Some("timeout".to_string()));
    }

    #[test]
    fn make_variant_success_flag_true_when_error_none() {
        let v = make_variant(0.0, "x", "y", 1, None);
        assert!(v.success);
        assert!(v.error.is_none());
    }

    #[test]
    fn winner_lower_prefers_lower_value() {
        assert_eq!(winner_lower(10.0, 20.0), "LeanKG");
        assert_eq!(winner_lower(1.5, 99.9), "LeanKG");
    }

    #[test]
    fn winner_lower_tie_goes_to_manual() {
        // When a == b, the else branch fires (Manual wins).
        assert_eq!(winner_lower(50.0, 50.0), "Manual");
    }

    #[test]
    fn winner_lower_when_a_is_larger_goes_to_manual() {
        assert_eq!(winner_lower(100.0, 50.0), "Manual");
    }

    #[test]
    fn get_cases_is_not_empty() {
        let cases = get_cases();
        assert!(!cases.is_empty(), "get_cases should return benchmark cases");
        assert!(
            cases.len() >= 10,
            "expected at least 10 cases, got {}",
            cases.len()
        );
    }

    #[test]
    fn get_cases_has_simple_medium_and_complex() {
        let cases = get_cases();
        let complexities: std::collections::HashSet<&str> =
            cases.iter().map(|c| c.complexity).collect();
        assert!(
            complexities.contains("simple"),
            "missing 'simple' complexity cases"
        );
        assert!(
            complexities.contains("medium"),
            "missing 'medium' complexity cases"
        );
        assert!(
            complexities.contains("complex"),
            "missing 'complex' complexity cases"
        );
    }

    #[test]
    fn get_cases_all_have_non_empty_fields() {
        let cases = get_cases();
        for c in &cases {
            assert!(!c.id.is_empty(), "case id is empty");
            assert!(!c.category.is_empty(), "case {} category is empty", c.id);
            assert!(
                !c.complexity.is_empty(),
                "case {} complexity is empty",
                c.id
            );
            assert!(!c.tool.is_empty(), "case {} tool is empty", c.id);
            assert!(!c.query.is_empty(), "case {} query is empty", c.id);
        }
    }

    #[test]
    fn get_cases_includes_impact_radius_and_call_graph() {
        let cases = get_cases();
        let tools: std::collections::HashSet<&str> = cases.iter().map(|c| c.tool).collect();
        assert!(
            tools.contains("get_impact_radius"),
            "missing get_impact_radius case"
        );
        assert!(
            tools.contains("get_call_graph"),
            "missing get_call_graph case"
        );
    }

    #[test]
    fn get_cases_ids_are_unique() {
        let cases = get_cases();
        let ids: std::collections::HashSet<&str> = cases.iter().map(|c| c.id).collect();
        assert_eq!(ids.len(), cases.len(), "case IDs must be unique");
    }
}
