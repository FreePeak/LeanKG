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
        // Pin migrations to the scratch schema — without this they land in
        // `public`, and every engine in the binary shares one global table
        // set (cross-test interference: a concurrent test's rows satisfy
        // another test's post-delete lookup).
        admin
            .batch_execute(&format!("SET search_path TO {name}, public"))
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

/// BUG-E wedge check: in R1, agent_focus with a fixture hung AND every
/// subsequent tool call timed out until restart. After agent_focus returns,
/// the next engine operation must respond normally — nothing may stay
/// locked/pooled.
#[test]
fn bug_e_agent_focus_does_not_wedge_subsequent_calls() {
    let Some(engine) = r1_engine() else {
        eprintln!("skipping: LEANKG_R2_PROBE_SCHEMA not set");
        return;
    };
    let persona = leankg::graph::query::AgentPersona {
        name: "r2-probe".into(),
        ..serde_json::from_str("{\"name\":\"r2-probe\",\"path_filters\":[\"./src/mcp\"]}").unwrap()
    };
    let _ = engine.agent_focus(&persona).expect("focus");

    // Immediately issue three different read paths (the kind of tools that
    // cascaded in R1) — each must complete well under the 30s watchdog.
    let t0 = std::time::Instant::now();
    let hits = engine
        .get_elements_by_qualified_names(&["./src/mcp/tools.rs::list_tools".to_string()])
        .expect("keyed lookup right after focus");
    assert!(!hits.is_empty(), "sanity: known element must resolve");
    let _ = engine.get_context("./src/main.rs", 800).expect("context");
    let report = engine.check_consistency().expect("consistency");
    assert!(report.total_relationships > 0);
    eprintln!("post-focus sequence done in {:?}", t0.elapsed());
    assert!(
        t0.elapsed().as_secs() < 15,
        "BUG-E: post-focus sequence took {:?} — executor still wedged?",
        t0.elapsed()
    );
}

/// Shared body: add on engine A, look up + status + delete on engine B.
fn assert_dynamic_roundtrip_across_instances(engine_a: &GraphEngine, engine_b: &GraphEngine) {
    let (gid, elements) = dynamic_known_issue("r2_persistence_probe");
    assert_eq!(elements.len(), 1);
    assert_eq!(elements[0].qualified_name, gid);
    assert_eq!(
        elements[0].metadata.get("source").and_then(|v| v.as_str()),
        Some("dynamic")
    );

    // Act 1 — session A adds the concept (same path as add_ontology_concept).
    for e in &elements {
        engine_a.insert_element(e).expect("insert concept");
    }

    // Act 2 — session B ("after restart"): keyed lookup by exact GID.
    let found = engine_b
        .get_elements_by_qualified_names(&[gid.clone()])
        .expect("lookup by gid");
    let elem = found
        .get(&gid)
        .unwrap_or_else(|| panic!("BUG-B: dynamic concept {gid} lost across instances"));

    // Delete-path preconditions hold on the reloaded row.
    assert!(elem.file_path.starts_with("ontology://"));
    assert_eq!(
        elem.metadata.get("source").and_then(|v| v.as_str()),
        Some("dynamic"),
        "reloaded row must still carry source=dynamic"
    );

    // OntologyQueryEngine (used by kg_ontology_status / concept_search) must
    // see the dynamic row too.
    let q = OntologyQueryEngine::new(engine_b.db_arc().clone());
    let status = q.get_ontology_status().expect("ontology status");
    assert!(
        status.dynamic_concepts >= 1,
        "dynamic_concepts must survive restart, got {}",
        status.dynamic_concepts
    );

    // Act 3 — session B deletes by GID (delete_ontology_concept core).
    engine_b
        .remove_elements_by_qualified_name(&gid)
        .expect("delete by gid");
    let after = engine_b
        .get_elements_by_qualified_names(&[gid.clone()])
        .expect("post-delete lookup");
    assert!(after.get(&gid).is_none(), "row must be gone after delete");
}

#[test]
fn bug_b_dynamic_concept_survives_new_instance_pg() {
    let Some(_) = pg_url() else {
        eprintln!("skipping: LEANKG_PG_URL not set");
        return;
    };
    let scratch = ScratchSchema::new();
    let engine_a = GraphEngine::new(scratch.backend());
    let engine_b = GraphEngine::new(scratch.backend());
    assert_dynamic_roundtrip_across_instances(&engine_a, &engine_b);
}

/// Read-only probe against the R1 sweep schema (`leankg_p_2e2f737263`,
/// project key "./src"): the exact gid that failed delete_ontology_concept
/// after restart. Requires LEANKG_R2_PROBE_SCHEMA to be set explicitly so
/// we never accidentally touch a foreign schema.
#[test]
fn bug_b_probe_r1_schema_gid_lookup() {
    let Ok(schema) = std::env::var("LEANKG_R2_PROBE_SCHEMA") else {
        eprintln!("skipping: LEANKG_R2_PROBE_SCHEMA not set");
        return;
    };
    assert!(!pg_url().is_none(), "LEANKG_PG_URL required");
    let engine = GraphEngine::new(backend_for_schema(&schema));
    let gid = "local:agent:known_issue:agent-18cdf56caab0b740:v1";
    let found = engine
        .get_elements_by_qualified_names(&[gid.to_string()])
        .expect("lookup");
    let elem = found
        .get(gid)
        .unwrap_or_else(|| panic!("BUG-B repro: {gid} not found"));
    assert!(elem.file_path.starts_with("ontology://"));
}

/// BUG-B root cause: the project→PG-schema identity must not depend on HOW
/// `project.project_path` is spelled. R1 saw a server write dynamic ontology
/// rows under key hex("./src") and — after a restart where the field was
/// absent/changed — read an EMPTY schema: "Element not found",
/// dynamic_concepts:0, while the rows sat in Postgres all along.
///
/// Layout mirrors the R1 worktree: `<proj>/leankg.yaml` + `<proj>/.leankg`,
/// sources under `<proj>/src`.
#[test]
fn bug_b_project_identity_stable_across_yaml_variants() {
    let tmp = tempfile::TempDir::new().unwrap();
    let proj = tmp.path().join("proj");
    std::fs::create_dir_all(proj.join("src")).unwrap();
    std::fs::create_dir_all(proj.join(".leankg")).unwrap();
    let db = proj.join(".leankg");

    // V2 — RELATIVE project_path "./src" (the R1 workaround spelling).
    std::fs::write(
        proj.join("leankg.yaml"),
        "project:\n  name: p\n  root: ./src\n  project_path: \"./src\"\n  languages: [rust]\n",
    )
    .unwrap();
    let k_relative = leankg::db::backend::schema_for_path(&db);

    // V3 — ABSOLUTE project_path pointing at the same dir (what `setup`
    // writes). Must land on the SAME schema as V2.
    std::fs::write(
        proj.join("leankg.yaml"),
        format!(
            "project:\n  name: p\n  root: ./src\n  project_path: \"{}\"\n  languages: [rust]\n",
            proj.join("src").display()
        ),
    )
    .unwrap();
    let k_absolute = leankg::db::backend::schema_for_path(&db);

    assert_eq!(
        k_relative, k_absolute,
        "relative and absolute spellings of the same dir must share one schema"
    );
}

/// BUG-B self-heal (hermetic part): a RELATIVE project_path contributes a
/// LEGACY candidate (the pre-fix literal-key schema name) after the resolved
/// identity, and an ABSOLUTE one contributes none.
#[test]
fn bug_b_legacy_candidates_shape() {
    let tmp = tempfile::TempDir::new().unwrap();
    let proj = tmp.path().join("proj");
    std::fs::create_dir_all(proj.join("src")).unwrap();
    std::fs::create_dir_all(proj.join(".leankg")).unwrap();
    let db_path = proj.join(".leankg");

    std::fs::write(
        proj.join("leankg.yaml"),
        "project:\n  name: p\n  root: ./src\n  project_path: \"./src\"\n  languages: [rust]\n",
    )
    .unwrap();
    let cands = leankg::db::backend::schema_candidates_for_path(&db_path);
    assert_eq!(cands.len(), 2, "relative value must add a legacy candidate");
    assert_eq!(
        cands[1], "leankg_p_2e2f737263",
        "legacy candidate must be hex of the raw literal './src'"
    );

    std::fs::write(
        proj.join("leankg.yaml"),
        format!(
            "project:\n  name: p\n  root: ./src\n  project_path: \"{}\"\n  languages: [rust]\n",
            proj.join("src").display()
        ),
    )
    .unwrap();
    let cands = leankg::db::backend::schema_candidates_for_path(&db_path);
    assert_eq!(cands.len(), 1, "absolute value needs no legacy candidate");
}

/// BUG-B second vector: the ontology YAML replace-cycle (boot/post-index
/// sync clears ALL `ontology://` rows, then re-inserts) must preserve
/// source:"dynamic" rows even when the ontology dir carries unrelated YAML.
#[test]
fn bug_b_dynamic_rows_survive_yaml_sync_cycle_pg() {
    let Some(_) = pg_url() else {
        eprintln!("skipping: LEANKG_PG_URL not set");
        return;
    };
    let scratch = ScratchSchema::new();
    let engine = GraphEngine::new(scratch.backend());

    // Session 1: agent adds a dynamic concept.
    let (gid, elements) = dynamic_known_issue("r2_sync_survivor");
    for e in &elements {
        engine.insert_element(e).unwrap();
    }

    // A repo-level ontology dir with unrelated static YAML (the boot /
    // post-index sync replaces the whole layer from this).
    let tmp = tempfile::TempDir::new().unwrap();
    let ont = tmp.path().join("ontology");
    std::fs::create_dir_all(&ont).unwrap();
    std::fs::write(
        ont.join("concepts.yaml"),
        "concepts:\n  - id: static_thing\n    name: Static Thing\n    env: local\n    type: domain_entity\n    description: static\n",
    )
    .unwrap();

    leankg::ontology::sync_from_dir(&ont, &engine, None).expect("sync");

    // The dynamic concept must survive the replace cycle.
    let found = engine
        .get_elements_by_qualified_names(&[gid.clone()])
        .expect("lookup after sync");
    assert!(
        found.contains_key(&gid),
        "BUG-B: yaml sync wiped a dynamic ontology concept"
    );

    let q = OntologyQueryEngine::new(engine.db_arc().clone());
    let status = q.get_ontology_status().expect("status after sync");
    assert!(status.dynamic_concepts >= 1);
}

/// FR-HEA-01: alias accounting must be self-consistent — every ontology node
/// kind seeds a name-derived alias. The YAML loader previously dropped
/// `step_def.aliases` entirely and `WorkflowStepMetadata::new` seeded
/// `aliases: vec![]`, so 100% of YAML-loaded workflow_steps were reported in
/// `nodes_missing_aliases` (live probe: 57 missing = 57 workflow_steps).
/// Parity: `WorkflowMetadata::new` and `FailureModeNode::new` both seed
/// `normalize_alias(name)`.
#[test]
fn fr_hea01_workflow_steps_carry_aliases() {
    // Red: unit-level — the metadata constructor must seed the name alias.
    let meta = leankg::ontology::WorkflowStepMetadata::new(
        "local",
        "default",
        "wf-1",
        "step-1",
        1,
        "desc",
        "Find Source Files",
    );
    assert!(
        !meta.aliases.is_empty(),
        "WorkflowStepMetadata::new must seed a name-derived alias (FR-HEA-01)"
    );
    assert_eq!(meta.aliases[0], "find source files");

    // Green at the node level: builder must merge YAML aliases on top of the
    // name-derived seed.
    let node = leankg::ontology::WorkflowStepNode::new(
        "local",
        "default",
        "wf-1",
        "step-1",
        "Find Source Files",
        1,
        "desc",
    )
    .with_aliases(vec!["file discovery".to_string()]);
    assert_eq!(node.metadata.aliases[0], "find source files");
    assert!(node
        .metadata
        .aliases
        .contains(&"file discovery".to_string()));
}

/// FR-HEA-01 end-to-end: sync a workflows.yaml whose steps declare aliases
/// (and some that do not) — after sync, `get_ontology_status` must report
/// zero workflow_steps in `nodes_missing_aliases`.
#[test]
fn fr_hea01_yaml_sync_backfills_step_aliases() {
    let Some(_) = pg_url() else {
        eprintln!("skipping: LEANKG_PG_URL not set");
        return;
    };
    let scratch = ScratchSchema::new();
    let engine = GraphEngine::new(scratch.backend());

    let tmp = tempfile::TempDir::new().unwrap();
    let ont = tmp.path().join("ontology");
    std::fs::create_dir_all(&ont).unwrap();
    std::fs::write(
        ont.join("workflows.yaml"),
        r#"workflows:
  - id: alias_flow
    name: Alias Flow
    env: local
    aliases: [aliased pipeline]
    description: flow used to verify step alias coverage
    steps:
      - id: aliased_step
        name: Aliased Step
        aliases: [custom step alias]
        failure_modes: []
      - id: bare_step
        name: Bare Step
        failure_modes: []
"#,
    )
    .unwrap();

    leankg::ontology::sync_from_dir(&ont, &engine, None).expect("sync");

    let q = OntologyQueryEngine::new(engine.db_arc().clone());
    let status = q.get_ontology_status().expect("status after sync");
    assert_eq!(
        status.nodes_missing_aliases, 0,
        "FR-HEA-01: no ontology node may lack a name-derived alias; \
         status={:?}",
        status
    );

    // Workflow-level alias search still works.
    let wf = q
        .search_workflows("aliased pipeline", "local")
        .expect("workflow search");
    assert!(
        wf.iter().any(|w| w.name == "Alias Flow"),
        "workflow-level alias must still match"
    );

    // Step-level: the declared step alias must round-trip into the stored
    // metadata (kg_trace_workflow / concept_search read it from there).
    let steps = q
        .trace_workflow("alias_flow", "local")
        .expect("steps via trace_workflow");
    let aliased = steps
        .iter()
        .find(|s| s.name == "Aliased Step")
        .expect("aliased step present");
    assert!(
        aliased
            .metadata
            .aliases
            .contains(&"custom step alias".to_string()),
        "declared YAML step alias must be applied; got {:?}",
        aliased.metadata.aliases
    );
    let bare = steps
        .iter()
        .find(|s| s.name == "Bare Step")
        .expect("bare step present");
    assert_eq!(
        bare.metadata.aliases,
        vec!["bare step".to_string()],
        "name-derived alias seed must backfill undeclared steps"
    );
}
