//! W8 wave-1 parity + behavior tests: api_keys and content-hash paths
//! converted from legacy Datalog `run_script` scripts to parameterized SQL
//! through the [`leankg::db::sql`] seam (see the SQL-migration plan under
//! `docs/`).
//!
//! Pattern: tests/pg_schema_test.rs — each test builds a scratch schema in
//! the target Postgres (LEANKG_PG_URL; remote managed PG supported through
//! the crate TLS connector) and is #[ignore]-gated so the default
//! `cargo test` run skips it.
//!
//! Run against the provisioned remote PG:
//!   set -a; source .env; set +a
//!   cargo test --release --test pg_sql_wave1_test -- --ignored --test-threads=1
//!
//! Dual-path parity: the Datalog translator is still present during W8, so
//! writes/reads made through the NEW SQL seam are cross-checked with the
//! OLD `run_script` path against the SAME rows.

use leankg::db::backend::{pg_connect, PostgresBackend, SharedDb};
use leankg::db::keys::{ApiKey, ApiKeyStore};
use std::sync::Arc;

fn pg_url() -> String {
    std::env::var("LEANKG_PG_URL")
        .unwrap_or_else(|_| "postgresql://postgres:postgres@localhost:5433/leankg".to_string())
}

/// Scratch schema + backend pinned to it (per-test isolation; dropped at end
/// of scope via the leaked admin connection pattern used by pg_schema_test).
struct Scratch {
    db: SharedDb,
}

impl Scratch {
    fn new(tag: &str) -> Scratch {
        let base = pg_url();
        static COUNTER: std::sync::atomic::AtomicU32 = std::sync::atomic::AtomicU32::new(0);
        let name = format!(
            "leankg_w8test_{}_{}",
            std::process::id(),
            COUNTER.fetch_add(1, std::sync::atomic::Ordering::Relaxed)
        );
        let mut admin = pg_connect(&base)
            .unwrap_or_else(|e| panic!("[{tag}] cannot connect to {}: {e}", "<pg>"));
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
        // Leak the admin connection so the scratch schema outlives this call
        // for the duration of the test process (same trick as lib tests);
        // schema is namespaced per-pid so CI reruns never collide.
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

#[allow(dead_code)]
impl Scratch {
    fn raw(&self, sql: &str) -> Vec<leankg::db::sql::SqlRow> {
        self.db.sql_query(sql, &[]).expect("raw sql")
    }
}

fn sorted_ids(keys: &[ApiKey]) -> Vec<String> {
    let mut v: Vec<String> = keys.iter().map(|k| k.id.clone()).collect();
    v.sort();
    v
}

#[test]
#[ignore = "requires live Postgres (LEANKG_PG_URL); run with --ignored"]
fn api_key_lifecycle_via_sql_seam() {
    let s = Scratch::new("lifecycle");
    let store = ApiKeyStore::with_db(s.db.clone());

    // CREATE
    let (secret, created) = store.create_key("w8-lifecycle").expect("create");
    assert!(!secret.is_empty());
    assert_eq!(created.name, "w8-lifecycle");

    // LIST: hash is blanked for display, name preserved
    let keys = store.list_keys().expect("list");
    assert_eq!(keys.len(), 1);
    assert_eq!(keys[0].id, created.id);
    assert_eq!(keys[0].name, "w8-lifecycle");
    assert_eq!(keys[0].key_hash, "", "hash must not surface in listings");

    // VALIDATE: correct key resolves to its id
    let got = store.validate_key(&secret).expect("validate");
    assert_eq!(got.as_deref(), Some(created.id.as_str()));

    // VALIDATE side effects: last_used recorded; identity PRESERVED
    // (deviation guard — the legacy path wiped name/created_at here).
    let rows = s.raw(&format!(
        "SELECT name, created_at, last_used_at FROM api_keys WHERE id = '{}'",
        created.id
    ));
    assert_eq!(rows.len(), 1);
    assert_eq!(rows[0].text("name").as_deref(), Some("w8-lifecycle"));
    assert_eq!(
        rows[0].text("created_at").as_deref(),
        Some(created.created_at.as_str())
    );
    assert!(rows[0].text("last_used_at").is_some(), "last_used recorded");

    // VALIDATE: wrong key rejected
    let bad = store.validate_key("lkkg_bogus_key_0000").expect("validate");
    assert_eq!(bad, None);

    // REVOKE
    assert!(store.revoke_key(&created.id).expect("revoke"));
    assert!(
        !store.revoke_key(&created.id).expect("revoke twice"),
        "second revoke must report false"
    );
    let after = store.validate_key(&secret).expect("validate revoked");
    assert_eq!(after, None, "revoked key must not validate");
    let listed = store.list_keys().expect("list after revoke");
    assert!(listed.is_empty(), "revoked keys are excluded from listings");
}

#[test]
#[ignore = "requires live Postgres (LEANKG_PG_URL); run with --ignored"]
fn api_key_dual_path_parity_old_datalog_vs_new_sql() {
    let s = Scratch::new("parity");
    let store = ApiKeyStore::with_db(s.db.clone());

    // Write A: NEW typed method.
    let (_sec_a, ka) = store.create_key("via-sql").expect("create sql");
    // Write B: OLD Datalog :put through run_script (translator still alive
    // during W8 — this IS the pre-conversion reference path).
    let legacy_id = "legacy-key-id";
    let script = r#"?[id, name, key_hash, created_at, last_used_at, revoked_at] <- [[$id, $name, $key_hash, $created_at, $last_used_at, $revoked_at]]
        :put api_keys { id, name, key_hash, created_at, last_used_at, revoked_at }"#;
    let params: std::collections::BTreeMap<String, serde_json::Value> =
        std::collections::BTreeMap::from([
            ("id".into(), serde_json::json!(legacy_id)),
            ("name".into(), serde_json::json!("legacy")),
            (
                "key_hash".into(),
                serde_json::json!("$argon2id$v=19$m=19456,t=2,p=1$deadbeef$cafebabe"),
            ),
            ("created_at".into(), serde_json::json!("1700000000")),
            ("last_used_at".into(), serde_json::Value::Null),
            ("revoked_at".into(), serde_json::Value::Null),
        ]);
    s.db.run_script(script, params).expect("legacy put");

    // Cross-read: OLD read path sees BOTH writes identically to NEW read path.
    let old_view = s
        .db
        .run_script(
            "?[id, name, key_hash, created_at, last_used_at, revoked_at] := *api_keys[id, name, key_hash, created_at, last_used_at, revoked_at]",
            std::collections::BTreeMap::new(),
        )
        .expect("datalog read");
    let new_view: Vec<ApiKey> = s.db.list_api_keys().expect("sql read");
    assert_eq!(old_view.rows.len(), 2, "{old_view:?}");
    assert_eq!(new_view.len(), 2);

    let old_rows: Vec<(String, String)> = old_view
        .rows
        .iter()
        .map(|r| {
            (
                r[0].get_str().unwrap_or("").to_string(),
                r[2].get_str().unwrap_or("").to_string(),
            )
        })
        .collect();
    let new_rows: Vec<(String, String)> = new_view
        .iter()
        .map(|k| (k.id.clone(), k.key_hash.clone()))
        .collect();
    let mut o = old_rows;
    o.sort();
    let mut n = new_rows;
    n.sort();
    assert_eq!(o, n, "(id, key_hash) tuples must match across both paths");

    // Revoke through the NEW path, verify through the OLD path.
    assert!(store.revoke_key(legacy_id).expect("revoke"));
    let still_there =
        s.db.run_script(
            "?[id, revoked_at] := *api_keys[id, revoked_at], id = $id",
            std::collections::BTreeMap::from([("id".into(), serde_json::json!(legacy_id))]),
        )
        .expect("read back");
    assert_eq!(still_there.rows.len(), 1);
    assert_eq!(
        still_there.rows[0][1].get_str().map(String::from).is_some(),
        true,
        "revoked_at must now be set"
    );

    let _ = ka;
}

#[test]
#[ignore = "requires live Postgres (LEANKG_PG_URL); run with --ignored"]
fn api_keys_multiple_and_listing_filter() {
    let s = Scratch::new("multi");
    let store = ApiKeyStore::with_db(s.db.clone());
    let mut ids = Vec::new();
    for i in 0..4 {
        let (_, k) = store.create_key(&format!("key-{i}")).expect("create");
        ids.push(k.id);
    }
    // Revoke two.
    assert!(store.revoke_key(&ids[0]).unwrap());
    assert!(store.revoke_key(&ids[2]).unwrap());
    let listed = store.list_keys().unwrap();
    let mut want: Vec<String> = vec![ids[1].clone(), ids[3].clone()];
    want.sort();
    let got = sorted_ids(&listed);
    assert_eq!(got, want);
}

#[test]
#[ignore = "requires live Postgres (LEANKG_PG_URL); run with --ignored"]
fn content_hashes_roundtrip_upsert_through_seam() {
    use leankg::indexer::content_hash::{load_hashes, save_hashes, IndexHashRow};

    let s = Scratch::new("hashes");
    let rows = vec![
        IndexHashRow {
            path: "src/a.rs".into(),
            hash: "aaa".into(),
        },
        IndexHashRow {
            path: "src/b.rs".into(),
            hash: "bbb".into(),
        },
    ];
    save_hashes(&*s.db, &rows).expect("save");

    // Upsert: same path, new hash; plus one brand-new path.
    let updated = vec![
        IndexHashRow {
            path: "src/a.rs".into(),
            hash: "aaa2".into(),
        },
        IndexHashRow {
            path: "src/c.rs".into(),
            hash: "ccc".into(),
        },
    ];
    save_hashes(&*s.db, &updated).expect("resave");

    let loaded = load_hashes(&*s.db).expect("load");
    let mut got: Vec<(String, String)> = loaded
        .iter()
        .map(|r| (r.path.clone(), r.hash.clone()))
        .collect();
    got.sort();
    assert_eq!(
        got,
        vec![
            ("src/a.rs".into(), "aaa2".into()), // overwritten, not duplicated
            ("src/b.rs".into(), "bbb".into()),
            ("src/c.rs".into(), "ccc".into()),
        ]
    );

    // Empty save is a no-op that must NOT wipe existing rows.
    save_hashes(&*s.db, &[]).expect("empty save");
    assert_eq!(load_hashes(&*s.db).unwrap().len(), 3);
}

#[test]
#[ignore = "requires live Postgres (LEANKG_PG_URL); run with --ignored"]
fn content_hash_load_empty_when_relation_empty() {
    use leankg::indexer::content_hash::load_hashes;
    let s = Scratch::new("hashes-empty");
    let loaded = load_hashes(&*s.db).expect("load on empty table");
    assert!(loaded.is_empty());
}
