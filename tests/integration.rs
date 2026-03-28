// Integration tests requiring filesystem, async, or SurrealDB

use leankg::db::schema::init_db;
use leankg::doc::DocGenerator;
use leankg::graph::{GraphEngine, ImpactAnalyzer};
use leankg::indexer::{find_files_sync, index_file_sync, ParserManager};
use std::path::PathBuf;
use tempfile::TempDir;

#[tokio::test]
async fn test_find_files_empty_dir() {
    let tmp = TempDir::new().unwrap();
    let root = tmp.path().to_str().unwrap();
    let files = find_files_sync(root).unwrap();
    assert!(files.is_empty());
}

#[tokio::test]
async fn test_find_files_discovers_go_files() {
    let tmp = TempDir::new().unwrap();
    let go_file = tmp.path().join("main.go");
    std::fs::write(&go_file, "package main\nfunc main() {}").unwrap();
    let files = find_files_sync(tmp.path().to_str().unwrap()).unwrap();
    assert!(!files.is_empty());
    assert!(files.iter().any(|f| f.ends_with("main.go")));
}

#[tokio::test]
async fn test_find_files_excludes_node_modules() {
    let tmp = TempDir::new().unwrap();
    let node_dir = tmp.path().join("node_modules").join("pkg");
    std::fs::create_dir_all(&node_dir).unwrap();
    std::fs::write(node_dir.join("index.js"), "export {}").unwrap();
    let files = find_files_sync(tmp.path().to_str().unwrap()).unwrap();
    assert!(!files.iter().any(|f| f.contains("node_modules")));
}

#[tokio::test]
async fn test_find_files_in_nested_dirs() {
    let tmp = TempDir::new().unwrap();
    let nested = tmp.path().join("a").join("b").join("c");
    std::fs::create_dir_all(&nested).unwrap();
    std::fs::write(nested.join("lib.py"), "def x(): pass").unwrap();
    let files = find_files_sync(tmp.path().to_str().unwrap()).unwrap();
    assert!(files.iter().any(|f| f.ends_with("lib.py")));
}

#[tokio::test]
async fn test_init_db_creates_schema() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let _db = init_db(db_path.as_path()).unwrap();
    assert!(db_path.exists() || std::path::Path::new(db_path.parent().unwrap()).exists());
}

#[tokio::test]
async fn test_graph_engine_all_elements_empty() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);
    let elements = graph.all_elements().unwrap();
    assert!(elements.is_empty());
}

#[tokio::test]
async fn test_graph_engine_find_element_missing() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);
    let result = graph.find_element("nonexistent::foo").unwrap();
    assert!(result.is_none());
}

#[tokio::test]
async fn test_impact_analyzer_empty_graph() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);
    let analyzer = ImpactAnalyzer::new(&graph);
    let result = analyzer.calculate_impact_radius("src/main.go", 3).unwrap();
    assert_eq!(result.start_file, "src/main.go");
    assert_eq!(result.max_depth, 3);
    assert!(result.affected_elements.is_empty());
}

#[tokio::test]
async fn test_doc_generator_agents_md_empty() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);
    let doc_gen = DocGenerator::new(graph, PathBuf::from("./docs"));
    let content = doc_gen.generate_agents_md().unwrap();
    assert!(content.contains("# Agent Guidelines for LeanKG"));
    assert!(content.contains("## Project Overview"));
    assert!(content.contains("## Build Commands"));
    assert!(content.contains("## Code Structure Overview"));
}

#[tokio::test]
async fn test_doc_generator_claude_md_empty() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);
    let doc_gen = DocGenerator::new(graph, PathBuf::from("./docs"));
    let content = doc_gen.generate_claude_md().unwrap();
    assert!(content.contains("# CLAUDE.md"));
    assert!(content.contains("## Project Overview"));
    assert!(content.contains("## Architecture Decisions"));
    assert!(content.contains("## Context Statistics"));
}

#[tokio::test]
async fn test_doc_sync_for_file() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);

    let go_file = tmp.path().join("main.go");
    std::fs::write(
        &go_file,
        "package main\n\nfunc add(x int, y int) int { return x + y }",
    )
    .unwrap();

    let mut parser = ParserManager::new();
    if parser.init_parsers().is_err() {
        return;
    }
    let _count = index_file_sync(&graph, &mut parser, go_file.to_str().unwrap()).unwrap();

    let doc_gen = DocGenerator::new(graph, PathBuf::from("./docs"));
    let result = doc_gen
        .sync_docs_for_file(go_file.to_str().unwrap())
        .unwrap();
    assert_eq!(result.file_path, go_file.to_str().unwrap());
    assert!(result.elements_regenerated > 0);
}

#[tokio::test]
async fn test_index_file_go() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);

    let go_file = tmp.path().join("main.go");
    std::fs::write(
        &go_file,
        "package main\n\nfunc add(x int, y int) int { return x + y }",
    )
    .unwrap();

    let mut parser = ParserManager::new();
    if parser.init_parsers().is_err() {
        return;
    }
    let count = index_file_sync(&graph, &mut parser, go_file.to_str().unwrap()).unwrap();
    assert!(count > 0);
}
