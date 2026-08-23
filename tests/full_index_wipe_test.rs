//! Full-index wipe regression test.
//!
//! `leankg index` (full, non-incremental) only INSERTs into `code_elements`
//! and `relationships` — both tables are unkeyed by design (schema.sql:25-57)
//! — so a full reindex accumulated duplicate rows. The fix makes full index
//! delete-then-insert per env, mirroring the incremental path.
//!
//! Expected to FAIL on current code (insert-only full index), PASS after fix.
//!
//! Requires the Phase 0 Postgres container (Postgres 18 + pgvector):
//!   docker exec leankg-pg-phase0 psql -U postgres -d leankg \
//!     -c "CREATE EXTENSION IF NOT EXISTS vector;"
//!
//! Run only this test (the crate has slow unrelated integration tests):
//!   cargo test --release --test full_index_wipe_test

use leankg::db::backend::init_db;
#[allow(unused_imports)]
use leankg::db::backend::pg_connect;
use leankg::db::models::CodeElement;
use leankg::graph::GraphEngine;
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::{Mutex, OnceLock};

/// Serializes the `LEANKG_PG_URL` mutation + `init_db` window — env is
/// process-global and tests run in parallel. The `PostgresBackend` captures
/// the URL at construction, so a short critical section is all that is needed.
static ENV_LOCK: OnceLock<Mutex<()>> = OnceLock::new();
static SCHEMA_COUNTER: AtomicU32 = AtomicU32::new(0);

fn base_url() -> String {
    std::env::var("LEANKG_PG_URL")
        .unwrap_or_else(|_| "postgresql://postgres:postgres@localhost:5433/leankg".to_string())
}

/// Create a fresh scratch schema, run migrations, and return the schema name.
/// Mirrors `ScratchSchema` in tests/pg_schema_test.rs but keeps the admin
/// connection alive (forgotten) so the schema exists for the process lifetime.
fn fresh_migrated_schema() -> String {
    let name = format!(
        "leankg_wipe_test_{}_{}",
        std::process::id(),
        SCHEMA_COUNTER.fetch_add(1, Ordering::Relaxed)
    );
    let base = base_url();
    let mut admin = pg_connect(&base).unwrap_or_else(|e| panic!("cannot connect to {base}: {e}"));
    admin
        .batch_execute(&format!("DROP SCHEMA IF EXISTS {name} CASCADE"))
        .expect("drop schema");
    admin
        .batch_execute(&format!("CREATE SCHEMA {name}"))
        .expect("create schema");
    admin
        .batch_execute(&format!("SET search_path TO {name}, public"))
        .expect("set search_path");
    leankg::db::pg::migrations::run_migrations(&mut admin)
        .expect("run_migrations on scratch schema");
    // Drop the admin connection — the schema persists server-side, and this
    // role has a tight connection cap (E53300/refused when leaked).
    drop(admin);
    name
}

/// Build a GraphEngine on a fresh migrated scratch schema.
fn fresh_engine() -> GraphEngine {
    let _schema = fresh_migrated_schema();
    // Under #[cfg(test)] init_db derives its scratch schema from db_path, so
    // a FIXED path here made every test share one schema — rows from one
    // test bled into another's assertions. Unique path per engine.
    static PATH_COUNTER: AtomicU32 = AtomicU32::new(0);
    let db_path = std::env::temp_dir().join(format!(
        "leankg_wipe_test_{}_{}.db",
        std::process::id(),
        PATH_COUNTER.fetch_add(1, Ordering::Relaxed)
    ));
    let guard = ENV_LOCK.get_or_init(|| Mutex::new(())).lock().unwrap();
    // Restore the caller's value afterwards — removing the var entirely made
    // later tests fall back to the built-in localhost:5433 default even when
    // the suite was pointed at remote PG via the environment.
    let prev = std::env::var("LEANKG_PG_URL").ok();
    std::env::set_var("LEANKG_PG_URL", &base_url());
    let db = init_db(&db_path).expect("init_db on scratch schema");
    match prev {
        Some(v) => std::env::set_var("LEANKG_PG_URL", v),
        None => std::env::remove_var("LEANKG_PG_URL"),
    }
    drop(guard);
    GraphEngine::new(db)
}

fn elem(qn: &str) -> CodeElement {
    CodeElement {
        qualified_name: qn.to_string(),
        element_type: "function".to_string(),
        name: qn.rsplit("::").next().unwrap_or(qn).to_string(),
        file_path: "/src/lib.rs".to_string(),
        line_start: 1,
        line_end: 5,
        language: "rust".to_string(),
        parent_qualified: Some("/src/lib.rs".to_string()),
        cluster_id: None,
        cluster_label: None,
        metadata: serde_json::json!({}),
        env: "local".to_string(),
    }
}

/// Simulate two full-index runs over the same source, then assert no
/// duplicate qualified_name survived the second run's wipe.
#[test]
fn full_reindex_does_not_accumulate_duplicates() {
    let ge = fresh_engine();

    let batch = vec![elem("/src/lib.rs::main"), elem("/src/lib.rs::helper")];
    ge.insert_elements_with(&batch, true).expect("run 1");

    // Second full run — must wipe before inserting.
    ge.wipe_elements_for_env("local").expect("wipe run 2");
    ge.invalidate_cache();
    ge.insert_elements_with(&batch, true).expect("run 2");
    ge.invalidate_cache();

    let all = ge.all_elements().expect("all_elements");
    let mut counts: std::collections::HashMap<String, usize> = std::collections::HashMap::new();
    for e in &all {
        *counts.entry(e.qualified_name.clone()).or_insert(0) += 1;
    }
    let dupes: Vec<_> = counts
        .into_iter()
        .filter(|(_, c)| *c > 1)
        .map(|(n, _)| n)
        .collect();

    assert!(
        dupes.is_empty(),
        "full reindex left duplicate qualified_names: {:?} (total rows {})",
        dupes,
        all.len()
    );
    assert_eq!(
        all.len(),
        2,
        "expected 2 elements after reindex, got {}",
        all.len()
    );
}

/// The wipe is env-scoped: rows in a different env must survive.
#[test]
fn wipe_is_scoped_to_env() {
    let ge = fresh_engine();
    let db_handle = ge.db_arc().clone();

    ge.insert_elements_with(&[elem("/src/lib.rs::main")], true)
        .expect("insert local");

    // Write a row in a different env via raw 13-col :put (insert_elements_with
    // doesn't write env — it inherits the schema DEFAULT 'local').
    let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [[$qn, $et, $nm, $fp, $ls, $le, $lg, $pq, $cid, $cl, $md, $env, $ol]] :put code_elements { qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer }"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert("qn".to_string(), serde_json::json!("/other.rs::keep"));
    params.insert("et".to_string(), serde_json::json!("function"));
    params.insert("nm".to_string(), serde_json::json!("keep"));
    params.insert("fp".to_string(), serde_json::json!("/other.rs"));
    params.insert("ls".to_string(), serde_json::json!(1));
    params.insert("le".to_string(), serde_json::json!(2));
    params.insert("lg".to_string(), serde_json::json!("rust"));
    params.insert("pq".to_string(), serde_json::json!("/other.rs"));
    params.insert("cid".to_string(), serde_json::Value::Null);
    params.insert("cl".to_string(), serde_json::Value::Null);
    params.insert("md".to_string(), serde_json::json!("{}"));
    params.insert("env".to_string(), serde_json::json!("other"));
    params.insert("ol".to_string(), serde_json::json!("procedural"));
    db_handle
        .run_script(query, params)
        .expect("put other-env row");

    ge.wipe_elements_for_env("local").expect("wipe local");
    ge.invalidate_cache();

    let all = ge.all_elements().expect("all_elements");
    let keep: Vec<_> = all
        .iter()
        .filter(|e| e.qualified_name == "/other.rs::keep")
        .collect();
    assert!(
        !keep.is_empty(),
        "other-env row was wiped by env-scoped wipe: {:?}",
        all.iter()
            .map(|e| (e.qualified_name.as_str(), e.env.as_str()))
            .collect::<Vec<_>>()
    );
    assert_eq!(keep[0].env, "other");
    assert!(
        !all.iter().any(|e| e.qualified_name == "/src/lib.rs::main"),
        "local-env row survived its own env wipe"
    );
}
