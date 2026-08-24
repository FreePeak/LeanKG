//! W8 wave-1b: knowledge_entries parameterized-SQL parity tests.
//!
//! Locks the semantics of the converted `db::mod` knowledge fns against the
//! legacy Datalog behavior: upsert-by-id, NULL optionals round-trip as None,
//! ILIKE substring search with exact-match filters, by-element/feature/env
//! lists, and delete-by-id absence tolerance.
//!
//! Pattern: tests/pg_sql_wave1_test.rs — #[ignore]-gated live-PG scratch.

use leankg::db::backend::{DbBackend, PostgresBackend, SharedDb};
use leankg::db::models::KnowledgeEntry;
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
            "leankg_w8b_{}_{}",
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
}

fn entry(id: &str, title: &str, content: &str) -> KnowledgeEntry {
    KnowledgeEntry {
        id: id.into(),
        knowledge_type: "decision".into(),
        title: title.into(),
        content: content.into(),
        element_qualified: Some(format!("src/{id}.rs::thing")),
        user_story_id: None,
        feature_id: None,
        tags: "[\"w8\"]".into(),
        environment: "production".into(),
        branch: None,
        author: "wave1b-test".into(),
        created_at: 1_700_000_000,
        updated_at: 1_700_000_100,
    }
}

#[test]
#[ignore = "requires live Postgres (LEANKG_PG_URL); run with --ignored --test-threads=1"]
fn knowledge_entries_sql_parity_round_trip() {
    let s = Scratch::new("parity");
    let db = s.db.as_ref();

    // create + get (second entry exercises NULL optional columns)
    let a = entry("w8b-1", "Fix auth flow", "token refresh races under load");
    leankg::db::create_knowledge_entry(db, &a).unwrap();
    let mut b = entry("w8b-2", "Overview cache", "l1 warm path");
    b.element_qualified = None;
    b.branch = None;
    b.user_story_id = Some("US-9".into());
    leankg::db::create_knowledge_entry(db, &b).unwrap();

    let got = leankg::db::get_knowledge_entry(db, "w8b-1")
        .unwrap()
        .expect("row must exist");
    assert_eq!(got.title, "Fix auth flow");
    assert_eq!(
        got.element_qualified.as_deref(),
        Some("src/w8b-1.rs::thing")
    );
    assert_eq!(got.tags, "[\"w8\"]");

    // update via upsert — exactly one row per id afterwards
    let mut a2 = a.clone();
    a2.title = "Fix auth flow v2".into();
    a2.updated_at = 1_700_000_200;
    leankg::db::update_knowledge_entry(db, &a2).unwrap();
    let got = leankg::db::get_knowledge_entry(db, "w8b-1")
        .unwrap()
        .unwrap();
    assert_eq!(got.title, "Fix auth flow v2");
    assert_eq!(got.updated_at, 1_700_000_200);
    let all = leankg::db::search_knowledge(db, "auth flow", None, None, 10).unwrap();
    assert_eq!(all.iter().filter(|e| e.id == "w8b-1").count(), 1);

    // NULL optional columns survive the round trip as None (wave-1 SqlRow bug)
    let got_b = leankg::db::get_knowledge_entry(db, "w8b-2")
        .unwrap()
        .unwrap();
    assert!(got_b.element_qualified.is_none(), "NULL must stay None");
    assert!(got_b.branch.is_none());
    assert_eq!(got_b.user_story_id.as_deref(), Some("US-9"));

    // search: case-insensitive substring + exact filters
    let hits = leankg::db::search_knowledge(db, "TOKEN REFRESH", None, None, 10).unwrap();
    assert_eq!(hits.len(), 1);
    assert_eq!(hits[0].id, "w8b-1");
    let none =
        leankg::db::search_knowledge(db, "auth", Some("nonexistent-type"), None, 10).unwrap();
    assert!(none.is_empty());

    // by_element / by_feature / by_environment
    let by_el = leankg::db::get_knowledge_by_element(db, "src/w8b-1.rs::thing").unwrap();
    assert_eq!(by_el.len(), 1);
    assert!(leankg::db::get_knowledge_by_feature(db, "FEAT-X")
        .unwrap()
        .is_empty());
    let envd = leankg::db::get_knowledge_by_environment(db, "production", 10).unwrap();
    assert!(envd.len() >= 2);

    // delete via trait method: present removes → true; absent tolerated → false
    assert!(db.delete_knowledge_entry_by_id("w8b-1").unwrap());
    assert!(!db.delete_knowledge_entry_by_id("w8b-1").unwrap());
    assert!(leankg::db::get_knowledge_entry(db, "w8b-1")
        .unwrap()
        .is_none());
}
