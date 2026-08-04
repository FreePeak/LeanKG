//! Storage-backend abstraction (Phase 1 of the CozoDB → PostgreSQL migration).
//!
//! Everything that touches a database goes through [`DbBackend`]; no code
//! outside this module holds a concrete `cozo::DbInstance`. The production
//! backend is PostgreSQL (Phase 3+); [`CozoBackend`] is a temporary
//! migration shim that delegates to CozoDB unchanged (deleted in Phase 8,
//! plan D4).

use crate::db::pg::translate;
use crate::db::schema::mutability_for;
use cozo::{NamedRows, ScriptMutability};
use std::collections::BTreeMap;
use std::path::Path;
use std::sync::{Arc, Mutex};

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

/// PostgreSQL backend (Phase 3 — plan T1.4, T3.5).
///
/// Holds one validated connection URL and a lazily-initialised
/// `postgres::Client` behind a `Mutex<Option<_>>`. The first call to
/// [`Self::run_script`] connects; subsequent calls reuse the handle.
/// Phase 6 adds a connection pool; today we lock per call.
///
/// Read classification flows through [`crate::db::schema::mutability_for`].
/// Writes are wrapped in a single transaction so multi-statement `:put`/
/// `:rm` scripts roll back cleanly on the first failure.
#[derive(Clone)]
pub struct PostgresBackend {
    pub pg_url: String,
    /// Lazy connection handle — tests construct an empty `Arc<Mutex<None>>`
    /// directly; production code goes through [`Self::from_env`].
    pub conn: Arc<Mutex<Option<postgres::Client>>>,
}

impl std::fmt::Debug for PostgresBackend {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("PostgresBackend")
            .field("pg_url", &redact_url(&self.pg_url))
            .field("conn", &"<lazy>")
            .finish()
    }
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
        Ok(Self {
            pg_url: url,
            conn: Arc::new(Mutex::new(None)),
        })
    }

    /// Connect lazily. Returns a guard that releases the mutex when
    /// dropped.
    fn connect(&self) -> Result<(), Box<dyn std::error::Error>> {
        let mut guard = self.conn.lock().unwrap();
        if guard.is_none() {
            let client = postgres::Client::connect(&self.pg_url, postgres::NoTls)?;
            *guard = Some(client);
        }
        Ok(())
    }
}

impl DbBackend for PostgresBackend {
    fn run_script(
        &self,
        query: &str,
        params: BTreeMap<String, serde_json::Value>,
    ) -> Result<NamedRows, Box<dyn std::error::Error>> {
        self.connect()?;
        let mut guard = self.conn.lock().unwrap();
        let client = guard
            .as_mut()
            .ok_or("connection not initialised (lazy connect failed)".to_string())?;

        let t = translate::translate(query, params).map_err(|e| -> Box<dyn std::error::Error> {
            Box::new(std::io::Error::other(format!(
                "translate({}): {e}",
                &query[..query.len().min(60)]
            )))
        })?;

        let mut head = t.head.clone();
        let mut rows: Vec<Vec<cozo::DataValue>> = Vec::new();
        match t.kind {
            translate::TranslationKind::Read => {
                let param_refs: Vec<&(dyn postgres::types::ToSql + Sync)> = t
                    .params
                    .iter()
                    .map(|b| b.as_ref() as &(dyn postgres::types::ToSql + Sync))
                    .collect();
                // Phase 4: `SET LOCAL` needs a tx; wrap the read so the
                // pgvector `hnsw.ef_search` knob from the translator takes
                // effect for this SELECT and reverts on commit.
                if t.gucs.is_empty() {
                    let result = client.query(&t.sql, &param_refs)?;
                    for row in &result {
                        let mapped = translate::map_row(row, &t.head)?;
                        rows.push(mapped);
                    }
                } else {
                    let mut tx = client.transaction()?;
                    apply_gucs(&mut tx, &t.gucs)?;
                    let result = tx.query(&t.sql, &param_refs)?;
                    for row in &result {
                        let mapped = translate::map_row(row, &t.head)?;
                        rows.push(mapped);
                    }
                    tx.commit()?;
                }
                // Header derivation fallback: when the translator didn't
                // record a head (e.g. `::relations`), synthesise generic
                // names from the column count.
                if head.is_empty() && !rows.is_empty() {
                    head = (0..rows[0].len()).map(|i| format!("col{i}")).collect();
                }
            }
            translate::TranslationKind::Write => {
                let param_refs: Vec<&(dyn postgres::types::ToSql + Sync)> = t
                    .params
                    .iter()
                    .map(|b| b.as_ref() as &(dyn postgres::types::ToSql + Sync))
                    .collect();
                let mut tx = client.transaction()?;
                apply_gucs(&mut tx, &t.gucs)?;
                tx.execute(&t.sql, &param_refs)?;
                tx.commit()?;
            }
            translate::TranslationKind::DdlNoop => {
                // `:create`, `:replace`, `VACUUM`, `PRAGMA`, `::hnsw` — no SQL
                // emitted (the schema.sql already pre-created everything).
            }
        }
        Ok(NamedRows::new(head, rows))
    }

    fn import_relations(
        &self,
        data: BTreeMap<String, NamedRows>,
    ) -> Result<(), Box<dyn std::error::Error>> {
        self.connect()?;
        let mut guard = self.conn.lock().unwrap();
        let client = guard
            .as_mut()
            .ok_or("connection not initialised".to_string())?;
        let mut tx = client.transaction()?;
        for (table, named) in data {
            let cols = named.headers.clone();
            let col_sql = cols
                .iter()
                .map(|c| crate::db::pg::translate::quote_ident(c))
                .collect::<Vec<_>>()
                .join(", ");
            // Determine whether the table has a PK — keyed tables in
            // schema.sql are: code_elements (no), relationships (no),
            // embedding_state (qualified_name), embedding_vectors
            // (qualified_name), index_inventory (key), index_hashes
            // (path), migrations (id).
            let pk_col = match table.as_str() {
                "embedding_state" | "embedding_vectors" => Some("qualified_name"),
                "index_inventory" => Some("key"),
                "index_hashes" => Some("path"),
                "migrations" => Some("id"),
                _ => None,
            };
            for row in &named.rows {
                let mut values: Vec<Box<dyn postgres::types::ToSql + Sync + Send>> = Vec::new();
                for (i, val) in row.iter().enumerate() {
                    values.push(cozo_to_pg(val, &cols[i]));
                }
                let value_refs: Vec<&(dyn postgres::types::ToSql + Sync)> = values
                    .iter()
                    .map(|b| b.as_ref() as &(dyn postgres::types::ToSql + Sync))
                    .collect();
                let sql = if let Some(pk) = pk_col {
                    let update_set = cols
                        .iter()
                        .filter(|c| c.as_str() != pk)
                        .map(|c| {
                            format!(
                                "{} = EXCLUDED.{}",
                                crate::db::pg::translate::quote_ident(c),
                                crate::db::pg::translate::quote_ident(c)
                            )
                        })
                        .collect::<Vec<_>>()
                        .join(", ");
                    format!(
                        "INSERT INTO {table} ({col_sql}) VALUES ({vals}) ON CONFLICT ({pk}) DO UPDATE SET {update_set}",
                        vals = (1..=values.len()).map(|i| format!("${i}")).collect::<Vec<_>>().join(", "),
                        pk = crate::db::pg::translate::quote_ident(pk),
                    )
                } else {
                    format!(
                        "INSERT INTO {table} ({col_sql}) VALUES ({vals})",
                        vals = (1..=values.len())
                            .map(|i| format!("${i}"))
                            .collect::<Vec<_>>()
                            .join(", ")
                    )
                };
                tx.execute(&sql, &value_refs)?;
            }
        }
        tx.commit()?;
        Ok(())
    }
}

/// Apply a list of `SET LOCAL name = value` statements on an open
/// transaction. Used to carry per-query pgvector knobs (currently
/// `hnsw.ef_search` for reads, `hnsw.ef_construction` for writes) through
/// the same tx as the main SQL so the GUC is in scope for the next statement
/// and reverts automatically on commit. `name` and `value` are validated by
/// the caller (translator — only known-safe HNSW knobs land here).
fn apply_gucs(
    tx: &mut postgres::Transaction,
    gucs: &[(String, String)],
) -> Result<(), postgres::Error> {
    for (name, value) in gucs {
        // SET LOCAL does not accept parameter placeholders; values are
        // interpolated by the translator (numeric strings from
        // `extract_ann_int_field` / `LEANKG_HNSW_EF_CONST`). The name comes
        // from a hardcoded allowlist (translator).
        let escaped_value = value.replace('\'', "''");
        let sql = format!("SET LOCAL {name} = '{escaped_value}'");
        tx.batch_execute(&sql)?;
    }
    Ok(())
}

/// Convert a cozo DataValue into a boxed `dyn ToSql` for binding. Vector
/// values are emitted as pgvector text literals (e.g. `[0.1, 0.2]`).
fn cozo_to_pg(v: &cozo::DataValue, col: &str) -> Box<dyn postgres::types::ToSql + Sync + Send> {
    use cozo::DataValue;
    match v {
        DataValue::Null => Box::new(Option::<String>::None),
        DataValue::Bool(b) => Box::new(*b),
        DataValue::Num(cozo::Num::Int(i)) => Box::new(*i),
        DataValue::Num(cozo::Num::Float(f)) => Box::new(*f),
        DataValue::Str(s) => Box::new(s.as_str().to_string()),
        DataValue::Json(j) => Box::new(j.0.to_string()),
        DataValue::List(items) if col == "vec" => {
            // pgvector literal: `[0.1,0.2,...]`.
            let mut s = String::from("[");
            for (i, item) in items.iter().enumerate() {
                if i > 0 {
                    s.push(',');
                }
                match item {
                    DataValue::Num(cozo::Num::Float(f)) => s.push_str(&format!("{f}")),
                    DataValue::Num(cozo::Num::Int(i)) => s.push_str(&format!("{i}")),
                    other => s.push_str(&format!("{other}")),
                }
            }
            s.push(']');
            Box::new(s)
        }
        DataValue::Vec(vec) => {
            // F32 or F64 ndarray.
            let mut s = String::from("[");
            match vec {
                cozo::Vector::F32(arr) => {
                    for (i, x) in arr.iter().enumerate() {
                        if i > 0 {
                            s.push(',');
                        }
                        s.push_str(&format!("{x}"));
                    }
                }
                cozo::Vector::F64(arr) => {
                    for (i, x) in arr.iter().enumerate() {
                        if i > 0 {
                            s.push(',');
                        }
                        s.push_str(&format!("{x}"));
                    }
                }
            }
            s.push(']');
            Box::new(s)
        }
        DataValue::Bytes(b) => Box::new(b.clone()),
        other => Box::new(format!("{other}")),
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
        // (c) when the URL is bogus, run_script fails at connection time
        // rather than silently panicking.
        let pg = PostgresBackend {
            pg_url: "postgres://invalid-host-not-real:1/leankg".into(),
            conn: std::sync::Arc::new(std::sync::Mutex::new(None)),
        };
        let err = pg
            .run_script("?[a] := *x[a]", Default::default())
            .unwrap_err()
            .to_string();
        // Either DNS or TCP connect failure surfaces a clear error.
        assert!(!err.is_empty(), "stub error must not be empty: {err}");
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
