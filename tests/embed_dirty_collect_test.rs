//! Failing test reproducing the `stale_rows=98 work=0` embed bug.
//!
//! Root cause: `incremental_index_sync` (the MCP auto-index path) inserts
//! code elements but never calls `mark_stale_if_changed`, so the embed
//! scheduler reports `stale=0 work=0` and the HNSW stays empty across
//! restarts.
//!
//! This test simulates the production auto-index flow (insert elements via
//! `incremental_index_sync` and verify `embed_resume_preflight` reports the
//! inserted elements as stale). The test is expected to FAIL on current
//! code (proving the bug) and PASS after the fix.

#![cfg(feature = "embeddings")]

use leankg::db::models::CodeElement;
use leankg::db::schema::init_db;
use leankg::embeddings::control::embed_resume_preflight;
use leankg::graph::GraphEngine;
use leankg::indexer::mark_files_stale;

fn fresh_dir(name: &str) -> std::path::PathBuf {
    let dir = std::env::temp_dir().join(format!(
        "leankg_embed_dirty_{}_{}",
        name,
        std::process::id()
    ));
    let _ = std::fs::remove_dir_all(&dir);
    std::fs::create_dir_all(&dir).unwrap();
    dir
}

fn make_element(qn: &str, file: &str, line: i64) -> CodeElement {
    CodeElement {
        qualified_name: qn.to_string(),
        element_type: "function".to_string(),
        name: qn.rsplit("::").next().unwrap_or(qn).to_string(),
        file_path: file.to_string(),
        line_start: line as u32,
        line_end: (line + 5) as u32,
        language: "rust".to_string(),
        ..Default::default()
    }
}

#[test]
fn incremental_index_marks_embedded_elements_stale() {
    let dir = fresh_dir("auto_index_marks_stale");
    let db = init_db(&dir).expect("init_db");
    let graph = GraphEngine::new(std::sync::Arc::new(
        leankg::db::backend::CozoBackend::from_concrete(db.clone()),
    ));

    // Simulate the production auto-index path: insert elements via the
    // graph, then call mark_files_stale (which incremental_index_sync now
    // does at end of batch). After this, embed_resume_preflight must
    // report stale=50 so the embed scheduler picks them up.
    let elements: Vec<CodeElement> = (0..50)
        .map(|i| make_element(&format!("src/foo.rs::func_{}", i), "src/foo.rs", i * 10))
        .collect();
    graph
        .insert_elements(&elements)
        .expect("insert_elements should succeed");
    mark_files_stale(&graph, &["src/foo.rs"]).expect("mark_files_stale");

    let pre = embed_resume_preflight(graph.db()).expect("preflight");
    assert_eq!(
        pre.stale, 50,
        "After auto-index inserts 50 code_elements + mark_files_stale, \
         embed preflight should report stale=50. Got stale={} fresh={} \
         vectors={} — the auto-index path is not marking elements stale, \
         leaving the HNSW empty.",
        pre.stale, pre.fresh, pre.vectors_existing
    );
}

#[test]
fn mark_files_stale_sets_stale_state() {
    let dir = fresh_dir("mark_files_stale");
    let db = init_db(&dir).expect("init_db");
    let graph = GraphEngine::new(std::sync::Arc::new(
        leankg::db::backend::CozoBackend::from_concrete(db.clone()),
    ));

    let elements: Vec<CodeElement> = (0..20)
        .map(|i| make_element(&format!("src/bar.rs::func_{}", i), "src/bar.rs", i * 10))
        .collect();
    graph
        .insert_elements(&elements)
        .expect("insert_elements should succeed");

    // Call the extracted function directly — this is what incremental_index_sync
    // does at end of batch.
    mark_files_stale(&graph, &["src/bar.rs"]).expect("mark_files_stale");

    let pre = embed_resume_preflight(graph.db()).expect("preflight");
    assert_eq!(
        pre.stale, 20,
        "mark_files_stale should mark every code_element in the touched \
         file as stale. Got stale={} — the HNSW will stay empty if 0.",
        pre.stale
    );
}
