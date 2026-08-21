//! Regression test: duplicate qualified_names across same-named symbols.
//!
//! Finding A of docs/plan-migrate-cozo-to-postgres-pgvector.md (§9 / finding 3
//! in docs/analysis/pg-perf-large-codebase.md): a real workspace index held
//! 727k code_elements rows but only 348k distinct qualified_names (52%
//! duplicates, up to 764 rows under one `...pb.validate.go::Error` key).
//! Keyed downstream stores (embedding_state / embedding_vectors PK =
//! qualified_name) silently merge distinct symbols into one embedding row.
//!
//! Fix: extraction-time disambiguation (`EntityExtractor::extract` now ends
//! with `disambiguate_qualified_names`, and receiver-less Go methods derive
//! their parent type from the receiver) — every element of a file batch gets
//! a unique qualified_name before it reaches the database.
//!
//! Requires LEANKG_PG_URL pointing at a Postgres 18 + pgvector instance
//! (TLS URLs accepted). Skipped when unset or unreachable.

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

fn write_fixture(root: &std::path::Path) {
    let src = root.join("src");
    std::fs::create_dir_all(&src).expect("mkdir fixture src");
    // The production collision shape: two message structs, each with an
    // `Error() string` method, plus TS/Rust variants of the same pattern.
    std::fs::write(
        root.join("src").join("msg.pb.validate.go"),
        concat!(
            "package pb\n",
            "\n",
            "type MsgA struct{}\n",
            "\n",
            "func (m *MsgA) Error() string { return \"a\" }\n",
            "\n",
            "type MsgB struct{}\n",
            "\n",
            "func (m *MsgB) Error() string { return \"b\" }\n",
        ),
    )
    .expect("write go fixture");
    std::fs::write(
        root.join("src").join("views.ts"),
        concat!(
            "class Alpha { render(): number { return 1; } }\n",
            "class Beta { render(): number { return 2; } }\n",
        ),
    )
    .expect("write ts fixture");
    std::fs::write(
        root.join("src").join("units.rs"),
        concat!(
            "pub struct Alpha;\n",
            "pub struct Beta;\n",
            "impl Alpha { pub fn new() -> Self { Alpha } }\n",
            "impl Beta { pub fn new() -> Self { Beta } }\n",
        ),
    )
    .expect("write rust fixture");
}

#[test]
fn indexed_symbols_are_unique_in_code_elements_and_embedding_state() {
    let url = base_url();
    if !pg_reachable(&url) {
        eprintln!("skipping: LEANKG_PG_URL unset or unreachable");
        return;
    }

    let project: PathBuf =
        std::env::temp_dir().join(format!("leankg_qn_collision_{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&project);
    write_fixture(&project);

    let db_path = project.join(".leankg");
    let schema = leankg::db::backend::schema_for_path(&db_path);
    drop_schema(&url, &schema);

    let ge = engine_for(&url, &db_path);
    let files = leankg::indexer::find_files_sync(project.to_str().expect("utf8 project path"))
        .expect("find files");
    assert!(!files.is_empty(), "fixture files must be discovered");

    leankg::indexer::index_files_parallel(&ge, &files, false).expect("index fixture");

    // 1. Every indexed element carries a unique qualified_name.
    let all = ge.all_elements().expect("all_elements");
    assert!(all.len() >= 6, "expected the fixture symbols, got {all:?}");
    let mut counts: std::collections::HashMap<&str, usize> = std::collections::HashMap::new();
    for e in &all {
        *counts.entry(e.qualified_name.as_str()).or_insert(0) += 1;
    }
    let dupes: Vec<_> = counts
        .iter()
        .filter(|(_, c)| **c > 1)
        .map(|(qn, c)| (*qn, *c))
        .collect();
    assert!(
        dupes.is_empty(),
        "indexed elements still collide on qualified_name: {dupes:?}"
    );

    // The disambiguated instances keep their parent symbol readable.
    let go_errors: Vec<&str> = all
        .iter()
        .filter(|e| e.language == "go" && e.name == "Error")
        .map(|e| e.qualified_name.as_str())
        .collect();
    assert_eq!(go_errors.len(), 2, "both Go Error methods must be indexed");

    // 2. contains edges survive the rewrite: every target resolves.
    let qns: std::collections::HashSet<&str> =
        all.iter().map(|e| e.qualified_name.as_str()).collect();
    let rels = ge.all_relationships().expect("all_relationships");
    for rel in rels.iter().filter(|r| r.rel_type == "contains") {
        assert!(
            qns.contains(rel.target_qualified.as_str()),
            "contains edge points at missing element {}: source={}",
            rel.target_qualified,
            rel.source_qualified
        );
    }

    // 3. Embed-state write path: one state row per distinct symbol — no
    // silent PK merge of previously colliding keys. This is the exact keyed
    // write `embeddings::state::mark_stale_for_qualified_names` performs
    // (import_relations → INSERT .. ON CONFLICT (qualified_name)); that
    // module is behind --features embeddings, so the write is issued here
    // directly against the same table.
    use leankg::db::backend::{DataValue, NamedRows};
    let now = "2026-08-21T00:00:00Z";
    let rows: Vec<Vec<DataValue>> = all
        .iter()
        .map(|e| {
            vec![
                DataValue::Str(e.qualified_name.as_str().into()),
                DataValue::from(0i64),
                DataValue::Str("".into()),
                DataValue::Str("stale".into()),
                DataValue::Str(now.into()),
            ]
        })
        .collect();
    let named = NamedRows::new(
        vec![
            "qualified_name".to_string(),
            "usearch_key".to_string(),
            "content_hash".to_string(),
            "state".to_string(),
            "embedded_at".to_string(),
        ],
        rows,
    );
    let mut batch = std::collections::BTreeMap::new();
    batch.insert("embedding_state".to_string(), named);
    ge.db_arc()
        .as_ref()
        .import_relations(batch)
        .expect("embedding_state upsert");

    let count = ge
        .db_arc()
        .as_ref()
        .run_script(
            "?[n] := *embedding_state[qualified_name, _, _, _, _], n = count(qualified_name)",
            Default::default(),
        )
        .expect("count embedding_state");
    let stored = count.rows[0][0].get_int().expect("int count") as usize;
    assert_eq!(
        stored,
        all.len(),
        "embedding_state merged distinct symbols ({}) into {} rows",
        all.len(),
        stored
    );

    drop_schema(&url, &schema);
    let _ = std::fs::remove_dir_all(&project);
}
