//! H9 integration — `leankg doctor --deep` against live Postgres.
//!
//! Mirrors the scratch-schema pattern of tests/pg_schema_test.rs and
//! tests/hackathon_r2_engine.rs: a per-test schema is created, migrated,
//! seeded with a tiny generated fixture (5 files), diagnosed, then dropped.
//!
//! Requires LEANKG_PG_URL; skips (does not fail) when unset:
//!   set -a; source ../.env; set +a
//!   cargo test --release --test doctor_deep_tests

use leankg::db::backend::{pg_connect, ClientPool, PostgresBackend};
use leankg::db::models::{CodeElement, Relationship};
use leankg::doctor::deep::{
    BackendProbes, CheckRegistry, CheckStatus, DeepContext, PoolEnvSnapshot,
};
use leankg::graph::GraphEngine;
use std::path::PathBuf;
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::Arc;

static SCHEMA_COUNTER: AtomicU32 = AtomicU32::new(0);

fn pg_url() -> Option<String> {
    match std::env::var("LEANKG_PG_URL") {
        Ok(v) if !v.trim().is_empty() => Some(v),
        _ => None,
    }
}

/// Scratch PG schema, dropped on drop; admin connection stays on the
/// schema's search_path so direct SQL cleanup hits the right tables.
struct ScratchSchema {
    admin: postgres::Client,
    name: String,
}

impl ScratchSchema {
    fn new() -> Self {
        let base = pg_url().expect("LEANKG_PG_URL must be set for doctor_deep_tests");
        let name = format!(
            "leankg_doctor_{}_{}",
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
        // Pin this session to the scratch schema so unqualified migration
        // DDL lands there (parallel tests each own a schema); keep public
        // on the path so the database-wide `vector` extension resolves.
        admin
            .batch_execute(&format!("SET search_path TO {name}, public"))
            .unwrap();
        leankg::db::pg::migrations::run_migrations(&mut admin).unwrap();
        Self { admin, name }
    }
}

impl Drop for ScratchSchema {
    fn drop(&mut self) {
        let _ = self
            .admin
            .batch_execute(&format!("DROP SCHEMA IF EXISTS {} CASCADE", self.name));
    }
}

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

/// Tiny generated fixture project: writable `.leankg`, 5 Rust source files.
/// Returns (keepalive tempdir, project root).
fn fixture_project() -> (tempfile::TempDir, PathBuf) {
    let dir = tempfile::tempdir().expect("tempdir");
    let root = dir.path().to_path_buf();
    std::fs::create_dir_all(root.join(".leankg")).unwrap();
    let src = root.join("src");
    std::fs::create_dir_all(&src).unwrap();
    for i in 1..=5 {
        let p = src.join(format!("mod{i}.rs"));
        std::fs::write(&p, format!("pub fn f{i}() -> i32 {{\n    {i}\n}}\n")).unwrap();
    }
    (dir, root)
}

fn elem(qn: &str, file: &str) -> CodeElement {
    CodeElement {
        qualified_name: qn.to_string(),
        element_type: "function".to_string(),
        name: qn.rsplit("::").next().unwrap_or(qn).to_string(),
        file_path: file.to_string(),
        line_start: 1,
        line_end: 3,
        language: "rust".to_string(),
        parent_qualified: Some(file.to_string()),
        cluster_id: None,
        cluster_label: None,
        metadata: serde_json::json!({}),
        env: "local".to_string(),
    }
}

/// init (migrate via ScratchSchema) + index a 5-file fixture through the
/// engine layer used by `index_files_parallel`, plus an explicit CALLS
/// chain so orphan semantics are deterministic.
struct SeededFixture {
    _schema: ScratchSchema,
    _dir: tempfile::TempDir,
    root: PathBuf,
    qualified_names: Vec<String>,
}

fn seeded_fixture() -> SeededFixture {
    let schema = ScratchSchema::new();
    let (_dir, root) = fixture_project();

    let src = root.join("src");
    let files: Vec<String> = (1..=5)
        .map(|i| src.join(format!("mod{i}.rs")).to_string_lossy().to_string())
        .collect();
    let qns: Vec<String> = files
        .iter()
        .enumerate()
        .map(|(i, f)| elem_qn(f, i + 1))
        .collect();

    let ge = GraphEngine::new(backend_for_schema(&schema.name));
    let elems: Vec<CodeElement> = qns
        .iter()
        .zip(files.iter())
        .map(|(qn, f)| elem(qn, f))
        .collect();
    ge.insert_elements_with(&elems, true).unwrap();

    let rels: Vec<Relationship> = qns
        .windows(2)
        .map(|w| Relationship {
            id: None,
            source_qualified: w[0].clone(),
            target_qualified: w[1].clone(),
            rel_type: "CALLS".to_string(),
            confidence: 0.9,
            metadata: serde_json::json!({}),
            env: "local".to_string(),
        })
        .collect();
    ge.insert_relationships_with(&rels, true).unwrap();

    SeededFixture {
        _schema: schema,
        _dir,
        root,
        qualified_names: qns,
    }
}

fn elem_qn(file: &str, i: usize) -> String {
    format!("{}::f{i}", file)
}

fn run_deep(fixture: &SeededFixture) -> leankg::doctor::deep::DoctorReport {
    // CLI-dispatch equivalent: probes over the project backend, default
    // registry, env snapshot captured once.
    let probes = BackendProbes::new(backend_for_schema(&fixture._schema.name));
    let registry = CheckRegistry::with_defaults();
    let leankg_dir = fixture.root.join(".leankg");
    let ctx = DeepContext::new(
        &probes,
        &fixture.root,
        &leankg_dir,
        PoolEnvSnapshot {
            pool_size: None,
            pool_wait_ms: None,
        },
    );
    registry.run_all(&ctx)
}

#[test]
fn deep_doctor_reports_all_pass_on_fresh_fixture() {
    if pg_url().is_none() {
        eprintln!("skipping: LEANKG_PG_URL not set");
        return;
    }
    let fx = seeded_fixture();
    let report = run_deep(&fx);

    for f in &report.findings {
        assert_eq!(
            f.status,
            CheckStatus::Pass,
            "{} failed: {} | hint: {}",
            f.check,
            f.detail,
            f.hint
        );
    }
    assert_eq!(report.findings.len(), 8);
    assert_eq!(report.exit_code(), 0);

    // --format json must be machine-parseable with an all-green summary.
    let parsed: serde_json::Value =
        serde_json::from_str(&report.render_json()).expect("valid JSON");
    assert_eq!(parsed["summary"]["pass"], 8);
    assert_eq!(parsed["summary"]["warn"], 0);
    assert_eq!(parsed["summary"]["fail"], 0);
    assert_eq!(parsed["findings"].as_array().map(Vec::len), Some(8));
}

#[test]
fn deleting_element_row_flips_orphan_check_to_fail() {
    if pg_url().is_none() {
        eprintln!("skipping: LEANKG_PG_URL not set");
        return;
    }
    let mut fx = seeded_fixture();
    assert_eq!(run_deep(&fx).exit_code(), 0, "precondition: healthy");

    // Corrupt referential integrity directly through the DB: drop the
    // middle element; its two CALLS edges become dangling.
    let victim = fx.qualified_names[2].clone();
    fx._schema
        .admin
        .execute(
            "DELETE FROM code_elements WHERE qualified_name = $1",
            &[&victim],
        )
        .expect("delete element row");

    let report = run_deep(&fx);
    let orphan = report
        .findings
        .iter()
        .find(|f| f.check == "orphaned-relationships")
        .expect("orphan check present");
    assert_eq!(orphan.status, CheckStatus::Fail, "{:?}", orphan);
    assert!(orphan.detail.contains(&victim), "detail: {}", orphan.detail);
    assert!(!orphan.hint.is_empty());
    assert_eq!(report.exit_code(), 2);
}
