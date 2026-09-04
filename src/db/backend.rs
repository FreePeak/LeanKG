//! PostgreSQL backend — the only storage engine (post-migration, plan D4).
//!
//! Everything that touches a database goes through [`PostgresBackend`].
//! The legacy `DbBackend` trait and embedded-backend shim were deleted in
//! Phase 8; `run_script` is now a concrete inherent method.

use crate::db::pg::mutability;
use crate::db::pg::translate;
use rustls_pki_types::pem::PemObject;
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

/// Build the root store for verified-TLS connections.
///
/// `Some(path)` — private/managed CA bundle (Aiven, Neon, ...); roots at that
/// file and rejects a bundle with zero parseable certificates (an empty root
/// store would otherwise only fail at handshake time, far from the cause).
/// `None` — public CAs, verified against the Mozilla roots compiled in via
/// webpki-roots (already in the tree through reqwest/hyper-rustls).
fn ca_root_store(
    ca_path: Option<&str>,
) -> Result<rustls::RootCertStore, Box<dyn std::error::Error>> {
    let mut roots = rustls::RootCertStore::empty();
    if let Some(path) = ca_path {
        let certs = rustls_pki_types::CertificateDer::pem_file_iter(path)
            .map_err(|e| format!("cannot read CA file {path}: {e}"))?
            .collect::<Result<Vec<_>, _>>()?;
        if certs.is_empty() {
            return Err(format!("no certificates parsed from CA file {path}").into());
        }
        for c in certs {
            roots
                .add(c)
                .map_err(|e| format!("bad CA cert in {path}: {e}"))?;
        }
    } else {
        roots.extend(webpki_roots::TLS_SERVER_ROOTS.iter().cloned());
    }
    Ok(roots)
}

/// Build the rustls client config for verified-TLS connections.
fn tls_client_config(
    ca_path: Option<&str>,
) -> Result<rustls::ClientConfig, Box<dyn std::error::Error>> {
    Ok(rustls::ClientConfig::builder()
        .with_root_certificates(ca_root_store(ca_path)?)
        .with_no_client_auth())
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
    let connector = match std::env::var("LEANKG_PG_CA_CERT").ok().as_deref() {
        Some(path) => {
            postgres_rustls::MakeTlsConnector::new(Arc::new(tls_client_config(Some(path))?).into())
        }
        None if wants_tls => {
            postgres_rustls::MakeTlsConnector::new(Arc::new(tls_client_config(None)?).into())
        }
        None => return Ok(cfg.connect(postgres::NoTls)?),
    };
    Ok(cfg.connect(connector)?)
}

/// Re-export the row/result value types the rest of the codebase consumes
/// positionally (`row[0].get_str()`, `NamedRows::new`, `DataValue::Num`).
pub use crate::db::value::{DataValue, NamedRows};

/// Storage-backend abstraction. Production uses [`PostgresBackend`];
/// tests use an in-memory [`crate::db::fake::FakeBackend`] so unit tests
/// never need a live Postgres.
pub trait DbBackend: Send + Sync {
    /// Run a legacy Datalog-style script query (translated to SQL by the PG
    /// backend, or interpreted in-memory by the fake). Returns named rows.
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

    /// FR-ENT-1: append a batch of hash-chained audit entries in ONE
    /// multi-row INSERT. Ids are assigned by the DB (BIGSERIAL); callers pass
    /// `id = 0` placeholders.
    ///
    /// Default errs so a backend that forgets to implement the ledger can
    /// never silently lose rows.
    fn insert_audit_batch(
        &self,
        _entries: &[crate::audit::AuditEntry],
    ) -> Result<(), Box<dyn std::error::Error>> {
        Err("audit ledger not supported by this backend".into())
    }

    /// FR-ENT-1: entry_hash of the newest audit row (`None` on an empty
    /// ledger), used by the recorder to continue the hash chain across
    /// process restarts. Errors when the table is absent (pre-migration).
    fn last_audit_entry_hash(&self) -> Result<Option<String>, Box<dyn std::error::Error>> {
        Err("audit ledger not supported by this backend".into())
    }

    /// FR-ENT-1: ledger rows in id order, filtered by an inclusive
    /// `[since, until]` timestamp window (either bound may be `None`).
    /// Backs `leankg audit export|verify`.
    fn query_audit(
        &self,
        _since: Option<std::time::SystemTime>,
        _until: Option<std::time::SystemTime>,
    ) -> Result<Vec<crate::audit::AuditEntry>, Box<dyn std::error::Error>> {
        Err("audit ledger not supported by this backend".into())
    }

    /// H10 / FR-PLG-8: usage-dashboard buckets over `context_metrics`, all
    /// computed in SINGLE grouped queries (`GROUP BY tool_name`,
    /// `timestamp/86400`, `project_path`, `query_pattern`). `since_cutoff`
    /// filters `timestamp >= cutoff` (epoch seconds); `None` = all time.
    /// Soft-deleted rows are always excluded.
    fn query_usage_aggregates(
        &self,
        _since_cutoff: Option<i64>,
    ) -> Result<crate::dashboard::UsageAggregates, Box<dyn std::error::Error>> {
        Err("usage dashboard not supported by this backend".into())
    }

    // ---- SQL-first API (W8 P0 — Datalog removal seam) ----

    /// Run a parameterized PostgreSQL SELECT and return owned named rows.
    /// The default (fake / un-migrated backends) reports unsupported.
    fn sql_query(
        &self,
        _sql: &str,
        _params: &[crate::db::sql::SqlParam],
    ) -> Result<Vec<crate::db::sql::SqlRow>, Box<dyn std::error::Error>> {
        Err(crate::db::sql::unsupported())
    }

    /// [`Self::sql_query`] with session GUCs applied inside the read
    /// transaction (e.g. `hnsw.ef_search` before a vector search).
    fn sql_query_gucs(
        &self,
        _sql: &str,
        _params: &[crate::db::sql::SqlParam],
        _gucs: &[(&str, &str)],
    ) -> Result<Vec<crate::db::sql::SqlRow>, Box<dyn std::error::Error>> {
        Err(crate::db::sql::unsupported())
    }

    /// Run a parameterized INSERT/UPDATE/DELETE/DDL statement; returns the
    /// affected row count.
    fn sql_execute(
        &self,
        _sql: &str,
        _params: &[crate::db::sql::SqlParam],
    ) -> Result<u64, Box<dyn std::error::Error>> {
        Err(crate::db::sql::unsupported())
    }

    /// Run several statements in ONE transaction — all commit or all roll
    /// back. Replaces multi-statement `:put`/`:rm` scripts.
    fn sql_execute_batch(
        &self,
        _stmts: &[(&str, Vec<crate::db::sql::SqlParam>)],
    ) -> Result<(), Box<dyn std::error::Error>> {
        Err(crate::db::sql::unsupported())
    }

    /// Bulk-load rows into `table` via PostgreSQL COPY (text format).
    /// Replaces `import_relations` / `submit_import`.
    fn sql_copy_import(
        &self,
        _table: &str,
        _columns: &[&str],
        _rows: &[Vec<crate::db::sql::SqlParam>],
    ) -> Result<(), Box<dyn std::error::Error>> {
        Err(crate::db::sql::unsupported())
    }

    // ---- W8 wave 1 typed queries: api_keys (parameterized SQL) ----
    // Each method replaces one Datalog `run_script` site in keys.rs. The
    // defaults err so a backend that skips the migration fails loudly.

    /// Upsert one API key row (`:put api_keys` parity — conflict target is
    /// the unique `key_hash`, matching the translator's pk_for_table).
    fn insert_api_key(
        &self,
        _key: &crate::db::keys::ApiKey,
    ) -> Result<(), Box<dyn std::error::Error>> {
        Err("SQL-first api_keys not supported by this backend".into())
    }

    /// All key rows. Replaces the unfiltered `*api_keys[...]` read; callers
    /// apply their own revoked/display filtering exactly as before.
    fn list_api_keys(&self) -> Result<Vec<crate::db::keys::ApiKey>, Box<dyn std::error::Error>> {
        Err("SQL-first api_keys not supported by this backend".into())
    }

    /// Revoke by id if not already revoked. Returns whether a row was
    /// updated: `false` covers BOTH "no such id" and "already revoked",
    /// matching the legacy two-step read+check behavior.
    fn mark_api_key_revoked(
        &self,
        _id: &str,
        _revoked_at: &str,
    ) -> Result<bool, Box<dyn std::error::Error>> {
        Err("SQL-first api_keys not supported by this backend".into())
    }

    /// `(id, key_hash)` for every non-revoked key — the argon2 verification
    /// candidate set for [`Self`]-level token validation.
    fn list_active_api_key_hashes(
        &self,
    ) -> Result<Vec<(String, String)>, Box<dyn std::error::Error>> {
        Err("SQL-first api_keys not supported by this backend".into())
    }

    /// Record last-use timestamp on successful validation.
    ///
    /// Deviation from the legacy path (documented in the plan): the old
    /// Datalog flow DELETEd the row and re-inserted it with `name=""`,
    /// `created_at=""` on EVERY validation, wiping the key's identity. The
    /// SQL-first path only updates `last_used_at`.
    fn touch_api_key_last_used(
        &self,
        _id: &str,
        _last_used_at: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        Err("SQL-first api_keys not supported by this backend".into())
    }

    // ========================================================================
    // W8 wave-1b: knowledge_entries parameterized-SQL surface.
    // Replaces the Datalog `:put`/`:rm`/regex-scan bodies in db/mod.rs.
    // Semantics locked by tests/pg_sql_wave1b_test.rs.
    // ========================================================================

    /// Insert or update by id (legacy `:put` = upsert; the wave-1 fix made
    /// the translator emit ON CONFLICT — here it is explicit).
    fn upsert_knowledge_entry(
        &self,
        _entry: &crate::db::models::KnowledgeEntry,
    ) -> Result<(), Box<dyn std::error::Error>> {
        Err("SQL-first knowledge_entries not supported by this backend".into())
    }

    fn find_knowledge_entry(
        &self,
        _id: &str,
    ) -> Result<Option<crate::db::models::KnowledgeEntry>, Box<dyn std::error::Error>> {
        Err("SQL-first knowledge_entries not supported by this backend".into())
    }

    /// Delete by id. Returns whether a row was removed (legacy `:rm`
    /// silently no-opped on absent ids, so absence is not an error).
    fn delete_knowledge_entry_by_id(&self, _id: &str) -> Result<bool, Box<dyn std::error::Error>> {
        Err("SQL-first knowledge_entries not supported by this backend".into())
    }

    /// Case-insensitive substring match over title/content (equivalent to
    /// the legacy `regex_matches(lowercase(x), ".*q.*")` scan), with
    /// optional exact-match filters and limit.
    fn search_knowledge_entries(
        &self,
        _query: &str,
        _knowledge_type: Option<&str>,
        _environment: Option<&str>,
        _limit: usize,
    ) -> Result<Vec<crate::db::models::KnowledgeEntry>, Box<dyn std::error::Error>> {
        Err("SQL-first knowledge_entries not supported by this backend".into())
    }

    fn list_knowledge_by_element(
        &self,
        _element_qualified: &str,
    ) -> Result<Vec<crate::db::models::KnowledgeEntry>, Box<dyn std::error::Error>> {
        Err("SQL-first knowledge_entries not supported by this backend".into())
    }

    fn list_knowledge_by_feature(
        &self,
        _feature_id: &str,
    ) -> Result<Vec<crate::db::models::KnowledgeEntry>, Box<dyn std::error::Error>> {
        Err("SQL-first knowledge_entries not supported by this backend".into())
    }

    fn list_knowledge_by_environment(
        &self,
        _environment: &str,
        _limit: usize,
    ) -> Result<Vec<crate::db::models::KnowledgeEntry>, Box<dyn std::error::Error>> {
        Err("SQL-first knowledge_entries not supported by this backend".into())
    }

    // ---- SQL-first code_elements reads (W8 wave-2) ----

    /// Keyed lookup by `qualified_name`. `env` is NOT selected (matches the
    /// legacy 11-column projection used by `find_element`/`find_element_by_name`).
    fn find_element_by_key(
        &self,
        _qualified_name: &str,
    ) -> Result<Option<crate::db::models::CodeElement>, Box<dyn std::error::Error>> {
        Err("SQL-first code_elements reads not supported by this backend".into())
    }

    /// First row whose `name` matches exactly (legacy `find_element_by_name`).
    fn find_element_by_name_col(
        &self,
        _name: &str,
    ) -> Result<Option<crate::db::models::CodeElement>, Box<dyn std::error::Error>> {
        Err("SQL-first code_elements reads not supported by this backend".into())
    }

    /// Keyed hydration for a set of qualified names (HNSW ANN hits, FR-SEM-07).
    /// `env` IS selected here. Callers dedup/drop-empties before calling; the
    /// backend chunks the IN-list internally.
    fn elements_by_qualified_names(
        &self,
        _qualified_names: &[String],
    ) -> Result<Vec<crate::db::models::CodeElement>, Box<dyn std::error::Error>> {
        Err("SQL-first code_elements reads not supported by this backend".into())
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

    /// Max time a checkout may block waiting for a free slot.
    /// `LEANKG_PG_POOL_WAIT_MS` (default 10_000).
    fn wait_timeout_from_env() -> std::time::Duration {
        let ms = std::env::var("LEANKG_PG_POOL_WAIT_MS")
            .ok()
            .and_then(|v| v.parse::<u64>().ok())
            .filter(|v| *v >= 100)
            .unwrap_or(10_000);
        std::time::Duration::from_millis(ms)
    }

    /// Check out a client, connecting a new one up to `max` live, else
    /// blocking on a Condvar until one is returned — but only up to
    /// `LEANKG_PG_POOL_WAIT_MS` (default 10s). BUG-E defense-in-depth: an
    /// unbounded wait let one wedged tool hold every slot forever, so ALL
    /// later tools starved ("timed out after 30s" cascade). Starvation now
    /// fails fast with a clear error instead of blocking indefinitely.
    pub fn checkout(&self, connect_url: &str) -> Result<PooledClient, Box<dyn std::error::Error>> {
        let wait_budget = Self::wait_timeout_from_env();
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
            // At capacity — wait for a return, bounded.
            let deadline = std::time::Duration::from_millis(wait_budget.as_millis() as u64);
            let (nguard, res) = self
                .inner
                .has_slot
                .wait_timeout(guard, deadline)
                .unwrap_or_else(|e| e.into_inner());
            guard = nguard;
            if res.timed_out() && guard.idle.is_empty() && guard.live >= self.inner.max {
                return Err(format!(
                    "pg connection pool exhausted: {} slots held for over {}ms; \
                     a slow tool may be stuck. Retry or raise LEANKG_PG_POOL_SIZE.",
                    self.inner.max,
                    deadline.as_millis()
                )
                .into());
            }
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
    /// Box the bind params once, then hand `&dyn ToSql` refs to the client.
    fn sql_binds(
        params: &[crate::db::sql::SqlParam],
    ) -> Vec<Box<dyn postgres::types::ToSql + Sync + Send>> {
        params.iter().map(|p| p.to_pg()).collect()
    }

    /// Pool selection for a SQL-first statement: a read-only backend never
    /// touches the RW pool (writes fail at the Postgres layer with a clean
    /// `read-only` error), mirroring [`Self::run_script_sync`].
    fn checkout_for_sql(&self) -> Result<PooledClient, Box<dyn std::error::Error>> {
        if self.read_only {
            self.checkout_read_only()
        } else {
            self.checkout()
        }
    }

    /// Emit one `leankg::pg_sql` line for a SQL-first op. Deliberately has NO
    /// legacy-engine field — converted paths are proven Datalog-free by its absence.
    fn log_sql_op(phase: &str, op: &str, sql: &str, rows: usize, elapsed_ms: u128) {
        tracing::info!(
            target: "leankg::pg_sql",
            phase,
            kind = "sql",
            op,
            rows,
            elapsed_ms,
            sql,
            "pg sql_first"
        );
    }

    /// W8 P0 sync body behind [`DbBackend::sql_query`].
    fn sql_query_sync(
        &self,
        sql: &str,
        params: &[crate::db::sql::SqlParam],
    ) -> Result<Vec<crate::db::sql::SqlRow>, Box<dyn std::error::Error>> {
        let binds = Self::sql_binds(params);
        let refs: Vec<&(dyn postgres::types::ToSql + Sync)> = binds
            .iter()
            .map(|b| b.as_ref() as &(dyn postgres::types::ToSql + Sync))
            .collect();
        let mut client = self.checkout_for_sql()?;
        let started = std::time::Instant::now();
        let result = client.query(sql, &refs)?;
        let rows: Vec<crate::db::sql::SqlRow> =
            result.iter().map(crate::db::sql::row_from_pg).collect();
        Self::log_sql_op(
            "done",
            "query",
            sql,
            rows.len(),
            started.elapsed().as_millis(),
        );
        Ok(rows)
    }

    /// W8 P0 sync body behind [`DbBackend::sql_query_gucs`]: `SET LOCAL`
    /// knobs ride the same transaction as the read and revert on commit.
    fn sql_query_gucs_sync(
        &self,
        sql: &str,
        params: &[crate::db::sql::SqlParam],
        gucs: &[(&str, &str)],
    ) -> Result<Vec<crate::db::sql::SqlRow>, Box<dyn std::error::Error>> {
        let binds = Self::sql_binds(params);
        let refs: Vec<&(dyn postgres::types::ToSql + Sync)> = binds
            .iter()
            .map(|b| b.as_ref() as &(dyn postgres::types::ToSql + Sync))
            .collect();
        let mut client = self.checkout_for_sql()?;
        let started = std::time::Instant::now();
        let mut tx = client.transaction()?;
        let owned: Vec<(String, String)> = gucs
            .iter()
            .map(|(n, v)| ((*n).to_string(), (*v).to_string()))
            .collect();
        apply_gucs(&mut tx, &owned)?;
        let result = tx.query(sql, &refs)?;
        let rows: Vec<crate::db::sql::SqlRow> =
            result.iter().map(crate::db::sql::row_from_pg).collect();
        tx.commit()?;
        Self::log_sql_op(
            "done",
            "query_gucs",
            sql,
            rows.len(),
            started.elapsed().as_millis(),
        );
        Ok(rows)
    }

    /// W8 P0 sync body behind [`DbBackend::sql_execute`].
    fn sql_execute_sync(
        &self,
        sql: &str,
        params: &[crate::db::sql::SqlParam],
    ) -> Result<u64, Box<dyn std::error::Error>> {
        let binds = Self::sql_binds(params);
        let refs: Vec<&(dyn postgres::types::ToSql + Sync)> = binds
            .iter()
            .map(|b| b.as_ref() as &(dyn postgres::types::ToSql + Sync))
            .collect();
        let mut client = self.checkout_for_sql()?;
        let started = std::time::Instant::now();
        let n = client.execute(sql, &refs)?;
        Self::log_sql_op(
            "done",
            "execute",
            sql,
            n as usize,
            started.elapsed().as_millis(),
        );
        Ok(n)
    }

    /// W8 P0 sync body behind [`DbBackend::sql_execute_batch`]: every
    /// statement runs in ONE transaction — any failure rolls back all.
    fn sql_execute_batch_sync(
        &self,
        stmts: &[(&str, Vec<crate::db::sql::SqlParam>)],
    ) -> Result<(), Box<dyn std::error::Error>> {
        let mut client = self.checkout_for_sql()?;
        let started = std::time::Instant::now();
        let mut tx = client.transaction()?;
        for (sql, params) in stmts {
            let binds = Self::sql_binds(params);
            let refs: Vec<&(dyn postgres::types::ToSql + Sync)> = binds
                .iter()
                .map(|b| b.as_ref() as &(dyn postgres::types::ToSql + Sync))
                .collect();
            tx.execute(*sql, &refs)?;
        }
        tx.commit()?;
        Self::log_sql_op(
            "done",
            "execute_batch",
            &format!("{} stmt(s)", stmts.len()),
            0,
            started.elapsed().as_millis(),
        );
        Ok(())
    }

    /// W8 P0 sync body behind [`DbBackend::sql_copy_import`] — text-format
    /// COPY inside a transaction. NULL is the `\N` marker so an empty string
    /// stays distinct from SQL NULL.
    fn sql_copy_import_sync(
        &self,
        table: &str,
        columns: &[&str],
        rows: &[Vec<crate::db::sql::SqlParam>],
    ) -> Result<(), Box<dyn std::error::Error>> {
        use std::io::Write;

        // Table/column names come from internal callers; still quoted like
        // every other identifier in this file.
        let q_table = translate::quote_ident(table);
        let q_cols = columns
            .iter()
            .map(|c| translate::quote_ident(c))
            .collect::<Vec<_>>()
            .join(", ");
        let copy_sql = format!("COPY {q_table} ({q_cols}) FROM STDIN");
        let mut client = self.checkout_for_sql()?;
        let started = std::time::Instant::now();
        let mut tx = client.transaction()?;
        let mut writer = tx.copy_in(&copy_sql)?;
        for row in rows {
            let mut line = String::new();
            for (i, val) in row.iter().enumerate() {
                if i > 0 {
                    line.push('\t');
                }
                if matches!(val, crate::db::sql::SqlParam::Null) {
                    // Raw \N marker: push_copy_text would escape the
                    // backslash, turning NULL into a literal "\N" string.
                    line.push_str("\\N");
                } else {
                    push_copy_text(&mut line, &val.to_copy_text());
                }
            }
            line.push('\n');
            writer.write_all(line.as_bytes())?;
        }
        writer.finish()?;
        tx.commit()?;
        Self::log_sql_op(
            "done",
            "copy_import",
            &copy_sql,
            rows.len(),
            started.elapsed().as_millis(),
        );
        Ok(())
    }

    /// Wrap a sync SQL-first body in the standard `block_in_place` guard
    /// (the sync `postgres::Client` must not run on an ambient tokio worker).
    fn sql_off_runtime<T>(
        &self,
        body: impl FnOnce() -> Result<T, Box<dyn std::error::Error>>,
    ) -> Result<T, Box<dyn std::error::Error>> {
        if tokio::runtime::Handle::try_current().is_ok() {
            tokio::task::block_in_place(body)
        } else {
            body()
        }
    }

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
        if after.is_empty() {
            format!("{before}?options={RO_FLAG}")
        } else {
            format!("{before}?{after}&options={RO_FLAG}")
        }
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

    /// FR-ENT-1 sync body: one multi-row INSERT for the whole audit batch
    /// (≤ 50 rows × 9 params per flush, well under the 65535 bind limit).
    fn insert_audit_batch_sync(
        &self,
        entries: &[crate::audit::AuditEntry],
    ) -> Result<(), Box<dyn std::error::Error>> {
        if entries.is_empty() {
            return Ok(());
        }
        const COLS: usize = 9;
        let mut sql = String::from(
            "INSERT INTO audit_log (ts, actor, agent_client, tool, project, \
             args_hash, result_status, prev_hash, entry_hash) VALUES ",
        );
        let mut params: Vec<Box<dyn postgres::types::ToSql + Sync>> =
            Vec::with_capacity(entries.len() * COLS);
        for (i, e) in entries.iter().enumerate() {
            if i > 0 {
                sql.push_str(", ");
            }
            let base = i * COLS;
            sql.push('(');
            for c in 0..COLS {
                if c > 0 {
                    sql.push(',');
                }
                sql.push_str(&format!("${}", base + c + 1));
            }
            sql.push(')');
            params.push(Box::new(e.ts));
            params.push(Box::new(e.actor.clone()));
            params.push(Box::new(e.agent_client.clone()));
            params.push(Box::new(e.tool.clone()));
            params.push(Box::new(e.project.clone()));
            params.push(Box::new(e.args_hash.clone()));
            params.push(Box::new(e.result_status.clone()));
            params.push(Box::new(e.prev_hash.clone()));
            params.push(Box::new(e.entry_hash.clone()));
        }
        let param_refs: Vec<&(dyn postgres::types::ToSql + Sync)> =
            params.iter().map(|p| p.as_ref()).collect();
        let mut client = self.checkout()?;
        client.execute(sql.as_str(), &param_refs)?;
        Ok(())
    }

    /// FR-ENT-1 sync body: chain head for recorder restarts. Errors when the
    /// audit_log table is absent — the recorder disables after one warn.
    fn last_audit_entry_hash_sync(&self) -> Result<Option<String>, Box<dyn std::error::Error>> {
        let mut client = self.checkout()?;
        let row = client.query_opt(
            "SELECT entry_hash FROM audit_log ORDER BY id DESC LIMIT 1",
            &[],
        )?;
        Ok(row.map(|r| r.get(0)))
    }

    /// FR-ENT-1 sync body: windowed ledger read for export/verify.
    fn query_audit_sync(
        &self,
        since: Option<std::time::SystemTime>,
        until: Option<std::time::SystemTime>,
    ) -> Result<Vec<crate::audit::AuditEntry>, Box<dyn std::error::Error>> {
        const COLS: &str = "id, ts, actor, agent_client, tool, project, args_hash, \
                            result_status, prev_hash, entry_hash";
        let mut clauses: Vec<String> = Vec::new();
        let mut params: Vec<Box<dyn postgres::types::ToSql + Sync>> = Vec::new();
        if let Some(s) = since {
            params.push(Box::new(s));
            clauses.push(format!("ts >= ${}", params.len()));
        }
        if let Some(u) = until {
            params.push(Box::new(u));
            clauses.push(format!("ts <= ${}", params.len()));
        }
        let mut sql = format!("SELECT {COLS} FROM audit_log");
        if !clauses.is_empty() {
            sql.push_str(" WHERE ");
            sql.push_str(&clauses.join(" AND "));
        }
        sql.push_str(" ORDER BY id ASC");

        let refs: Vec<&(dyn postgres::types::ToSql + Sync)> =
            params.iter().map(|p| p.as_ref()).collect();
        let mut client = self.checkout()?;
        let rows = client.query(sql.as_str(), &refs)?;
        Ok(rows
            .iter()
            .map(|r| crate::audit::AuditEntry {
                id: r.get(0),
                ts: r.get(1),
                actor: r.get(2),
                agent_client: r.get(3),
                tool: r.get(4),
                project: r.get(5),
                args_hash: r.get(6),
                result_status: r.get(7),
                prev_hash: r.get(8),
                entry_hash: r.get(9),
            })
            .collect())
    }

    /// H10 / FR-PLG-8: usage-dashboard buckets over `context_metrics`.
    ///
    /// Four SINGLE grouped queries (totals / tool / day / project + pattern);
    /// never row-by-row. Day bucketing uses integer division
    /// `timestamp / 86400` rather than `date_trunc('day', to_timestamp(..))`:
    /// the column is epoch seconds (BIGINT) and integer division is
    /// deterministic UTC regardless of session TimeZone — it matches the
    /// reference `dashboard::aggregate_rows` (`div_euclid`) exactly.
    fn query_usage_aggregates_sync(
        &self,
        since_cutoff: Option<i64>,
    ) -> Result<crate::dashboard::UsageAggregates, Box<dyn std::error::Error>> {
        let mut params: Vec<Box<dyn postgres::types::ToSql + Sync>> = Vec::new();
        let mut window = String::from("is_deleted = FALSE");
        if let Some(cutoff) = since_cutoff {
            params.push(Box::new(cutoff));
            window.push_str(&format!(" AND timestamp >= ${}", params.len()));
        }

        // PG semantics: SUM(bigint) widens to numeric — every token/time
        // sum is cast back to BIGINT so the sync client reads i64s.
        let totals_sql = format!(
            "SELECT COUNT(*)::BIGINT, COALESCE(SUM(input_tokens), 0)::BIGINT, \
             COALESCE(SUM(output_tokens), 0)::BIGINT, \
             COALESCE(SUM(tokens_saved), 0)::BIGINT, \
             COALESCE(SUM(savings_percent), 0)::FLOAT8, \
             COALESCE(SUM(CASE WHEN success THEN 1 ELSE 0 END), 0)::BIGINT \
             FROM context_metrics WHERE {window}"
        );
        let tool_sql = format!(
            "SELECT tool_name, COUNT(*)::BIGINT, COALESCE(SUM(tokens_saved), 0)::BIGINT, \
             COALESCE(SUM(execution_time_ms), 0)::BIGINT FROM context_metrics WHERE {window} \
             GROUP BY tool_name ORDER BY 3 DESC, tool_name ASC"
        );
        // Integer epoch-day bucket; see doc comment.
        let day_sql = format!(
            "SELECT timestamp / 86400, COUNT(*)::BIGINT, \
             COALESCE(SUM(tokens_saved), 0)::BIGINT \
             FROM context_metrics WHERE {window} GROUP BY 1 ORDER BY 1 ASC"
        );
        let project_sql = format!(
            "SELECT project_path, COUNT(*)::BIGINT, COALESCE(SUM(tokens_saved), 0)::BIGINT \
             FROM context_metrics WHERE {window} GROUP BY project_path \
             ORDER BY 3 DESC, project_path ASC LIMIT 5"
        );
        let pattern_sql = format!(
            "SELECT query_pattern, COUNT(*)::BIGINT, COALESCE(SUM(tokens_saved), 0)::BIGINT \
             FROM context_metrics WHERE {window} AND query_pattern IS NOT NULL \
             GROUP BY query_pattern ORDER BY 2 DESC, query_pattern ASC LIMIT 10"
        );

        let refs: Vec<&(dyn postgres::types::ToSql + Sync)> =
            params.iter().map(|p| p.as_ref()).collect();
        let mut client = self.checkout()?;
        let trow = client.query_one(&totals_sql, &refs)?;
        let mut agg = crate::dashboard::UsageAggregates {
            calls: trow.get::<_, i64>(0).max(0) as u64,
            input_tokens: trow.get(1),
            output_tokens: trow.get(2),
            tokens_saved: trow.get(3),
            savings_percent_sum: trow.get(4),
            successful_calls: trow.get::<_, i64>(5).max(0) as u64,
            ..Default::default()
        };
        for r in client.query(&tool_sql, &refs)? {
            let calls: i64 = r.get(1);
            let ms_sum: i64 = r.get(3);
            agg.tools.push(crate::dashboard::ToolUsage {
                tool: r.get(0),
                calls: calls.max(0) as u64,
                tokens_saved: r.get(2),
                avg_ms: ms_sum as f64 / calls.max(1) as f64,
            });
        }
        for r in client.query(&day_sql, &refs)? {
            let day_bucket: i64 = r.get(0);
            agg.days.push(crate::dashboard::DayUsage {
                day: crate::dashboard::day_label(day_bucket),
                calls: r.get::<_, i64>(1).max(0) as u64,
                tokens_saved: r.get(2),
            });
        }
        for r in client.query(&project_sql, &refs)? {
            agg.projects.push(crate::dashboard::ProjectUsage {
                project: r.get(0),
                calls: r.get::<_, i64>(1).max(0) as u64,
                tokens_saved: r.get(2),
            });
        }
        for r in client.query(&pattern_sql, &refs)? {
            agg.patterns
                .push((r.get(0), r.get::<_, i64>(1).max(0) as u64, r.get(2)));
        }
        Ok(agg)
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

    /// Execute a script (legacy Datalog-style dialect → SQL via the
    /// translator) and return named rows. Mirrors the historical 2-arg
    /// `run_script` convention
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
    /// (Phase 3 replaced the legacy engine's `import_relations`).
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
            // The legacy dialect name for the vector column is `vector`; the PG
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
                values.push(datavalue_to_sql(val, &cols[i]));
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
                    params.push(datavalue_to_sql(val, &cols[j]));
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

    // ---- SQL-first API (W8 P0) ----

    fn sql_query(
        &self,
        sql: &str,
        params: &[crate::db::sql::SqlParam],
    ) -> Result<Vec<crate::db::sql::SqlRow>, Box<dyn std::error::Error>> {
        self.sql_off_runtime(|| self.sql_query_sync(sql, params))
    }

    fn sql_query_gucs(
        &self,
        sql: &str,
        params: &[crate::db::sql::SqlParam],
        gucs: &[(&str, &str)],
    ) -> Result<Vec<crate::db::sql::SqlRow>, Box<dyn std::error::Error>> {
        self.sql_off_runtime(|| self.sql_query_gucs_sync(sql, params, gucs))
    }

    fn sql_execute(
        &self,
        sql: &str,
        params: &[crate::db::sql::SqlParam],
    ) -> Result<u64, Box<dyn std::error::Error>> {
        self.sql_off_runtime(|| self.sql_execute_sync(sql, params))
    }

    fn sql_execute_batch(
        &self,
        stmts: &[(&str, Vec<crate::db::sql::SqlParam>)],
    ) -> Result<(), Box<dyn std::error::Error>> {
        self.sql_off_runtime(|| self.sql_execute_batch_sync(stmts))
    }

    fn sql_copy_import(
        &self,
        table: &str,
        columns: &[&str],
        rows: &[Vec<crate::db::sql::SqlParam>],
    ) -> Result<(), Box<dyn std::error::Error>> {
        self.sql_off_runtime(|| self.sql_copy_import_sync(table, columns, rows))
    }

    // ---- W8 wave 1 typed queries: api_keys ----

    fn insert_api_key(
        &self,
        key: &crate::db::keys::ApiKey,
    ) -> Result<(), Box<dyn std::error::Error>> {
        self.sql_execute(
            "INSERT INTO api_keys (id, name, key_hash, created_at, last_used_at, revoked_at) \
             VALUES ($1, $2, $3, $4, $5, $6) \
             ON CONFLICT (key_hash) DO UPDATE SET \
               id = EXCLUDED.id, name = EXCLUDED.name, created_at = EXCLUDED.created_at, \
               last_used_at = EXCLUDED.last_used_at, revoked_at = EXCLUDED.revoked_at",
            &[
                crate::db::sql::SqlParam::Text(key.id.clone()),
                crate::db::sql::SqlParam::Text(key.name.clone()),
                crate::db::sql::SqlParam::Text(key.key_hash.clone()),
                crate::db::sql::SqlParam::Text(key.created_at.clone()),
                crate::db::sql::SqlParam::from(key.last_used_at.clone()),
                crate::db::sql::SqlParam::from(key.revoked_at.clone()),
            ],
        )
        .map(|_| ())
    }

    fn list_api_keys(&self) -> Result<Vec<crate::db::keys::ApiKey>, Box<dyn std::error::Error>> {
        let rows = self.sql_query(
            "SELECT id, name, key_hash, created_at, last_used_at, revoked_at FROM api_keys",
            &[],
        )?;
        Ok(rows
            .iter()
            .map(|r| crate::db::keys::ApiKey {
                id: r.text("id").unwrap_or_default(),
                name: r.text("name").unwrap_or_default(),
                key_hash: r.text("key_hash").unwrap_or_default(),
                created_at: r.text("created_at").unwrap_or_default(),
                last_used_at: r.text("last_used_at"),
                revoked_at: r.text("revoked_at"),
            })
            .collect())
    }

    fn mark_api_key_revoked(
        &self,
        id: &str,
        revoked_at: &str,
    ) -> Result<bool, Box<dyn std::error::Error>> {
        let n = self.sql_execute(
            "UPDATE api_keys SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL",
            &[
                crate::db::sql::SqlParam::Text(id.to_string()),
                crate::db::sql::SqlParam::Text(revoked_at.to_string()),
            ],
        )?;
        Ok(n > 0)
    }

    fn list_active_api_key_hashes(
        &self,
    ) -> Result<Vec<(String, String)>, Box<dyn std::error::Error>> {
        let rows = self.sql_query(
            "SELECT id, key_hash FROM api_keys WHERE revoked_at IS NULL",
            &[],
        )?;
        Ok(rows
            .iter()
            .map(|r| {
                (
                    r.text("id").unwrap_or_default(),
                    r.text("key_hash").unwrap_or_default(),
                )
            })
            .collect())
    }

    fn touch_api_key_last_used(
        &self,
        id: &str,
        last_used_at: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        self.sql_execute(
            "UPDATE api_keys SET last_used_at = $2 WHERE id = $1",
            &[
                crate::db::sql::SqlParam::Text(id.to_string()),
                crate::db::sql::SqlParam::Text(last_used_at.to_string()),
            ],
        )
        .map(|_| ())
    }

    // ========================================================================
    // W8 wave-1b: knowledge_entries parameterized-SQL implementations.
    // ========================================================================

    fn upsert_knowledge_entry(
        &self,
        entry: &crate::db::models::KnowledgeEntry,
    ) -> Result<(), Box<dyn std::error::Error>> {
        self.sql_execute(
            "INSERT INTO knowledge_entries \
               (id, knowledge_type, title, content, element_qualified, user_story_id, \
                feature_id, tags, environment, branch, author, created_at, updated_at) \
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) \
             ON CONFLICT (id) DO UPDATE SET \
               knowledge_type = EXCLUDED.knowledge_type, title = EXCLUDED.title, \
               content = EXCLUDED.content, element_qualified = EXCLUDED.element_qualified, \
               user_story_id = EXCLUDED.user_story_id, feature_id = EXCLUDED.feature_id, \
               tags = EXCLUDED.tags, environment = EXCLUDED.environment, \
               branch = EXCLUDED.branch, author = EXCLUDED.author, \
               created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at",
            &[
                crate::db::sql::SqlParam::Text(entry.id.clone()),
                crate::db::sql::SqlParam::Text(entry.knowledge_type.clone()),
                crate::db::sql::SqlParam::Text(entry.title.clone()),
                crate::db::sql::SqlParam::Text(entry.content.clone()),
                opt_text(entry.element_qualified.as_deref()),
                opt_text(entry.user_story_id.as_deref()),
                opt_text(entry.feature_id.as_deref()),
                // tags column is JSONB; legacy bound an arbitrary JSON-ish
                // string — parse when possible, else wrap as a JSON string.
                crate::db::sql::SqlParam::Json(
                    serde_json::from_str::<serde_json::Value>(&entry.tags)
                        .unwrap_or(serde_json::Value::String(entry.tags.clone())),
                ),
                crate::db::sql::SqlParam::Text(entry.environment.clone()),
                opt_text(entry.branch.as_deref()),
                crate::db::sql::SqlParam::Text(entry.author.clone()),
                crate::db::sql::SqlParam::Int(entry.created_at),
                crate::db::sql::SqlParam::Int(entry.updated_at),
            ],
        )
        .map(|_| ())
    }

    fn find_knowledge_entry(
        &self,
        id: &str,
    ) -> Result<Option<crate::db::models::KnowledgeEntry>, Box<dyn std::error::Error>> {
        let rows = self.sql_query(
            "SELECT id, knowledge_type, title, content, element_qualified, user_story_id, \
                    feature_id, tags, environment, branch, author, created_at, updated_at \
             FROM knowledge_entries WHERE id = $1",
            &[crate::db::sql::SqlParam::Text(id.to_string())],
        )?;
        Ok(rows.first().map(knowledge_entry_from_row))
    }

    fn delete_knowledge_entry_by_id(&self, id: &str) -> Result<bool, Box<dyn std::error::Error>> {
        self.sql_execute(
            "DELETE FROM knowledge_entries WHERE id = $1",
            &[crate::db::sql::SqlParam::Text(id.to_string())],
        )
        .map(|n| n > 0)
    }

    fn search_knowledge_entries(
        &self,
        query: &str,
        knowledge_type: Option<&str>,
        environment: Option<&str>,
        limit: usize,
    ) -> Result<Vec<crate::db::models::KnowledgeEntry>, Box<dyn std::error::Error>> {
        // Legacy semantics: regex .*q.* over lowercase(title|content). ILIKE
        // with escaped wildcards is the SQL equivalent for substring match.
        let needle = format!("%{}%", escape_like(query));
        let mut sql = String::from(
            "SELECT id, knowledge_type, title, content, element_qualified, user_story_id, \
                    feature_id, tags, environment, branch, author, created_at, updated_at \
             FROM knowledge_entries WHERE (title ILIKE $1 OR content ILIKE $1)",
        );
        let mut params: Vec<crate::db::sql::SqlParam> =
            vec![crate::db::sql::SqlParam::Text(needle)];
        if let Some(kt) = knowledge_type {
            params.push(crate::db::sql::SqlParam::Text(kt.to_string()));
            sql.push_str(&format!(" AND knowledge_type = ${}", params.len()));
        }
        if let Some(env) = environment {
            params.push(crate::db::sql::SqlParam::Text(env.to_string()));
            sql.push_str(&format!(" AND environment = ${}", params.len()));
        }
        params.push(crate::db::sql::SqlParam::Int(limit as i64));
        sql.push_str(&format!(
            " ORDER BY updated_at DESC LIMIT ${}",
            params.len()
        ));
        let rows = self.sql_query(&sql, &params)?;
        Ok(rows.iter().map(knowledge_entry_from_row).collect())
    }

    fn list_knowledge_by_element(
        &self,
        element_qualified: &str,
    ) -> Result<Vec<crate::db::models::KnowledgeEntry>, Box<dyn std::error::Error>> {
        let rows = self.sql_query(
            "SELECT id, knowledge_type, title, content, element_qualified, user_story_id, \
                    feature_id, tags, environment, branch, author, created_at, updated_at \
             FROM knowledge_entries WHERE element_qualified = $1",
            &[crate::db::sql::SqlParam::Text(
                element_qualified.to_string(),
            )],
        )?;
        Ok(rows.iter().map(knowledge_entry_from_row).collect())
    }

    fn list_knowledge_by_feature(
        &self,
        feature_id: &str,
    ) -> Result<Vec<crate::db::models::KnowledgeEntry>, Box<dyn std::error::Error>> {
        let rows = self.sql_query(
            "SELECT id, knowledge_type, title, content, element_qualified, user_story_id, \
                    feature_id, tags, environment, branch, author, created_at, updated_at \
             FROM knowledge_entries WHERE feature_id = $1",
            &[crate::db::sql::SqlParam::Text(feature_id.to_string())],
        )?;
        Ok(rows.iter().map(knowledge_entry_from_row).collect())
    }

    fn list_knowledge_by_environment(
        &self,
        environment: &str,
        limit: usize,
    ) -> Result<Vec<crate::db::models::KnowledgeEntry>, Box<dyn std::error::Error>> {
        let rows = self.sql_query(
            "SELECT id, knowledge_type, title, content, element_qualified, user_story_id, \
                    feature_id, tags, environment, branch, author, created_at, updated_at \
             FROM knowledge_entries WHERE environment = $1 \
             ORDER BY updated_at DESC LIMIT $2",
            &[
                crate::db::sql::SqlParam::Text(environment.to_string()),
                crate::db::sql::SqlParam::Int(limit as i64),
            ],
        )?;
        Ok(rows.iter().map(knowledge_entry_from_row).collect())
    }

    // ---- SQL-first code_elements reads (W8 wave-2) ----

    fn find_element_by_key(
        &self,
        qualified_name: &str,
    ) -> Result<Option<crate::db::models::CodeElement>, Box<dyn std::error::Error>> {
        let rows = self.sql_query(
            "SELECT qualified_name, element_type, name, file_path, line_start, line_end, \
                    language, parent_qualified, cluster_id, cluster_label, metadata \
             FROM code_elements WHERE qualified_name = $1 LIMIT 1",
            &[crate::db::sql::SqlParam::Text(qualified_name.to_string())],
        )?;
        Ok(rows.first().map(code_element_from_row))
    }

    fn find_element_by_name_col(
        &self,
        name: &str,
    ) -> Result<Option<crate::db::models::CodeElement>, Box<dyn std::error::Error>> {
        let rows = self.sql_query(
            "SELECT qualified_name, element_type, name, file_path, line_start, line_end, \
                    language, parent_qualified, cluster_id, cluster_label, metadata \
             FROM code_elements WHERE name = $1 LIMIT 1",
            &[crate::db::sql::SqlParam::Text(name.to_string())],
        )?;
        Ok(rows.first().map(code_element_from_row))
    }

    fn elements_by_qualified_names(
        &self,
        qualified_names: &[String],
    ) -> Result<Vec<crate::db::models::CodeElement>, Box<dyn std::error::Error>> {
        const CHUNK: usize = 500;
        let mut out = Vec::with_capacity(qualified_names.len());
        for chunk in qualified_names.chunks(CHUNK) {
            let placeholders: Vec<String> = (1..=chunk.len()).map(|i| format!("${i}")).collect();
            let sql = format!(
                "SELECT qualified_name, element_type, name, file_path, line_start, line_end, \
                        language, parent_qualified, cluster_id, cluster_label, metadata, env \
                 FROM code_elements WHERE qualified_name IN ({})",
                placeholders.join(", ")
            );
            let params: Vec<crate::db::sql::SqlParam> = chunk
                .iter()
                .map(|qn| crate::db::sql::SqlParam::Text(qn.clone()))
                .collect();
            let rows = self.sql_query(&sql, &params)?;
            out.extend(rows.iter().map(code_element_from_row_env));
        }
        Ok(out)
    }

    /// FR-ENT-1: one multi-row INSERT for the whole batch (≤ 50 rows × 9
    /// params per flush, well under the 65535 bind limit).
    fn insert_audit_batch(
        &self,
        entries: &[crate::audit::AuditEntry],
    ) -> Result<(), Box<dyn std::error::Error>> {
        if tokio::runtime::Handle::try_current().is_ok() {
            tokio::task::block_in_place(|| self.insert_audit_batch_sync(entries))
        } else {
            self.insert_audit_batch_sync(entries)
        }
    }

    /// FR-ENT-1: chain head for recorder restarts. Errors when the audit_log
    /// table is absent — the recorder treats that as "disable after one warn".
    fn last_audit_entry_hash(&self) -> Result<Option<String>, Box<dyn std::error::Error>> {
        if tokio::runtime::Handle::try_current().is_ok() {
            tokio::task::block_in_place(|| self.last_audit_entry_hash_sync())
        } else {
            self.last_audit_entry_hash_sync()
        }
    }

    /// FR-ENT-1: windowed ledger read for `leankg audit export|verify`.
    fn query_audit(
        &self,
        since: Option<std::time::SystemTime>,
        until: Option<std::time::SystemTime>,
    ) -> Result<Vec<crate::audit::AuditEntry>, Box<dyn std::error::Error>> {
        if tokio::runtime::Handle::try_current().is_ok() {
            tokio::task::block_in_place(|| self.query_audit_sync(since, until))
        } else {
            self.query_audit_sync(since, until)
        }
    }

    /// H10 / FR-PLG-8: grouped usage-dashboard buckets (see trait docs).
    fn query_usage_aggregates(
        &self,
        since_cutoff: Option<i64>,
    ) -> Result<crate::dashboard::UsageAggregates, Box<dyn std::error::Error>> {
        if tokio::runtime::Handle::try_current().is_ok() {
            tokio::task::block_in_place(|| self.query_usage_aggregates_sync(since_cutoff))
        } else {
            self.query_usage_aggregates_sync(since_cutoff)
        }
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
    script: &str,
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
        script,
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
        // The legacy dialect name for the vector column is `vector`; the PG
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
fn datavalue_to_sql(v: &DataValue, col: &str) -> Box<dyn postgres::types::ToSql + Sync + Send> {
    match v {
        DataValue::Null => Box::new(Option::<String>::None),
        DataValue::Bool(b) => Box::new(*b),
        DataValue::Num(crate::db::value::Num::Int(i)) => Box::new(*i),
        DataValue::Num(crate::db::value::Num::Float(f)) => Box::new(*f),
        DataValue::Str(s) => Box::new(s.clone()),
        DataValue::Json(j) => Box::new(j.clone()),
        // The caller's NamedRows headers use the legacy dialect name
        // (`vector`); the PG column is `vec` (schema.sql). Match both.
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
    let (key, _legacy) = project_identity_keys_in(db_path, &cwd);
    schema_name_from_key(&key)
}

/// [`schema_for_path`] with an explicit base for RELATIVE db_path spellings
/// (`leankg index ./src` resolves `./src` against the invocation CWD).
/// Pure — tests pass a base instead of mutating the process CWD.
#[doc(hidden)]
pub fn schema_for_path_in(db_path: &std::path::Path, base: &std::path::Path) -> String {
    let (key, _legacy) = project_identity_keys_in(db_path, base);
    schema_name_from_key(&key)
}

/// Ordered Postgres-schema candidates for a project: `[0]` is the preferred
/// identity (what new writes should use); any further entries are LEGACY
/// keys kept for read-compatibility with pre-fix data (e.g. a relative
/// `project_path` that older builds keyed literally). Callers pick the first
/// candidate whose schema exists AND is populated; otherwise `[0]`.
pub fn schema_candidates_for_path(db_path: &std::path::Path) -> Vec<String> {
    let cwd = std::env::current_dir().unwrap_or_else(|_| std::path::PathBuf::from("."));
    let (key, legacy) = project_identity_keys_in(db_path, &cwd);
    let mut out = vec![schema_name_from_key(&key)];
    if let Some(l) = legacy {
        let name = schema_name_from_key(&l);
        if name != out[0] {
            out.push(name);
        }
    }
    out
}

fn schema_name_from_key(key: &str) -> String {
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
    // Lexical normalization: drop `.` and resolve `..` against the preceding
    // component (required when canonicalize fails because an ANCESTOR is a
    // symlink but leaf dirs don't exist yet — e.g. macOS /var/folders).
    let mut out = std::path::PathBuf::new();
    for c in absolute.components() {
        match c {
            std::path::Component::CurDir => {}
            std::path::Component::ParentDir => {
                out.pop();
            }
            other => out.push(other.as_os_str()),
        }
    }
    out
}

/// Resolve the identity key(s) for a project: `(preferred, Option<legacy>)`.
///
/// Preferred key: `project.project_path` from the project's leankg.yaml when
/// present, RESOLVED (relative values joined to the project root and
/// canonicalized, so `"./src"` and `"<root>/src"` spell the same identity);
/// else the canonical `.leankg`-stripped root path itself. Legacy key: when
/// the raw yaml value was a RELATIVE path, the pre-fix literal spelling is
/// returned too so data written by older builds stays reachable.
///
/// `db_path` may be a `.leankg` dir or a project root; both `<root>/leankg.yaml`
/// and `<root>/.leankg/leankg.yaml` are consulted (setup writes the latter).
fn project_identity_keys_in(
    db_path: &std::path::Path,
    base: &std::path::Path,
) -> (String, Option<String>) {
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
    // The `.leankg` store is the authoritative config location (written by
    // `leankg init` / setup); the root-level `leankg.yaml` is a legacy
    // duplicate kept readable. Checking the store FIRST prevents a stale
    // root-level anchor from out-voting the managed one when the two copies
    // diverge (cycle-2 R2a live proof: init's cwd copy anchored `<root>`
    // while the healed store anchored `<root>/src`).
    let dot_leankg_yaml = root.join(".leankg").join("leankg.yaml");
    let root_yaml = root.join("leankg.yaml");
    let mut configs: Vec<std::path::PathBuf> = vec![dot_leankg_yaml];
    if root_yaml != configs[0] {
        configs.push(root_yaml);
    }

    for cfg_path in &configs {
        let Ok(content) = std::fs::read_to_string(cfg_path) else {
            continue;
        };
        let Ok(config) = serde_yaml::from_str::<crate::config::ProjectConfig>(&content) else {
            continue;
        };
        let Some(pp) = config.project.project_path else {
            continue;
        };
        let raw = pp.to_string_lossy().to_string();
        let is_abs = pp.is_absolute();
        // Canonicalize BOTH branches so `./src` and `/abs/src` (possibly
        // through symlinked parents like macOS /tmp) converge on one key.
        let joined = if is_abs {
            pp.clone()
        } else {
            // Relative values were historically keyed LITERALLY ("./src"),
            // which silently re-scoped every read/write whenever the field
            // appeared or disappeared. Resolve them against the project root
            // instead so all spellings converge on one identity.
            root.join(&pp)
        };
        let resolved = std::fs::canonicalize(&joined).unwrap_or(joined);
        let legacy = if !is_abs { Some(raw) } else { None };
        return (resolved.to_string_lossy().to_string(), legacy);
    }

    (root.to_string_lossy().to_string(), None)
}

/// `Option<&str>` → bind param: `None` maps to SQL NULL (matches the legacy
/// Datalog `serde_json::Value::Null` bindings for optional columns).
fn opt_text(v: Option<&str>) -> crate::db::sql::SqlParam {
    match v {
        Some(s) => crate::db::sql::SqlParam::Text(s.to_string()),
        None => crate::db::sql::SqlParam::Null,
    }
}

/// Escape LIKE wildcards in user input so `search_knowledge` substring
/// semantics match the legacy `regex_matches(lowercase(x), ".*q.*")` scan.
fn escape_like(s: &str) -> String {
    s.replace('\\', "\\\\")
        .replace('%', "\\%")
        .replace('_', "\\_")
}

/// Map a knowledge_entries row onto the model — field-for-field identical
/// to db/mod.rs's `row_to_knowledge_entry` defaults.
fn knowledge_entry_from_row(r: &crate::db::sql::SqlRow) -> crate::db::models::KnowledgeEntry {
    crate::db::models::KnowledgeEntry {
        id: r.text("id").unwrap_or_default(),
        knowledge_type: r.text("knowledge_type").unwrap_or_else(|| "general".into()),
        title: r.text("title").unwrap_or_default(),
        content: r.text("content").unwrap_or_default(),
        element_qualified: r.text("element_qualified"),
        user_story_id: r.text("user_story_id"),
        feature_id: r.text("feature_id"),
        tags: r.text("tags").unwrap_or_else(|| "[]".into()),
        environment: r.text("environment").unwrap_or_else(|| "production".into()),
        branch: r.text("branch"),
        author: r.text("author").unwrap_or_default(),
        created_at: r.int("created_at").unwrap_or(0),
        updated_at: r.int("updated_at").unwrap_or(0),
    }
}

fn code_element_from_row(r: &crate::db::sql::SqlRow) -> crate::db::models::CodeElement {
    code_element_from_row_env_impl(r, "local")
}

fn code_element_from_row_env(r: &crate::db::sql::SqlRow) -> crate::db::models::CodeElement {
    code_element_from_row_env_impl(r, &r.text("env").unwrap_or_else(|| "local".into()))
}

/// Shared mapper for the W8 SQL-first `code_elements` reads. `with_env_col`
/// selects between the 11-column (env defaulted) and 12-column (env selected)
/// query shapes in `find_element_by_key` / `elements_by_qualified_names`.
fn code_element_from_row_env_impl(
    r: &crate::db::sql::SqlRow,
    env: &str,
) -> crate::db::models::CodeElement {
    crate::db::models::CodeElement {
        qualified_name: r.text("qualified_name").unwrap_or_default(),
        element_type: r.text("element_type").unwrap_or_default(),
        name: r.text("name").unwrap_or_default(),
        file_path: r.text("file_path").unwrap_or_default(),
        line_start: r.int("line_start").unwrap_or(0) as u32,
        line_end: r.int("line_end").unwrap_or(0) as u32,
        language: r.text("language").unwrap_or_default(),
        parent_qualified: r.text("parent_qualified"),
        cluster_id: r.text("cluster_id"),
        cluster_label: r.text("cluster_label"),
        metadata: r
            .text("metadata")
            .and_then(|s| serde_json::from_str(&s).ok())
            .unwrap_or(serde_json::json!({})),
        env: env.to_string(),
    }
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
        let schema = pick_schema_for_init(db_path);
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
    schema_state(schema).populated
}

/// Existence/population state of one per-project PG schema.
///
/// - `exists`: the schema owns a `code_elements` table (created+migrated).
/// - `populated`: that table holds at least one row.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct SchemaState {
    exists: bool,
    populated: bool,
}

/// Probe [`SchemaState`] for `schema`, safe to call from async contexts
/// (same `block_in_place` guard as the other sync-PG helpers).
fn schema_state(schema: &str) -> SchemaState {
    if tokio::runtime::Handle::try_current().is_ok() {
        tokio::task::block_in_place(|| schema_state_sync(schema))
    } else {
        schema_state_sync(schema)
    }
}

fn schema_state_sync(schema: &str) -> SchemaState {
    let base = match PostgresBackend::from_env() {
        Ok(pg) => pg.pg_url,
        Err(_) => {
            return SchemaState {
                exists: false,
                populated: false,
            }
        }
    };
    let Ok(mut client) = pg_connect(&base) else {
        return SchemaState {
            exists: false,
            populated: false,
        };
    };
    // Schema names come from schema_for_path (hex/hash, always a safe
    // identifier), so qualifying the table directly is injection-safe.
    let exists = client
        .query_one(
            &format!("SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = '{schema}')"),
            &[],
        )
        .map(|row| row.get(0))
        .unwrap_or(false);
    if !exists {
        return SchemaState {
            exists: false,
            populated: false,
        };
    }
    let populated = client
        .query_one(
            &format!("SELECT EXISTS (SELECT 1 FROM {schema}.code_elements LIMIT 1)"),
            &[],
        )
        .map(|row| row.get(0))
        .unwrap_or(false);
    SchemaState {
        exists: true,
        populated,
    }
}

/// Pure N2 decision: may the LEGACY candidate be adopted over the preferred?
///
/// Only when the preferred schema does not exist or holds no rows AND the
/// legacy candidate actually holds rows. The R2 sweep caught this adopting a
/// stale 13k-row legacy schema even though the freshly indexed preferred
/// schema was fully populated.
fn should_adopt_legacy(
    preferred_exists: bool,
    preferred_populated: bool,
    legacy_exists: bool,
    legacy_populated: bool,
) -> bool {
    (!preferred_exists || !preferred_populated) && legacy_exists && legacy_populated
}

/// Probe-driven candidate selection (pure so tests can fake the probes):
/// keep the preferred identity whenever it is populated; otherwise adopt the
/// first legacy candidate that is populated; else stay on the preferred name.
type SchemaProbe<'a> = &'a dyn Fn(&str) -> SchemaState;

fn pick_schema_from_candidates(candidates: &[String], probe: SchemaProbe<'_>) -> String {
    let Some(preferred) = candidates.first() else {
        return "leankg_p_default".to_string();
    };
    let pref = probe(preferred);
    if pref.populated || candidates.len() == 1 {
        return preferred.clone();
    }
    for legacy in candidates.iter().skip(1) {
        let lg = probe(legacy);
        if should_adopt_legacy(pref.exists, pref.populated, lg.exists, lg.populated) {
            tracing::warn!(
                "project identity: preferred schema {preferred} is missing or empty; \
                 adopting populated legacy schema {legacy}"
            );
            return legacy.clone();
        }
    }
    preferred.clone()
}

/// Pick the schema for a project init: the preferred identity candidate
/// unless it is missing/empty while a populated LEGACY candidate exists
/// (BUG-B self-heal for pre-fix data keyed on the literal relative
/// `project_path`). A populated preferred schema always wins — a stale
/// legacy schema must never hijack fresh data.
fn pick_schema_for_init(db_path: &std::path::Path) -> String {
    let candidates = schema_candidates_for_path(db_path);
    let probe: SchemaProbe<'_> = &|s| schema_state(s);
    pick_schema_from_candidates(&candidates, probe)
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

/// Test-only: a migrated scratch-schema [`PostgresBackend`] for the
/// SQL-first seam tests (W8 P0). Process-stable per key path, same mapping
/// contract as [`test_scratch_schema`].
#[cfg(test)]
pub(crate) fn test_sql_scratch_backend() -> std::sync::Arc<PostgresBackend> {
    let schema = test_scratch_schema(std::path::Path::new("/tmp/leankg-w8-sql-seam"))
        .expect("scratch schema for SQL seam tests");
    let pg = PostgresBackend::from_env()
        .expect("pg url")
        .with_schema(&schema);
    std::sync::Arc::new(pg)
}

/// Test-only read-only variant ([`test_sql_scratch_backend`]): every
/// statement goes through the RO pool (`default_transaction_read_only = on`),
/// so writes fail at the Postgres layer.
#[cfg(test)]
pub(crate) fn test_sql_scratch_backend_ro() -> std::sync::Arc<PostgresBackend> {
    let schema = test_scratch_schema(std::path::Path::new("/tmp/leankg-w8-sql-seam"))
        .expect("scratch schema for SQL seam tests");
    let pg = PostgresBackend::from_env_read_only()
        .expect("pg url")
        .with_schema(&schema);
    std::sync::Arc::new(pg)
}

/// Open a read-only backend (T6.1): `default_transaction_read_only = on` —
/// writes fail at the Postgres layer instead of relying on the legacy
/// embedded-engine same-handle workaround.
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
        let schema = pick_schema_for_init(db_path);
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

/// Like [`init_db_readonly`] but NEVER falls back to the shared `public`
/// layout: returns `Err` when no per-project schema exists for `db_path`.
/// Multi-tenant remote Postgres makes the public fallback dangerous — it can
/// silently serve another project's rows (doctor --deep reported TempDir
/// fixtures from an unrelated schema through it).
pub fn init_db_readonly_strict(
    db_path: &std::path::Path,
) -> Result<SharedDb, Box<dyn std::error::Error>> {
    #[cfg(test)]
    {
        return test_init_db(db_path);
    }
    #[allow(unreachable_code)]
    {
        let schema = pick_schema_for_init(db_path);
        if !schema_exists(&schema) {
            return Err(format!(
                "no per-project schema for {} (tried {schema}); run `leankg init` + `leankg index` first",
                db_path.display()
            )
            .into());
        }
        let pg = PostgresBackend::from_env_read_only()?.with_schema(&schema);
        tracing::info!(
            "DB engine = postgres read-only strict (default_transaction_read_only = on): {}",
            redact_url(&pg.pg_url)
        );
        Ok(Arc::new(pg))
    }
}

/// FR-ENT-1: read-only backend for `leankg audit export|verify`.
///
/// Pins to the first project-schema candidate that OWNS an `audit_log`
/// table. Unlike [`init_db_readonly`] — which pins only when the project's
/// code index is already populated — the ledger must stay readable while a
/// fresh project is still indexing (or was initialized but never indexed),
/// otherwise `audit verify` could not attest the very calls that performed
/// the indexing. Falls back to the legacy public layout when no candidate
/// carries a ledger.
pub fn init_db_readonly_audit(
    db_path: &std::path::Path,
) -> Result<SharedDb, Box<dyn std::error::Error>> {
    init_db_readonly_probed(db_path, "audit_log", "audit ledger")
}

/// H10 / FR-PLG-8 + FR-ENT-1 shared seam: read-only backend pinned to the
/// first project-schema candidate that owns `table`. The same rationale as
/// [`init_db_readonly_audit`] applies to any per-project ledger that must
/// outlive a (re)index — for the dashboard that's `context_metrics`, which
/// starts filling during the very first MCP session. Falls back to the
/// legacy public layout when no candidate carries the table.
pub fn init_db_readonly_probed(
    db_path: &std::path::Path,
    table: &str,
    label: &str,
) -> Result<SharedDb, Box<dyn std::error::Error>> {
    let probe = PostgresBackend::from_env_read_only()?;
    let mut chosen: Option<String> = None;
    // The CLI runs inside Tokio (#main block_on); the sync PG client must
    // leave the ambient runtime first — same guard as checkout().
    let probe_url = probe.pg_url.clone();
    let probe_fn =
        |chosen: &mut Option<String>, table: &str| -> Result<(), Box<dyn std::error::Error>> {
            let mut client = pg_connect(&probe_url)?;
            for schema in schema_candidates_for_path(db_path) {
                // Candidate names are hex/hash-derived identifiers (see
                // schema_name_from_key), so direct interpolation is safe —
                // same assumption as schema_state_sync.
                let q = format!(
                    "SELECT EXISTS (SELECT 1 FROM information_schema.tables \
                     WHERE table_schema = '{schema}' AND table_name = '{table}')"
                );
                if client
                    .query_one(&q, &[])
                    .map(|r| r.get::<_, bool>(0))
                    .unwrap_or(false)
                {
                    *chosen = Some(schema);
                    break;
                }
            }
            Ok(())
        };
    if tokio::runtime::Handle::try_current().is_ok() {
        tokio::task::block_in_place(|| probe_fn(&mut chosen, table))?;
    } else {
        probe_fn(&mut chosen, table)?;
    }
    match chosen {
        Some(schema) => {
            let pg = PostgresBackend::from_env_read_only()?.with_schema(&schema);
            tracing::info!(
                "{label} backend pinned to schema {schema}: {}",
                redact_url(&pg.pg_url)
            );
            Ok(Arc::new(pg))
        }
        None => Ok(Arc::new(probe)),
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
        // The legacy `DataValue` accessors survive on the new type.
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

    // ------------------------------------------------------------------
    // N2 (cycle-2 R2a): legacy-schema adoption must never hijack a
    // populated preferred schema.
    // ------------------------------------------------------------------

    /// Full decision matrix: adopt the legacy schema ONLY when the preferred
    /// schema is missing or EMPTY and the legacy candidate actually holds
    /// rows. A populated preferred schema always wins (the R2 sweep saw a
    /// stale 13k-row legacy schema pin over a fresh index).
    #[test]
    fn legacy_adoption_matrix() {
        type Row = (bool, bool, bool, bool);
        let cases: Vec<((bool, bool, bool, bool), bool)> = vec![
            // (pref_exists, pref_populated, legacy_exists, legacy_populated) => adopt?
            ((false, false, true, true), true),
            ((false, false, true, false), false),
            ((false, false, false, false), false),
            ((true, false, true, true), true),
            ((true, false, true, false), false),
            ((true, true, true, true), false),
            ((true, true, true, false), false),
            ((true, true, false, false), false),
            ((false, true, true, true), true), // contradictory input; "missing preferred" wins
            ((true, false, false, true), false), // impossible: populated implies exists
        ];
        for (row, expect) in cases {
            assert_eq!(
                should_adopt_legacy(row.0, row.1, row.2, row.3),
                expect,
                "matrix row {row:?}"
            );
        }
    }

    /// Candidate selection with FAKE probes: populated preferred beats an
    /// existing legacy schema; empty preferred adopts populated legacy;
    /// fully-empty state keeps the preferred name.
    #[test]
    fn pick_schema_from_candidates_uses_probe_matrix() {
        let candidates = vec![
            "leankg_p_preferred".to_string(),
            "leankg_p_legacy".to_string(),
        ];
        let probe = |s: &str| SchemaState {
            exists: true,
            populated: s.ends_with("preferred"),
        };
        assert_eq!(
            pick_schema_from_candidates(&candidates, &probe),
            "leankg_p_preferred",
            "populated preferred must NOT be hijacked by an existing legacy schema"
        );

        let probe_empty_preferred = |s: &str| SchemaState {
            exists: true,
            populated: s.ends_with("legacy"),
        };
        assert_eq!(
            pick_schema_from_candidates(&candidates, &probe_empty_preferred),
            "leankg_p_legacy",
            "empty preferred + populated legacy must adopt the legacy schema"
        );

        let probe_nothing = |_: &str| SchemaState {
            exists: false,
            populated: false,
        };
        assert_eq!(
            pick_schema_from_candidates(&candidates, &probe_nothing),
            "leankg_p_preferred",
            "nothing populated anywhere must keep the preferred identity"
        );
    }

    // ------------------------------------------------------------------
    // N3 (cycle-2 R2a): launcher-CWD must not leak into project identity.
    // ------------------------------------------------------------------

    /// Once `--project` is canonicalized at the entrypoint (what mcp-http now
    /// does before any schema derivation), the derived schema for one physical
    /// project is identical no matter which directory the server was launched
    /// from.
    #[test]
    fn canonicalized_project_derives_same_schema_from_any_cwd() {
        let project = TempDir::new().unwrap();
        std::fs::create_dir_all(project.path().join(".leankg")).unwrap();
        std::fs::write(
            project.path().join(".leankg").join("leankg.yaml"),
            format!(
                "project:\n  name: p\n  root: .\n  project_path: {}\n",
                project.path().display()
            ),
        )
        .unwrap();
        let elsewhere = TempDir::new().unwrap();

        // Launch A: process CWD = an unrelated directory.
        let db_a = canonical_project_root_in(project.path(), elsewhere.path());
        // Launch B: process CWD = the parent of the project itself.
        let db_b = canonical_project_root_in(project.path(), project.path());

        let schema_a = schema_for_path_in(&db_a, elsewhere.path());
        let schema_b = schema_for_path_in(&db_b, project.path());
        assert_eq!(
            schema_a, schema_b,
            "same --project from different launcher CWDs must pin one schema"
        );
        assert_eq!(
            schema_a,
            schema_for_path(&project.path().join(".leankg")),
            "pre-canonicalized launch agrees with direct derivation"
        );
    }

    /// Documents the bug shape: WITHOUT entrypoint canonicalization a
    /// relative `--project` spelling resolves against the launcher CWD and
    /// two launches pin DIFFERENT schemas.
    #[test]
    fn uncanonicalized_relative_project_leaks_launcher_cwd() {
        let base_a = TempDir::new().unwrap();
        let base_b = TempDir::new().unwrap();
        for base in [&base_a, &base_b] {
            std::fs::create_dir_all(base.path().join("fixture").join(".leankg")).unwrap();
        }
        let rel = std::path::Path::new("fixture/.leankg");
        assert_ne!(
            schema_for_path_in(rel, base_a.path()),
            schema_for_path_in(rel, base_b.path()),
            "relative spelling without pre-canonicalization depends on CWD"
        );
    }

    /// The `.leankg/leankg.yaml` store is the authoritative anchor location:
    /// when it and a stale root-level copy disagree (init's cwd duplicate vs
    /// the healed store), the STORE must win — otherwise per-request routing
    /// re-keys the project onto an empty schema while boot used the right
    /// one.
    #[test]
    fn dot_leankg_store_anchor_outvotes_stale_root_level_copy() {
        let dir = TempDir::new().unwrap();
        let root = dir.path();
        std::fs::create_dir_all(root.join(".leankg")).unwrap();
        std::fs::create_dir_all(root.join("src")).unwrap();

        // Stale root-level copy anchored at the repo root itself...
        std::fs::write(
            root.join("leankg.yaml"),
            format!(
                "project:\n  name: p\n  root: ./src\n  project_path: {}\n",
                root.display()
            ),
        )
        .unwrap();
        // ...while the managed store carries the writer's anchor.
        std::fs::write(
            root.join(".leankg").join("leankg.yaml"),
            "project:\n  name: p\n  root: ./src\n  languages: [rust]\n  project_path: ./src\n",
        )
        .unwrap();

        let expected = schema_for_path(&root.join("src"));
        let derived = schema_for_path(&root.join(".leankg"));
        assert_eq!(
            derived, expected,
            "store anchor (./src) must outvote the stale root-level copy"
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

    #[test]
    fn tls_config_parses_ca_bundle_from_file() {
        // Throwaway self-signed CA written to a temp file — no tracked certs.
        let pem = concat!(
            "-----BEGIN CERTIFICATE-----\n",
            "MIIBiDCCAS+gAwIBAgIUZ8I/ebL09vl5EmBDKPyQnAaJsBgwCgYIKoZIzj0EAwIw\n",
            "GTEXMBUGA1UEAwwObGVhbmtnLXRlc3QtY2EwIBcNMjYwOTA0MDY1NTAzWhgPMjEy\n",
            "NjA4MTEwNjU1MDNaMBkxFzAVBgNVBAMMDmxlYW5rZy10ZXN0LWNhMFkwEwYHKoZI\n",
            "zj0CAQYIKoZIzj0DAQcDQgAEPq8HFrmX8qLxEYNDHPXZmKXEkpYMdiR/aqVCVcun\n",
            "Ib/DhzOuO3Y7UgzMGCxuqGMvI3TMHX5fJkMh35XE+sAG0aNTMFEwHQYDVR0OBBYE\n",
            "FMR/fX12loln7tFdh3QvfJamoG51MB8GA1UdIwQYMBaAFMR/fX12loln7tFdh3Qv\n",
            "fJamoG51MA8GA1UdEwEB/wQFMAMBAf8wCgYIKoZIzj0EAwIDRwAwRAIgU5dBV25f\n",
            "ZKIuqpwC1E1bHA7E4zO7UuOUYKd4Fh5biSsCIBYPbT4/BM3/dJ/pzVbEp3SffOSR\n",
            "bFMPuVLIRoh04whB\n",
            "-----END CERTIFICATE-----\n",
        );
        let path = std::env::temp_dir().join(format!(
            "leankg-test-ca-{}-{}.pem",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .expect("clock")
                .as_nanos()
        ));
        std::fs::write(&path, pem).expect("write temp CA");
        let store =
            ca_root_store(Some(path.to_str().expect("utf8 path"))).expect("valid CA bundle");
        std::fs::remove_file(&path).ok();
        assert_eq!(store.roots.len(), 1, "bundle has exactly 1 cert");
    }

    #[test]
    fn tls_config_rejects_empty_ca_file() {
        let err = ca_root_store(Some("/dev/null")).expect_err("empty CA must fail");
        assert!(
            err.to_string().contains("no certificates parsed"),
            "unexpected error: {err}"
        );
    }

    #[test]
    fn tls_config_rejects_missing_ca_file() {
        let err = ca_root_store(Some("/nonexistent/ca.pem")).expect_err("missing CA must fail");
        assert!(
            err.to_string().contains("cannot read CA file"),
            "unexpected error: {err}"
        );
    }

    #[test]
    fn tls_config_without_ca_uses_mozilla_roots() {
        let store = ca_root_store(None).expect("webpki roots");
        assert!(
            store.roots.len() > 100,
            "Mozilla root store must be populated: {}",
            store.roots.len()
        );
    }
}
