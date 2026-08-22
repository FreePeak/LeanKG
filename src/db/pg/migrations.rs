//! Versioned PostgreSQL schema runner (Phase 2, plan T2.2).
//!
//! The full DDL lives in `schema.sql` (mirroring the legacy relations from
//! the legacy query-shape inventory analysis §1, query_cache dropped per D2).
//! A `migrations` table records `(id, applied_at)` — the same shape as the
//! legacy relation, but `applied_at` is a real Postgres timestamp per T2.2.
//!
//! No sqlx yet (plan D1 defers the client choice to the translator phase);
//! this uses the `postgres` crate directly, the same one the Phase 0 spike
//! (tests/pg_phase0_spike.rs) already used.
//!
//! Note: `CREATE EXTENSION IF NOT EXISTS vector` and the HNSW index live
//! inside migration v1's SQL (embedded in `schema.sql`), so no separate
//! extension step is needed.

use postgres::Client;

/// Vector dimension written in schema.sql (plan D5 — 384 = BGE-small-en-v1.5).
/// Keep in sync with `VEC_DIM` in tests/pg_schema_test.rs. This is the
/// at-rest **default**; the effective width is
/// [`crate::embeddings::provider::vec_dim`] — `run_migrations` substitutes the
/// type at apply time and `reconcile_vector_dim` migrates existing databases.
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
pub const MIGRATIONS: &[(&str, &str)] = &[
    ("001_schema", include_str!("schema.sql")),
    (
        "002_multi_model_embed",
        include_str!("migrations/002_multi_model_embed.sql"),
    ),
    (
        "003_gemini_embed",
        include_str!("migrations/003_gemini_embed.sql"),
    ),
    ("004_auth", include_str!("migrations/004_auth.sql")),
    (
        "005_hnsw_dims_cleanup",
        include_str!("migrations/005_hnsw_dims_cleanup.sql"),
    ),
    (
        "006_audit_log",
        include_str!("migrations/006_audit_log.sql"),
    ),
];

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
///
/// FR-EMBED-DIM: when the effective vector width
/// ([`crate::embeddings::provider::vec_dim`]) differs from the at-rest
/// `vector(384)` in schema.sql, the DDL text is substituted at apply time.
/// Already-migrated databases are handled by [`reconcile_vector_dim`] instead.
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
        tx.batch_execute(&schema_sql_for_dim(
            sql,
            crate::embeddings::provider::vec_dim(),
        ))?;
        tx.execute("INSERT INTO migrations (id) VALUES ($1)", &[&version])?;
        tx.commit()?;
        applied.push((*version).to_string());
    }

    Ok(MigrationReport { applied, skipped })
}

/// Substitute the pgvector column width when the effective dim differs from
/// the at-rest default. Pure string op (kept separate for unit tests); only
/// the exact `vector(384)` type token is rewritten — comments mentioning
/// `384` alone are untouched.
fn schema_sql_for_dim(sql: &str, desired: usize) -> String {
    if desired == VEC_DIM {
        return sql.to_string();
    }
    sql.replace(&format!("vector({VEC_DIM})"), &format!("vector({desired})"))
}

/// Probe the live pgvector width of `embedding_vectors.vec` and, when it
/// differs from [`crate::embeddings::provider::vec_dim`], wipe + rebuild the
/// vector store: drop the HNSW index, truncate vectors, clear the freshness
/// ledger, `ALTER COLUMN vec TYPE vector(N)`, then rebuild the index.
///
/// Returns `Some((stored, desired))` when a wipe happened, `None` when the
/// store already matches or the table does not exist yet (pre-migration).
/// Called on every writer init (`create_schema_if_missing_sync`) so a model
/// switch takes effect on the next `index` / `embed` run without manual DDL.
pub fn reconcile_vector_dim(
    client: &mut Client,
) -> Result<Option<(usize, usize)>, postgres::Error> {
    let desired = crate::embeddings::provider::vec_dim();
    let stored_repr: Option<String> = client
        .query_opt(
            "SELECT format_type(atttypid, atttypmod) FROM pg_attribute \
             WHERE attrelid = 'embedding_vectors'::regclass AND attname = 'vec'",
            &[],
        )?
        .map(|row| row.get(0));
    let Some(stored) = stored_repr.as_deref().and_then(parse_pg_vector_dim) else {
        return Ok(None); // pre-migration / shared-layout with no vectors table
    };
    if stored == desired {
        return Ok(None);
    }
    let (m, ef) = reconcile_index_params();
    for stmt in reconcile_statements(desired, m, ef) {
        client.batch_execute(&stmt)?;
    }
    eprintln!(
        "leankg: embedding vector dim changed {stored} -> {desired}; \
         wiped embedding_vectors + embedding_state — run `leankg embed` to rebuild vectors"
    );
    Ok(Some((stored, desired)))
}

/// Parse pgvector's `format_type` rendering (`vector(4096)`) into its width.
/// Pure helper for unit tests.
fn parse_pg_vector_dim(type_repr: &str) -> Option<usize> {
    type_repr
        .strip_prefix("vector(")?
        .strip_suffix(')')?
        .trim()
        .parse()
        .ok()
}

/// HNSW `(m, ef_construction)` for the post-reconcile index rebuild. Mirrors
/// the schema.sql baseline (16/200) with the same env overrides the runtime
/// rebuild path honors (`LEANKG_HNSW_M` / `LEANKG_HNSW_EF_CONST`).
fn reconcile_index_params() -> (usize, usize) {
    let m = std::env::var("LEANKG_HNSW_M")
        .ok()
        .and_then(|v| v.parse::<usize>().ok())
        .filter(|v| (4..=256).contains(v))
        .unwrap_or(16);
    let ef = std::env::var("LEANKG_HNSW_EF_CONST")
        .ok()
        .and_then(|v| v.parse::<usize>().ok())
        .filter(|v| (1..=2000).contains(v))
        .unwrap_or(200)
        .max(2 * m);
    (m, ef)
}

/// The reconcile statement list (pure so unit tests can assert ordering and
/// content without a live Postgres).
fn reconcile_statements(desired: usize, m: usize, ef: usize) -> Vec<String> {
    vec![
        "DROP INDEX IF EXISTS embedding_vectors_vec_hnsw_idx".to_string(),
        "TRUNCATE embedding_vectors".to_string(),
        "DELETE FROM embedding_state".to_string(),
        format!("ALTER TABLE embedding_vectors ALTER COLUMN vec TYPE vector({desired})"),
        format!(
            "CREATE INDEX IF NOT EXISTS embedding_vectors_vec_hnsw_idx \
             ON embedding_vectors USING hnsw (vec vector_cosine_ops) \
             WITH (m = {m}, ef_construction = {ef})"
        ),
    ]
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

    /// FR-EMBED-DIM: at apply time the effective dim substitutes the
    /// at-rest default; the default is a no-op (bit-identical DDL).
    #[test]
    fn schema_sql_for_dim_substitutes_only_non_default() {
        let schema = include_str!("schema.sql");
        assert_eq!(schema_sql_for_dim(schema, VEC_DIM), schema);
        let wide = schema_sql_for_dim(schema, 4096);
        assert!(wide.contains("vector(4096)"));
        assert!(!wide.contains("vector(384)"));
        // Only the type token is rewritten.
        assert_eq!(
            wide.matches("vector(4096)").count(),
            schema.matches(&format!("vector({VEC_DIM})")).count()
        );
    }

    #[test]
    fn parse_pg_vector_dim_reads_format_type() {
        assert_eq!(parse_pg_vector_dim("vector(384)"), Some(384));
        assert_eq!(parse_pg_vector_dim("vector(4096)"), Some(4096));
        assert_eq!(parse_pg_vector_dim("text"), None);
        assert_eq!(parse_pg_vector_dim("vector()"), None);
    }

    /// The reconcile sequence must wipe BEFORE altering the column type
    /// (pgvector refuses ALTER TYPE with incompatible rows) and rebuild the
    /// HNSW index LAST.
    #[test]
    fn reconcile_statements_wipe_then_alter_then_reindex() {
        let stmts = reconcile_statements(4096, 16, 200);
        let joined = stmts.join("\n");
        let drop_idx = joined.find("DROP INDEX").unwrap();
        let truncate = joined.find("TRUNCATE embedding_vectors").unwrap();
        let clear_state = joined.find("DELETE FROM embedding_state").unwrap();
        let alter = joined
            .find("ALTER TABLE embedding_vectors ALTER COLUMN vec TYPE vector(4096)")
            .unwrap();
        let recreate = joined
            .find("CREATE INDEX IF NOT EXISTS embedding_vectors_vec_hnsw_idx")
            .unwrap();
        assert!(
            drop_idx < truncate
                && truncate < clear_state
                && clear_state < alter
                && alter < recreate
        );
        assert!(stmts
            .last()
            .unwrap()
            .contains("WITH (m = 16, ef_construction = 200)"));
    }

    #[test]
    fn reconcile_index_params_default_and_ef_floor() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        std::env::remove_var("LEANKG_HNSW_M");
        std::env::remove_var("LEANKG_HNSW_EF_CONST");
        assert_eq!(reconcile_index_params(), (16, 200));
        // pgvector requires ef_construction >= 2*m.
        std::env::set_var("LEANKG_HNSW_M", "150");
        std::env::set_var("LEANKG_HNSW_EF_CONST", "20");
        let (m, ef) = reconcile_index_params();
        assert_eq!(m, 150);
        assert_eq!(ef, 300);
        std::env::remove_var("LEANKG_HNSW_M");
        std::env::remove_var("LEANKG_HNSW_EF_CONST");
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

    /// 005 drops the never-buildable qwen 2560-d HNSW index and the cosmetic
    /// usearch_key DEFAULT. The strings below must survive in the SQL.
    #[test]
    fn migration_005_drops_qwen_hnsw_index_and_default() {
        let sql = include_str!("migrations/005_hnsw_dims_cleanup.sql");
        assert!(
            sql.contains("DROP INDEX IF EXISTS embedding_vectors_qwen3_emb_4b_2560_vec_hnsw_idx"),
            "005 must drop the qwen HNSW index"
        );
        assert!(
            sql.contains("ALTER COLUMN usearch_key DROP DEFAULT"),
            "005 must drop the qwen usearch_key DEFAULT"
        );
    }

    /// 002 must not create the qwen HNSW index (pgvector 2000-d cap makes it
    /// dead weight on fresh DBs).
    #[test]
    fn migration_002_has_no_qwen_hnsw_index() {
        let sql = include_str!("migrations/002_multi_model_embed.sql");
        assert!(
            !sql.contains("embedding_vectors_qwen3_emb_4b_2560_vec_hnsw_idx"),
            "002 must not create the qwen HNSW index"
        );
    }
}
