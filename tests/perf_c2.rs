//! Hackathon Cycle-2 R2b — performance/wedge regression tests (perf_c2).
//!
//! Root causes pinned here (R2b profiling, live 93k-edge corpus):
//!
//! 1. check_consistency / temporal_query / agent_focus latency was NOT
//!    per-element DB round trips — engine-level calls finish in ~5s on the
//!    real corpus. The wedge is `TokenBudget::truncate_value`
//!    (src/mcp/token_budget.rs), applied to EVERY tool response
//!    (handler.rs): array truncation clones + re-serializes the WHOLE array
//!    once per popped item (O(n²)) — 24k findings / 93k relationships means
//!    minutes of pure CPU inside a tokio worker (no await point), which is
//!    also the N4 internal-watchdog cascade source.
//!
//! 2. agent_focus additionally pays one remote-PG round trip per 500-QN
//!    chunk (~27 queries on a 13k-element focus) before the response is
//!    even built.
//!
//! 3. Pool starvation: a tool whose watchdog expires while its pooled PG
//!    connection is still checked out must not starve later tools beyond
//!    LEANKG_PG_POOL_WAIT_MS; slots must return promptly when the holder
//!    finishes OR when its future is cancelled at an await point.
//!
//! Run:
//! ```bash
//! set -a; source ../.env; set +a   # LEANKG_PG_URL (+ CA cert)
//! export CARGO_TARGET_DIR=/tmp/opencode/t-perf
//! # always-on token-budget + pool tests need LEANKG_PG_URL for pool tests;
//! # corpus probes additionally need LEANKG_R2_PROBE_SCHEMA=<populated schema>
//! cargo test --release --test perf_c2 -- --test-threads=1 --nocapture
//! ```

use leankg::db::backend::{pg_connect, ClientPool, PostgresBackend};
use leankg::graph::GraphEngine;
use leankg::mcp::token_budget::TokenBudget;
use serde_json::json;
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

static SCHEMA_COUNTER: AtomicU32 = AtomicU32::new(0);
/// Serialize tests that mutate process env (LEANKG_PG_POOL_WAIT_MS etc.).
static ENV_LOCK: Mutex<()> = Mutex::new(());

fn pg_url() -> Option<String> {
    match std::env::var("LEANKG_PG_URL") {
        Ok(v)
            if !v.trim().is_empty()
                && !v.contains("localhost:5433")
                && !v.contains("127.0.0.1:5433") =>
        {
            Some(v)
        }
        _ => None,
    }
}

fn backend_for_schema(schema: &str) -> Arc<PostgresBackend> {
    let base = pg_url().expect("LEANKG_PG_URL");
    let sep = if base.contains('?') { '&' } else { '?' };
    Arc::new(PostgresBackend {
        pg_url: format!("{base}{sep}options=-csearch_path%3D{schema}%2Cpublic"),
        schema: Some(schema.to_string()),
        pool: Arc::new(ClientPool::new(4)),
        ro_pool: Arc::new(ClientPool::new(4)),
        read_only: false,
        write_bus: None,
    })
}

/// Scratch PG schema, dropped on drop.
struct ScratchSchema {
    admin: postgres::Client,
    name: String,
}

impl ScratchSchema {
    fn new() -> Self {
        let base = pg_url().expect("LEANKG_PG_URL must be set for PG perf tests");
        let name = format!(
            "leankg_perf_{}_{}",
            std::process::id(),
            SCHEMA_COUNTER.fetch_add(1, Ordering::Relaxed)
        );
        let mut admin = pg_connect(&base).expect("admin connect");
        admin
            .batch_execute(&format!("DROP SCHEMA IF EXISTS {name} CASCADE"))
            .unwrap();
        admin
            .batch_execute(&format!("CREATE SCHEMA {name}"))
            .unwrap();
        leankg::db::pg::migrations::run_migrations(&mut admin).unwrap();
        Self { admin, name }
    }

    fn engine(&self) -> GraphEngine {
        GraphEngine::new(backend_for_schema(&self.name))
    }
}

impl Drop for ScratchSchema {
    fn drop(&mut self) {
        let _ = self
            .admin
            .batch_execute(&format!("DROP SCHEMA IF EXISTS {} CASCADE", self.name));
    }
}

/// ~200 elements across 10 files + ~5k edges whose TARGETS are missing →
/// check_consistency reports ~5k BROKEN findings (the exact payload shape
/// that wedged the server via quadratic token-budget truncation).
///
/// Seeding goes through the admin client with explicit `::jsonb` casts —
/// the generic NamedRows insert path binds JSONB as String, which PG
/// rejects.
fn seed_fixture(scratch: &mut ScratchSchema, elements: usize, edges: usize) {
    let mut sql = String::from("BEGIN;\n");
    for i in 0..elements {
        sql.push_str(&format!(
            "INSERT INTO code_elements (qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer) \
             VALUES ('./src/mod{f}.rs::fn_{i}', 'function', 'fn_{i}', './src/mod{f}.rs', {i}, {j}, 'rust', NULL, NULL, NULL, '{{}}'::jsonb, 'local', 'procedural');\n",
            f = i / 20,
            j = i + 10,
        ));
    }
    for e in 0..edges {
        let target = if e % 2 == 0 {
            format!(
                "./src/mod{}.rs::fn_{}",
                e % ((elements / 20).max(1)),
                e % elements
            )
        } else {
            format!("./src/ghost.rs::missing_{e}")
        };
        sql.push_str(&format!(
            "INSERT INTO relationships (source_qualified, target_qualified, rel_type, confidence, metadata, env) \
             VALUES ('./src/mod{s}.rs::fn_{s}', '{target}', 'calls', 1.0, '{{}}'::jsonb, 'local');\n",
            s = e % ((elements / 20).max(1)),
        ));
    }
    sql.push_str("COMMIT;");
    scratch.admin.batch_execute(&sql).expect("seed fixture");
}

/// The exact handler-shaped payload check_consistency returns
/// (src/mcp/handler.rs::check_consistency).
fn consistency_payload(report: &leankg::graph::query::ConsistencyReport) -> serde_json::Value {
    json!({
        "total_relationships": report.total_relationships,
        "broken": report.broken,
        "stale": report.stale,
        "findings": report.findings,
    })
}

// ===========================================================================
// 1. Token-budget unit RED tests — always run, no PG required.
// ===========================================================================

#[test]
fn token_budget_truncates_consistency_findings_under_5s() {
    let findings: Vec<serde_json::Value> = (0..4_000)
        .map(|i| {
            json!({
                "severity": "BROKEN",
                "source": format!("./src/mod{}.rs::fn_{}", i % 200, i % 4000),
                "target": format!("./src/ghost.rs::missing_{i}"),
                "rel_type": "calls",
                "message": "target element missing from code_elements",
            })
        })
        .collect();
    let payload = json!({
        "total_relationships": findings.len(),
        "broken": findings.len(),
        "stale": 0,
        "findings": findings,
    });

    let t0 = Instant::now();
    let out = TokenBudget::apply(payload, "check_consistency");
    let elapsed = t0.elapsed();
    eprintln!("token_budget apply(4k findings): {elapsed:?}");
    assert!(
        elapsed < Duration::from_secs(5),
        "quadratic truncation: apply took {elapsed:?} (>5s)"
    );
    assert!(out.get("findings").is_some(), "primary key preserved");
    assert!(out.get("_token_budget").is_some(), "budget marker present");
}

#[test]
fn token_budget_truncates_relationship_rows_under_5s() {
    // temporal_query returns up to 93k rows on the live corpus; 8k here keeps
    // the RED run bounded while still proving the loop is linear after GREEN.
    let rels: Vec<serde_json::Value> = (0..8_000)
        .map(|i| {
            json!({
                "source_qualified": format!("./src/a{}.rs::fn_{}", i % 40, i),
                "target_qualified": format!("./src/b{}.rs::fn_{}", i % 40, i),
                "rel_type": "calls",
                "confidence": 1.0,
                "metadata": {},
            })
        })
        .collect();
    let payload = json!({ "at": 1_800_000_000i64, "count": rels.len(), "relationships": rels });

    let t0 = Instant::now();
    let out = TokenBudget::apply(payload, "temporal_query");
    let elapsed = t0.elapsed();
    eprintln!("token_budget apply(8k relationships): {elapsed:?}");
    assert!(
        elapsed < Duration::from_secs(5),
        "quadratic truncation: apply took {elapsed:?} (>5s)"
    );
    assert!(out.get("relationships").is_some());
    assert!(out.get("_token_budget").is_some());
}

#[test]
fn token_budget_small_responses_untouched() {
    let payload = json!({ "broken": 0, "findings": [], "stale": 0, "total_relationships": 0 });
    let out = TokenBudget::apply(payload.clone(), "check_consistency");
    assert_eq!(
        out, payload,
        "under-budget responses pass through unchanged"
    );
}

// ===========================================================================
// 2. Full-path fixture perf tests (live PG, scratch schema).
//    Wall-clock budget covers engine scan + handler-shape payload +
//    TokenBudget::apply — i.e., everything the MCP tool does minus HTTP.
// ===========================================================================

#[test]
fn perf_c2_fixture_check_consistency_full_path_under_15s() {
    let Some(_) = pg_url() else {
        eprintln!("skipping: LEANKG_PG_URL not set");
        return;
    };
    let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
    let mut scratch = ScratchSchema::new();
    let engine = scratch.engine();
    seed_fixture(&mut scratch, 200, 5_000);

    let t0 = Instant::now();
    let report = engine.check_consistency().expect("consistency");
    let payload = consistency_payload(&report);
    let out = TokenBudget::apply(payload, "check_consistency");
    let elapsed = t0.elapsed();
    eprintln!(
        "fixture check_consistency full path: broken={} stale={} in {elapsed:?}",
        report.broken, report.stale
    );
    assert!(out.get("findings").is_some());
    assert!(
        elapsed < Duration::from_secs(15),
        "check_consistency full path took {elapsed:?} (>15s)"
    );
}

#[test]
fn perf_c2_fixture_temporal_query_full_path_under_15s() {
    let Some(_) = pg_url() else {
        eprintln!("skipping: LEANKG_PG_URL not set");
        return;
    };
    let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
    let mut scratch = ScratchSchema::new();
    let engine = scratch.engine();
    seed_fixture(&mut scratch, 200, 5_000);

    let t0 = Instant::now();
    let rels = engine.temporal_query(1_800_000_000).expect("temporal");
    let payload = json!({
        "at": 1_800_000_000i64,
        "count": rels.len(),
        "relationships": rels,
    });
    let out = TokenBudget::apply(payload, "temporal_query");
    let elapsed = t0.elapsed();
    eprintln!(
        "fixture temporal_query full path: {} rels in {elapsed:?}",
        rels.len()
    );
    assert!(out.get("relationships").is_some());
    assert!(
        elapsed < Duration::from_secs(15),
        "temporal_query full path took {elapsed:?} (>15s)"
    );
}

// ===========================================================================
// 3. Live-corpus probes (95k edges) — gated on LEANKG_R2_PROBE_SCHEMA.
//    Used for before/after timing evidence against the real hackathon
//    worktree schema.
// ===========================================================================

fn r2_engine() -> Option<GraphEngine> {
    let schema = std::env::var("LEANKG_R2_PROBE_SCHEMA").ok()?;
    assert!(
        pg_url().is_some(),
        "LEANKG_PG_URL required with probe schema"
    );
    Some(GraphEngine::new(backend_for_schema(&schema)))
}

#[test]
fn perf_c2_corpus_check_consistency_full_path_under_15s() {
    let Some(engine) = r2_engine() else {
        eprintln!("skipping: LEANKG_R2_PROBE_SCHEMA not set");
        return;
    };
    let t0 = Instant::now();
    let report = engine.check_consistency().expect("consistency");
    let payload = consistency_payload(&report);
    let out = TokenBudget::apply(payload, "check_consistency");
    let elapsed = t0.elapsed();
    eprintln!(
        "corpus check_consistency full path: broken={} stale={} total={} in {elapsed:?}",
        report.broken, report.stale, report.total_relationships
    );
    assert!(out.get("findings").is_some());
    assert!(
        elapsed < Duration::from_secs(15),
        "corpus check_consistency full path took {elapsed:?} (>15s)"
    );
}

#[test]
fn perf_c2_corpus_temporal_query_full_path_under_15s() {
    let Some(engine) = r2_engine() else {
        eprintln!("skipping: LEANKG_R2_PROBE_SCHEMA not set");
        return;
    };
    let t0 = Instant::now();
    let rels = engine.temporal_query(1_800_000_000).expect("temporal");
    let payload = json!({
        "at": 1_800_000_000i64,
        "count": rels.len(),
        "relationships": rels,
    });
    let out = TokenBudget::apply(payload, "temporal_query");
    let elapsed = t0.elapsed();
    eprintln!(
        "corpus temporal_query full path: {} rels in {elapsed:?}",
        rels.len()
    );
    assert!(out.get("relationships").is_some());
    assert!(
        elapsed < Duration::from_secs(15),
        "corpus temporal_query full path took {elapsed:?} (>15s)"
    );
}

#[test]
fn perf_c2_corpus_agent_focus_full_path_under_30s() {
    let Some(engine) = r2_engine() else {
        eprintln!("skipping: LEANKG_R2_PROBE_SCHEMA not set");
        return;
    };
    // Broad persona (no filters) = worst case: every element focused, and
    // every intra-focus relationship fetched before the response builds.
    let persona = leankg::graph::query::AgentPersona {
        name: "perf-c2-probe".into(),
        description: String::new(),
        focus_areas: vec![],
        path_filters: vec![],
        cluster_id: None,
        element_types: vec![],
    };
    let t0 = Instant::now();
    let focus = engine.agent_focus(&persona).expect("agent_focus");
    let payload = json!({
        "agent": focus.agent,
        "element_count": focus.elements.len(),
        "relationship_count": focus.relationships.len(),
        "elements": focus.elements,
        "relationships": focus.relationships,
    });
    let out = TokenBudget::apply(payload, "agent_focus");
    let elapsed = t0.elapsed();
    eprintln!(
        "corpus agent_focus full path: {} elements / {} rels in {elapsed:?}",
        focus.elements.len(),
        focus.relationships.len()
    );
    assert!(out.get("elements").is_some());
    assert!(
        elapsed < Duration::from_secs(30),
        "corpus agent_focus full path took {elapsed:?} (>30s)"
    );
}
