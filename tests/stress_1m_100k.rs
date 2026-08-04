//! 1M elements + 100K embed-row stress test. **NOT IN CI.**
//!
//! Opt-in only:
//! ```bash
//! cargo test --release --test stress_1m_100k -- --ignored --nocapture
//! ```
//!
//! Why `#[ignore]`: this materializes 1,000,000 `CodeElement` rows and
//! 100,000 `embedding_state` rows in a temp RocksDB. On CI runners with
//! 7 GB RAM it OOMs; on the host Mac it takes minutes. The default
//! `cargo test --release --lib` must stay green.
//!
//! What it measures (printed, not asserted):
//! - Index throughput: rows/sec for `insert_elements_with(bulk=true)` over 1M rows.
//! - Embed-row ingest: rows/sec for 100K rows into a stub `embeddings_pending`
//!   relation (no real fastembed model — the data plane is the bottleneck
//!   we're sizing, not the ONNX runtime).
//! - Latency summary (p50 / p95 / p99) of the per-batch `insert_elements`
//!   call across 100 batches of 10K elements.
//!
//! `LEANKG_STRESS_N` and `LEANKG_STRESS_EMBED_N` override the defaults for
//! quick smoke runs (e.g. `LEANKG_STRESS_N=10000 LEANKG_STRESS_EMBED_N=1000`).

use std::path::PathBuf;
use std::time::Instant;

use leankg::db::models::CodeElement;
use leankg::db::schema;
use leankg::graph::GraphEngine;

fn n_env(name: &str, default: usize) -> usize {
    std::env::var(name)
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(default)
}

fn percentile(sorted_ms: &[u128], p: f64) -> u128 {
    if sorted_ms.is_empty() {
        return 0;
    }
    let idx = ((sorted_ms.len() as f64 - 1.0) * p).round() as usize;
    sorted_ms[idx.min(sorted_ms.len() - 1)]
}

fn synth_element(i: usize) -> CodeElement {
    CodeElement {
        qualified_name: format!("src/syn::item_{i}"),
        element_type: "function".into(),
        name: format!("item_{i}"),
        file_path: format!("src/syn/item_{}.rs", i / 100),
        line_start: ((i % 1000) + 1) as u32,
        line_end: ((i % 1000) + 5) as u32,
        language: "rust".into(),
        parent_qualified: None,
        cluster_id: None,
        cluster_label: None,
        metadata: serde_json::json!({"stress": true}),
        env: "local".into(),
    }
}

fn ensure_pending_table(
    graph: &GraphEngine,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    graph.run_raw_query(
        ":create embeddings_pending {qn: String => created_at: Int}",
        std::collections::BTreeMap::new(),
    )?;
    Ok(())
}

#[test]
#[ignore]
fn stress_1m_elements_100k_embed_rows() {
    let n = n_env("LEANKG_STRESS_N", 1_000_000);
    let embed_n = n_env("LEANKG_STRESS_EMBED_N", 100_000);
    let batch = 10_000usize;

    let tmp = tempfile::tempdir().expect("tempdir");
    let db_path: PathBuf = tmp.path().join(".leankg");
    let db = schema::init_db(&db_path).expect("init_db");
    let graph = GraphEngine::new(std::sync::Arc::new(
        leankg::db::backend::CozoBackend::from_concrete(db.clone()),
    ));
    ensure_pending_table(&graph).expect("create pending table");

    eprintln!("[stress] indexing {n} elements in batches of {batch}");
    let total_start = Instant::now();
    let mut batch_latencies_ms: Vec<u128> = Vec::new();
    let mut inserted = 0usize;
    while inserted < n {
        let end = (inserted + batch).min(n);
        let chunk: Vec<CodeElement> = (inserted..end).map(synth_element).collect();
        let t = Instant::now();
        graph
            .insert_elements_with(&chunk, true)
            .expect("insert batch");
        let ms = t.elapsed().as_millis();
        batch_latencies_ms.push(ms);
        inserted = end;
    }
    let total_index_secs = total_start.elapsed().as_secs_f64();
    let index_vps = n as f64 / total_index_secs;
    eprintln!("[stress] indexed {n} elements in {total_index_secs:.2}s ({index_vps:.0} rows/s)");
    batch_latencies_ms.sort_unstable();
    eprintln!(
        "[stress] per-batch latency (10K rows) — p50 {} ms, p95 {} ms, p99 {} ms, max {} ms",
        percentile(&batch_latencies_ms, 0.50),
        percentile(&batch_latencies_ms, 0.95),
        percentile(&batch_latencies_ms, 0.99),
        batch_latencies_ms.last().copied().unwrap_or(0)
    );

    eprintln!("[stress] staging {embed_n} embed rows into embeddings_pending");
    let t_embed = Instant::now();
    let mut stmts = 0usize;
    let mut p = 0usize;
    while p < embed_n {
        let end = (p + 5_000).min(embed_n);
        let mut args: Vec<serde_json::Value> = Vec::with_capacity(end - p);
        for i in p..end {
            args.push(serde_json::json!([format!("src/syn::item_{i}"), 0]));
        }
        let mut params = std::collections::BTreeMap::new();
        params.insert("rows".to_string(), serde_json::Value::Array(args));
        graph
            .run_raw_query(
                "?[qn, created_at] <- $rows :put embeddings_pending {qn => created_at}",
                params,
            )
            .expect("embed insert");
        stmts += 1;
        p = end;
    }
    let embed_secs = t_embed.elapsed().as_secs_f64();
    let embed_vps = embed_n as f64 / embed_secs;
    eprintln!(
        "[stress] staged {embed_n} embed rows in {embed_secs:.2}s ({embed_vps:.0} rows/s, {} :put stmts)",
        stmts
    );

    // Read-back: full-table read simulates a real "list pending embeddings" sweep.
    let t_read = Instant::now();
    graph
        .run_raw_query(
            "?[qn, created_at] := *embeddings_pending{qn, created_at}",
            std::collections::BTreeMap::new(),
        )
        .expect("readback");
    let read_secs = t_read.elapsed().as_secs_f64();
    eprintln!("[stress] full-table readback: {read_secs:.2}s");

    eprintln!("[stress] done");
}
