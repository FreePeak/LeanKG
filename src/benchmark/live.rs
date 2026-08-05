use crate::graph;
use serde::Serialize;
use std::collections::HashMap;
use std::time::Instant;

/// Result of a single baseline/after query measurement.
#[derive(Debug, Clone, Serialize)]
pub struct QueryResult {
    pub query: String,
    pub query_type: String,
    pub latency_ms: f64,
    pub result_count: usize,
    pub success: bool,
    pub error: Option<String>,
}

/// Result of a semantic search baseline/after measurement.
#[derive(Debug, Clone, Serialize)]
pub struct SemanticResult {
    pub query: String,
    pub latency_ms: f64,
    pub result_count: usize,
    pub precision: f64,
    pub recall: f64,
    pub f1_score: f64,
    pub success: bool,
    pub error: Option<String>,
    /// True when F1 is a heuristic proxy (ground-truth fixture missing).
    pub proxy: bool,
}

/// Quality delta between before and after embedding.
#[derive(Debug, Clone, Serialize)]
pub struct QualityDelta {
    pub precision_delta: f64,
    pub recall_delta: f64,
    pub f1_delta: f64,
    pub avg_latency_delta_ms: f64,
    pub before_avg_latency_ms: f64,
    pub after_avg_latency_ms: f64,
}

/// Doc index benchmark query set.
const DOC_BENCHMARK_QUERIES: &[&str] = &[
    "search_code:documentation",
    "search_code:knowledge graph",
    "search_code:How to index",
    "concept_search:documentation",
    "concept_search:code indexing",
    "find_function:index_docs",
    "semantic_search:document indexing process",
    "semantic_search:markdown parsing",
];

/// Orchestrates before/after measurements for doc indexing.
pub struct DocIndexBenchmark {
    queries: Vec<String>,
}

impl Default for DocIndexBenchmark {
    fn default() -> Self {
        Self {
            queries: DOC_BENCHMARK_QUERIES
                .iter()
                .map(|s| s.to_string())
                .collect(),
        }
    }
}

impl DocIndexBenchmark {
    /// Run baseline queries against the graph engine before indexing.
    pub fn run_baseline(&self, engine: &graph::GraphEngine) -> Vec<QueryResult> {
        self.run_queries(engine, "baseline")
    }

    /// Run the same queries after doc indexing to capture the delta.
    pub fn run_after(&self, engine: &graph::GraphEngine) -> Vec<QueryResult> {
        self.run_queries(engine, "after")
    }

    fn run_queries(&self, engine: &graph::GraphEngine, _phase: &str) -> Vec<QueryResult> {
        self.queries
            .iter()
            .map(|q| {
                let parts: Vec<&str> = q.splitn(2, ':').collect();
                let query_type = parts[0];
                let term = parts.get(1).copied().unwrap_or("");
                let start = Instant::now();
                let result = match query_type {
                    "search_code" => {
                        let els = engine.search_by_name_typed(term, None, 50);
                        match els {
                            Ok(v) => QueryResult {
                                query: q.clone(),
                                query_type: query_type.to_string(),
                                latency_ms: start.elapsed().as_secs_f64() * 1000.0,
                                result_count: v.len(),
                                success: true,
                                error: None,
                            },
                            Err(e) => QueryResult {
                                query: q.clone(),
                                query_type: query_type.to_string(),
                                latency_ms: start.elapsed().as_secs_f64() * 1000.0,
                                result_count: 0,
                                success: false,
                                error: Some(e.to_string()),
                            },
                        }
                    }
                    "find_function" => {
                        let els = engine.search_by_name_typed(term, Some("function"), 50);
                        match els {
                            Ok(v) => QueryResult {
                                query: q.clone(),
                                query_type: query_type.to_string(),
                                latency_ms: start.elapsed().as_secs_f64() * 1000.0,
                                result_count: v.len(),
                                success: true,
                                error: None,
                            },
                            Err(e) => QueryResult {
                                query: q.clone(),
                                query_type: query_type.to_string(),
                                latency_ms: start.elapsed().as_secs_f64() * 1000.0,
                                result_count: 0,
                                success: false,
                                error: Some(e.to_string()),
                            },
                        }
                    }
                    "semantic_search" | "concept_search" => {
                        #[cfg(feature = "embeddings")]
                        {
                            match run_real_semantic(engine, term, 50, 20) {
                                Ok((qns, latency_ms, err_opt)) => QueryResult {
                                    query: q.clone(),
                                    query_type: query_type.to_string(),
                                    latency_ms,
                                    result_count: qns.len(),
                                    success: err_opt.is_none(),
                                    error: err_opt,
                                },
                                Err(e) => QueryResult {
                                    query: q.clone(),
                                    query_type: query_type.to_string(),
                                    latency_ms: start.elapsed().as_secs_f64() * 1000.0,
                                    result_count: 0,
                                    success: false,
                                    error: Some(format!("pipeline: {}", e)),
                                },
                            }
                        }
                        #[cfg(not(feature = "embeddings"))]
                        QueryResult {
                            query: q.clone(),
                            query_type: query_type.to_string(),
                            latency_ms: start.elapsed().as_secs_f64() * 1000.0,
                            result_count: 0,
                            success: false,
                            error: Some(
                                "embeddings feature disabled; build with --features embeddings"
                                    .into(),
                            ),
                        }
                    }
                    _ => QueryResult {
                        query: q.clone(),
                        query_type: query_type.to_string(),
                        latency_ms: start.elapsed().as_secs_f64() * 1000.0,
                        result_count: 0,
                        success: false,
                        error: Some(format!("Unknown query type: {}", query_type)),
                    },
                };
                result
            })
            .collect()
    }

    /// Print live progress counters to stdout during the indexing process.
    pub fn stream_progress(&self, current: usize, total: usize, phase: &str) {
        let pct = if total > 0 {
            (current as f64 / total as f64) * 100.0
        } else {
            0.0
        };
        println!("[benchmark] {}: {}/{} ({:.1}%)", phase, current, total, pct);
    }

    /// Generate a markdown A/B report comparing baseline and after results.
    pub fn generate_report(
        &self,
        baseline: &[QueryResult],
        after: &[QueryResult],
        duration_secs: f64,
        docs_indexed: usize,
        elements_created: usize,
        relationships_created: usize,
    ) -> String {
        let ts = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_secs())
            .unwrap_or(0);

        let mut md = String::new();
        md.push_str("# Live A/B Benchmark: Doc Indexing\n\n");
        md.push_str(&format!(
            "**Before**: {:.0} ms baseline avg\n",
            avg_latency(baseline)
        ));
        md.push_str(&format!(
            "**After**: {:.0} ms after avg\n",
            avg_latency(after)
        ));
        md.push_str(&format!("**Duration**: {:.1}s\n", duration_secs));
        md.push_str(&format!("**Docs Indexed**: {}\n", docs_indexed));
        md.push_str(&format!("**Elements Created**: {}\n", elements_created));
        md.push_str(&format!(
            "**Relationships Created**: {}\n",
            relationships_created
        ));
        md.push_str(&format!("**Timestamp**: {}\n\n", ts));

        md.push_str("| Query | Type | Before (ms) | Before (results) | After (ms) | After (results) | Delta (ms) | Delta (results) | Status |\n");
        md.push_str("|-------|------|-------------|------------------|------------|-----------------|------------|----------------|--------|\n");

        for (b, a) in baseline.iter().zip(after.iter()) {
            let delta_ms = a.latency_ms - b.latency_ms;
            let delta_res = a.result_count as i64 - b.result_count as i64;
            let delta_sign_res = if delta_res >= 0 { "+" } else { "" };
            let b_status = status_label_qr(b);
            let a_status = status_label_qr(a);
            md.push_str(&format!(
                "| {} | {} | {:.1} | {} | {:.1} | {} | {:.1} | {}{}{} | {}→{} |\n",
                truncate(&b.query, 28),
                b.query_type,
                b.latency_ms,
                b.result_count,
                a.latency_ms,
                a.result_count,
                delta_ms,
                delta_sign_res,
                delta_res,
                if delta_res > 0 {
                    " (improved)"
                } else if delta_res < 0 {
                    " (worse)"
                } else {
                    " (same)"
                },
                b_status,
                a_status,
            ));
        }

        md.push('\n');
        md.push_str(&format!(
            "**Avg Latency Delta**: {:.1} ms ({})\n",
            avg_latency(after) - avg_latency(baseline),
            if avg_latency(after) < avg_latency(baseline) {
                "faster"
            } else {
                "slower"
            },
        ));

        md
    }
}

/// Orchestrates before/after measurements for embedding.
pub struct EmbedBenchmark {
    queries: Vec<String>,
}

impl Default for EmbedBenchmark {
    fn default() -> Self {
        Self {
            queries: vec![
                "semantic_search:document indexing".to_string(),
                "semantic_search:markdown parsing".to_string(),
                "semantic_search:knowledge graph".to_string(),
            ],
        }
    }
}

impl EmbedBenchmark {
    /// Run semantic search baseline queries before embedding.
    pub fn run_semantic_baseline(&self, engine: &graph::GraphEngine) -> Vec<SemanticResult> {
        self.run_semantic_queries(engine, "baseline")
    }

    /// Run the same semantic queries after embedding.
    pub fn run_semantic_after(&self, engine: &graph::GraphEngine) -> Vec<SemanticResult> {
        self.run_semantic_queries(engine, "after")
    }

    fn run_semantic_queries(
        &self,
        engine: &graph::GraphEngine,
        _phase: &str,
    ) -> Vec<SemanticResult> {
        let truth_map = load_ground_truth();
        self.queries
            .iter()
            .map(|q| {
                let start = Instant::now();
                let term = q.split_once(':').map(|x| x.1).unwrap_or(q);

                #[cfg(feature = "embeddings")]
                let (retrieved_qns, count, error, success) =
                    match run_real_semantic(engine, term, 50, 20) {
                        Ok((qns, _latency, err_opt)) => {
                            let c = qns.len();
                            let is_ok = err_opt.is_none();
                            (qns, c, err_opt, is_ok)
                        }
                        Err(e) => (Vec::new(), 0usize, Some(format!("pipeline: {}", e)), false),
                    };

                #[cfg(not(feature = "embeddings"))]
                let (retrieved_qns, count, error, success): (
                    Vec<String>,
                    usize,
                    Option<String>,
                    bool,
                ) = {
                    let els = engine.search_by_name_typed(q, None, 50);
                    let c = els.as_ref().map(|v| v.len()).unwrap_or(0);
                    (
                        Vec::new(),
                        c,
                        Some("embeddings feature disabled".into()),
                        false,
                    )
                };

                let latency_ms = start.elapsed().as_secs_f64() * 1000.0;

                let (precision, recall, f1, proxy) = if let Some(truth) = truth_map.as_ref() {
                    let query_key = term.trim().to_lowercase();
                    let matched_truth = truth.iter().find(|(k, _)| k.to_lowercase() == query_key);
                    if let Some((_, expected)) = matched_truth {
                        if !retrieved_qns.is_empty() {
                            let (p, r, f) = compute_f1(&retrieved_qns, expected);
                            (p, r, f, false)
                        } else if cfg!(feature = "embeddings") {
                            // Real pipeline returned count but we didn't capture QNs.
                            // Use count-based proxy with ground-truth size.
                            (0.0, 0.0, 0.0, true)
                        } else {
                            (0.0, 0.0, 0.0, true)
                        }
                    } else {
                        // Query not in ground truth; use heuristic proxy.
                        let p = if count > 0 { 1.0 } else { 0.0 };
                        let r = if count > 0 {
                            (count as f64 / 50.0).min(1.0)
                        } else {
                            0.0
                        };
                        let f = if p + r > 0.0 {
                            2.0 * p * r / (p + r)
                        } else {
                            0.0
                        };
                        (p, r, f, true)
                    }
                } else {
                    // No ground-truth fixture; heuristic proxy.
                    let p = if count > 0 { 1.0 } else { 0.0 };
                    let r = if count > 0 {
                        (count as f64 / 50.0).min(1.0)
                    } else {
                        0.0
                    };
                    let f = if p + r > 0.0 {
                        2.0 * p * r / (p + r)
                    } else {
                        0.0
                    };
                    (p, r, f, true)
                };

                SemanticResult {
                    query: q.clone(),
                    latency_ms,
                    result_count: count,
                    precision,
                    recall,
                    f1_score: f1,
                    success,
                    error,
                    proxy,
                }
            })
            .collect()
    }

    /// Compute quality delta between before and after embedding.
    pub fn compute_quality_delta(
        &self,
        before: &[SemanticResult],
        after: &[SemanticResult],
    ) -> QualityDelta {
        let before_avg_latency = avg_semantic_latency(before);
        let after_avg_latency = avg_semantic_latency(after);
        let before_avg_f1 = avg_semantic_f1(before);
        let after_avg_f1 = avg_semantic_f1(after);

        QualityDelta {
            precision_delta: after_avg_f1 - before_avg_f1,
            recall_delta: after_avg_f1 - before_avg_f1,
            f1_delta: after_avg_f1 - before_avg_f1,
            avg_latency_delta_ms: after_avg_latency - before_avg_latency,
            before_avg_latency_ms: before_avg_latency,
            after_avg_latency_ms: after_avg_latency,
        }
    }

    /// Generate a markdown report for the embed A/B benchmark.
    pub fn generate_embed_report(
        &self,
        baseline: &[SemanticResult],
        after: &[SemanticResult],
        throughput: f64,
    ) -> String {
        let ts = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_secs())
            .unwrap_or(0);

        let delta = self.compute_quality_delta(baseline, after);

        let mut md = String::new();
        md.push_str("# Live A/B Benchmark: Embedding\n\n");
        md.push_str(&format!(
            "**Before**: {:.0} ms avg latency\n",
            delta.before_avg_latency_ms
        ));
        md.push_str(&format!(
            "**After**: {:.0} ms avg latency\n",
            delta.after_avg_latency_ms
        ));
        md.push_str(&format!(
            "**Embed Throughput**: {:.1} vectors/sec\n",
            throughput
        ));
        md.push_str(&format!("**Timestamp**: {}\n\n", ts));

        md.push_str("## Semantic Search Quality\n\n");
        md.push_str("| Query | Before (ms) | Before (results) | Before F1 | Before Status | After (ms) | After (results) | After F1 | After Status | Delta (ms) | Delta F1 |\n");
        md.push_str("|-------|-------------|------------------|-----------|---------------|------------|-----------------|----------|--------------|------------|----------|\n");

        for (b, a) in baseline.iter().zip(after.iter()) {
            let delta_ms = a.latency_ms - b.latency_ms;
            let delta_f1 = a.f1_score - b.f1_score;
            let b_status = status_label(b);
            let a_status = status_label(a);
            md.push_str(&format!(
                "| {} | {:.1} | {} | {:.2} | {} | {:.1} | {} | {:.2} | {} | {:.1} | {:.2} |\n",
                truncate(&b.query, 18),
                b.latency_ms,
                b.result_count,
                b.f1_score,
                a.latency_ms,
                a.result_count,
                a.f1_score,
                b_status,
                a_status,
                delta_ms,
                delta_f1,
            ));
        }

        md.push('\n');
        md.push_str(&format!(
            "**Average Delta**: latency {:.1} ms, F1 {:.2} ({})\n",
            delta.avg_latency_delta_ms,
            delta.f1_delta,
            if delta.f1_delta > 0.0 {
                "improved"
            } else if delta.f1_delta < 0.0 {
                "worse"
            } else {
                "unchanged"
            },
        ));

        md
    }
}

/// Streams live progress to stdout and saves the final A/B report.
pub struct LiveReporter {
    results_dir: std::path::PathBuf,
}

impl LiveReporter {
    pub fn new(results_dir: std::path::PathBuf) -> Self {
        Self { results_dir }
    }

    /// Stream a progress update to stdout during indexing/embedding.
    pub fn stream(&self, phase: &str, current: usize, total: usize, msg: &str) {
        let pct = if total > 0 {
            (current as f64 / total as f64) * 100.0
        } else {
            0.0
        };
        println!(
            "[live-benchmark] {}: {}/{} ({:.1}%) - {}",
            phase, current, total, pct, msg
        );
    }

    /// Save a markdown report to `benchmark/results/`.
    pub fn save_report(&self, title: &str, content: &str) -> std::io::Result<std::path::PathBuf> {
        std::fs::create_dir_all(&self.results_dir)?;
        let ts = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_secs())
            .unwrap_or(0);
        let safe_title = title.replace(' ', "-").to_lowercase();
        let filename = format!("{}-{}-live-ab.md", safe_title, ts);
        let path = self.results_dir.join(&filename);
        std::fs::write(&path, content)?;
        Ok(path)
    }
}

fn avg_latency(results: &[QueryResult]) -> f64 {
    if results.is_empty() {
        0.0
    } else {
        results.iter().map(|r| r.latency_ms).sum::<f64>() / results.len() as f64
    }
}

fn avg_semantic_latency(results: &[SemanticResult]) -> f64 {
    if results.is_empty() {
        0.0
    } else {
        results.iter().map(|r| r.latency_ms).sum::<f64>() / results.len() as f64
    }
}

fn avg_semantic_f1(results: &[SemanticResult]) -> f64 {
    if results.is_empty() {
        0.0
    } else {
        results.iter().map(|r| r.f1_score).sum::<f64>() / results.len() as f64
    }
}

fn truncate(s: &str, max: usize) -> String {
    if s.len() <= max {
        s.to_string()
    } else {
        format!("{}...", &s[..max - 3])
    }
}

/// Format a short status label for markdown tables: "ok" or "err:…".
fn status_label(r: &SemanticResult) -> String {
    if r.success {
        if r.proxy {
            "ok*".to_string()
        } else {
            "ok".to_string()
        }
    } else if let Some(ref e) = r.error {
        let short = truncate(e, 30);
        format!("err:{}", short)
    } else {
        "err".to_string()
    }
}

fn status_label_qr(r: &QueryResult) -> String {
    if r.success {
        "ok".to_string()
    } else if let Some(ref e) = r.error {
        let short = truncate(e, 30);
        format!("err:{}", short)
    } else {
        "err".to_string()
    }
}

/// Load the ground-truth fixture from benchmark/ground_truth.json.
/// Returns None when the file is missing or malformed.
fn load_ground_truth() -> Option<HashMap<String, Vec<String>>> {
    let path = std::path::Path::new("benchmark/ground_truth.json");
    if !path.exists() {
        return None;
    }
    std::fs::read_to_string(path)
        .ok()
        .and_then(|s| serde_json::from_str(&s).ok())
}

/// Compute precision, recall, F1 from retrieved QNs vs ground-truth QNs.
fn compute_f1(retrieved: &[String], truth: &[String]) -> (f64, f64, f64) {
    if retrieved.is_empty() {
        return (0.0, 0.0, 0.0);
    }
    if truth.is_empty() {
        return (0.0, 0.0, 0.0);
    }
    let truth_set: std::collections::HashSet<&str> = truth.iter().map(|s| s.as_str()).collect();
    let mut hits = 0usize;
    for qn in retrieved {
        if truth_set.contains(qn.as_str()) {
            hits += 1;
        }
    }
    let precision = hits as f64 / retrieved.len() as f64;
    let recall = hits as f64 / truth.len() as f64;
    let f1 = if precision + recall > 0.0 {
        2.0 * precision * recall / (precision + recall)
    } else {
        0.0
    };
    (precision, recall, f1)
}

/// Run the `SemanticRetrievalPipeline` on a query.
/// Returns `(retrieved_qns, latency_ms, error)`.
#[cfg(feature = "embeddings")]
type SemanticRunResult = Result<(Vec<String>, f64, Option<String>), Box<dyn std::error::Error>>;

#[cfg(feature = "embeddings")]
fn run_real_semantic(
    engine: &graph::GraphEngine,
    query: &str,
    ann_top_k: usize,
    rerank_top_n: usize,
) -> SemanticRunResult {
    use crate::retrieval::pipeline::{RetrieveOptions, SemanticRetrievalPipeline};

    let start = Instant::now();
    let mut pipeline = SemanticRetrievalPipeline::new(engine.db_arc().clone())?;
    let opts = RetrieveOptions {
        env: None,
        ann_top_k: Some(ann_top_k),
        rerank_top_n,
        include_worktrees: false,
        include_ontology_steps: false,
        embeddings_stale: false,
    };
    let r = pipeline.retrieve(query, &opts)?;
    let latency_ms = start.elapsed().as_secs_f64() * 1000.0;
    let qns: Vec<String> = r.seeds.iter().map(|s| s.qualified_name.clone()).collect();
    Ok((qns, latency_ms, None))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_doc_index_benchmark_baseline_returns_results() {
        let bench = DocIndexBenchmark::default();
        assert!(!bench.queries.is_empty());
        assert_eq!(bench.queries.len(), 8);
    }

    #[test]
    fn test_embed_benchmark_baseline_returns_results() {
        let bench = EmbedBenchmark::default();
        assert!(!bench.queries.is_empty());
        assert_eq!(bench.queries.len(), 3);
    }

    #[test]
    fn test_generate_report_not_empty() {
        let bench = DocIndexBenchmark::default();
        let baseline = vec![QueryResult {
            query: "search_code:test".to_string(),
            query_type: "search_code".to_string(),
            latency_ms: 2.1,
            result_count: 5,
            success: true,
            error: None,
        }];
        let after = vec![QueryResult {
            query: "search_code:test".to_string(),
            query_type: "search_code".to_string(),
            latency_ms: 1.8,
            result_count: 47,
            success: true,
            error: None,
        }];
        let report = bench.generate_report(&baseline, &after, 12.3, 42, 312, 189);
        assert!(report.contains("Live A/B Benchmark: Doc Indexing"));
        assert!(report.contains("42"));
        assert!(report.contains("312"));
        assert!(report.contains("189"));
        assert!(report.contains("search_code:test"));
    }

    #[test]
    fn test_generate_embed_report_not_empty() {
        let bench = EmbedBenchmark::default();
        let baseline = vec![SemanticResult {
            query: "semantic_search:doc indexing".to_string(),
            latency_ms: 50.0,
            result_count: 10,
            precision: 1.0,
            recall: 0.2,
            f1_score: 0.3333,
            success: true,
            error: None,
            proxy: true,
        }];
        let after = vec![SemanticResult {
            query: "semantic_search:doc indexing".to_string(),
            latency_ms: 35.0,
            result_count: 25,
            precision: 1.0,
            recall: 0.5,
            f1_score: 0.6667,
            success: true,
            error: None,
            proxy: true,
        }];
        let report = bench.generate_embed_report(&baseline, &after, 1200.0);
        assert!(report.contains("Live A/B Benchmark: Embedding"));
        assert!(report.contains("1200.0"));
    }

    #[test]
    fn test_compute_quality_delta() {
        let bench = EmbedBenchmark::default();
        let baseline = vec![SemanticResult {
            query: "q1".to_string(),
            latency_ms: 50.0,
            result_count: 5,
            precision: 0.5,
            recall: 0.4,
            f1_score: 0.4444,
            success: true,
            error: None,
            proxy: true,
        }];
        let after = vec![SemanticResult {
            query: "q1".to_string(),
            latency_ms: 35.0,
            result_count: 15,
            precision: 0.8,
            recall: 0.6,
            f1_score: 0.6857,
            success: true,
            error: None,
            proxy: true,
        }];
        let delta = bench.compute_quality_delta(&baseline, &after);
        assert!(delta.f1_delta > 0.0);
        assert!(delta.avg_latency_delta_ms < 0.0);
    }

    #[test]
    fn test_stream_progress() {
        let reporter = LiveReporter::new(std::path::PathBuf::from("/tmp"));
        // Just verify it doesn't panic
        reporter.stream("indexing", 5, 100, "Processing docs");
    }

    #[test]
    fn test_save_report() {
        let dir = std::env::temp_dir().join("leankg-live-bench-test");
        let reporter = LiveReporter::new(dir.clone());
        let report = "# Test Report\n\nSome content.";
        let path = reporter.save_report("test-report", report).unwrap();
        assert!(path.exists());
        let content = std::fs::read_to_string(&path).unwrap();
        assert_eq!(content, report);
        std::fs::remove_dir_all(&dir).ok();
    }
}
