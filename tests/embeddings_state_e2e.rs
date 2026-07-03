//! Integration tests for the embedding_state CozoDB table.
//!
//! Feature-gated: only compiled when the `embeddings` feature is on. Run with:
//!
//! ```bash
//! cargo test --release --features embeddings --test embeddings_state_e2e
//! ```
//!
//! These tests don't touch fastembed/usearch — they only exercise the state
//! table helpers in `leankg::embeddings::state`. Model downloads are not
//! required.

#![cfg(feature = "embeddings")]

use leankg::db::schema::init_db;
use leankg::embeddings::state::{
    count_by_state, delete_state_rows, ensure_embedding_state_table, list_all, list_orphans,
    list_stale, mark_stale_for_qualified_names, upsert_fresh, FreshRow,
};

fn fresh_db() -> leankg::db::schema::CozoDb {
    let tmp = tempfile::tempdir().expect("tempdir");
    let db_path = tmp.path().join("test.db");
    // init_db runs init_schema, which creates embedding_state when the
    // feature is compiled in. We hold on to tmp for the life of the test by
    // leaking it — these DBs are tiny and tests are short-lived.
    std::mem::forget(tmp);
    init_db(&db_path).expect("init_db")
}

#[test]
fn ensure_embedding_state_table_is_idempotent() {
    let db = fresh_db();
    ensure_embedding_state_table(&db).expect("first call");
    ensure_embedding_state_table(&db).expect("second call");
}

#[test]
fn mark_stale_inserts_rows_with_placeholder_hash() {
    let db = fresh_db();
    let qns: Vec<String> = (0..5)
        .map(|i| format!("src/file{i}.rs::fn{i}"))
        .collect();
    mark_stale_for_qualified_names(&db, &qns).expect("mark_stale");

    let stale = list_stale(&db).expect("list_stale");
    assert_eq!(stale.len(), 5);
    for row in &stale {
        assert_eq!(row.state, "stale");
        assert!(row.content_hash.is_empty());
    }
}

#[test]
fn mark_stale_is_idempotent() {
    let db = fresh_db();
    let qns = vec!["src/a.rs::f".to_string()];
    mark_stale_for_qualified_names(&db, &qns).expect("first");
    mark_stale_for_qualified_names(&db, &qns).expect("second");

    let all = list_all(&db).expect("list_all");
    assert_eq!(all.len(), 1, "no duplicates after double mark_stale");
}

#[test]
fn upsert_fresh_transitions_state_and_stores_hash() {
    let db = fresh_db();
    let qns: Vec<String> = (0..3).map(|i| format!("q{i}")).collect();
    mark_stale_for_qualified_names(&db, &qns).expect("mark");

    let fresh_rows: Vec<FreshRow> = qns
        .iter()
        .map(|qn| FreshRow {
            qualified_name: qn.clone(),
            usearch_key: leankg::embeddings::usearch_key_for(qn),
            content_hash: format!("hash-{qn}"),
        })
        .collect();
    upsert_fresh(&db, &fresh_rows).expect("upsert_fresh");

    let stale = list_stale(&db).expect("list_stale");
    assert!(stale.is_empty(), "no rows should still be stale");

    let all = list_all(&db).expect("list_all");
    for row in &all {
        assert_eq!(row.state, "fresh");
        assert!(row.content_hash.starts_with("hash-"));
    }
}

#[test]
fn list_orphans_detects_rows_without_code_elements() {
    let db = fresh_db();
    let qns = vec!["ghost1".to_string(), "ghost2".to_string()];
    mark_stale_for_qualified_names(&db, &qns).expect("mark");
    // No code_elements rows created → both are orphans.
    let orphans = list_orphans(&db).expect("list_orphans");
    assert_eq!(orphans.len(), 2);
}

#[test]
fn delete_state_rows_removes_named_rows() {
    let db = fresh_db();
    let qns: Vec<String> = (0..4).map(|i| format!("q{i}")).collect();
    mark_stale_for_qualified_names(&db, &qns).expect("mark");

    // delete_state_rows now takes full EmbeddingStateRow records (CozoDB 0.7.x
    // requires all key columns for `:rm`). Build the rows from list_all.
    let all_rows = list_all(&db).expect("list_all before delete");
    let to_delete: Vec<_> = all_rows
        .iter()
        .filter(|r| r.qualified_name == "q0" || r.qualified_name == "q1")
        .cloned()
        .collect();
    delete_state_rows(&db, &to_delete).expect("delete");

    let remaining = list_all(&db).expect("list_all");
    assert_eq!(remaining.len(), 2);
    let remaining_qns: std::collections::HashSet<String> =
        remaining.iter().map(|r| r.qualified_name.clone()).collect();
    assert!(remaining_qns.contains("q2"));
    assert!(remaining_qns.contains("q3"));
}

#[test]
fn count_by_state_partitions_correctly() {
    let db = fresh_db();
    let qns: Vec<String> = (0..5).map(|i| format!("q{i}")).collect();
    mark_stale_for_qualified_names(&db, &qns).expect("mark");

    let counts = count_by_state(&db).expect("count_by_state");
    assert_eq!(counts.stale, 5);
    assert_eq!(counts.fresh, 0);

    let fresh_rows: Vec<FreshRow> = qns[0..2]
        .iter()
        .map(|qn| FreshRow {
            qualified_name: qn.clone(),
            usearch_key: leankg::embeddings::usearch_key_for(qn),
            content_hash: "x".to_string(),
        })
        .collect();
    upsert_fresh(&db, &fresh_rows).expect("upsert");

    let counts = count_by_state(&db).expect("count_by_state again");
    assert_eq!(counts.fresh, 2);
    assert_eq!(counts.stale, 3);
}

#[test]
fn lookup_usearch_key_returns_computed_value() {
    let db = fresh_db();
    let qn = "src/main.rs::main".to_string();
    mark_stale_for_qualified_names(&db, &[qn.clone()]).expect("mark");

    let key = leankg::embeddings::state::lookup_usearch_key(&db, &qn).expect("lookup");
    let expected = leankg::embeddings::usearch_key_for(&qn);
    assert_eq!(key, Some(expected));
}
