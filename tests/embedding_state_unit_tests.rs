//! Unit + integration tests for `embedding_state` machinery:
//! - `mark_stale_if_changed` (FR-EMBED-RESUME-04 — no-op preservation)
//! - `mark_stale_for_qualified_names`
//! - `list_stale`, `list_orphans`, `list_all`
//! - `upsert_fresh` chunking (UPSERT_CHUNK = 500)
//! - `delete_state_rows`
//! - `parse_type_filter`
//!
//! Run:
//! ```bash
//! cargo test --release --features embeddings --test embedding_state_unit_tests
//! ```

#![cfg(feature = "embeddings")]

use leankg::db::models::CodeElement;
use leankg::db::schema::{init_db, run_script, CozoDb};
use leankg::embeddings::state::{
    delete_state_rows, ensure_embedding_state_table, list_all, list_orphans, list_stale,
    mark_stale_for_qualified_names, mark_stale_if_changed, upsert_fresh, EmbeddingStateRow,
    FreshRow,
};
use leankg::embeddings::text_blob::{build_blob, content_hash_for};
use leankg::embeddings::{parse_type_filter, BuildMode, BuildOptions};
use std::collections::HashSet;
use tempfile::TempDir;

fn fixture_db() -> (TempDir, CozoDb) {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("test.db");
    let db = init_db(&db_path).expect("init_db");
    ensure_embedding_state_table(&db).expect("ensure embedding_state table");
    (tmp, db)
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

fn insert_code_element(db: &CozoDb, qn: &str, name: &str) {
    let q = format!(
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <-
[["{}", "function", "{}", "./src/{}.rs", 1, 10, "rust", "", "", "", "{{}}", "local", "procedural"]]
:put code_elements {{qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}}"#,
        qn, name, name
    );
    run_script(db, &q, Default::default()).expect("insert code_element");
}

// ---------------------------------------------------------------------------
// mark_stale_for_qualified_names
// ---------------------------------------------------------------------------

#[test]
fn mark_stale_inserts_placeholders_for_unknown_qns() {
    let (_tmp, db) = fixture_db();
    let qns = vec!["a.rs::alpha".to_string(), "b.rs::beta".to_string()];
    mark_stale_for_qualified_names(&db, &qns).expect("mark_stale");

    let stale = list_stale(&db).expect("list_stale");
    assert_eq!(stale.len(), 2);
    let names: HashSet<String> = stale.iter().map(|r| r.qualified_name.clone()).collect();
    assert!(names.contains("a.rs::alpha"));
    assert!(names.contains("b.rs::beta"));
}

#[test]
fn mark_stale_is_idempotent_on_already_stale_rows() {
    let (_tmp, db) = fixture_db();
    let qns = vec!["a.rs::alpha".to_string()];
    mark_stale_for_qualified_names(&db, &qns).expect("mark_stale #1");
    mark_stale_for_qualified_names(&db, &qns).expect("mark_stale #2");
    let stale = list_stale(&db).expect("list_stale");
    assert_eq!(stale.len(), 1, "duplicate calls must not duplicate rows");
}

#[test]
fn mark_stale_empty_input_is_noop() {
    let (_tmp, db) = fixture_db();
    mark_stale_for_qualified_names(&db, &[]).expect("noop");
    let stale = list_stale(&db).expect("list_stale");
    assert!(stale.is_empty());
}

// ---------------------------------------------------------------------------
// mark_stale_if_changed (FR-EMBED-RESUME-04)
// ---------------------------------------------------------------------------

#[test]
fn mark_stale_if_changed_skips_fresh_with_matching_hash() {
    let (_tmp, db) = fixture_db();
    let el = make_function("alpha", "src/a.rs");
    insert_code_element(&db, &el.qualified_name, "alpha");

    // Build blob, compute content_hash, upsert as fresh.
    let blob = build_blob(&el).expect("blob");
    let hash = content_hash_for(&blob);
    let fresh = vec![FreshRow {
        qualified_name: el.qualified_name.clone(),
        usearch_key: 0,
        content_hash: hash.clone(),
    }];
    upsert_fresh(&db, &fresh).expect("upsert_fresh");

    // No-op full index with identical hash → must NOT mark stale.
    let items = vec![(el.qualified_name.clone(), hash.clone())];
    let (marked, skipped) = mark_stale_if_changed(&db, &items).expect("mark_stale_if_changed");
    assert_eq!(
        marked, 0,
        "fresh row with matching hash must not be re-marked"
    );
    assert_eq!(skipped, 1);

    // Confirm DB still says fresh.
    let stale = list_stale(&db).expect("list_stale");
    assert!(stale.is_empty(), "row should remain fresh: {stale:?}");
}

#[test]
fn mark_stale_if_changed_marks_rows_with_mismatched_hash() {
    let (_tmp, db) = fixture_db();
    let el = make_function("alpha", "src/a.rs");
    insert_code_element(&db, &el.qualified_name, "alpha");

    let blob = build_blob(&el).expect("blob");
    let hash = content_hash_for(&blob);
    upsert_fresh(
        &db,
        &[FreshRow {
            qualified_name: el.qualified_name.clone(),
            usearch_key: 0,
            content_hash: hash,
        }],
    )
    .expect("upsert_fresh");

    // Simulate a code change → hash differs.
    let items = vec![(el.qualified_name.clone(), "different-hash".to_string())];
    let (marked, skipped) = mark_stale_if_changed(&db, &items).expect("mark_stale_if_changed");
    assert_eq!(marked, 1);
    assert_eq!(skipped, 0);

    let stale = list_stale(&db).expect("list_stale");
    assert_eq!(stale.len(), 1);
    assert_eq!(stale[0].qualified_name, el.qualified_name);
}

// ---------------------------------------------------------------------------
// list_orphans
// ---------------------------------------------------------------------------

#[test]
fn list_orphans_returns_state_rows_not_in_code_elements() {
    let (_tmp, db) = fixture_db();

    // Mark two stale rows but only insert one CodeElement → other is orphan.
    let qns = vec!["alive.rs::alive".to_string(), "ghost.rs::ghost".to_string()];
    mark_stale_for_qualified_names(&db, &qns).expect("mark_stale");
    insert_code_element(&db, "alive.rs::alive", "alive");

    let orphans = list_orphans(&db).expect("list_orphans");
    assert_eq!(orphans.len(), 1);
    assert_eq!(orphans[0].qualified_name, "ghost.rs::ghost");
}

// ---------------------------------------------------------------------------
// upsert_fresh chunking
// ---------------------------------------------------------------------------

#[test]
fn upsert_fresh_chunks_at_500() {
    let (_tmp, db) = fixture_db();
    let n = 1_250usize; // > 2 * UPSERT_CHUNK
    let updates: Vec<FreshRow> = (0..n)
        .map(|i| FreshRow {
            qualified_name: format!("src/chunk.rs::f_{i}"),
            usearch_key: 0,
            content_hash: "x".into(),
        })
        .collect();
    upsert_fresh(&db, &updates).expect("upsert_fresh 1,250 rows");
    let all = list_all(&db).expect("list_all");
    assert_eq!(all.len(), n, "all chunks must land");
}

#[test]
fn upsert_fresh_empty_input_is_noop() {
    let (_tmp, db) = fixture_db();
    upsert_fresh(&db, &[]).expect("noop");
    let all = list_all(&db).expect("list_all");
    assert!(all.is_empty());
}

// ---------------------------------------------------------------------------
// delete_state_rows
// ---------------------------------------------------------------------------

#[test]
fn delete_state_rows_removes_only_targeted_rows() {
    let (_tmp, db) = fixture_db();
    let qns = vec!["a.rs::alpha".to_string(), "b.rs::beta".to_string()];
    mark_stale_for_qualified_names(&db, &qns).expect("mark_stale");

    let all = list_all(&db).expect("list_all");
    let to_delete: Vec<EmbeddingStateRow> = all
        .iter()
        .filter(|r| r.qualified_name == "a.rs::alpha")
        .cloned()
        .collect();

    delete_state_rows(&db, &to_delete).expect("delete");
    let remaining = list_all(&db).expect("list_all");
    assert_eq!(remaining.len(), 1);
    assert_eq!(remaining[0].qualified_name, "b.rs::beta");
}

#[test]
fn delete_state_rows_empty_is_noop() {
    let (_tmp, db) = fixture_db();
    delete_state_rows(&db, &[]).expect("noop");
}

// ---------------------------------------------------------------------------
// parse_type_filter
// ---------------------------------------------------------------------------

#[test]
fn parse_type_filter_handles_all_keyword() {
    assert!(parse_type_filter("all").is_none());
    assert!(parse_type_filter("ALL").is_none());
    assert!(parse_type_filter("").is_none());
    assert!(parse_type_filter("   ").is_none());
}

#[test]
fn parse_type_filter_lowercases_and_trims() {
    let f = parse_type_filter("Function, METHOD , class").expect("Some");
    assert!(f.contains("function"));
    assert!(f.contains("method"));
    assert!(f.contains("class"));
    assert!(!f.contains("Function"));
}

// ---------------------------------------------------------------------------
// BuildOptions + BuildMode smoke
// ---------------------------------------------------------------------------

#[test]
fn build_options_default_is_incremental_batch_32() {
    let opts = BuildOptions::default();
    assert!(matches!(opts.mode, BuildMode::Incremental));
    assert_eq!(opts.batch_size, 32);
    assert!(opts.type_filter.is_none());
    assert!(opts.reserve_capacity.is_none());
}
