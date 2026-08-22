//! Hackathon R2 — engine-layer bug regression tests.
//!
//! BUG-B: dynamic ontology concepts (source:"dynamic", added via MCP
//! add_ontology_concept) must survive an engine restart: a NEW GraphEngine
//! instance over the same DB must be able to look the concept up by GID and
//! delete it. Before the fix, the lookup returned nothing even though the row
//! was persisted (R1 sweep evidence: delete_ontology_concept →
//! "Element not found" + kg_ontology_status dynamic_concepts:0 after restart,
//! while psql showed the rows present in Postgres).
//!
//! Run (SQLite part always; PG part when LEANKG_PG_URL is set):
//! ```bash
//! set -a; source ../.env; set +a
//! cargo test --release --test hackathon_r2_engine -- --test-threads=1
//! ```

use leankg::db::backend::{pg_connect, ClientPool, PostgresBackend};
use leankg::graph::GraphEngine;
use leankg::ontology::{
    concept_nodes_to_elements, ConceptMetadata, ConceptNode, OntologyQueryEngine,
};
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::Arc;

static SCHEMA_COUNTER: AtomicU32 = AtomicU32::new(0);
/// Serialize tests that mutate process env (LEANKG_PG_URL) — Rust runs
/// tests in parallel and env is process-global.
static ENV_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

fn pg_url() -> Option<String> {
    // Only run the PG variants when a remote URL was explicitly provided.
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

/// Scratch PG schema, dropped on drop. Admin connection goes through
/// `pg_connect` so verified-TLS remote hosts work.
struct ScratchSchema {
    admin: postgres::Client,
    name: String,
}

impl ScratchSchema {
    fn new() -> Self {
        let base = pg_url().expect("LEANKG_PG_URL must be set for PG tests");
        let name = format!(
            "leankg_r2_{}_{}",
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

    /// A fresh backend pinned to the scratch schema — each call returns a
    /// brand-new instance with its own pool, simulating a server restart.
    fn backend(&self) -> Arc<PostgresBackend> {
        backend_for_schema(&self.name)
    }
}

/// A fresh backend pinned to an explicit schema — each call returns a
/// brand-new instance with its own pool, simulating a server restart.
fn backend_for_schema(schema: &str) -> Arc<PostgresBackend> {
    let base = pg_url().expect("LEANKG_PG_URL");
    let sep = if base.contains('?') { '&' } else { '?' };
    Arc::new(PostgresBackend {
        pg_url: format!("{base}{sep}options=-csearch_path%3D{schema}%2Cpublic"),
        schema: Some(schema.to_string()),
        pool: Arc::new(ClientPool::new(2)),
        ro_pool: Arc::new(ClientPool::new(2)),
        read_only: false,
        write_bus: None,
    })
}

impl Drop for ScratchSchema {
    fn drop(&mut self) {
        let _ = self
            .admin
            .batch_execute(&format!("DROP SCHEMA IF EXISTS {} CASCADE", self.name));
    }
}

/// Build the same element `add_ontology_concept` writes.
fn dynamic_known_issue(name: &str) -> (String, Vec<leankg::db::models::CodeElement>) {
    let id = format!("agent-{:016x}", rand());
    let env = "local";
    let scope = "agent";
    let mut metadata = ConceptMetadata::new(env, scope, "known_issue", &id, name, "r2 probe");
    metadata.source = Some("dynamic".to_string());
    let node = ConceptNode {
        gid: metadata.gid.clone(),
        name: name.to_string(),
        element_type: "known_issue".to_string(),
        aliases: vec![name.to_string()],
        description: "r2 probe".to_string(),
        env: env.to_string(),
        metadata,
    };
    (node.gid.clone(), concept_nodes_to_elements(&[node]))
}

fn rand() -> u64 {
    use std::time::{SystemTime, UNIX_EPOCH};
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.subsec_nanos() as u64 ^ d.as_secs())
        .unwrap_or(42)
}

// BUG-D probes — hang trio (get_context / check_consistency / temporal_query)
// against the real R1 corpus (13k elements / 92k relationships on remote PG).
// Read-only; requires LEANKG_R2_PROBE_SCHEMA.
// ===========================================================================

fn r1_engine() -> Option<GraphEngine> {
    let schema = std::env::var("LEANKG_R2_PROBE_SCHEMA").ok()?;
    assert!(!pg_url().is_none(), "LEANKG_PG_URL required");
    Some(GraphEngine::new(backend_for_schema(&schema)))
}

fn probe_file(engine: &GraphEngine) -> String {
    // A mid-size real file from the indexed corpus.
    let candidates = [
        "./src/mcp/tools.rs",
        "./src/mcp/handler.rs",
        "./src/graph/query.rs",
        "./src/main.rs",
    ];
    for f in candidates {
        let elems = engine.get_elements_by_file(f).expect("by_file");
        if !elems.is_empty() {
            return f.to_string();
        }
    }
    panic!("probe fixture files not found in corpus");
}

#[test]
fn bug_d_probe_temporal_query_latency() {
    let Some(engine) = r1_engine() else {
        eprintln!("skipping: LEANKG_R2_PROBE_SCHEMA not set");
        return;
    };
    let t0 = std::time::Instant::now();
    let rels = engine.temporal_query(1_800_000_000).expect("temporal");
    eprintln!("temporal_query: {} rels in {:?}", rels.len(), t0.elapsed());
    assert!(
        t0.elapsed().as_secs() < 10,
        "BUG-D: temporal_query took {:?} (>10s)",
        t0.elapsed()
    );
}

#[test]
fn bug_d_probe_check_consistency_latency() {
    let Some(engine) = r1_engine() else {
        eprintln!("skipping: LEANKG_R2_PROBE_SCHEMA not set");
        return;
    };
    let t0 = std::time::Instant::now();
    let report = engine.check_consistency().expect("consistency");
    eprintln!(
        "check_consistency: broken={} stale={} in {:?}",
        report.broken,
        report.stale,
        t0.elapsed()
    );
    assert!(
        t0.elapsed().as_secs() < 15,
        "BUG-D: check_consistency took {:?} (>15s)",
        t0.elapsed()
    );
}

#[test]
fn bug_d_probe_get_context_latency() {
    let Some(engine) = r1_engine() else {
        eprintln!("skipping: LEANKG_R2_PROBE_SCHEMA not set");
        return;
    };
    let file = probe_file(&engine);
    let t0 = std::time::Instant::now();
    let ctx = engine.get_context(&file, 4000).expect("context");
    eprintln!(
        "get_context({file}): {} elements in {:?}",
        ctx.elements.len(),
        t0.elapsed()
    );
    assert!(
        t0.elapsed().as_secs() < 10,
        "BUG-D: get_context took {:?} (>10s)",
        t0.elapsed()
    );
}

#[test]
fn bug_e_probe_agent_focus_latency() {
    let Some(engine) = r1_engine() else {
        eprintln!("skipping: LEANKG_R2_PROBE_SCHEMA not set");
        return;
    };
    let persona = leankg::graph::query::AgentPersona {
        name: "r2-probe".into(),
        ..serde_json::from_str(
            "{\"name\":\"r2-probe\",\"path_filters\":[\"./src/mcp\"],\"element_types\":[\"function\"]}",
        )
        .unwrap()
    };
    let t0 = std::time::Instant::now();
    let focus = engine.agent_focus(&persona).expect("focus");
    eprintln!(
        "agent_focus: {} elements {} rels in {:?}",
        focus.elements.len(),
        focus.relationships.len(),
        t0.elapsed()
    );
    assert!(
        t0.elapsed().as_secs() < 5,
        "BUG-E: agent_focus took {:?} (>5s)",
        t0.elapsed()
    );
}
