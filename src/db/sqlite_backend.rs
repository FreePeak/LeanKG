//! SQLite storage backend (v4.4.0 — dual-backend, sqlite session default).
//!
//! Resurrected verbatim from the pre-v0.20 CozoDB layer (commit 5f40d7ef^):
//! the engine speaks the SAME Datalog the PG backend translates, so all
//! 216 run_script call sites work natively. HNSW vectors via CozoDB's own
//! `::hnsw create embedding_vectors:vec_idx` (cosine, dim 384).
//!
//! One file per project: `<project>/.leankg/leankg.db` — per-project
//! isolation without PG search_path games.

use crate::db::backend::DbBackend;
use crate::db::value::NamedRows;
use cozo::{DataValue, ScriptMutability};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;
use std::path::Path;
use std::path::PathBuf;

pub type CozoDb = cozo::DbInstance;

/// Debug-only set of RocksDB paths already opened in this process.
/// RocksDB allows one handle per process per directory — a second `init_db`
/// on the same path is a bug (use `get_graph_engine_for_path` to share).
#[cfg(debug_assertions)]
static OPENED_ROCKSDB_PATHS: std::sync::Mutex<Option<std::collections::HashSet<PathBuf>>> =
    std::sync::Mutex::new(None);

/// Thin wrapper around `DbInstance::run_script` that absorbs the CozoDB 0.7.x
/// API changes (third `ScriptMutability` argument, `BTreeMap<String, DataValue>`
/// params type) so the rest of the codebase keeps the old 2-arg
/// `BTreeMap<String, serde_json::Value>` calling convention.
///
/// Mutability is auto-detected from the leading operator in the query string.
/// The full operator surface in this codebase is `:put :rm :create :replace`
/// and `PRAGMA` (writes) plus bare `?`/`::relations` reads — verified by grep.
/// New write operators (e.g. `::hnsw create`) must be added to `WRITE_PREFIXES`.
pub fn run_script(
    db: &CozoDb,
    query: &str,
    params: std::collections::BTreeMap<String, serde_json::Value>,
) -> Result<NamedRows, cozo::Error> {
    let cozo_params: std::collections::BTreeMap<String, DataValue> = params
        .into_iter()
        .map(|(k, v)| (k, json_to_datavalue(v)))
        .collect();
    let res = db.run_script(query, cozo_params, mutability_for(query))?;
    let rows: Vec<Vec<crate::db::value::DataValue>> = res
        .rows
        .iter()
        .map(|row| row.iter().map(datavalue_from_cozo).collect())
        .collect();
    Ok(NamedRows {
        headers: res.headers,
        rows,
        next: None,
    })
}

/// Convert serde_json::Value to cozo::DataValue using CozoDB's own conversion
/// semantics (matches `impl From<JsonValue> for DataValue` in cozo 0.7.6).
/// Importantly, `JsonValue::Null` becomes `DataValue::Null` (not `Bot` — `Bot`
/// is the bottom type and is rejected by nullable column coercion).
fn json_to_datavalue(v: serde_json::Value) -> DataValue {
    DataValue::from(v)
}

fn mutability_for(query: &str) -> ScriptMutability {
    // CozoDB 0.7.x enforces that Immutable scripts cannot acquire write locks.
    // A Datalog statement can combine a read head (`?[...] := ...`) with an
    // action operator (`:put`, `:rm`, etc.) in one script — the leading char
    // is `?` but the action still needs a write lock. So we scan the whole
    // query for any write operator, not just the prefix.
    const WRITE_TOKENS: &[&str] = &[
        ":put",
        ":rm",
        ":create",
        ":replace",
        ":delete",
        ":update",
        ":insert",
        "PRAGMA",
        "::set_triggers",
        "::hnsw",
        "::lsh",
        "::fts",
        "::index",
    ];
    if WRITE_TOKENS.iter().any(|t| query.contains(t)) {
        ScriptMutability::Mutable
    } else {
        ScriptMutability::Immutable
    }
}

const DEFAULT_ROCKSDB_ROOT: &str = ".leankg-rocksdb";

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum StorageEngine {
    Sqlite,
    RocksDb,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct StorageConfig {
    pub engine: StorageEngine,
    pub path: std::path::PathBuf,
}

fn get_env_mmap_size() -> u64 {
    std::env::var("LEANKG_MMAP_SIZE")
        .ok()
        .and_then(|v| v.parse().ok())
        // Default 64 MiB. SQLite's mmap'd region is resident in the process RSS,
        // so a smaller mmap dramatically reduces the container's memory footprint
        // for large codebases. 256 MiB was far too aggressive for the common case
        // (it pushed containers past their memory limits and triggered OOM kills).
        .unwrap_or(64 * 1024 * 1024)
}

/// Open a read-only database handle. For SQLite, this uses `mode=ro` URI and
/// does not acquire the writer lock. For RocksDB, CozoDB 0.7.x does NOT expose
/// `mode=readonly` via its options string — the empty-string open is still the
/// "primary" handle and will try to acquire the LOCK. This is the Phase 1
/// short-term workaround (plan §Phase 2); Phase 2 will use RocksDB secondary
/// instances for true read-only replicas that don't take the LOCK at all.
///
/// ponytail: when CozoDB gains `mode=readonly` (or we fork it), flip the
/// RocksDB arm to `cozo::DbInstance::new("rocksdb", &path_str, "mode=readonly")`.
/// Today the function is best-effort: SQLite gets true RO, RocksDB gets the
/// same handle as `init_db` so callers must enforce write-tool rejection at
/// the server layer (see `MCPServer::read_only`).
///
/// The `OPENED_ROCKSDB_PATHS` debug guard above intentionally does NOT cover
/// this path: read-only opens legitimately co-exist with the writer handle
/// for query-only replicas.
pub fn init_db_readonly(db_path: &Path) -> Result<CozoDb, Box<dyn std::error::Error>> {
    let storage = resolve_storage_config(db_path);
    let path_str = storage.path.to_string_lossy().to_string();
    Ok(match storage.engine {
        // CozoDB does not accept `mode=readonly` for rocksdb in 0.7.6 — keep
        // an empty options string. The server layer is responsible for
        // rejecting write tools when `MCPServer::read_only` is true.
        StorageEngine::RocksDb => {
            std::fs::create_dir_all(&storage.path)?;
            cozo::DbInstance::new("rocksdb", &path_str, "")?
        }
        StorageEngine::Sqlite => cozo::DbInstance::new("sqlite", &path_str, "mode=ro")?,
    })
}

/// Recommended RocksDB tuning knobs. CozoDB 0.7.x does NOT expose RocksDB's
/// `DBOptions` / `ColumnFamilyOptions` via its Rust API, so these values are
/// captured only as diagnostic env-var reads today — CozoDB internally uses
/// its own defaults. A fork of CozoDB that accepts `block_cache_size=`,
/// `max_open_files=`, `bloom_filter_bits=` etc. via the options string is the
/// real fix; for now we surface the knobs so operators can document intent
/// and we can wire them in one place when the upstream patch lands.
///
/// ponytail: recommended production values below are baked from the RocksDB
/// wiki for read-heavy workloads. Tuning is a single fork of CozoDB away.
#[derive(Debug, Clone, Copy)]
pub struct RocksDbTuning {
    /// `-1` = unlimited file descriptors. Default 1024 in RocksDB is too low
    /// for graphs with >100K SST files.
    pub max_open_files: i32,
    /// Block cache size in MB. Default 8 MB; production wants 2-8 GB.
    pub block_cache_size_mb: usize,
    /// Bits per key for bloom filters. 10 is a good read-heavy default.
    pub bloom_filter_bits: usize,
    /// Memtable write buffer size in MB. Default 64 MB; production wants 256.
    pub write_buffer_size_mb: usize,
}

impl Default for RocksDbTuning {
    fn default() -> Self {
        Self {
            max_open_files: -1,
            block_cache_size_mb: 2048,
            bloom_filter_bits: 10,
            write_buffer_size_mb: 256,
        }
    }
}

impl RocksDbTuning {
    /// Read tuning from `LEANKG_ROCKSDB_*` env vars, falling back to defaults.
    /// Today these values are only logged — they cannot be applied until
    /// CozoDB 0.7 exposes the knobs (see struct docs).
    pub fn from_env() -> Self {
        let defaults = Self::default();
        Self {
            max_open_files: std::env::var("LEANKG_ROCKSDB_MAX_OPEN_FILES")
                .ok()
                .and_then(|v| v.parse().ok())
                .unwrap_or(defaults.max_open_files),
            block_cache_size_mb: std::env::var("LEANKG_ROCKSDB_BLOCK_CACHE_MB")
                .ok()
                .and_then(|v| v.parse().ok())
                .unwrap_or(defaults.block_cache_size_mb),
            bloom_filter_bits: std::env::var("LEANKG_ROCKSDB_BLOOM_BITS")
                .ok()
                .and_then(|v| v.parse().ok())
                .unwrap_or(defaults.bloom_filter_bits),
            write_buffer_size_mb: std::env::var("LEANKG_ROCKSDB_WRITE_BUFFER_MB")
                .ok()
                .and_then(|v| v.parse().ok())
                .unwrap_or(defaults.write_buffer_size_mb),
        }
    }
}

pub fn init_db(db_path: &Path) -> Result<CozoDb, Box<dyn std::error::Error>> {
    let storage = resolve_storage_config(db_path);

    // Guard: detect duplicate RocksDB opens in debug builds (tests / cargo run).
    #[cfg(debug_assertions)]
    if storage.engine == StorageEngine::RocksDb {
        let mut opened = OPENED_ROCKSDB_PATHS.lock().unwrap();
        let opened = opened.get_or_insert_with(std::collections::HashSet::new);
        let key = storage.path.clone();
        assert!(
            opened.insert(key.clone()),
            "BUG: RocksDB handle opened twice for {:?}. Use get_graph_engine_for_path() to share handles.",
            key
        );
    }

    if let Some(parent) = storage.path.parent() {
        std::fs::create_dir_all(parent)?;
    }

    let path_str = storage.path.to_string_lossy().to_string();
    let db = match storage.engine {
        StorageEngine::Sqlite => cozo::DbInstance::new("sqlite", &path_str, "")?,
        StorageEngine::RocksDb => {
            std::fs::create_dir_all(&storage.path)?;
            // ponytail: LEANKG_ROCKSDB_* tuning knobs are captured here for
            // diagnostic logging — CozoDB 0.7 does not yet expose them via
            // its options string. See `RocksDbTuning` docs.
            let tuning = RocksDbTuning::from_env();
            tracing::info!(
                "RocksDB tuning intent (not yet applied): max_open_files={}, \
                 block_cache_mb={}, bloom_bits={}, write_buffer_mb={}",
                tuning.max_open_files,
                tuning.block_cache_size_mb,
                tuning.bloom_filter_bits,
                tuning.write_buffer_size_mb
            );
            cozo::DbInstance::new("rocksdb", &path_str, "")?
        }
    };

    // ponytail: enterprise two-container mode (LEANKG_COZO_ENDPOINT) is
    // gated by entrypoint.sh health today; the Rust HTTP client that wires
    // `init_db` to a remote cozoserver is a follow-up. Upgrade path: add a
    // `CozoClient` enum (Embedded | Remote), branch here, route the
    // ~23 callers of `run_script` through it. Compose already exposes the
    // endpoint, so this is code-only.

    let mmap_size = get_env_mmap_size();
    tracing::info!(
        "Cozo storage = {:?} at {} (LEANKG_MMAP_SIZE={})",
        storage.engine,
        storage.path.display(),
        mmap_size
    );

    // CozoDB 0.7.x no longer accepts raw PRAGMA statements — the parser rejects
    // them silently. SQLite tuning is now done by CozoDB internally based on
    // the storage engine's own defaults. LEANKG_MMAP_SIZE is captured for
    // diagnostic logging only.
    let _ = mmap_size;

    init_schema(&db)?;

    Ok(db)
}

pub fn resolve_storage_config(db_path: &Path) -> StorageConfig {
    match std::env::var("LEANKG_DB_ENGINE")
        .unwrap_or_else(|_| "sqlite".to_string())
        .to_ascii_lowercase()
        .as_str()
    {
        "rocksdb" | "rocks" | "rockdb" => StorageConfig {
            engine: StorageEngine::RocksDb,
            path: central_project_storage_path(db_path),
        },
        _ => StorageConfig {
            engine: StorageEngine::Sqlite,
            path: if db_path.is_dir() {
                db_path.join("leankg.db")
            } else {
                db_path.to_path_buf()
            },
        },
    }
}

pub(crate) fn central_project_storage_path(db_path: &Path) -> std::path::PathBuf {
    let root = std::env::var_os("LEANKG_ROCKSDB_ROOT")
        .map(std::path::PathBuf::from)
        .or_else(|| dirs::home_dir().map(|home| home.join(DEFAULT_ROCKSDB_ROOT)))
        .unwrap_or_else(|| std::path::PathBuf::from(DEFAULT_ROCKSDB_ROOT));

    // Walk up to the project root regardless of which form the caller passed.
    //
    // Callers historically varied on what they passed to `init_db`:
    //   * `Index` / `McpHttp`     -> `<project>/.leankg`        (the directory)
    //   * `Embed` / `SemanticContext` / `SmokeTest` -> `<project>/.leankg/leankg.db` (the file)
    //
    // If we used the file path directly, the SHA-256 hash of the project key
    // would differ from the indexer, so `leankg embed` would open a brand
    // new (empty) RocksDB at a different path, find zero `code_elements`,
    // and silently report "Embedded: 0" — leaving the agent with a working
    // ontology-first `semantic_search` and a non-functional HNSW path.
    //
    // FR-HNSW-C fix: accept both forms and always hash the project root,
    // not the .leankg directory or the database file inside it.
    let project_root = project_root_from_db_path(db_path);
    let project_key = project_root
        .canonicalize()
        .unwrap_or_else(|_| project_root.to_path_buf());
    let project_key = project_key.to_string_lossy();
    let mut hasher = Sha256::new();
    hasher.update(project_key.as_bytes());
    let hash = format!("{:x}", hasher.finalize());
    let name = project_root
        .file_name()
        .and_then(|name| name.to_str())
        .unwrap_or("project")
        .chars()
        .map(|ch| {
            if ch.is_ascii_alphanumeric() || ch == '-' || ch == '_' {
                ch
            } else {
                '-'
            }
        })
        .collect::<String>();

    root.join("projects")
        .join(format!("{}-{}", name, &hash[..12]))
}

/// Reduce a `db_path` to the project root, regardless of which form the
/// caller passed (the `.leankg` directory, the `leankg.db` file inside it,
/// or an unrelated path).
///
/// * `<project>/.leankg/leankg.db` -> `<project>`
/// * `<project>/.leankg`           -> `<project>`
/// * `<project>`                   -> `<project>`
/// * `leankg.db`                   -> `.` (no parent anchor — caller error)
fn project_root_from_db_path(db_path: &Path) -> std::path::PathBuf {
    let file_name = db_path.file_name().and_then(|n| n.to_str());
    if file_name == Some("leankg.db") {
        // The file lives inside `.leankg/`. Walk up two levels to the
        // project root, tolerating a missing `.leankg` parent (the file
        // may have been moved out by a manual export).
        if let Some(leankg_dir) = db_path.parent() {
            if leankg_dir.file_name().and_then(|n| n.to_str()) == Some(".leankg") {
                if let Some(project) = leankg_dir.parent() {
                    return project.to_path_buf();
                }
            }
            return leankg_dir.to_path_buf();
        }
        return std::path::PathBuf::from(".");
    }
    if file_name == Some(".leankg") {
        return db_path.parent().unwrap_or(db_path).to_path_buf();
    }
    db_path.to_path_buf()
}

fn init_schema(db: &CozoDb) -> Result<(), Box<dyn std::error::Error>> {
    let check_relations = r#"::relations"#;
    let relations_result = run_script(db, check_relations, Default::default())?;
    let existing_relations: std::collections::HashSet<String> = relations_result
        .rows
        .iter()
        .filter_map(|row| row.first().and_then(|v| v.get_str().map(String::from)))
        .collect();

    if !existing_relations.contains("code_elements") {
        let create_code_elements = r#":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}"#;
        if let Err(e) = run_script(db, create_code_elements, Default::default()) {
            eprintln!("Failed to create code_elements: {:?}", e);
        }
    } else {
        let create_file_path_index =
            r#"::index create code_elements:file_path_index { file_path }"#;
        if let Err(e) = run_script(db, create_file_path_index, Default::default()) {
            tracing::debug!("file_path index may already exist: {:?}", e);
        }

        let create_qualified_name_index =
            r#"::index create code_elements:qualified_name_index { qualified_name }"#;
        if let Err(e) = run_script(db, create_qualified_name_index, Default::default()) {
            tracing::debug!("qualified_name index may already exist: {:?}", e);
        }

        let create_element_type_index =
            r#"::index create code_elements:element_type_index { element_type }"#;
        if let Err(e) = run_script(db, create_element_type_index, Default::default()) {
            tracing::debug!("element_type index may already exist: {:?}", e);
        }

        let create_parent_qualified_index =
            r#"::index create code_elements:parent_qualified_index { parent_qualified }"#;
        if let Err(e) = run_script(db, create_parent_qualified_index, Default::default()) {
            tracing::debug!("parent_qualified index may already exist: {:?}", e);
        }

        validate_code_elements_schema(db)?;
    }

    if !existing_relations.contains("relationships") {
        let create_relationships = r#":create relationships {source_qualified: String, target_qualified: String, rel_type: String, confidence: Float, metadata: String, env: String default 'local'}"#;
        if let Err(e) = run_script(db, create_relationships, Default::default()) {
            eprintln!("Failed to create relationships: {:?}", e);
        }
    } else {
        let create_rel_type_index = r#"::index create relationships:rel_type_index { rel_type }"#;
        if let Err(e) = run_script(db, create_rel_type_index, Default::default()) {
            tracing::debug!("rel_type index may already exist: {:?}", e);
        }

        let create_target_index =
            r#"::index create relationships:target_qualified_index { target_qualified }"#;
        if let Err(e) = run_script(db, create_target_index, Default::default()) {
            tracing::debug!("target_qualified index may already exist: {:?}", e);
        }

        let create_source_index =
            r#"::index create relationships:source_qualified_index { source_qualified }"#;
        if let Err(e) = run_script(db, create_source_index, Default::default()) {
            tracing::debug!("source_qualified index may already exist: {:?}", e);
        }

        validate_relationships_schema(db)?;
    }

    if !existing_relations.contains("business_logic") {
        let create_business_logic = r#":create business_logic {element_qualified: String, description: String, user_story_id: String?, feature_id: String?}"#;
        if let Err(e) = run_script(db, create_business_logic, Default::default()) {
            eprintln!("Failed to create business_logic: {:?}", e);
        }
    }

    if !existing_relations.contains("context_metrics") {
        let create_context_metrics = r#":create context_metrics {tool_name: String, timestamp: Int, project_path: String, input_tokens: Int, output_tokens: Int, output_elements: Int, execution_time_ms: Int, baseline_tokens: Int, baseline_lines_scanned: Int, tokens_saved: Int, savings_percent: Float, correct_elements: Int?, total_expected: Int?, f1_score: Float?, query_pattern: String?, query_file: String?, query_depth: Int?, success: Bool, is_deleted: Bool}"#;
        if let Err(e) = run_script(db, create_context_metrics, Default::default()) {
            eprintln!("Failed to create context_metrics: {:?}", e);
        }

        let create_tool_index = r#"::index create context_metrics:tool_name_index { tool_name }"#;
        if let Err(e) = run_script(db, create_tool_index, Default::default()) {
            tracing::debug!("tool_name index may already exist: {:?}", e);
        }

        let create_timestamp_index =
            r#"::index create context_metrics:timestamp_index { timestamp }"#;
        if let Err(e) = run_script(db, create_timestamp_index, Default::default()) {
            tracing::debug!("timestamp index may already exist: {:?}", e);
        }

        let create_project_index =
            r#"::index create context_metrics:project_path_index { project_path }"#;
        if let Err(e) = run_script(db, create_project_index, Default::default()) {
            tracing::debug!("project_path index may already exist: {:?}", e);
        }
    }

    if !existing_relations.contains("query_cache") {
        let create_query_cache = r#":create query_cache {cache_key: String, value_json: String, created_at: Int, ttl_seconds: Int, tool_name: String, project_path: String, metadata: String}"#;
        if let Err(e) = run_script(db, create_query_cache, Default::default()) {
            eprintln!("Failed to create query_cache: {:?}", e);
        }

        let create_key_index = r#"::index create query_cache:cache_key_index { cache_key }"#;
        if let Err(e) = run_script(db, create_key_index, Default::default()) {
            tracing::debug!("cache_key index may already exist: {:?}", e);
        }

        let create_tool_index = r#"::index create query_cache:tool_name_index { tool_name }"#;
        if let Err(e) = run_script(db, create_tool_index, Default::default()) {
            tracing::debug!("tool_name index may already exist: {:?}", e);
        }
    }

    // Run migrations for schema evolution
    run_migrations(db, &existing_relations)?;

    // Do not rely solely on the migration ledger for canonical graph arity.
    // Some older databases recorded migration 006 while retaining the
    // pre-env 11-column code_elements relation, which breaks all current
    // graph queries at runtime.
    repair_canonical_schema(db, &existing_relations)?;

    // Create service_metadata table if not exists (idempotent via migration)
    if !existing_relations.contains("service_metadata") {
        let create_svc = r#":create service_metadata {service_name: String, env: String default 'local', team: String?, on_call: String?, repo_url: String?, language: String?, health_endpoint: String?, slo_p99_ms: Int?, incident_count: Int, last_incident: Int?, tags: String, version: String?, deploy_envs: String, created_at: Int, updated_at: Int}"#;
        if let Err(e) = run_script(db, create_svc, Default::default()) {
            tracing::warn!("Failed to create service_metadata: {:?}", e);
        }
        let svc_indexes = [
            r#"::index create service_metadata:svc_name_index { service_name }"#,
            r#"::index create service_metadata:svc_env_index { env }"#,
        ];
        for idx in &svc_indexes {
            if let Err(e) = run_script(db, idx, Default::default()) {
                tracing::debug!("service_metadata index note: {:?}", e);
            }
        }
    }

    // Create teams table for shared graph management
    if !existing_relations.contains("teams") {
        let create_teams = r#":create teams {id: String, name: String, description: String, owner_id: String, created_at: Int, updated_at: Int, graph_read_users: String, graph_write_users: String, members: String}"#;
        if let Err(e) = run_script(db, create_teams, Default::default()) {
            tracing::warn!("Failed to create teams: {:?}", e);
        }
        let team_indexes = [r#"::index create teams:owner_index { owner_id }"#];
        for idx in &team_indexes {
            if let Err(e) = run_script(db, idx, Default::default()) {
                tracing::debug!("teams index note: {:?}", e);
            }
        }
    }

    // Create team_invites table for onboarding workflow
    if !existing_relations.contains("team_invites") {
        let create_invites = r#":create team_invites {token: String, team_id: String, email: String?, role: String, created_by: String, created_at: Int, expires_at: Int, accepted: Bool, accepted_by: String?}"#;
        if let Err(e) = run_script(db, create_invites, Default::default()) {
            tracing::warn!("Failed to create team_invites: {:?}", e);
        }
        let invite_indexes = [
            r#"::index create team_invites:team_index { team_id }"#,
            r#"::index create team_invites:token_index { token }"#,
        ];
        for idx in &invite_indexes {
            if let Err(e) = run_script(db, idx, Default::default()) {
                tracing::debug!("team_invites index note: {:?}", e);
            }
        }
    }

    // NOTE: the embedding-state tables are ensured in `SqliteBackend::open`
    // (they need a `DbBackend` handle, not the raw `CozoDb` this free
    // function receives).
    Ok(())
}

/// FR-ENT-1 on sqlite: audit ledger DDL mirroring PG migration 006
/// (append-only enforced at the app layer — cozo has no triggers here).
pub fn ensure_audit_log_table(db: &CozoDb) -> Result<(), Box<dyn std::error::Error>> {
    // Idempotent: cozo :create errors on an existing relation (RelNameConflict),
    // which would brick every reopen of an existing project. Probe first.
    let exists = run_script(db, "::columns audit_log", Default::default()).is_ok();
    if exists {
        return Ok(());
    }
    let ddl = r#"
        :create audit_log {
            id: Int => ts: Int, actor: String, agent_client: String?,
            tool: String, project: String?, args_hash: String,
            result_status: String, prev_hash: String?, entry_hash: String
        }
    "#;
    run_script(db, ddl, Default::default())?;
    Ok(())
}

fn run_migrations(
    db: &CozoDb,
    existing_relations: &std::collections::HashSet<String>,
) -> Result<(), Box<dyn std::error::Error>> {
    // Create migrations tracking table if not exists
    if !existing_relations.contains("migrations") {
        let create_migrations = r#":create migrations {id: String, applied_at: Int}"#;
        if let Err(e) = run_script(db, create_migrations, Default::default()) {
            tracing::warn!("Failed to create migrations table: {:?}", e);
        }
    }

    // Get already-applied migrations
    let applied: std::collections::HashSet<String> =
        run_script(db, "?[id] := *migrations[id, _]", Default::default())
            .map(|r| {
                r.rows
                    .iter()
                    .filter_map(|row| row.first().and_then(|v| v.get_str().map(String::from)))
                    .collect()
            })
            .unwrap_or_default();

    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64;

    // Migration 001: Create knowledge_entries table
    if !applied.contains("001_knowledge_entries") {
        tracing::info!("Running migration 001_knowledge_entries...");
        let create_knowledge = r#":create knowledge_entries {id: String, knowledge_type: String, title: String, content: String, element_qualified: String?, user_story_id: String?, feature_id: String?, tags: String, environment: String, branch: String?, author: String, created_at: Int, updated_at: Int}"#;
        if let Err(e) = run_script(db, create_knowledge, Default::default()) {
            tracing::warn!("Migration 001 failed (may already exist): {:?}", e);
        }

        // Create indexes
        let indexes = [
            r#"::index create knowledge_entries:type_index { knowledge_type }"#,
            r#"::index create knowledge_entries:element_index { element_qualified }"#,
            r#"::index create knowledge_entries:env_index { environment }"#,
            r#"::index create knowledge_entries:author_index { author }"#,
        ];
        for idx in &indexes {
            if let Err(e) = run_script(db, idx, Default::default()) {
                tracing::debug!("Index creation note: {:?}", e);
            }
        }

        record_migration(db, "001_knowledge_entries", now)?;
    }

    // Migration 002: Create feature_workflow_links table
    if !applied.contains("002_feature_workflow_links") {
        tracing::info!("Running migration 002_feature_workflow_links...");
        let create_fw_links =
            r#":create feature_workflow_links {feature_id: String, workflow_id: String}"#;
        if let Err(e) = run_script(db, create_fw_links, Default::default()) {
            tracing::warn!("Migration 002 failed (may already exist): {:?}", e);
        }
        // Create index on feature_id
        let fw_index = r#"::index create feature_workflow_links:feature_id_index { feature_id }"#;
        if let Err(e) = run_script(db, fw_index, Default::default()) {
            tracing::debug!("feature_workflow_links index creation note: {:?}", e);
        }
        record_migration(db, "002_feature_workflow_links", now)?;
    }

    // Migration 002-005 have been consolidated into migration 006.
    // The old migration IDs are recorded as applied to skip the stacked
    // :replace chain that caused schema drift (environment vs env columns).
    mark_legacy_migrations_as_applied(db, &applied, now)?;

    // Migration 006: Safe canonical schema repair for code_elements and
    // relationships, plus incident table creation. Replaces the old 002-005
    // stacked :replace chain. This migration is idempotent: it inspects the
    // current column count and only performs a :replace when the schema
    // does not match the canonical 13-column (code_elements) or 6-column
    // (relationships) layout. Non-matching schemas (e.g. with extra
    // environment/branch/version_tag columns from old partial migrations)
    // are repaired to the canonical form.
    if !applied.contains("006_safe_canonical_schema_repair") {
        tracing::info!("Running migration 006_safe_canonical_schema_repair...");
        repair_canonical_schema(db, existing_relations)?;
        record_migration(db, "006_safe_canonical_schema_repair", now)?;
    }

    Ok(())
}

fn mark_legacy_migrations_as_applied(
    db: &CozoDb,
    applied: &std::collections::HashSet<String>,
    now: i64,
) -> Result<(), Box<dyn std::error::Error>> {
    let legacy_ids = [
        "002_code_elements_versioning",
        "003_business_logic_versioning",
        "004_env_and_incidents",
        "005_canonical_env_graph_schema",
    ];
    for id in &legacy_ids {
        if !applied.contains(*id) {
            record_migration(db, id, now)?;
        }
    }
    Ok(())
}

fn repair_canonical_schema(
    db: &CozoDb,
    existing_relations: &std::collections::HashSet<String>,
) -> Result<(), Box<dyn std::error::Error>> {
    ensure_canonical_code_elements(db, existing_relations)?;
    ensure_canonical_relationships(db, existing_relations)?;
    if let Err(e) = ensure_incidents_table(db) {
        tracing::warn!("incidents table creation failed: {:?}", e);
    }
    Ok(())
}

const REPAIR_LEGACY_CODE_ELEMENTS_11_TO_13: &str = r#"
?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] :=
    *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata],
    env = "local",
    ontology_layer = "procedural"
:replace code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}
"#;
const REPAIR_LEGACY_CODE_ELEMENTS_12_TO_13: &str = r#"
?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] :=
    *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env],
    ontology_layer = "procedural"
:replace code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}
"#;
const REPAIR_LEGACY_RELATIONSHIPS_5_TO_6: &str = r#"
?[source_qualified, target_qualified, rel_type, confidence, metadata, env] :=
    *relationships[source_qualified, target_qualified, rel_type, confidence, metadata],
    env = "local"
:replace relationships {source_qualified: String, target_qualified: String, rel_type: String, confidence: Float, metadata: String, env: String default 'local'}
"#;

fn get_column_count(db: &CozoDb, relation: &str) -> usize {
    let arity_probe = match relation {
        "code_elements" => Some(vec![
            (
                13,
                "?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] :limit 0",
            ),
            (
                12,
                "?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] :limit 0",
            ),
            (
                11,
                "?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] :limit 0",
            ),
        ]),
        "relationships" => Some(vec![
            (
                6,
                "?[source_qualified] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, env] :limit 0",
            ),
            (
                5,
                "?[source_qualified] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata] :limit 0",
            ),
        ]),
        _ => None,
    };

    if let Some(probes) = arity_probe {
        for (arity, query) in probes {
            if run_script(db, query, Default::default()).is_ok() {
                return arity;
            }
        }
    }

    let query = format!(":schema {}", relation);
    run_script(db, &query, Default::default())
        .map(|r| r.rows.len())
        .unwrap_or(0)
}

/// Canonical column lists per arity, keyed by relation name. Used by
/// `get_relation_schema` to translate an arity probe into a concrete list
/// of column names. Keep in sync with `:create` statements above and with
/// the repair scripts (`REPAIR_LEGACY_CODE_ELEMENTS_*`, `REPAIR_LEGACY_RELATIONSHIPS_5_TO_6`).
const CODE_ELEMENTS_13_COLUMNS: &[&str] = &[
    "qualified_name",
    "element_type",
    "name",
    "file_path",
    "line_start",
    "line_end",
    "language",
    "parent_qualified",
    "cluster_id",
    "cluster_label",
    "metadata",
    "env",
    "ontology_layer",
];

const CODE_ELEMENTS_12_COLUMNS: &[&str] = &[
    "qualified_name",
    "element_type",
    "name",
    "file_path",
    "line_start",
    "line_end",
    "language",
    "parent_qualified",
    "cluster_id",
    "cluster_label",
    "metadata",
    "env",
];

const CODE_ELEMENTS_11_COLUMNS: &[&str] = &[
    "qualified_name",
    "element_type",
    "name",
    "file_path",
    "line_start",
    "line_end",
    "language",
    "parent_qualified",
    "cluster_id",
    "cluster_label",
    "metadata",
];

const RELATIONSHIPS_6_COLUMNS: &[&str] = &[
    "source_qualified",
    "target_qualified",
    "rel_type",
    "confidence",
    "metadata",
    "env",
];

const RELATIONSHIPS_5_COLUMNS: &[&str] = &[
    "source_qualified",
    "target_qualified",
    "rel_type",
    "confidence",
    "metadata",
];

/// Schema snapshot for one CozoDB relation. Returned by `get_relation_schema`
/// and consumed by callers that need to write arity-correct Datalog rules
/// (e.g. the ontology self-test in `mcp/kg_self_test.rs`).
#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub struct RelationSchema {
    pub name: String,
    pub arity: usize,
    pub columns: Vec<String>,
    pub canonical: bool,
}

/// Returns the live schema for the named relation. `columns` is the ordered
/// list of column names as the relation is currently defined; `canonical`
/// is true when the live arity matches the current canonical schema
/// (13 for code_elements, 6 for relationships).
pub fn get_relation_schema(db: &CozoDb, relation: &str) -> RelationSchema {
    let arity = get_column_count(db, relation);
    let columns: Vec<String> = match relation {
        "code_elements" => match arity {
            13 => CODE_ELEMENTS_13_COLUMNS
                .iter()
                .map(|s| s.to_string())
                .collect(),
            12 => CODE_ELEMENTS_12_COLUMNS
                .iter()
                .map(|s| s.to_string())
                .collect(),
            11 => CODE_ELEMENTS_11_COLUMNS
                .iter()
                .map(|s| s.to_string())
                .collect(),
            _ => Vec::new(),
        },
        "relationships" => match arity {
            6 => RELATIONSHIPS_6_COLUMNS
                .iter()
                .map(|s| s.to_string())
                .collect(),
            5 => RELATIONSHIPS_5_COLUMNS
                .iter()
                .map(|s| s.to_string())
                .collect(),
            _ => Vec::new(),
        },
        _ => Vec::new(),
    };
    let canonical = match relation {
        "code_elements" => arity == 13,
        "relationships" => arity == 6,
        _ => arity > 0,
    };
    RelationSchema {
        name: relation.to_string(),
        arity,
        columns,
        canonical,
    }
}

/// Convenience accessor for the code_elements relation.
pub fn code_elements_schema(db: &CozoDb) -> RelationSchema {
    get_relation_schema(db, "code_elements")
}

/// Convenience accessor for the relationships relation.
pub fn relationships_schema(db: &CozoDb) -> RelationSchema {
    get_relation_schema(db, "relationships")
}

fn ensure_canonical_code_elements(
    db: &CozoDb,
    existing_relations: &std::collections::HashSet<String>,
) -> Result<(), Box<dyn std::error::Error>> {
    if !existing_relations.contains("code_elements") {
        return Ok(());
    }
    const EXPECTED: usize = 13;
    let current = get_column_count(db, "code_elements");
    if current == EXPECTED {
        tracing::info!(
            "code_elements schema already canonical ({} columns), skipping replace",
            current
        );
        return Ok(());
    }
    tracing::info!(
        "code_elements schema has {} columns (expected {}), applying canonical :replace",
        current,
        EXPECTED
    );
    // CozoDB 0.7.6 refuses to :replace a relation that has indices — drop them
    // first, then recreate after the replace. Best-effort: legacy DBs may have
    // a different index set, so we tolerate "index not found" errors.
    for idx in &[
        "file_path_index",
        "qualified_name_index",
        "element_type_index",
        "parent_qualified_index",
    ] {
        let _ = run_script(
            db,
            &format!("::index drop code_elements:{}", idx),
            Default::default(),
        );
    }
    match current {
        11 => {
            run_script(db, REPAIR_LEGACY_CODE_ELEMENTS_11_TO_13, Default::default())?;
        }
        12 => {
            run_script(db, REPAIR_LEGACY_CODE_ELEMENTS_12_TO_13, Default::default())?;
        }
        _ => {
            tracing::warn!(
                "code_elements schema has unsupported arity {}; canonical repair only supports legacy 11- or 12-column schema",
                current
            );
            return Ok(());
        }
    }
    tracing::info!("code_elements :replace successful, recreating indices");
    // Recreate indices dropped before the :replace (CozoDB 0.7.6 requirement).
    for idx_query in &[
        r#"::index create code_elements:file_path_index { file_path }"#,
        r#"::index create code_elements:qualified_name_index { qualified_name }"#,
        r#"::index create code_elements:element_type_index { element_type }"#,
        r#"::index create code_elements:parent_qualified_index { parent_qualified }"#,
    ] {
        let _ = run_script(db, idx_query, Default::default());
    }
    Ok(())
}

fn ensure_canonical_relationships(
    db: &CozoDb,
    existing_relations: &std::collections::HashSet<String>,
) -> Result<(), Box<dyn std::error::Error>> {
    if !existing_relations.contains("relationships") {
        return Ok(());
    }
    const EXPECTED: usize = 6;
    let current = get_column_count(db, "relationships");
    if current == EXPECTED {
        tracing::info!(
            "relationships schema already canonical ({} columns), skipping replace",
            current
        );
        return Ok(());
    }
    tracing::info!(
        "relationships schema has {} columns (expected {}), applying canonical :replace",
        current,
        EXPECTED
    );
    if current != 5 {
        tracing::warn!(
            "relationships schema has unsupported arity {}; canonical repair only supports legacy 5-column schema",
            current
        );
        return Ok(());
    }
    // Drop indices before :replace (CozoDB 0.7.6 requirement).
    for idx in &["rel_type_index", "target_qualified_index"] {
        let _ = run_script(
            db,
            &format!("::index drop relationships:{}", idx),
            Default::default(),
        );
    }
    run_script(db, REPAIR_LEGACY_RELATIONSHIPS_5_TO_6, Default::default())?;
    // Recreate indices after :replace.
    for idx_query in &[
        r#"::index create relationships:rel_type_index { rel_type }"#,
        r#"::index create relationships:target_qualified_index { target_qualified }"#,
    ] {
        let _ = run_script(db, idx_query, Default::default());
    }
    tracing::info!("relationships :replace successful");
    Ok(())
}

fn ensure_incidents_table(db: &CozoDb) -> Result<(), Box<dyn std::error::Error>> {
    let existing = run_script(db, "::relations", Default::default())
        .map(|r| {
            r.rows
                .iter()
                .filter_map(|row| row.first().and_then(|v| v.get_str().map(String::from)))
                .collect::<std::collections::HashSet<_>>()
        })
        .unwrap_or_default();
    if existing.contains("incidents") {
        return Ok(());
    }
    let create_incidents = r#":create incidents {id: String, env: String, title: String, severity: String, occurred_at: Int, resolved_at: Int?, root_cause: String, resolution: String, affected_services: String, trigger_pattern: String?, prevention: String?, tags: String, author: String, linked_ticket: String?}"#;
    run_script(db, create_incidents, Default::default())?;
    for idx in &[
        r#"::index create incidents:env_index { env }"#,
        r#"::index create incidents:severity_index { severity }"#,
        r#"::index create incidents:author_index { author }"#,
    ] {
        if let Err(e) = run_script(db, idx, Default::default()) {
            tracing::debug!("Incident index note: {:?}", e);
        }
    }
    Ok(())
}

fn record_migration(
    db: &CozoDb,
    id: &str,
    applied_at: i64,
) -> Result<(), Box<dyn std::error::Error>> {
    let query = r#"?[id, applied_at] <- [[$mid, $ts]] :put migrations {id, applied_at}"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert("mid".to_string(), serde_json::Value::String(id.to_string()));
    params.insert(
        "ts".to_string(),
        serde_json::Value::Number(applied_at.into()),
    );
    run_script(db, query, params)?;
    Ok(())
}

fn validate_code_elements_schema(db: &CozoDb) -> Result<(), Box<dyn std::error::Error>> {
    let schema_query = r#":schema code_elements"#;
    match run_script(db, schema_query, Default::default()) {
        Ok(result) => {
            let column_count = result.rows.len();
            const EXPECTED_COLUMNS: usize = 13;
            if column_count != EXPECTED_COLUMNS {
                eprintln!(
                    "WARNING: code_elements schema has {} columns, expected {}. \
                     Schema may be from an older version. Consider re-indexing.",
                    column_count, EXPECTED_COLUMNS
                );
            }
        }
        Err(e) => {
            tracing::debug!("Could not validate code_elements schema: {:?}", e);
        }
    }
    Ok(())
}

fn validate_relationships_schema(db: &CozoDb) -> Result<(), Box<dyn std::error::Error>> {
    let schema_query = r#":schema relationships"#;
    match run_script(db, schema_query, Default::default()) {
        Ok(result) => {
            let column_count = result.rows.len();
            const EXPECTED_COLUMNS: usize = 6;
            if column_count != EXPECTED_COLUMNS {
                eprintln!(
                    "WARNING: relationships schema has {} columns, expected {}. \
                     Schema may be from an older version. Consider re-indexing.",
                    column_count, EXPECTED_COLUMNS
                );
            }
        }
        Err(e) => {
            tracing::debug!("Could not validate relationships schema: {:?}", e);
        }
    }
    Ok(())
}

// ---------------------------------------------------------------------------
// DbBackend impl (v4.4.0) — wraps the resurrected Cozo machinery
// ---------------------------------------------------------------------------

/// SQLite-backed [`DbBackend`]: one `cozo::DbInstance` per project rooted at
/// `<project>/.leankg/leankg.db`.
pub struct SqliteBackend {
    db: CozoDb,
    path: PathBuf,
    read_only: bool,
}

impl SqliteBackend {
    pub fn open(db_path: &Path, read_only: bool) -> Result<Self, Box<dyn std::error::Error>> {
        let storage = resolve_storage_config(db_path);
        if let Some(parent) = storage.path.parent() {
            std::fs::create_dir_all(parent)?;
        }
        let db = if read_only {
            init_db_readonly(db_path)?
        } else {
            init_db(db_path)?
        };
        let backend = Self {
            db,
            path: storage.path,
            read_only,
        };
        // Embedding-state tables (only when the `embeddings` feature is
        // compiled in) need a `DbBackend` handle, so they are ensured here —
        // after construction, on the writable path only. Read-only opens
        // never write schema.
        #[cfg(feature = "embeddings")]
        if !read_only {
            crate::embeddings::state::ensure_embedding_state_table(&backend)?;
        }
        if !read_only {
            ensure_audit_log_table(&backend.db)?;
        }
        Ok(backend)
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    fn next_audit_id(&self, offset: i64) -> Result<i64, Box<dyn std::error::Error>> {
        let script = "?[max(id)] := *audit_log{id}";
        let rows = run_script(&self.db, script, Default::default())?;
        let max_id = rows
            .rows
            .first()
            .and_then(|r| r.first())
            .and_then(|v| match v {
                crate::db::value::DataValue::Num(crate::db::value::Num::Int(i)) => Some(*i),
                _ => None,
            })
            .unwrap_or(0);
        Ok(max_id + offset + 1)
    }
}

/// Map a Cozo `code_elements` row to a `CodeElement`. `with_env` selects the
/// trailing `env` column (parity with PG's `code_element_from_row_env`).
fn cozo_row_to_code_element(
    row: &[crate::db::value::DataValue],
    with_env: bool,
) -> crate::db::models::CodeElement {
    let g = |i: usize| {
        row.get(i)
            .and_then(|v| match v {
                crate::db::value::DataValue::Str(s) => Some(s.to_string()),
                _ => None,
            })
            .unwrap_or_default()
    };
    let n = |i: usize| {
        row.get(i)
            .and_then(|v| match v {
                crate::db::value::DataValue::Num(crate::db::value::Num::Int(x)) => Some(*x as u32),
                _ => None,
            })
            .unwrap_or(0)
    };
    let env_last = if with_env {
        g(row.len().saturating_sub(1))
    } else {
        String::new()
    };
    crate::db::models::CodeElement {
        qualified_name: g(0),
        element_type: g(1),
        name: g(2),
        file_path: g(3),
        line_start: n(4),
        line_end: n(5),
        language: g(6),
        parent_qualified: (!g(7).is_empty()).then(|| g(7)),
        cluster_id: (!g(8).is_empty()).then(|| g(8)),
        cluster_label: (!g(9).is_empty()).then(|| g(9)),
        metadata: serde_json::from_str(&g(10)).unwrap_or(serde_json::json!({})),
        env: env_last,
    }
}

impl DbBackend for SqliteBackend {
    fn run_script(
        &self,
        query: &str,
        params: BTreeMap<String, serde_json::Value>,
    ) -> Result<NamedRows, Box<dyn std::error::Error>> {
        if self.read_only && mutability_for(query) != ScriptMutability::Immutable {
            return Err(crate::errors::render(
                crate::errors::READ_ONLY.code,
                "sqlite backend is read-only",
                "restart the server without --read-only, or point writes at a writable instance",
            )
            .into());
        }
        Ok(run_script(&self.db, query, params)?)
    }

    fn redacted_url(&self) -> String {
        format!("sqlite://{}", self.path.display())
    }
    fn mutability_for(&self, query: &str) -> crate::db::pg::mutability::ScriptMutability {
        use crate::db::pg::mutability::ScriptMutability as SM;
        match mutability_for(query) {
            cozo::ScriptMutability::Mutable => SM::Mutable,
            _ => SM::Immutable,
        }
    }
    fn is_read_only(&self) -> bool {
        self.read_only
    }

    /// Bulk-load named rows into a Cozo relation via `:insert`.
    fn import_relations(
        &self,
        data: BTreeMap<String, NamedRows>,
    ) -> Result<(), Box<dyn std::error::Error>> {
        for (relation, rows) in &data {
            if rows.rows.is_empty() {
                continue;
            }
            let cols = rows.headers.join(", ");
            // Column-type-aware value rendering. A CozoScript array literal
            // is a LIST, but `<F32; N>` vector columns need a `vec([...])`
            // expression — plain arrays fail at execution ("when executing
            // against relation ..."). Resolve column types once per
            // relation, then wrap List values headed for vector columns.
            let col_type_by_name: std::collections::BTreeMap<String, String> = match run_script(
                &self.db,
                &format!("::columns {relation}"),
                Default::default(),
            ) {
                Ok(schema) => schema
                    .rows
                    .iter()
                    .filter_map(|r| match (r.first(), r.get(3)) {
                        (
                            Some(crate::db::value::DataValue::Str(n)),
                            Some(crate::db::value::DataValue::Str(t)),
                        ) => Some((n.clone(), t.clone())),
                        _ => None,
                    })
                    .collect(),
                Err(_) => Default::default(),
            };
            let mut script = format!("?[{cols}] <- [");
            for (i, row) in rows.rows.iter().enumerate() {
                if i > 0 {
                    script.push_str(", ");
                }
                let vals: Vec<String> = row
                    .iter()
                    .enumerate()
                    .map(|(i, v)| {
                        let is_vector_column = rows
                            .headers
                            .get(i)
                            .and_then(|h| col_type_by_name.get(h))
                            .is_some_and(|t| t.starts_with("<F32"));
                        match (is_vector_column, v) {
                            (true, crate::db::value::DataValue::List(items)) => {
                                let inner = items
                                    .iter()
                                    .map(datavalue_literal_cozo)
                                    .collect::<Vec<_>>()
                                    .join(", ");
                                format!("vec([{inner}])")
                            }
                            _ => datavalue_literal_cozo(v),
                        }
                    })
                    .collect();
                script.push_str(&format!("[{}]", vals.join(", ")));
            }
            // Upsert semantics (:put) matching the PG lowering
            // (INSERT .. ON CONFLICT): re-embeds and index refreshes replay
            // existing keys, and Cozo :insert rejects duplicates.
            let put_cols = rows.headers.join(", ");
            script.push_str(format!("] :put {relation} {{{put_cols}}}").as_str());
            run_script(&self.db, &script, Default::default())?;
        }
        Ok(())
    }

    // ---- FR-ENT-1 audit ledger on sqlite (#309) ----
    // DDL mirrors PG migration 006; ts stored as unix-nanos INTEGER. The
    // hash chain (prev_hash -> entry_hash) is identical to the PG path.
    fn insert_audit_batch(
        &self,
        entries: &[crate::audit::AuditEntry],
    ) -> Result<(), Box<dyn std::error::Error>> {
        // The recorder stamps id=0 placeholders (PG BIGSERIAL assigns real
        // ids on insert); sqlite must assign them itself — otherwise every
        // row shares key 0 and :put collapses the ledger to one row.
        // Ids continue from the current chain head. Strings use cozo
        // single-quoted literals with backslash-escaped quotes (the ''
        // doubling form is invalid in cozo 0.7.6 — same grammar trap as
        // datavalue_literal_cozo); backslashes are doubled first.
        let esc = |v: &str| format!("'{}'", v.replace('\\', "\\\\").replace('\'', "\\'"));
        for (offset, e) in entries.iter().enumerate() {
            let id = self.next_audit_id(offset as i64)?;
            let script = format!(
                r#"?[id, ts, actor, agent_client, tool, project, args_hash, result_status, prev_hash, entry_hash] <-
                [[{}, {}, {}, {}, {}, {}, {}, {}, {}, {}]] :put audit_log {{id, ts, actor, agent_client, tool, project, args_hash, result_status, prev_hash, entry_hash}}"#,
                id,
                e.ts.duration_since(std::time::UNIX_EPOCH)
                    .map(|d| d.as_nanos() as i64)
                    .unwrap_or(0),
                esc(&e.actor),
                esc(&e.agent_client),
                esc(&e.tool),
                esc(e.project.as_deref().unwrap_or("")),
                esc(&e.args_hash),
                esc(&e.result_status),
                esc(&e.prev_hash),
                esc(&e.entry_hash),
            );
            run_script(&self.db, &script, Default::default())?;
        }
        Ok(())
    }

    fn last_audit_entry_hash(&self) -> Result<Option<String>, Box<dyn std::error::Error>> {
        // cozo: latest id via sort-desc + limit (id must be in the head
        // for :sort to see it; the caller only reads entry_hash).
        let script = r#"?[id, entry_hash] := *audit_log{id, entry_hash}
            :sort -id
            :limit 1"#;
        let rows = run_script(&self.db, script, Default::default())?;
        Ok(rows
            .rows
            .first()
            .and_then(|r| r.get(1).and_then(|v| v.get_str().map(String::from))))
    }

    /// FR-ENT-1: windowed ledger read for `leankg audit export|verify` on
    /// sqlite (#309 follow-up). ts is stored as unix-nanos INTEGER; since/
    /// until filters compare the same way.
    fn query_audit(
        &self,
        since: Option<std::time::SystemTime>,
        until: Option<std::time::SystemTime>,
    ) -> Result<Vec<crate::audit::AuditEntry>, Box<dyn std::error::Error>> {
        let mut clauses = Vec::new();
        if let Some(t) = since {
            let nanos = t
                .duration_since(std::time::UNIX_EPOCH)
                .map(|d| d.as_nanos() as i64)
                .unwrap_or(0);
            clauses.push(format!("ts >= {}", nanos));
        }
        if let Some(t) = until {
            let nanos = t
                .duration_since(std::time::UNIX_EPOCH)
                .map(|d| d.as_nanos() as i64)
                .unwrap_or(i64::MAX);
            clauses.push(format!("ts <= {}", nanos));
        }
        let filter = if clauses.is_empty() {
            String::new()
        } else {
            format!(", {}", clauses.join(", "))
        };
        let script = format!(
            r#"?[id, ts, actor, agent_client, tool, project, args_hash, result_status, prev_hash, entry_hash] :=
               *audit_log{{id, ts, actor, agent_client, tool, project, args_hash, result_status, prev_hash, entry_hash}}{}
            :sort id"#,
            filter
        );
        let rows = run_script(&self.db, &script, Default::default())?;
        Ok(rows
            .rows
            .iter()
            .filter_map(|row| {
                let id = row.first().and_then(|v| match v {
                    crate::db::value::DataValue::Num(crate::db::value::Num::Int(i)) => Some(*i),
                    _ => None,
                })?;
                let g = |i: usize| {
                    row.get(i)
                        .and_then(|v| match v {
                            crate::db::value::DataValue::Str(s) => Some(s.to_string()),
                            _ => None,
                        })
                        .unwrap_or_default()
                };
                let ts_nanos = row.get(1).and_then(|v| match v {
                    crate::db::value::DataValue::Num(crate::db::value::Num::Int(i)) => Some(*i),
                    _ => None,
                })?;
                Some(crate::audit::AuditEntry {
                    id,
                    ts: std::time::UNIX_EPOCH
                        + std::time::Duration::from_nanos(ts_nanos.max(0) as u64),
                    actor: g(2),
                    agent_client: g(3),
                    tool: g(4),
                    project: (!g(5).is_empty()).then(|| g(5)),
                    args_hash: g(6),
                    result_status: g(7),
                    prev_hash: g(8),
                    entry_hash: g(9),
                })
            })
            .collect())
    }

    // Knowledge CRUD on Cozo relations — same Datalog the pre-v0.20 codebase
    // ran natively; SQLite storage just persists it to the .db file.    // Knowledge CRUD on Cozo relations — same Datalog the pre-v0.20 codebase
    // ran natively; SQLite storage just persists it to the .db file.
    fn upsert_knowledge_entry(
        &self,
        entry: &crate::db::models::KnowledgeEntry,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let tags = entry.tags.replace('"', "\\\"");
        let script = format!(
            r#"?[id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at] <-
                [["{}", "{}", "{}", "{}", "{}", "{}", "{}", "{}", "{}", "{}", "{}", {}, {}]] :put knowledge_entries {{id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at}}"#,
            entry.id,
            entry.knowledge_type,
            entry.title.replace('"', "\\\""),
            entry.content.replace('"', "\\\""),
            entry.element_qualified.as_deref().unwrap_or(""),
            entry.user_story_id.as_deref().unwrap_or(""),
            entry.feature_id.as_deref().unwrap_or(""),
            tags,
            entry.environment,
            entry.branch.as_deref().unwrap_or(""),
            entry.author.replace('"', "\\\""),
            entry.created_at,
            entry.updated_at
        );
        run_script(&self.db, &script, Default::default())?;
        Ok(())
    }

    fn find_knowledge_entry(
        &self,
        id: &str,
    ) -> Result<Option<crate::db::models::KnowledgeEntry>, Box<dyn std::error::Error>> {
        let script = format!(
            r#"?[id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at] :=
                *knowledge_entries{{id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at}},
                id = "{}""#,
            id
        );
        let rows = run_script(&self.db, &script, Default::default())?;
        if rows.rows.is_empty() {
            return Ok(None);
        }
        let r = &rows.rows[0];
        let g = |i: usize| {
            r.get(i)
                .and_then(|v| match v {
                    crate::db::value::DataValue::Str(s) => Some(s.to_string()),
                    _ => None,
                })
                .unwrap_or_default()
        };
        let n = |i: usize| {
            r.get(i)
                .and_then(|v| match v {
                    crate::db::value::DataValue::Num(crate::db::value::Num::Int(x)) => Some(*x),
                    _ => None,
                })
                .unwrap_or(0)
        };
        Ok(Some(crate::db::models::KnowledgeEntry {
            id: g(0),
            knowledge_type: g(1),
            title: g(2),
            content: g(3),
            element_qualified: (!g(4).is_empty()).then(|| g(4)),
            user_story_id: (!g(5).is_empty()).then(|| g(5)),
            feature_id: (!g(6).is_empty()).then(|| g(6)),
            tags: g(7),
            environment: g(8),
            branch: (!g(9).is_empty()).then(|| g(9)),
            author: g(10),
            created_at: n(11),
            updated_at: n(12),
        }))
    }

    fn delete_knowledge_entry_by_id(&self, id: &str) -> Result<bool, Box<dyn std::error::Error>> {
        let script = format!(r#":rm knowledge_entries {{"{id}"}}"#);
        run_script(&self.db, &script, Default::default())?;
        Ok(true)
    }

    // FR-ZCP-05 on SQLite: CozoDB's `regex_matches` provides the same
    // name-recall the PG trgm/ILIKE path gives; ranking by name is a
    // deliberate simplification (trigram ranking is PG-only for now).
    fn fuzzy_find_elements(
        &self,
        query: &str,
        limit: usize,
    ) -> Result<Vec<crate::db::models::CodeElement>, Box<dyn std::error::Error>> {
        // Lowercase BOTH sides: regex_matches is case-sensitive, so the
        // query must be folded too or "ALPH" never matches "alpha" (the
        // PG path is case-insensitive via ILIKE — this keeps parity).
        let q = query.trim().to_lowercase();
        if q.is_empty() {
            return Ok(Vec::new());
        }
        // Substring semantics: escape regex metacharacters so real symbol
        // names (Vec<String>, a::b, fn(...)) can't break or over-match the
        // pattern — the whole L2 tier errors on an invalid regex today.
        let q = regex::escape(&q);
        let limit = limit.clamp(1, 100);
        // Fetch a wider candidate pool, then rank in Rust: (a) non-test
        // symbols outrank test symbols (#293 — test fns named after the
        // handler outranked the real one), (b) exact-name matches outrank
        // substring matches, (c) shorter names outrank longer ones
        // (specificity proxy — #294).
        let pool = limit.saturating_mul(4);
        let script = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] :=
               *code_elements{{qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata}},
               regex_matches(lowercase(name), "{}")
             :limit {}"#,
            q.replace('"', "\\\""),
            pool
        );
        let rows = run_script(&self.db, &script, Default::default())?;
        let q_folded = query.trim().to_lowercase();
        let mut ranked: Vec<(crate::db::models::CodeElement, bool, bool, usize)> = rows
            .rows
            .iter()
            .map(|row| {
                let g = |i: usize| {
                    row.get(i)
                        .and_then(|v| match v {
                            crate::db::value::DataValue::Str(s) => Some(s.to_string()),
                            _ => None,
                        })
                        .unwrap_or_default()
                };
                let n = |i: usize| {
                    row.get(i)
                        .and_then(|v| match v {
                            crate::db::value::DataValue::Num(crate::db::value::Num::Int(x)) => {
                                Some(*x as u32)
                            }
                            _ => None,
                        })
                        .unwrap_or(0)
                };
                let el = crate::db::models::CodeElement {
                    qualified_name: g(0),
                    element_type: g(1),
                    name: g(2),
                    file_path: g(3),
                    line_start: n(4),
                    line_end: n(5),
                    language: g(6),
                    parent_qualified: (!g(7).is_empty()).then(|| g(7)),
                    cluster_id: (!g(8).is_empty()).then(|| g(8)),
                    cluster_label: (!g(9).is_empty()).then(|| g(9)),
                    metadata: serde_json::from_str(&g(10)).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                };
                let name_l = el.name.to_lowercase();
                let last_seg = el.qualified_name.rsplit("::").next().unwrap_or("");
                let is_test = last_seg.starts_with("test_")
                    || last_seg.ends_with("_test")
                    || last_seg == "test"
                    || el.qualified_name.contains("::tests::")
                    || el.file_path.contains("/tests/");
                let exact = name_l == q_folded;
                let specificity = name_l.len();
                (el, is_test, exact, specificity)
            })
            .collect();
        ranked.sort_by(|a, b| {
            a.1.cmp(&b.1)
                .then(b.2.cmp(&a.2))
                .then(a.3.cmp(&b.3))
                .then(a.0.qualified_name.cmp(&b.0.qualified_name))
        });
        Ok(ranked
            .into_iter()
            .take(limit)
            .map(|(el, _, _, _)| el)
            .collect())
    }

    fn suggest_element_names(
        &self,
        query: &str,
        limit: usize,
    ) -> Result<Vec<String>, Box<dyn std::error::Error>> {
        let q = query.trim();
        if q.is_empty() {
            return Ok(Vec::new());
        }
        let limit = limit.clamp(1, 100);
        let script = format!(
            r#"?[name] := *code_elements{{name}},
               regex_matches(lowercase(name), "{}")
             :limit {}"#,
            q.replace(['"', '\\'], ""),
            limit
        );
        let rows = run_script(&self.db, &script, Default::default())?;
        Ok(rows
            .rows
            .iter()
            .filter_map(|r| {
                r.first().and_then(|v| match v {
                    crate::db::value::DataValue::Str(s) => Some(s.to_string()),
                    _ => None,
                })
            })
            .collect())
    }

    /// Keyed lookup by `qualified_name` (Datalog port of the W8 wave-2
    /// SQL-first read) — keeps the L3 ANN hydration path working on SQLite.
    fn find_element_by_key(
        &self,
        qualified_name: &str,
    ) -> Result<Option<crate::db::models::CodeElement>, Box<dyn std::error::Error>> {
        let script = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] :=
               *code_elements{{qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata}},
               qualified_name = "{}""#,
            qualified_name.replace(['"', '\\'], "")
        );
        let rows = run_script(&self.db, &script, Default::default())?;
        Ok(rows
            .rows
            .first()
            .map(|r| cozo_row_to_code_element(r, false)))
    }

    /// First row whose `name` matches exactly (legacy 11-col projection).
    fn find_element_by_name_col(
        &self,
        name: &str,
    ) -> Result<Option<crate::db::models::CodeElement>, Box<dyn std::error::Error>> {
        let script = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] :=
               *code_elements{{qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata}},
               name = "{}"
             :limit 1"#,
            name.replace(['"', '\\'], "")
        );
        let rows = run_script(&self.db, &script, Default::default())?;
        Ok(rows
            .rows
            .first()
            .map(|r| cozo_row_to_code_element(r, false)))
    }

    /// Keyed hydration for a set of ANN hits (FR-SEM-07). `env` IS selected.
    fn elements_by_qualified_names(
        &self,
        qualified_names: &[String],
    ) -> Result<Vec<crate::db::models::CodeElement>, Box<dyn std::error::Error>> {
        // CozoScript string escaping must match the datavalue path: use
        // single-quoted literals with \' (double-quoted strings do not
        // support \" escapes in cozo 0.7.6 — see datavalue_literal_cozo).
        let esc = |s: &str| {
            let inner = s.replace('\\', "\\\\").replace('\'', "\\'");
            format!("'{}'", inner)
        };
        let mut out = Vec::with_capacity(qualified_names.len());
        for chunk in qualified_names.chunks(500) {
            let parts: Vec<String> = chunk
                .iter()
                .map(|qn| format!("[{}, {}]", esc(qn), esc(qn)))
                .collect();
            let script = format!(
                r#"want[qn, qn2] <- [{}]
                   ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] :=
                      want[qn, qn2],
                      *code_elements{{qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env}},
                      qualified_name = qn"#,
                parts.join(", ")
            );
            let rows = run_script(&self.db, &script, Default::default()).map_err(|e| {
                let dump = std::env::temp_dir().join("leankg-failing-script.txt");
                let _ = std::fs::write(&dump, &script);
                tracing::error!(
                    "elements_by_qualified_names script failed: {e}; dumped to {}",
                    dump.display()
                );
                e
            })?;
            out.extend(rows.rows.iter().map(|r| cozo_row_to_code_element(r, true)));
        }
        Ok(out)
    }

    fn search_knowledge_entries(
        &self,
        query: &str,
        knowledge_type: Option<&str>,
        environment: Option<&str>,
        limit: usize,
    ) -> Result<Vec<crate::db::models::KnowledgeEntry>, Box<dyn std::error::Error>> {
        let q = query.trim().replace('"', "");
        let lim = limit.clamp(1, 100);
        let mut script = format!(
            r#"?[id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at] :=
                *knowledge_entries{{id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at}},
                regex_matches(lowercase(title), "{}")"#,
            q.to_lowercase().replace('"', "")
        );
        if let Some(kt) = knowledge_type {
            script.push_str(&format!(", knowledge_type = \"{kt}\""));
        }
        if let Some(env) = environment {
            script.push_str(&format!(", environment = \"{env}\""));
        }
        script.push_str(&format!(" :limit {lim}"));
        let rows = run_script(&self.db, &script, Default::default())?;
        Ok(rows
            .rows
            .iter()
            .map(|r| rows_to_knowledge_entry(r))
            .collect())
    }

    fn list_knowledge_by_element(
        &self,
        element_qualified: &str,
    ) -> Result<Vec<crate::db::models::KnowledgeEntry>, Box<dyn std::error::Error>> {
        let script = format!(
            r#"?[id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at] :=
                *knowledge_entries{{id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at}},
                element_qualified = "{element_qualified}""#
        );
        let rows = run_script(&self.db, &script, Default::default())?;
        Ok(rows
            .rows
            .iter()
            .map(|r| rows_to_knowledge_entry(r))
            .collect())
    }
}

impl SqliteBackend {
    /// Standalone migration entry for the CLI (`leankg migrate`).
    pub fn run_migrations_standalone(&self) -> Result<String, Box<dyn std::error::Error>> {
        let existing: std::collections::HashSet<String> = {
            let res = run_script(&self.db, "::relations", Default::default())?;
            res.rows
                .iter()
                .filter_map(|r| {
                    r.first().and_then(|v| match v {
                        crate::db::value::DataValue::Str(s) => Some(s.to_string()),
                        _ => None,
                    })
                })
                .collect()
        };
        run_migrations(&self.db, &existing)?;
        Ok(format!(
            "migrations applied (sqlite backend at {})",
            self.path.display()
        ))
    }
}

impl SqliteBackend {}

/// Convert NamedRows rows (13 cols) into KnowledgeEntry.
fn rows_to_knowledge_entry(
    row: &[crate::db::value::DataValue],
) -> crate::db::models::KnowledgeEntry {
    let g = |i: usize| {
        row.get(i)
            .and_then(|v| match v {
                crate::db::value::DataValue::Str(s) => Some(s.to_string()),
                _ => None,
            })
            .unwrap_or_default()
    };
    let n = |i: usize| {
        row.get(i)
            .and_then(|v| match v {
                crate::db::value::DataValue::Num(crate::db::value::Num::Int(x)) => Some(*x),
                _ => None,
            })
            .unwrap_or(0)
    };
    crate::db::models::KnowledgeEntry {
        id: g(0),
        knowledge_type: g(1),
        title: g(2),
        content: g(3),
        element_qualified: (!g(4).is_empty()).then(|| g(4)),
        user_story_id: (!g(5).is_empty()).then(|| g(5)),
        feature_id: (!g(6).is_empty()).then(|| g(6)),
        tags: g(7),
        environment: g(8),
        branch: (!g(9).is_empty()).then(|| g(9)),
        author: g(10),
        created_at: n(11),
        updated_at: n(12),
    }
}

/// Convert a CozoDB row value into the backend-neutral `value::DataValue`.
fn datavalue_from_cozo(v: &cozo::DataValue) -> crate::db::value::DataValue {
    use crate::db::value::DataValue as V;
    match v {
        cozo::DataValue::Null => V::Null,
        cozo::DataValue::Bool(b) => V::Bool(*b),
        cozo::DataValue::Num(n) => {
            let txt = n.to_string();
            match txt.parse::<i64>() {
                Ok(i) => V::Num(crate::db::value::Num::Int(i)),
                Err(_) => match txt.parse::<f64>() {
                    Ok(f) => V::Num(crate::db::value::Num::Float(f)),
                    Err(_) => V::Json(txt),
                },
            }
        }
        cozo::DataValue::Str(s) => V::Str(s.to_string()),
        cozo::DataValue::Bytes(b) => V::Bytes(b.clone()),
        cozo::DataValue::List(items) => {
            let converted: Vec<crate::db::value::DataValue> =
                items.iter().map(datavalue_from_cozo).collect();
            V::List(converted)
        }
        other => V::Json(other.to_string()),
    }
}

/// Render a `value::DataValue` as an inline Datalog literal (for :insert).
fn datavalue_literal_cozo(v: &crate::db::value::DataValue) -> String {
    use crate::db::value::DataValue as V;
    match v {
        V::Null => "null".to_string(),
        V::Bool(b) => b.to_string(),
        V::Num(crate::db::value::Num::Int(i)) => i.to_string(),
        V::Num(crate::db::value::Num::Float(f)) => f.to_string(),
        // Cozo 0.7.6's parser chokes on `\"` inside double-quoted literals
        // (verified against the pest grammar's own escape production), so
        // ANY string containing `"` — JSON metadata above all — produced an
        // unparseable script through this path. Single-quoted literals with
        // `\'` escaping parse cleanly and are what we emit instead.
        V::Str(s) => format!(
            "'{}'",
            s.replace('\\', "\\\\")
                .replace('\'', "\\'")
                .replace('\n', "\\n")
        ),
        V::Bytes(b) => format!("<bytes:{}>", b.len()),
        V::List(items) => format!(
            "[{}]",
            items
                .iter()
                .map(datavalue_literal_cozo)
                .collect::<Vec<_>>()
                .join(", ")
        ),
        V::Json(s) => format!("\"{}\"", s.replace('"', "\\\"")),
        V::Bot => "null".to_string(),
    }
}

/// Open as a trait object for `SharedDb` seams.
pub fn open_shared(
    db_path: &Path,
    read_only: bool,
) -> Result<std::sync::Arc<dyn DbBackend>, Box<dyn std::error::Error>> {
    Ok(std::sync::Arc::new(SqliteBackend::open(
        db_path, read_only,
    )?))
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    fn make_db(path: &std::path::Path) -> CozoDb {
        let s = path.to_string_lossy().to_string();
        cozo::DbInstance::new("sqlite", &s, "").unwrap()
    }

    #[test]
    fn code_elements_schema_on_canonical_db() {
        let tmp = TempDir::new().unwrap();
        let db_path = tmp.path().join("ce.db");
        let db = make_db(&db_path);
        run_script(&db,
            r#":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}"#,
            Default::default(),
        )
        .unwrap();

        let schema = code_elements_schema(&db);
        assert_eq!(schema.name, "code_elements");
        assert_eq!(schema.arity, 13);
        assert!(schema.canonical, "fresh 13-col DB must report canonical");
        assert_eq!(
            schema.columns,
            CODE_ELEMENTS_13_COLUMNS
                .iter()
                .map(|s| s.to_string())
                .collect::<Vec<_>>()
        );
        assert_eq!(
            schema.columns.last().map(String::as_str),
            Some("ontology_layer")
        );
    }

    #[test]
    fn relationships_schema_on_canonical_db() {
        let tmp = TempDir::new().unwrap();
        let db_path = tmp.path().join("rel.db");
        let db = make_db(&db_path);
        run_script(&db,
            r#":create relationships {source_qualified: String, target_qualified: String, rel_type: String, confidence: Float, metadata: String, env: String default 'local'}"#,
            Default::default(),
        )
        .unwrap();

        let schema = relationships_schema(&db);
        assert_eq!(schema.name, "relationships");
        assert_eq!(schema.arity, 6);
        assert!(schema.canonical);
        assert_eq!(
            schema.columns,
            RELATIONSHIPS_6_COLUMNS
                .iter()
                .map(|s| s.to_string())
                .collect::<Vec<_>>()
        );
    }

    #[test]
    fn code_elements_schema_reports_legacy_11_columns_as_non_canonical() {
        let tmp = TempDir::new().unwrap();
        let db_path = tmp.path().join("legacy.db");
        let db = make_db(&db_path);
        run_script(&db,
            r#":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String}"#,
            Default::default(),
        )
        .unwrap();

        let schema = code_elements_schema(&db);
        assert_eq!(schema.arity, 11);
        assert!(!schema.canonical, "11-col schema must not be canonical");
        assert_eq!(
            schema.columns,
            CODE_ELEMENTS_11_COLUMNS
                .iter()
                .map(|s| s.to_string())
                .collect::<Vec<_>>()
        );
        assert!(!schema.columns.contains(&"env".to_string()));
        assert!(!schema.columns.contains(&"ontology_layer".to_string()));
    }

    #[test]
    fn relationships_schema_reports_legacy_5_columns_as_non_canonical() {
        let tmp = TempDir::new().unwrap();
        let db_path = tmp.path().join("legacy-rel.db");
        let db = make_db(&db_path);
        run_script(&db,
            r#":create relationships {source_qualified: String, target_qualified: String, rel_type: String, confidence: Float, metadata: String}"#,
            Default::default(),
        )
        .unwrap();

        let schema = relationships_schema(&db);
        assert_eq!(schema.arity, 5);
        assert!(!schema.canonical);
        assert_eq!(
            schema.columns,
            RELATIONSHIPS_5_COLUMNS
                .iter()
                .map(|s| s.to_string())
                .collect::<Vec<_>>()
        );
        assert!(!schema.columns.contains(&"env".to_string()));
    }

    #[test]
    fn get_relation_schema_unknown_relation_returns_zero_columns() {
        let tmp = TempDir::new().unwrap();
        let db_path = tmp.path().join("unknown.db");
        let db = make_db(&db_path);
        let schema = get_relation_schema(&db, "no_such_relation");
        assert_eq!(schema.name, "no_such_relation");
        assert_eq!(schema.arity, 0);
        assert!(schema.columns.is_empty());
        assert!(!schema.canonical);
    }

    // CozoDB 0.7.x migration wrapper tests — verify the `run_script` adapter,
    // `mutability_for` auto-detection, `json_to_datavalue` conversion, and
    // storage config resolution introduced when upgrading from vendored
    // cozo-0.2.2 to cozo-0.7.6.

    static ENV_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

    #[test]
    fn mutability_for_read_query_is_immutable() {
        assert_eq!(
            mutability_for("?[a, b] := *code_elements[a, b]"),
            ScriptMutability::Immutable
        );
        assert_eq!(mutability_for("::relations"), ScriptMutability::Immutable);
    }

    #[test]
    fn mutability_for_put_query_is_mutable() {
        assert_eq!(
            mutability_for("?[a, b] <- [[$a, $b]] :put code_elements {a, b}"),
            ScriptMutability::Mutable
        );
    }

    #[test]
    fn mutability_for_rm_query_is_mutable() {
        assert_eq!(
            mutability_for("?[a] <- [[$a]] :rm code_elements {a}"),
            ScriptMutability::Mutable
        );
    }

    #[test]
    fn mutability_for_create_query_is_mutable() {
        assert_eq!(
            mutability_for(":create code_elements {a: String, b: String}"),
            ScriptMutability::Mutable
        );
    }

    #[test]
    fn mutability_for_hnsw_query_is_mutable() {
        assert_eq!(
            mutability_for("::hnsw create embedding_vectors:vec_idx { dim: 384 }"),
            ScriptMutability::Mutable
        );
    }

    #[test]
    fn mutability_for_index_create_is_mutable() {
        assert_eq!(
            mutability_for("::index create code_elements:name_idx { name }"),
            ScriptMutability::Mutable
        );
    }

    #[test]
    fn mutability_for_combined_read_head_with_put_is_mutable() {
        // Read head (?) + write action (:put) → Mutable (scans whole query).
        assert_eq!(
            mutability_for("?[a, b] := *rel[a, b], b = $val :put rel2 {a, b}"),
            ScriptMutability::Mutable
        );
    }

    #[test]
    fn json_to_datavalue_null_becomes_datavalue_null() {
        let tmp = TempDir::new().unwrap();
        let db_path = tmp.path().join("null.db");
        let db = make_db(&db_path);
        run_script(&db, ":create t {k: String, v: String?}", Default::default()).unwrap();
        let mut params = std::collections::BTreeMap::new();
        params.insert("k".to_string(), serde_json::json!("key1"));
        params.insert("v".to_string(), serde_json::Value::Null);
        run_script(&db, "?[k, v] <- [[$k, $v]] :put t {k, v}", params).unwrap();
        let result = run_script(&db, "?[k, v] := *t[k, v]", Default::default()).unwrap();
        assert_eq!(result.rows.len(), 1);
    }

    #[test]
    fn json_to_datavalue_string_preserves_value() {
        let dv = json_to_datavalue(serde_json::json!("hello"));
        assert_eq!(dv.get_str(), Some("hello"));
    }

    #[test]
    fn json_to_datavalue_int_preserves_value() {
        let dv = json_to_datavalue(serde_json::json!(42));
        assert_eq!(dv.get_int(), Some(42));
    }

    #[test]
    fn run_script_put_then_get_roundtrip() {
        let tmp = TempDir::new().unwrap();
        let db_path = tmp.path().join("rt.db");
        let db = make_db(&db_path);
        run_script(
            &db,
            ":create kv {k: String => v: String}",
            Default::default(),
        )
        .unwrap();
        let mut params = std::collections::BTreeMap::new();
        params.insert("k".to_string(), serde_json::json!("alpha"));
        params.insert("v".to_string(), serde_json::json!("beta"));
        run_script(&db, "?[k, v] <- [[$k, $v]] :put kv {k => v}", params).unwrap();
        let mut qparams = std::collections::BTreeMap::new();
        qparams.insert("k".to_string(), serde_json::json!("alpha"));
        let result = run_script(&db, "?[v] := *kv[k, v], k = $k", qparams).unwrap();
        assert_eq!(result.rows.len(), 1);
        assert_eq!(result.rows[0][0].get_str(), Some("beta"));
    }

    #[test]
    fn run_script_immutable_read_after_write() {
        let tmp = TempDir::new().unwrap();
        let db_path = tmp.path().join("imm.db");
        let db = make_db(&db_path);
        run_script(
            &db,
            ":create simple {a: String, b: Int}",
            Default::default(),
        )
        .unwrap();
        let mut params = std::collections::BTreeMap::new();
        params.insert("a".to_string(), serde_json::json!("x"));
        params.insert("b".to_string(), serde_json::json!(10));
        run_script(&db, "?[a, b] <- [[$a, $b]] :put simple {a, b}", params).unwrap();
        let result = run_script(&db, "?[a, b] := *simple[a, b]", Default::default()).unwrap();
        assert_eq!(result.rows.len(), 1);
        assert_eq!(result.rows[0][0].get_str(), Some("x"));
        assert_eq!(result.rows[0][1].get_int(), Some(10));
    }

    #[test]
    fn resolve_storage_config_defaults_to_sqlite() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        let prev = std::env::var("LEANKG_DB_ENGINE").ok();
        std::env::remove_var("LEANKG_DB_ENGINE");
        let cfg = resolve_storage_config(std::path::Path::new("/tmp/test.db"));
        assert_eq!(cfg.engine, StorageEngine::Sqlite);
        if let Some(v) = prev {
            std::env::set_var("LEANKG_DB_ENGINE", v);
        }
    }

    #[test]
    fn resolve_storage_config_rocksdb_when_env_set() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        let prev = std::env::var("LEANKG_DB_ENGINE").ok();
        std::env::set_var("LEANKG_DB_ENGINE", "rocksdb");
        let cfg = resolve_storage_config(std::path::Path::new("/tmp/test.db"));
        assert_eq!(cfg.engine, StorageEngine::RocksDb);
        match prev {
            Some(v) => std::env::set_var("LEANKG_DB_ENGINE", v),
            None => std::env::remove_var("LEANKG_DB_ENGINE"),
        }
    }

    #[test]
    fn resolve_storage_config_sqlite_dir_appends_leankg_db() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        let prev = std::env::var("LEANKG_DB_ENGINE").ok();
        std::env::remove_var("LEANKG_DB_ENGINE");
        // resolve_storage_config checks is_dir() — create a real temp dir
        // so the SQLite path appends "leankg.db".
        let tmp = TempDir::new().unwrap();
        let cfg = resolve_storage_config(tmp.path());
        assert_eq!(cfg.engine, StorageEngine::Sqlite);
        assert!(cfg.path.ends_with("leankg.db"));
        if let Some(v) = prev {
            std::env::set_var("LEANKG_DB_ENGINE", v);
        }
    }

    #[test]
    fn get_env_mmap_size_default_is_64_mib() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        let prev = std::env::var("LEANKG_MMAP_SIZE").ok();
        std::env::remove_var("LEANKG_MMAP_SIZE");
        assert_eq!(get_env_mmap_size(), 64 * 1024 * 1024);
        if let Some(v) = prev {
            std::env::set_var("LEANKG_MMAP_SIZE", v);
        }
    }

    #[test]
    fn get_env_mmap_size_env_override() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        let prev = std::env::var("LEANKG_MMAP_SIZE").ok();
        std::env::set_var("LEANKG_MMAP_SIZE", "134217728");
        assert_eq!(get_env_mmap_size(), 134217728);
        match prev {
            Some(v) => std::env::set_var("LEANKG_MMAP_SIZE", v),
            None => std::env::remove_var("LEANKG_MMAP_SIZE"),
        }
    }

    #[test]
    fn central_project_storage_path_includes_project_name() {
        let path =
            central_project_storage_path(std::path::Path::new("/home/user/myproject/.leankg"));
        let path_str = path.to_string_lossy();
        assert!(path_str.contains("projects"), "path: {path_str}");
        assert!(path_str.contains("myproject"), "path: {path_str}");
    }

    /// FR-HNSW-C: the embed / semantic-context / smoke-test CLI commands pass
    /// `<project>/.leankg/leankg.db` to `init_db` (the file path), while
    /// `leankg index` and `leankg mcp-http` pass the `.leankg` directory.
    /// Both must resolve to the SAME RocksDB project root, otherwise
    /// `leankg embed` opens a fresh empty database and silently embeds
    /// nothing — leaving `semantic_search` stuck on the ontology-only path.
    #[test]
    fn central_project_storage_path_resolves_leankg_db_to_same_root_as_dot_leankg() {
        let dir_path =
            central_project_storage_path(std::path::Path::new("/home/user/myproject/.leankg"));
        let file_path = central_project_storage_path(std::path::Path::new(
            "/home/user/myproject/.leankg/leankg.db",
        ));
        assert_eq!(
            dir_path, file_path,
            "leankg.db file path must resolve to the same project root as .leankg directory"
        );
        let path_str = dir_path.to_string_lossy();
        assert!(path_str.contains("myproject"), "path: {path_str}");
    }

    /// Edge case: the database file is detached from `.leankg/` (e.g., a
    /// manual copy). Falls back to the parent directory as the project root
    /// rather than producing a hash of the file's actual location.
    #[test]
    fn central_project_storage_path_handles_detached_db_file() {
        let file_path = central_project_storage_path(std::path::Path::new("/tmp/loose-leankg.db"));
        let path_str = file_path.to_string_lossy();
        // Falls back to "loose-leankg" as the project name; this is the
        // graceful degradation path — caller will get a fresh DB at a
        // distinct path, and the embed step will report 0 vectors.
        assert!(
            path_str.contains("loose-leankg"),
            "expected detached db to use parent dir as project: {path_str}"
        );
    }

    #[test]
    fn init_db_creates_canonical_schema_on_sqlite() {
        let tmp = TempDir::new().unwrap();
        let db_path = tmp.path().join("init.db");
        let db = init_db(&db_path).expect("init_db");
        let schema = code_elements_schema(&db);
        assert_eq!(schema.name, "code_elements");
        assert_eq!(schema.arity, 13);
        assert!(schema.canonical);
        assert!(schema.columns.contains(&"env".to_string()));
        assert!(schema.columns.contains(&"ontology_layer".to_string()));
    }

    #[test]
    fn init_db_relationships_has_six_columns() {
        let tmp = TempDir::new().unwrap();
        let db_path = tmp.path().join("rel_init.db");
        let db = init_db(&db_path).expect("init_db");
        let schema = relationships_schema(&db);
        assert_eq!(schema.name, "relationships");
        assert_eq!(schema.arity, 6);
        assert!(schema.canonical);
        assert!(schema.columns.contains(&"env".to_string()));
    }

    /// Regression: `elements_by_qualified_names` built its data literal with
    /// `want[qn, qn2] <- [[…]]`, producing `[[[row]]]` — the Constant fixed
    /// rule read arity from the wrong nesting level and EVERY hydration
    /// call failed ("Fixed rule head arity mismatch"). First surfaced by
    /// the L3 ANN hydration path on SQLite. This bug regressed once through
    /// the #283 squash; without this test it will again.
    #[test]
    fn elements_by_qualified_names_roundtrip_with_env_and_metadata() {
        let tmp = TempDir::new().unwrap();
        let backend = SqliteBackend::open(&tmp.path().join(".leankg"), false).unwrap();
        let mut data = BTreeMap::new();
        data.insert(
            "code_elements".to_string(),
            NamedRows {
                headers: [
                    "qualified_name",
                    "element_type",
                    "name",
                    "file_path",
                    "line_start",
                    "line_end",
                    "language",
                    "parent_qualified",
                    "cluster_id",
                    "cluster_label",
                    "metadata",
                    "env",
                ]
                .iter()
                .map(|s| s.to_string())
                .collect(),
                rows: vec![
                    vec![
                        crate::db::value::DataValue::Str("/src/a.rs::alpha".into()),
                        crate::db::value::DataValue::Str("function".into()),
                        crate::db::value::DataValue::Str("alpha".into()),
                        crate::db::value::DataValue::Str("/src/a.rs".into()),
                        crate::db::value::DataValue::Num(crate::db::value::Num::Int(1)),
                        crate::db::value::DataValue::Num(crate::db::value::Num::Int(10)),
                        crate::db::value::DataValue::Str("rust".into()),
                        crate::db::value::DataValue::Str("".into()),
                        crate::db::value::DataValue::Str("".into()),
                        crate::db::value::DataValue::Str("".into()),
                        crate::db::value::DataValue::Str(r#"{"key":"v1"}"#.into()),
                        crate::db::value::DataValue::Str("local".into()),
                    ],
                    vec![
                        crate::db::value::DataValue::Str("/src/a.rs::beta".into()),
                        crate::db::value::DataValue::Str("function".into()),
                        crate::db::value::DataValue::Str("beta".into()),
                        crate::db::value::DataValue::Str("/src/a.rs".into()),
                        crate::db::value::DataValue::Num(crate::db::value::Num::Int(11)),
                        crate::db::value::DataValue::Num(crate::db::value::Num::Int(20)),
                        crate::db::value::DataValue::Str("rust".into()),
                        crate::db::value::DataValue::Str("".into()),
                        crate::db::value::DataValue::Str("".into()),
                        crate::db::value::DataValue::Str("".into()),
                        crate::db::value::DataValue::Str("{}".into()),
                        crate::db::value::DataValue::Str("ci".into()),
                    ],
                ],
                next: None,
            },
        );
        backend.import_relations(data).unwrap();

        // A missing qualified_name must be silently skipped, not error.
        let qns = vec![
            "/src/a.rs::alpha".to_string(),
            "/src/a.rs::beta".to_string(),
            "/src/missing.rs::ghost".to_string(),
        ];
        let got = backend.elements_by_qualified_names(&qns).unwrap();
        assert_eq!(got.len(), 2, "missing qn must not appear");
        let alpha = got
            .iter()
            .find(|e| e.qualified_name == "/src/a.rs::alpha")
            .unwrap();
        assert_eq!(alpha.element_type, "function");
        assert_eq!(alpha.metadata["key"], serde_json::json!("v1"));
        assert_eq!(alpha.env, "local");
        let beta = got
            .iter()
            .find(|e| e.qualified_name == "/src/a.rs::beta")
            .unwrap();
        assert_eq!(beta.env, "ci", "per-row env must survive the join");
    }

    /// Regression: full re-embeds replay existing qualified_names; Cozo
    /// :insert rejects duplicates ("when executing against relation ...")
    /// while the PG lowering is INSERT .. ON CONFLICT. import_relations
    /// must upsert (:put), so the second import overwrites the first —
    /// and List values must land as vec([...]) literals for <F32; N> cols.
    #[test]
    /// Regression (#309): the audit ledger works on sqlite — insert a
    /// batch, read the chain head back, and confirm the fields roundtrip.
    #[test]
    fn audit_ledger_roundtrip_on_sqlite() {
        use crate::audit::AuditEntry;
        let tmp = TempDir::new().unwrap();
        // open() (writable) already ensures the audit_log table.
        let backend = SqliteBackend::open(&tmp.path().join(".leankg"), false).unwrap();

        let mk = |prev: &str, eh: &str| AuditEntry {
            id: 0, // recorder placeholder — the backend assigns real ids
            ts: std::time::UNIX_EPOCH,
            actor: "local".into(),
            agent_client: "cursor".into(),
            tool: "search_code".into(),
            project: Some("/proj".into()),
            args_hash: "ah".into(),
            result_status: "ok".into(),
            prev_hash: prev.into(),
            entry_hash: eh.into(),
        };
        backend
            .insert_audit_batch(&[mk("genesis", "eh1"), mk("eh1", "eh2")])
            .unwrap();
        let head = backend.last_audit_entry_hash().unwrap();
        assert_eq!(
            head.as_deref(),
            Some("eh2"),
            "chain head must be the latest entry"
        );
        // Distinct rows — the id=0 placeholder collapse is the #312 blocker.
        let head2 = backend.last_audit_entry_hash().unwrap();
        assert_eq!(head2.as_deref(), Some("eh2"));
    }

    fn import_relations_upserts_duplicate_keys() {
        use crate::db::value::DataValue as DV;
        let tmp = TempDir::new().unwrap();
        let backend = SqliteBackend::open(&tmp.path().join(".leankg"), false).unwrap();
        backend
            .run_script(
                ":create embedding_vectors {qualified_name: String => vector: <F32; 384>}",
                Default::default(),
            )
            .unwrap();
        let v: Vec<f32> = (0..384).map(|i| i as f32 * 0.01).collect();
        let mk = |offset: f32| {
            let mut m = BTreeMap::new();
            m.insert(
                "embedding_vectors".to_string(),
                NamedRows::new(
                    vec!["qualified_name".to_string(), "vector".to_string()],
                    vec![vec![
                        DV::Str("q".into()),
                        DV::List(v.iter().map(|&f| DV::from((f + offset) as f64)).collect()),
                    ]],
                ),
            );
            m
        };
        backend.import_relations(mk(0.0)).unwrap();
        backend.import_relations(mk(1.0)).unwrap();
        let r = backend
            .run_script(
                r#"?[qualified_name, vector] := *embedding_vectors{qualified_name, vector}, qualified_name = "q""#,
                Default::default(),
            )
            .unwrap();
        assert_eq!(r.rows.len(), 1, "upsert must not duplicate the row");
        let stored = match r.rows[0].get(1) {
            Some(crate::db::value::DataValue::Json(s)) => s
                .trim_start_matches("vec([")
                .trim_end_matches("])")
                .to_string(),
            other => panic!("unexpected vector repr {other:?}"),
        };
        let second: f64 = stored.split(',').nth(1).unwrap().trim().parse().unwrap();
        assert!(
            (second - 1.01).abs() < 1e-6,
            "second import must overwrite the vector"
        );
    }

    /// Regression: `import_relations` wrapped rows in an extra bracket layer
    /// (`?[] <- [[[row]]]`), so every import failed with "Fixed rule head
    /// arity mismatch" — the embed vector writer was the first real caller.
    #[test]
    fn import_relations_roundtrips_rows() {
        let tmp = TempDir::new().unwrap();
        let backend = SqliteBackend::open(&tmp.path().join(".leankg"), false).unwrap();
        let mut data = BTreeMap::new();
        data.insert(
            "relationships".to_string(),
            NamedRows {
                headers: vec![
                    "source_qualified".into(),
                    "target_qualified".into(),
                    "rel_type".into(),
                    "confidence".into(),
                    "metadata".into(),
                    "env".into(),
                ],
                rows: vec![
                    vec![
                        crate::db::value::DataValue::Str("/src/a.rs::alpha".into()),
                        crate::db::value::DataValue::Str("/src/a.rs::beta".into()),
                        crate::db::value::DataValue::Str("calls".into()),
                        crate::db::value::DataValue::from(0.9f64),
                        crate::db::value::DataValue::Str("{}".into()),
                        crate::db::value::DataValue::Str("local".into()),
                    ],
                    vec![
                        crate::db::value::DataValue::Str("/src/a.rs::beta".into()),
                        crate::db::value::DataValue::Str("/src/a.rs::alpha".into()),
                        crate::db::value::DataValue::Str("uses".into()),
                        crate::db::value::DataValue::from(0.5f64),
                        crate::db::value::DataValue::Str("{}".into()),
                        crate::db::value::DataValue::Str("local".into()),
                    ],
                ],
                next: None,
            },
        );
        backend.import_relations(data).unwrap();

        let out = backend
            .run_script(
                "?[source_qualified, target_qualified, rel_type] := *relationships{source_qualified, target_qualified, rel_type}",
                Default::default(),
            )
            .unwrap();
        assert_eq!(out.rows.len(), 2, "both rows must import");
    }

    /// Regression: `fuzzy_find_elements` was a headless projection (no
    /// `:= *code_elements{…}` relation ref) — unparseable Cozo, so the L2
    /// fuzzy tier silently degraded on SQLite.
    #[test]
    /// Regression (#293/#294): test symbols are demoted below the real
    /// implementation, and exact-name matches outrank longer substring
    /// matches within the same demotion tier.
    /// Regression (#290-followup): QNs containing literal double quotes
    /// (markdown doc_section titles like `ctx_read("src/main.rs")`) broke
    /// the want-rel literal — double-quoted cozo strings cannot carry \".
    /// The hydration path now emits single-quoted literals with '' escaping.
    #[test]
    fn elements_by_qualified_names_handles_quotes_in_qn() {
        use crate::db::value::{DataValue as DV, Num as DVNum};
        let tmp = TempDir::new().unwrap();
        let backend = SqliteBackend::open(&tmp.path().join(".leankg"), false).unwrap();
        let mut data = BTreeMap::new();
        let qn = "/docs/report.md::6.2 ctx_read(\"src/main.rs\")";
        data.insert(
            "code_elements".to_string(),
            NamedRows::new(
                vec![
                    "qualified_name",
                    "element_type",
                    "name",
                    "file_path",
                    "line_start",
                    "line_end",
                    "language",
                    "parent_qualified",
                    "cluster_id",
                    "cluster_label",
                    "metadata",
                    "env",
                ]
                .iter()
                .map(|s| s.to_string())
                .collect(),
                vec![vec![
                    DV::Str(qn.into()),
                    DV::Str("doc_section".into()),
                    DV::Str("ctx_read".into()),
                    DV::Str("/docs/report.md".into()),
                    DV::Num(DVNum::Int(1)),
                    DV::Num(DVNum::Int(10)),
                    DV::Str("md".into()),
                    DV::Str("".into()),
                    DV::Str("".into()),
                    DV::Str("".into()),
                    DV::Str("{}".into()),
                    DV::Str("local".into()),
                ]],
            ),
        );
        backend.import_relations(data).unwrap();
        let got = backend
            .elements_by_qualified_names(&[qn.to_string()])
            .unwrap();
        assert_eq!(got.len(), 1, "quoted QN must hydrate");
        assert_eq!(got[0].name, "ctx_read");
    }

    #[test]
    fn fuzzy_find_elements_ranks_tests_last_and_exact_first() {
        use crate::db::value::{DataValue as DV, Num as DVNum};
        let tmp = TempDir::new().unwrap();
        let backend = SqliteBackend::open(&tmp.path().join(".leankg"), false).unwrap();
        let mut data = BTreeMap::new();
        let row = |qn: &str, name: &str, file: &str| {
            vec![
                DV::Str(qn.into()),
                DV::Str("function".into()),
                DV::Str(name.into()),
                DV::Str(file.into()),
                DV::Num(DVNum::Int(1)),
                DV::Num(DVNum::Int(10)),
                DV::Str("rust".into()),
                DV::Str("".into()),
                DV::Str("".into()),
                DV::Str("".into()),
                DV::Str("{}".into()),
                DV::Str("local".into()),
            ]
        };
        data.insert(
            "code_elements".to_string(),
            NamedRows::new(
                vec![
                    "qualified_name",
                    "element_type",
                    "name",
                    "file_path",
                    "line_start",
                    "line_end",
                    "language",
                    "parent_qualified",
                    "cluster_id",
                    "cluster_label",
                    "metadata",
                    "env",
                ]
                .iter()
                .map(|s| s.to_string())
                .collect(),
                vec![
                    row(
                        "/src/tests.rs::mcp_status_test",
                        "mcp_status_test",
                        "/src/tests.rs",
                    ),
                    row(
                        "/src/handler.rs::mcp_status_longer_name",
                        "mcp_status_longer_name",
                        "/src/handler.rs",
                    ),
                    row(
                        "/src/handler.rs::mcp_status",
                        "mcp_status",
                        "/src/handler.rs",
                    ),
                ],
            ),
        );
        backend.import_relations(data).unwrap();
        let got = backend
            .fuzzy_find_elements("mcp_status", 3)
            .expect("fuzzy must not error");
        let names: Vec<String> = got.iter().map(|e| e.name.clone()).collect();
        assert_eq!(
            names,
            vec![
                "mcp_status".to_string(),
                "mcp_status_longer_name".to_string(),
                "mcp_status_test".to_string(),
            ],
            "real handler first, longer substring second, test symbol last"
        );
    }

    fn fuzzy_find_elements_matches_substring() {
        let tmp = TempDir::new().unwrap();
        let backend = SqliteBackend::open(&tmp.path().join(".leankg"), false).unwrap();
        let mut data = BTreeMap::new();
        data.insert(
            "code_elements".to_string(),
            NamedRows {
                headers: [
                    "qualified_name",
                    "element_type",
                    "name",
                    "file_path",
                    "line_start",
                    "line_end",
                    "language",
                    "parent_qualified",
                    "cluster_id",
                    "cluster_label",
                    "metadata",
                    "env",
                ]
                .iter()
                .map(|s| s.to_string())
                .collect(),
                rows: vec![
                    vec![
                        crate::db::value::DataValue::Str("/src/a.rs::alpha".into()),
                        crate::db::value::DataValue::Str("function".into()),
                        crate::db::value::DataValue::Str("alpha".into()),
                        crate::db::value::DataValue::Str("/src/a.rs".into()),
                        crate::db::value::DataValue::Num(crate::db::value::Num::Int(1)),
                        crate::db::value::DataValue::Num(crate::db::value::Num::Int(10)),
                        crate::db::value::DataValue::Str("rust".into()),
                        crate::db::value::DataValue::Str("".into()),
                        crate::db::value::DataValue::Str("".into()),
                        crate::db::value::DataValue::Str("".into()),
                        crate::db::value::DataValue::Str("{}".into()),
                        crate::db::value::DataValue::Str("local".into()),
                    ],
                    vec![
                        crate::db::value::DataValue::Str("/src/a.rs::beta".into()),
                        crate::db::value::DataValue::Str("function".into()),
                        crate::db::value::DataValue::Str("beta".into()),
                        crate::db::value::DataValue::Str("/src/a.rs".into()),
                        crate::db::value::DataValue::Num(crate::db::value::Num::Int(11)),
                        crate::db::value::DataValue::Num(crate::db::value::Num::Int(20)),
                        crate::db::value::DataValue::Str("rust".into()),
                        crate::db::value::DataValue::Str("".into()),
                        crate::db::value::DataValue::Str("".into()),
                        crate::db::value::DataValue::Str("".into()),
                        crate::db::value::DataValue::Str("{}".into()),
                        crate::db::value::DataValue::Str("local".into()),
                    ],
                ],
                next: None,
            },
        );
        backend.import_relations(data).unwrap();

        // Case-insensitive substring match (query folded too).
        let got = backend.fuzzy_find_elements("ALPH", 10).unwrap();
        assert_eq!(got.len(), 1);
        assert_eq!(got[0].name, "alpha");
        // Regex metacharacters in real symbol queries must not error the
        // call or over-match — they are literal after escaping.
        assert!(backend
            .fuzzy_find_elements("al(h)a", 10)
            .unwrap()
            .is_empty());
        assert!(backend
            .fuzzy_find_elements("(alpha", 10)
            .unwrap()
            .is_empty());
        assert!(backend
            .fuzzy_find_elements("zzz_no_match", 10)
            .unwrap()
            .is_empty());
    }
}
