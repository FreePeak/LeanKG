//! H10 / FR-PLG-8 — usage-dashboard integration tests, live Postgres.
//!
//! Connection pattern follows tests/pg_audit_log_tests.rs: a scratch schema
//! `leankg_test_<pid>_<n>` per test, migrations run inside it, dropped on
//! scope exit. The shared `leankg` database is never touched.
//!
//! Run:
//!   set -a; source ../.env; set +a
//!   cargo test --release --test dashboard_tests -- --ignored

use leankg::dashboard::{self, MetricRow};
use std::env;
use std::sync::Arc;

fn pg_url() -> String {
    env::var("LEANKG_PG_URL")
        .unwrap_or_else(|_| "postgresql://postgres:postgres@localhost:5433/leankg".to_string())
}

/// Scratch schema guard (same shape as tests/pg_schema_test.rs).
struct ScratchSchema {
    client: Option<postgres::Client>,
    name: String,
}

impl ScratchSchema {
    fn new() -> ScratchSchema {
        static COUNTER: std::sync::atomic::AtomicU32 = std::sync::atomic::AtomicU32::new(0);
        Self::with_forced_name(&format!(
            "leankg_test_{}_{}",
            std::process::id(),
            COUNTER.fetch_add(1, std::sync::atomic::Ordering::Relaxed)
        ))
    }

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

    fn dispose(mut self) {
        if let Some(mut client) = self.client.take() {
            let name = self.name.clone();
            std::thread::spawn(move || {
                let _ = client.batch_execute(&format!("DROP SCHEMA IF EXISTS {name} CASCADE"));
            });
        }
    }
}

impl Drop for ScratchSchema {
    fn drop(&mut self) {
        if let Some(client) = self.client.take() {
            let name = self.name.clone();
            std::thread::spawn(move || {
                let mut client = client;
                let _ = client.batch_execute(&format!("DROP SCHEMA IF EXISTS {name} CASCADE"));
            });
        }
    }
}

// ---------------------------------------------------------------------------
// Fixture: 50 synthetic rows spanning 3 UTC days × 2 projects × 5 tools.
// Clusters anchor to NOW (−1h / −25h / −49h, each spreading +16 min) so
// results are wall-clock independent: three distinct day buckets, and a
// `--since 24h` cutoff of now−86400 keeps exactly the first cluster (17).
// ---------------------------------------------------------------------------

const TOOLS: [&str; 5] = [
    "search_code",
    "find_function",
    "query_file",
    "get_context",
    "impact",
];
const PROJECTS: [&str; 2] = ["/work/proj-alpha", "/work/proj-beta"];
const PATTERNS: [Option<&str>; 3] = [
    Some("search_code(query=auth)"),
    Some("find_function(name=main)"),
    None,
];

/// Build the fixture rows. Day d ∈ {0,1,2} → now −(1h + d·24h); 17 rows
/// per cluster (truncated to 50 overall).
fn fixture_rows(now: i64) -> Vec<MetricRow> {
    let day_stamps = [now - 3_600, now - 25 * 3_600, now - 49 * 3_600];
    let mut rows = Vec::with_capacity(50);
    for (d, ts) in day_stamps.iter().enumerate() {
        for i in 0..17 {
            let g = d * 17 + i; // global index 0..50
            rows.push(MetricRow {
                tool_name: TOOLS[g % 5].to_string(),
                timestamp: *ts + (i as i64) * 60, // spread within the day
                project_path: PROJECTS[g % 2].to_string(),
                input_tokens: 1_000 + g as i64 * 10,
                output_tokens: 400 + g as i64 * 5,
                execution_time_ms: 50 + (g as i64 % 7) * 25,
                tokens_saved: 100 * ((g % 9) as i64 + 1),
                savings_percent: match g % 4 {
                    0 => 90.0,
                    1 => 45.5,
                    2 => 12.25,
                    _ => 66.75,
                },
                success: g % 7 != 0,
                query_pattern: PATTERNS[g % 3].map(str::to_string),
            });
        }
    }
    rows.truncate(50);
    rows
}

/// Insert fixture rows with one multi-row INSERT (trusted literals).
fn insert_rows(conn: &mut postgres::Client, rows: &[MetricRow]) {
    let mut sql = String::from(
        "INSERT INTO context_metrics (tool_name, timestamp, project_path, input_tokens, \
         output_tokens, output_elements, execution_time_ms, baseline_tokens, \
         baseline_lines_scanned, tokens_saved, savings_percent, correct_elements, \
         total_expected, f1_score, query_pattern, query_file, query_depth, success, is_deleted) VALUES ",
    );
    for (i, r) in rows.iter().enumerate() {
        if i > 0 {
            sql.push(',');
        }
        let pattern = match &r.query_pattern {
            Some(p) => format!("'{p}'"),
            None => "NULL".to_string(),
        };
        sql.push_str(&format!(
            "('{}', {}, '{}', {}, {}, 0, {}, 5000, 800, {}, {}, NULL, NULL, NULL, {pattern}, NULL, NULL, {}, false)",
            r.tool_name.replace('\'', "''"),
            r.timestamp,
            r.project_path.replace('\'', "''"),
            r.input_tokens,
            r.output_tokens,
            r.execution_time_ms,
            r.tokens_saved,
            r.savings_percent,
            r.success,
        ));
    }
    conn.batch_execute(&sql).unwrap();
}

#[test]
#[ignore = "requires LEANKG_PG_URL (remote Postgres via .env)"]
fn collect_matches_reference_aggregation_over_fixture() {
    let mut s = ScratchSchema::new();
    leankg::db::pg::migrations::run_migrations(s.conn()).unwrap();

    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64;
    let rows = fixture_rows(now);
    assert_eq!(rows.len(), 50);
    insert_rows(s.conn(), &rows);

    let backend: leankg::db::backend::SharedDb = Arc::new(s.backend());
    let got = dashboard::collect(&backend, None).expect("collect");
    let want = dashboard::aggregate_rows(&rows);

    assert_eq!(got.totals.calls, 50);
    assert_eq!(got.totals.tokens_saved, want.totals.tokens_saved);
    assert_eq!(got.by_tool, want.by_tool, "by_tool must match reference");
    assert_eq!(got.by_day.len(), 3, "three distinct UTC days");
    assert_eq!(got.by_project, want.by_project);
    s.dispose();
}

#[test]
#[ignore = "requires LEANKG_PG_URL (remote Postgres via .env)"]
fn collect_since_24h_filters_old_days() {
    let mut s = ScratchSchema::new();
    leankg::db::pg::migrations::run_migrations(s.conn()).unwrap();

    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64;
    let rows = fixture_rows(now);
    insert_rows(s.conn(), &rows);

    let backend: leankg::db::backend::SharedDb = Arc::new(s.backend());
    let got =
        dashboard::collect(&backend, Some(now - dashboard::SECS_PER_DAY)).expect("collect since");
    let want = dashboard::aggregate_rows(
        &rows
            .iter()
            .filter(|r| r.timestamp >= now - dashboard::SECS_PER_DAY)
            .cloned()
            .collect::<Vec<_>>(),
    );
    assert_eq!(got.totals.calls, 17, "only the now−1h cluster survives 24h");
    assert!(got.totals.calls < 50);
    assert_eq!(got.totals.tokens_saved, want.totals.tokens_saved);
    assert_eq!(got.by_tool, want.by_tool);
    assert_eq!(got.by_day.len(), 1, "single remaining day");
    s.dispose();
}

#[test]
#[ignore = "requires LEANKG_PG_URL (remote Postgres via .env)"]
fn collect_empty_ledger_is_zeroed() {
    let mut s = ScratchSchema::new();
    leankg::db::pg::migrations::run_migrations(s.conn()).unwrap();
    let backend: leankg::db::backend::SharedDb = Arc::new(s.backend());
    let got = dashboard::collect(&backend, None).expect("collect");
    assert_eq!(got.totals.calls, 0);
    assert!(got.by_tool.is_empty());
    let text = dashboard::render_text(&got);
    assert!(text.to_lowercase().contains("no metrics yet"));
}
