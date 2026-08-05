//! Phase 3 parity tests (plan T3.6).
//!
//! Runs the SAME cozo query against the legacy CozoDB shim and against the
//! new PostgresBackend; compares `NamedRows` column-order + values
//! (Null = Null equality, DataValue equality elsewhere). This is the safety
//! net that catches translator bugs row-for-row before they reach
//! production.
//!
//! Requires the Phase 0 Postgres container:
//!   docker exec leankg-pg-phase0 psql -U postgres -d leankg -c "CREATE EXTENSION IF NOT EXISTS vector;"
//!
//! Run only these:
//!   cargo test --release --test pg_translate_parity_test -- --ignored
//!
//! All tests are `#[ignore]`-gated so the default `cargo test` run skips them.

use leankg::db::backend::{CozoBackend, DbBackend, PostgresBackend};
use std::collections::BTreeMap;
use std::env;
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::Mutex;

/// Serialize integration tests: each DROPs/CREATEs a shared scratch schema.
/// Poison-recovering (same pattern as tests/pg_phase4_vector.rs) so a
/// single failing test doesn't cascade `PoisonError` into every other test.
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

/// Build a CozoDB shim backed by a fresh tempdir (runs the schema-less
/// path). The parity tests hand-`:create` the exact relation shape they
/// need — this matches the pre-migration cozo behavior and avoids the
/// canonical `init_schema` arity mismatch (13-col code_elements vs the
/// shorter test puts).
///
/// The `TempDir` is intentionally leaked: dropping it deletes the sqlite
/// file out from under the open connection, and cozo's engine then fails
/// the first script with "when executing against relation '<name>'". Test
/// processes are short-lived; the OS temp cleaner reclaims the dir.
fn cozo_shim() -> std::sync::Arc<CozoBackend> {
    let tmp = Box::leak(Box::new(tempfile::TempDir::new().unwrap()));
    let path = tmp.path().join("parity.db");
    let db = cozo::DbInstance::new("sqlite", path.as_path(), "").unwrap();
    std::sync::Arc::new(CozoBackend::from_concrete(db))
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
        pool: std::sync::Arc::new(leankg::db::backend::ClientPool::new(5)),
        ro_pool: std::sync::Arc::new(leankg::db::backend::ClientPool::new(5)),
        read_only: false,
    })
}

/// Compare two NamedRows for equality (column-order + value-equality,
/// Null = Null). Used to gate every parity test.
fn assert_rows_equal(actual: &cozo::NamedRows, expected: &cozo::NamedRows, ctx: &str) {
    assert_eq!(
        actual.headers, expected.headers,
        "{ctx}: header mismatch\nactual:   {:?}\nexpected: {:?}",
        actual.headers, expected.headers
    );
    assert_eq!(
        actual.rows.len(),
        expected.rows.len(),
        "{ctx}: row count mismatch (actual {} vs expected {})\nactual rows: {:#?}\nexpected rows: {:#?}",
        actual.rows.len(),
        expected.rows.len(),
        actual.rows,
        expected.rows
    );
    for (i, (a, e)) in actual.rows.iter().zip(expected.rows.iter()).enumerate() {
        assert_eq!(a.len(), e.len(), "{ctx}: row {i} col count mismatch");
        for (j, (av, ev)) in a.iter().zip(e.iter()).enumerate() {
            assert_eq!(
                av, ev,
                "{ctx}: row {i} col {j} mismatch\nactual: {av:?}\nexpected: {ev:?}"
            );
        }
    }
}

/// Helper: bind a string param.
fn pstr(map: &mut BTreeMap<String, serde_json::Value>, k: &str, v: &str) {
    map.insert(k.into(), serde_json::Value::String(v.into()));
}

/// Run the same query against both backends; compare results.
fn parity(
    scratch: &mut Scratch,
    cozo: &CozoBackend,
    pg: &PostgresBackend,
    setup: &[&str],
    query: &str,
    params: BTreeMap<String, serde_json::Value>,
    ctx: &str,
) {
    // Run setup on both backends.
    for stmt in setup {
        cozo.run_script(stmt, BTreeMap::new()).unwrap_or_else(|e| {
            panic!("cozo setup `{stmt}` failed: {e}");
        });
        // For PG setup, the statement is cozo DDL — translate it (best-effort)
        // and ignore DDL no-ops.
        let _ = pg.run_script(stmt, BTreeMap::new());
    }
    // The cozo backend owns the schema; for PG, the scratch schema already
    // has all tables from `migrations::run_migrations`, so DDL statements
    // (`:create`, `::index`, `::hnsw`) become no-ops on PG.
    let q_trimmed = query.trim_start();
    let is_write = q_trimmed.starts_with(":put")
        || q_trimmed.starts_with(":rm")
        || q_trimmed.starts_with(":delete")
        || q_trimmed.contains(":put ")
        || q_trimmed.contains(" :rm ");
    let cozo_res = cozo
        .run_script(query, params.clone())
        .unwrap_or_else(|e| panic!("cozo run `{ctx}` failed: {e}"));
    let pg_res = pg
        .run_script(query, params)
        .unwrap_or_else(|e| panic!("pg run `{ctx}` failed: {e}"));
    if is_write {
        // Cozo's `:put`/`:rm`/`:delete` return a `status` header row; the
        // PG backend returns empty rows. The meaningful contract is that
        // both succeed — compare only success, not the write-result shape.
        return;
    }
    assert_rows_equal(&pg_res, &cozo_res, ctx);
}

// ---------------------------------------------------------------------------
// Parity tests (one per query shape).
// ---------------------------------------------------------------------------

#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn parity_equality_select() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);
    let setup = [
        r#":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}"#,
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["a::b", "function", "f", "f.rs", 1, 10, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
    ];
    let mut params = BTreeMap::new();
    pstr(&mut params, "qn", "a::b");
    parity(&mut s, &cozo, &pg, &setup,
        "?[qualified_name, element_type, name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer], qualified_name = $qn",
        params, "equality select");
}

#[test]
#[ignore = "requires container"]
fn parity_range_filter() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);
    let setup = [
        ":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}",
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["a", "function", "f", "src/foo.rs", 1, 1, "rust", null, null, null, "{}", "local", "procedural"], ["b", "function", "f", "src/bar.rs", 2, 2, "rust", null, null, null, "{}", "local", "procedural"], ["c", "function", "f", "src/qux.rs", 3, 3, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
    ];
    let mut params = BTreeMap::new();
    pstr(&mut params, "lo", "src/b");
    pstr(&mut params, "hi", "src/c\x7f");
    parity(&mut s, &cozo, &pg, &setup,
        "?[qualified_name, file_path] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer], file_path >= $lo, file_path < $hi",
        params, "range filter");
}

#[test]
#[ignore = "requires container"]
fn parity_or_pair() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);
    let setup = [
        ":create relationships {source_qualified: String, target_qualified: String, rel_type: String, confidence: Float, metadata: String, env: String default 'local'}",
        r#"?[source_qualified, target_qualified, rel_type, confidence, metadata, env] <- [["src", "tgt", "calls", 0.9, "{}", "local"]] :put relationships {source_qualified, target_qualified, rel_type, confidence, metadata, env}"#,
    ];
    let mut params = BTreeMap::new();
    pstr(&mut params, "sq1", "src");
    pstr(&mut params, "sq2", "./src");
    parity(&mut s, &cozo, &pg, &setup,
        "?[source_qualified, target_qualified, rel_type, confidence, metadata, env] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, env], (source_qualified = $sq1 or source_qualified = $sq2)",
        params, "or pair");
}

#[test]
#[ignore = "requires container"]
fn parity_null_equality() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);
    let setup = [
        ":create api_keys {id: String, name: String, key_hash: String, created_at: String, last_used_at: String?, revoked_at: String?}",
        r#"?[id, name, key_hash, created_at, last_used_at, revoked_at] <- [["k1", "n", "h", "now", null, null]] :put api_keys {id, name, key_hash, created_at, last_used_at, revoked_at}"#,
    ];
    parity(
        &mut s,
        &cozo,
        &pg,
        &setup,
        "?[id, key_hash] := *api_keys[id, _, key_hash, _, _, _], revoked_at = null",
        BTreeMap::new(),
        "null equality (revoked_at = null)",
    );
}

#[test]
#[ignore = "requires container"]
fn parity_count_group_order() {
    // NOTE (Phase 5): this test's `:group [...] :order count(n) desc`
    // query is INVALID cozo 0.7.x syntax (parser error at the `:group`
    // token) — the cozo shim cannot execute it, so this parity test is
    // aspirational. The PG translator's GROUP BY handling is covered by
    // `parity_aggregate_order_neg` + unit tests. Revisit in Phase 5.5.
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);
    let setup = [
        ":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}",
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["a", "function", "f", "f.rs", 1, 1, "rust", null, null, null, "{}", "dev", "procedural"], ["b", "function", "f", "g.rs", 2, 2, "rust", null, null, null, "{}", "dev", "procedural"], ["c", "function", "f", "h.rs", 3, 3, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
    ];
    parity(&mut s, &cozo, &pg, &setup,
        "?[qualified_name, env, count(n)] := *code_elements[n, _, _, qualified_name, _, _, _, _, _, _, env, _, _] :group [qualified_name, env] :order count(n) desc",
        BTreeMap::new(), "count + group + order desc");
}

#[test]
#[ignore = "requires container"]
fn parity_aggregate_order_neg() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);
    let setup = [
        ":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}",
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["a", "function", "f", "f.rs", 1, 1, "rust", null, null, null, "{}", "local", "procedural"], ["b", "function", "f", "g.rs", 2, 2, "rust", null, null, null, "{}", "local", "procedural"], ["c", "struct", "S", "h.rs", 3, 3, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
    ];
    parity(&mut s, &cozo, &pg, &setup,
        "?[element_type, count(element_type)] := *code_elements[_, element_type, _, _, _, _, _, _, _, _, _, _, _] :order -count(element_type)",
        BTreeMap::new(), "agg order -count");
}

#[test]
#[ignore = "requires container"]
fn parity_not_exists_orphans() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);
    let setup = [
        ":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}",
        ":create embedding_state {qualified_name: String => usearch_key: Int, content_hash: String, state: String, embedded_at: String}",
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["real", "function", "f", "f.rs", 1, 1, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
        r#"?[qualified_name, usearch_key, content_hash, state, embedded_at] <- [["real", 0, "h", "fresh", "now"], ["orphan", 0, "h", "fresh", "now"]] :put embedding_state {qualified_name => usearch_key, content_hash, state, embedded_at}"#,
    ];
    parity(&mut s, &cozo, &pg, &setup,
        "?[qualified_name, usearch_key, content_hash, state, embedded_at] := *embedding_state[qualified_name, usearch_key, content_hash, state, embedded_at], not *code_elements[qualified_name, _, _, _, _, _, _, _, _, _, _, _, _]",
        BTreeMap::new(), "not exists orphans");
}

#[test]
#[ignore = "requires container"]
fn parity_ann_topk() {
    // NOTE (Phase 5): the canonical PG schema fixes embedding_vectors at
    // dim 384 (`vector(384)` in migrations), but this test seeds dim-3
    // vectors — the raw-SQL setup fails with "expected 384 dimensions".
    // The ANN translation itself is covered by `parity_ann_*` in
    // tests/pg_phase4_vector.rs (dim-384). Revisit in Phase 5.5.
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);

    // Insert 5 dim-3 vectors in BOTH backends. Vector literal here is a
    // proxy for the dim-384 production format (we just check set + order).
    let setup_cozo = [
        ":create embedding_vectors {qualified_name: String => vector: <F32; 3>}",
        r#"?[qualified_name, vector] <- [["a", vec([1.0, 0.0, 0.0])], ["b", vec([0.0, 1.0, 0.0])], ["c", vec([0.0, 0.0, 1.0])], ["d", vec([0.7, 0.7, 0.0])], ["e", vec([0.0, 0.7, 0.7])]] :put embedding_vectors {qualified_name => vector}"#,
    ];
    for stmt in setup_cozo {
        cozo.run_script(stmt, BTreeMap::new()).unwrap();
    }

    // Insert into PG using direct INSERT (avoid the vector literal cozo
    // syntax — go through raw SQL).
    let setup_pg = [
        "DELETE FROM embedding_vectors",
        "INSERT INTO embedding_vectors (qualified_name, vec) VALUES ('a', '[1,0,0]')",
        "INSERT INTO embedding_vectors (qualified_name, vec) VALUES ('b', '[0,1,0]')",
        "INSERT INTO embedding_vectors (qualified_name, vec) VALUES ('c', '[0,0,1]')",
        "INSERT INTO embedding_vectors (qualified_name, vec) VALUES ('d', '[0.7,0.7,0]')",
        "INSERT INTO embedding_vectors (qualified_name, vec) VALUES ('e', '[0,0.7,0.7]')",
    ];
    for stmt in setup_pg {
        s.client.batch_execute(stmt).unwrap();
    }

    // Run ANN query on both backends — compare ordered set (the `dist`
    // values differ numerically between cozo Cosine and pgvector L2; only
    // ordering matters).
    let q = "?[dist, qualified_name] := ~embedding_vectors:vec_idx { qualified_name | query: vec([1.0, 0.0, 0.0]), k: 3, ef: 50, bind_distance: dist }";
    let cozo_res = cozo.run_script(q, BTreeMap::new()).unwrap();
    let pg_res = pg.run_script(q, BTreeMap::new()).unwrap();
    assert_eq!(cozo_res.headers, pg_res.headers, "ANN headers");
    assert_eq!(cozo_res.rows.len(), pg_res.rows.len(), "ANN row count");
    // Compare ordered (qualified_name, signed-distance-rank) — strip the
    // numeric `dist` value, which differs between engines.
    let cozo_names: Vec<String> = cozo_res
        .rows
        .iter()
        .map(|r| r.get(1).and_then(|v| v.get_str()).unwrap_or("").to_string())
        .collect();
    let pg_names: Vec<String> = pg_res
        .rows
        .iter()
        .map(|r| r.get(1).and_then(|v| v.get_str()).unwrap_or("").to_string())
        .collect();
    assert_eq!(
        cozo_names, pg_names,
        "ANN ordered qualified_names differ\ncozo: {cozo_names:?}\npg:   {pg_names:?}"
    );
}

#[test]
#[ignore = "requires container"]
fn parity_keyed_put_upsert() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);
    let setup = [
        ":create embedding_state {qualified_name: String => usearch_key: Int, content_hash: String, state: String, embedded_at: String}",
    ];
    // Initial insert.
    parity(
        &mut s,
        &cozo,
        &pg,
        &setup,
        r#"?[qualified_name, usearch_key, content_hash, state, embedded_at] <- [["a", 0, "h1", "fresh", "t1"]] :put embedding_state {qualified_name => usearch_key, content_hash, state, embedded_at}"#,
        BTreeMap::new(),
        "initial keyed put",
    );
    // Upsert with same key — must UPDATE the row.
    parity(
        &mut s,
        &cozo,
        &pg,
        &[],
        r#"?[qualified_name, usearch_key, content_hash, state, embedded_at] <- [["a", 0, "h2", "stale", "t2"]] :put embedding_state {qualified_name => usearch_key, content_hash, state, embedded_at}"#,
        BTreeMap::new(),
        "keyed put upsert",
    );
    // Verify the row was updated, not duplicated.
    parity(
        &mut s,
        &cozo,
        &pg,
        &[],
        "?[qualified_name, state] := *embedding_state[qualified_name, _, _, state, _]",
        BTreeMap::new(),
        "verify upsert",
    );
}

#[test]
#[ignore = "requires container"]
fn parity_non_keyed_put_allows_duplicates() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);
    let setup = [
        ":create business_logic {element_qualified: String, description: String, user_story_id: String?, feature_id: String?}",
    ];
    // Insert same qualified_name twice — cozo non-keyed semantics allow
    // duplicates; PG translator must NOT add a PK for this table. The
    // `:create` runs once (first parity call); subsequent calls pass empty
    // setup (cozo's `:create` is not idempotent).
    for i in 0..2 {
        parity(
            &mut s,
            &cozo,
            &pg,
            if i == 0 { &setup } else { &[] },
            r#"?[element_qualified, description, user_story_id, feature_id] <- [["a::b", "d", null, null]] :put business_logic {element_qualified, description, user_story_id, feature_id}"#,
            BTreeMap::new(),
            "non-keyed duplicate insert",
        );
    }
}

#[test]
#[ignore = "requires container"]
fn parity_delete_where() {
    // NOTE (Phase 5): `:delete table where col = $x` is INVALID cozo
    // 0.7.x syntax (parser error at `where`) — the cozo shim cannot run
    // it. Production swallows the error on the shim (`cleanup_old_metrics`
    // eprintln's and moves on); the PG translator implements `:delete ...
    // where` (delete_where). Covered by translator unit tests.
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);
    let setup = [
        ":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}",
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["a", "function", "f", "f.rs", 1, 1, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
    ];
    parity(
        &mut s,
        &cozo,
        &pg,
        &setup,
        ":delete code_elements where qualified_name = $qn",
        {
            let mut p = BTreeMap::new();
            pstr(&mut p, "qn", "a");
            p
        },
        "delete where",
    );
}

#[test]
#[ignore = "requires container"]
fn parity_regex_filter() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);
    let setup = [
        ":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}",
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["a", "function", "f", "ontology://x", 1, 1, "rust", null, null, null, "{}", "local", "procedural"], ["b", "function", "f", "src/x.rs", 2, 2, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
    ];
    parity(&mut s, &cozo, &pg, &setup,
        "?[qualified_name, file_path] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer], regex_matches(file_path, \"^ontology://\")",
        BTreeMap::new(), "regex filter");
}

#[test]
#[ignore = "requires container"]
fn parity_limit_offset() {
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);
    let setup = [
        ":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}",
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["a", "function", "f", "f.rs", 1, 1, "rust", null, null, null, "{}", "local", "procedural"], ["b", "function", "f", "g.rs", 2, 2, "rust", null, null, null, "{}", "local", "procedural"], ["c", "function", "f", "h.rs", 3, 3, "rust", null, null, null, "{}", "local", "procedural"], ["d", "function", "f", "i.rs", 4, 4, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
    ];
    parity(&mut s, &cozo, &pg, &setup,
        "?[qualified_name] := *code_elements[qualified_name, _, _, _, _, _, _, _, _, _, _, _, _] :limit 2 :offset 1",
        BTreeMap::new(), "limit/offset");
}

// ---------------------------------------------------------------------------
// Unit tests — no container. Validate parity comparison helper + scratch
// schema isolation.
// ---------------------------------------------------------------------------

#[test]
fn assert_rows_equal_handles_null() {
    use cozo::{DataValue, NamedRows};
    let a = NamedRows::new(
        vec!["x".into()],
        vec![vec![DataValue::Null], vec![DataValue::Str("y".into())]],
    );
    let b = NamedRows::new(
        vec!["x".into()],
        vec![vec![DataValue::Null], vec![DataValue::Str("y".into())]],
    );
    assert_rows_equal(&a, &b, "null eq");
}

#[test]
fn assert_rows_equal_detects_mismatch() {
    use cozo::{DataValue, NamedRows};
    let a = NamedRows::new(vec!["x".into()], vec![vec![DataValue::Str("y".into())]]);
    let b = NamedRows::new(vec!["x".into()], vec![vec![DataValue::Str("z".into())]]);
    let res = std::panic::catch_unwind(|| assert_rows_equal(&a, &b, "mismatch"));
    assert!(res.is_err(), "expected panic on mismatch");
}

// ---------------------------------------------------------------------------
// Phase 5 — new translator shapes (multi-rule count, head alias, attr
// binding, key-only rm, str_includes param, :put $args object).
// ---------------------------------------------------------------------------

#[test]
#[ignore = "requires container"]
fn parity_multi_rule_count_distinct() {
    // H6/G88 — `count_files`: intermediate rule dedupes file_path.
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);
    let setup = [
        ":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}",
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["a", "function", "f", "f.rs", 1, 1, "rust", null, null, null, "{}", "dev", "procedural"], ["b", "function", "f", "f.rs", 2, 2, "rust", null, null, null, "{}", "dev", "procedural"], ["c", "function", "f", "g.rs", 3, 3, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
    ];
    parity(
        &mut s,
        &cozo,
        &pg,
        &setup,
        "files[f] := *code_elements[n, a, b, f, c, d, e, g, h, i, j, k, l]\n?[count(f)] := files[f]",
        BTreeMap::new(),
        "multi-rule count distinct",
    );
}

#[test]
#[ignore = "requires container"]
fn parity_head_alias_span() {
    // G107 — `span = line_end - line_start` head alias + `:order -span`.
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);
    let setup = [
        ":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}",
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["a", "function", "f", "f.rs", 1, 10, "rust", null, null, null, "{}", "local", "procedural"], ["b", "function", "g", "g.rs", 5, 6, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
    ];
    parity(
        &mut s,
        &cozo,
        &pg,
        &setup,
        r#"?[qualified_name, file_path, line_end, line_start, language, name, span] := *code_elements[qualified_name, et, name, file_path, line_start, line_end, language, _, _, _, _, _, _], line_end >= 0, line_start >= 0, (line_end - line_start) >= 1, et in ["function", "struct"], span = line_end - line_start:order -span"#,
        BTreeMap::new(),
        "head-alias span",
    );
}

#[test]
#[ignore = "requires container"]
fn parity_attr_binding_read() {
    // Risk note 8 — `*service_metadata{...}` attr syntax + `==` equality.
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);
    let setup = [
        ":create service_metadata {service_name: String, env: String, team: String?, on_call: String?, repo_url: String?, language: String?, health_endpoint: String?, slo_p99_ms: Int?, incident_count: Int, last_incident: Int?, tags: String, version: String?, deploy_envs: String, created_at: Int, updated_at: Int}",
        r#"?[service_name, env, team, on_call, repo_url, language, health_endpoint, slo_p99_ms, incident_count, last_incident, tags, version, deploy_envs, created_at, updated_at] <- [["svc1", "prod", "team-a", "alice", "repo", "rust", "/health", 99, 3, 100, "[]", "1.0", "[]", 1000, 2000]] :put service_metadata {service_name, env, team, on_call, repo_url, language, health_endpoint, slo_p99_ms, incident_count, last_incident, tags, version, deploy_envs, created_at, updated_at}"#,
    ];
    let mut params = BTreeMap::new();
    pstr(&mut params, "svc", "svc1");
    pstr(&mut params, "env", "prod");
    parity(&mut s, &cozo, &pg, &setup,
        "?[service_name, env, team] := *service_metadata{service_name, env, team}, service_name == $svc, env == $env",
        params, "attr-binding read");
}

#[test]
#[ignore = "requires container"]
fn parity_rm_key_only() {
    // Key-only `:rm` on a keyed table (embedding_vectors).
    // NOTE (Phase 5): the canonical PG schema fixes embedding_vectors at
    // dim 384; this test seeds dim-3 vectors, so the PG setup put fails
    // with "expected 384 dimensions". The `:rm {key}` shape itself is
    // covered by the `rm_key_only_infers_table` /
    // `rm_literal_key_only_with_table_prefix` translator unit tests.
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);
    let setup = [
        ":create embedding_vectors {qualified_name: String => vector: <F32; 3>}",
        r#"?[qualified_name, vector] <- [["a", vec([1.0, 0.0, 0.0])], ["b", vec([0.0, 1.0, 0.0])]] :put embedding_vectors {qualified_name => vector}"#,
    ];
    parity(
        &mut s,
        &cozo,
        &pg,
        &setup,
        r#"?[qualified_name] <- [["a"]] :rm embedding_vectors {qualified_name}"#,
        BTreeMap::new(),
        "key-only rm",
    );
    // Verify only "b" remains.
    parity(
        &mut s,
        &cozo,
        &pg,
        &[],
        "?[qualified_name] := *embedding_vectors[qualified_name, _]",
        BTreeMap::new(),
        "verify rm",
    );
}

#[test]
#[ignore = "requires container"]
fn parity_str_includes_param() {
    // str_includes with a bound param (search_code shapes).
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);
    let setup = [
        ":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}",
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["a::b", "function", "AlphaBeta", "f.rs", 1, 1, "rust", null, null, null, "{}", "local", "procedural"], ["c::d", "function", "Gamma", "g.rs", 2, 2, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
    ];
    let mut params = BTreeMap::new();
    pstr(&mut params, "pattern", "alphabeta");
    parity(&mut s, &cozo, &pg, &setup,
        "?[qualified_name, name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, _, _], str_includes(lowercase(name), lowercase($pattern))",
        params, "str_includes param");
}

#[test]
#[ignore = "requires container"]
fn parity_put_object_args() {
    // CH2 — content_hash.rs save_hashes upsert on the keyed index_hashes
    // relation. The canonical cozo form is the rule put
    // `?[path, hash] <- [[$path, $hash]] :put index_hashes {path => hash}`
    // (the `:put table {cols} <- $args` short form is invalid cozo).
    let _guard = pg_lock();
    let mut s = Scratch::new();
    let cozo = cozo_shim();
    let pg = pg_backend(&s.name);
    let setup = [":create index_hashes {path: String => hash: String}"];
    let mut params = BTreeMap::new();
    pstr(&mut params, "path", "src/a.rs");
    pstr(&mut params, "hash", "h1");
    parity(
        &mut s,
        &cozo,
        &pg,
        &setup,
        r#"?[path, hash] <- [[$path, $hash]] :put index_hashes {path => hash}"#,
        params,
        "put object args",
    );
    parity(
        &mut s,
        &cozo,
        &pg,
        &[],
        "?[path, hash] := *index_hashes[path, hash]",
        BTreeMap::new(),
        "verify object args put",
    );
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
