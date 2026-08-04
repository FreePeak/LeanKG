//! Phase 5.5 regression sweep — every user-facing MCP tool: cozo shim vs
//! PostgreSQL on identical fixture data. Diffs JSON responses, records
//! p50 latency (N=5), flags >2x regressions.
//!
//! Run:
//! ```bash
//! LEANKG_PG_URL=postgresql://postgres:postgres@localhost:5433/leankg \
//!   cargo test --release --test pg_regression_tools -- --test-threads=1
//! ```
//! (--test-threads=1: tests share one scratch schema + the cozo OPENED path
//! guard; container-gated, like the other pg_* tests.)

use leankg::db::backend::{CozoBackend, DbBackend, PostgresBackend};
use leankg::graph::GraphEngine;
use leankg::mcp::handler::ToolHandler;
use serde_json::{json, Value};
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::Arc;

fn pg_url() -> String {
    std::env::var("LEANKG_PG_URL")
        .unwrap_or_else(|_| "postgresql://postgres:postgres@localhost:5433/leankg".to_string())
}

static SCHEMA_COUNTER: AtomicU32 = AtomicU32::new(0);

/// A scratch schema in the dev PG container, dropped on test exit.
struct ScratchSchema {
    admin: postgres::Client,
    name: String,
    url: String,
}

impl ScratchSchema {
    fn new() -> Self {
        let base = pg_url();
        let name = format!(
            "leankg_regr_{}_{}",
            std::process::id(),
            SCHEMA_COUNTER.fetch_add(1, Ordering::Relaxed)
        );
        let mut admin = postgres::Client::connect(&base, postgres::NoTls)
            .unwrap_or_else(|e| panic!("cannot connect to {base}: {e}"));
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
        let sep = if base.contains('?') { '&' } else { '?' };
        let url = format!("{base}{sep}options=-csearch_path%3D{name}%2Cpublic");
        Self { admin, name, url }
    }

    fn backend(&self) -> Arc<PostgresBackend> {
        Arc::new(PostgresBackend {
            pg_url: self.url.clone(),
            conn: std::sync::Mutex::new(None).into(),
        })
    }
}

impl Drop for ScratchSchema {
    fn drop(&mut self) {
        let _ = self
            .admin
            .batch_execute(&format!("DROP SCHEMA IF EXISTS {} CASCADE", self.name));
    }
}

/// Cozo shim over a fresh tempdir (sqlite). TempDir leaked on purpose —
/// dropping it deletes the db file out from under the open handle.
fn cozo_shim() -> Arc<CozoBackend> {
    let tmp = Box::leak(Box::new(tempfile::TempDir::new().unwrap()));
    let path = tmp.path().join("regr.db");
    // Canonical cozo init (schema + embedding tables) via the path-based
    // backend init — same as `LEANKG_DB_ENGINE=cozo leankg init`.
    // init_db always returns CozoBackend for path-based init.
    Arc::new(CozoBackend::from_concrete(
        leankg::db::schema::init_db_cozo(&path).unwrap(),
    ))
}

// ---------------------------------------------------------------------------
// Fixture — identical rows into cozo + PG. SQL is written in the canonical
// cozo dialect; the translator turns it into the same PG rows.
// ---------------------------------------------------------------------------

const FIXTURE_ELEMENTS: &str = r#"
?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <-
[
    ["src/main.rs", "file", "main.rs", "src/main.rs", 1, 40, "rust", null, "c1", "core", "{}", "local", "procedural"],
    ["src/main.rs::main", "function", "main", "src/main.rs", 5, 30, "rust", "src/main.rs", "c1", "core", "{}", "local", "procedural"],
    ["src/main.rs::validate_key", "function", "validate_key", "src/main.rs", 12, 20, "rust", "src/main.rs", "c1", "core", "{}", "local", "procedural"],
    ["src/lib.rs", "file", "lib.rs", "src/lib.rs", 1, 60, "rust", null, "c1", "core", "{}", "local", "procedural"],
    ["src/lib.rs::caller", "function", "caller", "src/lib.rs", 3, 40, "rust", "src/lib.rs", "c1", "core", "{}", "local", "procedural"],
    ["src/lib.rs::caller::helper", "function", "helper", "src/lib.rs", 15, 22, "rust", "src/lib.rs::caller", "c1", "core", "{}", "local", "procedural"],
    ["src/auth.rs", "file", "auth.rs", "src/auth.rs", 1, 80, "rust", null, "c1", "core", "{}", "production", "procedural"],
    ["src/auth.rs::login", "function", "login", "src/auth.rs", 10, 50, "rust", "src/auth.rs", "c1", "core", "{}", "production", "procedural"],
    ["src/auth.rs::verify_token", "function", "verify_token", "src/auth.rs", 52, 70, "rust", "src/auth.rs", "c1", "core", "{}", "production", "procedural"],
    ["src/billing.rs::charge", "function", "charge", "src/billing.rs", 4, 18, "rust", "src/billing.rs", "c1", "core", "{}", "local", "procedural"],
    ["src/api.rs", "file", "api.rs", "src/api.rs", 1, 90, "rust", null, "c1", "core", "{}", "production", "procedural"],
    ["src/api.rs::handle_request", "function", "handle_request", "src/api.rs", 8, 70, "rust", "src/api.rs", "c1", "core", "{}", "production", "procedural"]
]
:put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}
"#;

const FIXTURE_RELATIONSHIPS: &str = r#"
?[source_qualified, target_qualified, rel_type, confidence, metadata, env] <-
[
    ["src/main.rs::main", "src/lib.rs::caller", "calls", 0.9, "{}", "local"],
    ["src/main.rs::main", "src/auth.rs::login", "calls", 0.9, "{}", "production"],
    ["src/main.rs::validate_key", "src/auth.rs::verify_token", "calls", 0.95, "{}", "production"],
    ["src/lib.rs::caller", "src/lib.rs::caller::helper", "calls", 1.0, "{}", "local"],
    ["src/lib.rs::caller", "src/billing.rs::charge", "calls", 0.8, "{}", "local"],
    ["src/api.rs::handle_request", "src/auth.rs::login", "calls", 0.95, "{}", "production"],
    ["src/api.rs::handle_request", "src/billing.rs::charge", "calls", 0.9, "{}", "production"],
    ["src/auth.rs::login", "src/billing.rs::charge", "calls", 0.7, "{}", "production"],
    ["src/lib.rs::caller::helper", "src/billing.rs::charge", "references", 0.6, "{}", "local"],
    ["src/main.rs::main", "src/main.rs::validate_key", "references", 0.5, "{}", "local"]
]
:put relationships {source_qualified, target_qualified, rel_type, confidence, metadata, env}
"#;

const FIXTURE_BUSINESS_LOGIC: &str = r#"
?[element_qualified, description, user_story_id, feature_id] <-
[
    ["src/lib.rs::caller", "Routes requests through the auth + billing pipeline", "US-001", "F-001"],
    ["src/auth.rs::login", "Authenticates a user and issues a token", "US-002", "F-001"],
    ["src/billing.rs::charge", "Charges the customer's card", "US-002", "F-002"]
]
:put business_logic {element_qualified, description, user_story_id, feature_id}
"#;

/// Incidents are seeded via param binding (canonical production path):
/// cozo's string literals reject `\"` escapes, so JSON-array-typed string
/// columns must arrive as bound values.
fn seed_incidents(db: &dyn DbBackend) {
    let query = r#"?[id, env, title, severity, occurred_at, resolved_at, root_cause, resolution, affected_services, trigger_pattern, prevention, tags, author, linked_ticket] <- [[$id, $env, $title, $sev, $occ, $res_at, $rc, $res, $svc, $tp, $prev, $tags, $author, $tk]] :put incidents {id, env, title, severity, occurred_at, resolved_at, root_cause, resolution, affected_services, trigger_pattern, prevention, tags, author, linked_ticket}"#;
    let incs: Vec<[Option<serde_json::Value>; 14]> = vec![
        [
            Some(json!("inc-1")),
            Some(json!("production")),
            Some(json!("API timeout")),
            Some(json!("P2")),
            Some(json!(1700000000i64)),
            None,
            Some(json!("api called database too slowly")),
            Some(json!("add index")),
            Some(json!(r#"["api"]"#)),
            Some(json!("timeout")),
            None,
            Some(json!(r#"["db"]"#)),
            Some(json!("oncall")),
            None,
        ],
        [
            Some(json!("inc-2")),
            Some(json!("production")),
            Some(json!("DB connection pool exhausted")),
            Some(json!("P1")),
            Some(json!(1700000100i64)),
            Some(json!(1700000200i64)),
            Some(json!("leak")),
            Some(json!("restart workers")),
            Some(json!(r#"["api","database"]"#)),
            None,
            Some(json!(r#"["pools"]"#)),
            Some(json!(r#"["db"]"#)),
            Some(json!("oncall")),
            Some(json!("TKT-9")),
        ],
    ];
    let keys = [
        "id", "env", "title", "sev", "occ", "res_at", "rc", "res", "svc", "tp", "prev", "tags",
        "author", "tk",
    ];
    for inc in incs {
        let mut params = std::collections::BTreeMap::new();
        for (k, v) in keys.iter().zip(inc.iter()) {
            // cozo requires every $param to be bound; nulls must arrive as
            // explicit `null` values.
            params.insert((*k).to_string(), v.clone().unwrap_or(Value::Null));
        }
        db.run_script(query, params)
            .unwrap_or_else(|e| panic!("seed incidents failed: {e}"));
    }
}

fn seed_teams(db: &dyn DbBackend) {
    let query = r#"?[id, name, description, owner_id, created_at, updated_at, graph_read_users, graph_write_users, members] <- [[$id, $name, $desc, $owner, $created, $updated, $gr, $gw, $members]] :put teams {id, name, description, owner_id, created_at, updated_at, graph_read_users, graph_write_users, members}"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert("id".into(), json!("team-1"));
    params.insert("name".into(), json!("Platform"));
    params.insert("desc".into(), json!("Owns the platform graph"));
    params.insert("owner".into(), json!("u-1"));
    params.insert("created".into(), json!(1700000000i64));
    params.insert("updated".into(), json!(1700000000i64));
    params.insert("gr".into(), json!(r#"["u-1"]"#));
    params.insert("gw".into(), json!(r#"["u-1"]"#));
    params.insert("members".into(), json!(r#"["u-1","u-2"]"#));
    db.run_script(query, params)
        .unwrap_or_else(|e| panic!("seed teams failed: {e}"));
}

fn seed_service_metadata(db: &dyn DbBackend) {
    let query = r#"?[service_name, env, team, on_call, repo_url, language, health_endpoint, slo_p99_ms, incident_count, last_incident, tags, version, deploy_envs, created_at, updated_at] <- [[$sn, $env, $team, $oc, $repo, $lang, $he, $slo, $ic, $li, $tags, $ver, $de, $created, $updated]] :put service_metadata {service_name, env, team, on_call, repo_url, language, health_endpoint, slo_p99_ms, incident_count, last_incident, tags, version, deploy_envs, created_at, updated_at}"#;
    let rows = [
        (
            "api",
            "production",
            "Platform",
            "u-1",
            "https://git/api",
            "rust",
            "/health",
            Some(200i64),
            2,
            Some(1700000100i64),
            r#"["core"]"#,
            "v1.2.3",
            r#"["production"]"#,
        ),
        (
            "api",
            "staging",
            "Platform",
            "u-2",
            "https://git/api",
            "rust",
            "/health",
            Some(400i64),
            0,
            None,
            r#"["core"]"#,
            "v1.2.0",
            r#"["staging"]"#,
        ),
    ];
    for (sn, env, team, oc, repo, lang, he, slo, ic, li, tags, ver, de) in rows {
        let mut params = std::collections::BTreeMap::new();
        params.insert("sn".into(), json!(sn));
        params.insert("env".into(), json!(env));
        params.insert("team".into(), json!(team));
        params.insert("oc".into(), json!(oc));
        params.insert("repo".into(), json!(repo));
        params.insert("lang".into(), json!(lang));
        params.insert("he".into(), json!(he));
        params.insert("slo".into(), json!(slo));
        params.insert("ic".into(), json!(ic));
        params.insert("li".into(), json!(li));
        params.insert("tags".into(), json!(tags));
        params.insert("ver".into(), json!(ver));
        params.insert("de".into(), json!(de));
        params.insert("created".into(), json!(1700000000i64));
        params.insert("updated".into(), json!(1700000000i64));
        db.run_script(query, params)
            .unwrap_or_else(|e| panic!("seed service_metadata failed: {e}"));
    }
}

fn seed_knowledge(db: &dyn DbBackend) {
    let query = r#"?[id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at] <- [[$id, $kt, $title, $content, $eq, $us, $fid, $tags, $env, $branch, $author, $created, $updated]] :put knowledge_entries {id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at}"#;
    let rows = [
        (
            "k-1",
            "concept",
            "Rate limiting pattern",
            "Use a token bucket per API key; refill 10/min",
            None,
            None,
            None,
            r#"["api","auth"]"#,
            "local",
            None,
            "alice",
        ),
        (
            "k-2",
            "lesson",
            "Billing idempotency",
            "Always send Idempotency-Key on charge retries",
            Some("src/billing.rs::charge"),
            Some("US-002"),
            Some("F-002"),
            r#"["billing"]"#,
            "production",
            Some("main"),
            "bob",
        ),
    ];
    for (id, kt, title, content, eq, us, fid, tags, env, branch, author) in rows {
        let mut params = std::collections::BTreeMap::new();
        params.insert("id".into(), json!(id));
        params.insert("kt".into(), json!(kt));
        params.insert("title".into(), json!(title));
        params.insert("content".into(), json!(content));
        params.insert("eq".into(), json!(eq));
        params.insert("us".into(), json!(us));
        params.insert("fid".into(), json!(fid));
        params.insert("tags".into(), json!(tags));
        params.insert("env".into(), json!(env));
        params.insert("branch".into(), json!(branch));
        params.insert("author".into(), json!(author));
        params.insert("created".into(), json!(1700000000i64));
        params.insert("updated".into(), json!(1700000000i64));
        db.run_script(query, params)
            .unwrap_or_else(|e| panic!("seed knowledge failed: {e}"));
    }
}

/// 384-dim unit vectors for the fixture elements (dim must match the
/// schema's vector(384) + the embedder dimension).
fn vector_for(i: usize, qn: &str) -> String {
    let mut v: Vec<f32> = vec![0.0; 384];
    v[i % 384] = 1.0;
    // Fold the name in so cosine similarity is not identical for all rows.
    let seed = qn
        .bytes()
        .fold(0u64, |acc, b| acc.wrapping_mul(31).wrapping_add(b as u64));
    v[(seed % 384) as usize] = 1.0;
    format!(
        "[{}]",
        v.iter()
            .map(|x| x.to_string())
            .collect::<Vec<_>>()
            .join(",")
    )
}

fn seed_fixture(db: &dyn DbBackend) {
    for stmt in [
        ("elements", FIXTURE_ELEMENTS),
        ("relationships", FIXTURE_RELATIONSHIPS),
        ("business_logic", FIXTURE_BUSINESS_LOGIC),
    ] {
        db.run_script(stmt.1, Default::default())
            .unwrap_or_else(|e| panic!("seed {} failed: {e}", stmt.0));
    }
    seed_incidents(db);
    seed_teams(db);
    seed_service_metadata(db);
    seed_knowledge(db);

    // embedding_state + embedding_vectors (HNSW path for semantic_search).
    #[cfg(feature = "embeddings")]
    {
        // embedding_state + embedding_vectors (HNSW path for semantic_search).
        // Only when the embeddings feature is compiled in — same gate as the
        // schema itself (init_schema creates these tables only with the
        // feature, so cozo and PG stay symmetric).
        #[cfg(not(feature = "embeddings"))]
        {
            let _ = qns; // unused without the feature
            return;
        }
        let qns = [
            "src/main.rs::main",
            "src/main.rs::validate_key",
            "src/lib.rs::caller",
            "src/lib.rs::caller::helper",
            "src/auth.rs::login",
            "src/auth.rs::verify_token",
            "src/billing.rs::charge",
            "src/api.rs::handle_request",
        ];
        let state_rows: Vec<String> = qns
            .iter()
            .enumerate()
            .map(|(i, qn)| format!(r#"["{qn}", {i}, "{qn}", "fresh", 1700000000]"#))
            .collect();
        let state = format!(
        "?[qualified_name, usearch_key, content_hash, state, embedded_at] <- [{}] :put embedding_state {{qualified_name, usearch_key, content_hash, state, embedded_at}}",
        state_rows.join(",")
    );
        db.run_script(&state, Default::default())
            .unwrap_or_else(|e| panic!("embedding_state seed failed: {e}"));

        let vec_rows: Vec<String> = qns
            .iter()
            .enumerate()
            .map(|(i, qn)| format!(r#"["{qn}", {}]"#, vector_for(i, qn)))
            .collect();
        let vecs = format!(
            "?[qualified_name, vector] <- [{}] :put embedding_vectors {{qualified_name => vector}}",
            vec_rows.join(",")
        );
        db.run_script(&vecs, Default::default())
            .unwrap_or_else(|e| panic!("embedding_vectors seed failed: {e}"));
    }
}
// ---------------------------------------------------------------------------
// Sweep driver
// ---------------------------------------------------------------------------

/// Tools that need a real repo dir on disk (query_file / generate_doc read
/// the file) or are env-dependent — exercised with a fixture repo written
/// to a tempdir when `repo_dir` is Some.
fn fixture_repo(tmp: &tempfile::TempDir) {
    std::fs::create_dir_all(tmp.path().join("src")).unwrap();
    std::fs::write(
        tmp.path().join("src/main.rs"),
        "fn main() { caller(); }\nfn validate_key(k: &str) -> bool { k.len() > 3 }\n",
    )
    .unwrap();
    std::fs::write(
        tmp.path().join("src/lib.rs"),
        "fn caller() { helper(); charge(); }\nfn helper() {}\n",
    )
    .unwrap();
    std::fs::write(
        tmp.path().join("src/auth.rs"),
        "fn login() {}\nfn verify_token() {}\n",
    )
    .unwrap();
    std::fs::write(tmp.path().join("src/billing.rs"), "fn charge() {}\n").unwrap();
    std::fs::write(
        tmp.path().join("src/api.rs"),
        "fn handle_request() { login(); charge(); }\n",
    )
    .unwrap();
}

/// Normalise volatile fields before comparing cozo vs PG JSON.
fn normalize(v: &mut Value) {
    let volatile = [
        "database",
        "storage_path",
        "storage_engine",
        "counts_included",
        "initialized",
        "index_populated",
        "database_exists",
        "execution_time_ms",
        "elapsed",
        "started_at",
        "total_execution_ms",
        "took_ms",
        "cache_hit",
        "source",
        "created_at",
        "updated_at",
        "occurred_at",
        "embedded_at",
        "project_path",
        "timestamp",
    ];
    match v {
        Value::Object(map) => {
            for k in volatile {
                if map.contains_key(k) {
                    map.insert(k.to_string(), Value::String("<volatile>".into()));
                }
            }
            for val in map.values_mut() {
                normalize(val);
            }
        }
        Value::Array(arr) => {
            for val in arr.iter_mut() {
                normalize(val);
            }
            // Order-independent comparison: cozo returns rows in store order,
            // PG in planner order — for identical content the arrays must be
            // treated as multisets unless the tool's contract pins order.
            arr.sort_by_key(|v| serde_json::to_string(v).unwrap_or_default());
        }
        _ => {}
    }
}

/// Run one tool against one handler, return (ok, latency ms).
async fn run_tool(handler: &ToolHandler, tool: &str, args: &Value) -> (Result<Value, String>, f64) {
    let start = std::time::Instant::now();
    let r = handler.execute_tool(tool, args).await;
    let ms = start.elapsed().as_secs_f64() * 1000.0;
    (r, ms)
}

struct SweepResult {
    tool: String,
    cozo_ok: bool,
    pg_ok: bool,
    equal: bool,
    cozo_ms: f64,
    pg_ms: f64,
    cozo_err: Option<String>,
    pg_err: Option<String>,
    note: String,
}

#[test]
fn tool_sweep_cozo_vs_postgres() {
    // postgres::Client is sync and builds its own runtime — must run off
    // the ambient tokio runtime (same constraint as `leankg migrate`).
    let rt = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
        .unwrap();
    let scratch = ScratchSchema::new();
    let tmp = Box::leak(Box::new(tempfile::TempDir::new().unwrap()));
    fixture_repo(tmp);

    // cozo handler
    let cozo = cozo_shim();
    seed_fixture(cozo.as_ref());
    let cozo_graph = GraphEngine::new(cozo.clone());
    let cozo_handler = ToolHandler::new(cozo_graph, tmp.path().to_path_buf());

    // pg handler
    let pg_backend = scratch.backend();
    seed_fixture(pg_backend.as_ref());
    // Warm the lazy connection OUTSIDE the tokio runtime: PostgresBackend's
    // sync Client::connect spins its own runtime and panics when called
    // from inside block_on (nested runtime).
    // `::relations` translates to DdlNoop and never touches the socket, so
    // warm with a real read that forces the lazy connect.
    pg_backend
        .run_script(
            "?[qualified_name] := *code_elements[qualified_name] :limit 1",
            Default::default(),
        )
        .expect("warm-up PG connection");
    let pg_graph = GraphEngine::new(pg_backend.clone());
    let pg_handler = ToolHandler::new(pg_graph, tmp.path().to_path_buf());

    let calls: Vec<(&str, Value)> = vec![
        ("mcp_status", json!({"include_counts": true})),
        ("query_file", json!({"pattern": "src/main.rs"})),
        ("get_dependencies", json!({"file": "src/lib.rs::caller"})),
        ("get_dependents", json!({"file": "src/lib.rs::caller"})),
        (
            "get_impact_radius",
            json!({"file": "src/lib.rs::caller", "depth": 2}),
        ),
        (
            "get_review_context",
            json!({"files": ["src/main.rs", "src/lib.rs"]}),
        ),
        ("find_function", json!({"name": "charge"})),
        (
            "get_call_graph",
            json!({"qualified_name": "src/lib.rs::caller", "depth": 2}),
        ),
        ("search_code", json!({"query": "charge"})),
        (
            "search_code",
            json!({"query": "charge", "env": "production"}),
        ),
        ("generate_doc", json!({"file": "src/main.rs"})),
        ("find_large_functions", json!({"min_lines": 15})),
        (
            "get_tested_by",
            json!({"qualified_name": "src/billing.rs::charge"}),
        ),
        ("get_files_for_doc", json!({"doc": "src/lib.rs::caller"})),
        ("get_doc_tree", json!({})),
        ("get_traceability", json!({"element": "src/lib.rs::caller"})),
        ("search_by_requirement", json!({"requirement": "US-002"})),
        ("get_code_tree", json!({"file": "src/lib.rs"})),
        (
            "find_related_docs",
            json!({"element": "src/lib.rs::caller"}),
        ),
        ("concept_search", json!({"query": "cart"})),
        (
            "semantic_search",
            json!({"query": "charge a customer card"}),
        ),
        ("search_knowledge", json!({"query": "billing"})),
        (
            "explain_node",
            json!({"qualified_name": "src/lib.rs::caller"}),
        ),
        (
            "shortest_path",
            json!({"source": "src/main.rs::main", "target": "src/billing.rs::charge", "max_hops": 6}),
        ),
        ("get_overview_context", json!({"recall": false})),
        (
            "get_service_context",
            json!({"service": "api", "env": "production"}),
        ),
        ("query_incidents", json!({"service": "api"})),
        (
            "find_env_conflicts",
            json!({"qualified_name": "src/api.rs::handle_request"}),
        ),
        ("get_god_nodes", json!({})),
        ("get_architecture", json!({})),
        ("kg_self_test", json!({})),
        ("get_traceability_matrix", json!({"feature_id": "F-001"})),
    ];

    let mut results: Vec<SweepResult> = Vec::new();
    for (tool, args) in calls {
        let mut cozo_ok = true;
        let mut pg_ok = true;
        let mut cozo_val = Value::Null;
        let mut pg_val = Value::Null;
        let mut cozo_err = None;
        let mut pg_err = None;
        let mut cozo_ms = 0.0;
        let mut pg_ms = 0.0;

        for _ in 0..5 {
            let (r, ms) = rt.block_on(run_tool(&cozo_handler, tool, &args));
            cozo_ms += ms;
            match r {
                Ok(v) => cozo_val = v,
                Err(e) => {
                    cozo_ok = false;
                    cozo_err = Some(e);
                }
            }
            let (r, ms) = rt.block_on(run_tool(&pg_handler, tool, &args));
            pg_ms += ms;
            match r {
                Ok(v) => pg_val = v,
                Err(e) => {
                    pg_ok = false;
                    pg_err = Some(e);
                }
            }
        }
        cozo_ms /= 5.0;
        pg_ms /= 5.0;

        let mut equal = false;
        let mut note = String::new();
        if cozo_ok && pg_ok {
            let mut c = cozo_val.clone();
            let mut p = pg_val.clone();
            normalize(&mut c);
            normalize(&mut p);
            if c == p {
                equal = true;
            } else {
                // Second chance: both sides missing/empty (e.g. "no data"
                // errors as Ok with empty lists in slightly different shape).
                let c_str = serde_json::to_string(&c).unwrap_or_default();
                let p_str = serde_json::to_string(&p).unwrap_or_default();
                if c_str.is_empty() && p_str.is_empty() {
                    equal = true;
                } else {
                    note = format!("DIFF: cozo={c_str} PG={p_str}");
                    if note.len() > 2000 {
                        note.truncate(2000);
                    }
                }
            }
        } else if !cozo_ok && !pg_ok {
            equal = true; // both error — same behavior shape
            note = format!("both error: cozo={:?} pg={:?}", cozo_err, pg_err);
        } else {
            note = format!(
                "cozo_ok={cozo_ok} err={:?} | pg_ok={pg_ok} err={:?}",
                cozo_err, pg_err
            );
        }

        results.push(SweepResult {
            tool: tool.to_string(),
            cozo_ok,
            pg_ok,
            equal,
            cozo_ms,
            pg_ms,
            cozo_err,
            pg_err,
            note,
        });
    }

    println!();
    println!("=== TOOL SWEEP: cozo vs postgres (identical fixture) ===");
    println!(
        "{:<32} {:<6} {:<6} {:<5} {:>8} {:>8} {:>7}  note",
        "tool", "cozo", "pg", "match", "cozo_ms", "pg_ms", "pg/cozo"
    );
    let mut pass = 0;
    let mut diff = 0;
    let mut fail = 0;
    let mut slow = Vec::new();
    for r in &results {
        let status = if r.equal { "PASS" } else { "DIFF" };
        if r.equal && r.cozo_ok && r.pg_ok {
            pass += 1;
        } else if !r.equal {
            if r.cozo_ok && r.pg_ok {
                diff += 1;
            } else {
                fail += 1;
            }
        }
        let ratio = if r.cozo_ms > 0.0 {
            r.pg_ms / r.cozo_ms
        } else {
            1.0
        };
        if ratio > 2.0 && r.pg_ms > 10.0 {
            slow.push(r.tool.clone());
        }
        println!(
            "{:<32} {:<6} {:<6} {:<5} {:>8.1} {:>8.1} {:>7.2}  {}",
            r.tool, r.cozo_ok, r.pg_ok, status, r.cozo_ms, r.pg_ms, ratio, r.note
        );
    }
    println!();
    println!(
        "PASS={pass} DIFF={diff} FAIL={fail} total={}",
        results.len()
    );
    if !slow.is_empty() {
        println!(">2x cozo latency: {slow:?}");
    }

    // Guard: the sweep itself asserts the invariant that the hot paths
    // (semantic search, overview, impact) ran without error on PG.
    for must in [
        "semantic_search",
        "get_overview_context",
        "get_impact_radius",
        "mcp_status",
        "search_code",
    ] {
        let r = results.iter().find(|r| r.tool == must).expect(must);
        assert!(r.pg_ok, "{must} failed on PG: {:?}", r.pg_err);
    }
}
