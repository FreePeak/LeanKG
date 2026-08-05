//! Storage-backend abstraction (Phase 1 of the CozoDB → PostgreSQL migration).
//!
//! Everything that touches a database goes through [`DbBackend`]; no code
//! outside this module holds a concrete `cozo::DbInstance`. The production
//! backend is PostgreSQL (Phase 3+); [`CozoBackend`] is a temporary
//! migration shim that delegates to CozoDB unchanged (deleted in Phase 8,
//! plan D4).

use crate::db::pg::translate;
use crate::db::schema::mutability_for;
use cozo::ScriptMutability;
use std::collections::{BTreeMap, VecDeque};
use std::ops::{Deref, DerefMut};
use std::path::Path;
use std::sync::{Arc, Condvar, Mutex};

/// Re-export the row/result value types the rest of the codebase consumes
/// positionally (`row[0].get_str()`, `NamedRows::new`, `DataValue::Num`).
/// Phase 5 keeps non-shim `src/` files free of `cozo::` references; the
/// concrete re-export disappears with the shim in Phase 8.
#[allow(unused_imports)]
pub use cozo::{DataValue, NamedRows, Num};

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

/// PostgreSQL backend (Phase 3 — plan T1.4, T3.5; pool added Phase 6).
///
/// Holds one validated connection URL and a lazy pool of `postgres::Client`
/// behind `Mutex<VecDeque>` + Condvar (Phase 6, T6.3). The first call to
/// [`Self::run_script`] connects; subsequent calls reuse checked-out
/// clients. Pool size comes from `LEANKG_PG_POOL_SIZE` (default 5) so
/// concurrent reads on the async MCP path don't serialize on one socket.
///
/// Read classification flows through [`crate::db::schema::mutability_for`].
/// Writes are wrapped in a single transaction so multi-statement `:put`/
/// `:rm` scripts roll back cleanly on the first failure.
#[derive(Clone)]
pub struct PostgresBackend {
    pub pg_url: String,
    /// Pool of lazy read-write connections (Phase 6). Tests construct an
    /// `Arc<ClientPool>` directly; production code goes through
    /// [`Self::from_env`].
    pub pool: Arc<ClientPool>,
    /// Pool of lazy read-only connections (`default_transaction_read_only =
    /// on`, T6.1). Kept separate so RO clients can never be handed to a
    /// writer (a write through an RO session would fail with a confusing
    /// "read-only transaction" error).
    pub ro_pool: Arc<ClientPool>,
    /// When true (T6.1), ALL run_script calls use the RO pool —
    /// `init_db_readonly` semantics on PG. Writes through such a backend
    /// fail at the Postgres layer with a clean error, never silently.
    pub read_only: bool,
}

/// RAII checked-out connection: returns its client to the pool on drop.
pub struct PooledClient {
    client: Option<postgres::Client>,
    pool: Arc<ClientPool>,
}

impl PooledClient {
    fn new(client: postgres::Client, pool: Arc<ClientPool>) -> Self {
        Self {
            client: Some(client),
            pool,
        }
    }
}

impl Deref for PooledClient {
    type Target = postgres::Client;
    fn deref(&self) -> &postgres::Client {
        self.client.as_ref().unwrap()
    }
}

impl DerefMut for PooledClient {
    fn deref_mut(&mut self) -> &mut postgres::Client {
        self.client.as_mut().unwrap()
    }
}

impl Drop for PooledClient {
    fn drop(&mut self) {
        if let Some(c) = self.client.take() {
            self.pool.release(c);
        }
    }
}

/// The pool itself. `Send + Sync` (all members are), so it survives inside
/// `Arc<dyn DbBackend>` across threads (the embed writer thread + MCP async
/// dispatch).
///
/// ponytail: a hand-rolled `VecDeque<Client>` pool rather than
/// deadpool-postgres, because the backend speaks the sync `postgres` crate
/// and deadpool needs tokio-postgres (async) — switching clients would ripple
/// through every `DbBackend` impl + the `block_in_place` guard. The sync
/// pool keeps the same call surface; if async Postgres ever lands (Phase 8),
/// swap this struct for `deadpool::Pool<Manager>`.
#[derive(Clone)]
pub struct ClientPool {
    inner: Arc<ClientPoolState>,
}

struct ClientPoolState {
    max: usize,
    has_slot: Condvar,
    state: Mutex<PoolState>,
}

#[derive(Default)]
struct PoolState {
    idle: VecDeque<postgres::Client>,
    live: usize,
}

impl Drop for ClientPoolState {
    fn drop(&mut self) {
        // The sync postgres Client::drop closes the socket via an internal
        // runtime; inside tokio::main (the CLI) that panics with "Cannot
        // start a runtime from within a runtime". Drain idle clients off
        // the ambient runtime before the VecDeque drops them.
        let state = std::mem::take(&mut self.state);
        let mut inner = state.into_inner().unwrap_or_else(|e| e.into_inner());
        if tokio::runtime::Handle::try_current().is_ok() {
            tokio::task::block_in_place(move || inner.idle.clear());
        } else {
            inner.idle.clear();
        }
    }
}

impl ClientPool {
    /// A pool that connects lazily (first checkout). `max` is clamped >= 1.
    pub fn new(max: usize) -> Self {
        Self {
            inner: Arc::new(ClientPoolState {
                max: max.max(1),
                has_slot: Condvar::new(),
                state: Mutex::new(PoolState {
                    idle: VecDeque::new(),
                    live: 0,
                }),
            }),
        }
    }

    /// Read `LEANKG_PG_POOL_SIZE` (default 5, clamped >= 1).
    pub fn size_from_env() -> usize {
        std::env::var("LEANKG_PG_POOL_SIZE")
            .ok()
            .and_then(|v| v.parse::<usize>().ok())
            .filter(|v| *v >= 1)
            .unwrap_or(5)
    }

    /// Total live + idle clients (used by tests to assert pool reuse).
    pub fn live_count(&self) -> usize {
        self.inner.state.lock().unwrap().live
    }

    /// Check out a client, connecting a new one up to `max` live, else
    /// blocking on a Condvar until one is returned.
    pub fn checkout(&self, connect_url: &str) -> Result<PooledClient, Box<dyn std::error::Error>> {
        let mut guard = self.inner.state.lock().unwrap();
        let pool_arc = Arc::new(self.clone());
        loop {
            if let Some(c) = guard.idle.pop_front() {
                return Ok(PooledClient::new(c, pool_arc.clone()));
            }
            if guard.live < self.inner.max {
                let client = postgres::Client::connect(connect_url, postgres::NoTls)?;
                guard.live += 1;
                return Ok(PooledClient::new(client, pool_arc.clone()));
            }
            // At capacity — wait for a return.
            guard = self
                .inner
                .has_slot
                .wait(guard)
                .unwrap_or_else(|e| e.into_inner());
        }
    }

    fn release(&self, client: postgres::Client) {
        let mut guard = self.inner.state.lock().unwrap();
        guard.idle.push_back(client);
        self.inner.has_slot.notify_one();
    }
}

impl std::fmt::Debug for PostgresBackend {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("PostgresBackend")
            .field("pg_url", &redact_url(&self.pg_url))
            .field("pool", &"<lazy>")
            .field("read_only", &self.read_only)
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
            pool: Arc::new(ClientPool::new(ClientPool::size_from_env())),
            ro_pool: Arc::new(ClientPool::new(ClientPool::size_from_env())),
            read_only: false,
        })
    }

    /// Constructor for the read-only backend (T6.1): `init_db_readonly`
    /// semantics. All script execution goes through the RO pool
    /// (`default_transaction_read_only = on`).
    pub fn from_env_read_only() -> Result<Self, String> {
        Ok(Self::from_env()?.with_read_only())
    }

    /// Builder: pin this backend to read-only execution.
    pub fn with_read_only(mut self) -> Self {
        self.read_only = true;
        self
    }

    /// URL with `default_transaction_read_only = on` injected via the
    /// `options` libpq param. If the URL already carries an `options=`
    /// param (e.g. tests pinning `search_path`), the RO flag is appended
    /// space-separated to that same param — libpq splits `-c` flags on
    /// spaces (verified against PG 18); a second `options=` param would be
    /// dropped. For a read-write backend this is the plain URL (no GUC).
    pub fn read_only_url(&self) -> String {
        if !self.read_only {
            return self.pg_url.clone();
        }
        let base = &self.pg_url;
        if base.contains("default_transaction_read_only") {
            return base.clone();
        }
        const RO_FLAG: &str = "-cdefault_transaction_read_only%3Don";
        // Reuse the existing options= value if present.
        if let Some(pos) = base.find("options=") {
            let after = &base[pos + "options=".len()..];
            let end = after.find('&').unwrap_or(after.len());
            let value = &after[..end];
            let rest = &after[end..]; // "", or "&sslmode=..." etc.
            return format!(
                "{}{}%20{}{}",
                &base[..pos + "options=".len()],
                value,
                RO_FLAG,
                rest
            );
        }
        let (before, after) = base.split_once('?').unwrap_or((base, ""));
        let sep = if after.is_empty() { "?" } else { "&" };
        format!("{before}{sep}{after}options={RO_FLAG}")
    }

    /// Check out a client from the pool. On the first call this connects
    /// lazily (the sync `postgres` client spins up its own tokio runtime —
    /// must run off the ambient runtime, same guard as run_script); with a
    /// warm pool this is a pure mutex hand-off.
    fn checkout(&self) -> Result<PooledClient, Box<dyn std::error::Error>> {
        let url = self.pg_url.clone();
        let pool = self.pool.clone();
        if tokio::runtime::Handle::try_current().is_ok() {
            tokio::task::block_in_place(move || pool.checkout(&url))
        } else {
            pool.checkout(&url)
        }
    }

    /// Check out a client pinned to read-only mode (T6.1) from the RO pool.
    fn checkout_read_only(&self) -> Result<PooledClient, Box<dyn std::error::Error>> {
        let url = self.read_only_url();
        let pool = self.ro_pool.clone();
        if tokio::runtime::Handle::try_current().is_ok() {
            tokio::task::block_in_place(move || pool.checkout(&url))
        } else {
            pool.checkout(&url)
        }
    }

    /// Take the PG advisory lock for exclusive jobs (e.g. `leankg index`,
    /// T6.4b). Blocks until acquired; the lock lives on the session, so the
    /// guard must outlive the job. Returns an unlock guard.
    pub fn advisory_lock(&self, key: i64) -> Result<AdvisoryLock, Box<dyn std::error::Error>> {
        // The execute + (blocking) wait are sync postgres calls — same
        // runtime guard as run_script.
        if tokio::runtime::Handle::try_current().is_ok() {
            tokio::task::block_in_place(|| self.advisory_lock_sync(key))
        } else {
            self.advisory_lock_sync(key)
        }
    }

    fn advisory_lock_sync(&self, key: i64) -> Result<AdvisoryLock, Box<dyn std::error::Error>> {
        let mut client = self.checkout()?;
        client.execute("SELECT pg_advisory_lock($1)", &[&key])?;
        Ok(AdvisoryLock {
            client: Some(client),
            key,
        })
    }

    /// Advisory-lock key for exclusive `leankg index` jobs (T6.4b).
    /// Arbitrary fixed key, database-wide (two-instance serialization).
    pub const INDEX_LOCK_KEY: i64 = 0x6C65616E6B67; // "leankg"

    /// Non-blocking variant: `pg_try_advisory_lock`. Returns None when the
    /// lock is held elsewhere (second concurrent index run).
    pub fn try_advisory_lock(
        &self,
        key: i64,
    ) -> Result<Option<AdvisoryLock>, Box<dyn std::error::Error>> {
        if tokio::runtime::Handle::try_current().is_ok() {
            tokio::task::block_in_place(|| self.try_advisory_lock_sync(key))
        } else {
            self.try_advisory_lock_sync(key)
        }
    }

    fn try_advisory_lock_sync(
        &self,
        key: i64,
    ) -> Result<Option<AdvisoryLock>, Box<dyn std::error::Error>> {
        let mut client = self.checkout()?;
        let ok: bool = client
            .query_one("SELECT pg_try_advisory_lock($1)", &[&key])?
            .get(0);
        if ok {
            Ok(Some(AdvisoryLock {
                client: Some(client),
                key,
            }))
        } else {
            // Return the client to the pool unused. Do NOT drop it here:
            // dropping a sync postgres Client closes its socket via an
            // internal runtime, which panics inside tokio::main.
            let c = client.client.take().unwrap();
            client.pool.release(c);
            Ok(None)
        }
    }

    /// The sync body behind [`DbBackend::run_script`]. Must only run off a
    /// tokio runtime (see the `block_in_place` guard in the trait impl).
    fn run_script_sync(
        &self,
        query: &str,
        params: BTreeMap<String, serde_json::Value>,
    ) -> Result<NamedRows, Box<dyn std::error::Error>> {
        // D2 (plan): the `query_cache` table was dropped — the moka L1 cache
        // in `QueryCache` is the only cache. PersistentCache's DB methods
        // must become no-ops on PG: reads return empty, writes do nothing.
        if query.contains("query_cache") {
            if query.trim_start().starts_with("?[") && !query.contains(":put") {
                // Read: `?[value_json, ...] := *query_cache[...]` → empty
                // result with the declared head columns.
                let head: Vec<String> = query
                    .split_once("?[")
                    .and_then(|(_, rest)| rest.split_once(']'))
                    .map(|(inner, _)| {
                        inner
                            .split(',')
                            .map(|s| s.trim().to_string())
                            .filter(|s| !s.is_empty())
                            .collect()
                    })
                    .unwrap_or_default();
                return Ok(cozo::NamedRows::new(head, Vec::new()));
            }
            // `:put query_cache ...` / `:delete query_cache ...` /
            // `:delete query_cache where ...` → no-op write.
            return Ok(cozo::NamedRows::new(Vec::new(), Vec::new()));
        }
        // T6.1: a read-only backend never touches the RW pool — writes are
        // rejected by Postgres itself (`default_transaction_read_only = on`).
        let mut client = if self.read_only {
            self.checkout_read_only()?
        } else {
            self.checkout()?
        };

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
    fn import_relations_sync(
        &self,
        data: BTreeMap<String, NamedRows>,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let mut client = if self.read_only {
            self.checkout_read_only()?
        } else {
            self.checkout()?
        };
        let mut tx = client.transaction()?;
        for (table, named) in data {
            let cols = named.headers.clone();
            // Cozo names the vector column `vector`; the PG schema uses
            // `vec`. Map before emitting SQL / binding values.
            let cols: Vec<String> = cols
                .into_iter()
                .map(|c| {
                    if table == "embedding_vectors" && c == "vector" {
                        "vec".to_string()
                    } else {
                        c
                    }
                })
                .collect();
            // Keyed tables (single PK) get the COPY + ON CONFLICT path;
            // non-keyed tables (code_elements, relationships, ...) fall back
            // to multi-row INSERT (they cannot dedupe via a PK).
            let pk_col = match table.as_str() {
                "embedding_state" | "embedding_vectors" => Some("qualified_name"),
                "index_inventory" => Some("key"),
                "index_hashes" => Some("path"),
                "migrations" => Some("id"),
                _ => None,
            };
            match pk_col {
                Some(pk) if bulk_copy_enabled() => {
                    self.copy_upsert(&mut tx, &table, &cols, pk, &named)?;
                }
                _ => {
                    self.insert_rows(&mut tx, &table, &cols, pk_col, &named)?;
                }
            }
        }
        tx.commit()?;
        Ok(())
    }

    /// COPY-based upsert (plan T7.1). Writes `rows` into a temporary staging
    /// table shaped LIKE the target, then one `INSERT ... SELECT ... ON
    /// CONFLICT (pk) DO UPDATE` folds the batch in. COPY is the fastest bulk
    /// path in Postgres (no per-row round trip, no WAL-per-row bind); the
    /// single follow-up INSERT keeps `ON CONFLICT DO UPDATE` semantics that
    /// the cozo `import_relations` callers rely on (upsert_fresh, vectors).
    ///
    /// The temp table is `CREATE TEMP TABLE ... ON COMMIT DROP`, so it is
    /// scoped to this transaction and vanishes on commit — no schema pollution.
    fn copy_upsert(
        &self,
        tx: &mut postgres::Transaction,
        table: &str,
        cols: &[String],
        pk: &str,
        named: &crate::db::backend::NamedRows,
    ) -> Result<(), Box<dyn std::error::Error>> {
        use std::io::Write;

        if named.rows.is_empty() {
            return Ok(());
        }
        let q_table = crate::db::pg::translate::quote_ident(table);
        let q_cols = cols
            .iter()
            .map(|c| crate::db::pg::translate::quote_ident(c))
            .collect::<Vec<_>>()
            .join(", ");
        // Staging name from the unquoted table + suffix (the quote is applied
        // around the whole identifier, so `"embedding_vectors_staging"` is a
        // single quoted ident — never `"embedding_vectors"_staging`).
        let staging = crate::db::pg::translate::quote_ident(&format!("{table}_staging"));
        // `LIKE` inherits column types; ON COMMIT DROP scopes the table to
        // this transaction.
        tx.batch_execute(&format!(
            "CREATE TEMP TABLE {staging} (LIKE {q_table}) ON COMMIT DROP"
        ))?;
        let copy_sql = format!("COPY {staging} ({q_cols}) FROM STDIN");
        let mut writer = tx.copy_in(&copy_sql)?;
        for row in &named.rows {
            let mut line = String::new();
            for (i, val) in row.iter().enumerate() {
                if i > 0 {
                    line.push('\t');
                }
                // Escape per COPY text format: tab, newline, carriage return,
                // backslash.
                push_copy_text(&mut line, &data_to_copy_text(val, &cols[i]));
            }
            line.push('\n');
            writer.write_all(line.as_bytes())?;
        }
        writer.finish()?;

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
        let q_pk = crate::db::pg::translate::quote_ident(pk);
        tx.execute(
            &format!(
                "INSERT INTO {q_table} ({q_cols}) SELECT {q_cols} FROM {staging} \
                 ON CONFLICT ({q_pk}) DO UPDATE SET {update_set}"
            ),
            &[],
        )?;
        Ok(())
    }

    /// Multi-row INSERT path (fallback for non-keyed tables and when the COPY
    /// env gate is off). Kept from Phase 3/4 — the per-row bound loop the
    /// plan's Phase 6 hand-off flagged as the bottleneck; the COPY path above
    /// replaces it for keyed tables.
    fn insert_rows(
        &self,
        tx: &mut postgres::Transaction,
        table: &str,
        cols: &[String],
        pk: Option<&str>,
        named: &crate::db::backend::NamedRows,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let col_sql = cols
            .iter()
            .map(|c| crate::db::pg::translate::quote_ident(c))
            .collect::<Vec<_>>()
            .join(", ");
        for row in &named.rows {
            let mut values: Vec<Box<dyn postgres::types::ToSql + Sync + Send>> = Vec::new();
            for (i, val) in row.iter().enumerate() {
                values.push(cozo_to_pg(val, &cols[i]));
            }
            let value_refs: Vec<&(dyn postgres::types::ToSql + Sync)> = values
                .iter()
                .map(|b| b.as_ref() as &(dyn postgres::types::ToSql + Sync))
                .collect();
            let sql = if let Some(pk) = pk {
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
        Ok(())
    }
}

/// Session-scoped advisory lock (T6.4b). `pg_advisory_lock` is held on the
/// connection until `pg_advisory_unlock` or the session ends; dropping the
/// guard unlocks explicitly.
pub struct AdvisoryLock {
    client: Option<PooledClient>,
    key: i64,
}

impl Drop for AdvisoryLock {
    fn drop(&mut self) {
        if let Some(mut client) = self.client.take() {
            let key = self.key;
            // The unlock is a sync postgres call — off the ambient runtime.
            if tokio::runtime::Handle::try_current().is_ok() {
                let _ = tokio::task::block_in_place(move || {
                    client.execute("SELECT pg_advisory_unlock($1)", &[&key])
                });
            } else {
                let _ = client.execute("SELECT pg_advisory_unlock($1)", &[&key]);
            }
            // client returns to the pool here (Drop of PooledClient).
        }
    }
}

impl DbBackend for PostgresBackend {
    /// Execute a script. Phase 5.5 regression finding: the `postgres`
    /// sync client spins up its own tokio runtime internally, so calling it
    /// from inside a tokio runtime (the MCP server's async tool dispatch)
    /// panics with "Cannot start a runtime from within a runtime". CozoDB
    /// was sync-native, so nothing noticed until PG. `block_in_place`
    /// yields the worker thread and lets the blocking client run; on
    /// non-runtime threads (CLI, `leankg migrate`, sync tests) it is a
    /// no-op.
    fn run_script(
        &self,
        query: &str,
        params: BTreeMap<String, serde_json::Value>,
    ) -> Result<NamedRows, Box<dyn std::error::Error>> {
        if tokio::runtime::Handle::try_current().is_ok() {
            tokio::task::block_in_place(|| self.run_script_sync(query, params))
        } else {
            self.run_script_sync(query, params)
        }
    }

    /// Execute a script. Phase 5.5 regression finding: the `postgres`
    /// sync client spins up its own tokio runtime internally, so calling it
    /// from inside a tokio runtime (the MCP server's async tool dispatch)
    /// panics with "Cannot start a runtime from within a runtime". CozoDB
    /// was sync-native, so nothing noticed until PG. `block_in_place`
    /// yields the worker thread and lets the blocking client run; on
    /// non-runtime threads (CLI, `leankg migrate`, sync tests) it is a
    /// no-op.
    fn import_relations(
        &self,
        data: BTreeMap<String, NamedRows>,
    ) -> Result<(), Box<dyn std::error::Error>> {
        if tokio::runtime::Handle::try_current().is_ok() {
            tokio::task::block_in_place(|| self.import_relations_sync(data))
        } else {
            self.import_relations_sync(data)
        }
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

/// Whether `import_relations` uses the COPY bulk path (T7.1) for keyed
/// tables. On by default; `LEANKG_EMBED_COPY=0` opts back into the per-row
/// INSERT loop (parity / debugging).
fn bulk_copy_enabled() -> bool {
    std::env::var("LEANKG_EMBED_COPY")
        .map(|v| !matches!(v.as_str(), "0" | "false" | "off"))
        .unwrap_or(true)
}

/// Env gate for the drop-index-during-bulk + reindex strategy (T7.2).
/// When the total batch exceeds `LEANKG_EMBED_BULK_REINDEX_THRESHOLD`
/// (default 100k) OR `LEANKG_EMBED_COPY=1` is explicitly set, the HNSW
/// index is dropped before the COPY batches and recreated after — faster
/// than incremental index maintenance on very large cold embeds.
fn bulk_reindex_enabled(total_rows: usize) -> bool {
    if std::env::var("LEANKG_EMBED_COPY")
        .map(|v| matches!(v.as_str(), "1" | "true" | "on"))
        .unwrap_or(false)
    {
        return true;
    }
    let threshold = std::env::var("LEANKG_EMBED_BULK_REINDEX_THRESHOLD")
        .ok()
        .and_then(|v| v.parse::<usize>().ok())
        .unwrap_or(100_000);
    total_rows >= threshold
}

/// Render a `DataValue` into Postgres COPY text (one field). Vectors become
/// pgvector literals (`[0.1,0.2,...]`); strings/ints/floats/bools/null use
/// their natural textual form. The caller escapes COPY metacharacters.
fn data_to_copy_text(v: &cozo::DataValue, col: &str) -> String {
    use cozo::DataValue;
    match v {
        DataValue::Null => String::new(), // COPY: empty field == NULL
        DataValue::Bool(b) => b.to_string(),
        DataValue::Num(cozo::Num::Int(i)) => i.to_string(),
        DataValue::Num(cozo::Num::Float(f)) => f.to_string(),
        DataValue::Str(s) => s.as_str().to_string(),
        DataValue::Json(j) => j.0.to_string(),
        // Cozo names the vector column `vector`; the PG schema uses `vec`.
        DataValue::List(items) if col == "vec" || col == "vector" => {
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
            s
        }
        DataValue::Vec(vec) => {
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
            s
        }
        DataValue::Bytes(b) => {
            let mut s = String::with_capacity(b.len() * 2);
            for byte in b {
                s.push_str(&format!("\\{:03o}", byte));
            }
            s
        }
        other => format!("{other}"),
    }
}

/// Append `s` to `out`, escaping Postgres COPY text-format metacharacters
/// (tab, newline, carriage return, backslash).
fn push_copy_text(out: &mut String, s: &str) {
    for ch in s.chars() {
        match ch {
            '\t' => out.push_str("\\t"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\\' => out.push_str("\\\\"),
            _ => out.push(ch),
        }
    }
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
        // The caller's NamedRows headers use the cozo name (`vector`); the
        // PG column is `vec` (schema.sql). Match both.
        DataValue::List(items) if col == "vec" || col == "vector" => {
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
/// * unset → cozo shim (tests / local flows never need a running Postgres).
/// * `postgres` → **`PostgresBackend` when `LEANKG_PG_URL` is set**, else
///   cozo shim (Phase 6 CLI routing: explicit engine + URL routes every
///   path-based init — CLI, web server, MCP — through Postgres). A missing
///   URL fails loudly only on the explicit `init_db_pg` entry point.
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

/// True when the EXPLICIT Postgres engine is selected AND a URL is present —
/// the Phase 6 gate for routing path-based init through `PostgresBackend`.
/// `resolve_engine()` defaults an unset var to `"postgres"` (the migration
/// end-state), but during the migration the engine must be set explicitly
/// before any code path connects to Postgres — otherwise a stray
/// `LEANKG_PG_URL` in the environment (dev shell, CI) would silently swap
/// every cozo-backed test onto Postgres.
fn postgres_configured() -> bool {
    std::env::var("LEANKG_DB_ENGINE")
        .map(|v| v.eq_ignore_ascii_case("postgres"))
        .unwrap_or(false)
        && std::env::var("LEANKG_PG_URL").is_ok()
}

/// Open the default backend for a file/dir database path. See
/// [`resolve_engine`] for selection semantics. Phase 6 (T6.4c CLI routing):
/// with `LEANKG_DB_ENGINE=postgres` + `LEANKG_PG_URL`, this returns a
/// `PostgresBackend` — every CLI entry point (`init`/`index`/`serve`/
/// `status`/…) goes through this function.
pub fn init_db(db_path: &Path) -> Result<SharedDb, Box<dyn std::error::Error>> {
    if postgres_configured() {
        let pg = PostgresBackend::from_env()?;
        tracing::info!(
            "DB engine = postgres (LEANKG_DB_ENGINE=postgres + LEANKG_PG_URL): {}",
            redact_url(&pg.pg_url)
        );
        return Ok(Arc::new(pg));
    }
    init_cozo(db_path, false)
}

/// Open a read-only backend for a file/dir database path. Phase 6 (T6.1):
/// on the explicit Postgres engine this opens a true read-only connection
/// (`default_transaction_read_only = on`) — writes fail at the Postgres
/// layer instead of the CozoDB RocksDB same-handle workaround. Falls back
/// to the cozo shim (sqlite `mode=ro`) when the engine is unset/cozo.
pub fn init_db_readonly(db_path: &Path) -> Result<SharedDb, Box<dyn std::error::Error>> {
    if postgres_configured() {
        let pg = PostgresBackend::from_env_read_only()?;
        tracing::info!(
            "DB engine = postgres read-only (default_transaction_read_only = on): {}",
            redact_url(&pg.pg_url)
        );
        return Ok(Arc::new(pg));
    }
    init_cozo(db_path, true)
}

/// Open a PostgreSQL backend. Fails when `LEANKG_PG_URL` is missing or
/// malformed. This is the entry point that produces a [`PostgresBackend`]
/// unconditionally (used by `leankg migrate` and the in-process harness).
pub fn init_db_pg() -> Result<SharedDb, Box<dyn std::error::Error>> {
    let pg = PostgresBackend::from_env()?;
    tracing::info!("DB engine = postgres: {}", redact_url(&pg.pg_url));
    Ok(Arc::new(pg))
}

/// Acquire the index advisory lock when the explicit Postgres engine is
/// configured, else return None (cozo shim — no lock). Blocks until the
/// lock is free, so a second concurrent `leankg index` waits for the first
/// to finish. The lock lives on a dedicated session, so it also guards
/// against a nested `index_codebase` re-entry (incremental → full fallback)
/// deadlocking itself on a second connection: we return the already-held
/// lock via a process-level registry.
///
/// `LEANKG_PG_LOCK=0` disables the advisory lock (operators who manage
/// exclusivity externally, e.g. a job queue). Default: on when the engine
/// is Postgres.
pub fn index_advisory_lock() -> Result<Option<AdvisoryLock>, Box<dyn std::error::Error>> {
    if !postgres_configured() {
        return Ok(None);
    }
    if std::env::var("LEANKG_PG_LOCK")
        .ok()
        .map(|v| v.eq_ignore_ascii_case("0") || v.eq_ignore_ascii_case("false"))
        .unwrap_or(false)
    {
        tracing::info!("LEANKG_PG_LOCK=0 — index advisory lock disabled");
        return Ok(None);
    }
    let key = PostgresBackend::INDEX_LOCK_KEY;
    // Reentrant within this process: the same `leankg index` may call
    // index_codebase twice (incremental fallback). A second PG advisory
    // lock on a different session would deadlock against the first.
    let mut held = INDEX_LOCK_HELD.lock().unwrap();
    if *held {
        return Ok(None);
    }
    let pg = PostgresBackend::from_env()?;
    let lock = pg.advisory_lock(key)?;
    *held = true;
    tracing::info!("index advisory lock held (key {key})");
    Ok(Some(lock))
}

/// Process-level flag so nested index invocations skip re-acquiring.
static INDEX_LOCK_HELD: std::sync::Mutex<bool> = std::sync::Mutex::new(false);

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
            pool: std::sync::Arc::new(ClientPool::new(1)),
            ro_pool: std::sync::Arc::new(ClientPool::new(1)),
            read_only: false,
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
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        assert!(PostgresBackend::from_env().is_err(), "no env -> error");
        std::env::set_var("LEANKG_PG_URL", "not-a-url");
        let err = PostgresBackend::from_env().unwrap_err();
        assert!(err.contains("not-a-url"));
        let redacted = redact_url("postgres://user:s3cret@host:5432/db?sslmode=require");
        assert!(!redacted.contains("s3cret"));
        assert!(redacted.contains("postgres://user:****@host:5432/db?sslmode=require"));
        std::env::remove_var("LEANKG_PG_URL");
    }

    /// Serialize tests that mutate process env (LEANKG_DB_ENGINE /
    /// LEANKG_PG_URL / LEANKG_PG_POOL_SIZE) — Rust runs tests in parallel
    /// and env is process-global.
    static ENV_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

    #[test]
    fn engine_selection_unset_defaults_to_cozo_path_init() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
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
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
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
    fn engine_selection_postgres_explicit_without_url_keeps_cozo_shim() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        // Phase 6 CLI routing: explicit `postgres` engine but NO URL must
        // NOT break existing paths — path init keeps returning the cozo
        // shim so tests / local dev without PG are unaffected.
        std::env::set_var("LEANKG_DB_ENGINE", "postgres");
        std::env::remove_var("LEANKG_PG_URL");
        let tmp = TempDir::new().unwrap();
        let db = init_db(&tmp.path().join("sel.db")).unwrap();
        assert!(
            db.run_script("?[a] <- [[1]]", Default::default()).is_ok(),
            "path-init must return the cozo shim (runs scripts), not the PG stub"
        );
        assert!(init_db_pg().is_err(), "no LEANKG_PG_URL -> error");
        std::env::remove_var("LEANKG_DB_ENGINE");
    }

    #[test]
    fn engine_selection_postgres_with_url_returns_pg_backend() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        // Phase 6 CLI routing: explicit `postgres` + URL routes path-based
        // init through PostgresBackend. The connect itself is lazy, so no
        // live Postgres is required here. Discriminator: the cozo shim
        // executes `?[a] <- [[1]]`, the PG backend rejects it at translate
        // time — no `downcast_ref` needed (trait shape unchanged).
        std::env::set_var("LEANKG_DB_ENGINE", "postgres");
        std::env::set_var(
            "LEANKG_PG_URL",
            "postgresql://postgres:postgres@localhost:5433/leankg",
        );
        let tmp = TempDir::new().unwrap();
        let db = init_db(&tmp.path().join("sel.db")).unwrap();
        assert!(
            db.run_script("?[a] <- [[1]]", Default::default()).is_err(),
            "explicit engine + URL must produce the PG backend (translator rejects bare lists)"
        );
        std::env::remove_var("LEANKG_DB_ENGINE");
        std::env::remove_var("LEANKG_PG_URL");
    }

    #[test]
    fn engine_selection_postgres_readonly_uses_ro_url() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        // T6.1: the read-only path must inject default_transaction_read_only.
        // Connect is lazy; assert via the URL builder instead.
        std::env::set_var("LEANKG_DB_ENGINE", "postgres");
        std::env::set_var(
            "LEANKG_PG_URL",
            "postgresql://postgres:postgres@localhost:5433/leankg",
        );
        let pg = PostgresBackend::from_env_read_only().unwrap();
        assert!(pg.read_only);
        let ro_url = pg.read_only_url();
        assert!(
            ro_url.contains("default_transaction_read_only%3Don"),
            "RO URL must inject the read-only GUC: {ro_url}"
        );
        // The RW URL must NOT contain it.
        let rw_url = PostgresBackend::from_env().unwrap().read_only_url();
        assert!(!rw_url.contains("default_transaction_read_only%3Don"));
        std::env::remove_var("LEANKG_DB_ENGINE");
        std::env::remove_var("LEANKG_PG_URL");
    }

    #[test]
    fn engine_selection_unset_with_url_keeps_cozo_shim() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        // A stray LEANKG_PG_URL in the env must NOT reroute path init — the
        // engine var has to be set explicitly (migration guard).
        std::env::remove_var("LEANKG_DB_ENGINE");
        std::env::set_var(
            "LEANKG_PG_URL",
            "postgresql://postgres:postgres@localhost:5433/leankg",
        );
        let tmp = TempDir::new().unwrap();
        let db = init_db(&tmp.path().join("sel.db")).unwrap();
        assert!(
            db.run_script("?[a] <- [[1]]", Default::default()).is_ok(),
            "unset engine must keep the cozo shim even with LEANKG_PG_URL set"
        );
        std::env::remove_var("LEANKG_PG_URL");
    }

    #[test]
    fn pool_size_from_env_defaults_and_clamps() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        std::env::remove_var("LEANKG_PG_POOL_SIZE");
        assert_eq!(ClientPool::size_from_env(), 5);
        std::env::set_var("LEANKG_PG_POOL_SIZE", "0");
        assert_eq!(ClientPool::size_from_env(), 5, "0 -> clamp to default");
        std::env::set_var("LEANKG_PG_POOL_SIZE", "-3");
        assert_eq!(ClientPool::size_from_env(), 5, "negative -> default");
        std::env::set_var("LEANKG_PG_POOL_SIZE", "12");
        assert_eq!(ClientPool::size_from_env(), 12);
        std::env::set_var("LEANKG_PG_POOL_SIZE", "banana");
        assert_eq!(ClientPool::size_from_env(), 5, "garbage -> default");
        std::env::remove_var("LEANKG_PG_POOL_SIZE");
    }

    #[test]
    fn pool_new_clamps_max_to_one() {
        let p = ClientPool::new(0);
        // checkout with a dead URL still attempts a connection; the clamp is
        // internal. Verify via a direct connection error only — the max is
        // exercised by container-gated tests.
        assert!(p
            .checkout("postgres://invalid-host-not-real:1/leankg")
            .is_err());
    }
}
