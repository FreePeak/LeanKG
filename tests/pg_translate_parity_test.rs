//! Phase 3 parity tests (plan T3.6), Phase 8 reworked to PG-only.
//!
//! Runs the cozo-dialect query against the PostgresBackend; verifies the
//! translator turns each query shape into working SQL (setup inserts + read
//! returns the expected rows). The legacy CozoDB shim comparison arm was
//! deleted in Phase 8 (plan D4) — Postgres is the only backend.
//!
//! Requires the Phase 0 Postgres container:
//!   docker exec leankg-pg-phase0 psql -U postgres -d leankg -c "CREATE EXTENSION IF NOT EXISTS vector;"
//!
//! Run only these:
//!   cargo test --release --test pg_translate_parity_test -- --ignored
//!
//! All tests are `#[ignore]`-gated so the default `cargo test` run skips them.

use leankg::db::backend::{ClientPool, PostgresBackend};
use leankg::db::value::{DataValue, NamedRows};
use std::collections::BTreeMap;
use std::env;
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::Mutex;

/// Serialize integration tests: each DROPs/CREATEs a shared scratch schema.
static PG_LOCK: Mutex<()> = Mutex::new(());

fn pg_lock() -> std::sync::MutexGuard<'static, ()> {
    PG_LOCK.lock().unwrap_or_else(|e| e.into_inner())
}

/// Generate a unique scratch schema name per test (tests run in parallel).
fn pg_url() -> String {
    env::var("LEANKG_PG_URL")
        .unwrap_or_else(|_| "postgresql://postgres:postgres@localhost:5433/leankg".to_string())
}

fn scratch_schema_name() -> String {
    static COUNTER: AtomicU32 = AtomicU32::new(0);
    format!(
        "leankg_parity_{}_{}",
        std::process::id(),
        COUNTER.fetch_add(1, Ordering::Relaxed)
    )
}

struct Scratch {
    client: postgres::Client,
    name: String,
}

impl Scratch {
    fn new() -> Self {
        let url = pg_url();
        let name = scratch_schema_name();
        let mut admin = postgres::Client::connect(&url, postgres::NoTls)
            .unwrap_or_else(|e| panic!("cannot connect to {url}: {e}"));
        admin
            .batch_execute(&format!("DROP SCHEMA IF EXISTS {name} CASCADE"))
            .unwrap();
        admin
            .batch_execute(&format!("CREATE SCHEMA {name}"))
            .unwrap();
        admin
            .batch_execute(&format!("SET search_path TO {name}, public"))
            .unwrap();
        leankg::db::pg::migrations::run_migrations(&mut admin).unwrap();
        Scratch {
            client: admin,
            name,
        }
    }
}

impl Drop for Scratch {
    fn drop(&mut self) {
        let _ = self
            .client
            .batch_execute(&format!("DROP SCHEMA IF EXISTS {} CASCADE", self.name));
    }
}

/// A PostgresBackend pinned to a scratch schema via the connection URL
/// (`options=-csearch_path`). Without this the backend's own connection
/// would use the default `public` schema and tests would collide/accumulate.
fn pg_backend(schema: &str) -> std::sync::Arc<PostgresBackend> {
    let base = pg_url();
    let sep = if base.contains('?') { '&' } else { '?' };
    let url = format!("{base}{sep}options=-csearch_path%3D{schema}%2Cpublic");
    std::sync::Arc::new(PostgresBackend {
        pg_url: url,
        pool: std::sync::Arc::new(ClientPool::new(5)),
        ro_pool: std::sync::Arc::new(ClientPool::new(5)),
        read_only: false,
    })
}

/// Helper: bind a string param.
fn pstr(map: &mut BTreeMap<String, serde_json::Value>, k: &str, v: &str) {
    map.insert(k.into(), serde_json::Value::String(v.into()));
}

/// Run setup (cozo-dialect DDL, translated to no-op SQL on PG) then the
/// query against the PG backend. For writes, assert success; for reads,
/// assert the result is non-empty and return it.
fn run_pg(
    pg: &PostgresBackend,
    setup: &[&str],
    query: &str,
    params: BTreeMap<String, serde_json::Value>,
) -> NamedRows {
    for stmt in setup {
        // DDL statements (`:create`, `::index`, `::hnsw`) become no-ops on
        // PG (the scratch schema already has all tables from
        // `migrations::run_migrations`).
        let _ = pg.run_script(stmt, BTreeMap::new());
    }
    pg.run_script(query, params)
        .unwrap_or_else(|e| panic!("pg run `{query:.80}` failed: {e}"))
}

/// Assert the read result has at least `min_rows` rows and that the first
/// row's first column matches `expected_first` (a shared sanity check across
/// the query shapes).
fn assert_read(pg: &PostgresBackend, setup: &[&str], query: &str, min_rows: usize) -> NamedRows {
    let res = run_pg(pg, setup, query, BTreeMap::new());
    assert!(
        res.rows.len() >= min_rows,
        "query `{query:.60}` returned {} rows, expected >= {min_rows}: {:?}",
        res.rows.len(),
        res.rows
    );
    res
}

// ---------------------------------------------------------------------------
// Query-shape tests (one per shape).
// ---------------------------------------------------------------------------

#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn parity_equality_select() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let pg = pg_backend(&s.name);
    let setup = [
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["a::b", "function", "f", "f.rs", 1, 10, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
    ];
    let mut params = BTreeMap::new();
    pstr(&mut params, "qn", "a::b");
    let res = run_pg(
        &pg,
        &setup,
        "?[qualified_name, element_type, name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer], qualified_name = $qn",
        params,
    );
    assert_eq!(res.rows.len(), 1);
    assert_eq!(res.rows[0][0].get_str(), Some("a::b"));
    assert_eq!(res.rows[0][1].get_str(), Some("function"));
}

#[test]
#[ignore = "requires container"]
fn parity_range_filter() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let pg = pg_backend(&s.name);
    let setup = [
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["a", "function", "f", "src/foo.rs", 1, 1, "rust", null, null, null, "{}", "local", "procedural"], ["b", "function", "f", "src/bar.rs", 2, 2, "rust", null, null, null, "{}", "local", "procedural"], ["c", "function", "f", "src/qux.rs", 3, 3, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
    ];
    let mut params = BTreeMap::new();
    pstr(&mut params, "lo", "src/b");
    pstr(&mut params, "hi", "src/c\x7f");
    let res = run_pg(
        &pg,
        &setup,
        "?[qualified_name, file_path] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer], file_path >= $lo, file_path < $hi",
        params,
    );
    assert_eq!(res.rows.len(), 1, "range filter must return exactly 'b'");
    assert_eq!(res.rows[0][1].get_str(), Some("src/bar.rs"));
}

#[test]
#[ignore = "requires container"]
fn parity_or_pair() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let pg = pg_backend(&s.name);
    let setup = [
        r#"?[source_qualified, target_qualified, rel_type, confidence, metadata, env] <- [["src", "tgt", "calls", 0.9, "{}", "local"]] :put relationships {source_qualified, target_qualified, rel_type, confidence, metadata, env}"#,
    ];
    let mut params = BTreeMap::new();
    pstr(&mut params, "sq1", "src");
    pstr(&mut params, "sq2", "./src");
    let res = run_pg(
        &pg,
        &setup,
        "?[source_qualified, target_qualified, rel_type, confidence, metadata, env] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, env], (source_qualified = $sq1 or source_qualified = $sq2)",
        params,
    );
    assert_eq!(res.rows.len(), 1);
    assert_eq!(res.rows[0][0].get_str(), Some("src"));
}

#[test]
#[ignore = "requires container"]
fn parity_null_equality() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let pg = pg_backend(&s.name);
    let setup = [
        r#"?[id, name, key_hash, created_at, last_used_at, revoked_at] <- [["k1", "n", "h", "now", null, null]] :put api_keys {id, name, key_hash, created_at, last_used_at, revoked_at}"#,
    ];
    let res = run_pg(
        &pg,
        &setup,
        "?[id, key_hash] := *api_keys[id, _, key_hash, _, _, _], revoked_at = null",
        BTreeMap::new(),
    );
    assert_eq!(
        res.rows.len(),
        1,
        "null equality must match the NULL revoked_at"
    );
    assert_eq!(res.rows[0][0].get_str(), Some("k1"));
}

#[test]
#[ignore = "requires container"]
fn parity_aggregate_order_neg() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let pg = pg_backend(&s.name);
    let setup = [
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["a", "function", "f", "f.rs", 1, 1, "rust", null, null, null, "{}", "local", "procedural"], ["b", "function", "f", "g.rs", 2, 2, "rust", null, null, null, "{}", "local", "procedural"], ["c", "struct", "S", "h.rs", 3, 3, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
    ];
    let res = assert_read(
        &pg,
        &setup,
        "?[element_type, count(element_type)] := *code_elements[_, element_type, _, _, _, _, _, _, _, _, _, _, _] :order -count(element_type)",
        1,
    );
    // Highest count first: function (2) then struct (1).
    assert_eq!(res.rows[0][0].get_str(), Some("function"));
}

#[test]
#[ignore = "requires container"]
fn parity_not_exists_orphans() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let pg = pg_backend(&s.name);
    let setup = [
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["real", "function", "f", "f.rs", 1, 1, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
        r#"?[qualified_name, usearch_key, content_hash, state, embedded_at] <- [["real", 0, "h", "fresh", "now"], ["orphan", 0, "h", "fresh", "now"]] :put embedding_state {qualified_name => usearch_key, content_hash, state, embedded_at}"#,
    ];
    let res = assert_read(
        &pg,
        &setup,
        "?[qualified_name, usearch_key, content_hash, state, embedded_at] := *embedding_state[qualified_name, usearch_key, content_hash, state, embedded_at], not *code_elements[qualified_name, _, _, _, _, _, _, _, _, _, _, _, _]",
        1,
    );
    assert_eq!(res.rows[0][0].get_str(), Some("orphan"));
}

#[test]
#[ignore = "requires container"]
fn parity_keyed_put_upsert() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let pg = pg_backend(&s.name);
    // Initial insert.
    run_pg(
        &pg,
        &[],
        r#"?[qualified_name, usearch_key, content_hash, state, embedded_at] <- [["a", 0, "h1", "fresh", "t1"]] :put embedding_state {qualified_name => usearch_key, content_hash, state, embedded_at}"#,
        BTreeMap::new(),
    );
    // Upsert with same key — must UPDATE the row.
    run_pg(
        &pg,
        &[],
        r#"?[qualified_name, usearch_key, content_hash, state, embedded_at] <- [["a", 0, "h2", "stale", "t2"]] :put embedding_state {qualified_name => usearch_key, content_hash, state, embedded_at}"#,
        BTreeMap::new(),
    );
    // Verify the row was updated, not duplicated.
    let res = run_pg(
        &pg,
        &[],
        "?[qualified_name, state] := *embedding_state[qualified_name, _, _, state, _]",
        BTreeMap::new(),
    );
    assert_eq!(res.rows.len(), 1, "keyed upsert must not duplicate");
    assert_eq!(res.rows[0][1].get_str(), Some("stale"));
}

#[test]
#[ignore = "requires container"]
fn parity_non_keyed_put_allows_duplicates() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let pg = pg_backend(&s.name);
    // Insert same qualified_name twice — PG translator must NOT add a PK for
    // business_logic, so duplicates are allowed (matching cozo).
    for _ in 0..2 {
        run_pg(
            &pg,
            &[],
            r#"?[element_qualified, description, user_story_id, feature_id] <- [["a::b", "d", null, null]] :put business_logic {element_qualified, description, user_story_id, feature_id}"#,
            BTreeMap::new(),
        );
    }
    let res = run_pg(
        &pg,
        &[],
        "?[element_qualified] := *business_logic[element_qualified, _, _, _]",
        BTreeMap::new(),
    );
    assert_eq!(res.rows.len(), 2, "non-keyed duplicates must be preserved");
}

#[test]
#[ignore = "requires container"]
fn parity_delete_where() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let pg = pg_backend(&s.name);
    let setup = [
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["a", "function", "f", "f.rs", 1, 1, "rust", null, null, null, "{}", "local", "procedural"], ["b", "function", "f", "g.rs", 2, 2, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
    ];
    let mut params = BTreeMap::new();
    pstr(&mut params, "qn", "a");
    run_pg(
        &pg,
        &setup,
        ":delete code_elements where qualified_name = $qn",
        params,
    );
    let res = run_pg(
        &pg,
        &[],
        "?[qualified_name] := *code_elements[qualified_name, _, _, _, _, _, _, _, _, _, _, _, _]",
        BTreeMap::new(),
    );
    assert_eq!(
        res.rows.len(),
        1,
        "delete-where must remove exactly one row"
    );
    assert_eq!(res.rows[0][0].get_str(), Some("b"));
}

#[test]
#[ignore = "requires container"]
fn parity_regex_filter() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let pg = pg_backend(&s.name);
    let setup = [
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["a", "function", "f", "ontology://x", 1, 1, "rust", null, null, null, "{}", "local", "procedural"], ["b", "function", "f", "src/x.rs", 2, 2, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
    ];
    let res = assert_read(
        &pg,
        &setup,
        "?[qualified_name, file_path] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer], regex_matches(file_path, \"^ontology://\")",
        1,
    );
    assert_eq!(res.rows[0][1].get_str(), Some("ontology://x"));
}

#[test]
#[ignore = "requires container"]
fn parity_limit_offset() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let pg = pg_backend(&s.name);
    let setup = [
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["a", "function", "f", "f.rs", 1, 1, "rust", null, null, null, "{}", "local", "procedural"], ["b", "function", "f", "g.rs", 2, 2, "rust", null, null, null, "{}", "local", "procedural"], ["c", "function", "f", "h.rs", 3, 3, "rust", null, null, null, "{}", "local", "procedural"], ["d", "function", "f", "i.rs", 4, 4, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
    ];
    let res = assert_read(
        &pg,
        &setup,
        "?[qualified_name] := *code_elements[qualified_name, _, _, _, _, _, _, _, _, _, _, _, _] :limit 2 :offset 1",
        2,
    );
}

// ---------------------------------------------------------------------------
// Unit tests — no container. Validate the value-type helpers.
// ---------------------------------------------------------------------------

#[test]
fn value_helpers_null_and_str() {
    let a = NamedRows::new(
        vec!["x".into()],
        vec![vec![DataValue::Null], vec![DataValue::Str("y".into())]],
    );
    assert_eq!(a.rows[0][0].get_str(), None);
    assert_eq!(a.rows[1][0].get_str(), Some("y"));
}

#[test]
fn translate_unit_pure_no_io() {
    // Smoke: translator works without a backend; verifies the column-order
    // is preserved.
    let t =
        leankg::db::pg::translate::translate("?[a, b, c] := *t[a, b, c]", BTreeMap::new()).unwrap();
    assert_eq!(t.head, vec!["a", "b", "c"]);
    assert!(t.sql.contains("FROM t"));
}
