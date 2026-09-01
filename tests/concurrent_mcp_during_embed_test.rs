//! Concurrent RO MCP queries during worker index/embed lock + writes.
//!
//! Proves query-only MCP (`with_read_only(true)`) stays responsive while a
//! worker holds `index_advisory_lock` and performs batched embed-style
//! `import_relations` writes into `embedding_vectors`.
//!
//! Skip when `LEANKG_PG_URL` is unset (same pattern as `embeddings::state`
//! PG e2e helpers). With PG up:
//!
//! ```text
//! LEANKG_PG_URL=postgresql://postgres:postgres@localhost:5433/leankg \
//!   cargo test --test concurrent_mcp_during_embed_test -- --nocapture
//! ```

#[allow(unused_imports)]
use leankg::db::backend::pg_connect;
use leankg::db::backend::{index_advisory_lock, init_db, DataValue, NamedRows, PostgresBackend};
use leankg::db::models::CodeElement;
use leankg::graph::GraphEngine;
use leankg::mcp::server::MCPServer;
use serde_json::{json, Map};
use std::collections::BTreeMap;
use std::sync::atomic::{AtomicBool, AtomicU32, Ordering};
use std::sync::{Arc, Mutex, OnceLock};
use std::time::{Duration, Instant};
use tempfile::TempDir;

/// Serializes `LEANKG_PG_URL` mutation — env is process-global.
static ENV_LOCK: OnceLock<Mutex<()>> = OnceLock::new();
static SCHEMA_COUNTER: AtomicU32 = AtomicU32::new(0);

const VEC_DIM: usize = 384;

/// Tunable for high-RTT managed Postgres (defaults assume local-latency PG).
fn env_secs(var: &str, default: u64) -> Duration {
    Duration::from_secs(
        std::env::var(var)
            .ok()
            .and_then(|v| v.parse().ok())
            .unwrap_or(default),
    )
}

fn query_timeout() -> Duration {
    // 5s assumes local-latency PG; a single WAN RTT spike against managed
    // remote Postgres can exceed it while the worker holds the advisory
    // lock. Default to 15s when LEANKG_PG_URL points off-host.
    let default = match std::env::var("LEANKG_PG_URL") {
        Ok(url) if !url.is_empty() && !url.contains("localhost") && !url.contains("127.0.0.1") => {
            15
        }
        _ => 5,
    };
    env_secs("LEANKG_TEST_QUERY_TIMEOUT_SECS", default)
}

fn worker_hold() -> Duration {
    env_secs("LEANKG_TEST_WORKER_HOLD_SECS", 2)
}

fn require_pg_url() -> Option<String> {
    match std::env::var("LEANKG_PG_URL") {
        Ok(v) if !v.trim().is_empty() => Some(v),
        _ => {
            eprintln!("skipping: LEANKG_PG_URL not set");
            None
        }
    }
}

/// Pins `LEANKG_PG_URL` to a migrated scratch schema for the test body.
/// Admin connection is forgotten (same pattern as `full_index_wipe_test`) so
/// Drop never closes a sync `postgres::Client` inside a tokio runtime.
struct ScopedPg {
    base_url: String,
    schema: String,
    prev_url: Option<String>,
    _env_guard: std::sync::MutexGuard<'static, ()>,
}

impl ScopedPg {
    fn enter(base_url: &str) -> Self {
        let schema = format!(
            "leankg_conc_{}_{}",
            std::process::id(),
            SCHEMA_COUNTER.fetch_add(1, Ordering::Relaxed)
        );
        let mut admin =
            pg_connect(base_url).unwrap_or_else(|e| panic!("cannot connect to {base_url}: {e}"));
        admin
            .batch_execute(&format!("DROP SCHEMA IF EXISTS {schema} CASCADE"))
            .expect("drop schema");
        admin
            .batch_execute(&format!("CREATE SCHEMA {schema}"))
            .expect("create schema");
        admin
            .batch_execute(&format!("SET search_path TO {schema}, public"))
            .expect("set search_path");
        leankg::db::pg::migrations::run_migrations(&mut admin).expect("run_migrations");
        // Keep the admin connection alive so the schema survives the body.
        std::mem::forget(admin);

        let env_guard = ENV_LOCK.get_or_init(|| Mutex::new(())).lock().unwrap();
        let prev_url = std::env::var("LEANKG_PG_URL").ok();
        let sep = if base_url.contains('?') { '&' } else { '?' };
        let scoped = format!("{base_url}{sep}options=-csearch_path%3D{schema}%2Cpublic");
        std::env::set_var("LEANKG_PG_URL", &scoped);

        ScopedPg {
            base_url: base_url.to_string(),
            schema,
            prev_url,
            _env_guard: env_guard,
        }
    }

    fn cleanup(self) {
        let schema = self.schema.clone();
        let base = self.base_url.clone();
        // Drop runs env restore; schema DROP is sync PG — must be off-runtime
        // or under block_in_place (caller responsibility).
        drop(self);
        if let Ok(mut admin) = pg_connect(&base) {
            let _ = admin.batch_execute(&format!("DROP SCHEMA IF EXISTS {schema} CASCADE"));
        }
    }
}

impl Drop for ScopedPg {
    fn drop(&mut self) {
        match &self.prev_url {
            Some(u) => std::env::set_var("LEANKG_PG_URL", u),
            None => std::env::remove_var("LEANKG_PG_URL"),
        }
    }
}

fn seed_elem(qn: &str, name: &str) -> CodeElement {
    CodeElement {
        qualified_name: qn.to_string(),
        element_type: "function".to_string(),
        name: name.to_string(),
        file_path: "/src/lib.rs".to_string(),
        line_start: 1,
        line_end: 10,
        language: "rust".to_string(),
        parent_qualified: Some("/src/lib.rs".to_string()),
        cluster_id: None,
        cluster_label: None,
        metadata: serde_json::json!({}),
        env: "local".to_string(),
    }
}

fn named_vectors(pairs: &[(String, Vec<f32>)]) -> BTreeMap<String, NamedRows> {
    let mut rows: Vec<Vec<DataValue>> = Vec::with_capacity(pairs.len());
    for (qn, vec) in pairs {
        let mut row = Vec::with_capacity(2);
        row.push(DataValue::Str(qn.as_str().into()));
        let list: Vec<DataValue> = vec.iter().map(|&f| DataValue::from(f as f64)).collect();
        row.push(DataValue::List(list));
        rows.push(row);
    }
    let named = NamedRows::new(
        vec!["qualified_name".to_string(), "vector".to_string()],
        rows,
    );
    let mut map = BTreeMap::new();
    map.insert("embedding_vectors".to_string(), named);
    map
}

fn unit_vector(seed: u32) -> Vec<f32> {
    let mut v = vec![0.0f32; VEC_DIM];
    let idx = (seed as usize) % VEC_DIM;
    v[idx] = 1.0;
    v
}

fn assert_not_ro_or_conn_error(tool: &str, result: Result<serde_json::Value, String>) {
    match result {
        Ok(_) => {}
        Err(msg) => {
            assert!(
                !msg.to_lowercase().contains("read-only mode"),
                "{tool} must not hit RO gate during concurrent worker: {msg}"
            );
            let lower = msg.to_lowercase();
            assert!(
                !lower.contains("connection")
                    && !lower.contains("closed")
                    && !lower.contains("timeout")
                    && !lower.contains("pool"),
                "{tool} must not fail with connection/pool errors during worker embed: {msg}"
            );
            panic!("{tool} failed unexpectedly: {msg}");
        }
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn concurrent_ro_mcp_queries_succeed_while_worker_holds_lock_and_writes() {
    let Some(base_url) = require_pg_url() else {
        return;
    };

    // Sync postgres::Client must not run on a tokio worker without
    // block_in_place (nested-runtime panic).
    let (scoped, _tmp, server) = tokio::task::block_in_place(|| {
        let scoped = ScopedPg::enter(&base_url);

        let tmp = TempDir::new().unwrap();
        let db_path = tmp.path().join("leankg");
        std::fs::create_dir_all(&db_path).unwrap();

        // Seed minimal graph via RW backend (same LEANKG_PG_URL / scratch schema).
        {
            let db = init_db(&db_path).expect("init_db seed");
            let ge = GraphEngine::new(db);
            ge.insert_elements_with(
                &[
                    seed_elem("/src/lib.rs::helper", "helper"),
                    seed_elem("/src/lib.rs::main", "main"),
                ],
                true,
            )
            .expect("seed elements");
        }

        let server = MCPServer::new(db_path).with_read_only(true);
        assert!(server.is_read_only());
        (scoped, tmp, server)
    });

    let stop = Arc::new(AtomicBool::new(false));
    let stop_w = Arc::clone(&stop);
    let batches_done = Arc::new(AtomicU32::new(0));
    let batches_w = Arc::clone(&batches_done);

    // Worker: hold index advisory lock + slow batched embed writes.
    let worker = std::thread::spawn(move || {
        let lock_path = _tmp.path().join("leankg");
        let _lock = index_advisory_lock("local", lock_path.to_str().expect("utf8 db path"))
            .expect("index_advisory_lock");
        let pg = PostgresBackend::from_env().expect("worker PostgresBackend");
        let mut n = 0u32;
        while !stop_w.load(Ordering::SeqCst) {
            let pairs: Vec<(String, Vec<f32>)> = (0..32)
                .map(|i| {
                    (
                        format!("/embed/batch{n}/item{i}"),
                        unit_vector(n.wrapping_mul(32).wrapping_add(i)),
                    )
                })
                .collect();
            pg.import_relations(named_vectors(&pairs))
                .unwrap_or_else(|e| panic!("embed write batch {n}: {e}"));
            batches_w.fetch_add(1, Ordering::SeqCst);
            n += 1;
            std::thread::sleep(Duration::from_millis(40));
        }
    });

    // Give the worker a moment to acquire the lock and start writing.
    tokio::time::sleep(Duration::from_millis(80)).await;

    let deadline = Instant::now() + worker_hold();
    let mut status_ok = 0u32;
    let mut search_ok = 0u32;

    while Instant::now() < deadline {
        let t0 = Instant::now();
        let status = server.execute_tool_pub("mcp_status", Map::new()).await;
        assert!(
            t0.elapsed() < query_timeout(),
            "mcp_status exceeded {:?}: {:?}",
            query_timeout(),
            t0.elapsed()
        );
        assert_not_ro_or_conn_error("mcp_status", status);
        status_ok += 1;

        let mut args = Map::new();
        args.insert("query".into(), json!("helper"));
        args.insert("use_ontology".into(), json!(false));
        let t1 = Instant::now();
        let search = server.execute_tool_pub("search_code", args).await;
        assert!(
            t1.elapsed() < query_timeout(),
            "search_code exceeded {:?}: {:?}",
            query_timeout(),
            t1.elapsed()
        );
        assert_not_ro_or_conn_error("search_code", search);
        search_ok += 1;
    }

    stop.store(true, Ordering::SeqCst);
    worker.join().expect("worker thread panicked");

    let batches = batches_done.load(Ordering::SeqCst);
    assert!(
        batches >= 1,
        "worker must complete at least one embed write batch (got {batches})"
    );
    // At least one successful RO query per kind during the hold. The old
    // `>= 2` assumed local-latency PG (2s hold fits ~4+ round trips); over
    // managed remote Postgres a single query can eat most of the window.
    assert!(
        status_ok >= 1 && search_ok >= 1,
        "expected successful RO queries during worker hold \
         (mcp_status={status_ok}, search_code={search_ok}, batches={batches})"
    );
    eprintln!(
        "concurrent RO MCP ok: mcp_status={status_ok} search_code={search_ok} \
         worker_batches={batches}"
    );

    tokio::task::block_in_place(|| scoped.cleanup());
}
