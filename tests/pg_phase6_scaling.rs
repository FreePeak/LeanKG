//! Phase 6 — read-only + server scaling semantics (plan T6.1-T6.4).
//!
//! Requires the Phase 0 Postgres container (Postgres 18 + pgvector):
//!   docker exec leankg-pg-phase0 psql -U postgres -d leankg -c "CREATE EXTENSION IF NOT EXISTS vector;"
//!
//! Run only these:
//!   LEANKG_PG_URL=postgresql://postgres:postgres@localhost:5433/leankg \
//!     cargo test --release --test pg_phase6_scaling -- --include-ignored --test-threads=1
//!
//! Every test is #[ignore]-gated by default so `cargo test` skips them
//! (the container is not required on dev machines).

use leankg::db::backend::{ClientPool, DbBackend, PostgresBackend};
use std::collections::BTreeMap;
use std::env;
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::{Arc, Mutex};

/// Serialize integration tests: each DROPs/CREATEs a shared scratch schema.
/// Same PG_LOCK pattern as the other pg_* test files.
static PG_LOCK: Mutex<()> = Mutex::new(());

fn pg_lock() -> std::sync::MutexGuard<'static, ()> {
    PG_LOCK.lock().unwrap_or_else(|e| e.into_inner())
}

fn pg_url() -> String {
    env::var("LEANKG_PG_URL")
        .unwrap_or_else(|_| "postgresql://postgres:postgres@localhost:5433/leankg".to_string())
}

fn scratch_schema_name() -> String {
    static COUNTER: AtomicU32 = AtomicU32::new(0);
    format!(
        "leankg_p6_{}_{}",
        std::process::id(),
        COUNTER.fetch_add(1, Ordering::Relaxed)
    )
}

/// A scratch schema in the dev PG container, dropped on test exit. All
/// backends built from it pin `search_path` so tests never touch `public`.
struct Scratch {
    admin: postgres::Client,
    name: String,
    rw_url: String,
    ro_url: String,
}

impl Scratch {
    fn new() -> Self {
        let base = pg_url();
        let name = scratch_schema_name();
        let mut admin = postgres::Client::connect(&base, postgres::NoTls)
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
        let rw_url = format!("{base}{sep}options=-csearch_path%3D{name}%2Cpublic");
        let ro_url = format!(
            "{base}{sep}options=-csearch_path%3D{name}%2Cpublic%20-cdefault_transaction_read_only%3Don"
        );
        Scratch {
            admin,
            name,
            rw_url,
            ro_url,
        }
    }

    fn rw_backend(&self) -> Arc<PostgresBackend> {
        Arc::new(PostgresBackend {
            pg_url: self.rw_url.clone(),
            pool: Arc::new(ClientPool::new(5)),
            ro_pool: Arc::new(ClientPool::new(5)),
            read_only: false,
        })
    }

    fn ro_backend(&self) -> Arc<PostgresBackend> {
        Arc::new(PostgresBackend {
            pg_url: self.rw_url.clone(),
            pool: Arc::new(ClientPool::new(5)),
            ro_pool: Arc::new(ClientPool::new(5)),
            read_only: true,
        })
    }
}

impl Drop for Scratch {
    fn drop(&mut self) {
        let _ = self
            .admin
            .batch_execute(&format!("DROP SCHEMA IF EXISTS {} CASCADE", self.name));
    }
}

/// Seed one row into an EXISTING schema.sql table (index_hashes: path PK,
/// hash) via the RW backend so RO/visibility tests have data. `:create` is
/// a DdlNoop on PG — the table must pre-exist in schema.sql.
fn seed_probe(db: &dyn DbBackend) {
    // Params must be plain strings: index_hashes.path / .hash are TEXT
    // columns — serde_json::Value params type as jsonb and the TEXT column
    // rejects them at bind time.
    let q = r#"?[path, hash] <- [[$id, $val]] :put index_hashes {path, hash}"#;
    let mut p = BTreeMap::new();
    p.insert("id".into(), serde_json::Value::String("p6-k1".into()));
    p.insert("val".into(), serde_json::Value::String("hash1".into()));
    db.run_script(q, p).unwrap();
}

/// T6.1 — a read-only backend REJECTS writes at the Postgres layer.
#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn read_only_backend_rejects_writes_but_serves_reads() {
    let _g = pg_lock();
    let s = Scratch::new();
    // Seed the table definition via the RW backend (schema runs in
    // migrations; probe is a keyed table so :create is a no-op on PG —
    // the translator DdlNoop path). Use :put directly.
    let rw = s.rw_backend();
    seed_probe(rw.as_ref());

    let ro = s.ro_backend();
    // Reads work through the RO connection.
    let q = r#"?[path] := *index_hashes[path, _]"#;
    let res = ro.run_script(q, BTreeMap::new()).unwrap();
    assert_eq!(res.rows.len(), 1, "RO read must see seeded row");

    // Writes fail with a clean error (Postgres read-only transaction), NOT
    // silent corruption.
    let mut p = BTreeMap::new();
    p.insert("id".into(), serde_json::Value::String("p6-k2".into()));
    p.insert("val".into(), serde_json::Value::String("hash2".into()));
    let err = ro
        .run_script(
            r#"?[path, hash] <- [[$id, $val]] :put index_hashes {path, hash}"#,
            p,
        )
        .expect_err("RO backend must reject :put");
    // The postgres crate surfaces RO violations as "db error" with the
    // real message on the DbError (SQLSTATE 25006 = read_only_sql_transaction).
    let msg = err.to_string();
    let is_ro = if let Some(pg_err) = err.downcast_ref::<postgres::Error>() {
        pg_err
            .as_db_error()
            .map(|d| d.message().contains("read-only"))
            .unwrap_or(false)
    } else {
        msg.contains("read-only") || msg.contains("read only")
    };
    assert!(
        is_ro,
        "write rejection must be the PG read-only error (SQLSTATE 25006), got: {msg}"
    );

    // And the row really did NOT land.
    let res = rw
        .run_script(r#"?[path] := *index_hashes[path, _]"#, BTreeMap::new())
        .unwrap();
    assert_eq!(res.rows.len(), 1, "rejected write must not have landed");
}

/// T6.1 — the RO URL builder merges cleanly with an existing search_path
/// option (two separate `-c` flags in one options param).
#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn read_only_url_merges_search_path_option() {
    let _g = pg_lock();
    let s = Scratch::new();
    let ro = s.ro_backend();
    let url = ro.read_only_url();
    assert!(
        url.contains("search_path%3D") && url.contains("default_transaction_read_only%3Don"),
        "RO URL must carry both options: {url}"
    );
    // Connect with it and confirm the session is actually read-only.
    let mut c = postgres::Client::connect(&url, postgres::NoTls).unwrap();
    let ro_flag: String = c
        .query_one("SHOW default_transaction_read_only", &[])
        .unwrap()
        .get(0);
    assert_eq!(ro_flag, "on", "session must be read-only");
    let sp: String = c.query_one("SHOW search_path", &[]).unwrap().get(0);
    assert!(sp.starts_with(&s.name), "search_path must be pinned: {sp}");
}

/// T6.3 — the pool serves N concurrent reads; live connections never exceed
/// the pool size.
#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn pool_serves_concurrent_reads_without_exceeding_capacity() {
    let _g = pg_lock();
    let s = Scratch::new();
    let rw = s.rw_backend();
    seed_probe(rw.as_ref());

    let pool_size = 5usize;
    let pool = ClientPool::new(pool_size);
    let n_threads = 8; // > pool size: forces reuse/waiting
    let mut handles = Vec::new();
    let url = s.rw_url.clone();
    for _ in 0..n_threads {
        let pool = pool.clone();
        let url = url.clone();
        handles.push(std::thread::spawn(move || {
            let mut client = pool.checkout(&url).unwrap();
            let rows = client.query("SELECT 1", &[]).unwrap();
            rows[0].get::<_, i32>(0)
        }));
    }
    for h in handles {
        assert_eq!(h.join().unwrap(), 1, "concurrent read must succeed");
    }
    assert!(
        pool.live_count() <= pool_size,
        "live connections {} must stay within pool size {pool_size}",
        pool.live_count()
    );
}

/// T6.3 — pool checkout blocks at capacity and reuses returned clients
/// (two sequential checkouts reuse ONE connection).
#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn pool_reuses_returned_clients() {
    let _g = pg_lock();
    let s = Scratch::new();
    let pool = ClientPool::new(2);
    let url = s.rw_url.clone();

    let mut a = pool.checkout(&url).unwrap();
    let a_id = a
        .query_one("SELECT pg_backend_pid()", &[])
        .unwrap()
        .get::<_, i32>(0);
    drop(a);
    let mut b = pool.checkout(&url).unwrap();
    let b_id = b
        .query_one("SELECT pg_backend_pid()", &[])
        .unwrap()
        .get::<_, i32>(0);
    drop(b);
    assert_eq!(
        a_id, b_id,
        "second checkout must reuse the returned connection (pid {a_id} vs {b_id})"
    );
    assert_eq!(pool.live_count(), 1, "one live connection after reuse");
}

/// T6.4b — the advisory lock serializes index jobs: a second session cannot
/// take the lock while the first holds it, and CAN after release.
#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn advisory_lock_serializes_second_holder() {
    let _g = pg_lock();
    let s = Scratch::new();
    let key = PostgresBackend::INDEX_LOCK_KEY;

    let a = s.rw_backend();
    let lock_a = a
        .advisory_lock(key)
        .expect("first lock acquisition must succeed");
    let b = s.rw_backend();
    // Non-blocking try from another backend/session.
    let try_b = b.try_advisory_lock(key).expect("try lock must not error");
    assert!(
        try_b.is_none(),
        "second holder must NOT get the lock while first holds it"
    );

    drop(lock_a);
    let try_b2 = b
        .try_advisory_lock(key)
        .expect("try lock after release must not error");
    assert!(
        try_b2.is_some(),
        "second holder must get the lock after the first releases"
    );
    drop(try_b2);
}

/// T6.4a — two backend instances (two "server processes") on the SAME
/// Postgres: a write via A is visible via B (no per-instance masking at
/// the backend layer; the moka L1 cache is the only per-instance cache and
/// is bypassed by raw run_script).
#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn two_instances_write_visibility() {
    let _g = pg_lock();
    let s = Scratch::new();
    let a = s.rw_backend();
    let b = s.rw_backend();
    seed_probe(a.as_ref());

    // B sees A's write immediately (no cache at this layer).
    let res = b
        .run_script(r#"?[hash] := *index_hashes[path, hash], path = $id"#, {
            let mut p = BTreeMap::new();
            p.insert("id".into(), serde_json::json!("p6-k1"));
            p
        })
        .unwrap();
    assert_eq!(res.rows.len(), 1, "instance B must see instance A's write");
    assert_eq!(
        res.rows[0][0].get_str(),
        Some("hash1"),
        "value must round-trip through PG"
    );

    // And a write via B is visible back on A.
    let mut p = BTreeMap::new();
    p.insert("id".into(), serde_json::Value::String("p6-k2".into()));
    p.insert("val".into(), serde_json::Value::String("hash2".into()));
    b.run_script(
        r#"?[path, hash] <- [[$id, $val]] :put index_hashes {path, hash}"#,
        p,
    )
    .unwrap();
    let res = a
        .run_script(r#"?[hash] := *index_hashes[path, hash], path = $id"#, {
            let mut p = BTreeMap::new();
            p.insert("id".into(), serde_json::json!("p6-k2"));
            p
        })
        .unwrap();
    assert_eq!(res.rows.len(), 1, "instance A must see instance B's write");
}
