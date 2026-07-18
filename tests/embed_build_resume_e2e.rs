//! E2E tests for FR-EMBED-RESUME-01/02: incremental embed must no-op when all
//! work items are already fresh (no ONNX load, no HNSW rebuild).
//!
//! ```bash
//! cargo test --release --features embeddings --test embed_build_resume_e2e
//! ```

#![cfg(feature = "embeddings")]

use leankg::db::models::CodeElement;
use leankg::db::schema::init_db;
use leankg::embeddings::state::{upsert_fresh, FreshRow};
use leankg::embeddings::text_blob::{build_blob, content_hash_for};
use leankg::embeddings::{build_index, BuildMode, BuildOptions};
use leankg::graph::GraphEngine;
use std::collections::HashSet;
use tempfile::TempDir;

fn with_test_graph<F>(callback: F)
where
    F: FnOnce(&GraphEngine, &TempDir),
{
    let tmp = TempDir::new().expect("tempdir");
    let db_path = tmp.path().join("test.db");
    let db = init_db(db_path.as_path()).expect("init_db");
    let graph = GraphEngine::new(db);
    callback(&graph, &tmp);
}

fn make_function(name: &str, file: &str) -> CodeElement {
    CodeElement {
        qualified_name: format!("{file}::{name}"),
        element_type: "function".to_string(),
        name: name.to_string(),
        file_path: file.to_string(),
        line_start: 1,
        line_end: 10,
        language: "rust".to_string(),
        ..Default::default()
    }
}

fn incremental_function_filter() -> HashSet<String> {
    ["function".to_string()].into_iter().collect()
}

/// FR-EMBED-RESUME-01/02: all embeddable nodes are fresh with matching hashes
/// → incremental build is a no-op (no Embedder, no HNSW drop/rebuild).
#[test]
fn incremental_build_skips_when_all_rows_fresh() {
    with_test_graph(|graph, tmp| {
        let elements = vec![
            make_function("alpha", "src/a.rs"),
            make_function("beta", "src/b.rs"),
            make_function("gamma", "src/c.rs"),
        ];
        graph.insert_elements(&elements).expect("insert_elements");

        let mut fresh_rows = Vec::with_capacity(elements.len());
        for el in &elements {
            let blob = build_blob(el).expect("embeddable function must produce blob");
            fresh_rows.push(FreshRow {
                qualified_name: el.qualified_name.clone(),
                usearch_key: 0,
                content_hash: content_hash_for(&blob),
            });
        }
        upsert_fresh(graph.db(), &fresh_rows).expect("upsert_fresh");

        let index_path = tmp.path().join("dummy.usearch");
        let opts = BuildOptions {
            mode: BuildMode::Incremental,
            type_filter: Some(incremental_function_filter()),
            ..Default::default()
        };

        let report = build_index(graph, &index_path, &opts).expect("build_index resume path");

        assert_eq!(report.embedded_count, 0, "must not embed fresh rows");
        assert_eq!(
            report.skipped_fresh_count, report.considered_count,
            "every considered item should be skipped as fresh"
        );
        assert_eq!(report.considered_count, 3);
        assert_eq!(report.orphaned_count, 0);
    });
}

/// FR-EMBED-RESUME-02: empty graph / no embedding state → early exit before
/// model load (cold embed would require ONNX; we only assert the cheap path).
#[test]
fn incremental_build_empty_db_exits_before_embedder() {
    with_test_graph(|graph, tmp| {
        let index_path = tmp.path().join("dummy.usearch");
        let opts = BuildOptions {
            mode: BuildMode::Incremental,
            type_filter: Some(incremental_function_filter()),
            ..Default::default()
        };

        let report = build_index(graph, &index_path, &opts).expect("empty DB early exit");

        assert_eq!(report.considered_count, 0);
        assert_eq!(report.embedded_count, 0);
        assert_eq!(report.skipped_fresh_count, 0);
        assert_eq!(report.orphaned_count, 0);
    });
}
