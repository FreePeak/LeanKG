//! Versioned PostgreSQL schema runner (Phase 2, plan T2.2).
//!
//! The full DDL lives in `schema.sql` (mirroring the cozo relations from
//! docs/analysis/cozo-query-inventory.md §1, query_cache dropped per D2).
//! A `migrations` table records `(id, applied_at)` — the same shape as the
//! cozo relation, but `applied_at` is a real Postgres timestamp per T2.2.
//!
//! No sqlx yet (plan D1 defers the client choice to the translator phase);
//! this uses the `postgres` crate directly, the same one the Phase 0 spike
//! (tests/pg_phase0_spike.rs) already used.
//!
//! Note: `CREATE EXTENSION IF NOT EXISTS vector` and the HNSW index live
//! inside migration v1's SQL (embedded in `schema.sql`), so no separate
//! extension step is needed.

use postgres::Client;

/// Vector dimension for embedding_vectors.vec (plan D5 — 384 = BGE-small-en-v1.5).
/// Keep in sync with `VEC_DIM` in tests/pg_schema_test.rs.
pub const VEC_DIM: usize = 384;

const MIGRATIONS_TABLE: &str = r#"
CREATE TABLE IF NOT EXISTS migrations (
    id         TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
"#;

/// Migration steps, newest last. A step applies atomically (each runs in its
/// own transaction with the migrations-table insert). To add a migration,
/// append a `(version, sql)` entry here and bump the const.
pub const MIGRATIONS: &[(&str, &str)] = &[("001_schema", include_str!("schema.sql"))];

pub struct MigrationReport {
    pub applied: Vec<String>,
    pub skipped: Vec<String>,
}

/// Connect string for the Postgres backend. Precedence: `LEANKG_PG_URL`
/// env > `db:` block in `leankg.yaml` > dev default (local).
pub fn pg_url() -> String {
    std::env::var("LEANKG_PG_URL")
        .ok()
        .filter(|v| !v.trim().is_empty())
        .or_else(|| {
            crate::config::db_config_from_cwd()
                .and_then(|db| db.url.filter(|u| !u.trim().is_empty()))
        })
        .unwrap_or_else(|| "postgresql://postgres:postgres@localhost:5433/leankg".to_string())
}

/// Create the `migrations` table if absent, then apply every MIGRATIONS step
/// not yet recorded, in order, each inside a transaction. Idempotent: a second
/// run applies nothing.
pub fn run_migrations(client: &mut Client) -> Result<MigrationReport, postgres::Error> {
    client.batch_execute(MIGRATIONS_TABLE)?;

    let mut applied = Vec::new();
    let mut skipped = Vec::new();

    for (version, sql) in MIGRATIONS {
        let already: bool = client
            .query_one(
                "SELECT EXISTS (SELECT 1 FROM migrations WHERE id = $1)",
                &[&version],
            )?
            .get(0);
        if already {
            skipped.push((*version).to_string());
            continue;
        }

        let mut tx = client.transaction()?;
        tx.batch_execute(sql)?;
        tx.execute("INSERT INTO migrations (id) VALUES ($1)", &[&version])?;
        tx.commit()?;
        applied.push((*version).to_string());
    }

    Ok(MigrationReport { applied, skipped })
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Serialize tests that mutate LEANKG_PG_URL (process-global env).
    static ENV_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

    #[test]
    fn pg_url_prefers_env_then_yaml_then_default() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        std::env::remove_var("LEANKG_PG_URL");
        // No env, no leankg.yaml at cwd -> dev default.
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(
            dir.path().join("leankg.yaml"),
            "db:\n  url: postgresql://u:p@yaml-host:7777/mydb\n",
        )
        .unwrap();
        let prev = std::env::current_dir().unwrap();
        std::env::set_current_dir(dir.path()).unwrap();
        assert_eq!(pg_url(), "postgresql://u:p@yaml-host:7777/mydb");
        // Env wins over yaml.
        std::env::set_var("LEANKG_PG_URL", "postgresql://u:p@env-host:1111/envdb");
        assert_eq!(pg_url(), "postgresql://u:p@env-host:1111/envdb");
        std::env::remove_var("LEANKG_PG_URL");
        std::env::set_current_dir(prev).unwrap();
    }

    /// Migration IDs are unique and in strictly ascending order (no
    /// framework here — this is the ordering guarantee).
    #[test]
    fn migrations_are_unique_and_ordered() {
        let mut prev: Option<&str> = None;
        for (version, sql) in MIGRATIONS {
            assert!(!version.is_empty(), "migration id must not be empty");
            assert!(!sql.trim().is_empty(), "migration {version} has empty SQL");
            if let Some(p) = prev {
                assert!(*version > p, "migration {version} out of order after {p}");
            }
            prev = Some(version);
        }
    }

    /// VEC_DIM sanity: pgvector vector() types are written by hand in
    /// schema.sql and must match the const (a mismatch would fail at
    /// insert time in the container test, not here).
    #[test]
    fn schema_sql_matches_vec_dim_const() {
        let schema = include_str!("schema.sql");
        assert!(
            schema.contains(&format!("vector({VEC_DIM})")),
            "schema.sql must declare vector({VEC_DIM})"
        );
    }

    /// The dropped query_cache table must not come back (D2). The name may
    /// appear in comments; no CREATE TABLE statement may create it.
    #[test]
    fn schema_sql_has_no_query_cache_table() {
        let schema = include_str!("schema.sql");
        for line in schema.lines() {
            let t = line.trim();
            if t.starts_with("CREATE TABLE") && t.contains("query_cache") {
                panic!("query_cache dropped per D2 — schema.sql creates it: {t}");
            }
        }
    }
}
