//! H11 integration: `leankg export --markdown` end-to-end over live Postgres.
//!
//! Indexes a tiny 5-file Rust fixture into a scratch PG schema via the real
//! binary, exports Markdown graph docs through the same dispatch the CLI
//! uses, and asserts:
//!
//! - required sections exist, in order;
//! - overview counts match the database (`count_elements` / `count_relationships`);
//! - god-node rows are capped at 10 and sorted degree-descending;
//! - re-running the export is byte-deterministic once the single
//!   `generated_at:` front-matter line is ignored.
//!
//! Requires LEANKG_PG_URL pointing at a Postgres 18 + pgvector instance.
//! Skipped when it is unset or unreachable.

use std::path::PathBuf;
use std::process::Command;
use std::sync::{Mutex, OnceLock};

const BIN: &str = env!("CARGO_BIN_EXE_leankg");

static ENV_LOCK: OnceLock<Mutex<()>> = OnceLock::new();

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

fn engine_for(url: &str, db_path: &std::path::Path) -> leankg::graph::GraphEngine {
    let guard = ENV_LOCK.get_or_init(|| Mutex::new(())).lock().unwrap();
    std::env::set_var("LEANKG_PG_URL", url);
    let db = leankg::db::backend::init_db(db_path)
        .unwrap_or_else(|e| panic!("init_db({db_path:?}) failed: {e}"));
    drop(guard);
    leankg::graph::GraphEngine::new(db)
}

/// 5-file Rust fixture with cross-file calls so god nodes are non-trivial.
fn write_fixture(project: &std::path::Path) {
    let src = project.join("src");
    std::fs::create_dir_all(src.join("api")).expect("create src/api");
    std::fs::create_dir_all(src.join("db")).expect("create src/db");

    std::fs::write(
        src.join("main.rs"),
        "mod api;\nmod db;\nmod util;\n\nfn main() {\n    let s = db::store::Store::new();\n    api::handler::serve(&s);\n    util::log_line(\"boot\");\n}\n",
    )
    .expect("write main.rs");
    std::fs::write(
        src.join("util.rs"),
        "pub fn log_line(msg: &str) {\n    println!(\"{msg}\");\n}\n\npub fn version() -> &'static str {\n    \"0.1.0\"\n}\n",
    )
    .expect("write util.rs");
    std::fs::write(
        src.join("api").join("handler.rs"),
        "use crate::db::store::Store;\n\npub fn serve(store: &Store) {\n    store.get(\"k\");\n    super::routes::route(\"/\");\n}\n",
    )
    .expect("write handler.rs");
    std::fs::write(
        src.join("api").join("routes.rs"),
        "pub fn route(path: &str) -> String {\n    path.to_string()\n}\n",
    )
    .expect("write routes.rs");
    std::fs::write(
        src.join("db").join("store.rs"),
        "pub struct Store;\n\nimpl Store {\n    pub fn new() -> Self {\n        Store\n    }\n\n    pub fn get(&self, key: &str) -> Option<String> {\n        Some(key.to_string())\n    }\n}\n",
    )
    .expect("write store.rs");
}

fn run_export_cli(project: &std::path::Path, url: &str, out_file: &str) {
    let status = Command::new(BIN)
        .args(["export", "--markdown"])
        .arg("--out")
        .arg(out_file)
        .current_dir(project)
        .env("LEANKG_PG_URL", url)
        .status()
        .expect("spawn leankg export");
    assert!(
        status.success(),
        "`leankg export --markdown` failed: {status}"
    );
}

#[test]
fn export_markdown_end_to_end_matches_db_and_is_deterministic() {
    let url = base_url();
    if std::env::var("LEANKG_PG_URL").is_err() || !pg_reachable(&url) {
        eprintln!("skipping: LEANKG_PG_URL unset or unreachable");
        return;
    }

    let project: PathBuf =
        std::env::temp_dir().join(format!("leankg_exportmd_{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&project);
    std::fs::create_dir_all(project.join(".leankg")).expect("create .leankg");
    write_fixture(&project);

    // Start cold: drop this project's derived schema, then real `index`.
    let schema = leankg::db::backend::schema_for_path(&project.join(".leankg"));
    drop_schema(&url, &schema);

    let index_status = Command::new(BIN)
        .args(["index", "."])
        .current_dir(&project)
        .env("LEANKG_PG_URL", &url)
        .status()
        .expect("spawn leankg index");
    assert!(
        index_status.success(),
        "`leankg index` failed: {index_status}"
    );

    // Export #1 — relative out path anchors at the project root.
    let rel_out = "docs/graph-docs.md";
    run_export_cli(&project, &url, rel_out);
    let doc1_path = project.join(rel_out);
    let doc1 = std::fs::read_to_string(&doc1_path).expect("read doc #1");

    // Front matter + section order.
    assert!(
        doc1.starts_with("---\ntitle: LeanKG Graph Docs\n"),
        "{doc1}"
    );
    assert!(doc1.contains("\nproject: "), "{doc1}");
    let mut last = 0;
    for h in [
        "# LeanKG Graph Docs",
        "## Overview",
        "## Top Clusters",
        "## God Nodes (top 10 by degree)",
        "## Architecture Tree",
        "## Cluster Details",
    ] {
        let pos = doc1
            .find(h)
            .unwrap_or_else(|| panic!("missing {h}:\n{doc1}"));
        assert!(pos >= last, "section {h} out of order");
        last = pos;
    }

    // Counts match the DB through the lib API the CLI dispatches into.
    let engine = engine_for(&url, &project.join(".leankg"));
    let db_elements = engine.count_elements().expect("count_elements");
    let db_rels = engine.count_relationships().expect("count_relationships");
    assert!(db_elements > 0, "fixture must index elements");

    let elements_line = format!("- Elements: {db_elements}\n");
    let rels_line = format!("- Relationships: {db_rels}\n");
    assert!(
        doc1.contains(&elements_line),
        "element count mismatch: expected '{elements_line}' in:\n{}",
        &doc1[..doc1.find("### Elements by type").unwrap()]
    );
    assert!(
        doc1.contains(&rels_line),
        "relationship count mismatch: expected '{rels_line}'"
    );

    // God nodes: ≤10 rows, degrees non-increasing.
    let gn_start = doc1.find("| Degree | Qualified name | Type |").unwrap();
    let gn_end = doc1[gn_start..].find("\n\n").map(|i| gn_start + i).unwrap();
    let rows: Vec<usize> = doc1[gn_start..gn_end]
        .lines()
        .filter(|l| l.starts_with("| ") && !l.contains("Qualified"))
        .filter_map(|l| l.split('|').nth(1)?.trim().parse().ok())
        .collect();
    assert!(rows.len() <= 10, "god nodes must be capped at 10: {rows:?}");
    let mut sorted = rows.clone();
    sorted.sort_by(|a, b| b.cmp(a));
    assert_eq!(rows, sorted, "god nodes must be sorted degree-desc");

    // Export #2 — determinism: identical modulo the single timestamp line.
    run_export_cli(&project, &url, rel_out);
    let doc2 = std::fs::read_to_string(&doc1_path).expect("read doc #2");
    let strip = |s: &str| {
        s.lines()
            .filter(|l| !l.starts_with("generated_at:"))
            .collect::<Vec<_>>()
            .join("\n")
    };
    assert_eq!(
        strip(&doc1),
        strip(&doc2),
        "re-export must be byte-identical (modulo generated_at)"
    );
    assert_ne!(
        std::fs::metadata(&doc1_path).unwrap().len(),
        0,
        "artifact must not be empty"
    );

    drop_schema(&url, &schema);
    let _ = std::fs::remove_dir_all(&project);
}
