//! Regression test for the cycle-2 R2a project-identity cluster (sweep
//! findings N1–N3, docs/analysis/hackathon-sweep-R2.md).
//!
//! N1 flow, live: a fixture whose `.leankg/leankg.yaml` carries a RELATIVE
//! `project_path` ("./src") plus an unmodeled custom key is indexed; the yaml
//! is then rewritten WITHOUT the anchor (the corruption the sweep observed);
//! re-indexing must (a) leave the custom key intact and (b) reopen ONE stable
//! schema through a fresh writer/reader `init_db` — elements written by run 2
//! are visible to a brand-new backend open.
//!
//! Requires LEANKG_PG_URL pointing at Postgres 18 + pgvector. Skipped when it
//! is unset or unreachable (same contract as tests/pg_schema_test.rs).

use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::{Mutex, OnceLock};

const BIN: &str = env!("CARGO_BIN_EXE_leankg");

/// Serializes the LEANKG_PG_URL mutation window — env is process-global and
/// tests run in parallel.
static ENV_LOCK: OnceLock<Mutex<()>> = OnceLock::new();

fn pg_url() -> String {
    std::env::var("LEANKG_PG_URL")
        .unwrap_or_else(|_| "postgresql://postgres:postgres@localhost:5433/leankg".to_string())
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

fn count_elements(url: &str, schema: &str) -> i64 {
    let mut admin = leankg::db::backend::pg_connect(url).expect("connect for count");
    let row = admin
        .query_one(&format!("SELECT count(*) FROM {schema}.code_elements"), &[])
        .unwrap_or_else(|e| panic!("count in {schema}: {e}"));
    row.get(0)
}

/// Five source files so the index has real work (mirrors the R2 fixtures).
fn write_sources(src: &Path) {
    std::fs::create_dir_all(src).unwrap();
    for i in 0..5 {
        std::fs::write(
            src.join(format!("mod{i}.rs")),
            format!(
                "pub fn alpha_{i}(x: i32) -> i32 {{ x + {i} }}\n\npub fn beta_{i}() -> &'static str {{ \"b{i}\" }}\n"
            ),
        )
        .unwrap();
    }
}

fn seed_yaml(project: &Path) {
    let leankg = project.join(".leankg");
    std::fs::create_dir_all(&leankg).unwrap();
    std::fs::write(
        leankg.join("leankg.yaml"),
        "project:\n  name: ident-fixture-a\n  root: ./src\n  project_path: \"./src\"\n  languages:\n    - rust\nteam_identity_probe: keep-me-through-reindex\n",
    )
    .unwrap();
}

fn rewrite_yaml_without_anchor(project: &Path) {
    // The corruption from the sweep: the identity anchor disappears while an
    // unrelated user key survives.
    std::fs::write(
        project.join(".leankg").join("leankg.yaml"),
        "project:\n  name: ident-fixture-a\n  root: ./src\n  languages:\n    - rust\nteam_identity_probe: keep-me-through-reindex\n",
    )
    .unwrap();
}

fn run_index(project: &Path, url: &str, run: usize) {
    let status = Command::new(BIN)
        .args(["index", "./src"])
        .current_dir(project)
        .env("LEANKG_PG_URL", url)
        .status()
        .expect("spawn leankg index");
    assert!(
        status.success(),
        "`leankg index` run #{run} failed: {status}"
    );
}

#[test]
fn identity_cluster_yaml_survives_reindex_and_engine_reopens_same_schema() {
    let url = pg_url();
    if !pg_reachable(&url) {
        eprintln!("skipping: LEANKG_PG_URL unset or unreachable");
        return;
    }

    let fixture: PathBuf = std::env::temp_dir().join(format!(
        "leankg_ident_cluster_{}_{:?}",
        std::process::id(),
        std::thread::current().id()
    ));
    let _ = std::fs::remove_dir_all(&fixture);
    write_sources(&fixture.join("src"));
    seed_yaml(&fixture);

    // Schema A: what the seeded relative anchor resolves to.
    let db_path = fixture.join(".leankg");
    let schema_a = leankg::db::backend::schema_for_path(&db_path);
    drop_schema(&url, &schema_a);

    // Run #1 — index with the anchor present.
    run_index(&fixture, &url, 1);
    assert!(
        count_elements(&url, &schema_a) > 0,
        "run 1 must populate {schema_a}"
    );

    // Corrupt: delete the anchor, keep the user probe key.
    rewrite_yaml_without_anchor(&fixture);

    // Run #2 — re-index after corruption.
    run_index(&fixture, &url, 2);

    // (a) the custom field survived BOTH runs and the corruption window.
    let yaml = std::fs::read_to_string(db_path.join("leankg.yaml")).unwrap();
    assert!(
        yaml.contains("team_identity_probe: keep-me-through-reindex"),
        "custom user field must survive:\n{yaml}"
    );

    // (b) a FRESH engine open (writer path) lands on one stable schema that
    // actually holds rows — reader/writer agree even post-corruption.
    let guard = ENV_LOCK.get_or_init(|| Mutex::new(())).lock().unwrap();
    std::env::set_var("LEANKG_PG_URL", &url);
    let db = leankg::db::backend::init_db(&db_path).expect("fresh init_db");
    drop(guard);
    let ge = leankg::graph::GraphEngine::new(db);
    ge.invalidate_cache();
    let elements = ge.all_elements().expect("all_elements on fresh open");
    assert!(
        !elements.is_empty(),
        "fresh init_db must see the indexed elements"
    );
    assert_eq!(
        leankg::db::backend::schema_for_path(&db_path),
        schema_a,
        "engine must reopen the SAME schema the writer used ({schema_a})"
    );

    drop_schema(&url, &schema_a);
    let _ = std::fs::remove_dir_all(&fixture);
}
