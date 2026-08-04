//! Storage-backend abstraction (Phase 1 of the CozoDB → PostgreSQL migration).
//!
//! Everything that touches a database goes through [`DbBackend`]; no code
//! outside this module holds a concrete `cozo::DbInstance`. The production
//! backend is PostgreSQL (Phase 3+); [`CozoBackend`] is a temporary
//! migration shim that delegates to CozoDB unchanged (deleted in Phase 8,
//! plan D4). [`PostgresBackend`] is a stub that only validates
//! `LEANKG_PG_URL` — the real connection + schema land in Phases 2-3
//! (`src/db/pg/`).

use crate::db::schema::mutability_for;
use cozo::{NamedRows, ScriptMutability};
use std::collections::BTreeMap;
use std::path::Path;
use std::sync::Arc;

/// Shared handle used throughout the codebase. `Arc` because clones of
/// `GraphEngine` must share ONE underlying DB handle (RocksDB allows one
/// handle per process per path).
pub type SharedDb = Arc<dyn DbBackend>;

/// The database call surface (plan §2.3, inventory §4).
///
/// Backends must be `Send + Sync`: `GraphEngine` clones and the embed
/// writer thread move `SharedDb` across threads.
pub trait DbBackend: Send + Sync {
    /// Execute a script (Datalog today, SQL after the translator lands) and
    /// return named rows. Mirrors the historical `schema::run_script` 2-arg
    /// convention (`serde_json::Value` params, auto-detected mutability).
    fn run_script(
        &self,
        query: &str,
        params: BTreeMap<String, serde_json::Value>,
    ) -> Result<NamedRows, Box<dyn std::error::Error>>;

    /// Classify a script as read or write. Auto-detected from the leading
    /// operator surface — see [`crate::db::schema::mutability_for`].
    fn mutability_for(&self, query: &str) -> ScriptMutability {
        mutability_for(query)
    }

    /// Bulk-load named rows into a relation. Cozo's `import_relations`
    /// (skips script parsing — ~8x faster than `:put`, build.rs:1292).
    /// The Postgres backend replaces this with batched `COPY`/upsert in
    /// Phase 3; the default is an unsupported error so callers fail loudly
    /// on a backend that has not implemented the bulk path.
    fn import_relations(
        &self,
        _data: BTreeMap<String, NamedRows>,
    ) -> Result<(), Box<dyn std::error::Error>> {
        Err("import_relations: not implemented for this backend (Phase 3: COPY/upsert)".into())
    }
}

/// **Migration shim — deleted in Phase 8 (plan D4).**
///
/// Wraps the existing CozoDB `DbInstance` and the `schema::run_script`
/// adapter so the rest of the codebase can switch to [`DbBackend`] without
/// touching query logic. No behavior change vs the pre-trait code paths.
#[derive(Clone)]
pub struct CozoBackend {
    pub(crate) db: crate::db::schema::CozoDb,
}

impl CozoBackend {
    /// Wrap an already-open concrete CozoDB handle. Used by modules that
    /// manage their own database file (e.g. `ApiKeyStore`'s separate
    /// `keys.db`, which must NOT run the full graph `init_schema`).
    pub fn from_concrete(db: crate::db::schema::CozoDb) -> Self {
        Self { db }
    }

    /// Open a raw CozoDB file WITHOUT running the graph schema — for
    /// standalone databases like `keys.db` that own their own tables.
    pub fn open_raw(path: &Path, engine: &str) -> Result<Self, Box<dyn std::error::Error>> {
        let path_str = path.to_string_lossy().to_string();
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)?;
        }
        Ok(Self {
            db: crate::db::schema::CozoDb::new(engine, &path_str, "")?,
        })
    }
}

impl DbBackend for CozoBackend {
    fn run_script(
        &self,
        query: &str,
        params: BTreeMap<String, serde_json::Value>,
    ) -> Result<NamedRows, Box<dyn std::error::Error>> {
        crate::db::schema::run_script_cozo(&self.db, query, params).map_err(|e| {
            Box::new(std::io::Error::other(e.to_string())) as Box<dyn std::error::Error>
        })
    }

    fn mutability_for(&self, query: &str) -> ScriptMutability {
        mutability_for(query)
    }

    fn import_relations(
        &self,
        data: BTreeMap<String, NamedRows>,
    ) -> Result<(), Box<dyn std::error::Error>> {
        self.db.import_relations(data).map_err(|e| {
            Box::new(std::io::Error::other(e.to_string())) as Box<dyn std::error::Error>
        })
    }
}

/// PostgreSQL backend stub — Phase 3 (plan T1.4, T2.x).
///
/// Today it only validates `LEANKG_PG_URL` (a `postgres://` URL must be
/// set) and stores the URL. It runs NO queries and creates NO tables
/// (Phase 2 owns schema). `run_script` always fails with the documented
/// "not yet implemented" error.
#[derive(Debug, Clone)]
pub struct PostgresBackend {
    /// Validated connection URL (format-checked only; no connection yet).
    pub pg_url: String,
}

impl PostgresBackend {
    /// Format-check `LEANKG_PG_URL`. Returns Err with a clear message when
    /// the env var is missing or does not look like a Postgres URL.
    pub fn from_env() -> Result<Self, String> {
        let url = std::env::var("LEANKG_PG_URL")
            .map_err(|_| "LEANKG_PG_URL is not set; the Postgres backend requires it (run `docker compose up postgres`)")?;
        if !url.starts_with("postgres://") && !url.starts_with("postgresql://") {
            return Err(format!(
                "LEANKG_PG_URL must be a postgres:// URL, got: {}",
                redact_url(&url)
            ));
        }
        Ok(Self { pg_url: url })
    }
}

impl DbBackend for PostgresBackend {
    fn run_script(
        &self,
        _query: &str,
        _params: BTreeMap<String, serde_json::Value>,
    ) -> Result<NamedRows, Box<dyn std::error::Error>> {
        Err("PostgresBackend: not yet implemented (Phase 3)".into())
    }
}

/// Never echo credentials back in errors/logs.
fn redact_url(url: &str) -> String {
    let mut out = String::with_capacity(url.len());
    let mut in_userinfo = false;
    let mut seen_scheme = false;
    let mut seen_at = false;
    for ch in url.chars() {
        match ch {
            '@' => {
                seen_at = true;
                in_userinfo = false;
                out.push('@');
            }
            // Only `user:pass@` is userinfo: the first `:` after the
            // `://` scheme separator and before the `@`. The port colon
            // (`host:5432`) is after `@` and stays visible. Fixed 4-star
            // mask (hides password length).
            ':' if !in_userinfo && seen_scheme && !seen_at => {
                in_userinfo = true;
                out.push(':');
                out.push_str("****");
            }
            '/' | '?' | '#' => {
                in_userinfo = false;
                out.push(ch);
            }
            _ if !in_userinfo => out.push(ch),
            _ => {}
        }
        if ch == '/' {
            seen_scheme = true;
        }
    }
    out
}

/// Engine selection: `LEANKG_DB_ENGINE` = `postgres` (default) | `cozo`
/// (migration shim). Both values are removed after Phase 8 (plan D4).
///
/// Selection rules (chosen to keep the embedded path working everywhere):
/// * `cozo` → `init_db`/`init_db_readonly` → `CozoBackend` shim.
/// * unset or `postgres` → **path-based init always returns the cozo shim**
///   so tests, `leankg init`, and CLI flows keep working without a running
///   Postgres. A Postgres connection is only attempted when the caller
///   explicitly asks for one (`init_db_pg`) — which is exactly where a
///   missing `LEANKG_PG_URL` should fail loudly.
pub fn resolve_engine() -> &'static str {
    match std::env::var("LEANKG_DB_ENGINE")
        .unwrap_or_default()
        .to_ascii_lowercase()
        .as_str()
    {
        "cozo" => "cozo",
        _ => "postgres",
    }
}

/// Open the default backend for a file/dir database path. See
/// [`resolve_engine`] for selection semantics.
pub fn init_db(db_path: &Path) -> Result<SharedDb, Box<dyn std::error::Error>> {
    init_cozo(db_path, false)
}

/// Open a read-only backend for a file/dir database path (sqlite `mode=ro`;
/// rocksdb uses the same handle as `init_db` — the documented CozoDB 0.7.x
/// workaround, see `schema::init_db_readonly`).
pub fn init_db_readonly(db_path: &Path) -> Result<SharedDb, Box<dyn std::error::Error>> {
    init_cozo(db_path, true)
}

/// Open a PostgreSQL backend. Fails when `LEANKG_PG_URL` is missing or
/// malformed. This is the only entry point that produces a
/// [`PostgresBackend`]; the full connection + schema arrive in Phase 2-3.
pub fn init_db_pg() -> Result<SharedDb, Box<dyn std::error::Error>> {
    let pg = PostgresBackend::from_env()?;
    tracing::info!(
        "DB engine = postgres (stub, Phase 3): {}",
        redact_url(&pg.pg_url)
    );
    Ok(Arc::new(pg))
}

/// The migration shim: open CozoDB and return it boxed behind `DbBackend`.
/// Used for both read-write and read-only paths, and always for
/// path-based init (see [`resolve_engine`]).
pub fn init_cozo(db_path: &Path, read_only: bool) -> Result<SharedDb, Box<dyn std::error::Error>> {
    let cozo = if read_only {
        crate::db::schema::init_db_readonly_cozo(db_path)?
    } else {
        crate::db::schema::init_db_cozo(db_path)?
    };
    Ok(Arc::new(CozoBackend { db: cozo }))
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    #[test]
    fn cozo_backend_run_script_roundtrip_via_trait_object() {
        // (a) dyn dispatch + (b) shim still returns expected rows.
        let tmp = TempDir::new().unwrap();
        let db: SharedDb = init_db(&tmp.path().join("t.db")).unwrap();
        db.run_script(":create kv {k: String => v: String}", Default::default())
            .unwrap();
        let mut params = BTreeMap::new();
        params.insert("k".into(), serde_json::json!("a"));
        params.insert("v".into(), serde_json::json!("b"));
        db.run_script("?[k, v] <- [[$k, $v]] :put kv {k => v}", params)
            .unwrap();
        let mut qp = BTreeMap::new();
        qp.insert("k".into(), serde_json::json!("a"));
        let res = db.run_script("?[v] := *kv[k, v], k = $k", qp).unwrap();
        assert_eq!(res.rows.len(), 1);
        assert_eq!(res.rows[0][0].get_str(), Some("b"));
    }

    #[test]
    fn cozo_backend_import_relations_works() {
        let tmp = TempDir::new().unwrap();
        let db: SharedDb = init_db(&tmp.path().join("t.db")).unwrap();
        db.run_script(":create t {k: String => v: String}", Default::default())
            .unwrap();
        let rows = cozo::NamedRows::new(
            vec!["k".into(), "v".into()],
            vec![vec![
                cozo::DataValue::Str("x".into()),
                cozo::DataValue::Str("y".into()),
            ]],
        );
        let mut map = BTreeMap::new();
        map.insert("t".to_string(), rows);
        db.import_relations(map).unwrap();
        let res = db
            .run_script("?[v] := *t[k, v]", Default::default())
            .unwrap();
        assert_eq!(res.rows.len(), 1);
    }

    #[test]
    fn postgres_backend_stub_returns_documented_error() {
        // (c) stub behavior is explicit, not a panic.
        let pg = PostgresBackend {
            pg_url: "postgres://localhost/leankg".into(),
        };
        let err = pg
            .run_script("?[a] := *x[a]", Default::default())
            .unwrap_err()
            .to_string();
        assert!(
            err.contains("Phase 3"),
            "stub error must name the phase: {err}"
        );
        assert!(pg.import_relations(BTreeMap::new()).is_err());
    }

    #[test]
    fn postgres_backend_validates_url_and_redacts() {
        assert!(PostgresBackend::from_env().is_err(), "no env -> error");
        std::env::set_var("LEANKG_PG_URL", "not-a-url");
        let err = PostgresBackend::from_env().unwrap_err();
        assert!(err.contains("not-a-url"));
        let redacted = redact_url("postgres://user:s3cret@host:5432/db?sslmode=require");
        assert!(!redacted.contains("s3cret"));
        assert!(redacted.contains("postgres://user:****@host:5432/db?sslmode=require"));
        std::env::remove_var("LEANKG_PG_URL");
    }

    #[test]
    fn engine_selection_unset_defaults_to_cozo_path_init() {
        // (d) LEANKG_DB_ENGINE unset -> path-based init still returns the
        // cozo shim (tests / local flows never need a running Postgres).
        std::env::remove_var("LEANKG_DB_ENGINE");
        let tmp = TempDir::new().unwrap();
        let db = init_db(&tmp.path().join("sel.db")).unwrap();
        assert!(
            db.run_script("?[a] <- [[1]]", Default::default()).is_ok(),
            "path-init must return the cozo shim (runs scripts), not the PG stub"
        );
    }

    #[test]
    fn engine_selection_cozo_explicit() {
        std::env::set_var("LEANKG_DB_ENGINE", "cozo");
        let tmp = TempDir::new().unwrap();
        let db = init_db(&tmp.path().join("sel.db")).unwrap();
        assert!(
            db.run_script("?[a] <- [[1]]", Default::default()).is_ok(),
            "path-init must return the cozo shim (runs scripts), not the PG stub"
        );
        std::env::remove_var("LEANKG_DB_ENGINE");
    }

    #[test]
    fn engine_selection_postgres_explicit_requires_pg_url() {
        std::env::set_var("LEANKG_DB_ENGINE", "postgres");
        let tmp = TempDir::new().unwrap();
        // Path-based init still resolves to the cozo shim — a missing PG
        // only fails on the explicit init_db_pg() entry point.
        let db = init_db(&tmp.path().join("sel.db")).unwrap();
        assert!(
            db.run_script("?[a] <- [[1]]", Default::default()).is_ok(),
            "path-init must return the cozo shim (runs scripts), not the PG stub"
        );
        assert!(init_db_pg().is_err(), "no LEANKG_PG_URL -> error");
        std::env::remove_var("LEANKG_DB_ENGINE");
    }
}
