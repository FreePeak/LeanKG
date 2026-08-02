#![allow(dead_code)]
use crate::db::schema::init_db;
use crate::graph::l1_cache::CachingGraphEngine;
use crate::graph::GraphEngine;
use crate::mcp::auth::AuthManager;
use crate::mcp::handler::ToolHandler;
use crate::mcp::tools::ToolRegistry;
use crate::mcp::tracker::WriteTracker;
use crate::mcp::watcher::start_watcher;
use crate::orchestrator::intent::IntentParser;
use axum::{
    body::Body,
    extract::{Query, State},
    http::{header, HeaderMap, Method, StatusCode},
    response::Response,
    routing::get,
    Router,
};
// use futures_util::StreamExt;  // Reserved for future streaming support
use moka::future::Cache;
use once_cell::sync::Lazy;
use parking_lot::RwLock;
use rmcp::handler::server::ServerHandler;
use rmcp::model::{CallToolRequestParams, CallToolResult, Content, ListToolsResult, Tool};
use rmcp::service::{serve_server, RoleServer};
use rmcp::transport::stdio;
use serde::{Deserialize, Serialize};
use std::collections::{HashMap, HashSet};
use std::fs;
use std::net::SocketAddr;
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, AtomicU32, Ordering};
use std::sync::Arc;
use std::time::{Duration, SystemTime};
use tokio::signal;
use tokio::sync::{Mutex as TokioMutex, RwLock as TokioRwLock};
use tower_http::cors::{Any, CorsLayer};

/// Tools that mutate the underlying DB or state. Everything else is treated
/// as a read at the dispatch layer (it may still go to the DB internally,
/// but its lock semantics differ).
static WRITE_TOOLS: Lazy<HashSet<&'static str>> = Lazy::new(|| {
    [
        "mcp_init",
        "mcp_index",
        "mcp_index_docs",
        "add_knowledge",
        "update_knowledge",
        "delete_knowledge",
        "add_annotation",
        "link_element",
        "add_documentation",
        "promote_environment",
        "embed_control",
        "ontology_control",
    ]
    .into_iter()
    .collect()
});

/// Build the per-server dispatch JSON response cache. Sized and TTL'd
/// independently of the engine-level caches inside `CachingGraphEngine` so
/// they can be tuned per deployment.
fn build_dispatch_cache() -> Cache<String, serde_json::Value> {
    let cap = std::env::var("LEANKG_L1_DISPATCH_SIZE")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(5_000u64);
    let ttl_secs = std::env::var("LEANKG_L1_DISPATCH_TTL")
        .ok()
        .and_then(|v| v.parse::<u64>().ok())
        .unwrap_or(60);
    Cache::builder()
        .max_capacity(cap)
        .time_to_live(Duration::from_secs(ttl_secs))
        .build()
}

/// Compose a deterministic cache key from `(tool_name, args_json)`. We rely on
/// serde_json's stable ordering for `serde_json::Map` (which is BTreeMap-backed
/// in the public API) — agents can't induce key collisions because every
/// `Map` field is escaped below.
fn dispatch_cache_key(
    tool_name: &str,
    args: &serde_json::Map<String, serde_json::Value>,
) -> String {
    let mut buf = String::with_capacity(tool_name.len() + 32);
    buf.push_str(tool_name);
    buf.push('|');
    // BTreeMap iteration order is sorted by key — stable across calls.
    for (k, v) in args.iter() {
        buf.push_str(k);
        buf.push('=');
        // Compact JSON form keeps the key compact and stable.
        match serde_json::to_string(v) {
            Ok(s) => buf.push_str(&s),
            Err(_) => buf.push('?'),
        }
        buf.push(';');
    }
    buf
}

/// Drop every L1 cache (engine + dispatch) for `server`. Free function so
/// the borrow checker stays happy inside `execute_tool`'s `&self` flow.
async fn invalidate_l1_caches(server: &MCPServer) {
    server.invalidate_l1_caches_public().await;
}

/// Session information for coordination between multiple LeanKG instances
#[derive(Debug, Serialize, Deserialize)]
struct SessionInfo {
    pid: u32,
    port: u16,
    started_at: String,
    db_path: String,
}

pub struct MCPServer {
    auth_manager: Arc<TokioRwLock<AuthManager>>,
    db_path: Arc<RwLock<PathBuf>>,
    graph_engine: Arc<parking_lot::Mutex<Option<GraphEngine>>>,
    graph_engine_cache: Arc<parking_lot::Mutex<HashMap<PathBuf, GraphEngine>>>,
    /// Per-project `CachingGraphEngine` keyed by project DB path. Built lazily
    /// on first read after a fresh `GraphEngine` is opened, and invalidated
    /// whenever the underlying engine is dropped (mcp_init / mcp_index /
    /// knowledge contribution tools).
    caching_engine_cache: Arc<RwLock<HashMap<PathBuf, CachingGraphEngine>>>,
    /// Dispatch-level JSON response cache for read tools. Keyed by
    /// `(tool_name, args_json)`. Sized by `LEANKG_L1_DISPATCH_SIZE` /
    /// `LEANKG_L1_DISPATCH_TTL` so it can be tuned independently of the
    /// engine-level caches inside `CachingGraphEngine`.
    dispatch_cache: Cache<String, serde_json::Value>,
    watch_path: Option<PathBuf>,
    write_tracker: Arc<WriteTracker>,
    intent_parser: IntentParser,
    /// Child API server processes managed by this instance (owned for proper cleanup)
    child_processes: Arc<TokioRwLock<HashMap<u16, u32>>>,
    /// Shutdown flag to signal when server should stop
    shutdown_flag: Arc<AtomicBool>,
    /// Port this server is bound to (for cleanup tracking)
    bound_port: Arc<AtomicU32>,
    /// Serializes MCP write/index operations so Cozo SQLite is not written concurrently.
    write_lock: Arc<TokioMutex<()>>,
    /// When true, the server rejects any tool that mutates state. Read tools
    /// (search_code, get_context, kg_*, etc.) still work; write tools return
    /// `"server is in read-only mode"` before being dispatched.
    read_only: bool,
}

impl std::fmt::Debug for MCPServer {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("MCPServer")
            .field("db_path", &self.db_path)
            .field("read_only", &self.read_only)
            .finish()
    }
}

impl Clone for MCPServer {
    fn clone(&self) -> Self {
        Self {
            auth_manager: self.auth_manager.clone(),
            db_path: self.db_path.clone(),
            graph_engine: self.graph_engine.clone(),
            graph_engine_cache: self.graph_engine_cache.clone(),
            caching_engine_cache: self.caching_engine_cache.clone(),
            dispatch_cache: self.dispatch_cache.clone(),
            watch_path: self.watch_path.clone(),
            write_tracker: self.write_tracker.clone(),
            intent_parser: IntentParser::new(),
            child_processes: self.child_processes.clone(),
            shutdown_flag: self.shutdown_flag.clone(),
            bound_port: self.bound_port.clone(),
            write_lock: self.write_lock.clone(),
            read_only: self.read_only,
        }
    }
}

impl MCPServer {
    pub fn new(db_path: std::path::PathBuf) -> Self {
        let effective_db_path = Self::resolve_project_root(db_path);
        Self {
            auth_manager: Arc::new(TokioRwLock::new(AuthManager::with_default_token())),
            db_path: Arc::new(RwLock::new(effective_db_path)),
            graph_engine: Arc::new(parking_lot::Mutex::new(None)),
            graph_engine_cache: Arc::new(parking_lot::Mutex::new(HashMap::new())),
            caching_engine_cache: Arc::new(RwLock::new(HashMap::new())),
            dispatch_cache: build_dispatch_cache(),
            watch_path: None,
            write_tracker: Arc::new(WriteTracker::new()),
            intent_parser: IntentParser::new(),
            child_processes: Arc::new(TokioRwLock::new(HashMap::new())),
            shutdown_flag: Arc::new(AtomicBool::new(false)),
            bound_port: Arc::new(AtomicU32::new(0)),
            write_lock: Arc::new(TokioMutex::new(())),
            read_only: false,
        }
    }

    pub fn new_with_watch(db_path: std::path::PathBuf, watch_path: std::path::PathBuf) -> Self {
        let effective_db_path = Self::resolve_project_root(db_path);
        Self {
            auth_manager: Arc::new(TokioRwLock::new(AuthManager::with_default_token())),
            db_path: Arc::new(RwLock::new(effective_db_path)),
            graph_engine: Arc::new(parking_lot::Mutex::new(None)),
            graph_engine_cache: Arc::new(parking_lot::Mutex::new(HashMap::new())),
            caching_engine_cache: Arc::new(RwLock::new(HashMap::new())),
            dispatch_cache: build_dispatch_cache(),
            watch_path: Some(watch_path),
            write_tracker: Arc::new(WriteTracker::new()),
            intent_parser: IntentParser::new(),
            child_processes: Arc::new(TokioRwLock::new(HashMap::new())),
            shutdown_flag: Arc::new(AtomicBool::new(false)),
            bound_port: Arc::new(AtomicU32::new(0)),
            write_lock: Arc::new(TokioMutex::new(())),
            read_only: false,
        }
    }

    /// Toggle the read-only flag. Builder-style; returns `self` so callers
    /// can chain `MCPServer::new(db_path).with_read_only(read_only)`.
    pub fn with_read_only(mut self, read_only: bool) -> Self {
        self.read_only = read_only;
        self
    }

    /// Returns true if this server is currently running in read-only mode.
    pub fn is_read_only(&self) -> bool {
        self.read_only
    }

    /// Read leankg.yaml and resolve project root with fallback chain:
    /// 1. project_path from config (if exists and valid)
    /// 2. project.root relative path resolution
    /// 3. Original db_path as fallback
    fn resolve_project_root(db_path: std::path::PathBuf) -> std::path::PathBuf {
        let config_path = db_path.join("leankg.yaml");
        if !config_path.exists() {
            return db_path;
        }

        let content = match std::fs::read_to_string(&config_path) {
            Ok(c) => c,
            Err(_) => return db_path,
        };

        let config: crate::config::ProjectConfig = match serde_yaml::from_str(&content) {
            Ok(c) => c,
            Err(_) => return db_path,
        };

        // 1. Check project_path first (absolute path stored at init time)
        if let Some(project_path) = config.project.project_path {
            let db_at_path = project_path.join(".leankg");
            if db_at_path.is_dir() {
                tracing::info!(
                    "Using project_path from leankg.yaml: {}",
                    db_at_path.display()
                );
                return db_at_path;
            } else {
                tracing::warn!(
                    "project_path in leankg.yaml points to non-existent directory: {}. Searching for project...",
                    project_path.display()
                );
            }
        }

        // 2. If root is not ".", check if that directory has its own .leankg
        let root = &config.project.root;
        if root.as_os_str() != "." && root.as_os_str() != "" {
            // Resolve root relative to db_path's parent (project root)
            let project_root = db_path.parent().unwrap_or(&db_path);
            let resolved_root = if root.is_absolute() {
                root.clone()
            } else {
                project_root.join(root)
            };

            // Check if root or its parent has .leankg
            let alternative_db = resolved_root.join(".leankg");
            if alternative_db.is_dir() && alternative_db != db_path {
                tracing::info!(
                    "Using project root from leankg.yaml: {}",
                    alternative_db.display()
                );
                return alternative_db;
            }

            // Check parent of resolved root
            if let Some(parent) = resolved_root.parent() {
                let parent_db = parent.join(".leankg");
                if parent_db.is_dir() && parent_db != db_path {
                    tracing::info!(
                        "Using parent project from leankg.yaml: {}",
                        parent_db.display()
                    );
                    return parent_db;
                }
            }
        }

        // 3. Fall back to original db_path
        tracing::debug!("Using default db_path: {}", db_path.display());
        db_path
    }

    pub fn db_path(&self) -> std::sync::Arc<parking_lot::RwLock<std::path::PathBuf>> {
        self.db_path.clone()
    }

    fn get_db_path(&self) -> std::path::PathBuf {
        self.db_path.read().clone()
    }

    fn find_leankg_for_path(path: &str) -> Option<PathBuf> {
        let path = if path.starts_with('/') {
            PathBuf::from(path)
        } else {
            std::env::current_dir().ok()?.join(path)
        };

        for ancestor in path.ancestors() {
            let leankg_path = ancestor.join(".leankg");
            if leankg_path.is_dir() {
                return Some(leankg_path);
            }
            if ancestor.join("leankg.yaml").exists() && leankg_path.exists() {
                return Some(leankg_path);
            }
        }
        None
    }

    /// Resolve `<project>/.leankg` for MCP `project=` routing (multi-mount RocksDB).
    fn resolve_project_db_path(fp: &str) -> Option<PathBuf> {
        if let Some(found) = Self::find_leankg_for_path(fp) {
            return Some(found);
        }
        let path = if fp.starts_with('/') {
            PathBuf::from(fp)
        } else {
            std::env::current_dir().ok()?.join(fp)
        };
        let project_root = if path.is_file() {
            path.parent()?.to_path_buf()
        } else {
            path
        };
        if !project_root.is_dir() {
            return None;
        }
        let candidate = project_root.join(".leankg");
        let rocksdb = std::env::var("LEANKG_DB_ENGINE")
            .unwrap_or_else(|_| "sqlite".to_string())
            .eq_ignore_ascii_case("rocksdb");
        if rocksdb {
            let central = crate::db::schema::central_project_storage_path(&candidate);
            if central.join("data/CURRENT").exists() || central.join("manifest").exists() {
                return Some(candidate);
            }
        }
        if candidate.is_dir() {
            return Some(candidate);
        }
        None
    }

    fn get_graph_engine_for_path(&self, file_path: Option<&String>) -> Result<GraphEngine, String> {
        let project_db_path = if let Some(fp) = file_path {
            if let Some(leankg_path) = Self::resolve_project_db_path(fp.as_str()) {
                tracing::debug!(
                    "Routing query for '{}' to database at {}",
                    fp,
                    leankg_path.display()
                );
                leankg_path
            } else {
                tracing::debug!("No .leankg found for '{}', using default db_path", fp);
                self.get_db_path()
            }
        } else {
            Self::resolve_project_db_path(".")
                .or_else(|| Self::find_leankg_for_path("."))
                .unwrap_or_else(|| self.get_db_path())
        };

        let project_db_path = match project_db_path.canonicalize() {
            Ok(p) => p,
            Err(_) if project_db_path.is_absolute() => project_db_path,
            Err(_) => std::env::current_dir()
                .map(|d| d.join(&project_db_path))
                .map_err(|e| format!("Failed to resolve db path: {}", e))?,
        };

        let rocksdb_central_ok = {
            let rocksdb = std::env::var("LEANKG_DB_ENGINE")
                .unwrap_or_else(|_| "sqlite".to_string())
                .eq_ignore_ascii_case("rocksdb");
            rocksdb && {
                let central = crate::db::schema::central_project_storage_path(&project_db_path);
                central.join("data/CURRENT").exists() || central.join("manifest").exists()
            }
        };

        if !project_db_path.exists() && !rocksdb_central_ok {
            return Err(
                "LeanKG not initialized. No .leankg directory found. Run 'leankg init' first."
                    .to_string(),
            );
        }

        // Single critical section: cache check + init_db + cache insert.
        // The Mutex must be held across init_db so concurrent callers serialize
        // the RocksDB open (one-writer-per-path) instead of racing.
        let mut cache = self.graph_engine_cache.lock();
        if let Some(ge) = cache.get(&project_db_path) {
            return Ok(ge.clone());
        }

        tracing::debug!("Initializing database at: {}", project_db_path.display());
        let db = if self.read_only {
            crate::db::schema::init_db_readonly(&project_db_path)
                .map_err(|e| format!("Database error: {}", e))?
        } else {
            init_db(&project_db_path).map_err(|e| format!("Database error: {}", e))?
        };
        let ge = GraphEngine::with_persistence(db);
        cache.insert(project_db_path.clone(), ge.clone());
        Ok(ge)
    }

    pub async fn auth_manager_read(&self) -> tokio::sync::RwLockReadGuard<'_, AuthManager> {
        self.auth_manager.read().await
    }

    fn get_graph_engine(&self) -> Result<GraphEngine, String> {
        // Route through the path-keyed cache so request handlers and the
        // background auto-index share the SAME DbInstance handle. Without
        // this unification, two separate caches each open their own
        // RocksDB handle to the same path and the second handle fails with
        // "lock hold by current process".
        self.get_graph_engine_for_path(None)
    }

    /// Run kg_self_test and log the result. Designed to be called once at
    /// MCP HTTP server startup, immediately after the listener is bound.
    /// Never panics and never blocks request handling -- best-effort
    /// visibility tool. See step 4 of the ontology self-test plan.
    fn run_kg_self_test_on_startup(&self) {
        // Lock the shared GraphEngine directly (not via get_graph_engine()
        // which clones the engine and its DB handle). Cloning the
        // CozoDB/RocksDB handle leaves a session that holds a
        // per-process RocksDB write lock until the next restart; calling
        // self-test on the shared handle reuses the existing session.
        let guard = self.graph_engine.lock();
        let ge = match &*guard {
            Some(ge) => ge,
            None => {
                tracing::warn!("kg_self_test skipped at startup: graph engine not yet initialised");
                return;
            }
        };
        let query_engine = crate::ontology::OntologyQueryEngine::new(ge.db().clone());
        let report = query_engine.self_test();

        if report.all_ok {
            tracing::info!(
                "kg_self_test: OK (code_elements={} cols, relationships={} cols)",
                report.code_elements.arity,
                report.relationships.arity
            );
            return;
        }

        if !report.code_elements.canonical {
            tracing::warn!(
                "kg_self_test: code_elements schema is non-canonical ({} cols, expected 13). \
                 Run the canonical repair migration or rebuild the index. Columns present: {:?}",
                report.code_elements.arity,
                report.code_elements.columns
            );
        }
        if !report.relationships.canonical {
            tracing::warn!(
                "kg_self_test: relationships schema is non-canonical ({} cols, expected 6). \
                 Run the canonical repair migration or rebuild the index. Columns present: {:?}",
                report.relationships.arity,
                report.relationships.columns
            );
        }
        for (name, entry) in [
            ("kg_context", &report.kg_context),
            ("kg_concept_map", &report.kg_concept_map),
            ("kg_trace_workflow", &report.kg_trace_workflow),
            ("kg_ontology_status", &report.kg_ontology_status),
        ] {
            if !entry.ok {
                let msg = entry.error.as_deref().unwrap_or("(no error message)");
                tracing::warn!("kg_self_test: {} FAILED at startup: {}", name, msg);
            }
        }
        tracing::warn!(
            "kg_self_test: one or more kg_* tools are unhealthy. Agents relying on kg_* may \
             see -32603 errors. Call kg_self_test via MCP for the full report."
        );
    }

    /// Parse the `LEANKG_VACUUM_INTERVAL_HOURS` env var.
    /// Returns `None` if the scheduler should be disabled (`0` or negative).
    /// Falls back to the default 1 hour if the var is unset or unparseable.
    fn parse_vacuum_interval() -> Option<Duration> {
        let raw = std::env::var("LEANKG_VACUUM_INTERVAL_HOURS")
            .ok()
            .unwrap_or_else(|| "1".to_string());
        let hours: i64 = match raw.parse() {
            Ok(n) => n,
            Err(_) => return Some(Duration::from_secs(3600)),
        };
        if hours <= 0 {
            return None;
        }
        Some(Duration::from_secs((hours as u64).saturating_mul(3600)))
    }

    /// Spawn a tokio task that periodically calls `GraphEngine::vacuum()` to
    /// reclaim free pages in the active CozoDB store. Skips ticks where the
    /// engine is not yet initialized. Exits cleanly on shutdown.
    ///
    /// Configuration: `LEANKG_VACUUM_INTERVAL_HOURS` (default `1`, `0` disables).
    /// The vacuum is a no-op on RocksDB backends (Cozo's RocksDB backend does
    /// not support `VACUUM`); in that case the tick is logged at debug level.
    fn spawn_vacuum_scheduler(&self) {
        let interval = match Self::parse_vacuum_interval() {
            Some(d) => d,
            None => {
                tracing::info!("Vacuum scheduler disabled (LEANKG_VACUUM_INTERVAL_HOURS=0)");
                return;
            }
        };
        let interval_hours = interval.as_secs() / 3600;
        let shutdown_flag = self.shutdown_flag.clone();
        let graph_engine = self.graph_engine.clone();

        tokio::spawn(async move {
            tracing::info!(
                "Vacuum scheduler started: running every {} hour(s)",
                interval_hours
            );
            loop {
                tokio::time::sleep(interval).await;
                if shutdown_flag.load(Ordering::SeqCst) {
                    tracing::info!("Vacuum scheduler shutting down");
                    break;
                }
                let result = {
                    let guard = graph_engine.lock();
                    (*guard).as_ref().map(|engine| engine.vacuum())
                };
                match result {
                    Some(Ok(())) => {
                        tracing::info!("Vacuum tick: ok");
                    }
                    Some(Err(e)) => {
                        // Cozo's RocksDB backend returns an error (no-op).
                        // Log at debug to avoid noise; warn only for anything
                        // unexpected (e.g. a real Sqlite error).
                        let msg = e.to_string();
                        if msg.to_lowercase().contains("vacuum") {
                            tracing::debug!("Vacuum tick: {}", msg);
                        } else {
                            tracing::warn!("Vacuum tick failed: {}", msg);
                        }
                    }
                    None => {
                        tracing::debug!("Vacuum tick: engine not initialized");
                    }
                }
            }
        });
    }

    /// Spawn a memory-pressure watchdog. Polls RSS every
    /// `LEANKG_GC_POLL_SECS` (default 10) and runs the in-RAM
    /// release callback when the daemon has been idle past
    /// `LEANKG_GC_IDLE_AFTER_SECS` (default 60) — **once per idle
    /// period** — or when RSS exceeds `LEANKG_GC_MAX_RSS_MB`
    /// (default 4096, force-trim throttled to 30s). Skips when
    /// caches are already empty; calls `trim_heap()` after a real
    /// release.
    fn spawn_gc_watchdog(&self) {
        let shutdown_flag = self.shutdown_flag.clone();
        let graph_engine = self.graph_engine.clone();
        tokio::spawn(async move {
            let mut guard = crate::gc::MemoryGuard::new(Some(Box::new(move || {
                let guard = graph_engine.lock();
                let Some(engine) = guard.as_ref() else {
                    return false;
                };
                // Skip when caches are already cold — avoids write-lock
                // churn and info-spam while the daemon stays idle.
                if !engine.is_cache_valid() {
                    return false;
                }
                engine.invalidate_cache();
                let _ = crate::gc::trim_heap();
                true
            })));
            loop {
                tokio::time::sleep(crate::gc::MemoryGuard::poll_interval()).await;
                if shutdown_flag.load(Ordering::SeqCst) {
                    break;
                }
                match guard.tick() {
                    crate::gc::GcAction::Skipped | crate::gc::GcAction::NoOp { .. } => {}
                    crate::gc::GcAction::IdleTrim { idle_secs, rss_mb } => {
                        tracing::info!(
                            "GC watchdog: idle {}s, RSS {} MB - released in-RAM caches",
                            idle_secs,
                            rss_mb
                        );
                    }
                    crate::gc::GcAction::ForceTrim { rss_mb } => {
                        tracing::warn!(
                            "GC watchdog: RSS {} MB exceeded cap; released in-RAM caches",
                            rss_mb
                        );
                    }
                }
            }
        });
    }

    /// Plan §"Part B Option 3" — in-process background embed.
    ///
    /// Spawns a detached thread that holds a clone of the same
    /// `CozoDb`/`GraphEngine` MCP is using, so the embed runs against
    /// the live DB without violating RocksDB's single-writer-per-process
    /// rule. Defaults to 1 worker / batch 32 — conservative for macOS
    /// RSS. Further capped by `LEANKG_EMBED_MAX_MB` (default 2048 on
    /// macOS). Operators can tune via env:
    ///
    /// - `LEANKG_EMBED_MAX_MB` (default 2048 macOS / 3072 else)
    /// - `LEANKG_EMBED_BACKGROUND_WORKERS` (default 1)
    /// - `LEANKG_EMBED_BACKGROUND_BATCH` (default 32)
    /// - `LEANKG_EMBED_BACKGROUND_TYPES` (default = heuristic)
    /// - `LEANKG_EMBED_BACKGROUND_FULL=1` to force a full re-embed
    /// - `LEANKG_EMBED_AUTO_ARM=1` arms the idle scheduler on first idle pass
    #[cfg(feature = "embeddings")]
    fn spawn_embed_idle_scheduler(&self) {
        let shutdown_flag = self.shutdown_flag.clone();
        let server = self.clone();
        tokio::spawn(async move {
            // First-pass auto-arm when LEANKG_EMBED_AUTO_ARM=1 (non-blocking
            // equivalent of LEANKG_EMBED_BACKGROUND=1, but with proper
            // partial=true + idle gating via embed_control).
            if !crate::embeddings::is_armed() {
                let auto_arm = std::env::var("LEANKG_EMBED_AUTO_ARM")
                    .ok()
                    .map(|v| v == "1" || v.eq_ignore_ascii_case("true"))
                    .unwrap_or(false);
                if auto_arm {
                    let cfg = Self::auto_arm_cfg_from_env();
                    crate::embeddings::control::arm_embed(cfg.clone());
                    tracing::info!(
                        "embed idle scheduler: auto-armed (workers={}, batch={}, full={})",
                        cfg.workers,
                        cfg.batch_size,
                        cfg.full
                    );
                }
            }
            loop {
                tokio::time::sleep(std::time::Duration::from_secs(2)).await;
                if shutdown_flag.load(std::sync::atomic::Ordering::SeqCst) {
                    break;
                }
                if !crate::embeddings::is_armed() {
                    continue;
                }
                if crate::embeddings::control::is_in_process_embed_active() {
                    continue;
                }
                if crate::embeddings::control::phase() == crate::embeddings::control::PHASE_RUNNING
                    || crate::embeddings::control::phase()
                        == crate::embeddings::control::PHASE_PAUSED
                {
                    continue;
                }
                if !crate::embeddings::control::mcp_is_idle_for_embed() {
                    crate::embeddings::control::set_phase(
                        crate::embeddings::control::PHASE_WAITING,
                    );
                    continue;
                }
                // RSS soft check
                let rss = crate::gc::MemoryGuard::rss_mb();
                let budget = crate::embeddings::resolve_partial_embed_budget_mb(0.0);
                if budget > 0 && rss > 0 && rss >= (budget * 95) / 100 {
                    tracing::info!(
                        "embed scheduler: RSS {} MB near budget {} MB; waiting",
                        rss,
                        budget
                    );
                    continue;
                }
                let Some(cfg) = crate::embeddings::control::take_armed_config() else {
                    continue;
                };
                tracing::info!("embed scheduler: idle+RSS ok; spawning partial resume embed");
                crate::embeddings::control::set_phase(crate::embeddings::control::PHASE_RUNNING);
                server.spawn_background_embed_with_config(cfg);
            }
        });
    }

    #[cfg(not(feature = "embeddings"))]
    fn spawn_embed_idle_scheduler(&self) {}

    /// Build a default `BackgroundEmbedConfig` from the
    /// `LEANKG_EMBED_BACKGROUND_*` env vars (used by `LEANKG_EMBED_AUTO_ARM=1`
    /// and by multi-project arming). Always sets `partial=true` so the duty
    /// cycle (yield + pause) keeps MCP responsive.
    #[cfg(feature = "embeddings")]
    fn auto_arm_cfg_from_env() -> crate::embeddings::BackgroundEmbedConfig {
        let w: usize = std::env::var("LEANKG_EMBED_BACKGROUND_WORKERS")
            .ok()
            .and_then(|v| v.parse().ok())
            .filter(|n: &usize| (1..=32).contains(n))
            .unwrap_or(1);
        let b: usize = std::env::var("LEANKG_EMBED_BACKGROUND_BATCH")
            .ok()
            .and_then(|v| v.parse().ok())
            .filter(|n: &usize| (1..=2048).contains(n))
            .unwrap_or(32);
        let f = std::env::var("LEANKG_EMBED_BACKGROUND_FULL")
            .ok()
            .map(|v| v == "1" || v.eq_ignore_ascii_case("true"))
            .unwrap_or(false);
        let types = std::env::var("LEANKG_EMBED_BACKGROUND_TYPES").unwrap_or_default();
        crate::embeddings::BackgroundEmbedConfig {
            batch_size: b,
            workers: w,
            full: f,
            types_filter: types,
            partial: true,
            rss_fraction: 0.0,
            project_path: None,
        }
    }

    /// Sequential arm for every project in `LEANKG_PROJECT_DIRS`. Each
    /// project embed runs against the same MCP `GraphEngine` only when its
    /// path equals `LEANKG_MCP_PROJECT`; side mounts need their own process
    /// (each project uses its own RocksDB subdirectory, so opening them
    /// inside MCP would require a second `CozoDb` handle — out of scope for
    /// the auto-arm path).
    ///
    /// ponytail: schedules one arm per project; first one runs while
    /// subsequent waits for `is_in_process_embed_active() == false`. Add a
    /// per-project `GraphEngine::open(...)` when side mounts must embed
    /// inside the same container.
    #[cfg(feature = "embeddings")]
    fn schedule_multi_project_arm(shutdown_flag: std::sync::Arc<std::sync::atomic::AtomicBool>) {
        tokio::spawn(async move {
            let dirs = std::env::var("LEANKG_PROJECT_DIRS").unwrap_or_default();
            let primary =
                std::env::var("LEANKG_MCP_PROJECT").unwrap_or_else(|_| "/workspace".to_string());
            let projects = Self::parse_project_dirs(&dirs);
            if projects.is_empty() {
                return;
            }
            for proj in projects {
                // Only arm the primary project from inside MCP; side mounts
                // log a one-liner and rely on the offline embed job.
                if !Self::is_primary_project(&proj, &primary) {
                    tracing::info!(
                        "embed multi-project: {} is a side mount; run `docker-compose.embed.yml --profile embed` against it for in-process embed",
                        proj
                    );
                    continue;
                }
                // Wait for any in-flight embed to finish before re-arming.
                while crate::embeddings::control::is_in_process_embed_active() {
                    if shutdown_flag.load(std::sync::atomic::Ordering::SeqCst) {
                        return;
                    }
                    tokio::time::sleep(std::time::Duration::from_secs(2)).await;
                }
                if shutdown_flag.load(std::sync::atomic::Ordering::SeqCst) {
                    return;
                }
                let cfg = Self::auto_arm_cfg_from_env();
                crate::embeddings::control::arm_embed(cfg);
                tracing::info!("embed multi-project: armed primary {}", primary);
                // One project at a time; loop top exits after primary completes
                // via the scheduler draining `take_armed_config()`.
                break;
            }
        });
    }

    /// Parse `LEANKG_PROJECT_DIRS` (comma-separated) into a deduped,
    /// sorted list of project paths. Empty / whitespace-only entries
    /// are skipped. Pure function — used by `schedule_multi_project_arm`
    /// and unit-tested.
    #[cfg(feature = "embeddings")]
    fn parse_project_dirs(dirs: &str) -> Vec<String> {
        let mut projects: Vec<String> = dirs
            .split(',')
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
            .collect();
        // De-dup so we don't arm the same primary twice.
        projects.sort();
        projects.dedup();
        projects
    }

    /// Whether a project path equals the primary container mount
    /// (`LEANKG_MCP_PROJECT`, default `/workspace`). Pure helper so the
    /// primary-filter logic stays unit-testable without a tokio runtime.
    #[cfg(feature = "embeddings")]
    fn is_primary_project(project: &str, primary: &str) -> bool {
        project == primary
    }

    #[cfg(feature = "embeddings")]
    fn spawn_background_embed_with_config(&self, cfg: crate::embeddings::BackgroundEmbedConfig) {
        // Resolve project-aware graph + .leankg dir before opening.
        let (graph, leankg_dir, project_label) = if let Some(ref proj) = cfg.project_path {
            match self.get_graph_engine_for_path(Some(proj)) {
                Ok(g) => {
                    let dir =
                        Self::resolve_project_db_path(proj).unwrap_or_else(|| self.get_db_path());
                    (g, dir, proj.clone())
                }
                Err(e) => {
                    tracing::warn!(
                        "embed_control spawn skipped; cannot open graph for {} ({})",
                        proj,
                        e
                    );
                    crate::embeddings::control::set_phase(crate::embeddings::control::PHASE_FAILED);
                    crate::embeddings::disarm_embed();
                    return;
                }
            }
        } else {
            match self.get_graph_engine() {
                Ok(g) => (g, self.get_db_path(), "<primary>".to_string()),
                Err(e) => {
                    tracing::warn!("embed_control spawn skipped; graph not ready ({})", e);
                    crate::embeddings::control::set_phase(crate::embeddings::control::PHASE_FAILED);
                    crate::embeddings::disarm_embed();
                    return;
                }
            }
        };
        // Mega + full requires force (cfg.full already gated by tool).
        let force_mega = std::env::var("LEANKG_EMBED_BACKGROUND_MEGA")
            .ok()
            .map(|v| v == "1" || v.eq_ignore_ascii_case("true"))
            .unwrap_or(false);
        if cfg.full && !force_mega && crate::ontology::safe_discover::is_mega_graph(&graph) {
            tracing::warn!(
                "embed_control: full rebuild refused on mega-graph (project={}) without MEGA=1",
                project_label
            );
            crate::embeddings::control::set_phase(crate::embeddings::control::PHASE_FAILED);
            crate::embeddings::disarm_embed();
            return;
        }
        match crate::embeddings::spawn_background_embed(graph, leankg_dir, cfg) {
            Ok(Some(h)) => tracing::info!(
                "embed_control spawned in-process embed pid={} project={}",
                h.pid,
                project_label
            ),
            Ok(None) => tracing::info!("embed_control: embed already running"),
            Err(e) => {
                tracing::error!("embed_control spawn failed: {}", e);
                crate::embeddings::control::set_phase(crate::embeddings::control::PHASE_FAILED);
                crate::embeddings::disarm_embed();
            }
        }
    }

    #[cfg(feature = "embeddings")]
    fn handle_embed_control(
        &self,
        arguments: &serde_json::Map<String, serde_json::Value>,
    ) -> Result<serde_json::Value, String> {
        let action = arguments
            .get("action")
            .and_then(|v| v.as_str())
            .unwrap_or("status");
        let project_path = arguments
            .get("project")
            .and_then(|v| v.as_str())
            .map(String::from);
        let leankg_dir = if let Some(ref p) = project_path {
            Self::resolve_project_db_path(p).unwrap_or_else(|| self.get_db_path())
        } else {
            self.get_db_path()
        };
        match action {
            "status" => {
                let mut status = crate::embeddings::embed_job_status(&leankg_dir);
                let graph_result = if let Some(ref p) = project_path {
                    self.get_graph_engine_for_path(Some(p))
                } else {
                    self.get_graph_engine()
                };
                if let Ok(graph) = graph_result {
                    if let Ok(pre) = crate::embeddings::embed_resume_preflight(graph.db()) {
                        status["resume_preflight"] = serde_json::json!({
                            "vectors_existing": pre.vectors_existing,
                            "fresh": pre.fresh,
                            "stale": pre.stale,
                            "has_embed_data": pre.has_embed_data,
                        });
                        // P0: flag a completed embed_status.json that the live
                        // vector store contradicts, instead of echoing it.
                        if crate::embeddings::control::file_status_is_stale(
                            status.get("file_status"),
                            pre.vectors_existing,
                        ) {
                            status["file_status_stale"] = serde_json::Value::Bool(true);
                        }
                    }
                    if let Ok(Some(inv)) =
                        crate::graph::inventory::load_latest_inventory(graph.db())
                    {
                        status["inventory"] = crate::graph::inventory::inventory_to_json(&inv);
                    }
                }
                if let Some(ref p) = project_path {
                    status["project"] = serde_json::Value::String(p.clone());
                }
                Ok(serde_json::json!({
                    "status": "ok",
                    "tool": "embed_control",
                    "data": status,
                }))
            }
            "off" => {
                crate::embeddings::request_cancel_in_process_embed();
                crate::embeddings::disarm_embed();
                crate::embeddings::control::set_phase(crate::embeddings::control::PHASE_CANCELLED);
                Ok(serde_json::json!({
                    "status": "ok",
                    "tool": "embed_control",
                    "data": {
                        "action": "off",
                        "phase": "cancelled",
                        "message": "cooperative cancel requested; armed cleared",
                    }
                }))
            }
            "on" => {
                let mode = arguments
                    .get("mode")
                    .and_then(|v| v.as_str())
                    .unwrap_or("partial");
                let mut full = arguments
                    .get("full")
                    .and_then(|v| v.as_bool())
                    .unwrap_or(false);
                let force_full = arguments
                    .get("force_full")
                    .and_then(|v| v.as_bool())
                    .unwrap_or(false);
                let graph_for_check = if let Some(ref p) = project_path {
                    self.get_graph_engine_for_path(Some(p))
                } else {
                    self.get_graph_engine()
                };
                if let Ok(ref g) = graph_for_check {
                    if full && crate::ontology::safe_discover::is_mega_graph(g) && !force_full {
                        full = false;
                        tracing::warn!(
                            "embed_control on: cleared full on mega-graph (pass force_full=true to override)"
                        );
                    }
                }
                let workers = arguments
                    .get("workers")
                    .and_then(|v| v.as_u64())
                    .unwrap_or(1)
                    .clamp(1, 8) as usize;
                let batch_size = arguments
                    .get("batch_size")
                    .and_then(|v| v.as_u64())
                    .unwrap_or(32)
                    .clamp(1, 512) as usize;
                let rss_fraction = arguments
                    .get("rss_fraction")
                    .and_then(|v| v.as_f64())
                    .unwrap_or(0.40);
                let types_filter = arguments
                    .get("types")
                    .and_then(|v| v.as_str())
                    .unwrap_or("")
                    .to_string();
                let cfg = crate::embeddings::BackgroundEmbedConfig {
                    batch_size,
                    workers,
                    full,
                    types_filter,
                    partial: mode != "continuous",
                    rss_fraction,
                    project_path: project_path.clone(),
                };
                crate::embeddings::control::clear_cancel();
                crate::embeddings::arm_embed(cfg);
                Ok(serde_json::json!({
                    "status": "ok",
                    "tool": "embed_control",
                    "data": {
                        "action": "on",
                        "phase": "waiting_idle",
                        "mode": mode,
                        "full": full,
                        "message": "armed; will start when MCP idle and RSS headroom available",
                    }
                }))
            }
            other => Err(format!("unknown embed_control action '{}'", other)),
        }
    }

    #[cfg(not(feature = "embeddings"))]
    fn handle_embed_control(
        &self,
        _arguments: &serde_json::Map<String, serde_json::Value>,
    ) -> Result<serde_json::Value, String> {
        Err("embed_control requires the `embeddings` cargo feature".into())
    }

    /// FR-ONT-PROC-01: watch ontology YAML and re-sync without dropping HTTP.
    fn spawn_ontology_yaml_watcher_if_present(&self) {
        let project_root = match self.find_project_root() {
            Ok(p) => p,
            Err(e) => {
                tracing::debug!("ontology watcher skipped: {}", e);
                return;
            }
        };
        let graph = match self.get_graph_engine() {
            Ok(g) => g,
            Err(e) => {
                tracing::warn!("ontology watcher: graph not ready ({})", e);
                return;
            }
        };
        let me = self.clone();
        if crate::ontology::spawn_ontology_yaml_watcher(project_root, graph, move |_stats| {
            let mut guard = me.graph_engine.lock();
            *guard = None;
            let mut cache = me.graph_engine_cache.lock();
            cache.clear();
        })
        .is_some()
        {
            tracing::info!("Ontology YAML auto-sync watcher started");
        }
    }

    /// FR-ONT-PROC-03: idempotent ontology refresh after index (or explicit MCP sync).
    fn refresh_ontology_after_index(&self) {
        let project_root = match self.find_project_root() {
            Ok(p) => p,
            Err(_) => return,
        };
        let graph = match self.get_graph_engine() {
            Ok(g) => g,
            Err(e) => {
                tracing::warn!("ontology post-index sync skipped: {}", e);
                return;
            }
        };
        match crate::ontology::sync_for_project(&project_root, &graph) {
            Ok(stats) => {
                tracing::info!(
                    "Ontology refreshed (workflows={}, steps={})",
                    stats.workflows,
                    stats.workflow_steps
                );
                let mut guard = self.graph_engine.lock();
                *guard = None;
                let mut cache = self.graph_engine_cache.lock();
                cache.clear();
            }
            Err(e) => {
                tracing::debug!("Ontology post-index sync skipped: {}", e);
            }
        }
    }

    fn handle_ontology_control(
        &self,
        arguments: &serde_json::Map<String, serde_json::Value>,
    ) -> Result<serde_json::Value, String> {
        let action = arguments
            .get("action")
            .and_then(|v| v.as_str())
            .unwrap_or("status");
        let project_root = self.find_project_root()?;
        match action {
            "status" => {
                let mut status = crate::ontology::ontology_sync_status(&project_root);
                if let Ok(graph) = self.get_graph_engine() {
                    let q = crate::ontology::OntologyQueryEngine::new(graph.db().clone());
                    if let Ok(ont) = q.get_ontology_status() {
                        status["procedural_counts"] = serde_json::json!(ont.procedural_counts);
                        status["concept_counts"] = serde_json::json!(ont.concept_counts);
                    }
                }
                Ok(serde_json::json!({
                    "status": "ok",
                    "tool": "ontology_control",
                    "data": status,
                }))
            }
            "sync" => {
                let graph = self.get_graph_engine()?;
                let stats = crate::ontology::sync_for_project(&project_root, &graph)
                    .map_err(|e| format!("ontology sync failed: {}", e))?;
                {
                    let mut guard = self.graph_engine.lock();
                    *guard = None;
                }
                {
                    let mut cache = self.graph_engine_cache.lock();
                    cache.clear();
                }
                Ok(serde_json::json!({
                    "status": "ok",
                    "tool": "ontology_control",
                    "data": {
                        "action": "sync",
                        "stats": stats,
                        "message": "ontology YAML synced into served DB",
                    }
                }))
            }
            other => Err(format!("unknown ontology_control action '{}'", other)),
        }
    }

    #[cfg(feature = "embeddings")]
    fn spawn_background_embed_in_process(&self) {
        // Read tuning env once (default-friendly fallbacks).
        let workers: usize = std::env::var("LEANKG_EMBED_BACKGROUND_WORKERS")
            .ok()
            .and_then(|v| v.parse().ok())
            .filter(|n: &usize| (1..=32).contains(n))
            .unwrap_or(1);
        let batch_size: usize = std::env::var("LEANKG_EMBED_BACKGROUND_BATCH")
            .ok()
            .and_then(|v| v.parse().ok())
            .filter(|n: &usize| (1..=2048).contains(n))
            .unwrap_or(32);
        let types_filter = std::env::var("LEANKG_EMBED_BACKGROUND_TYPES").unwrap_or_default();
        let full = std::env::var("LEANKG_EMBED_BACKGROUND_FULL")
            .ok()
            .map(|v| v == "1" || v.eq_ignore_ascii_case("true"))
            .unwrap_or(false);

        // Try to get a shared GraphEngine clone. If we can't (DB not
        // initialized yet, etc.), log a warning and skip — the next
        // `leankg embed --wait` invocation can be used instead.
        let graph = match self.get_graph_engine() {
            Ok(g) => g,
            Err(e) => {
                tracing::warn!(
                    "LEANKG_EMBED_BACKGROUND=1 but graph engine not ready ({}); skipping in-process embed",
                    e
                );
                return;
            }
        };
        // Mega-graphs: in-process background embed calls all_elements() and
        // contends with MCP on RocksDB/memory, which makes search tools hang
        // or the container go unhealthy. Require an explicit opt-in.
        let force_mega = std::env::var("LEANKG_EMBED_BACKGROUND_MEGA")
            .ok()
            .map(|v| v == "1" || v.eq_ignore_ascii_case("true"))
            .unwrap_or(false);
        if !force_mega && crate::ontology::safe_discover::is_mega_graph(&graph) {
            let n = graph.count_elements().unwrap_or(0);
            tracing::warn!(
                "LEANKG_EMBED_BACKGROUND=1 skipped on mega-graph ({} elements). \
                 Use offline `leankg embed --wait` or set LEANKG_EMBED_BACKGROUND_MEGA=1 \
                 (not recommended under MCP mem_limit). Search tools stay available.",
                n
            );
            return;
        }
        let leankg_dir = self.get_db_path();
        let cfg = crate::embeddings::BackgroundEmbedConfig {
            batch_size,
            workers,
            full,
            types_filter,
            // Partial embed (serial + duty-cycle) keeps MCP responsive; full
            // rebuild only opts into the heavy parallel path via --full.
            partial: !full,
            rss_fraction: 0.0,
            project_path: None,
        };
        // Optional LEANKG_EMBED_BACKGROUND_PARTIAL override (advanced).
        let cfg = if let Ok(p) = std::env::var("LEANKG_EMBED_BACKGROUND_PARTIAL") {
            if p == "0" || p.eq_ignore_ascii_case("false") {
                crate::embeddings::BackgroundEmbedConfig {
                    partial: false,
                    ..cfg
                }
            } else if p == "1" || p.eq_ignore_ascii_case("true") {
                crate::embeddings::BackgroundEmbedConfig {
                    partial: true,
                    ..cfg
                }
            } else {
                cfg
            }
        } else {
            cfg
        };
        match crate::embeddings::spawn_background_embed(graph, leankg_dir.clone(), cfg) {
            Ok(Some(handle)) => {
                tracing::info!(
                    "In-process background embed started (PID {}, {} workers, batch {}, leankg_dir={})",
                    handle.pid,
                    workers,
                    batch_size,
                    leankg_dir.display()
                );
            }
            Ok(None) => {
                tracing::info!("Background embed already running; not spawning a new one");
            }
            Err(e) => {
                tracing::error!("Failed to spawn background embed: {}", e);
            }
        }
    }

    #[cfg(not(feature = "embeddings"))]
    fn spawn_background_embed_in_process(&self) {
        // Embeddings feature off — nothing to do.
    }

    pub async fn serve_stdio(&self) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        if let Err(e) = self.auto_init_if_needed().await {
            tracing::warn!(
                "Auto-init skipped: {}. Server will operate in uninitialized state.",
                e
            );
        }

        // Ensure API server is running (starts it if not)
        match self.ensure_api_server_running().await {
            Ok(port) => {
                tracing::info!("API server ready on port {}", port);
            }
            Err(e) => {
                tracing::warn!(
                    "Failed to ensure API server running: {}. Continuing anyway.",
                    e
                );
            }
        }

        if let Some(ref watch_path) = self.watch_path {
            let db_path = self.get_db_path();
            let watch_path = watch_path.clone();
            let shutdown = self.shutdown_flag.clone();
            match self.get_graph_engine() {
                Ok(ge) => {
                    tokio::spawn(async move {
                        let (tx, rx) = tokio::sync::mpsc::channel(100);
                        start_watcher(ge, db_path, watch_path, shutdown, rx).await;
                        let _ = tx; // silence unused warning
                    });
                    tracing::info!(
                        "Auto-indexing enabled for {}",
                        self.watch_path
                            .as_ref()
                            .unwrap_or(&std::path::PathBuf::from("?"))
                            .display()
                    );
                }
                Err(e) => {
                    tracing::warn!("Watcher skipped: {}", e);
                }
            }
        }

        self.spawn_ontology_yaml_watcher_if_present();

        // Background maintenance: periodically reclaim free pages via VACUUM.
        // See HLD §2.5 / PRD FR-10.
        self.spawn_vacuum_scheduler();
        self.spawn_gc_watchdog();
        self.spawn_embed_idle_scheduler();

        // Setup graceful shutdown for stdio mode
        let shutdown_flag = self.shutdown_flag.clone();
        let server = self.clone();
        tokio::spawn(async move {
            signal::ctrl_c().await.ok();
            tracing::info!("Shutdown signal received in stdio mode");
            shutdown_flag.store(true, Ordering::SeqCst);
            // For stdio, we just cleanup child processes - the transport will close naturally
            let mut children = server.child_processes.write().await;
            for (port, pid) in children.drain() {
                tracing::info!("Killing child API server on port {} (PID {})", port, pid);
                if let Err(e) = MCPServer::kill_process_by_pid(pid) {
                    tracing::warn!("Failed to kill child process {}: {}", pid, e);
                }
            }
        });

        let transport = stdio();
        let _running = serve_server(self.clone(), transport).await?;
        futures_util::future::pending().await
    }

    /// Check if the API server is running on the given port by connecting to it
    async fn is_api_server_running(port: u16) -> bool {
        let addr = SocketAddr::from(([127, 0, 0, 1], port));
        tokio::net::TcpStream::connect(addr).await.is_ok()
    }

    /// Ensure the API server is running, starting it if not
    /// Tracks the child process for proper cleanup on shutdown
    async fn ensure_api_server_running(
        &self,
    ) -> Result<u16, Box<dyn std::error::Error + Send + Sync>> {
        // Get port from environment or use default 9699
        let requested_port = std::env::var("LEANKG_API_PORT")
            .ok()
            .and_then(|p| p.parse().ok())
            .unwrap_or(9699);

        // First check if API server is already running on the requested/default port
        if Self::is_api_server_running(requested_port).await {
            tracing::info!("API server already running on port {}", requested_port);
            return Ok(requested_port);
        }

        // Find an available port starting from the requested port
        let port = Self::find_available_port(requested_port);

        // Check again if API server is running on the available port
        // (it might have started between our first check and find_available_port)
        if Self::is_api_server_running(port).await {
            tracing::info!("API server already running on port {}", port);
            return Ok(port);
        }

        // Find the current executable path
        let exe_path = std::env::current_exe()?;
        tracing::info!("Starting API server on port {} (exe: {:?})", port, exe_path);

        // Start API server as a background process
        // Run with LEANKG_API_PORT set to communicate the port
        let child = std::process::Command::new(&exe_path)
            .args(["api-serve", "--port", &port.to_string()])
            .env("LEANKG_API_PORT", port.to_string())
            .spawn();

        match child {
            Ok(child) => {
                tracing::info!("Spawned API server process (PID: {})", child.id());
                // Track child process for cleanup
                let mut children = self.child_processes.write().await;
                children.insert(port, child.id());
            }
            Err(e) => {
                tracing::warn!("Failed to spawn API server: {}. Continuing anyway.", e);
                return Ok(port);
            }
        }

        // Wait for server to start (check every 100ms for up to 5 seconds)
        for _ in 0..50 {
            tokio::time::sleep(tokio::time::Duration::from_millis(100)).await;
            if Self::is_api_server_running(port).await {
                tracing::info!("API server started on port {}", port);
                return Ok(port);
            }
        }

        tracing::warn!("API server may not be fully started yet on port {}", port);
        Ok(port)
    }

    /// Find an available port starting from the given port, incrementing if taken.
    /// Uses SO_REUSEADDR to handle TIME_WAIT state properly.
    fn find_available_port(start_port: u16) -> u16 {
        let mut port = start_port;
        while port < start_port + 100 {
            if Self::is_port_available(port) {
                return port;
            }
            port += 1;
        }
        start_port
    }

    /// Check if a port is available for binding using SO_REUSEADDR.
    fn is_port_available(port: u16) -> bool {
        let addr = SocketAddr::from(([0, 0, 0, 0], port));
        if let Ok(listener) = std::net::TcpListener::bind(addr) {
            // Set SO_REUSEPORT if available (macOS/BSD)
            #[cfg(unix)]
            {
                use std::os::fd::AsRawFd;
                let fd = listener.as_raw_fd();
                unsafe {
                    libc::setsockopt(
                        fd,
                        libc::SOL_SOCKET,
                        libc::SO_REUSEADDR,
                        &1 as *const i32 as *const libc::c_void,
                        std::mem::size_of::<i32>() as libc::socklen_t,
                    );
                }
            }
            // Drop the listener so the port is released for actual use
            drop(listener);
            return true;
        }
        false
    }

    /// Path to session coordination directory
    fn session_coord_dir(&self) -> PathBuf {
        self.get_db_path().join(".leankg_sessions")
    }

    /// Path to our session file
    fn session_file(&self, port: u16) -> PathBuf {
        self.session_coord_dir()
            .join(format!("session_{}.json", port))
    }

    /// Path to lock file for atomic port reservation
    fn lock_file(&self, port: u16) -> PathBuf {
        self.session_coord_dir().join(format!("port_{}.lock", port))
    }

    /// Attempt to acquire an exclusive lock on the port.
    /// Returns Ok(None) if lock acquired, Ok(Some(pid)) if another process holds it.
    fn try_acquire_port_lock(&self, port: u16) -> Result<Option<u32>, String> {
        let lock_path = self.lock_file(port);
        let coord_dir = self.session_coord_dir();

        // Ensure directory exists
        if let Err(e) = fs::create_dir_all(&coord_dir) {
            return Err(format!("Failed to create session dir: {}", e));
        }

        // Check for existing lock file
        if lock_path.exists() {
            if let Ok(contents) = fs::read_to_string(&lock_path) {
                if let Ok(pid) = contents.trim().parse::<u32>() {
                    // Check if process is still alive AND actually responds as the MCP server
                    // (PID recycling can cause false positives with kill -0 alone)
                    if Self::is_process_alive(pid) {
                        // Verify without creating a nested Tokio runtime. This method is called
                        // from async startup paths, so block_on here can panic.
                        let alive = Self::check_health_blocking(port);
                        if alive {
                            return Ok(Some(pid));
                        }
                        tracing::warn!(
                            "PID {} is alive but not our server on port {}, removing stale lock",
                            pid,
                            port
                        );
                    }
                }
            }
            // Stale lock - remove it
            let _ = fs::remove_file(&lock_path);
        }

        // Try to create lock file
        let pid = std::process::id();
        match fs::write(&lock_path, pid.to_string()) {
            Ok(_) => Ok(None),
            Err(e) => Err(format!("Failed to create lock file: {}", e)),
        }
    }

    /// Synchronous health check for use in non-async contexts
    async fn check_health_sync(port: u16) -> bool {
        let url = format!("http://127.0.0.1:{}/health", port);
        reqwest::Client::new()
            .get(&url)
            .timeout(Duration::from_millis(500))
            .send()
            .await
            .map(|r| r.status().is_success())
            .unwrap_or(false)
    }

    fn check_health_blocking(port: u16) -> bool {
        let url = format!("http://127.0.0.1:{}/health", port);
        reqwest::blocking::Client::builder()
            .timeout(Duration::from_millis(500))
            .build()
            .and_then(|client| client.get(url).send())
            .map(|r| r.status().is_success())
            .unwrap_or(false)
    }

    /// Check if a process is alive by sending signal 0
    fn is_process_alive(pid: u32) -> bool {
        std::process::Command::new("kill")
            .args(["-0", &pid.to_string()])
            .output()
            .map(|o| o.status.success())
            .unwrap_or(false)
    }

    /// Kill a process by PID
    fn kill_process_by_pid(pid: u32) -> Result<(), String> {
        std::process::Command::new("kill")
            .args(["-TERM", &pid.to_string()])
            .output()
            .map_err(|e| format!("Failed to send TERM: {}", e))?;

        // Wait briefly then check if it's dead, if not send SIGKILL
        std::thread::sleep(Duration::from_millis(500));
        if Self::is_process_alive(pid) {
            std::process::Command::new("kill")
                .args(["-KILL", &pid.to_string()])
                .output()
                .map_err(|e| format!("Failed to send KILL: {}", e))?;
        }
        Ok(())
    }

    /// Release the port lock if we own it
    fn release_port_lock(&self, port: u16) {
        let lock_path = self.lock_file(port);
        if lock_path.exists() {
            if let Ok(contents) = fs::read_to_string(&lock_path) {
                if let Ok(pid) = contents.trim().parse::<u32>() {
                    if pid == std::process::id() {
                        let _ = fs::remove_file(&lock_path);
                    }
                }
            }
        }
    }

    /// Check if a session is still alive by calling its health endpoint
    async fn is_session_alive(&self, port: u16) -> bool {
        let url = format!("http://127.0.0.1:{}/health", port);
        match reqwest::Client::new()
            .get(&url)
            .timeout(Duration::from_secs(1))
            .send()
            .await
        {
            Ok(resp) => resp.status().is_success(),
            Err(_) => false,
        }
    }

    /// Register our session, returns (should_start_server, existing_port)
    /// - If another session owns the port and is alive: (false, existing_port)
    /// - If we're the owner or no one else: (true, port)
    async fn register_session(
        &self,
        port: u16,
    ) -> Result<(bool, Option<u16>), Box<dyn std::error::Error + Send + Sync>> {
        let coord_dir = self.session_coord_dir();
        fs::create_dir_all(&coord_dir)?;

        // Check for existing sessions
        let entries = fs::read_dir(&coord_dir)?;
        for entry in entries.flatten() {
            let filename = entry.file_name();
            let filename_str = filename.to_string_lossy();

            // Skip our own session file
            let our_filename = format!("session_{}.json", port);
            if filename_str == our_filename {
                continue;
            }

            // Parse existing session
            if let Ok(contents) = fs::read_to_string(entry.path()) {
                if let Ok(session) = serde_json::from_str::<SessionInfo>(&contents) {
                    if session.port == port {
                        // Verify both PID liveness AND actual server health to avoid
                        // false positives from PID recycling
                        let pid_alive = Self::is_process_alive(session.pid);
                        let server_alive = self.is_session_alive(port).await;
                        if pid_alive && server_alive {
                            tracing::info!(
                                "Existing session {} is alive on port {}, reusing it",
                                session.pid,
                                port
                            );
                            return Ok((false, Some(port)));
                        }
                        if pid_alive && !server_alive {
                            tracing::warn!(
                                "Session PID {} alive but server not responding on port {}, cleaning stale session",
                                session.pid, port
                            );
                            let _ = fs::remove_file(entry.path());
                        } else {
                            let _ = fs::remove_file(entry.path());
                        }
                    }
                }
            }
        }

        // Write our session info
        let session = SessionInfo {
            pid: std::process::id(),
            port,
            started_at: SystemTime::now()
                .duration_since(SystemTime::UNIX_EPOCH)
                .map(|d| d.as_secs().to_string())
                .unwrap_or_else(|_| "0".to_string()),
            db_path: self.get_db_path().to_string_lossy().to_string(),
        };
        let json = serde_json::to_string_pretty(&session)?;
        fs::write(self.session_file(port), json)?;

        Ok((true, None))
    }

    /// Unregister our session on shutdown
    async fn unregister_session(&self, port: u16) {
        let session_path = self.session_file(port);
        if session_path.exists() {
            // Only delete if it's our PID (defensive)
            if let Ok(contents) = fs::read_to_string(&session_path) {
                if let Ok(session) = serde_json::from_str::<SessionInfo>(&contents) {
                    if session.pid == std::process::id() {
                        fs::remove_file(session_path).ok();
                    }
                }
            }
        }
    }

    pub async fn serve_http(
        &self,
        port: u16,
        auth_token: Option<String>,
        reuse: bool,
    ) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        // Session coordination: check if another instance is already running
        let (should_start, existing_port) = self.register_session(port).await?;
        if !should_start && !reuse {
            tracing::info!(
                "Session on port {} already running, waiting for it to be available...",
                existing_port.unwrap_or(port)
            );
            // Wait up to 60 seconds for the port to become available
            for i in 0..60 {
                tokio::time::sleep(Duration::from_secs(1)).await;
                if !self.is_session_alive(port).await {
                    tracing::info!("Previous session on port {} has stopped", port);
                    break;
                }
                if i % 10 == 9 {
                    tracing::info!("Still waiting for port {}...", port);
                }
            }
        } else if !should_start && reuse {
            // In reuse mode, check if existing server is alive and return success
            if self.is_session_alive(port).await {
                tracing::info!(
                    "Existing MCP HTTP server is running on port {}, reusing it (exit 0)",
                    port
                );
                std::process::exit(0);
            }
        }

        if let Err(e) = self.auto_init_if_needed().await {
            tracing::warn!(
                "Auto-init skipped: {}. Server will operate in uninitialized state.",
                e
            );
        }

        if let Some(ref watch_path) = self.watch_path {
            let db_path = self.get_db_path();
            let watch_path = watch_path.clone();
            let shutdown = self.shutdown_flag.clone();
            match self.get_graph_engine() {
                Ok(ge) => {
                    tokio::spawn(async move {
                        let (tx, rx) = tokio::sync::mpsc::channel(100);
                        start_watcher(ge, db_path, watch_path, shutdown, rx).await;
                        let _ = tx; // silence unused warning
                    });
                    tracing::info!(
                        "Auto-indexing enabled for {}",
                        self.watch_path
                            .as_ref()
                            .unwrap_or(&std::path::PathBuf::from("?"))
                            .display()
                    );
                }
                Err(e) => {
                    tracing::warn!("Watcher skipped: {}", e);
                }
            }
        }

        self.spawn_ontology_yaml_watcher_if_present();

        // Background maintenance: periodically reclaim free pages via VACUUM.
        // See HLD §2.5 / PRD FR-10.
        self.spawn_vacuum_scheduler();
        self.spawn_gc_watchdog();

        // Plan §"Part B Option 3" — in-process background embed. We
        // share the MCP's CozoDb handle (via GraphEngine::Arc<CozoDb>)
        // so we don't open a second RocksDB writer in the same process,
        // which RocksDB would reject. The worker is throttled (default
        // 2 workers, batch 64) so request threads keep their latency
        // budget while HNSW catches up. Progress is written to
        // `<leankg_dir>/embed_status.json` — agents polling
        // `leankg embed --status` see live numbers.
        if std::env::var("LEANKG_EMBED_BACKGROUND")
            .ok()
            .map(|v| v == "1" || v.eq_ignore_ascii_case("true"))
            .unwrap_or(false)
        {
            self.spawn_background_embed_in_process();
        }
        self.spawn_embed_idle_scheduler();

        // Multi-project arm (LEANKG_EMBED_AUTO_ARM=1 only): re-arms the
        // primary project after the current embed completes. Side mounts
        // log a hint and rely on the offline embed job.
        #[cfg(feature = "embeddings")]
        {
            let auto_arm = std::env::var("LEANKG_EMBED_AUTO_ARM")
                .ok()
                .map(|v| v == "1" || v.eq_ignore_ascii_case("true"))
                .unwrap_or(false);
            if auto_arm {
                let shutdown_for_arm = self.shutdown_flag.clone();
                Self::schedule_multi_project_arm(shutdown_for_arm);
            }
        }

        let server = Arc::new(HttpMcpServer {
            mcp_server: self.clone(),
            auth_token,
            auth_manager: AuthManager::with_default_token(),
        });

        let cors = CorsLayer::new()
            .allow_origin(Any)
            .allow_methods([Method::GET, Method::POST, Method::OPTIONS])
            .allow_headers(Any)
            .expose_headers([header::CONTENT_TYPE]);

        // Keep both SSE entrypoints:
        // - GET /mcp        — streamable-HTTP / modern Cursor clients
        // - GET /mcp/stream — legacy SSE fallback (Cursor falls back here)
        let app = Router::new()
            .route("/mcp", get(handle_sse_stream).post(handle_mcp_request))
            .route("/mcp/stream", get(handle_sse_stream))
            .route("/health", get(health_check))
            .layer(cors)
            .with_state(server);

        let addr = std::net::SocketAddr::from(([0, 0, 0, 0], port));

        // Acquire port lock before binding to prevent race conditions
        match self.try_acquire_port_lock(port) {
            Ok(Some(other_pid)) => {
                if reuse {
                    tracing::info!(
                        "Port {} locked by PID {}, server already running (exit 0)",
                        port,
                        other_pid
                    );
                    std::process::exit(0);
                } else {
                    tracing::info!(
                        "Port {} locked by PID {}, waiting for release...",
                        port,
                        other_pid
                    );
                    // Wait for lock to be released
                    for i in 0..60 {
                        tokio::time::sleep(Duration::from_secs(1)).await;
                        if self
                            .try_acquire_port_lock(port)
                            .map(|r| r.is_none())
                            .unwrap_or(false)
                        {
                            tracing::info!("Port {} released, acquiring lock", port);
                            break;
                        }
                        if i % 10 == 9 {
                            tracing::info!("Still waiting for port {}...", port);
                        }
                    }
                }
            }
            Ok(None) => {
                tracing::debug!("Acquired lock for port {}", port);
            }
            Err(e) => {
                tracing::warn!("Failed to acquire port lock: {}, proceeding anyway", e);
            }
        }

        // Bind with SO_REUSEADDR to handle TIME_WAIT and prevent "Address already in use"
        let std_listener = std::net::TcpListener::bind(addr)?;
        #[cfg(unix)]
        {
            use std::os::fd::AsRawFd;
            let fd = std_listener.as_raw_fd();
            unsafe {
                libc::setsockopt(
                    fd,
                    libc::SOL_SOCKET,
                    libc::SO_REUSEADDR,
                    &1 as *const i32 as *const libc::c_void,
                    std::mem::size_of::<i32>() as libc::socklen_t,
                );
            }
        }
        std_listener.set_nonblocking(true)?;
        let listener = tokio::net::TcpListener::from_std(std_listener)?;
        tracing::info!("MCP HTTP server listening on http://{}", addr);

        // The startup kg_self_test probe is intentionally skipped at
        // server boot. The CozoDB 0.2.2 / RocksDB binding holds a
        // per-process write lock on every cloned DbInstance until the
        // process restarts, so a startup probe against a cloned handle
        // would block every subsequent tool call with "lock hold by
        // current process". The probe is still available to agents via
        // the kg_self_test MCP tool (see mcp/tools.rs) -- it runs against
        // the shared engine handle per request and does not leak a
        // session. Operators wanting startup visibility should run
        // `docker logs leankg-leankg-1 | grep kg_self_test` immediately
        // after the first MCP tool call lands.

        // Track bound port for cleanup
        self.bound_port.store(port as u32, Ordering::SeqCst);

        // Perform graceful shutdown on signal
        let shutdown_flag = self.shutdown_flag.clone();
        let server = self.clone();
        let bound_port = port;

        tokio::spawn(async move {
            signal::ctrl_c().await.ok();
            tracing::info!("Shutdown signal received, cleaning up...");
            shutdown_flag.store(true, Ordering::SeqCst);
            server.cleanup_on_shutdown(bound_port).await;
        });

        // Use graceful shutdown with axum
        let shutdown_flag2 = self.shutdown_flag.clone();
        let graceful = tokio::task::spawn(async move {
            let mut interrupt_count = 0;
            loop {
                if shutdown_flag2.load(Ordering::SeqCst) {
                    interrupt_count += 1;
                    tracing::info!("Shutdown in progress... (signal {})", interrupt_count);
                    if interrupt_count >= 2 {
                        tracing::warn!("Forceful shutdown after {} interrupts", interrupt_count);
                        break;
                    }
                }
                tokio::time::sleep(Duration::from_millis(100)).await;
            }
        });

        tokio::select! {
            result = axum::serve(listener, app) => {
                match result {
                    Ok(_) => tracing::info!("HTTP server shutdown complete"),
                    Err(e) => tracing::error!("HTTP server error: {}", e),
                }
            }
            _ = graceful => {
                tracing::info!("Graceful shutdown triggered");
            }
        }

        // Cleanup on shutdown
        self.cleanup_on_shutdown(port).await;
        Ok(())
    }

    /// Cleanup resources on shutdown: release port lock, unregister session, kill child processes
    async fn cleanup_on_shutdown(&self, port: u16) {
        tracing::info!("Starting cleanup for port {}...", port);

        // 1. Release port lock
        self.release_port_lock(port);

        // 2. Unregister session
        self.unregister_session(port).await;

        // 3. Kill child API server processes
        let mut children = self.child_processes.write().await;
        for (child_port, child_pid) in children.drain() {
            tracing::info!(
                "Killing child API server on port {} (PID {})",
                child_port,
                child_pid
            );
            if let Err(e) = Self::kill_process_by_pid(child_pid) {
                tracing::warn!("Failed to kill child process {}: {}", child_pid, e);
            }
        }

        // 4. Drop cached GraphEngine handles so RocksDB LOCK can release.
        // Give the watcher (~250ms poll) a moment to exit after shutdown_flag.
        tokio::time::sleep(Duration::from_millis(300)).await;
        {
            let mut cache = self.graph_engine_cache.lock();
            let n = cache.len();
            cache.clear();
            if n > 0 {
                tracing::info!("Cleared {} cached GraphEngine handle(s)", n);
            }
        }
        {
            let mut ge = self.graph_engine.lock();
            *ge = None;
        }

        // 5. Remove PID file if exists
        let pid_file = self.get_db_path().join("leankg.pid");
        if pid_file.exists() {
            if let Ok(contents) = fs::read_to_string(&pid_file) {
                if let Ok(pid) = contents.trim().parse::<u32>() {
                    if pid == std::process::id() {
                        let _ = fs::remove_file(&pid_file);
                        tracing::info!("Removed PID file");
                    }
                }
            }
        }

        tracing::info!("Cleanup complete for port {}", port);
    }

    async fn auto_init_if_needed(&self) -> Result<(), String> {
        let project_root = self.find_project_root()?;

        let leankg_path = project_root.join(".leankg");
        let leankg_dir_exists = leankg_path.is_dir();
        let leankg_yaml_exists = project_root.join("leankg.yaml").exists();

        if leankg_path.exists() && !leankg_dir_exists {
            tracing::warn!(
                ".leankg exists but is not a directory. Removing and re-initializing..."
            );
            std::fs::remove_file(&leankg_path)
                .map_err(|e| format!("Failed to remove invalid .leankg file: {}", e))?;
        } else if leankg_dir_exists {
            tracing::info!(
                "LeanKG project already initialized at {}",
                project_root.display()
            );
            // Run the (potentially long-running) auto-index in the background
            // so the HTTP listener can bind immediately. Without this,
            // freshness-triggered incremental reindexes over a polyrepo
            // block the listener for tens of minutes and /health fails for
            // the entire duration.
            let me = self.clone();
            tokio::spawn(async move {
                if let Err(e) = me.auto_index_if_needed().await {
                    tracing::warn!("Background auto-index failed: {}", e);
                }
            });
            return Ok(());
        } else if leankg_yaml_exists {
            tracing::info!(
                "LeanKG config exists at {}, creating missing .leankg directory",
                project_root.display()
            );
        }

        tracing::info!("LeanKG not found, searching for project root...");

        let test_file = project_root.join(".leankg_write_test");
        if std::fs::write(&test_file, "test").is_err() {
            std::fs::remove_file(test_file).ok();
            return Err(format!(
                "Filesystem at {} is not writable: Read-only file system",
                project_root.display()
            ));
        }
        std::fs::remove_file(test_file).ok();

        std::fs::create_dir_all(&leankg_path)
            .map_err(|e| format!("Failed to create .leankg: {}", e))?;
        let config = crate::config::ProjectConfig::default();
        let config_yaml = serde_yaml::to_string(&config)
            .map_err(|e| format!("Failed to serialize config: {}", e))?;
        std::fs::write(project_root.join(".leankg/leankg.yaml"), config_yaml)
            .map_err(|e| format!("Failed to write config: {}", e))?;

        tracing::info!(
            "Auto-init: Created .leankg/ and leankg.yaml at {}",
            project_root.display()
        );

        let db_path = project_root.join(".leankg");
        tokio::fs::create_dir_all(&db_path)
            .await
            .map_err(|e| format!("Failed to create db path: {}", e))?;

        // Share the handle via the path cache — do not call init_db here
        // (RocksDB one-writer-per-path; watcher/tools must reuse this handle).
        let root_key = project_root.to_string_lossy().to_string();
        let graph_engine = self
            .get_graph_engine_for_path(Some(&root_key))
            .map_err(|e| format!("Database error: {}", e))?;
        let mut parser_manager = crate::indexer::ParserManager::new();
        parser_manager
            .init_parsers()
            .map_err(|e| format!("Parser init error: {}", e))?;

        let files = crate::indexer::find_files_sync(&root_key)
            .map_err(|e| format!("Find files error: {}", e))?;
        let mut indexed = 0;

        for file_path in &files {
            if crate::indexer::index_file_sync(&graph_engine, &mut parser_manager, file_path)
                .is_ok()
            {
                indexed += 1;
            }
        }

        tracing::info!("Auto-init: Indexed {} files", indexed);

        if let Err(e) = graph_engine.resolve_call_edges() {
            tracing::warn!("Auto-init: Failed to resolve call edges: {}", e);
        }

        if let Ok(true) = std::path::Path::new("docs").try_exists() {
            if let Ok(doc_result) = crate::doc_indexer::index_docs_directory(
                std::path::Path::new("docs"),
                &graph_engine,
            ) {
                tracing::info!(
                    "Auto-init: Indexed {} documents",
                    doc_result.documents.len()
                );
            }
        }

        {
            let mut db_path_guard = parking_lot::RwLock::write(&self.db_path);
            *db_path_guard = db_path.clone();
        }
        let mut ge_guard = self.graph_engine.lock();
        *ge_guard = Some(graph_engine);

        tracing::info!("Auto-init complete");
        Ok(())
    }

    async fn auto_index_if_needed(&self) -> Result<(), String> {
        let project_root = self.find_project_root()?;
        let config_path = project_root.join(".leankg/leankg.yaml");

        let config = if config_path.exists() {
            let content = std::fs::read_to_string(&config_path)
                .map_err(|e| format!("Failed to read config: {}", e))?;
            serde_yaml::from_str::<crate::config::ProjectConfig>(&content)
                .map_err(|e| format!("Failed to parse config: {}", e))?
        } else {
            crate::config::ProjectConfig::default()
        };

        if !config.mcp.auto_index_on_start {
            tracing::info!("Auto-indexing on start is disabled in config");
            return Ok(());
        }

        // FR-MG-AUTO-01: operators can skip freshness reindex without wiping
        // data (mega-graph Docker OOM escape hatch). Documented in AGENTS /
        // embed ops reports — previously referenced but unimplemented.
        if std::env::var("LEANKG_SKIP_FRESHNESS_CHECK")
            .map(|v| v == "1" || v.eq_ignore_ascii_case("true"))
            .unwrap_or(false)
        {
            tracing::info!("LEANKG_SKIP_FRESHNESS_CHECK set, skipping MCP auto-index on start");
            return Ok(());
        }

        let db_path = self.get_db_path();
        let db_file = db_path.join("leankg.db");

        if !db_file.exists() {
            tracing::info!("Database file does not exist, skipping auto-index");
            return Ok(());
        }

        let is_git = crate::indexer::git_workspace::has_git_context(&project_root);
        if config.mcp.require_git_for_auto_index && !is_git {
            tracing::info!(
                "No git repo (or nested repos) under {}, skipping auto-index",
                project_root.display()
            );
            return Ok(());
        }

        let last_commit_time = if !is_git {
            tracing::info!(
                "No git context under {} but require_git_for_auto_index=false, forcing reindex",
                project_root.display()
            );
            i64::MAX
        } else {
            match crate::indexer::git_workspace::workspace_last_commit_time(&project_root) {
                Ok(t) => {
                    tracing::info!(
                        "Git workspace freshness: last nested/root commit ts={} at {}",
                        t,
                        project_root.display()
                    );
                    t
                }
                Err(e) => {
                    tracing::warn!("Failed to get last commit time: {}", e);
                    return Ok(());
                }
            }
        };

        let db_modified = std::fs::metadata(&db_file)
            .and_then(|m| m.modified())
            .map(|t| {
                t.duration_since(std::time::UNIX_EPOCH)
                    .map(|d| d.as_secs() as i64)
                    .unwrap_or(0)
            })
            .unwrap_or(0);

        let threshold_seconds = (config.mcp.auto_index_threshold_minutes * 60) as i64;

        if last_commit_time <= db_modified + threshold_seconds {
            tracing::info!(
                "Index is fresh (last commit: {}, db modified: {}), skipping auto-index",
                last_commit_time,
                db_modified
            );
            return Ok(());
        }

        tracing::info!(
            "Index may be stale (last commit: {}, db modified: {}), running incremental index...",
            last_commit_time,
            db_modified
        );

        let graph_engine = self
            .get_graph_engine()
            .map_err(|e| format!("Database error: {}", e))?;
        let mut parser_manager = crate::indexer::ParserManager::new();
        parser_manager
            .init_parsers()
            .map_err(|e| format!("Parser init error: {}", e))?;

        let root_str = project_root.to_string_lossy().to_string();
        match crate::indexer::incremental_index_sync(&graph_engine, &mut parser_manager, &root_str)
            .await
        {
            Ok(result) => {
                tracing::info!(
                    "Auto-index: Processed {} files ({} elements)",
                    result.total_files_processed,
                    result.elements_indexed
                );
            }
            Err(e) => {
                tracing::warn!("Auto-index failed: {}, falling back to full index", e);
                let files = crate::indexer::find_files_sync(&root_str)
                    .map_err(|fe| format!("Find files error: {}", fe))?;
                let mut indexed = 0;
                for file_path in &files {
                    if crate::indexer::index_file_sync(
                        &graph_engine,
                        &mut parser_manager,
                        file_path,
                    )
                    .is_ok()
                    {
                        indexed += 1;
                    }
                }
                tracing::info!("Auto-index (fallback): Indexed {} files", indexed);
            }
        }

        if let Err(e) = graph_engine.resolve_call_edges() {
            tracing::warn!("Auto-index: Failed to resolve call edges: {}", e);
        }

        if let Ok(true) = project_root.join("docs").try_exists() {
            if let Ok(doc_result) = crate::doc_indexer::index_docs_directory(
                project_root.join("docs").as_path(),
                &graph_engine,
            ) {
                tracing::info!(
                    "Auto-index: Indexed {} documents",
                    doc_result.documents.len()
                );
            }
        }

        tracing::info!("Auto-index complete");

        self.refresh_ontology_after_index();

        {
            let mut guard = self.graph_engine.lock();
            *guard = None;
        }

        Ok(())
    }

    /// Ensure a specific project is indexed if needed (used for per-request auto-indexing)
    async fn ensure_project_indexed(&self, project_path: &str) -> Result<(), String> {
        let project_root = if project_path.starts_with('/') {
            PathBuf::from(project_path)
        } else {
            std::env::current_dir()
                .map_err(|e| format!("Failed to get current dir: {}", e))?
                .join(project_path)
        };

        let config_path = project_root.join(".leankg/leankg.yaml");
        let config = if config_path.exists() {
            let content = std::fs::read_to_string(&config_path)
                .map_err(|e| format!("Failed to read config: {}", e))?;
            serde_yaml::from_str::<crate::config::ProjectConfig>(&content)
                .map_err(|e| format!("Failed to parse config: {}", e))?
        } else {
            crate::config::ProjectConfig::default()
        };

        if !config.mcp.auto_index_on_start {
            return Ok(());
        }

        let db_path = project_root.join(".leankg");
        let db_file = db_path.join("leankg.db");

        if !db_file.exists() {
            tracing::debug!(
                "Database file does not exist at {}, skipping auto-index",
                db_file.display()
            );
            return Ok(());
        }

        // Check git status to determine if indexing is needed (supports nested multi-repo roots)
        let last_commit_time = if config.mcp.require_git_for_auto_index {
            if !crate::indexer::git_workspace::has_git_context(&project_root) {
                tracing::debug!(
                    "No git context under {}, skipping auto-index",
                    project_root.display()
                );
                return Ok(());
            }
            match crate::indexer::git_workspace::workspace_last_commit_time(&project_root) {
                Ok(t) => t,
                Err(e) => {
                    tracing::debug!(
                        "Failed to get last commit time for {}: {}, skipping auto-index",
                        project_root.display(),
                        e
                    );
                    return Ok(());
                }
            }
        } else {
            tracing::debug!(
                "require_git_for_auto_index=false, forcing reindex for {}",
                project_root.display()
            );
            i64::MAX
        };

        let db_modified = std::fs::metadata(&db_file)
            .and_then(|m| m.modified())
            .map(|t| {
                t.duration_since(std::time::UNIX_EPOCH)
                    .map(|d| d.as_secs() as i64)
                    .unwrap_or(0)
            })
            .unwrap_or(0);

        let threshold_seconds = (config.mcp.auto_index_threshold_minutes * 60) as i64;

        if last_commit_time <= db_modified + threshold_seconds {
            tracing::debug!(
                "Project {} index is fresh (last commit: {}, db modified: {}), skipping auto-index",
                project_root.display(),
                last_commit_time,
                db_modified
            );
            return Ok(());
        }

        tracing::info!(
            "Project {} index is stale, running incremental index...",
            project_root.display()
        );

        let graph_engine = self
            .get_graph_engine_for_path(Some(&project_root.to_string_lossy().to_string()))
            .map_err(|e| format!("Database error: {}", e))?;
        let mut parser_manager = crate::indexer::ParserManager::new();
        parser_manager
            .init_parsers()
            .map_err(|e| format!("Parser init error: {}", e))?;

        let root_str = project_root.to_string_lossy().to_string();
        match crate::indexer::incremental_index_sync(&graph_engine, &mut parser_manager, &root_str)
            .await
        {
            Ok(result) => {
                tracing::info!(
                    "Auto-index for {}: Processed {} files ({} elements)",
                    project_root.display(),
                    result.total_files_processed,
                    result.elements_indexed
                );
            }
            Err(e) => {
                tracing::warn!("Auto-index for {} failed: {}", project_root.display(), e);
                return Err(e.to_string());
            }
        }

        if let Err(e) = graph_engine.resolve_call_edges() {
            tracing::warn!("Auto-index: Failed to resolve call edges: {}", e);
        }

        tracing::debug!("Auto-index complete for {}", project_root.display());
        Ok(())
    }

    async fn trigger_reindex(&self) -> Result<(), String> {
        let project_root = self.find_project_root()?;
        let graph_engine = self
            .get_graph_engine()
            .map_err(|e| format!("Database error: {}", e))?;
        let mut parser_manager = crate::indexer::ParserManager::new();
        parser_manager
            .init_parsers()
            .map_err(|e| format!("Parser init error: {}", e))?;

        let root_str = project_root.to_string_lossy().to_string();
        match crate::indexer::incremental_index_sync(&graph_engine, &mut parser_manager, &root_str)
            .await
        {
            Ok(result) => {
                tracing::info!(
                    "Reindex triggered by external write: {} files processed",
                    result.total_files_processed
                );
            }
            Err(e) => {
                tracing::warn!("Reindex failed: {}", e);
            }
        }

        {
            let mut guard = self.graph_engine.lock();
            *guard = None;
        }
        Ok(())
    }

    fn load_config(
        &self,
        project_root: &std::path::Path,
    ) -> Result<crate::config::ProjectConfig, String> {
        let config_path = project_root.join(".leankg/leankg.yaml");
        if config_path.exists() {
            let content = std::fs::read_to_string(&config_path)
                .map_err(|e| format!("Failed to read config: {}", e))?;
            serde_yaml::from_str::<crate::config::ProjectConfig>(&content)
                .map_err(|e| format!("Failed to parse config: {}", e))
        } else {
            Ok(crate::config::ProjectConfig::default())
        }
    }

    fn find_project_root(&self) -> Result<std::path::PathBuf, String> {
        let configured_db_path = self.get_db_path();
        if configured_db_path.ends_with(".leankg") {
            if let Some(parent) = configured_db_path.parent() {
                if !parent.as_os_str().is_empty() && parent.exists() {
                    tracing::debug!(
                        "Using configured db_path parent as project root: {}",
                        parent.display()
                    );
                    return Ok(parent.to_path_buf());
                }
            }
        }

        let current_dir =
            std::env::current_dir().map_err(|e| format!("Failed to get current dir: {}", e))?;

        if current_dir.join(".leankg").exists() || current_dir.join("leankg.yaml").exists() {
            tracing::debug!(
                "Found .leankg/leankg.yaml at current dir: {}",
                current_dir.display()
            );
            return Ok(current_dir);
        }

        if current_dir.join(".git").exists() {
            tracing::debug!("Found .git at current dir: {}", current_dir.display());
            return Ok(current_dir);
        }

        for dir in current_dir.ancestors() {
            if dir.join(".git").exists() {
                tracing::debug!("Found git repo at {}, this is project root", dir.display());
                if dir.join(".leankg").exists() || dir.join("leankg.yaml").exists() {
                    tracing::debug!(
                        "Found .leankg/leankg.yaml in project root: {}",
                        dir.display()
                    );
                    return Ok(dir.to_path_buf());
                }
                tracing::debug!(
                    "No .leankg in project root {}, will need auto-init",
                    dir.display()
                );
                return Ok(dir.to_path_buf());
            }
        }

        for dir in current_dir.ancestors() {
            if dir.join(".leankg").exists() || dir.join("leankg.yaml").exists() {
                tracing::debug!("Found project at {} (parent without .git)", dir.display());
                return Ok(dir.to_path_buf());
            }
        }

        tracing::debug!(
            "No project markers found, using current dir: {}",
            current_dir.display()
        );
        Ok(current_dir)
    }

    fn validate_required_params(
        &self,
        tool_name: &str,
        arguments: &serde_json::Map<String, serde_json::Value>,
    ) -> Option<String> {
        let tools = ToolRegistry::list_tools();
        let tool = tools.iter().find(|t| t.name == tool_name)?;

        let required_params = tool.input_schema.get("required")?.as_array()?;
        for param in required_params {
            let param_name = param.as_str()?;
            if !arguments.contains_key(param_name)
                || arguments.get(param_name).is_none_or(|v| v.is_null())
            {
                return Some(format!(
                    "Missing required parameter '{}' for tool '{}'",
                    param_name, tool_name
                ));
            }
        }
        None
    }

    /// L1 read cache wrapping the project's `GraphEngine`. Built lazily on
    /// first read after a fresh `GraphEngine` is opened, and dropped whenever
    /// the underlying engine is dropped (mcp_init / mcp_index / knowledge
    /// contribution tools).
    pub fn get_caching_graph_engine(&self) -> Result<CachingGraphEngine, String> {
        let engine = self.get_graph_engine()?;
        let db_path = self.get_db_path();
        {
            let cache = self.caching_engine_cache.read();
            if let Some(c) = cache.get(&db_path) {
                return Ok(c.clone());
            }
        }
        let wrapper = CachingGraphEngine::new(engine);
        let mut cache = self.caching_engine_cache.write();
        cache.insert(db_path, wrapper.clone());
        Ok(wrapper)
    }

    /// Drop the L1 dispatch cache and every per-project `CachingGraphEngine`.
    /// Called from the write path inside `execute_tool` after a successful
    /// mutation. Public so tests can drive it directly.
    pub(crate) async fn invalidate_l1_caches_public(&self) {
        // Drain the cache under the lock and collect references; then drop the
        // guard before awaiting each invalidate so the parking_lot write guard
        // is not held across an `.await` (which would break the `Send` bound
        // the rmcp handler requires).
        let engines: Vec<CachingGraphEngine> = {
            let mut cache = self.caching_engine_cache.write();
            cache.drain().map(|(_, e)| e).collect()
        };
        for engine in &engines {
            engine.invalidate().await;
        }
        let _ = self.dispatch_cache.invalidate_entries_if(|_, _| true);
    }

    async fn execute_tool(
        &self,
        tool_name: &str,
        arguments: serde_json::Map<String, serde_json::Value>,
    ) -> Result<serde_json::Value, String> {
        // Read-only enforcement: short-circuit before acquiring any locks or
        // touching the DB. The set of write tools here MUST match the set
        // Subagent A uses to invalidate the L1 cache (`requires_write_lock`
        // below) — duplicates are fine, missing tools are not.
        if self.read_only && Self::is_write_tool(tool_name) {
            return Err(format!(
                "server is in read-only mode (tool '{}' is a write tool)",
                tool_name
            ));
        }
        let project_root = self.find_project_root()?;
        tracing::info!(
            "execute_tool called. project_root={}, db_path={}",
            project_root.display(),
            self.get_db_path().display()
        );

        // Validate required parameters before dispatching to handler
        if let Some(err) = self.validate_required_params(tool_name, &arguments) {
            return Err(err);
        }

        if tool_name == "embed_control" {
            // embed_control spawns its own background thread; it does not
            // touch the L1 cache directly. Mark it as write so we serialise
            // on write_lock the same way the legacy path did.
            let _write_guard = self.write_lock.lock().await;
            return self.handle_embed_control(&arguments);
        }

        if tool_name == "ontology_control" {
            // Same as embed_control — write-ish, serialise.
            let _write_guard = self.write_lock.lock().await;
            return self.handle_ontology_control(&arguments);
        }

        // Read path: serve from dispatch cache if possible before we even
        // touch the write_lock. The cache is invalidated on writes below, so
        // a hit is guaranteed to be either fresh or stale-and-self-correcting
        // within the configured TTL.
        if !Self::requires_write_lock(tool_name) && !self.write_tracker.is_dirty() {
            let cache_key = dispatch_cache_key(tool_name, &arguments);
            if let Some(cached) = self.dispatch_cache.get(&cache_key).await {
                tracing::debug!("L1 dispatch cache HIT tool={}", tool_name);
                return Ok(cached);
            }
        }

        // The remainder of the function is the legacy body, with two
        // refinements:
        //   1. write_lock is acquired only when the tool actually mutates
        //      the DB (or when the write tracker is dirty — which forces a
        //      reindex before any read can run);
        //   2. the L1 caches (engine-level + dispatch) are invalidated
        //      after a successful write so the next read sees fresh data.
        let needs_write = Self::requires_write_lock(tool_name) || self.write_tracker.is_dirty();
        let _write_guard = if needs_write {
            Some(self.write_lock.lock().await)
        } else {
            None
        };

        // Validate required parameters before dispatching to handler.
        // (embed_control / ontology_control short-circuits above; here we
        // still need this for the rest of the dispatch.)
        if let Some(err) = self.validate_required_params(tool_name, &arguments) {
            return Err(err);
        }

        if tool_name == "mcp_init" {
            if let Some(path) = arguments.get("path").and_then(|v| v.as_str()) {
                let new_db_path = std::path::PathBuf::from(path);
                {
                    let mut guard = self.graph_engine.lock();
                    *guard = None;
                }
                {
                    let mut db_path_guard = parking_lot::RwLock::write(&self.db_path);
                    *db_path_guard = new_db_path.clone();
                }
                tracing::info!("Updated db_path to {}", new_db_path.display());
            }
        }

        if self.write_tracker.is_dirty() {
            let config = self.load_config(&project_root)?;
            if config.mcp.auto_index_on_db_write {
                tracing::info!("External write detected, triggering incremental reindex...");
                self.trigger_reindex().await?;
                self.write_tracker.clear_dirty();
            }
        }

        let file_path: Option<String> = if tool_name == "orchestrate" {
            // For orchestrate, parse intent to extract target file
            arguments
                .get("intent")
                .and_then(|v| v.as_str())
                .and_then(|intent| {
                    let parsed = self.intent_parser.parse(intent);
                    parsed.target
                })
                .or_else(|| {
                    arguments
                        .get("file")
                        .and_then(|v| v.as_str())
                        .map(String::from)
                })
        } else {
            arguments
                .get("file")
                .and_then(|v| v.as_str())
                .or_else(|| arguments.get("path").and_then(|v| v.as_str()))
                .or_else(|| arguments.get("project").and_then(|v| v.as_str()))
                .map(String::from)
        };

        let project_db_path = if let Some(ref fp) = file_path {
            if let Some(leankg_path) = Self::resolve_project_db_path(fp.as_str()) {
                tracing::debug!(
                    "Routing query for '{}' to database at {}",
                    fp,
                    leankg_path.display()
                );
                leankg_path
            } else {
                tracing::debug!("No .leankg found for '{}', using default db_path", fp);
                self.get_db_path()
            }
        } else {
            Self::resolve_project_db_path(".")
                .or_else(|| Self::find_leankg_for_path("."))
                .unwrap_or_else(|| self.get_db_path())
        };

        let graph_engine = self.get_graph_engine_for_path(file_path.as_ref())?;

        // On-demand auto-indexing: if project has .leankg but no RocksDB index, index it
        if tool_name != "mcp_index" && tool_name != "mcp_init" && tool_name != "mcp_index_docs" {
            let rocksdb_path = crate::db::schema::central_project_storage_path(&project_db_path);
            let has_index = rocksdb_path.join("manifest").exists()
                || rocksdb_path.join("data/CURRENT").exists();
            if !has_index {
                tracing::info!(
                    "Project at {} has no RocksDB index, triggering auto-index",
                    project_db_path.display()
                );
                let _ = self
                    .ensure_project_indexed(
                        project_db_path
                            .parent()
                            .unwrap_or(&project_db_path)
                            .to_string_lossy()
                            .as_ref(),
                    )
                    .await;
            }
        }

        let handler = ToolHandler::new(graph_engine, project_db_path);
        let arguments_obj = arguments.clone();
        let args_value = serde_json::Value::Object(arguments);
        let result = handler.execute_tool(tool_name, &args_value).await;

        if tool_name == "mcp_index" {
            if result.is_ok() {
                self.refresh_ontology_after_index();
            }
            let mut guard = self.graph_engine.lock();
            *guard = None;
        }

        // Invalidate cached GraphEngine after write tools so subsequent reads
        // get a fresh RocksDB connection (avoids lock contention from :put ops)
        if matches!(
            tool_name,
            "mcp_index"
                | "mcp_index_docs"
                | "add_knowledge"
                | "update_knowledge"
                | "delete_knowledge"
                | "add_annotation"
                | "link_element"
                | "add_documentation"
                | "promote_environment"
                | "ontology_control"
        ) {
            let mut guard = self.graph_engine.lock();
            *guard = None;
            let mut cache = self.graph_engine_cache.lock();
            cache.clear();
        }

        // Mark write tracker dirty for knowledge contribution tools
        if matches!(
            tool_name,
            "add_knowledge"
                | "update_knowledge"
                | "delete_knowledge"
                | "add_annotation"
                | "link_element"
                | "add_documentation"
                | "promote_environment"
        ) {
            self.write_tracker.mark_dirty();
        }

        // L1 cache invalidation on writes. After a successful mutation we
        // (a) drop the dispatch cache for every tool (cheap; full reset),
        // and (b) invalidate each cached `CachingGraphEngine` so its
        // method-level caches cannot serve stale data.
        if needs_write || Self::requires_write_lock(tool_name) {
            invalidate_l1_caches(self).await;
        } else if let Ok(ref v) = result {
            // Populate dispatch cache for read tools on miss.
            let cache_key = dispatch_cache_key(tool_name, &arguments_obj);
            self.dispatch_cache.insert(cache_key, v.clone()).await;
        }

        result
    }

    fn requires_write_lock(tool_name: &str) -> bool {
        WRITE_TOOLS.contains(tool_name)
    }

    /// Returns true when the given tool mutates state. Mirrors the write set
    /// Subagent A uses to invalidate the L1 cache (`requires_write_lock`).
    /// When the server is in read-only mode, `execute_tool` returns an error
    /// for any tool this returns true for.
    pub fn is_write_tool(tool_name: &str) -> bool {
        Self::requires_write_lock(tool_name)
    }

    /// Public wrapper for `execute_tool` so integration tests can drive the
    /// read-only gate end-to-end. Internal callers use the private method.
    pub async fn execute_tool_pub(
        &self,
        tool_name: &str,
        arguments: serde_json::Map<String, serde_json::Value>,
    ) -> Result<serde_json::Value, String> {
        self.execute_tool(tool_name, arguments).await
    }
}

impl ServerHandler for MCPServer {
    fn get_info(&self) -> rmcp::model::ServerInfo {
        rmcp::model::ServerInfo::new(
            rmcp::model::ServerCapabilities::builder()
                .enable_tools()
                .enable_resources()
                .build(),
        )
        .with_server_info(
            rmcp::model::Implementation::new("leankg", env!("CARGO_PKG_VERSION"))
                .with_title("LeanKG")
                .with_description("Lightweight knowledge graph for codebase understanding")
        )
        .with_instructions("LeanKG: an indexed knowledge graph of the CURRENT WORKING DIRECTORY (the repo you are analyzing). Always query it first via ToolSearch(\"leankg\") to discover MCP tools, then call mcp__leankg__mcp_status to verify the index. PREFER-ORDER: mcp__leankg__get_overview_context → mcp__leankg__concept_search → mcp__leankg__semantic_search → mcp__leankg__search_code. For fuzzy/NL/domain questions use mcp__leankg__semantic_search (or mcp__leankg__concept_search). For exact symbol names use mcp__leankg__find_function. Do NOT call mcp__leankg__query_graph as the first discovery tool. Read/Grep/Bash remain available as fallback.")
    }

    async fn list_tools(
        &self,
        _params: Option<rmcp::model::PaginatedRequestParams>,
        _context: rmcp::service::RequestContext<RoleServer>,
    ) -> Result<ListToolsResult, rmcp::model::ErrorData> {
        let tools = ToolRegistry::list_tools();
        let rmcp_tools: Vec<Tool> = tools
            .into_iter()
            .map(|t| {
                Tool::new(
                    t.name,
                    t.description,
                    Arc::new(t.input_schema.as_object().cloned().unwrap_or_default()),
                )
            })
            .collect();
        Ok(ListToolsResult::with_all_items(rmcp_tools))
    }

    async fn call_tool(
        &self,
        request: CallToolRequestParams,
        _context: rmcp::service::RequestContext<RoleServer>,
    ) -> Result<CallToolResult, rmcp::model::ErrorData> {
        let tool_name = request.name.as_ref();
        let arguments = request.arguments.unwrap_or_default();

        // Always use TOON format (ignore client's format preference)
        let use_toon = true;

        match self.execute_tool(tool_name, arguments).await {
            Ok(result) => {
                let content_str = if let Some(s) = result.as_str() {
                    // Already purely text (e.g. from context chunk fetch) - preserve as-is
                    s.to_string()
                } else if use_toon {
                    // Use TOON format with Response Format Envelope
                    crate::mcp::toon::wrap_response(tool_name, &result, true)
                } else {
                    // Use JSON format with Response Format Envelope
                    crate::mcp::toon::wrap_response(tool_name, &result, false)
                };

                Ok(CallToolResult::success(vec![Content::text(content_str)]))
            }
            Err(e) => Ok(CallToolResult::error(vec![Content::text(format!(
                "Tool execution failed: {}",
                e
            ))])),
        }
    }
}

// ============================================================================
// HTTP Transport for Remote MCP Server
// ============================================================================

/// HTTP MCP Server state shared across requests
struct HttpMcpServer {
    mcp_server: MCPServer,
    auth_token: Option<String>,
    auth_manager: AuthManager,
}

/// Query parameters extracted from MCP HTTP requests
#[derive(Debug, serde::Deserialize)]
struct McpQueryParams {
    /// Project root directory - overrides server's default db_path
    project: Option<String>,
}

impl McpQueryParams {
    fn resolve_db_path(&self, default_db_path: &std::path::Path) -> std::path::PathBuf {
        if let Some(ref project) = self.project {
            let path = std::path::PathBuf::from(project);
            let db_path = if path.ends_with(".leankg") {
                path
            } else {
                path.join(".leankg")
            };
            if db_path.is_dir() {
                tracing::debug!("Using project from query param: {}", db_path.display());
                return db_path;
            }
            tracing::warn!(
                "Project path from query param not found: {}, using default",
                db_path.display()
            );
        }
        default_db_path.to_path_buf()
    }
}

/// MCP JSON-RPC request envelope
#[derive(Debug, Serialize, Deserialize, Clone)]
struct JsonRpcRequest {
    jsonrpc: String,
    #[serde(default)]
    id: Option<serde_json::Value>,
    method: String,
    params: Option<serde_json::Value>,
}

/// MCP JSON-RPC response envelope
#[derive(Debug, Serialize)]
struct JsonRpcResponse {
    jsonrpc: String,
    id: serde_json::Value,
    #[serde(skip_serializing_if = "Option::is_none")]
    result: Option<serde_json::Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    error: Option<JsonRpcError>,
}

#[derive(Debug, Serialize)]
struct JsonRpcError {
    code: i32,
    message: String,
    data: Option<serde_json::Value>,
}

/// MCP JSON-RPC error codes
mod json_rpc_code {
    pub const PARSE_ERROR: i32 = -32700;
    pub const INVALID_REQUEST: i32 = -32600;
    pub const METHOD_NOT_FOUND: i32 = -32601;
    pub const INVALID_PARAMS: i32 = -32602;
    pub const INTERNAL_ERROR: i32 = -32603;
}

fn should_resolve_tool_paths(tool_name: &str) -> bool {
    matches!(
        tool_name,
        "mcp_index" | "mcp_index_docs" | "mcp_init" | "detect_changes"
    )
}

/// Extract bearer token from Authorization header using constant-time comparison
/// to prevent timing attacks on bearer tokens. Returns an AuthContext with role.
fn extract_auth_context(
    auth_header: Option<&str>,
    server: &HttpMcpServer,
) -> Result<crate::db::models::AuthContext, StatusCode> {
    if server.auth_token.is_none() {
        // No auth configured — grant admin
        return Ok(crate::db::models::AuthContext {
            client_id: "anonymous".to_string(),
            role: crate::db::models::Role::Admin,
        });
    }

    let expected_token = server.auth_token.as_ref().unwrap();

    if let Some(auth) = auth_header {
        if let Some(stripped) = auth.strip_prefix("Bearer ") {
            // Use constant-time comparison to prevent timing attacks
            let matches: bool =
                subtle::ConstantTimeEq::ct_eq(stripped.as_bytes(), expected_token.as_bytes())
                    .into();
            if matches {
                return server
                    .auth_manager
                    .validate_token(stripped)
                    .map_err(|_| StatusCode::UNAUTHORIZED);
            }
        }
    }
    Err(StatusCode::UNAUTHORIZED)
}

/// Handle POST /mcp - JSON-RPC request endpoint
async fn handle_mcp_request(
    State(server): State<Arc<HttpMcpServer>>,
    uri: axum::http::Uri,
    headers: HeaderMap,
    body: String,
) -> Response {
    // Extract project from URL query param
    let project_param = uri
        .query()
        .and_then(|q| q.split('&').find(|s| s.starts_with("project=")))
        .and_then(|s| s.strip_prefix("project="))
        .map(|s| {
            // Simple percent-decode: %XX → byte
            let mut result = String::new();
            let mut chars = s.chars().peekable();
            while let Some(c) = chars.next() {
                if c == '%' {
                    let hex: String = chars.by_ref().take(2).collect();
                    if hex.len() == 2 {
                        if let Ok(byte) = u8::from_str_radix(&hex, 16) {
                            result.push(byte as char);
                        } else {
                            result.push('%');
                            result.push_str(&hex);
                        }
                    } else {
                        result.push('%');
                        result.push_str(&hex);
                    }
                } else {
                    result.push(c);
                }
            }
            result
        });

    // Extract Authorization header
    let auth_value = headers
        .get(header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok());

    // Check authentication and get auth context
    let auth_context = match extract_auth_context(auth_value, &server) {
        Ok(ctx) => ctx,
        Err(status) => {
            return Response::builder()
                .status(status)
                .body(Body::from(r#"{"error": "Unauthorized"}"#))
                .unwrap();
        }
    };

    // Parse JSON-RPC request
    let request: JsonRpcRequest = match serde_json::from_str(&body) {
        Ok(req) => req,
        Err(e) => {
            let response = JsonRpcResponse {
                jsonrpc: "2.0".to_string(),
                id: serde_json::Value::Null,
                result: None,
                error: Some(JsonRpcError {
                    code: json_rpc_code::PARSE_ERROR,
                    message: format!("Parse error: {}", e),
                    data: None,
                }),
            };
            return Response::builder()
                .status(StatusCode::OK)
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(serde_json::to_string(&response).unwrap()))
                .unwrap();
        }
    };

    // Check if this is a notification (no id) - notifications must not receive a response
    let is_notification = request.id.is_none();

    // Apply project override from query param. Inject "project" for DB routing,
    // but only absolutize arguments for tools that read the filesystem. Graph
    // query tools expect stored project-relative paths like "./src/main.rs";
    // rewriting those to absolute paths forces expensive full-graph scans.
    let request = if let Some(ref project) = project_param {
        let project_path = std::path::PathBuf::from(project);
        let mut req = request.clone();
        if let Some(ref mut params) = req.params {
            if let Some(obj) = params.as_object_mut() {
                let resolve_tool_paths = obj
                    .get("name")
                    .and_then(|v| v.as_str())
                    .map(should_resolve_tool_paths)
                    .unwrap_or(false);
                // Inject project param for routing
                if let Some(ref mut args) = obj.get_mut("arguments") {
                    if let Some(args_obj) = args.as_object_mut() {
                        args_obj
                            .entry("project".to_string())
                            .or_insert(serde_json::Value::String(project.clone()));
                        if resolve_tool_paths {
                            // Resolve relative filesystem paths against project root.
                            for key in &["file", "doc", "path"] {
                                if let Some(serde_json::Value::String(v)) = args_obj.get_mut(*key) {
                                    if !v.starts_with('/') {
                                        let resolved = project_path.join(&*v);
                                        *v = resolved.to_string_lossy().to_string();
                                    }
                                }
                            }
                            // Resolve files array elements too.
                            if let Some(serde_json::Value::Array(arr)) = args_obj.get_mut("files") {
                                for item in arr.iter_mut() {
                                    if let serde_json::Value::String(v) = item {
                                        if !v.starts_with('/') {
                                            let resolved = project_path.join(&*v);
                                            *v = resolved.to_string_lossy().to_string();
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
        req
    } else {
        request
    };

    if is_notification {
        // Process the notification but don't send a response
        let _ = process_jsonrpc_request(
            &server.mcp_server,
            &request,
            project_param.as_deref(),
            crate::db::models::AuthContext {
                client_id: "anonymous".to_string(),
                role: crate::db::models::Role::Admin,
            },
        )
        .await;
        return Response::builder()
            .status(StatusCode::NO_CONTENT)
            .body(Body::empty())
            .unwrap();
    }

    // Process the request, passing project param for routing
    let result = process_jsonrpc_request(
        &server.mcp_server,
        &request,
        project_param.as_deref(),
        auth_context,
    )
    .await;

    // Build response
    // unwrap is safe because if id was None we already returned NO_CONTENT above
    let response = match result {
        Ok(result) => JsonRpcResponse {
            jsonrpc: "2.0".to_string(),
            id: request.id.clone().unwrap(),
            result: Some(result),
            error: None,
        },
        Err(e) => JsonRpcResponse {
            jsonrpc: "2.0".to_string(),
            id: request.id.clone().unwrap(),
            result: None,
            error: Some(JsonRpcError {
                code: json_rpc_code::INTERNAL_ERROR,
                message: e,
                data: None,
            }),
        },
    };

    Response::builder()
        .status(StatusCode::OK)
        .header(header::CONTENT_TYPE, "application/json")
        .body(Body::from(serde_json::to_string(&response).unwrap()))
        .unwrap()
}

/// Process a JSON-RPC request and return the result
async fn process_jsonrpc_request(
    mcp_server: &MCPServer,
    request: &JsonRpcRequest,
    project_param: Option<&str>,
    auth_context: crate::db::models::AuthContext,
) -> Result<serde_json::Value, String> {
    let method = &request.method;
    let params = request.params.as_ref();

    match method.as_str() {
        "initialize" => Ok(serde_json::json!({
            "protocolVersion": "2025-06-18",
            "capabilities": {
                "tools": { "listChanged": true },
                "resources": {}
            },
            "serverInfo": {
                "name": "leankg",
                "version": env!("CARGO_PKG_VERSION")
            }
        })),
        "notifications/initialized" => {
            // Client is done initializing, no response needed
            Ok(serde_json::Value::Null)
        }
        "resources/list" => {
            // US-GN-08: two static resources, backed by the same seam as
            // `get_overview_context`. Kept in sync with the rmcp
            // ServerHandler::list_resources (used by stdio transport).
            let resources = vec![
                serde_json::json!({
                    "uri": "leankg://overview",
                    "name": "LeanKG overview",
                    "description": "Session-start overview: project identity (L0) + critical facts (L1)",
                    "mimeType": "text/markdown",
                }),
                serde_json::json!({
                    "uri": "leankg://overview/wake_up",
                    "name": "LeanKG wake-up summary",
                    "description": "wake_up_summary project snapshot",
                    "mimeType": "text/markdown",
                }),
            ];
            Ok(serde_json::json!({ "resources": resources }))
        }
        "resources/read" => {
            // US-GN-08: read overview / wake_up resources. Mirrors the rmcp
            // ServerHandler::read_resource (stdio transport); routes through
            // project_param the same way tools/call does (per-project graph).
            let uri = params
                .and_then(|p| p.get("uri"))
                .and_then(|v| v.as_str())
                .ok_or("Missing uri for resources/read")?;
            let project_ref = project_param.map(|s| s.to_string());
            let engine = mcp_server
                .get_graph_engine_for_path(project_ref.as_ref())
                .map_err(|e| e.to_string())?;
            let project_name = "project";
            let text = match uri {
                "leankg://overview" => {
                    let l0 = engine.identity_context(project_name).unwrap_or_default();
                    let l1 = engine.critical_facts_context().unwrap_or_default();
                    format!("{}\n{}", l0, l1)
                }
                "leankg://overview/wake_up" => engine.wake_up_summary().unwrap_or_default(),
                _ => return Err(format!("unknown resource URI: {uri}")),
            };
            Ok(serde_json::json!({
                "contents": [{
                    "uri": uri,
                    "mimeType": "text/markdown",
                    "text": text,
                }]
            }))
        }
        "resources/templates/list" => Ok(serde_json::json!({ "resourceTemplates": [] })),
        "prompts/list" => Ok(serde_json::json!({ "prompts": [] })),
        "tools/list" => {
            let tools = ToolRegistry::list_tools();
            let rmcp_tools: Vec<serde_json::Value> = tools
                .into_iter()
                .map(|t| {
                    serde_json::json!({
                        "name": t.name,
                        "description": t.description,
                        "inputSchema": t.input_schema
                    })
                })
                .collect();
            Ok(serde_json::json!({ "tools": rmcp_tools }))
        }
        "tools/call" => {
            let params_obj = params
                .and_then(|p| p.as_object())
                .ok_or("Missing params for tools/call")?;

            let tool_name = params_obj
                .get("name")
                .and_then(|v| v.as_str())
                .ok_or("Missing tool name")?;

            // RBAC: Check if user has permission to call this tool
            if let Err(e) = mcp_server
                .auth_manager
                .read()
                .await
                .check_permission(&auth_context, tool_name)
            {
                return Err(format!("Permission denied: {}", e));
            }

            let mut arguments = params_obj
                .get("arguments")
                .and_then(|v| v.as_object())
                .cloned()
                .unwrap_or_default();

            // Inject project from URL query param if not already in arguments
            if let Some(ref project) = project_param {
                arguments
                    .entry("project".to_string())
                    .or_insert(serde_json::Value::String(project.to_string()));
            }

            let result = mcp_server
                .execute_tool(tool_name, arguments)
                .await
                .map_err(|e| e.to_string())?;

            // Format as MCP tool result
            // Tool results are either plain strings (as_str()) or structured JSON
            // that needs to be wrapped in MCP response format
            let content_str = if let Some(s) = result.as_str() {
                s.to_string()
            } else {
                crate::mcp::toon::wrap_response(tool_name, &result, true)
            };

            Ok(serde_json::json!({
                "content": [{ "type": "text", "text": content_str }]
            }))
        }
        _ => Err(format!("Method not found: {}", method)),
    }
}

/// Build the SSE endpoint URL advertised to the client during the SSE
/// discovery handshake. Preserves the `?project=` query string from the
/// incoming GET request so that Cursor's streamable-HTTP MCP transport
/// POSTs subsequent JSON-RPC calls to the correct project instead of
/// falling back to the CLI default project.
///
/// `project = None` and `project = Some("")` both produce the bare path,
/// matching the original (buggy) behavior for clients that did not pass
/// the query string.
pub(crate) fn discovery_endpoint_url(project: Option<&str>) -> String {
    match project.filter(|p| !p.is_empty()) {
        Some(p) => format!("/mcp?project={}", percent_encode_path(p)),
        None => "/mcp".to_string(),
    }
}

/// Percent-encode a path/query value using RFC 3986 unreserved set
/// (`A-Z a-z 0-9 - . _ ~`). UTF-8 is preserved byte-by-byte so the result
/// is safe to round-trip through `percent_decode_path` for any string.
fn percent_encode_path(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for &b in s.as_bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'.' | b'_' | b'~' => {
                out.push(b as char);
            }
            _ => {
                out.push('%');
                out.push_str(&format!("{:02X}", b));
            }
        }
    }
    out
}

/// Percent-decode a previously-encoded path/query value. Operates on bytes
/// to preserve UTF-8 sequences; invalid UTF-8 falls back to lossy decoding
/// so callers always receive a valid `String`.
fn percent_decode_path(s: &str) -> String {
    let bytes = s.as_bytes();
    let mut out: Vec<u8> = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%' && i + 2 < bytes.len() {
            if let (Some(h), Some(l)) = (hex_value(bytes[i + 1]), hex_value(bytes[i + 2])) {
                out.push((h << 4) | l);
                i += 3;
                continue;
            }
        }
        out.push(bytes[i]);
        i += 1;
    }
    String::from_utf8_lossy(&out).into_owned()
}

fn hex_value(b: u8) -> Option<u8> {
    match b {
        b'0'..=b'9' => Some(b - b'0'),
        b'a'..=b'f' => Some(b - b'a' + 10),
        b'A'..=b'F' => Some(b - b'A' + 10),
        _ => None,
    }
}

/// Build the SSE endpoint-discovery response. Pure helper so the wire
/// format is unit-testable without standing up a full Router / State.
fn build_sse_endpoint_response(project_query: Option<&str>) -> Response {
    let sse_data = format!(
        "event: endpoint\ndata: {}\n\n",
        discovery_endpoint_url(project_query)
    );
    Response::builder()
        .status(StatusCode::OK)
        .header(header::CONTENT_TYPE, "text/event-stream")
        .header(header::CACHE_CONTROL, "no-cache")
        .header(header::CONNECTION, "keep-alive")
        .body(Body::from(sse_data))
        .unwrap()
}

/// Handle GET /mcp/stream - SSE endpoint for server-initiated messages
async fn handle_sse_stream(
    State(server): State<Arc<HttpMcpServer>>,
    headers: HeaderMap,
    Query(query): Query<HashMap<String, String>>,
) -> Response {
    // Extract Authorization header
    let auth_value = headers
        .get(header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok());

    // Check authentication and get auth context
    let _auth_context = match extract_auth_context(auth_value, &server) {
        Ok(ctx) => ctx,
        Err(status) => {
            return Response::builder()
                .status(status)
                .body(Body::from(r#"event: error\ndata: Unauthorized\n\n"#))
                .unwrap();
        }
    };

    // Preserve the `?project=` query string from the incoming GET so that
    // Cursor's streamable-HTTP MCP transport routes subsequent JSON-RPC
    // calls to the correct project instead of falling back to the CLI
    // default. See docs/analysis/fix-mcp-sse-discovery-preserve-project-2026-07-30.md
    let project_query = query.get("project").map(String::as_str);
    build_sse_endpoint_response(project_query)
}

/// Health check endpoint
async fn health_check() -> Response {
    Response::builder()
        .status(StatusCode::OK)
        .header(header::CONTENT_TYPE, "application/json")
        .body(Body::from(r#"{"status": "ok"}"#))
        .unwrap()
}

#[cfg(test)]
mod tests {
    use super::*;

    // Serialize tests that mutate process-wide environment variables.
    // `std::env::set_var` / `remove_var` are not thread-safe; without this
    // lock, parallel `cargo test` invocations can race and observe the
    // wrong value. See `parse_vacuum_interval_*` tests below.
    static ENV_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

    #[tokio::test]
    async fn test_mcp_server_creation() {
        let _server = MCPServer::new(std::path::PathBuf::from(".leankg"));
    }

    #[tokio::test]
    async fn test_mcp_server_new_with_custom_path() {
        let db_path = std::path::PathBuf::from("/custom/path/.leankg");
        let server = MCPServer::new(db_path.clone());
        assert!(server.auth_manager.try_read().is_ok());
    }

    #[test]
    fn test_project_routing_only_absolutizes_filesystem_tools() {
        assert!(should_resolve_tool_paths("mcp_index"));
        assert!(should_resolve_tool_paths("mcp_index_docs"));
        assert!(!should_resolve_tool_paths("get_context"));
        assert!(!should_resolve_tool_paths("search_code"));
        assert!(!should_resolve_tool_paths("find_function"));
    }

    // ---------------------------------------------------------------------
    // FR-A01: MCP `project` resolves to the correct RocksDB project for
    // multi-mount setups.
    //
    // The seam is `resolve_project_db_path(fp)` — used by every tool that
    // receives a `project=` arg (`get_graph_engine_for_path`, embed_control,
    // session paths, …). Each container mount is a separate project root
    // with its own `.leankg`, so resolution must:
    //   * return that mount's own `.leankg` (not the server default)
    //   * walk up ancestors to the nearest `.leankg` for sub-paths
    //   * return None when no `.leankg` exists (caller falls back to default)
    // ---------------------------------------------------------------------

    fn make_project_mount(root: &std::path::Path, name: &str) -> std::path::PathBuf {
        let mount = root.join(name);
        std::fs::create_dir_all(mount.join(".leankg")).unwrap();
        mount
    }

    #[test]
    fn fr_a01_project_mount_resolves_to_own_leankg() {
        let tmp = tempfile::TempDir::new().unwrap();
        // Two mounts side by side, each with its own .leankg — the MCP
        // server keys RocksDB per project by that path (FR-A01).
        let mount_a = make_project_mount(tmp.path(), "project-a");
        let mount_b = make_project_mount(tmp.path(), "project-b");
        assert_eq!(
            MCPServer::resolve_project_db_path(mount_a.to_str().unwrap()),
            Some(mount_a.join(".leankg"))
        );
        assert_eq!(
            MCPServer::resolve_project_db_path(mount_b.to_str().unwrap()),
            Some(mount_b.join(".leankg")),
            "each mount must resolve to its own .leankg, not the server default"
        );
    }

    #[test]
    fn fr_a01_subpath_walks_up_to_nearest_leankg() {
        let tmp = tempfile::TempDir::new().unwrap();
        let mount = make_project_mount(tmp.path(), "project-a");
        let subdir = mount.join("src/deep/nested");
        std::fs::create_dir_all(&subdir).unwrap();
        // A file deep inside the mount resolves back to the mount's .leankg.
        let file = subdir.join("main.rs");
        std::fs::write(&file, "fn main() {}").unwrap();
        assert_eq!(
            MCPServer::resolve_project_db_path(file.to_str().unwrap()),
            Some(mount.join(".leankg"))
        );
        assert_eq!(
            MCPServer::resolve_project_db_path(subdir.to_str().unwrap()),
            Some(mount.join(".leankg"))
        );
    }

    #[test]
    fn fr_a01_no_leankg_returns_none() {
        let tmp = tempfile::TempDir::new().unwrap();
        let bare = tmp.path().join("uninitialized-project");
        std::fs::create_dir_all(&bare).unwrap();
        assert_eq!(
            MCPServer::resolve_project_db_path(bare.to_str().unwrap()),
            None,
            "no .leankg anywhere up the tree -> caller falls back to default db_path"
        );
    }

    #[test]
    fn fr_a01_resolve_project_db_path_does_not_descend_into_sibling_mounts() {
        let tmp = tempfile::TempDir::new().unwrap();
        // A sub-mount nested *inside* another mount — the nearest `.leankg`
        // wins so a nested repo never bleeds into its parent project.
        let outer = make_project_mount(tmp.path(), "outer");
        let inner = make_project_mount(&outer, "inner");
        assert_eq!(
            MCPServer::resolve_project_db_path(inner.to_str().unwrap()),
            Some(inner.join(".leankg")),
            "nested mount must resolve to the innermost .leankg"
        );
    }

    #[test]
    fn test_parse_vacuum_interval_default_when_unset() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        // SAFETY: env::remove_var is unsafe on the 2024 edition; gate behind cfg.
        // Here we accept the existing project's edition to keep behavior simple.
        let prev = std::env::var("LEANKG_VACUUM_INTERVAL_HOURS").ok();
        // SAFETY: tests are single-threaded for env mutation in this binary.
        unsafe {
            std::env::remove_var("LEANKG_VACUUM_INTERVAL_HOURS");
        }
        let result = MCPServer::parse_vacuum_interval();
        // Default: Some(1 hour) — but on this codebase the default is `1`, so we
        // expect Some(3600s).
        assert_eq!(result, Some(std::time::Duration::from_secs(3600)));
        if let Some(v) = prev {
            // SAFETY: see above.
            unsafe {
                std::env::set_var("LEANKG_VACUUM_INTERVAL_HOURS", v);
            }
        }
    }

    #[test]
    fn test_parse_vacuum_interval_zero_disables() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        // SAFETY: tests are single-threaded for env mutation in this binary.
        unsafe {
            std::env::set_var("LEANKG_VACUUM_INTERVAL_HOURS", "0");
        }
        assert_eq!(MCPServer::parse_vacuum_interval(), None);
        unsafe {
            std::env::remove_var("LEANKG_VACUUM_INTERVAL_HOURS");
        }
    }

    #[test]
    fn test_parse_vacuum_interval_negative_disables() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        unsafe {
            std::env::set_var("LEANKG_VACUUM_INTERVAL_HOURS", "-1");
        }
        assert_eq!(MCPServer::parse_vacuum_interval(), None);
        unsafe {
            std::env::remove_var("LEANKG_VACUUM_INTERVAL_HOURS");
        }
    }

    #[test]
    fn test_parse_vacuum_interval_custom() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        unsafe {
            std::env::set_var("LEANKG_VACUUM_INTERVAL_HOURS", "6");
        }
        assert_eq!(
            MCPServer::parse_vacuum_interval(),
            Some(std::time::Duration::from_secs(6 * 3600))
        );
        unsafe {
            std::env::remove_var("LEANKG_VACUUM_INTERVAL_HOURS");
        }
    }

    // ---------- Embed auto-arm + multi-project helpers ----------
    //
    // Cover `MCPServer::auto_arm_cfg_from_env`, the partial-flip default,
    // `parse_project_dirs`, and `is_primary_project`. All env-mutating
    // tests serialize through `ENV_LOCK` to avoid races with the
    // vacuum-interval tests above (process-wide env vars are not
    // thread-safe across `cargo test` workers).

    /// Helper: snapshot every `LEANKG_EMBED_BACKGROUND_*` env var so a
    /// test can clean up without leaking into siblings. Returns the
    /// snapshot so callers can restore the original values.
    #[cfg(feature = "embeddings")]
    fn snapshot_embed_bg_env() -> Vec<(&'static str, Option<String>)> {
        const KEYS: &[&str] = &[
            "LEANKG_EMBED_BACKGROUND_WORKERS",
            "LEANKG_EMBED_BACKGROUND_BATCH",
            "LEANKG_EMBED_BACKGROUND_FULL",
            "LEANKG_EMBED_BACKGROUND_TYPES",
            "LEANKG_EMBED_BACKGROUND_PARTIAL",
        ];
        KEYS.iter().map(|k| (*k, std::env::var(k).ok())).collect()
    }

    #[cfg(feature = "embeddings")]
    fn restore_embed_bg_env(snap: Vec<(&'static str, Option<String>)>) {
        for (k, v) in snap {
            // SAFETY: tests serialize via ENV_LOCK; env mutation is safe
            // on this crate's edition (2021).
            unsafe {
                match v {
                    Some(v) => std::env::set_var(k, v),
                    None => std::env::remove_var(k),
                }
            }
        }
    }

    #[cfg(feature = "embeddings")]
    #[test]
    fn auto_arm_cfg_from_env_defaults_partial_true() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        let snap = snapshot_embed_bg_env();
        for (k, _) in snap.iter() {
            // SAFETY: see helper.
            unsafe {
                std::env::remove_var(k);
            }
        }
        let cfg = MCPServer::auto_arm_cfg_from_env();
        assert!(cfg.partial, "partial must default to true for auto-arm");
        assert_eq!(cfg.workers, 1);
        assert_eq!(cfg.batch_size, 32);
        assert!(!cfg.full);
        assert_eq!(cfg.types_filter, "");
        assert_eq!(cfg.rss_fraction, 0.0);
        restore_embed_bg_env(snap);
    }

    #[cfg(feature = "embeddings")]
    #[test]
    fn auto_arm_cfg_from_env_reads_workers_and_batch() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        let snap = snapshot_embed_bg_env();
        // SAFETY: see helper.
        unsafe {
            std::env::set_var("LEANKG_EMBED_BACKGROUND_WORKERS", "4");
            std::env::set_var("LEANKG_EMBED_BACKGROUND_BATCH", "64");
            std::env::set_var("LEANKG_EMBED_BACKGROUND_FULL", "true");
            std::env::set_var("LEANKG_EMBED_BACKGROUND_TYPES", "function,method");
        }
        let cfg = MCPServer::auto_arm_cfg_from_env();
        assert!(cfg.partial, "partial stays true regardless of FULL knob");
        assert_eq!(cfg.workers, 4);
        assert_eq!(cfg.batch_size, 64);
        assert!(cfg.full);
        assert_eq!(cfg.types_filter, "function,method");
        restore_embed_bg_env(snap);
    }

    #[cfg(feature = "embeddings")]
    #[test]
    fn auto_arm_cfg_from_env_clamps_invalid() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        let snap = snapshot_embed_bg_env();
        // 999 > 32 (workers cap), 99999 > 2048 (batch cap) → fall back to defaults.
        // SAFETY: see helper.
        unsafe {
            std::env::set_var("LEANKG_EMBED_BACKGROUND_WORKERS", "999");
            std::env::set_var("LEANKG_EMBED_BACKGROUND_BATCH", "99999");
            std::env::set_var("LEANKG_EMBED_BACKGROUND_FULL", "not-a-bool");
        }
        let cfg = MCPServer::auto_arm_cfg_from_env();
        assert_eq!(cfg.workers, 1, "out-of-range workers fall back to 1");
        assert_eq!(cfg.batch_size, 32, "out-of-range batch falls back to 32");
        assert!(!cfg.full, "unrecognized FULL value falls back to false");
        assert!(cfg.partial);
        restore_embed_bg_env(snap);
    }

    #[cfg(feature = "embeddings")]
    #[test]
    fn auto_arm_cfg_from_env_accepts_case_insensitive_true_false() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        let snap = snapshot_embed_bg_env();
        // SAFETY: see helper.
        unsafe {
            std::env::set_var("LEANKG_EMBED_BACKGROUND_FULL", "TRUE");
        }
        let cfg = MCPServer::auto_arm_cfg_from_env();
        assert!(cfg.full, "TRUE (uppercase) must be parsed as true");
        // SAFETY: see helper.
        unsafe {
            std::env::set_var("LEANKG_EMBED_BACKGROUND_FULL", "False");
        }
        let cfg = MCPServer::auto_arm_cfg_from_env();
        assert!(!cfg.full, "False (mixed case) must be parsed as false");
        restore_embed_bg_env(snap);
    }

    /// Cover the `partial:` flip default in
    /// `spawn_background_embed_in_process` by constructing the same
    /// `BackgroundEmbedConfig` from the env-derived inputs and asserting
    /// that with `LEANKG_EMBED_BACKGROUND_FULL=1` the produced
    /// `partial=false` and with `LEANKG_EMBED_BACKGROUND_PARTIAL=1`
    /// we can override back to `partial=true`. This pins the bug-fix
    /// behavior without dragging the full method (which needs a graph).
    #[cfg(feature = "embeddings")]
    #[test]
    fn partial_flip_default_and_override() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        let snap = snapshot_embed_bg_env();
        // Default: not full → partial=true (the bug-fix the PR introduces).
        // SAFETY: see helper.
        unsafe {
            std::env::remove_var("LEANKG_EMBED_BACKGROUND_FULL");
            std::env::remove_var("LEANKG_EMBED_BACKGROUND_PARTIAL");
        }
        let mut cfg = crate::embeddings::BackgroundEmbedConfig {
            batch_size: 32,
            workers: 1,
            full: false,
            types_filter: String::new(),
            partial: !false, // mirrors spawn_background_embed_in_process flip
            rss_fraction: 0.0,
            project_path: None,
        };
        assert!(cfg.partial);
        // Override: FULL=1 flips partial=false, then PARTIAL=1 forces it back.
        // SAFETY: see helper.
        unsafe {
            std::env::set_var("LEANKG_EMBED_BACKGROUND_FULL", "1");
        }
        let full = true;
        cfg = crate::embeddings::BackgroundEmbedConfig {
            partial: !full,
            ..cfg
        };
        assert!(!cfg.partial);
        // SAFETY: see helper.
        unsafe {
            std::env::set_var("LEANKG_EMBED_BACKGROUND_PARTIAL", "1");
        }
        cfg = crate::embeddings::BackgroundEmbedConfig {
            partial: true,
            ..cfg
        };
        assert!(cfg.partial);
        restore_embed_bg_env(snap);
    }

    #[cfg(feature = "embeddings")]
    #[test]
    fn parse_project_dirs_dedups_sorts_and_trims() {
        // Comma-separated list with duplicates + whitespace + empty entries.
        let dirs = " /workspace ,/workspace-other,/workspace,/workspace-other ,";
        let parsed = MCPServer::parse_project_dirs(dirs);
        assert_eq!(
            parsed,
            vec!["/workspace".to_string(), "/workspace-other".to_string()],
            "duplicates collapse and the list is sorted + trimmed"
        );
        // Empty input → empty vec (multi-project helper returns early).
        assert!(MCPServer::parse_project_dirs("").is_empty());
        assert!(MCPServer::parse_project_dirs("   , ,").is_empty());
    }

    #[cfg(feature = "embeddings")]
    #[test]
    fn is_primary_project_matches_by_path() {
        // Schedule helper only arms the project that equals
        // `LEANKG_MCP_PROJECT` (default /workspace); side mounts are skipped.
        assert!(MCPServer::is_primary_project("/workspace", "/workspace"));
        assert!(MCPServer::is_primary_project("/workspace", "/workspace")); // default fallback
        assert!(!MCPServer::is_primary_project(
            "/workspace-other",
            "/workspace"
        ));
        assert!(!MCPServer::is_primary_project("/workspace/", "/workspace"));
    }

    // --- SSE endpoint discovery: preserve ?project= query ----------------
    // Bug: src/mcp/server.rs hardcodes the SSE "endpoint" event to
    // `data: /mcp` regardless of the incoming ?project= query string, which
    // causes Cursor's streamable-HTTP MCP transport to drop the project
    // override and fall back to LEANKG_MCP_PROJECT (= /workspace).
    // See docs/analysis/fix-mcp-sse-discovery-preserve-project-2026-07-30.md

    #[test]
    fn returns_just_mcp_when_project_is_none() {
        assert_eq!(discovery_endpoint_url(None), "/mcp");
    }

    #[test]
    fn returns_mcp_with_query_when_project_is_set() {
        // The encoder escapes `/` per RFC 3986 so the receiver can never
        // confuse the value with a path delimiter.
        assert_eq!(
            discovery_endpoint_url(Some("/workspace-be")),
            "/mcp?project=%2Fworkspace-be"
        );
    }

    #[test]
    fn treats_empty_project_as_none() {
        assert_eq!(discovery_endpoint_url(Some("")), "/mcp");
    }

    #[test]
    fn encodes_spaces_and_special_chars() {
        // '/' is reserved-as-safe and MUST be percent-encoded per RFC 3986,
        // otherwise the receiving parser would see two `project=` segments.
        // ' ' and '?' are reserved and MUST be percent-encoded.
        assert_eq!(
            discovery_endpoint_url(Some("/workspace foo?bar")),
            "/mcp?project=%2Fworkspace%20foo%3Fbar"
        );
    }

    #[test]
    fn handles_unicode_path() {
        // Non-ASCII bytes must round-trip through encode → server's existing
        // query-string parser without data loss.
        let original = "/workspace/дөлөө";
        let url = discovery_endpoint_url(Some(original));
        assert!(
            url.starts_with("/mcp?project="),
            "endpoint must keep /mcp?project= prefix, got {url}"
        );
        let encoded = url.trim_start_matches("/mcp?project=");
        let decoded = percent_decode_path(encoded);
        assert_eq!(decoded, original, "round-trip must preserve UTF-8");
    }

    #[tokio::test]
    async fn sse_response_preserves_project_in_data_event() {
        use axum::body::to_bytes;
        let resp = build_sse_endpoint_response(Some("/workspace-be"));
        let body = to_bytes(resp.into_body(), 1024).await.unwrap();
        assert_eq!(
            &body[..],
            b"event: endpoint\ndata: /mcp?project=%2Fworkspace-be\n\n"
        );
    }

    #[tokio::test]
    async fn sse_response_omits_query_when_project_is_none() {
        use axum::body::to_bytes;
        let resp = build_sse_endpoint_response(None);
        let body = to_bytes(resp.into_body(), 1024).await.unwrap();
        assert_eq!(&body[..], b"event: endpoint\ndata: /mcp\n\n");
    }

    #[tokio::test]
    async fn sse_response_uses_text_event_stream_content_type() {
        // Cursor's streamable-HTTP transport rejects discovery responses
        // that don't carry the SSE content type.
        let resp = build_sse_endpoint_response(Some("/workspace-be"));
        let ct = resp.headers().get(header::CONTENT_TYPE).unwrap();
        assert_eq!(ct, "text/event-stream");
    }
}
