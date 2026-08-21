//! Regression test: re-running `leankg index` on an already-indexed project.
//!
//! Historical bug (docs/analysis/pg-perf-large-codebase.md finding 1): a bare
//! `create_dir` on `<project>/.leankg` returned `Os { code: 17, AlreadyExists }`
//! whenever the project had been indexed before — blocking the normal
//! `index` → `reindex` workflow. Idempotent re-index requires: `create_dir_all`
//! for the `.leankg` dir, `CREATE SCHEMA IF NOT EXISTS` + idempotent migrations
//! for the per-project PG schema on every writer init, and a wipe-then-insert
//! full index (LEANKG_INDEX_WIPE).
//!
//! Two layers:
//! 1. Programmatic — `init_db` twice on the same db_path (pre-created
//!    `.leankg` dir), then two full wipe/insert cycles: both must succeed and
//!    the second must not accumulate duplicates.
//! 2. CLI end-to-end — spawn the real binary's `leankg index` twice against a
//!    temp project whose `.leankg` dir already exists; both runs must exit 0.
//!
//! Requires LEANKG_PG_URL pointing at a Postgres 18 + pgvector instance
//! (TLS URLs accepted). Skipped when it is unset or unreachable.

use std::path::PathBuf;
use std::process::Command;
use std::sync::{Mutex, OnceLock};

const BIN: &str = env!("CARGO_BIN_EXE_leankg");

/// Serializes the LEANKG_PG_URL mutation + `init_db` window — env is
/// process-global and tests run in parallel.
static ENV_LOCK: OnceLock<Mutex<()>> = OnceLock::new();

/// Captured once so later `set_var`/`remove_var` windows inside one test
/// cannot make another test observe a missing variable.
fn base_url() -> String {
    static BASE: OnceLock<String> = OnceLock::new();
    BASE.get_or_init(|| {
        std::env::var("LEANKG_PG_URL")
            .unwrap_or_else(|_| "postgresql://postgres:postgres@localhost:5433/leankg".to_string())
    })
    .clone()
}

fn pg_reachable(url: &str) -> bool {
    leankg::db::backend::pg_connect(url)
        .and_then(|mut c| c.batch_execute("SELECT 1").map_err(|e| e.into()))
        .is_ok()
}

fn drop_schema(url: &str, schema: &str) {
    if let Ok(mut admin) = leankg::db::backend::pg_connect(url) {
        let _ = admin.batch_execute(&format!("DROP SCHEMA IF EXISTS {schema} CASCADE"));
    }
}

#[test]
fn init_db_and_full_index_twice_on_existing_leankg_dir_succeeds() {
    let url = base_url();
    if !pg_reachable(&url) {
        eprintln!("skipping: LEANKG_PG_URL unset or unreachable");
        return;
    }

    // The historical EEXIST trigger: the project root already contains a
    // `.leankg` directory from a previous index run. The writer derives a
    // deterministic per-project PG schema from db_path; start it cold.
    let project = std::env::temp_dir().join(format!("leankg_eexist_prog_{}", std::process::id()));
    let db_path = project.join(".leankg");
    std::fs::create_dir_all(&db_path).expect("pre-create .leankg dir");
    let schema = leankg::db::backend::schema_for_path(&db_path);
    drop_schema(&url, &schema);

    let batch = vec![elem("/src/lib.rs::main"), elem("/src/lib.rs::helper")];

    // Run #1 — first `leankg index`.
    let ge1 = engine_for(&url, &db_path);
    ge1.insert_elements_with(&batch, true)
        .expect("full index run 1");
    drop(ge1);

    // Run #2 — re-index the SAME project: init_db again on the existing
    // path/schema, wipe-then-insert must succeed without EEXIST or dupes.
    let ge2 = engine_for(&url, &db_path);
    ge2.wipe_elements_for_env("local")
        .expect("wipe before run 2");
    ge2.invalidate_cache();
    ge2.insert_elements_with(&batch, true)
        .expect("full index run 2");
    ge2.invalidate_cache();

    let all = ge2.all_elements().expect("all_elements");
    assert_eq!(
        all.len(),
        2,
        "re-index on an existing project must not duplicate rows"
    );

    drop_schema(&url, &schema);
    let _ = std::fs::remove_dir_all(&project);
}

/// Build a GraphEngine via the writer entrypoint (`init_db`) while
/// LEANKG_PG_URL points at the shared instance. The engine pins its own
/// per-project schema derived from db_path.
fn engine_for(url: &str, db_path: &std::path::Path) -> leankg::graph::GraphEngine {
    let guard = ENV_LOCK.get_or_init(|| Mutex::new(())).lock().unwrap();
    std::env::set_var("LEANKG_PG_URL", url);
    let db = leankg::db::backend::init_db(db_path)
        .unwrap_or_else(|e| panic!("init_db({db_path:?}) failed: {e}"));
    drop(guard);
    leankg::graph::GraphEngine::new(db)
}

fn elem(qn: &str) -> leankg::db::models::CodeElement {
    leankg::db::models::CodeElement {
        qualified_name: qn.to_string(),
        element_type: "function".to_string(),
        name: qn.rsplit("::").next().unwrap_or(qn).to_string(),
        file_path: "/src/lib.rs".to_string(),
        line_start: 1,
        line_end: 5,
        language: "rust".to_string(),
        parent_qualified: None,
        metadata: serde_json::json!({}),
        env: "local".to_string(),
        ..Default::default()
    }
}

/// End-to-end: the real CLI indexes a temp project whose `.leankg` directory
/// already exists — twice. Both runs must exit 0 (no EEXIST).
#[test]
fn cli_index_twice_on_preexisting_leankg_dir_succeeds() {
    let url = base_url();
    if std::env::var("LEANKG_PG_URL").is_err() || !pg_reachable(&url) {
        eprintln!("skipping: LEANKG_PG_URL unset or unreachable");
        return;
    }

    let project: PathBuf =
        std::env::temp_dir().join(format!("leankg_eexist_cli_{}", std::process::id()));
    let src = project.join("src");
    std::fs::create_dir_all(&src).expect("create fixture src/");
    std::fs::write(src.join("main.rs"), "fn main() { println!(\"hello\"); }\n")
        .expect("write main.rs");
    std::fs::write(
        src.join("util.rs"),
        "pub fn add(a: i32, b: i32) -> i32 { a + b }\n",
    )
    .expect("write util.rs");
    // The trigger: `.leankg` already present before the first index call.
    std::fs::create_dir_all(project.join(".leankg")).expect("pre-create .leankg dir");

    // The CLI pins a per-project schema derived from the project path; drop
    // any leftover state first so the run starts cold.
    let schema = leankg::db::backend::schema_for_path(&project.join(".leankg"));
    drop_schema(&url, &schema);

    for run in 1..=2 {
        let status = Command::new(BIN)
            .arg("index")
            .arg(&project)
            .current_dir(&project)
            .env("LEANKG_PG_URL", &url)
            .status()
            .expect("spawn leankg index");
        assert!(
            status.success(),
            "`leankg index` run #{run} failed on an already-initialized project: {status}"
        );
    }

    drop_schema(&url, &schema);
    let _ = std::fs::remove_dir_all(&project);
}
