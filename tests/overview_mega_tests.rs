//! TDD red tests for `wake_up_summary` / `identity_context` /
//! `critical_facts_context` on large graphs.
//!
//! Bug: all three pull the full element+relationship set into memory via the
//! deprecated `all_elements()` / `all_relationships()` calls. On the workspace-be
//! mega-graph (721k elements, 2.3M rels) this exceeds
//! `LEANKG_MCP_TOOL_TIMEOUT_SECS` and the MCP tool returns -32001 Request timed
//! out. Fix: replace the bulk pulls with bounded paginated / aggregate queries
//! (counts + top-N sample).
//!
//! Seam: the three methods must succeed and return non-empty markdown for any
//! graph size, completing in well under the MCP tool timeout.

use leankg::db::models::CodeElement;
use leankg::graph::GraphEngine;
use std::env;
use std::fs;
use std::path::PathBuf;
use std::sync::atomic::{AtomicU32, Ordering};
use std::time::Instant;

static DB_COUNTER: AtomicU32 = AtomicU32::new(0);

fn fresh_db() -> PathBuf {
    let n = DB_COUNTER.fetch_add(1, Ordering::SeqCst);
    let path = env::temp_dir().join(format!("leankg_overview_mega_{}.db", n));
    let _ = fs::remove_file(&path);
    path
}

fn cleanup(p: &PathBuf) {
    let _ = fs::remove_file(p);
}

fn make_element(qn: &str, et: &str, name: &str, fp: &str, lang: &str) -> CodeElement {
    CodeElement {
        qualified_name: qn.to_string(),
        element_type: et.to_string(),
        name: name.to_string(),
        file_path: fp.to_string(),
        line_start: 1,
        line_end: 1,
        language: lang.to_string(),
        parent_qualified: None,
        cluster_id: None,
        cluster_label: None,
        metadata: serde_json::json!({}),
        env: "local".to_string(),
    }
}

/// Seed a graph with `n` elements spanning the type categories the three
/// overview methods aggregate over. Keep the per-row payload cheap so the test
/// runs in seconds, not minutes.
fn seed(graph: &GraphEngine, n: usize) {
    let types = ["File", "function", "class", "struct", "import", "directory"];
    let langs = ["rust", "typescript", "python"];
    for i in 0..n {
        let et = types[i % types.len()];
        let lang = langs[i % langs.len()];
        let fp = if et == "directory" {
            format!("./{}", ["src", "tests", "docs", "scripts"][i % 4])
        } else {
            format!("./src/file_{}.rs", i)
        };
        let elem = make_element(&format!("qn_{}", i), et, &format!("name_{}", i), &fp, lang);
        graph.insert_element(&elem).expect("insert failed");
    }
}

/// Helper that times a call and asserts it stays well below the MCP timeout.
/// 3s ceiling is tight: with the all_elements()/all_relationships() bulk-pull
/// bug, even 60k rows takes >>3s because every row is materialized into a Vec
/// then re-iterated; with the count()/paginated fix, all three methods complete
/// in milliseconds regardless of graph size.
fn assert_fast<T>(label: &str, started: Instant, result: &Result<T, String>) {
    let elapsed = started.elapsed();
    assert!(
        result.is_ok(),
        "{label} returned error: {:?}",
        result.as_ref().err()
    );
    assert!(
        elapsed < std::time::Duration::from_secs(3),
        "{label} took {elapsed:?} (expected <3s)"
    );
}

#[test]
fn wake_up_summary_returns_valid_summary_on_large_graph() {
    // Force the mega-graph cache threshold low so seed() is "large".
    // The fix must remain correct regardless of cache state.
    env::set_var("LEANKG_MAX_CACHE_ELEMENTS", "100");

    let path = fresh_db();
    let db = leankg::db::backend::init_db(&path).expect("init_db");
    let graph = GraphEngine::new(db.clone());
    // Seed enough to force the deprecated bulk-pull path (15k > 100 cache
    // threshold) but stay within CI wall-clock budget.
    seed(&graph, 15_000);

    let started = Instant::now();
    let result = graph.wake_up_summary();
    assert_fast("wake_up_summary", started, &result);

    let body = result.unwrap();
    assert!(!body.is_empty(), "wake_up_summary returned empty body");
    assert!(body.contains("Files:"), "expected Files section in: {body}");
    assert!(
        body.contains("Relationships:"),
        "expected Relationships section in: {body}"
    );

    cleanup(&path);
}

#[test]
fn identity_context_returns_non_empty_on_large_graph() {
    env::set_var("LEANKG_MAX_CACHE_ELEMENTS", "100");

    let path = fresh_db();
    let db = leankg::db::backend::init_db(&path).expect("init_db");
    let graph = GraphEngine::new(db.clone());
    seed(&graph, 15_000);

    let started = Instant::now();
    let result = graph.identity_context("workspace-be");
    assert_fast("identity_context", started, &result);

    let body = result.unwrap();
    assert!(!body.is_empty(), "identity_context returned empty body");
    assert!(
        body.contains("workspace-be"),
        "expected project name in: {body}"
    );

    cleanup(&path);
}

#[test]
fn critical_facts_context_returns_non_empty_on_large_graph() {
    env::set_var("LEANKG_MAX_CACHE_ELEMENTS", "100");

    let path = fresh_db();
    let db = leankg::db::backend::init_db(&path).expect("init_db");
    let graph = GraphEngine::new(db.clone());
    seed(&graph, 15_000);

    let started = Instant::now();
    let result = graph.critical_facts_context();
    assert_fast("critical_facts_context", started, &result);

    let body = result.unwrap();
    assert!(
        !body.is_empty(),
        "critical_facts_context returned empty body"
    );
    assert!(
        body.contains("Elements:") && body.contains("Relationships:"),
        "expected counts section in: {body}"
    );

    cleanup(&path);
}
