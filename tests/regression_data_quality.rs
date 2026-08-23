//! Regression test: data quality of a fresh full index (hackathon Cycle-2 R2c).
//!
//! Findings from `doctor --deep --deep` sweep N5 (docs/analysis/hackathon-sweep-R2.md):
//!   * F1 — orphaned relationships: edges whose endpoints never existed in
//!     code_elements (unresolved bare-name call targets, synthetic
//!     `event::<name>` targets with bare receiver sources, docs-hierarchy
//!     `contains` edges sourced from directory paths that were never written
//!     as elements).
//!   * F2 — duplicated qualified_names: repeated markdown headings (`### Fix`)
//!     produced multiple `doc_section` rows under one identical QN.
//!
//! A fresh index of a mixed fixture must yield ZERO duplicate qualified_names
//! and ZERO orphaned relationships.
//!
//! Requires LEANKG_PG_URL pointing at a Postgres instance (TLS URLs accepted;
//! verify-full is rewritten to require internally). Skipped when unset or
//! unreachable.

use std::path::PathBuf;
use std::sync::{Mutex, OnceLock};

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

fn write_fixture(root: &std::path::Path) -> (PathBuf, PathBuf) {
    let src = root.join("src");
    let docs = root.join("docs");
    std::fs::create_dir_all(&src).expect("mkdir src");
    std::fs::create_dir_all(&docs).expect("mkdir docs");

    // Two markdown files, EACH with a repeated heading name — the F2 shape.
    std::fs::write(
        docs.join("alpha.md"),
        "# Alpha\n\n## Context\n\nc\n\n## Fix\n\nfix one\n\n## Fix\n\nfix two\n",
    )
    .expect("write alpha.md");
    std::fs::write(
        docs.join("beta.md"),
        "# Beta\n\n## Fix\n\nother fix\n\n## Fix\n\nanother fix\n",
    )
    .expect("write beta.md");

    // Event channels + an unresolvable call target — the F1 shape.
    std::fs::write(
        src.join("events.ts"),
        concat!(
            "export function wire(): void {\n",
            "  emitter.emit('data:changed', payload);\n",
            "  bus.on('data:changed', handler);\n",
            "  totallyUnknownUtil();\n",
            "}\n",
        ),
    )
    .expect("write events.ts");

    (src.to_path_buf(), docs.to_path_buf())
}

#[test]
fn fresh_index_has_zero_duplicate_names_and_zero_orphan_edges() {
    let url = base_url();
    if !pg_reachable(&url) {
        eprintln!("skipping: LEANKG_PG_URL unset or unreachable");
        return;
    }

    let project: PathBuf =
        std::env::temp_dir().join(format!("leankg_data_quality_{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&project);
    let (src, docs_dir) = write_fixture(&project);

    let db_path = project.join(".leankg");
    let schema = leankg::db::backend::schema_for_path(&db_path);
    drop_schema(&url, &schema);

    let ge = engine_for(&url, &db_path);

    // Code phase (mirrors `leankg index ./src` — code files only).
    let files =
        leankg::indexer::find_files_sync(src.to_str().expect("utf8 src path")).expect("find files");
    assert!(!files.is_empty(), "fixture files must be discovered");
    leankg::indexer::index_files_parallel(&ge, &files, false).expect("index fixture");

    // Docs phase — TWICE, mirroring repeated MCP `index_docs` calls that
    // produced the ×5 stale-section duplicates in finding F2.
    leankg::doc_indexer::index_docs_directory(&docs_dir, &ge).expect("index docs");
    leankg::doc_indexer::index_docs_directory(&docs_dir, &ge).expect("re-index docs (idempotency)");

    // 1. Zero duplicate qualified_names across ALL indexed elements.
    let all = ge.all_elements().expect("all_elements");
    assert!(
        all.len() >= 10,
        "expected a populated corpus, got {} elements",
        all.len()
    );
    let mut counts: std::collections::HashMap<&str, usize> = std::collections::HashMap::new();
    for e in &all {
        *counts.entry(e.qualified_name.as_str()).or_insert(0) += 1;
    }
    let dupes: Vec<_> = counts.iter().filter(|(_, c)| **c > 1).collect();
    assert!(
        dupes.is_empty(),
        "duplicate qualified_names after fresh index: {dupes:?}"
    );

    // 2. Zero orphaned relationships — every edge endpoint must resolve.
    let qns: std::collections::HashSet<&str> =
        all.iter().map(|e| e.qualified_name.as_str()).collect();
    let rels = ge.all_relationships().expect("all_relationships");
    assert!(!rels.is_empty(), "expected relationships in fixture index");
    let orphans: Vec<&leankg::db::models::Relationship> = rels
        .iter()
        .filter(|r| {
            !qns.contains(r.source_qualified.as_str()) || !qns.contains(r.target_qualified.as_str())
        })
        .collect();
    assert!(
        orphans.is_empty(),
        "{} orphaned relationship(s) after fresh index; e.g. {:?}",
        orphans.len(),
        orphans
            .iter()
            .take(5)
            .map(|r| format!(
                "{}: {} -> {}",
                r.rel_type, r.source_qualified, r.target_qualified
            ))
            .collect::<Vec<_>>()
    );

    drop_schema(&url, &schema);
    let _ = std::fs::remove_dir_all(&project);
}
