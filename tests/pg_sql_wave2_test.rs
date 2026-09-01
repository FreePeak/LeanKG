//! W8 wave-2: SQL-first `code_elements` read parity tests.
//!
//! Locks the three converted element-lookup reads in `graph/query.rs`
//! (`find_element`, `get_elements_by_qualified_names`, `find_element_by_name`)
//! against the legacy Datalog behavior: keyed lookup, NULL optional columns
//! round-trip as `None`, the env-including vs env-omitting projections, and
//! the chunked IN-list path (dedup + drop-empties).
//!
//! Pattern: tests/pg_sql_wave1b_test.rs — #[ignore]-gated live-PG scratch.

use leankg::db::backend::{PostgresBackend, SharedDb};
use leankg::db::sql::SqlParam;
use leankg::graph::query::GraphEngine;
use std::sync::Arc;

fn pg_url() -> String {
    std::env::var("LEANKG_PG_URL")
        .unwrap_or_else(|_| "postgresql://postgres:postgres@localhost:5433/leankg".to_string())
}

struct Scratch {
    db: SharedDb,
}

impl Scratch {
    fn new(tag: &str) -> Scratch {
        let base = pg_url();
        static COUNTER: std::sync::atomic::AtomicU32 = std::sync::atomic::AtomicU32::new(0);
        let name = format!(
            "leankg_w8w2_{}_{}",
            std::process::id(),
            COUNTER.fetch_add(1, std::sync::atomic::Ordering::Relaxed)
        );
        let mut admin = leankg::db::backend::pg_connect(&base)
            .unwrap_or_else(|e| panic!("[{tag}] cannot connect to PG: {e}"));
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
        std::mem::forget(admin);

        let sep = if base.contains('?') { '&' } else { '?' };
        let url = format!("{base}{sep}options=-csearch_path%3D{name}%2Cpublic");
        let db: SharedDb = Arc::new(PostgresBackend {
            pg_url: url,
            schema: Some(name.clone()),
            pool: Arc::new(leankg::db::backend::ClientPool::new(2)),
            ro_pool: Arc::new(leankg::db::backend::ClientPool::new(2)),
            read_only: false,
            write_bus: None,
        });
        Scratch { db }
    }

    /// Insert one code_elements row. Optional columns pass NULL when `None`.
    fn put_element(&self, qn: &str, name: &str, env: &str, parent: Option<&str>) {
        let sql = "INSERT INTO code_elements (qualified_name, element_type, name, file_path, \
                   line_start, line_end, language, parent_qualified, cluster_id, cluster_label, \
                   metadata, env) \
                   VALUES ($1,'function',$2,'src/x.rs',10,20,'rust',$3,$4,$5,$6,$7)";
        self.db
            .sql_execute(
                sql,
                &[
                    SqlParam::Text(qn.to_string()),
                    SqlParam::Text(name.to_string()),
                    parent
                        .map(|p| SqlParam::Text(p.to_string()))
                        .unwrap_or(SqlParam::Null),
                    SqlParam::Null, // cluster_id
                    SqlParam::Null, // cluster_label
                    SqlParam::Json(serde_json::json!({"k": 1})),
                    SqlParam::Text(env.to_string()),
                ],
            )
            .unwrap();
    }
}

#[test]
#[ignore = "requires live Postgres (LEANKG_PG_URL); run with --ignored --test-threads=1"]
fn element_reads_sql_parity() {
    let s = Scratch::new("parity");
    let engine = GraphEngine::new(s.db.clone());

    s.put_element("src/x.rs::alpha", "alpha", "production", None);
    s.put_element("src/x.rs::beta", "beta", "staging", Some("src/x.rs::alpha"));

    // find_element: keyed lookup, env omitted (legacy 11-col projection).
    let a = engine
        .find_element("src/x.rs::alpha")
        .unwrap()
        .expect("alpha must exist");
    assert_eq!(a.name, "alpha");
    assert_eq!(a.line_start, 10);
    assert_eq!(a.line_end, 20);
    assert_eq!(a.metadata["k"], 1);
    assert!(a.parent_qualified.is_none());
    assert_eq!(a.env, "local"); // not selected by find_element

    // NULL optional columns round-trip as None (beta has parent, no cluster).
    let b = engine
        .find_element("src/x.rs::beta")
        .unwrap()
        .expect("beta must exist");
    assert_eq!(b.parent_qualified.as_deref(), Some("src/x.rs::alpha"));
    assert!(b.cluster_id.is_none());
    assert!(b.cluster_label.is_none());

    // Missing key → None.
    assert!(engine.find_element("nope::missing").unwrap().is_none());

    // get_elements_by_qualified_names: env IS selected (FR-SEM-07 keyed path).
    let qns = vec![
        "src/x.rs::alpha".to_string(),
        "src/x.rs::beta".to_string(),
        "src/x.rs::alpha".to_string(), // dup → fetched once
        String::new(),                 // empty → dropped
    ];
    let got = engine.get_elements_by_qualified_names(&qns).unwrap();
    assert_eq!(got.len(), 2, "dedup + drop-empties → exactly 2 rows");
    assert_eq!(got["src/x.rs::alpha"].env, "production");
    assert_eq!(got["src/x.rs::beta"].env, "staging");

    // find_element_by_name: first row by name, env omitted.
    let by_name = engine.find_element_by_name("beta").unwrap().expect("beta");
    assert_eq!(by_name.qualified_name, "src/x.rs::beta");
    assert_eq!(by_name.env, "local");
    assert!(engine.find_element_by_name("ghost").unwrap().is_none());
}
