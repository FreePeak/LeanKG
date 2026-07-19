//! Bench: overhead of complementary MCP tool paths (v3.7.4 audit).
//!
//! Goal: prove that the **deeper** tool is never significantly slower than
//! the shallower one for the common case, so complementary pairs in
//! `tests/redundant_tools_matrix.rs` remain sound after FR-SURF-03 removals.
//!
//! Run:
//! ```bash
//! cargo bench --release --bench redundant_tool_overhead
//! ```
//!
//! Tools compared:
//! - `get_architecture` vs `get_overview_context` (deep vs shallow)
//!
//! Each pair is a small, deterministic handler-call so the bench stays
//! hermetic (no MCP HTTP, no embedder). The headline number is wall time.

use criterion::{criterion_group, criterion_main, Criterion};
use leankg::db::schema::{init_db, run_script};
use leankg::graph::GraphEngine;
use leankg::mcp::handler::ToolHandler;
use serde_json::json;
use tempfile::TempDir;

fn fixture_db() -> (ToolHandler, TempDir) {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(&db_path).unwrap();
    let seed = r#"
        ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <-
        [
            ["src/a.rs", "file", "a.rs", "src/a.rs", 1, 100, "rust", "", "c1", "core", "{}", "local", "procedural"],
            ["src/a.rs::alpha", "function", "alpha", "src/a.rs", 1, 10, "rust", "src/a.rs", "c1", "core", "{}", "local", "procedural"],
            ["src/a.rs::beta",  "function", "beta",  "src/a.rs", 11, 20, "rust", "src/a.rs", "c1", "core", "{}", "local", "procedural"]
        ]
        :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}
    "#;
    run_script(&db, seed, Default::default()).unwrap();
    let graph = GraphEngine::new(db);
    (ToolHandler::new(graph, db_path), tmp)
}

fn run_sync(handler: &ToolHandler, tool: &str, args: serde_json::Value) -> serde_json::Value {
    let rt = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
        .unwrap();
    rt.block_on(handler.execute_tool(tool, &args)).unwrap()
}

fn bench_overview_pair(c: &mut Criterion) {
    let (handler, _tmp) = fixture_db();

    c.bench_function("get_overview_context_shallow", |b| {
        b.iter(|| run_sync(&handler, "get_overview_context", json!({})))
    });

    c.bench_function("get_architecture_deep", |b| {
        b.iter(|| run_sync(&handler, "get_architecture", json!({})))
    });
}

criterion_group!(benches, bench_overview_pair);
criterion_main!(benches);
