//! ENT-9 / hackathon H4 — provenance labels surfaced in ALL graph responses.
//!
//! Every tool response that contains relationships/edges must carry a
//! `confidence_label ∈ {EXTRACTED, INFERRED, AMBIGUOUS}` per edge. This
//! contract test indexes a small fixture into a scratch PG schema, calls the
//! graph-returning tools through `ToolHandler`, and asserts every edge
//! object carries a valid label.
//!
//! Run (live Postgres via .env, never Docker):
//! ```bash
//! set -a; source ../.env; set +a
//! CARGO_TARGET_DIR=/tmp/opencode/t-c3b cargo test --release \
//!   --test provenance_labels -- --ignored --test-threads=1
//! ```

use leankg::db::backend::PostgresBackend;
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

struct ScratchSchema {
    admin: postgres::Client,
    name: String,
    url: String,
}

impl ScratchSchema {
    fn new() -> Self {
        let base = pg_url();
        let name = format!(
            "leankg_prov_{}_{}",
            std::process::id(),
            SCHEMA_COUNTER.fetch_add(1, Ordering::Relaxed)
        );
        // The crate's TLS-aware connector handles verify-full managed URLs.
        let mut admin = leankg::db::backend::pg_connect(&base)
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
            schema: Some(self.name.clone()),
            pool: Arc::new(leankg::db::backend::ClientPool::new(5)),
            ro_pool: Arc::new(leankg::db::backend::ClientPool::new(5)),
            read_only: false,
            write_bus: None,
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

/// Small fixture: two files, a call chain, an import edge with a resolver
/// method override, and one synthetic event element reachable from main.
const FIXTURE_ELEMENTS: &str = r#"
?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] <-
[
    ["src/main.rs", "file", "main.rs", "src/main.rs", 1, 40, "rust", null, "c1", "core", "{}", "local"],
    ["src/main.rs::main", "function", "main", "src/main.rs", 5, 30, "rust", "src/main.rs", "c1", "core", "{}", "local"],
    ["src/lib.rs", "file", "lib.rs", "src/lib.rs", 1, 60, "rust", null, "c2", "lib", "{}", "local"],
    ["src/lib.rs::caller", "function", "caller", "src/lib.rs", 3, 40, "rust", "src/lib.rs", "c2", "lib", "{}", "local"],
    ["src/lib.rs::callee", "function", "callee", "src/lib.rs", 41, 50, "rust", "src/lib.rs", "c2", "lib", "{}", "local"],
    ["event::deploy", "event", "deploy", "event://deploy", 0, 0, "synthetic", null, "c1", "core", "{\"synthetic\": true}", "local"]
]
:put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env}
"#;

const FIXTURE_RELATIONSHIPS: &str = r#"
?[source_qualified, target_qualified, rel_type, confidence, metadata, env] <-
[
    ["src/main.rs", "src/main.rs::main", "contains", 1.0, "{}", "local"],
    ["src/main.rs::main", "src/lib.rs::caller", "calls", 0.9, "{\"resolution_method\": \"typed\"}", "local"],
    ["src/lib.rs::caller", "src/lib.rs::callee", "calls", 0.65, "{\"resolution_method\": \"name_file_hint\"}", "local"],
    ["src/lib.rs", "src/main.rs", "imports", 0.7, "{\"resolution_method\": \"name\", \"target\": \"src/main.rs\"}", "local"],
    ["src/main.rs::main", "event::deploy", "references", 0.5, "{}", "local"]
]
:put relationships {source_qualified, target_qualified, rel_type, confidence, metadata, env}
"#;

const VALID_LABELS: &[&str] = &["EXTRACTED", "INFERRED", "AMBIGUOUS"];

fn seed(db: &PostgresBackend) {
    db.run_script(FIXTURE_ELEMENTS, Default::default())
        .expect("seed elements");
    db.run_script(FIXTURE_RELATIONSHIPS, Default::default())
        .expect("seed relationships");
}

fn assert_valid_label(label: &Value, ctx: &str) {
    let s = label.as_str().unwrap_or_else(|| {
        panic!("{ctx}: confidence_label must be a string, got {label}");
    });
    assert!(
        VALID_LABELS.contains(&s),
        "{ctx}: invalid confidence_label '{s}'"
    );
}

#[test]
#[ignore = "requires live Postgres (LEANKG_PG_URL)"]
fn impact_radius_entries_all_labeled() {
    let out = run_tool(
        "get_impact_radius",
        json!({"file": "src/main.rs::main", "depth": 2}),
    );

    let entries = out["elements_with_confidence"]
        .as_array()
        .expect("elements_with_confidence array");
    assert!(!entries.is_empty(), "impact radius must return entries");
    for e in entries {
        assert_valid_label(
            &e["confidence_label"],
            &format!("impact entry {}", e["qualified_name"]),
        );
    }
}

#[test]
#[ignore = "requires live Postgres (LEANKG_PG_URL)"]
fn shortest_path_hops_all_labeled() {
    let out = run_tool(
        "shortest_path",
        json!({"source": "src/main.rs::main", "target": "src/lib.rs::callee"}),
    );

    assert_eq!(out["found"], json!(true), "path must exist: {out}");
    let path = out["result"]["path"].as_array().expect("path array");
    assert!(!path.is_empty(), "hops must be present");
    for hop in path {
        assert_valid_label(
            &hop["confidence_label"],
            &format!("hop {}->{}", hop["from"], hop["to"]),
        );
    }
}

#[test]
#[ignore = "requires live Postgres (LEANKG_PG_URL)"]
fn dependencies_edges_all_labeled() {
    let out = run_tool("get_dependencies", json!({"file": "src/lib.rs"}));

    let deps = out["dependencies"].as_array().expect("dependencies array");
    assert!(!deps.is_empty(), "fixture lib.rs imports src/main.rs");
    for d in deps {
        assert_valid_label(&d["confidence_label"], "dependency edge");
    }
}

#[test]
#[ignore = "requires live Postgres (LEANKG_PG_URL)"]
fn dependents_edges_all_labeled() {
    let out = run_tool("get_dependents", json!({"file": "src/lib.rs::caller"}));

    let deps = out["dependents"].as_array().expect("dependents array");
    assert!(!deps.is_empty(), "fixture main calls caller");
    for d in deps {
        assert_valid_label(&d["confidence_label"], "dependent edge");
    }
}

#[test]
#[ignore = "requires live Postgres (LEANKG_PG_URL)"]
fn review_context_relationships_all_labeled() {
    let out = run_tool(
        "get_review_context",
        json!({"files": ["src/main.rs", "src/lib.rs"]}),
    );

    let rels = out["relationships"]
        .as_array()
        .expect("relationships array");
    assert!(!rels.is_empty(), "fixture has edges from both files");
    for r in rels {
        assert_valid_label(&r["confidence_label"], "review relationship");
    }
}

#[test]
#[ignore = "requires live Postgres (LEANKG_PG_URL)"]
fn synthetic_event_element_flagged_in_graph_response() {
    let out = run_tool(
        "get_impact_radius",
        json!({"file": "src/main.rs::main", "depth": 1}),
    );

    let elements = out["elements"].as_array().expect("elements array");
    let ev = elements
        .iter()
        .find(|e| e["qualified_name"] == json!("event::deploy"))
        .unwrap_or_else(|| panic!("event::deploy should be affected; got {elements:?}"));
    assert_eq!(
        ev["synthetic"],
        json!(true),
        "event element must carry the synthetic marker"
    );
}

/// Fresh scratch schema + handler per call. Scratch setup and the lazy
/// pool warm-up MUST stay outside block_on — PostgresBackend's sync
/// Client::connect spins its own runtime and panics inside one.
fn run_tool(tool: &str, args: Value) -> Value {
    let scratch = ScratchSchema::new();
    let backend = scratch.backend();
    seed(&backend);
    backend
        .run_script(
            "?[qualified_name] := *code_elements[qualified_name] :limit 1",
            Default::default(),
        )
        .expect("warm-up PG connection");
    let engine = GraphEngine::new(backend);
    let handler = ToolHandler::new(
        engine,
        std::env::temp_dir().join("provenance_labels_fixture"),
    );

    let rt = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
        .unwrap();
    rt.block_on(async move {
        handler
            .execute_tool(tool, &args)
            .await
            .unwrap_or_else(|e| panic!("{tool} failed: {e}"))
    })
}
