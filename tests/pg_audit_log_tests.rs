//! FR-ENT-1 (backlog H2) audit-log integration tests — live Postgres.
//!
//! Connection pattern follows tests/pg_schema_test.rs: a scratch schema
//! `leankg_test_<pid>_<n>` per test, migrations run inside it, dropped on
//! scope exit. The shared `leankg` database is never touched.
//!
//! Run:
//!   cargo test --release --test pg_audit_log_tests -- --ignored

use leankg::audit::{
    jsonl_to_rows, rows_to_jsonl, verify_chain, AuditEntry, AuditRecord, GENESIS_HASH,
};
use std::env;

fn pg_url() -> String {
    env::var("LEANKG_PG_URL")
        .unwrap_or_else(|_| "postgresql://postgres:postgres@localhost:5433/leankg".to_string())
}

/// Scratch schema guard (same shape as tests/pg_schema_test.rs).
///
/// The sync `postgres::Client` must never be DROPPED on a tokio worker
/// thread (its internal connection runtime panics when nested), so async
/// tests call [`ScratchSchema::dispose`] — which drops + drops the schema
/// inside `spawn_blocking`. Sync tests may just let it fall out of scope.
struct ScratchSchema {
    client: Option<postgres::Client>,
    name: String,
}

impl ScratchSchema {
    /// Standard per-test scratch schema (pid + counter keyed).
    fn new() -> ScratchSchema {
        static COUNTER: std::sync::atomic::AtomicU32 = std::sync::atomic::AtomicU32::new(0);
        Self::with_forced_name(&format!(
            "leankg_test_{}_{}",
            std::process::id(),
            COUNTER.fetch_add(1, std::sync::atomic::Ordering::Relaxed)
        ))
    }

    /// Scratch schema with an EXACT name — used when tests must address the
    /// same schema derivation the production code performs (e.g. the
    /// audit-opener pinning test).
    fn with_forced_name(name: &str) -> ScratchSchema {
        let url = pg_url();
        let mut admin = leankg::db::backend::pg_connect(&url)
            .unwrap_or_else(|e| panic!("cannot connect to {url}: {e}"));
        admin
            .batch_execute(&format!("DROP SCHEMA IF EXISTS {name} CASCADE"))
            .unwrap();
        admin
            .batch_execute(&format!("CREATE SCHEMA {name}"))
            .unwrap();
        admin
            .batch_execute(&format!("SET search_path TO {name}, public"))
            .unwrap();
        ScratchSchema {
            client: Some(admin),
            name: name.to_string(),
        }
    }

    /// Mutable access to the admin connection (migrations / assertions).
    fn conn(&mut self) -> &mut postgres::Client {
        self.client.as_mut().expect("connection not yet disposed")
    }

    /// A PostgresBackend whose pool connections land in the scratch schema.
    fn backend(&self) -> leankg::db::backend::PostgresBackend {
        leankg::db::backend::PostgresBackend {
            pg_url: pg_url(),
            schema: None,
            pool: Arc::new(leankg::db::backend::ClientPool::new(2)),
            ro_pool: Arc::new(leankg::db::backend::ClientPool::new(2)),
            read_only: false,
            write_bus: None,
        }
        .with_schema(&self.name)
    }

    /// Drop the schema and close the admin connection OFF the tokio runtime.
    async fn dispose(mut self) {
        if let Some(mut client) = self.client.take() {
            let name = self.name.clone();
            let _ = tokio::task::spawn_blocking(move || {
                let _ = client.batch_execute(&format!("DROP SCHEMA IF EXISTS {name} CASCADE"));
                drop(client);
            })
            .await;
        }
    }
}

impl Drop for ScratchSchema {
    fn drop(&mut self) {
        // The sync postgres Client must never be dropped ON a tokio worker
        // thread (its internal connection runtime panics when nested), so
        // close it on a plain OS thread instead. Async tests normally reach
        // dispose() first (client already taken).
        if let Some(client) = self.client.take() {
            let name = self.name.clone();
            std::thread::spawn(move || {
                let mut client = client;
                let _ = client.batch_execute(&format!("DROP SCHEMA IF EXISTS {name} CASCADE"));
            });
        }
    }
}

use std::sync::Arc;

/// Deterministic event #n for the 100-event scenario.
fn event(n: usize) -> AuditRecord {
    AuditRecord {
        ts: std::time::SystemTime::now(),
        actor: if n % 2 == 0 {
            "local".into()
        } else {
            format!("acct-{n}")
        },
        agent_client: if n % 4 == 0 {
            "stdio".into()
        } else {
            "cursor".into()
        },
        tool: format!("tool_{n}"),
        project: Some(format!("/proj/{n}")),
        args_hash: leankg::audit::hash_args(&serde_json::json!({ "n": n })),
        result_status: if n % 7 == 0 { "error" } else { "ok" }.to_string(),
    }
}

/// Fetch every audit row from the scratch schema, ordered by id.
fn fetch_entries(s: &mut ScratchSchema) -> Vec<AuditEntry> {
    let rows = s
        .conn()
        .query(
            "SELECT id, ts, actor, agent_client, tool, project, args_hash, \
                    result_status, prev_hash, entry_hash \
             FROM audit_log ORDER BY id ASC",
            &[],
        )
        .unwrap();
    rows.iter()
        .map(|r| AuditEntry {
            id: r.get(0),
            ts: r.get::<_, std::time::SystemTime>(1),
            actor: r.get(2),
            agent_client: r.get(3),
            tool: r.get(4),
            project: r.get(5),
            args_hash: r.get(6),
            result_status: r.get(7),
            prev_hash: r.get(8),
            entry_hash: r.get(9),
        })
        .collect()
}

/// Migration 006 creates the append-only ledger; UPDATE/DELETE raise the
/// trigger exception.
#[test]
#[ignore = "requires LEANKG_PG_URL (remote Postgres via .env)"]
fn migration_006_creates_append_only_ledger() {
    let mut s = ScratchSchema::new();
    let report = leankg::db::pg::migrations::run_migrations(s.conn()).unwrap();
    assert!(
        report.applied.contains(&"006_audit_log".to_string()),
        "006_audit_log must be applied; applied: {:?}",
        report.applied
    );

    // One direct insert works; indexes exist.
    s.conn()
        .execute(
            "INSERT INTO audit_log (actor, agent_client, tool, args_hash, result_status, prev_hash, entry_hash)
             VALUES ('a', 'c', 't', 'h', 'ok', $1, 'e')",
            &[&GENESIS_HASH],
        )
        .unwrap();
    let n: i64 = s
        .conn()
        .query_one("SELECT count(*) FROM audit_log", &[])
        .unwrap()
        .get(0);
    assert_eq!(n, 1);

    // Append-only enforcement: UPDATE and DELETE must both fail loudly.
    // tokio-postgres 0.7 renders trigger errors as a bare "db error" via
    // Display; the SQLSTATE + message live on the downcast DbError.
    let assert_violation = |label: &str, err: postgres::Error| {
        let db = err
            .as_db_error()
            .unwrap_or_else(|| panic!("{label}: not a DB error: {err}"));
        assert_eq!(
            db.code().code(),
            "P0001",
            "{label}: expected raise_exception (P0001), got {:?}: {}",
            db.code(),
            db.message()
        );
        assert!(
            db.message().contains("append-only"),
            "{label}: expected append-only violation, got: {}",
            db.message()
        );
    };
    let upd = s
        .conn()
        .execute("UPDATE audit_log SET actor = 'mallory'", &[])
        .err()
        .expect("UPDATE on audit_log must fail");
    assert_violation("UPDATE", upd);
    let del = s
        .conn()
        .execute("DELETE FROM audit_log", &[])
        .err()
        .expect("DELETE on audit_log must fail");
    assert_violation("DELETE", del);
}

/// Recorder writes exactly N rows through the batcher; all fields populated;
/// exported chain verifies against genesis.
#[tokio::test(flavor = "multi_thread")]
#[ignore = "requires LEANKG_PG_URL (remote Postgres via .env)"]
async fn recorder_persists_hundred_events_and_chain_verifies() {
    let mut s = tokio::task::block_in_place(ScratchSchema::new);
    // Sync admin client must run OFF the ambient tokio runtime (its internal
    // runtime panics when nested — same guard the backend uses in checkout).
    tokio::task::block_in_place(|| leankg::db::pg::migrations::run_migrations(s.conn())).unwrap();

    let recorder = leankg::audit::AuditRecorder::shared(Arc::new(s.backend()));
    for n in 0..100 {
        recorder.record(event(n));
    }
    recorder.flush().await;

    let entries = tokio::task::block_in_place(|| fetch_entries(&mut s));
    assert_eq!(
        entries.len(),
        100,
        "recorder must persist exactly 100 events"
    );

    // All mandated fields populated on every row.
    for e in &entries {
        assert!(!e.actor.is_empty());
        assert!(!e.agent_client.is_empty());
        assert!(!e.tool.is_empty());
        assert!(!e.args_hash.is_empty() && e.args_hash.len() == 64);
        assert!(
            e.result_status == "ok" || e.result_status == "error",
            "result_status must be ok|error, got {}",
            e.result_status
        );
        assert_eq!(e.prev_hash.len(), 64);
        assert_eq!(e.entry_hash.len(), 64);
        // ts sane: not before 2020.
        let ms =
            e.ts.duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_secs();
        assert!(ms > 1_577_836_800, "ts must be populated");
    }

    // Genesis linkage + full tamper-evident verification over live data.
    assert_eq!(entries[0].prev_hash, GENESIS_HASH);
    verify_chain(&entries).expect("persisted chain must verify");

    s.dispose().await;
}

/// Export → valid JSONL → verify OK; tampering line 50 names its seq id.
#[tokio::test(flavor = "multi_thread")]
#[ignore = "requires LEANKG_PG_URL (remote Postgres via .env)"]
async fn export_verify_roundtrip_and_tamper_detection() {
    let mut s = tokio::task::block_in_place(ScratchSchema::new);
    // Sync admin client must run OFF the ambient tokio runtime (see above).
    tokio::task::block_in_place(|| leankg::db::pg::migrations::run_migrations(s.conn())).unwrap();

    let recorder = leankg::audit::AuditRecorder::shared(Arc::new(s.backend()));
    for n in 0..100 {
        recorder.record(event(n));
    }
    recorder.flush().await;

    let entries = tokio::task::block_in_place(|| fetch_entries(&mut s));
    assert_eq!(entries.len(), 100);

    // Export produces 100 well-formed JSONL lines with required fields.
    let jsonl = rows_to_jsonl(&entries);
    assert_eq!(jsonl.lines().count(), 100);
    let parsed = jsonl_to_rows(&jsonl).expect("every line must parse");
    verify_chain(&parsed).expect("exported ledger verifies");

    // Tamper with line 50 (seq id 50): flip the recorded status.
    let mut tampered = parsed;
    let target_id = tampered[49].id;
    tampered[49].result_status = if tampered[49].result_status == "ok" {
        "error"
    } else {
        "ok"
    }
    .to_string();
    let err = verify_chain(&tampered).expect_err("tampered export must fail verification");
    assert_eq!(
        err.seq, target_id,
        "verify must name the tampered sequence id"
    );

    // Chain continuity across batches: entry 51 continues entry 50's hash.
    assert_eq!(entries[50].prev_hash, entries[49].entry_hash);

    s.dispose().await;
}

/// The batcher keeps the chain continuous across multiple flushes (batch max
/// is 50; 120 events force three batches).
#[tokio::test(flavor = "multi_thread")]
#[ignore = "requires LEANKG_PG_URL (remote Postgres via .env)"]
async fn chain_survives_multiple_batches() {
    let mut s = tokio::task::block_in_place(ScratchSchema::new);
    // Sync admin client must run OFF the ambient tokio runtime (see above).
    tokio::task::block_in_place(|| leankg::db::pg::migrations::run_migrations(s.conn())).unwrap();

    let recorder = leankg::audit::AuditRecorder::shared(Arc::new(s.backend()));
    for n in 0..120 {
        recorder.record(event(n));
        if n == 59 {
            recorder.flush().await; // force a mid-stream batch boundary
        }
    }
    recorder.flush().await;

    let entries = tokio::task::block_in_place(|| fetch_entries(&mut s));
    assert_eq!(entries.len(), 120);
    verify_chain(&entries).expect("multi-batch chain must stay intact");

    s.dispose().await;
}

/// `leankg audit export|verify` must pin to the project schema even when its
/// code index is still EMPTY (init_db_readonly would fall back to public and
/// miss the ledger — regression for the live-test finding).
#[tokio::test(flavor = "multi_thread")]
#[ignore = "requires LEANKG_PG_URL (remote Postgres via .env)"]
async fn readonly_audit_opener_pins_ledger_schema_of_unindexed_project() {
    use std::sync::Arc as StdArc;

    // A scratch PROJECT directory: candidates derive from the path, so the
    // per-project schema name is deterministic before anything is created.
    let tmp = tempfile::TempDir::new().unwrap();
    let project = tmp.path().join("proj");
    std::fs::create_dir_all(project.join(".leankg")).unwrap();

    let schema_name =
        leankg::db::backend::schema_candidates_for_path(&project.join(".leankg"))[0].clone();

    // Create ONLY the ledger (no code_elements rows): migrations + one row.
    let mut s = tokio::task::block_in_place(|| ScratchSchema::with_forced_name(&schema_name));
    tokio::task::block_in_place(|| leankg::db::pg::migrations::run_migrations(s.conn())).unwrap();
    let recorder = leankg::audit::AuditRecorder::shared(StdArc::new(s.backend()));
    recorder.record(event(1));
    recorder.flush().await;

    // The opener under test: pins despite zero indexed elements.
    let db = leankg::db::backend::init_db_readonly_audit(&project.join(".leankg"))
        .expect("opener must find the ledger schema");
    let rows = db.query_audit(None, None).unwrap();
    assert_eq!(rows.len(), 1, "pinned backend must read the project ledger");
    verify_chain(&rows).expect("ledger read through the pinned opener verifies");

    s.dispose().await;
}
