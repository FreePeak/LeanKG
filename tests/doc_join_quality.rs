//! Integration tests for doc↔code join quality (FR-DOCJOIN-04).

use leankg::db::schema::init_db;
use leankg::doc_indexer::index_docs_directory;
use leankg::graph::GraphEngine;
use leankg::indexer::index_file_sync;
use leankg::indexer::ParserManager;
use leankg::mcp::handler::ToolHandler;
use serde_json::json;
use std::fs;
use std::path::{Path, PathBuf};
use tempfile::TempDir;

struct ProjectRootGuard {
    previous: PathBuf,
}

impl ProjectRootGuard {
    fn change_to(root: &Path) -> Self {
        let previous = std::env::current_dir().expect("current_dir");
        std::env::set_current_dir(root).expect("set_current_dir");
        Self { previous }
    }
}

impl Drop for ProjectRootGuard {
    fn drop(&mut self) {
        let _ = std::env::set_current_dir(&self.previous);
    }
}

const WIDGET_KEY: &str = "./src/widget.rs";

#[test]
fn doc_join_round_trip_via_graph() {
    let tmp = TempDir::new().unwrap();
    let root = tmp.path();
    let src = root.join("src");
    let docs = root.join("docs");
    fs::create_dir_all(&src).unwrap();
    fs::create_dir_all(&docs).unwrap();
    fs::write(src.join("widget.rs"), "pub fn widget() {}\n").unwrap();
    fs::write(docs.join("guide.md"), "# Guide\n\nSee `src/widget.rs`.\n").unwrap();

    let db = init_db(&root.join("leankg.db")).unwrap();
    let graph = GraphEngine::new(db);
    let mut parser = ParserManager::new();
    parser.init_parsers().unwrap();
    let _root = ProjectRootGuard::change_to(root);
    index_file_sync(&graph, &mut parser, WIDGET_KEY).unwrap();
    index_docs_directory(Path::new("docs"), &graph).unwrap();

    let doc_rels = graph.get_relationships("docs/guide.md").unwrap();
    let refs: Vec<_> = doc_rels
        .iter()
        .filter(|r| r.rel_type == "references")
        .collect();
    assert!(!refs.is_empty(), "expected references from doc to code");
    assert!(
        refs.iter()
            .any(|r| r.target_qualified.contains("widget.rs")),
        "references should resolve to indexed file key: {:?}",
        refs
    );

    let file_rels = graph.get_relationships(WIDGET_KEY).unwrap();
    let documented: Vec<_> = file_rels
        .iter()
        .filter(|r| r.rel_type == "documented_by")
        .collect();
    assert!(
        !documented.is_empty(),
        "expected documented_by from file to doc"
    );
    assert!(
        documented
            .iter()
            .any(|r| r.target_qualified == "docs/guide.md"),
        "documented_by should point at doc key"
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn doc_join_mcp_tools_with_aliases() {
    let tmp = TempDir::new().unwrap();
    let root = tmp.path();
    let src = root.join("src");
    let docs = root.join("docs");
    fs::create_dir_all(&src).unwrap();
    fs::create_dir_all(&docs).unwrap();
    fs::write(src.join("widget.rs"), "pub fn widget() {}\n").unwrap();
    fs::write(docs.join("guide.md"), "# Guide\n\nSee `src/widget.rs`.\n").unwrap();

    let db_path = root.join("leankg.db");
    let db = init_db(&db_path).unwrap();
    let graph = GraphEngine::new(db);
    let mut parser = ParserManager::new();
    parser.init_parsers().unwrap();
    let _root = ProjectRootGuard::change_to(root);
    index_file_sync(&graph, &mut parser, WIDGET_KEY).unwrap();
    index_docs_directory(Path::new("docs"), &graph).unwrap();

    let handler = ToolHandler::new(graph, db_path);

    for doc_arg in ["docs/guide.md", "./docs/guide.md", "guide.md"] {
        let result = handler
            .execute_tool("get_files_for_doc", &json!({"doc": doc_arg}))
            .await
            .unwrap();
        let files = result["files"].as_array().unwrap();
        assert!(
            !files.is_empty(),
            "get_files_for_doc({doc_arg}) should return files: {result}"
        );
    }

    for file_arg in [WIDGET_KEY, "src/widget.rs"] {
        let result = handler
            .execute_tool("find_related_docs", &json!({"file": file_arg}))
            .await
            .unwrap();
        let docs_out = result["related_docs"].as_array().unwrap();
        assert!(
            !docs_out.is_empty(),
            "find_related_docs({file_arg}) should return docs: {result}"
        );
    }

    let miss = handler
        .execute_tool("get_files_for_doc", &json!({"doc": "unknown.md"}))
        .await
        .unwrap();
    assert!(miss["files"].as_array().unwrap().is_empty());
    assert!(miss.get("tried").is_some());
}

#[test]
fn doc_join_skips_unresolved_refs() {
    let tmp = TempDir::new().unwrap();
    let root = tmp.path();
    let docs = root.join("docs");
    fs::create_dir_all(&docs).unwrap();
    fs::write(
        docs.join("orphan.md"),
        "# Orphan\n\nSee `src/missing.rs`.\n",
    )
    .unwrap();

    let db = init_db(&root.join("leankg.db")).unwrap();
    let graph = GraphEngine::new(db);
    index_docs_directory(&docs, &graph).unwrap();

    let rels = graph.get_relationships("docs/orphan.md").unwrap();
    let refs: Vec<_> = rels.iter().filter(|r| r.rel_type == "references").collect();
    assert!(
        refs.is_empty(),
        "unresolved refs must not invent edges: {:?}",
        refs
    );
}
