//! Integration tests for doc embed, index inventory, and perf type filter (FR-DOCEMBED / FR-INDEX-INV / FR-EMBED-TYPES).

#![cfg(feature = "embeddings")]

use leankg::db::models::CodeElement;
use leankg::db::schema::init_db;
use leankg::doc_indexer::index_docs_directory;
use leankg::embeddings::{parse_type_filter, state, text_blob, PERF_TYPE_PRESET};
use leankg::graph::inventory::{load_latest_inventory, refresh_index_inventory};
use leankg::graph::GraphEngine;
use leankg::indexer::index_file_sync;
use leankg::indexer::ParserManager;
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use tempfile::TempDir;

/// `set_current_dir` is process-global; serialize tests that chdir into TempDirs.
static CHDIR_TEST_LOCK: Mutex<()> = Mutex::new(());

fn chdir_test_lock() -> std::sync::MutexGuard<'static, ()> {
    CHDIR_TEST_LOCK.lock().unwrap_or_else(|e| e.into_inner())
}

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

#[test]
fn doc_indexer_enriches_metadata() {
    let _lock = chdir_test_lock();
    let tmp = TempDir::new().unwrap();
    let root = tmp.path();
    let docs = root.join("docs");
    fs::create_dir_all(&docs).unwrap();
    fs::write(
        docs.join("feature.md"),
        "# Feature\n\nFirst paragraph about embedding docs.\n\n## Details\n\nMore text.\n",
    )
    .unwrap();

    let db = init_db(&root.join("leankg.db")).unwrap();
    let graph = GraphEngine::new(db);
    let _guard = ProjectRootGuard::change_to(root);
    let result = index_docs_directory(Path::new("docs"), &graph).unwrap();

    assert_eq!(result.documents.len(), 1);
    let doc = &result.documents[0];
    assert_eq!(doc.metadata["title"], "Feature");
    assert!(doc.metadata["first_paragraph"]
        .as_str()
        .unwrap_or("")
        .contains("First paragraph"));
    let heading_path = doc.metadata["heading_path"].as_array().unwrap();
    assert_eq!(heading_path[0], "Feature");

    assert!(!result.sections.is_empty());
    let section = result
        .sections
        .iter()
        .find(|s| s.name == "Details")
        .expect("Details section");
    assert_eq!(section.metadata["title"], "Details");
    assert!(section.metadata["heading_path"]
        .as_array()
        .unwrap()
        .contains(&serde_json::json!("Details")));
}

#[test]
fn index_docs_marks_embedding_state_stale() {
    let _lock = chdir_test_lock();
    let tmp = TempDir::new().unwrap();
    let root = tmp.path();
    let docs = root.join("docs");
    fs::create_dir_all(&docs).unwrap();
    fs::write(docs.join("note.md"), "# Note\n\nEmbed me.\n").unwrap();

    let db = init_db(&root.join("leankg.db")).unwrap();
    let graph = GraphEngine::new(db.clone());
    state::ensure_embedding_state_table(&db).unwrap();
    let _guard = ProjectRootGuard::change_to(root);
    index_docs_directory(Path::new("docs"), &graph).unwrap();

    let stale = state::list_stale(&db).unwrap();
    assert!(
        stale.iter().any(|r| r.qualified_name.contains("note.md")),
        "doc index should mark document rows stale for embed: {:?}",
        stale
    );
}

#[test]
fn index_inventory_persists_after_doc_index() {
    let _lock = chdir_test_lock();
    let tmp = TempDir::new().unwrap();
    let root = tmp.path();
    let docs = root.join("docs");
    fs::create_dir_all(&docs).unwrap();
    fs::write(
        docs.join("inv.md"),
        "# Inv\n\nInventory test.\n\n## Section\n\nSection body.\n",
    )
    .unwrap();

    let db = init_db(&root.join("leankg.db")).unwrap();
    let graph = GraphEngine::new(db.clone());
    let _guard = ProjectRootGuard::change_to(root);
    index_docs_directory(Path::new("docs"), &graph).unwrap();

    let inv = load_latest_inventory(&db).unwrap().expect("inventory row");
    assert!(inv.total_documents >= 1);
    assert!(inv.total_doc_sections >= 1);
    assert_eq!(inv.notes, "doc_index");
}

#[test]
fn index_inventory_updates_after_code_index() {
    let _lock = chdir_test_lock();
    let tmp = TempDir::new().unwrap();
    let root = tmp.path();
    let src = root.join("src");
    fs::create_dir_all(&src).unwrap();
    fs::write(src.join("lib.rs"), "pub fn hello() {}\n").unwrap();

    let db = init_db(&root.join("leankg.db")).unwrap();
    let graph = GraphEngine::new(db.clone());
    let mut parser = ParserManager::new();
    parser.init_parsers().unwrap();
    let _guard = ProjectRootGuard::change_to(root);
    index_file_sync(&graph, &mut parser, "./src/lib.rs").unwrap();

    let inv = load_latest_inventory(&db)
        .unwrap()
        .expect("inventory after code index");
    assert!(inv.total_elements >= 1);
    assert_eq!(inv.notes, "code_index");
}

#[test]
fn perf_type_filter_matches_preset() {
    let filter = parse_type_filter("perf").expect("perf expands");
    for token in PERF_TYPE_PRESET {
        assert!(filter.contains(*token), "missing {token}");
    }
    assert_eq!(filter.len(), PERF_TYPE_PRESET.len());
}

#[test]
fn struct_and_doc_classify_for_embedding() {
    let mut doc = CodeElement {
        element_type: "document".to_string(),
        name: "PRD".to_string(),
        qualified_name: "docs/prd.md".to_string(),
        metadata: serde_json::json!({
            "title": "PRD",
            "first_paragraph": "Requirements document."
        }),
        ..Default::default()
    };
    let blob = text_blob::build_blob(&doc).unwrap();
    assert!(blob.contains("PRD"));
    assert!(blob.contains("Requirements"));

    doc.element_type = "struct".to_string();
    doc.qualified_name = "src/types.rs::Widget".to_string();
    doc.name = "Widget".to_string();
    doc.metadata = serde_json::json!({"doc_comment": "A widget struct."});
    let struct_blob = text_blob::build_blob(&doc).unwrap();
    assert!(struct_blob.contains("Widget"));
}

#[test]
fn untracked_elements_need_full_or_stale_mark() {
    let tmp = TempDir::new().unwrap();
    let db = init_db(tmp.path()).unwrap();
    state::ensure_embedding_state_table(&db).unwrap();
    let graph = GraphEngine::new(db.clone());

    let el = CodeElement {
        element_type: "function".to_string(),
        name: "orphan_fn".to_string(),
        qualified_name: "src/orphan.rs::orphan_fn".to_string(),
        file_path: "src/orphan.rs".to_string(),
        metadata: serde_json::json!({"doc_comment": "never indexed into embedding_state"}),
        ..Default::default()
    };
    graph.insert_elements(&[el]).unwrap();

    let stale_before = state::list_stale(&db).unwrap();
    assert!(
        stale_before.is_empty(),
        "untracked QN has no embedding_state row until --full or stale mark"
    );

    let blob = text_blob::build_blob(
        &graph
            .find_element("src/orphan.rs::orphan_fn")
            .unwrap()
            .unwrap(),
    )
    .unwrap();
    let hash = text_blob::content_hash_for(&blob);
    state::mark_stale_if_changed(&db, &[("src/orphan.rs::orphan_fn".to_string(), hash)]).unwrap();

    let stale_after = state::list_stale(&db).unwrap();
    assert!(
        stale_after
            .iter()
            .any(|r| r.qualified_name == "src/orphan.rs::orphan_fn"),
        "mark_stale_if_changed enrolls untracked work for incremental embed"
    );

    let inv = refresh_index_inventory(&graph, "test_untracked").unwrap();
    assert!(inv.total_elements >= 1);
}
