//! PostgreSQL backend — the only storage engine (post-migration, plan D4).
//!
//! Everything that touches a database goes through [`PostgresBackend`].
//! The legacy `DbBackend` trait and `CozoBackend` shim were deleted in
//! Phase 8; `run_script` is now a concrete inherent method.

use crate::db::pg::mutability;
use crate::db::pg::translate;
use std::collections::{BTreeMap, VecDeque};
use std::ops::{Deref, DerefMut};
use std::sync::{Arc, Condvar, Mutex};

/// Connect a Postgres client, choosing NoTls or rustls at the call site.
///
/// Local dev PG on :5433 (no `LEANKG_PG_CA_CERT`, no `sslmode=verify-*`)
/// connects plain; remote managed Postgres connects over rustls rooted at the
/// CA in `LEANKG_PG_CA_CERT` (Aiven private CA). A `verify-full`/`verify-ca`
/// URL without that env var falls back to the Mozilla root store compiled in
/// via `webpki-roots` (public CAs like Let's Encrypt). Each branch uses a
/// concrete connector — `MakeTlsConnect` has associated types, so a single
/// boxed trait object is not object-safe — hence the separate
/// `cfg.connect(...)` calls below.
///
/// True when the URL requests libpq `sslmode=verify-full` / `verify-ca`.
fn url_wants_verified_tls(url: &str) -> bool {
    url.split('?').nth(1).unwrap_or("").split('&').any(|kv| {
        kv.strip_prefix("sslmode=")
            .map(|v| matches!(v, "verify-full" | "verify-ca"))
            .unwrap_or(false)
    })
}

/// Rewrite `sslmode=verify-full` / `verify-ca` to `sslmode=require` in the URL.
///
/// tokio-postgres 0.7 only understands disable|prefer|require and *rejects*
/// unknown `sslmode` values at `Config::parse` time, so the rewrite must
/// happen on the URL string before parsing. Chain + hostname verification is
/// still performed — it comes from the rustls root store + server name, which
/// is exactly what verify-full/verify-ca mean. Returns the URL unchanged when
/// it is already parseable.
fn normalize_pg_url_for_parse(url: &str) -> String {
    let Some((base, query)) = url.split_once('?') else {
        return url.to_string();
    };
    let normalized: Vec<&str> = query
        .split('&')
        .map(|kv| match kv.strip_prefix("sslmode=") {
            Some("verify-full") | Some("verify-ca") => "sslmode=require",
            _ => kv,
        })
        .collect();
    format!("{base}?{}", normalized.join("&"))
}

/// Connect a `postgres::Client`, applying the crate's URL/TLS rules
/// (`sslmode=verify-full` normalization, `LEANKG_PG_CA_CERT`, webpki roots).
/// Public so integration tests can perform admin DDL against the same
/// managed-Postgres URLs the binary itself accepts.
pub fn pg_connect(url: &str) -> Result<postgres::Client, Box<dyn std::error::Error>> {
    let normalized = normalize_pg_url_for_parse(url);
    let wants_tls = url_wants_verified_tls(url);
    let cfg: postgres::Config = normalized.parse()?;
    // Install the ring crypto provider before any rustls use (idempotent;
    // rustls 0.23 panics at first handshake if no provider is installed).
    let _ = rustls::crypto::ring::default_provider().install_default();
    let ca_path = std::env::var("LEANKG_PG_CA_CERT").ok();
    match ca_path {
        Some(path) => {
            // Private/managed CA (Aiven, Neon, ...): root at that CA cert.
            let pem = std::fs::read(&path)?;
            let certs = rustls_pemfile::certs(&mut &pem[..]).collect::<Result<Vec<_>, _>>()?;
            let mut roots = rustls::RootCertStore::empty();
            for c in certs {
                roots
                    .add(c)
                    .map_err(|e| format!("bad CA cert in {path}: {e}"))?;
            }
            let config = rustls::ClientConfig::builder()
                .with_root_certificates(roots)
                .with_no_client_auth();
            let connector = postgres_rustls::MakeTlsConnector::new(Arc::new(config).into());
            Ok(cfg.connect(connector)?)
        }
        None if wants_tls => {
            // Public CA (Let's Encrypt, ...): verify against the Mozilla roots
            // compiled in via webpki-roots (already in the tree through
            // reqwest/hyper-rustls; no new dependency).
            let mut roots = rustls::RootCertStore::empty();
            roots.extend(webpki_roots::TLS_SERVER_ROOTS.iter().cloned());
            let config = rustls::ClientConfig::builder()
                .with_root_certificates(roots)
                .with_no_client_auth();
            let connector = postgres_rustls::MakeTlsConnector::new(Arc::new(config).into());
            Ok(cfg.connect(connector)?)
        }
        None => Ok(cfg.connect(postgres::NoTls)?),
    }
}

/// Re-export the row/result value types the rest of the codebase consumes
/// positionally (`row[0].get_str()`, `NamedRows::new`, `DataValue::Num`).
pub use crate::db::value::{DataValue, NamedRows};

/// Storage-backend abstraction. Production uses [`PostgresBackend`];
/// tests use an in-memory [`crate::db::fake::FakeBackend`] so unit tests
/// never need a live Postgres.
pub trait DbBackend: Send + Sync {
    /// Run a Cozo-script query (translated to SQL by the PG backend, or
    /// interpreted in-memory by the fake). Returns named rows.
    fn run_script(
        &self,
        query: &str,
        params: BTreeMap<String, serde_json::Value>,
    ) -> Result<NamedRows, Box<dyn std::error::Error>>;

    /// Submit a write through the priority write bus (Slice 4 seam).
    ///
    /// Default impl runs inline (same as `run_script`). The PG backend
    /// overrides to route via [`crate::db::write_bus::WriteBus`] when the
    /// backend carries one, so tool writes jump embed writes without
    /// callers having to know which backend they hold. Callers use this
    /// for tool writes (add_knowledge, add_annotation, ...); reads and
    /// embed bulk writes keep using `run_script` / `import_relations`.
    fn submit_write(
        &self,
        query: &str,
        params: BTreeMap<String, serde_json::Value>,
        _priority: crate::db::write_bus::Priority,
    ) -> Result<NamedRows, Box<dyn std::error::Error>> {
        self.run_script(query, params)
    }

    /// Bulk-load named rows into a relation.
    fn import_relations(
        &self,
        data: BTreeMap<String, NamedRows>,
    ) -> Result<(), Box<dyn std::error::Error>>;

    /// Priority variant of `import_relations` for embed bulk writes
    /// (Slice 5). Default impl runs inline; PG backend overrides to route
    /// through the bus as `Priority::EmbedWrite` so tool writes can jump
    /// embed batches.
    fn submit_import(
        &self,
        data: BTreeMap<String, NamedRows>,
        _priority: crate::db::write_bus::Priority,
    ) -> Result<(), Box<dyn std::error::Error>> {
        self.import_relations(data)
    }

    /// Safe-to-log connection URL (password masked).
    fn redacted_url(&self) -> String;

    /// Classify a script as read/write/DDL.
    fn mutability_for(&self, query: &str) -> mutability::ScriptMutability;

    /// Whether this backend rejects writes (read-only MCP/query process).
    /// Callers that best-effort writes (metrics, stats) no-op on RO backends
    /// instead of surfacing an opaque `db error` on every query.
    fn is_read_only(&self) -> bool {
        false
    }
}

/// Shared handle used throughout the codebase. `Arc` so clones of
/// `GraphEngine` share ONE underlying backend (one PG pool / one fake store).
pub type SharedDb = Arc<dyn DbBackend>;

/// PostgreSQL backend (Phase 3 — plan T1.4, T3.5; pool added Phase 6).
///
/// Holds one validated connection URL and a lazy pool of `postgres::Client`
/// behind `Mutex<VecDeque>` + Condvar (Phase 6, T6.3). The first call to
/// [`Self::run_script`] connects; subsequent calls reuse checked-out
/// clients. Pool size comes from `LEANKG_PG_POOL_SIZE` (default 5) so
/// concurrent reads on the async MCP path don't serialize on one socket.
///
/// Read classification flows through [`crate::db::pg::mutability::mutability_for`].
/// Writes are wrapped in a single transaction so multi-statement `:put`/
/// `:rm` scripts roll back cleanly on the first failure.
#[derive(Clone)]
pub struct PostgresBackend {
    pub pg_url: String,
    /// Per-project PG schema (multi-project on one shared Postgres, Phase 8
    /// D4). When `Some`, the connection URL carries
    /// `options=-csearch_path=<schema>,public` so every query is scoped to
    /// that project's tables. Derived from the project `.leankg` path by
    /// [`schema_for_path`]; None keeps the default `public` search_path.
    pub schema: Option<String>,
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
    /// Optional priority write bus (Slice 3). When `Some`, write-classified
    /// scripts are submitted to the bus (so tool writes jump embed writes).
    /// When `None` — the current default — writes run inline as before.
    /// Opt-in to avoid touching every test backend constructor today.
    pub write_bus: Option<Arc<dyn crate::db::write_bus::WriteBus>>,
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
/// `Arc<PostgresBackend>` across threads (the embed writer thread + MCP
/// async dispatch).
///
/// ponytail: a hand-rolled `VecDeque<Client>` pool rather than
/// deadpool-postgres, because the backend speaks the sync `postgres` crate
/// and deadpool needs tokio-postgres (async) — switching clients would
/// ripple through every `DbBackend` impl + the `block_in_place` guard. The
/// sync pool keeps the same call surface; if async Postgres ever lands,
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
        Self::size_from_env_or(None)
    }

    /// Pool size: `LEANKG_PG_POOL_SIZE` env > `db.pool_size` yaml > 5.
    pub fn size_from_env_or(yaml: Option<usize>) -> usize {
        std::env::var("LEANKG_PG_POOL_SIZE")
            .ok()
            .and_then(|v| v.parse::<usize>().ok())
            .filter(|v| *v >= 1)
            .or(yaml.filter(|v| *v >= 1))
            .unwrap_or(5)
    }

    /// Total live + idle clients (used by tests to assert pool reuse).
    pub fn live_count(&self) -> usize {
        self.inner.state.lock().unwrap().live
    }

    /// Configured pool cap (used by tests to assert pool sizing).
    pub fn max_size(&self) -> usize {
        self.inner.max
    }

    /// Check out a client, connecting a new one up to `max` live, else
    /// blocking on a Condvar until one is returned.
    pub fn checkout(&self, connect_url: &str) -> Result<PooledClient, Box<dyn std::error::Error>> {
        let mut guard = self.inner.state.lock().unwrap();
        let pool_arc = Arc::new(self.clone());
        loop {
            if let Some(c) = guard.idle.pop_front() {
                if c.is_closed() {
                    // Stale connection (server closed it — idle timeout,
                    // network blip, OrbStack host.docker.internal flap).
                    // Drop it and open a fresh one; never hand a dead
                    // client to a caller (that surfaces as opaque
                    // `Error::Closed` from every subsequent query).
                    guard.live -= 1;
                    tracing::warn!("pg pool: dropped closed idle client; live={}", guard.live);
                    continue;
                }
                return Ok(PooledClient::new(c, pool_arc.clone()));
            }
            if guard.live < self.inner.max {
                let client = pg_connect(connect_url)?;
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
        if client.is_closed() {
            guard.live -= 1;
            tracing::warn!(
                "pg pool: dropped closed client on release; live={}",
                guard.live
            );
        } else {
            guard.idle.push_back(client);
        }
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
    /// Format-check the resolved Postgres URL. Precedence:
    /// `LEANKG_PG_URL` env > `db:` block in `leankg.yaml` > built-in dev
    /// default (`postgresql://postgres:postgres@localhost:5433/leankg`,
    /// matches the container-gated dev Postgres). Returns Err when the
    /// resolved URL does not look like a Postgres URL.
    pub fn from_env() -> Result<Self, String> {
        let url = std::env::var("LEANKG_PG_URL")
            .ok()
            .filter(|v| !v.trim().is_empty())
            .or_else(|| {
                crate::config::db_config_from_cwd()
                    .and_then(|db| db.url.filter(|u| !u.trim().is_empty()))
            })
            .unwrap_or_else(|| "postgresql://postgres:postgres@localhost:5433/leankg".to_string());
        if !url.starts_with("postgres://") && !url.starts_with("postgresql://") {
            return Err(format!(
                "LEANKG_PG_URL must be a postgres:// URL, got: {}",
                redact_url(&url)
            ));
        }
        let pool_size = ClientPool::size_from_env_or(
            crate::config::db_config_from_cwd().and_then(|db| db.pool_size),
        );
        Ok(Self {
            pg_url: url,
            schema: None,
            pool: Arc::new(ClientPool::new(pool_size)),
            ro_pool: Arc::new(ClientPool::new(pool_size)),
            read_only: false,
            write_bus: None,
        })
    }

    /// Pin this backend to a per-project PG schema. Injects
    /// `options=-csearch_path=<schema>,public` into the connection URL (the
    /// `read_only_url` merge in [`Self::read_only_url`] appends the RO flag
    /// to the same `options=` value). The schema must already exist — the
    /// caller (`init_db` / `init_db_readonly`) creates + migrates it.
    pub fn with_schema(mut self, schema: &str) -> Self {
        self.schema = Some(schema.to_string());
        self.pg_url = inject_search_path(&self.pg_url, schema);
        self
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

    /// The connection URL with the password masked (safe for logs / status).
    pub fn redacted_url(&self) -> String {
        redact_url(&self.pg_url)
    }

    /// URL with `default_transaction_read_only = on` injected via the
    /// `options` libpq param. If the URL already carries an `options=`
    /// param (e.g. a per-project `search_path` from [`Self::with_schema`]),
    /// the RO flag is appended space-separated to that same param — libpq
    /// splits `-c` flags on spaces (verified against PG 18); a second
    /// `options=` param would be dropped. For a read-write backend this is
    /// the plain URL (no GUC).
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

    /// Execute a script (cozo dialect → SQL via the translator) and return
    /// named rows. Mirrors the historical 2-arg `run_script` convention
    /// (`serde_json::Value` params). Phase 5.5 regression finding: the
    /// `postgres` sync client spins up its own tokio runtime internally, so
    /// calling it from inside a tokio runtime (the MCP server's async tool
    /// dispatch) panics with "Cannot start a runtime from within a runtime".
    /// `block_in_place` yields the worker thread and lets the blocking
    /// client run; on non-runtime threads (CLI, `leankg migrate`, sync
    /// tests) it is a no-op.
    pub fn run_script(
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

    /// Mutability classification, kept for callers that branch on read vs
    /// write (e.g. RO pools, write-tracking).
    pub fn mutability_for(&self, query: &str) -> mutability::ScriptMutability {
        mutability::mutability_for(query)
    }

    /// Bulk-load named rows into a relation via batched `COPY`/upsert
    /// (Phase 3 replaced cozo's `import_relations`).
    pub fn import_relations(
        &self,
        data: BTreeMap<String, NamedRows>,
    ) -> Result<(), Box<dyn std::error::Error>> {
        if tokio::runtime::Handle::try_current().is_ok() {
            tokio::task::block_in_place(|| self.import_relations_sync(data))
        } else {
            self.import_relations_sync(data)
        }
    }

    /// The sync body behind [`Self::run_script`]. Must only run off a
    /// tokio runtime (see the `block_in_place` guard).
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
                return Ok(NamedRows::new(head, Vec::new()));
            }
            // `:put query_cache ...` / `:delete query_cache ...` /
            // `:delete query_cache where ...` → no-op write.
            return Ok(NamedRows::new(Vec::new(), Vec::new()));
        }
        // T6.1: a read-only backend never touches the RW pool — writes are
        // rejected by Postgres itself (`default_transaction_read_only = on`).
        let mut client = if self.read_only {
            self.checkout_read_only()?
        } else {
            self.checkout()?
        };

        let named_params_log = if pg_sql_log_enabled() {
            format_named_params_for_log(&params)
        } else {
            String::new()
        };
        let t = translate::translate(query, params).map_err(|e| -> Box<dyn std::error::Error> {
            Box::new(std::io::Error::other(format!(
                "translate({}): {e}",
                &query[..query.len().min(60)]
            )))
        })?;

        let started = std::time::Instant::now();
        log_pg_run_script(
            "start",
            t.kind,
            query,
            &t.sql,
            &named_params_log,
            t.params.len(),
            &t.gucs,
            None,
            None,
        );

        let mut head = t.head.clone();
        let mut rows: Vec<Vec<DataValue>> = Vec::new();
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
        log_pg_run_script(
            "done",
            t.kind,
            query,
            &t.sql,
            &named_params_log,
            t.params.len(),
            &t.gucs,
            Some(rows.len()),
            Some(started.elapsed().as_millis()),
        );
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
            // The legacy cozo name for the vector column is `vector`; the PG
            // schema uses `vec`. Map before emitting SQL / binding values.
            let is_vectors_table =
                table == "embedding_vectors" || table.starts_with("embedding_vectors_");
            let cols: Vec<String> = cols
                .into_iter()
                .map(|c| {
                    if is_vectors_table && c == "vector" {
                        "vec".to_string()
                    } else {
                        c
                    }
                })
                .collect();
            // Keyed tables (single PK) get the COPY + ON CONFLICT path;
            // non-keyed tables (code_elements, relationships, ...) fall back
            // to multi-row INSERT (they cannot dedupe via a PK).
            let is_state_table =
                table == "embedding_state" || table.starts_with("embedding_state_");
            let pk_col = match table.as_str() {
                _ if is_vectors_table || is_state_table => Some("qualified_name"),
                "index_inventory" => Some("key"),
                "index_hashes" => Some("path"),
                "migrations" => Some("id"),
                _ => None,
            };
            match pk_col {
                Some(pk) if use_copy_path(&table) => {
                    self.copy_upsert(&mut tx, &table, &cols, pk, &named)?;
                }
                Some(pk) => {
                    self.upsert_values(&mut tx, &table, &cols, pk, &named)?;
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
    /// the legacy `import_relations` callers rely on (upsert_fresh, vectors).
    ///
    /// The temp table is `CREATE TEMP TABLE ... ON COMMIT DROP`, so it is
    /// scoped to this transaction and vanishes on commit — no schema pollution.
    fn copy_upsert(
        &self,
        tx: &mut postgres::Transaction,
        table: &str,
        cols: &[String],
        pk: &str,
        named: &NamedRows,
    ) -> Result<(), Box<dyn std::error::Error>> {
        use std::io::Write;

        if named.rows.is_empty() {
            return Ok(());
        }
        // Dedupe by PK before COPY: `INSERT ... SELECT ... ON CONFLICT (pk)
        // DO UPDATE` fails with "ON CONFLICT DO UPDATE command cannot affect
        // row a second time" when the staging batch contains the same PK
        // twice (real graphs have duplicate qualified_names across files —
        // see plan §9 qualified_name-collision finding). Keep the last row
        // per PK, matching last-write-wins ON CONFLICT semantics.
        let pk_idx = cols.iter().position(|c| c.as_str() == pk);
        let rows: Vec<&Vec<DataValue>> = if let Some(idx) = pk_idx {
            let mut seen: std::collections::HashMap<String, &Vec<DataValue>> =
                std::collections::HashMap::with_capacity(named.rows.len());
            for row in &named.rows {
                seen.insert(row.get(idx).map(|v| v.to_string()).unwrap_or_default(), row);
            }
            seen.into_values().collect()
        } else {
            named.rows.iter().collect()
        };
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
        log_pg_import(
            table,
            "copy_upsert",
            cols,
            rows.len(),
            &format!(
                "CREATE TEMP TABLE {staging} (LIKE {q_table}) ON COMMIT DROP; \
                 {copy_sql}; \
                 INSERT INTO {q_table} ({q_cols}) SELECT {q_cols} FROM {staging} \
                 ON CONFLICT ({}) DO UPDATE SET …",
                crate::db::pg::translate::quote_ident(pk)
            ),
        );
        let mut writer = tx.copy_in(&copy_sql)?;
        for row in rows {
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
        named: &NamedRows,
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

    /// Multi-row VALUES upsert for keyed tables — the default bulk path
    /// (COPY FROM STDIN is unsafe through the pgcat pooler, see
    /// `import_relations_sync`). One `INSERT ... VALUES (...),(...) ON
    /// CONFLICT (pk) DO UPDATE` per chunk, parameterized; mirrors the SQL
    /// shape the `:put` translator (`translate::build_insert`) emits.
    ///
    /// Rows are deduped by PK first — duplicate PKs in one VALUES list fail
    /// with "ON CONFLICT DO UPDATE command cannot affect row a second time"
    /// (the same reason `copy_upsert` dedupes before COPY). Chunks are
    /// bounded by a param budget (PG's 65535 limit) and the ~5k-row ceiling
    /// proven for multi-row statements through the pooler.
    fn upsert_values(
        &self,
        tx: &mut postgres::Transaction,
        table: &str,
        cols: &[String],
        pk: &str,
        named: &NamedRows,
    ) -> Result<(), Box<dyn std::error::Error>> {
        if named.rows.is_empty() {
            return Ok(());
        }
        // Dedupe by PK, last row wins (matches ON CONFLICT semantics).
        let pk_idx = cols.iter().position(|c| c.as_str() == pk);
        let rows: Vec<&Vec<DataValue>> = if let Some(idx) = pk_idx {
            let mut seen: std::collections::HashMap<String, &Vec<DataValue>> =
                std::collections::HashMap::with_capacity(named.rows.len());
            for row in &named.rows {
                seen.insert(row.get(idx).map(|v| v.to_string()).unwrap_or_default(), row);
            }
            seen.into_values().collect()
        } else {
            named.rows.iter().collect()
        };
        let col_sql = cols
            .iter()
            .map(|c| crate::db::pg::translate::quote_ident(c))
            .collect::<Vec<_>>()
            .join(", ");
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
        let max_batch = (60_000usize / cols.len().max(1)).clamp(1, 5000);
        for chunk in rows.chunks(max_batch) {
            let mut values_sql = String::new();
            let mut params: Vec<Box<dyn postgres::types::ToSql + Sync + Send>> = Vec::new();
            for (i, row) in chunk.iter().enumerate() {
                if i > 0 {
                    values_sql.push_str(", ");
                }
                values_sql.push('(');
                for (j, val) in row.iter().enumerate() {
                    if j > 0 {
                        values_sql.push_str(", ");
                    }
                    // pgvector column: the text literal `[0.1,0.2,...]` needs
                    // the explicit cast (PG has no implicit text -> vector),
                    // same as the `:put` translator.
                    if cols[j] == "vec" || cols[j] == "vector" {
                        values_sql.push_str(&format!("${}::text::vector", params.len() + 1));
                    } else {
                        values_sql.push_str(&format!("${}", params.len() + 1));
                    }
                    params.push(cozo_to_pg(val, &cols[j]));
                }
                values_sql.push(')');
            }
            let param_refs: Vec<&(dyn postgres::types::ToSql + Sync)> = params
                .iter()
                .map(|p| p.as_ref() as &(dyn postgres::types::ToSql + Sync))
                .collect();
            let sql = format!(
                "INSERT INTO {table} ({col_sql}) VALUES {values_sql} \
                 ON CONFLICT ({q_pk}) DO UPDATE SET {update_set}"
            );
            log_pg_import(table, "upsert_values", cols, chunk.len(), &sql);
            tx.execute(&sql, &param_refs)?;
        }
        Ok(())
    }
}

impl DbBackend for PostgresBackend {
    fn is_read_only(&self) -> bool {
        self.read_only
    }

    fn run_script(
        &self,
        query: &str,
        params: BTreeMap<String, serde_json::Value>,
    ) -> Result<NamedRows, Box<dyn std::error::Error>> {
        PostgresBackend::run_script(self, query, params)
    }

    fn submit_write(
        &self,
        query: &str,
        params: BTreeMap<String, serde_json::Value>,
        priority: crate::db::write_bus::Priority,
    ) -> Result<NamedRows, Box<dyn std::error::Error>> {
        // Route through the priority bus when attached. The bus worker
        // already runs in a tokio task; `block_in_place` on the sync
        // `postgres::Client` inside the closure is safe.
        match &self.write_bus {
            Some(bus) => {
                let query = query.to_string();
                let me = self.clone();
                let params_clone = params.clone();
                bus.submit(crate::db::write_bus::WriteJob {
                    priority,
                    kind: "tool_write",
                    run: Box::new(move || {
                        me.run_script(&query, params_clone)
                            .map(|_| ())
                            .map_err(|e| e.to_string())
                    }),
                })
                .map_err(|e| -> Box<dyn std::error::Error> { e.into() })?;
                // Writes are fire-and-forget on the bus; the caller cannot
                // observe per-row results. Reads still go through run_script.
                Ok(NamedRows::new(Vec::new(), Vec::new()))
            }
            None => self.run_script(query, params),
        }
    }

    fn import_relations(
        &self,
        data: BTreeMap<String, NamedRows>,
    ) -> Result<(), Box<dyn std::error::Error>> {
        PostgresBackend::import_relations(self, data)
    }

    fn submit_import(
        &self,
        data: BTreeMap<String, NamedRows>,
        priority: crate::db::write_bus::Priority,
    ) -> Result<(), Box<dyn std::error::Error>> {
        match &self.write_bus {
            Some(bus) => {
                let me = self.clone();
                let kind = match priority {
                    crate::db::write_bus::Priority::EmbedWrite => "embed_import",
                    crate::db::write_bus::Priority::ToolWrite => "tool_import",
                };
                bus.submit(crate::db::write_bus::WriteJob {
                    priority,
                    kind,
                    run: Box::new(move || me.import_relations(data).map_err(|e| e.to_string())),
                })
                .map_err(|e| -> Box<dyn std::error::Error> { e.into() })?;
                Ok(())
            }
            None => self.import_relations(data),
        }
    }

    fn redacted_url(&self) -> String {
        PostgresBackend::redacted_url(self)
    }

    fn mutability_for(&self, query: &str) -> mutability::ScriptMutability {
        PostgresBackend::mutability_for(self, query)
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

/// Whether a table routes through the COPY staging path (vs the multi-row
/// VALUES upsert). EXACT `embedding_vectors` only: per-model embed tables
/// (`embedding_vectors_<model_id>`) and all `embedding_state*` tables are
/// written through the pgcat pooler mid-embed, where COPY FROM STDIN
/// deadlocks (the long-lived statement reads as "idle" to the pooler, which
/// idle-timeouts and kills the socket mid-CopyIn). They fall through to
/// `upsert_values`.
fn use_copy_path(table: &str) -> bool {
    bulk_copy_enabled() && table == "embedding_vectors"
}

/// Whether the `leankg::pg_sql` tracing target is enabled (`LEANKG_PG_SQL_LOG`).
/// Emits SQL + params at `tracing::info!` for every run_script / import. Off
/// by default to avoid log spam on hot paths.
pub(crate) fn pg_sql_log_enabled() -> bool {
    std::env::var("LEANKG_PG_SQL_LOG")
        .ok()
        .map(|v| {
            let v = v.trim().to_ascii_lowercase();
            matches!(v.as_str(), "1" | "true" | "on")
        })
        .unwrap_or(false)
}

/// Render a JSON value for the SQL log, truncating to `limit` chars.
fn truncate_json_for_log(v: &serde_json::Value, limit: usize) -> String {
    let s = v.to_string();
    if s.len() <= limit {
        return s;
    }
    format!("{}…(+{} bytes)", &s[..limit], s.len() - limit)
}

/// Render named params for the SQL log: `{k=v, ...}` with values truncated.
fn format_named_params_for_log(params: &BTreeMap<String, serde_json::Value>) -> String {
    if params.is_empty() {
        return "{}".to_string();
    }
    let mut parts: Vec<String> = Vec::with_capacity(params.len());
    for (k, v) in params {
        parts.push(format!("{k}={}", truncate_json_for_log(v, 240)));
    }
    format!("{{{}}}", parts.join(", "))
}

/// Log a run_script execution (start or done) on the `leankg::pg_sql` target.
#[allow(clippy::too_many_arguments)]
fn log_pg_run_script(
    phase: &str,
    kind: crate::db::pg::translate::TranslationKind,
    cozo: &str,
    sql: &str,
    named_params: &str,
    bound_param_count: usize,
    gucs: &[(String, String)],
    rows: Option<usize>,
    elapsed_ms: Option<u128>,
) {
    tracing::info!(
        target: "leankg::pg_sql",
        phase,
        kind = ?kind,
        bound_params = bound_param_count,
        rows,
        elapsed_ms,
        gucs = ?gucs,
        named_params,
        cozo,
        sql,
        "pg run_script"
    );
}

/// Log an import_relations write on the `leankg::pg_sql` target.
#[allow(clippy::too_many_arguments)]
fn log_pg_import(table: &str, path: &str, cols: &[String], rows: usize, sql: &str) {
    tracing::info!(
        target: "leankg::pg_sql",
        table,
        path,
        rows,
        cols = %cols.join(","),
        sql = %sql,
        "pg import_relations"
    );
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
fn data_to_copy_text(v: &DataValue, col: &str) -> String {
    match v {
        DataValue::Null => String::new(), // COPY: empty field == NULL
        DataValue::Bool(b) => b.to_string(),
        DataValue::Num(crate::db::value::Num::Int(i)) => i.to_string(),
        DataValue::Num(crate::db::value::Num::Float(f)) => f.to_string(),
        DataValue::Str(s) => s.as_str().to_string(),
        DataValue::Json(j) => j.clone(),
        // The legacy cozo name for the vector column is `vector`; the PG
        // schema uses `vec`.
        DataValue::List(items) if col == "vec" || col == "vector" => {
            let mut s = String::from("[");
            for (i, item) in items.iter().enumerate() {
                if i > 0 {
                    s.push(',');
                }
                match item {
                    DataValue::Num(crate::db::value::Num::Float(f)) => s.push_str(&format!("{f}")),
                    DataValue::Num(crate::db::value::Num::Int(i)) => s.push_str(&format!("{i}")),
                    other => s.push_str(&format!("{other}")),
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

/// Convert a `DataValue` into a boxed `dyn ToSql` for binding. Vector
/// values are emitted as pgvector text literals (e.g. `[0.1, 0.2]`).
fn cozo_to_pg(v: &DataValue, col: &str) -> Box<dyn postgres::types::ToSql + Sync + Send> {
    match v {
        DataValue::Null => Box::new(Option::<String>::None),
        DataValue::Bool(b) => Box::new(*b),
        DataValue::Num(crate::db::value::Num::Int(i)) => Box::new(*i),
        DataValue::Num(crate::db::value::Num::Float(f)) => Box::new(*f),
        DataValue::Str(s) => Box::new(s.clone()),
        DataValue::Json(j) => Box::new(j.clone()),
        // The caller's NamedRows headers use the legacy cozo name (`vector`);
        // the PG column is `vec` (schema.sql). Match both.
        DataValue::List(items) if col == "vec" || col == "vector" => {
            // pgvector literal: `[0.1,0.2,...]`.
            let mut s = String::from("[");
            for (i, item) in items.iter().enumerate() {
                if i > 0 {
                    s.push(',');
                }
                match item {
                    DataValue::Num(crate::db::value::Num::Float(f)) => s.push_str(&format!("{f}")),
                    DataValue::Num(crate::db::value::Num::Int(i)) => s.push_str(&format!("{i}")),
                    other => s.push_str(&format!("{other}")),
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
/// Inject `options=-csearch_path=<schema>,public` into a PG connection URL.
/// Appends to an existing `options=` param (space-separated `-c` flags) or
/// adds a new one — libpq splits multiple `-c` GUCs on spaces.
fn inject_search_path(url: &str, schema: &str) -> String {
    const SP_FLAG: &str = "-csearch_path%3D";
    // `,public` appended so unqualified references to shared tables still
    // resolve; matches `test_schema_url` and the doc comment above. The comma
    // is `%2C` (URL-safe) so a raw `,` never breaks the query string.
    let encoded = format!("{}%2Cpublic", percent_encode(schema));
    let base = url;
    let sp_flag = format!("{SP_FLAG}{encoded}");
    // Already carrying a search_path — replace it to avoid duplicate GUCs.
    if let Some(pos) = base.find("search_path") {
        let start = base[..pos].rfind('=').unwrap_or(pos);
        let end = base[pos..]
            .find(|c: char| ['&', '%'].contains(&c))
            .map(|i| pos + i)
            .unwrap_or(base.len());
        return format!("{}{}{}", &base[..start + 1], sp_flag, &base[end..]);
    }
    if let Some(pos) = base.find("options=") {
        let after = &base[pos + "options=".len()..];
        let end = after.find('&').unwrap_or(after.len());
        let value = &after[..end];
        let rest = &after[end..];
        return format!(
            "{}{}%20{}{}",
            &base[..pos + "options=".len()],
            value,
            sp_flag,
            rest
        );
    }
    // `split_once('?')` removes the `?` from `before`, so it must be
    // re-added before appending `options=`. When the URL already carries a
    // query string, the new param joins with `&`; when bare, it starts with
    // `?`. (The old code used `&` for the non-empty case, which merged the
    // db name with the query — `defaultdb&sslmode=...` — and broke remote
    // TLS URLs like `?sslmode=require`.)
    let (before, after) = base.split_once('?').unwrap_or((base, ""));
    if after.is_empty() {
        format!("{before}?options={sp_flag}")
    } else {
        format!("{before}?{after}&options={sp_flag}")
    }
}

/// Percent-encode a schema name for a libpq `options=-csearch_path=<v>`
/// param: `%` → `%25`, `/` → `%2F`, space → `%20`.
fn percent_encode(s: &str) -> String {
    s.replace('%', "%25")
        .replace('/', "%2F")
        .replace(' ', "%20")
}

/// Derive a stable, valid Postgres schema identifier for a project.
///
/// The key must be the **same for reader and writer even when they see the
/// project at different mount paths** (reader `/app/.leankg` vs writer
/// `/Users/.../.leankg`). We use the project's `project_path` from its
/// `leankg.yaml` when present (a host-absolute path, identical from every
/// mount); otherwise fall back to `db_path` itself. The result is
/// `leankg_p_` + hex bytes (short paths) or a 16-hex SipHash (long paths) —
/// always a valid, lowercase PG identifier (≤ 63 bytes).
pub fn schema_for_path(db_path: &std::path::Path) -> String {
    let cwd = std::env::current_dir().unwrap_or_else(|_| std::path::PathBuf::from("."));
    schema_for_path_in(db_path, &cwd)
}

/// [`schema_for_path`] with an explicit base for RELATIVE db_path spellings
/// (`leankg index ./src` resolves `./src` against the invocation CWD).
/// Pure — tests pass a base instead of mutating the process CWD.
#[doc(hidden)]
pub fn schema_for_path_in(db_path: &std::path::Path, base: &std::path::Path) -> String {
    let key = project_identity_key_in(db_path, base);
    use std::fmt::Write;
    let mut hex = String::with_capacity(key.len() * 2);
    for b in key.as_bytes() {
        let _ = write!(hex, "{:02x}", b);
    }
    // Cap length: PG identifiers are ≤ 63 bytes. Hash long paths.
    const MAX: usize = 32;
    if hex.len() > MAX {
        use std::hash::{DefaultHasher, Hasher};
        let mut h = DefaultHasher::new();
        h.write(key.as_bytes());
        hex = format!("{:016x}", h.finish());
    }
    format!("leankg_p_{hex}")
}

/// Canonicalize a project path for identity-key derivation.
///
/// The ONE canonicalization used everywhere a project key is derived (CLI
/// index/embed and MCP server alike). Existing paths are resolved through
/// `std::fs::canonicalize` so symlinks, `.`/`..` components, relative
/// spellings and trailing slashes all collapse to one physical directory.
/// Non-existent paths fall back to lexical normalization (joined against
/// the process CWD, dot-components dropped) — needed before first init and
/// for the cross-mount `project_path` contract in leankg.yaml.
pub fn canonical_project_root(path: &std::path::Path) -> std::path::PathBuf {
    let cwd = std::env::current_dir().unwrap_or_else(|_| std::path::PathBuf::from("."));
    canonical_project_root_in(path, &cwd)
}

/// [`canonical_project_root`] against an explicit base directory for
/// relative spellings — pure, so tests don't have to mutate the process CWD.
pub fn canonical_project_root_in(
    path: &std::path::Path,
    base: &std::path::Path,
) -> std::path::PathBuf {
    if let Ok(canon) = std::fs::canonicalize(path) {
        return canon;
    }
    let absolute = if path.is_absolute() {
        path.to_path_buf()
    } else {
        base.join(path)
    };
    absolute
        .components()
        .filter(|c| !matches!(c, std::path::Component::CurDir))
        .collect()
}

/// Resolve the schema key for a project: prefer `project_path` from the
/// project's `leankg.yaml` (stable across mounts), else the `.leankg` dir
/// path itself. `db_path` may be a `.leankg` dir or a project root.
fn project_identity_key_in(db_path: &std::path::Path, base: &std::path::Path) -> String {
    // db_path is either `<root>/.leankg` (reader/MCP) or
    // `<root>/.leankg/leankg.db` (writer index/embed). Normalize BOTH the
    // suffix stripping and the resulting root through ONE canonicalization
    // so reader and writer derive the SAME schema regardless of how the
    // path was spelled (R1 sweep issue #5: CLI `index ./src` keyed the
    // literal string while MCP `--project <abs>` hashed the real path).
    let normalized = canonical_project_root_in(db_path, base);
    let mut root = normalized;
    // Strip a trailing `<root>/.leankg/leankg.db` → `<root>/.leankg`.
    if root.ends_with("leankg.db") {
        root = root.parent().unwrap_or(&root).to_path_buf();
    }
    // Strip a trailing `<root>/.leankg` → `<root>` (re-canonicalize in case
    // the suffix strip exposed a symlinked ancestor).
    if root.ends_with(".leankg") {
        root = root.parent().unwrap_or(&root).to_path_buf();
    }
    let root = canonical_project_root(&root);
    if let Ok(content) = std::fs::read_to_string(root.join("leankg.yaml")) {
        if let Ok(config) = serde_yaml::from_str::<crate::config::ProjectConfig>(&content) {
            if let Some(pp) = config.project.project_path {
                // Resolve a relative project_path against THIS project root
                // (`project_path: "./src"` means `<root>/src`, identical to
                // what `leankg index ./src` inside <root> keys on), then use
                // the same canonicalization. Unresolvable paths keep their
                // lexical form — the cross-mount identity contract.
                let joined = if pp.is_relative() { root.join(pp) } else { pp };
                return canonical_project_root(&joined)
                    .to_string_lossy()
                    .to_string();
            }
        }
    }
    // No yaml/project_path: key on the normalized project root path.
    root.to_string_lossy().to_string()
}

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

/// Open the Postgres backend from `LEANKG_PG_URL`. Fails loudly when the
/// env var is missing or malformed — Postgres is the only engine (D4), so
/// there is no fallback.
///
/// Open a PostgreSQL backend for a project path. Multi-project (Phase 8 D4):
/// the schema is derived from `db_path` and **created + migrated if missing**
/// (writer path — `leankg index` / `leankg embed`). The returned backend is
/// pinned to that schema so every write lands in the right project's tables.
///
/// Under `#[cfg(test)]` the `db_path` is used to select a per-path scratch
/// schema (see [`test_scratch_schema`]): unit tests call `init_db` with a
/// temp path and get a real, isolated Postgres schema in the dev container
/// instead of the pre-migration sqlite shim.
pub fn init_db(db_path: &std::path::Path) -> Result<SharedDb, Box<dyn std::error::Error>> {
    #[cfg(test)]
    {
        return test_init_db(db_path);
    }
    #[allow(unreachable_code)]
    {
        let schema = schema_for_path(db_path);
        // Writer path: ALWAYS create + pin to the per-project schema. The
        // writer owns schema creation; it never falls back to `public` (that
        // fallback is reader-only, so a pre-schema index stays visible).
        let ext_schema = create_schema_if_missing(&schema)?;
        let mut pg = PostgresBackend::from_env()?.with_schema(&schema);
        // If the pgvector extension lives in a schema other than {schema} or
        // public, add it to the runtime search_path so `vector(N)` resolves.
        if let Some(ref ext_s) = ext_schema {
            if ext_s != &schema && ext_s != "public" {
                let sep = if pg.pg_url.contains('?') { '&' } else { '?' };
                pg.pg_url = format!(
                    "{}{sep}options=-csearch_path%3D{schema}%2C{ext_s}%2Cpublic",
                    pg.pg_url
                );
            }
        }
        tracing::info!("DB engine = postgres: {}", redact_url(&pg.pg_url));
        Ok(Arc::new(pg))
    }
}

/// Create + migrate a per-project schema if it doesn't exist yet. Safe to
/// call on every writer init — `CREATE SCHEMA IF NOT EXISTS` + idempotent
/// migrations make it a cheap no-op on warm paths. May be called from async
/// contexts (embed `--wait` on a tokio runtime), so the sync PG body runs
/// under the same `block_in_place` guard as `run_script`.
pub fn create_schema_if_missing(
    schema: &str,
) -> Result<Option<String>, Box<dyn std::error::Error>> {
    if tokio::runtime::Handle::try_current().is_ok() {
        tokio::task::block_in_place(|| create_schema_if_missing_sync(schema))
    } else {
        create_schema_if_missing_sync(schema)
    }
}

/// Whether a per-project schema is **populated** (has at least one
/// `code_elements` row). Used to fall back to `public` when a per-project
/// schema exists but is empty (e.g. a re-index that created tables but wrote
/// no elements), so existing shared-layout local indexes stay queryable.
fn schema_exists(schema: &str) -> bool {
    if tokio::runtime::Handle::try_current().is_ok() {
        tokio::task::block_in_place(|| schema_exists_sync(schema))
    } else {
        schema_exists_sync(schema)
    }
}

fn schema_exists_sync(schema: &str) -> bool {
    let base = match PostgresBackend::from_env() {
        Ok(pg) => pg.pg_url,
        Err(_) => return false,
    };
    let Ok(mut client) = pg_connect(&base) else {
        return false;
    };
    // Schema names come from schema_for_path (hex/hash, always a safe
    // identifier), so qualifying the table directly is injection-safe.
    let q = format!("SELECT EXISTS (SELECT 1 FROM {schema}.code_elements LIMIT 1)");
    client
        .query_one(&q, &[])
        .map(|row| row.get(0))
        .unwrap_or(false)
}

fn create_schema_if_missing_sync(
    schema: &str,
) -> Result<Option<String>, Box<dyn std::error::Error>> {
    let base = PostgresBackend::from_env()?.pg_url;
    let mut client = pg_connect(&base)?;
    client.batch_execute(&format!(
        "CREATE SCHEMA IF NOT EXISTS {schema}; SET search_path TO {schema}, public"
    ))?;
    // Ensure the pgvector extension is available in this schema's search_path.
    // If vector was installed in a different schema by a prior project, the
    // CREATE EXTENSION IF NOT EXISTS in schema.sql is a no-op and the type
    // becomes invisible. Fix: find the extension's schema and prepend it.
    let ext_schema: Option<String> = client
        .query_one(
            "SELECT n.nspname FROM pg_extension e \
             JOIN pg_namespace n ON e.extnamespace = n.oid \
             WHERE e.extname = 'vector'",
            &[],
        )
        .ok()
        .and_then(|row| row.get(0));
    if let Some(ref ext_s) = ext_schema {
        if ext_s != schema && ext_s != "public" {
            client.batch_execute(&format!("SET search_path TO {schema}, {ext_s}, public"))?;
        }
    }
    crate::db::pg::migrations::run_migrations(&mut client)?;
    // FR-EMBED-DIM: a model/dim switch (LEANKG_EMBED_DIM / LEANKG_EMBED_API_DIM)
    // reconciles the pgvector column width here — wiping derived vectors +
    // freshness rows — so the next `embed` rebuilds at the new width.
    if let Some((stored, desired)) = crate::db::pg::migrations::reconcile_vector_dim(&mut client)
        .map_err(|e| -> Box<dyn std::error::Error> { e.into() })?
    {
        tracing::warn!(
            "embedding vector dim changed {stored} -> {desired}; vector store wiped (re-embed required)"
        );
    }
    // Restore clean search_path for the caller, still including ext_schema if needed.
    if let Some(ref ext_s) = ext_schema {
        if ext_s != schema && ext_s != "public" {
            client.batch_execute(&format!("SET search_path TO {schema}, {ext_s}, public"))?;
        } else {
            client.batch_execute(&format!("SET search_path TO {schema}, public"))?;
        }
    } else {
        client.batch_execute(&format!("SET search_path TO {schema}, public"))?;
    }
    Ok(ext_schema)
}

#[cfg(test)]
fn test_init_db(db_path: &std::path::Path) -> Result<SharedDb, Box<dyn std::error::Error>> {
    Ok(Arc::new(crate::db::fake::FakeBackend::for_path(db_path)))
}

/// The dev-Postgres URL used by unit tests when `LEANKG_PG_URL` is unset.
/// Matches the container-gated integration tests' default (`leankg-pg-phase0`
/// on :5433). Override with `LEANKG_PG_URL` for a different instance.
#[cfg(test)]
pub(crate) fn test_pg_url() -> String {
    std::env::var("LEANKG_PG_URL")
        .unwrap_or_else(|_| "postgresql://postgres:postgres@localhost:5433/leankg".to_string())
}

/// Whether the local dev Postgres is reachable. Live-PG unit tests call
/// `init_db_pg()` behind this probe and skip (not fail) when it's down —
/// plain `cargo test` must stay green without a running database.
#[cfg(test)]
pub(crate) fn test_pg_available() -> bool {
    let Ok(mut client) = pg_connect(&test_pg_url()) else {
        return false;
    };
    client.batch_execute("SELECT 1").is_ok()
}

#[cfg(test)]
fn test_schema_url(schema: &str) -> Result<String, Box<dyn std::error::Error>> {
    let base = test_pg_url();
    let sep = if base.contains('?') { '&' } else { '?' };
    Ok(format!(
        "{base}{sep}options=-csearch_path%3D{schema}%2Cpublic"
    ))
}

/// Test-only: map a temp `db_path` to a unique scratch schema in the dev
/// Postgres, run migrations on first use, and return the schema name. A
/// `static Mutex<HashMap>` keeps the mapping process-stable so a test that
/// calls `init_db(path)` twice (e.g. seed + readonly) reuses the schema.
#[cfg(test)]
fn test_scratch_schema(db_path: &std::path::Path) -> Result<String, Box<dyn std::error::Error>> {
    use std::collections::HashMap;
    use std::sync::Mutex as StdMutex;
    use std::sync::OnceLock;

    static MAP: OnceLock<StdMutex<HashMap<std::path::PathBuf, String>>> = OnceLock::new();
    let map = MAP.get_or_init(|| StdMutex::new(HashMap::new()));
    let mut guard = map.lock().unwrap_or_else(|e| e.into_inner());

    let key = db_path.to_path_buf();
    if let Some(schema) = guard.get(&key) {
        return Ok(schema.clone());
    }

    let schema = create_scratch_schema()?;
    guard.insert(key, schema.clone());
    Ok(schema)
}

/// Create a fresh schema, run migrations, and drop it on process exit.
#[cfg(test)]
fn create_scratch_schema() -> Result<String, Box<dyn std::error::Error>> {
    static COUNTER: std::sync::atomic::AtomicU32 = std::sync::atomic::AtomicU32::new(0);
    let base = test_pg_url();
    let name = format!(
        "leankg_libtest_{}_{}",
        std::process::id(),
        COUNTER.fetch_add(1, std::sync::atomic::Ordering::Relaxed)
    );
    let mut client = pg_connect(&base)?;
    client.batch_execute(&format!("DROP SCHEMA IF EXISTS {name} CASCADE"))?;
    client.batch_execute(&format!("CREATE SCHEMA {name}"))?;
    client.batch_execute(&format!("SET search_path TO {name}, public"))?;
    crate::db::pg::migrations::run_migrations(&mut client)?;
    // Keep the admin connection alive so the schema is dropped on exit.
    std::mem::forget(client);
    Ok(name)
}

/// Open a read-only backend (T6.1): `default_transaction_read_only = on` —
/// writes fail at the Postgres layer instead of the legacy CozoDB RocksDB
/// same-handle workaround.
///
/// Multi-project: when `db_path` resolves to a `.leankg` dir, the backend is
/// pinned to that project's PG schema ([`schema_for_path`]) so `?project=`
/// queries only its own tables. The schema must already exist (created +
/// migrated by the writer `leankg index`/`leankg embed`); a read-only process
/// never creates schemas.
pub fn init_db_readonly(db_path: &std::path::Path) -> Result<SharedDb, Box<dyn std::error::Error>> {
    #[cfg(test)]
    {
        return test_init_db(db_path);
    }
    #[allow(unreachable_code)]
    {
        let schema = schema_for_path(db_path);
        let pg = if schema_exists(&schema) {
            PostgresBackend::from_env_read_only()?.with_schema(&schema)
        } else {
            // Fall back to `public` (single-shared layout) so existing local
            // indexes stay visible when no per-project schema has been
            // indexed yet.
            PostgresBackend::from_env_read_only()?
        };
        tracing::info!(
            "DB engine = postgres read-only (default_transaction_read_only = on): {}",
            redact_url(&pg.pg_url)
        );
        Ok(Arc::new(pg))
    }
}

/// Open a PostgreSQL backend. Fails when `LEANKG_PG_URL` is missing or
/// malformed. This is the single entry point for every path-based init
/// (CLI, web server, MCP).
pub fn init_db_pg() -> Result<SharedDb, Box<dyn std::error::Error>> {
    let pg = PostgresBackend::from_env()?;
    tracing::info!("DB engine = postgres: {}", redact_url(&pg.pg_url));
    Ok(Arc::new(pg))
}

/// Acquire the index advisory lock for exclusive `leankg index` jobs (T6.4b).
/// Blocks until the lock is free, so a second concurrent `leankg index`
/// waits for the first to finish. The lock lives on a dedicated session, so
/// it also guards against a nested `index_codebase` re-entry (incremental →
/// full fallback) deadlocking itself on a second connection: we return the
/// already-held lock via a process-level registry.
///
/// `LEANKG_PG_LOCK=0` disables the advisory lock (operators who manage
/// exclusivity externally, e.g. a job queue). Default: on.
pub fn index_advisory_lock(
    env: &str,
    path: &str,
) -> Result<Option<AdvisoryLock>, Box<dyn std::error::Error>> {
    // Disable when `LEANKG_PG_LOCK=0` env, or `db.lock: false` in leankg.yaml.
    let env_disables = std::env::var("LEANKG_PG_LOCK")
        .ok()
        .map(|v| v.eq_ignore_ascii_case("0") || v.eq_ignore_ascii_case("false"))
        .unwrap_or(false);
    let yaml_disables = crate::config::db_config_from_cwd()
        .and_then(|db| db.lock)
        .map(|v| !v)
        .unwrap_or(false);
    if env_disables || yaml_disables {
        tracing::info!("PG index advisory lock disabled (LEANKG_PG_LOCK=0 or db.lock: false)");
        return Ok(None);
    }
    // Per-(env,path) key so the same env+path serializes across instances
    // while different projects run concurrently (multi-project PG schemas).
    let canon = std::fs::canonicalize(path)
        .map(|p| p.display().to_string())
        .unwrap_or_else(|_| path.to_string());
    let key = index_lock_key(env, &canon);
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

/// FNV-1a hash over `env`, a salt, and `path` — a stable i64 advisory-lock
/// key that serializes the same project across instances and lets different
/// projects lock independently.
fn index_lock_key(env: &str, path: &str) -> i64 {
    const OFFSET: u64 = 0xcbf2_9ce4_8422_2325;
    const PRIME: u64 = 0x0000_0100_0000_01b3;
    const SALT: &[u8] = b"leank";
    let mut h = OFFSET;
    for b in env.bytes().chain(SALT.iter().copied()).chain(path.bytes()) {
        h ^= u64::from(b);
        h = h.wrapping_mul(PRIME);
    }
    h as i64
}

/// Process-level flag so nested index invocations skip re-acquiring.
static INDEX_LOCK_HELD: std::sync::Mutex<bool> = std::sync::Mutex::new(false);

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    /// Serialize tests that mutate process env (LEANKG_PG_URL /
    /// LEANKG_PG_POOL_SIZE / LEANKG_PG_LOCK) — Rust runs tests in parallel
    /// and env is process-global.
    static ENV_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

    // Pool self-heal for stale/closed idle clients is exercised end-to-end by
    // tests/tmp_pool_isolation.rs against a live PG (LEANKG_PG_URL): before
    // the fix, the second sequential query failed with `connection closed`;
    // after it, `count_elements` returns the real 804k. The `postgres::Client`
    // API (`close(self)` consumes) makes a focused unit test awkward, so the
    // integration test is the coverage.

    #[test]
    fn postgres_backend_stub_returns_documented_error() {
        // when the URL is bogus, run_script fails at connection time
        // rather than silently panicking.
        let pg = PostgresBackend {
            pg_url: "postgres://invalid-host-not-real:1/leankg".into(),
            schema: None,
            pool: std::sync::Arc::new(ClientPool::new(1)),
            ro_pool: std::sync::Arc::new(ClientPool::new(1)),
            read_only: false,
            write_bus: None,
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
        // No env + no db: block -> built-in dev default (not an error).
        std::env::remove_var("LEANKG_PG_URL");
        let ok = PostgresBackend::from_env().expect("no env -> dev default");
        assert!(ok.pg_url.starts_with("postgresql://"));
        std::env::set_var("LEANKG_PG_URL", "not-a-url");
        let err = PostgresBackend::from_env().unwrap_err();
        assert!(err.contains("not-a-url"));
        let redacted = redact_url("postgres://user:s3cret@host:5432/db?sslmode=require");
        assert!(!redacted.contains("s3cret"));
        assert!(redacted.contains("postgres://user:****@host:5432/db?sslmode=require"));
        std::env::remove_var("LEANKG_PG_URL");
    }

    #[test]
    fn url_wants_verified_tls_folds_verify_modes() {
        // verify-full / verify-ca -> TLS required (folded to require by
        // pg_connect); everything else stays plain / require / prefer.
        assert!(url_wants_verified_tls(
            "postgres://u:p@host:5432/db?sslmode=verify-full"
        ));
        assert!(url_wants_verified_tls(
            "postgres://u:p@host:5432/db?sslmode=verify-ca&connect_timeout=5"
        ));
        assert!(!url_wants_verified_tls(
            "postgres://u:p@host:5432/db?sslmode=require"
        ));
        assert!(!url_wants_verified_tls(
            "postgres://u:p@host:5432/db?sslmode=prefer"
        ));
        assert!(!url_wants_verified_tls(
            "postgres://u:p@host:5432/db?sslmode=disable"
        ));
        assert!(!url_wants_verified_tls("postgres://u:p@host:5432/db"));
        // Not an exact `sslmode=` value — must not match.
        assert!(!url_wants_verified_tls(
            "postgres://u:p@host:5432/db?sslmode=verify_ca"
        ));
    }

    #[test]
    fn normalize_pg_url_for_parse_folds_verify_to_require() {
        assert_eq!(
            normalize_pg_url_for_parse("postgres://u:p@h:5432/db?sslmode=verify-full"),
            "postgres://u:p@h:5432/db?sslmode=require"
        );
        assert_eq!(
            normalize_pg_url_for_parse(
                "postgres://u:p@h:5432/db?sslmode=verify-ca&connect_timeout=5"
            ),
            "postgres://u:p@h:5432/db?sslmode=require&connect_timeout=5"
        );
        // Unchanged when already parseable or no query string.
        assert_eq!(
            normalize_pg_url_for_parse("postgres://u:p@h:5432/db?sslmode=require"),
            "postgres://u:p@h:5432/db?sslmode=require"
        );
        assert_eq!(
            normalize_pg_url_for_parse("postgres://u:p@h:5432/db"),
            "postgres://u:p@h:5432/db"
        );
        // The folded URL must parse cleanly (this used to error with
        // InvalidValue("sslmode")).
        let cfg: postgres::Config =
            normalize_pg_url_for_parse("postgres://u:p@h:5432/db?sslmode=verify-full")
                .parse()
                .expect("folded URL parses");
        assert_eq!(cfg.get_ssl_mode(), postgres::config::SslMode::Require);
    }

    #[test]
    fn postgres_backend_requires_url() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        std::env::remove_var("LEANKG_PG_URL");
        // Production path: no env and no leankg.yaml -> built-in dev default.
        let pg = init_db_pg().expect("built-in default URL applies");
        assert!(pg.redacted_url().starts_with("postgresql://"));
        // Test-mode init falls back to the dev-container default (a real,
        // lazily-connected PostgresBackend is still produced).
        assert!(init_db(std::path::Path::new("/tmp/none.db")).is_ok());
    }

    #[test]
    fn pg_url_from_leankg_yaml_db_block() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        std::env::remove_var("LEANKG_PG_URL");
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(
            dir.path().join("leankg.yaml"),
            "db:\n  url: postgresql://u:p@yaml-host:7777/mydb\n",
        )
        .unwrap();
        let prev = std::env::current_dir().unwrap();
        std::env::set_current_dir(dir.path()).unwrap();
        let pg = PostgresBackend::from_env();
        std::env::set_current_dir(prev).unwrap();
        let pg = pg.expect("yaml db.url applies");
        assert!(pg.pg_url.contains("yaml-host:7777/mydb"));
    }

    #[test]
    fn pg_url_env_wins_over_yaml() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        std::env::set_var("LEANKG_PG_URL", "postgresql://u:p@env-host:1111/envdb");
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(
            dir.path().join("leankg.yaml"),
            "db:\n  url: postgresql://u:p@yaml-host:7777/mydb\n",
        )
        .unwrap();
        let prev = std::env::current_dir().unwrap();
        std::env::set_current_dir(dir.path()).unwrap();
        let pg = PostgresBackend::from_env();
        std::env::set_current_dir(prev).unwrap();
        std::env::remove_var("LEANKG_PG_URL");
        let pg = pg.expect("env URL wins");
        assert!(pg.pg_url.contains("env-host:1111"));
    }

    #[test]
    fn pg_pool_size_from_yaml() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        std::env::remove_var("LEANKG_PG_POOL_SIZE");
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join("leankg.yaml"), "db:\n  pool_size: 12\n").unwrap();
        let prev = std::env::current_dir().unwrap();
        std::env::set_current_dir(dir.path()).unwrap();
        let pg = PostgresBackend::from_env();
        std::env::set_current_dir(prev).unwrap();
        let pg = pg.expect("yaml pool_size applies");
        assert_eq!(pg.pool.max_size(), 12);
    }

    #[test]
    fn init_db_accepts_pg_url() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        std::env::set_var(
            "LEANKG_PG_URL",
            "postgresql://postgres:postgres@localhost:5433/leankg",
        );
        let db = PostgresBackend::from_env().unwrap();
        assert!(db.pg_url.contains("postgresql://"));
        assert!(!db.read_only);
        let ro = db.clone().with_read_only();
        assert!(ro.read_only);
        assert!(ro
            .read_only_url()
            .contains("default_transaction_read_only%3Don"));
        let rw_url = db.read_only_url();
        assert!(!rw_url.contains("default_transaction_read_only%3Don"));
        std::env::remove_var("LEANKG_PG_URL");
    }

    #[test]
    fn init_db_with_url_produces_pg_backend() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        std::env::set_var(
            "LEANKG_PG_URL",
            "postgresql://postgres:postgres@localhost:5433/leankg",
        );
        let db = PostgresBackend::from_env().unwrap();
        // The PG backend rejects bare list literals at translate time (no
        // live Postgres needed — connect is lazy).
        assert!(
            db.run_script("?[a] <- [[1]]", Default::default()).is_err(),
            "path-init must produce the PG backend (translator rejects bare lists)"
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

    #[test]
    fn data_value_roundtrips() {
        // The legacy cozo `DataValue` accessors survive on the new type.
        use crate::db::value::DataValue;
        let v = DataValue::from(42i64);
        assert_eq!(v.get_int(), Some(42));
        let f = DataValue::from(3.5f64);
        assert_eq!(f.get_float(), Some(3.5));
        let s = DataValue::from("hi");
        assert_eq!(s.get_str(), Some("hi"));
        let b = DataValue::Bool(true);
        assert_eq!(b.get_bool(), Some(true));
    }

    // ------------------------------------------------------------------
    // Slice 3 — write_bus=None keeps the current direct path (no
    // regression). With Some(bus), writes route through the bus (Slice 4
    // wires callers). This test exercises the seam on the bus side: the
    // closure submitted from a write site runs on the worker.
    // ------------------------------------------------------------------

    #[tokio::test(flavor = "multi_thread")]
    async fn write_bus_some_routes_writes_through_worker() {
        use crate::db::backend::DbBackend;
        use crate::db::fake::FakeBackend;
        use crate::db::write_bus::{InProcessWriteBus, Priority, WriteBus, WriteJob};
        use std::sync::atomic::{AtomicUsize, Ordering};
        use std::sync::Arc;

        let backend: Arc<dyn DbBackend> = Arc::new(FakeBackend::new());
        let bus = Arc::new(InProcessWriteBus::default());
        let executed = Arc::new(AtomicUsize::new(0));

        // The closure body mirrors what Slice 4 will execute from the MCP
        // tool handlers: translate() + checkout + execute() on the shared
        // backend. For this RED test, the FakeBackend's run_script on a
        // `:put` query is what counts.
        let backend_for_job = backend.clone();
        let executed_for_job = executed.clone();
        bus.submit(WriteJob {
            priority: Priority::ToolWrite,
            kind: "tool_write",
            run: Box::new(move || {
                let q = "?[a] <- [[$a]] :put business_logic { element_qualified: $a }";
                let mut params = std::collections::BTreeMap::new();
                params.insert(
                    "a".to_string(),
                    serde_json::Value::String("src/lib.rs::foo".to_string()),
                );
                backend_for_job
                    .run_script(q, params)
                    .map_err(|e| e.to_string())?;
                executed_for_job.fetch_add(1, Ordering::SeqCst);
                Ok(())
            }),
        })
        .unwrap();

        // Poll until the worker drains.
        let mut tries = 0;
        while executed.load(Ordering::SeqCst) == 0 && tries < 200 {
            tokio::time::sleep(std::time::Duration::from_millis(5)).await;
            tries += 1;
        }
        assert_eq!(
            executed.load(Ordering::SeqCst),
            1,
            "bus-routed write must execute the closure once"
        );
        bus.shutdown();
    }

    // ------------------------------------------------------------------
    // Slice 4 — trait `submit_write` on PostgresBackend routes via the
    // bus when one is attached, and falls back to inline `run_script`
    // when not. We exercise the override path with a dead URL: the bus
    // path is fire-and-forget so the call returns Ok even though the
    // inner closure would fail at connect time. The smoke test confirms
    // the bus is consulted (the override does not short-circuit to
    // inline run_script, which would surface a connection error).
    // ------------------------------------------------------------------

    #[tokio::test(flavor = "multi_thread")]
    async fn submit_write_with_bus_does_not_connect_inline() {
        use crate::db::backend::DbBackend;
        use crate::db::write_bus::{InProcessWriteBus, Priority};
        use std::sync::Arc;

        let mut pg = PostgresBackend {
            pg_url: "postgres://invalid-host-not-real:1/leankg".into(),
            schema: None,
            pool: std::sync::Arc::new(ClientPool::new(1)),
            ro_pool: std::sync::Arc::new(ClientPool::new(1)),
            read_only: false,
            write_bus: None,
        };
        // Same backend with a write bus attached.
        let bus = Arc::new(InProcessWriteBus::default());
        pg.write_bus = Some(bus.clone());
        // Bus path is fire-and-forget → returns Ok without ever touching
        // the network. Inline run_script on the same backend would fail
        // with a connection error. This confirms the override routed via
        // the bus.
        let q = "?[a] <- [[$a]] :put business_logic { element_qualified: $a }";
        let mut params = std::collections::BTreeMap::new();
        params.insert("a".to_string(), serde_json::Value::String("x".into()));
        let res = pg.submit_write(q, params, Priority::ToolWrite);
        assert!(
            res.is_ok(),
            "submit_write with bus must not error on dead URL (bus is async), got {:?}",
            res.err()
        );
        bus.shutdown();
    }

    #[test]
    fn reader_and_writer_derive_same_schema_without_project_path() {
        // Reader (MCP) passes `<root>/.leankg`; writer embed passes
        // `<root>/.leankg/leankg.db`. Both must normalize to the SAME schema
        // even when leankg.yaml has no `project_path`.
        let dir = TempDir::new().unwrap();
        let root = dir.path();
        // No leankg.yaml at all → fallback path key.
        let reader = std::path::Path::new(root).join(".leankg");
        let writer = std::path::Path::new(root).join(".leankg").join("leankg.db");
        assert_eq!(
            schema_for_path(&reader),
            schema_for_path(&writer),
            "reader/writer schema must agree without project_path"
        );
    }

    #[test]
    fn reader_and_writer_derive_same_schema_with_project_path() {
        let dir = TempDir::new().unwrap();
        let root = dir.path();
        std::fs::write(
            root.join("leankg.yaml"),
            "project:\n  name: demo\n  root: .\n  languages:\n  - rust\n  project_path: /host/demo\n",
        )
        .unwrap();
        let reader = root.join(".leankg");
        let writer = root.join(".leankg").join("leankg.db");
        let rs = schema_for_path(&reader);
        let ws = schema_for_path(&writer);
        assert_eq!(rs, ws, "reader/writer schema must agree with project_path");
        assert_eq!(
            rs,
            schema_for_path(std::path::Path::new("/host/demo/.leankg")),
            "schema must key on the host project_path, not the mount path"
        );
    }

    #[test]
    fn relative_and_absolute_spellings_derive_same_schema() {
        // R1 sweep issue #5: `leankg index ./src` keyed the schema on the
        // literal string "./src" while the MCP server started with
        // --project <abs-path> derived a different key — the server served
        // an EMPTY DB right after a successful index. The same physical
        // directory must always produce the same identity key.
        //
        // Uses the *_in variants with an explicit base so this test never
        // mutates the process CWD (other tests read it concurrently).
        let dir = TempDir::new().unwrap();
        let src = dir.path().join("src");
        std::fs::create_dir_all(&src).unwrap();

        // CLI flow: relative path resolved against the invocation CWD.
        let rel_key =
            schema_for_path_in(std::path::Path::new("./src/.leankg/leankg.db"), dir.path());
        // MCP flow: absolute --project path, no base needed.
        let abs_key = schema_for_path(&src.join(".leankg").join("leankg.db"));
        assert_eq!(
            rel_key, abs_key,
            "CLI relative index path and MCP absolute --project must share one schema"
        );

        // The canonicalization itself collapses spellings of one directory:
        // trailing slash + dot components + symlink-free relative form.
        let plain = canonical_project_root_in(&src, dir.path());
        assert_eq!(
            plain,
            canonical_project_root_in(
                std::path::Path::new(&format!("{}/./src/../src/", dir.path().display())),
                dir.path()
            )
        );
    }

    #[test]
    fn trailing_slash_and_dot_components_derive_same_schema() {
        let dir = TempDir::new().unwrap();
        let src = dir.path().join("src");
        std::fs::create_dir_all(&src).unwrap();

        let plain = schema_for_path(&src.join(".leankg"));
        let trailing =
            schema_for_path(std::path::Path::new(&format!("{}/.leankg/", src.display())));
        let dotted = schema_for_path(std::path::Path::new(&format!(
            "{}/./src/../src/.leankg/leankg.db",
            dir.path().display()
        )));
        assert_eq!(plain, trailing, "trailing slash must not change the key");
        assert_eq!(
            plain, dotted,
            "./ and ../ components must not change the key"
        );
    }

    #[cfg(unix)]
    #[test]
    fn symlinked_project_root_derives_same_schema() {
        let dir = TempDir::new().unwrap();
        let real = dir.path().join("real-project");
        std::fs::create_dir_all(&real).unwrap();
        let link = dir.path().join("link-to-project");
        std::os::unix::fs::symlink(&real, &link).unwrap();

        assert_eq!(
            schema_for_path(&link),
            schema_for_path(&real),
            "a symlink pointing at the project root must not fork the schema"
        );
    }

    #[test]
    fn inject_search_path_emits_public_and_merges_with_ro() {
        let base = "postgresql://postgres:postgres@localhost:5433/leankg";
        let pinned = inject_search_path(base, "leankg_p_abc");
        assert!(
            pinned.contains("-csearch_path%3Dleankg_p_abc%2Cpublic"),
            "search_path must include ,public: {pinned}"
        );
        // RO flag merges into the same options= param (space-separated).
        let ro = PostgresBackend {
            pg_url: pinned.clone(),
            schema: Some("leankg_p_abc".into()),
            pool: std::sync::Arc::new(ClientPool::new(1)),
            ro_pool: std::sync::Arc::new(ClientPool::new(1)),
            read_only: true,
            write_bus: None,
        }
        .read_only_url();
        assert!(
            ro.contains("-cdefault_transaction_read_only%3Don"),
            "RO flag must merge into options: {ro}"
        );
        assert!(
            ro.contains("search_path"),
            "search_path must survive RO merge: {ro}"
        );
    }

    #[test]
    fn index_lock_key_is_stable_and_path_env_distinct() {
        let k1 = index_lock_key("local", "/workspace/a");
        assert_eq!(k1, index_lock_key("local", "/workspace/a"));
        assert_ne!(k1, index_lock_key("local", "/workspace/b"));
        assert_ne!(k1, index_lock_key("prod", "/workspace/a"));
    }

    #[test]
    fn index_lock_key_differs_from_old_single_key() {
        assert_ne!(
            index_lock_key("local", "/workspace/a"),
            PostgresBackend::INDEX_LOCK_KEY,
            "per-project key must differ from the legacy fixed key"
        );
    }

    #[test]
    fn index_lock_key_fits_i64() {
        // FNV-1a result must be a valid i64 (it is by construction).
        let k = index_lock_key("local", "/workspace/a");
        let _ = k.checked_add(1).unwrap();
    }

    #[test]
    fn embedding_state_never_routes_to_copy_even_when_copy_enabled() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        std::env::set_var("LEANKG_EMBED_COPY", "1");
        assert!(use_copy_path("embedding_vectors"));
        assert!(!use_copy_path("embedding_state"));
        assert!(!use_copy_path("embedding_vectors_staging"));
        assert!(!use_copy_path("embedding_vectors_qwen3_emb_4b_2560"));
        assert!(!use_copy_path("embedding_state_qwen3_emb_4b_2560"));
        std::env::remove_var("LEANKG_EMBED_COPY");
    }

    #[test]
    fn max_batch_sanity() {
        assert_eq!((60_000usize / 5).clamp(1, 5000), 5000);
        assert_eq!((60_000usize / 60).clamp(1, 5000), 1000);
        assert_eq!((60_000usize / 20_000).clamp(1, 5000), 3);
    }

    #[test]
    fn pg_sql_log_enabled_truthy_values() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        std::env::remove_var("LEANKG_PG_SQL_LOG");
        assert!(!pg_sql_log_enabled());
        for v in ["1", "true", "on", "TRUE", "ON"] {
            std::env::set_var("LEANKG_PG_SQL_LOG", v);
            assert!(pg_sql_log_enabled(), "expected {v} truthy");
        }
        for v in ["0", "no", "off", ""] {
            std::env::set_var("LEANKG_PG_SQL_LOG", v);
            assert!(!pg_sql_log_enabled(), "expected {v:?} falsy");
        }
        std::env::remove_var("LEANKG_PG_SQL_LOG");
    }

    #[test]
    fn format_named_params_for_log_truncates_long_values() {
        let mut params = BTreeMap::new();
        params.insert("q".to_string(), serde_json::json!("x".repeat(300)));
        let s = format_named_params_for_log(&params);
        assert!(s.contains("(+"), "long value must be truncated: {s}");
        assert!(s.len() < 300, "truncated log must stay small: {s}");
        let empty = format_named_params_for_log(&BTreeMap::new());
        assert_eq!(empty, "{}");
    }

    #[test]
    fn truncate_json_for_log_short_value_unchanged() {
        let short = serde_json::json!("abc");
        assert_eq!(truncate_json_for_log(&short, 240), "\"abc\"");
    }
}
