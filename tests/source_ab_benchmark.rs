//! A/B benchmark for document indexing against a deterministic local fixture.
//!
//! Variant A indexes code only. Variant B indexes same code plus Markdown docs.
//! This proves document indexing adds searchable graph elements without
//! changing code fixture or database setup.

use leankg::db::backend::init_db;
use leankg::doc_indexer::index_docs_directory;
use leankg::graph::GraphEngine;
use leankg::indexer::{index_file_sync, ParserManager};
use serde::Serialize;
use std::fs;
use std::path::Path;
use std::time::Instant;
use tempfile::TempDir;

#[derive(Debug, Serialize)]
struct AbResult {
    baseline_code_results: usize,
    candidate_code_results: usize,
    baseline_document_results: usize,
    candidate_document_results: usize,
    baseline_ms: f64,
    candidate_ms: f64,
    document_result_delta: isize,
}

fn build_fixture(root: &Path) {
    fs::create_dir_all(root.join("src")).expect("create source fixture");
    fs::create_dir_all(root.join("docs")).expect("create docs fixture");
    fs::write(
        root.join("src/lib.rs"),
        "pub fn refresh_index() {}\npub fn search_documents() {}\n",
    )
    .expect("write source fixture");
    fs::write(
        root.join("docs/search.md"),
        "# Document Search\n\nSemantic document indexing keeps product requirements searchable.\n\n## Refresh\n\nRun refresh after changing source or documentation.\n",
    )
    .expect("write document fixture");
}

fn index_code(root: &Path, database_name: &str) -> (GraphEngine, ParserManager) {
    let db = init_db(&root.join(database_name)).expect("initialize database");
    let graph = GraphEngine::new(db.clone());
    let mut parser = ParserManager::new();
    parser.init_parsers().expect("initialize parsers");
    index_file_sync(&graph, &mut parser, "src/lib.rs").expect("index code fixture");
    (graph, parser)
}

#[test]
fn document_indexing_ab_adds_document_results() {
    let tmp = TempDir::new().expect("create benchmark tempdir");
    build_fixture(tmp.path());
    let _cwd = CurrentDirGuard::change_to(tmp.path());

    let (baseline, _) = index_code(tmp.path(), "baseline.db");
    let baseline_start = Instant::now();
    let baseline_code_results = baseline
        .search_by_name_typed("refresh", None, 20)
        .expect("baseline code search");
    let baseline_document_results = baseline
        .search_by_name_typed("Document Search", Some("document"), 20)
        .expect("baseline document search");
    let baseline_ms = baseline_start.elapsed().as_secs_f64() * 1000.0;

    let (candidate, _) = index_code(tmp.path(), "candidate.db");
    let candidate_start = Instant::now();
    index_docs_directory(Path::new("docs"), &candidate).expect("index document fixture");
    let candidate_code_results = candidate
        .search_by_name_typed("refresh", None, 20)
        .expect("candidate code search");
    let candidate_document_results = candidate
        .search_by_name_typed("Document Search", Some("document"), 20)
        .expect("candidate document search");
    let candidate_ms = candidate_start.elapsed().as_secs_f64() * 1000.0;

    assert!(!baseline_code_results.is_empty());
    assert_eq!(baseline_document_results.len(), 0);
    assert!(!candidate_code_results.is_empty());
    assert!(!candidate_document_results.is_empty());

    let result = AbResult {
        baseline_code_results: baseline_code_results.len(),
        candidate_code_results: candidate_code_results.len(),
        baseline_document_results: baseline_document_results.len(),
        candidate_document_results: candidate_document_results.len(),
        baseline_ms,
        candidate_ms,
        document_result_delta: candidate_document_results.len() as isize
            - baseline_document_results.len() as isize,
    };
    if let Ok(path) = std::env::var("LEANKG_SOURCE_AB_OUT") {
        fs::write(
            path,
            serde_json::to_vec_pretty(&result).expect("serialize A/B result"),
        )
        .expect("write A/B result");
    }
    assert!(result.document_result_delta > 0);
}

struct CurrentDirGuard {
    previous: std::path::PathBuf,
}

impl CurrentDirGuard {
    fn change_to(path: &Path) -> Self {
        let previous = std::env::current_dir().expect("read current directory");
        std::env::set_current_dir(path).expect("change current directory");
        Self { previous }
    }
}

impl Drop for CurrentDirGuard {
    fn drop(&mut self) {
        std::env::set_current_dir(&self.previous).expect("restore current directory");
    }
}
