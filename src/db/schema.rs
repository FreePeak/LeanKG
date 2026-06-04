use sha2::{Digest, Sha256};
use std::path::Path;

pub type CozoDb = cozo::DbInstance;

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
        .unwrap_or(268435456) // Default 256MB
}

pub fn init_db(db_path: &Path) -> Result<CozoDb, Box<dyn std::error::Error>> {
    let storage = resolve_storage_config(db_path);
    if let Some(parent) = storage.path.parent() {
        std::fs::create_dir_all(parent)?;
    }

    let path_str = storage.path.to_string_lossy().to_string();
    let db = match storage.engine {
        StorageEngine::Sqlite => cozo::DbInstance::new("sqlite", &path_str, "")?,
        StorageEngine::RocksDb => {
            std::fs::create_dir_all(&storage.path)?;
            cozo::DbInstance::new("rocksdb", &path_str, "")?
        }
    };

    let mmap_size = get_env_mmap_size();
    tracing::info!(
        "Cozo storage = {:?} at {} (LEANKG_MMAP_SIZE={})",
        storage.engine,
        storage.path.display(),
        mmap_size
    );

    // Set memory limits for SQLite (CozoDB backend) - run individually to avoid parsing issues
    let static_pragmas: &[&str] = &[
        "PRAGMA cache_size = -64000",
        "PRAGMA temp_store = MEMORY",
        "PRAGMA synchronous = NORMAL",
        "PRAGMA journal_mode = WAL",
        "PRAGMA wal_autocheckpoint = 100",
    ];
    for pragma in static_pragmas {
        if let Err(e) = db.run_script(pragma, Default::default()) {
            tracing::debug!("Pragma '{}' failed (may not be supported): {}", pragma, e);
        }
    }

    // mmap_size is dynamic based on env var
    let mmap_pragma = format!("PRAGMA mmap_size = {}", mmap_size);
    if let Err(e) = db.run_script(&mmap_pragma, Default::default()) {
        tracing::debug!("mmap_size pragma failed: {}", e);
    }

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

    let project_root = if db_path.file_name().and_then(|name| name.to_str()) == Some(".leankg") {
        db_path.parent().unwrap_or(db_path)
    } else {
        db_path
    };
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

fn init_schema(db: &CozoDb) -> Result<(), Box<dyn std::error::Error>> {
    let check_relations = r#"::relations"#;
    let relations_result = db.run_script(check_relations, Default::default())?;
    let existing_relations: std::collections::HashSet<String> = relations_result
        .rows
        .iter()
        .filter_map(|row| row.first().and_then(|v| v.as_str().map(String::from)))
        .collect();

    if !existing_relations.contains("code_elements") {
        let create_code_elements = r#":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}"#;
        if let Err(e) = db.run_script(create_code_elements, Default::default()) {
            eprintln!("Failed to create code_elements: {:?}", e);
        }
    } else {
        let create_file_path_index =
            r#":create code_elements::file_path_index {ref: (file_path), compressed: true}"#;
        if let Err(e) = db.run_script(create_file_path_index, Default::default()) {
            tracing::debug!("file_path index may already exist: {:?}", e);
        }

        let create_qualified_name_index = r#":create code_elements::qualified_name_index {ref: (qualified_name), compressed: true}"#;
        if let Err(e) = db.run_script(create_qualified_name_index, Default::default()) {
            tracing::debug!("qualified_name index may already exist: {:?}", e);
        }

        let create_element_type_index =
            r#":create code_elements::element_type_index {ref: (element_type), compressed: true}"#;
        if let Err(e) = db.run_script(create_element_type_index, Default::default()) {
            tracing::debug!("element_type index may already exist: {:?}", e);
        }

        let create_parent_qualified_index = r#":create code_elements::parent_qualified_index {ref: (parent_qualified), compressed: true}"#;
        if let Err(e) = db.run_script(create_parent_qualified_index, Default::default()) {
            tracing::debug!("parent_qualified index may already exist: {:?}", e);
        }

        validate_code_elements_schema(db)?;
    }

    if !existing_relations.contains("relationships") {
        let create_relationships = r#":create relationships {source_qualified: String, target_qualified: String, rel_type: String, confidence: Float, metadata: String, env: String default 'local'}"#;
        if let Err(e) = db.run_script(create_relationships, Default::default()) {
            eprintln!("Failed to create relationships: {:?}", e);
        }
    } else {
        let create_rel_type_index =
            r#":create relationships::rel_type_index {ref: (rel_type), compressed: true}"#;
        if let Err(e) = db.run_script(create_rel_type_index, Default::default()) {
            tracing::debug!("rel_type index may already exist: {:?}", e);
        }

        let create_target_index = r#":create relationships::target_qualified_index {ref: (target_qualified), compressed: true}"#;
        if let Err(e) = db.run_script(create_target_index, Default::default()) {
            tracing::debug!("target_qualified index may already exist: {:?}", e);
        }

        // NOTE: source_qualified_index is created for each DB to avoid migration issues
        // See get_relationships_for_elements_optimized in query.rs

        validate_relationships_schema(db)?;
    }

    if !existing_relations.contains("business_logic") {
        let create_business_logic = r#":create business_logic {element_qualified: String, description: String, user_story_id: String?, feature_id: String?}"#;
        if let Err(e) = db.run_script(create_business_logic, Default::default()) {
            eprintln!("Failed to create business_logic: {:?}", e);
        }
    }

    if !existing_relations.contains("context_metrics") {
        let create_context_metrics = r#":create context_metrics {tool_name: String, timestamp: Int, project_path: String, input_tokens: Int, output_tokens: Int, output_elements: Int, execution_time_ms: Int, baseline_tokens: Int, baseline_lines_scanned: Int, tokens_saved: Int, savings_percent: Float, correct_elements: Int?, total_expected: Int?, f1_score: Float?, query_pattern: String?, query_file: String?, query_depth: Int?, success: Bool, is_deleted: Bool}"#;
        if let Err(e) = db.run_script(create_context_metrics, Default::default()) {
            eprintln!("Failed to create context_metrics: {:?}", e);
        }

        let create_tool_index =
            r#":create context_metrics::tool_name_index {ref: (tool_name), compressed: true}"#;
        if let Err(e) = db.run_script(create_tool_index, Default::default()) {
            tracing::debug!("tool_name index may already exist: {:?}", e);
        }

        let create_timestamp_index =
            r#":create context_metrics::timestamp_index {ref: (timestamp), compressed: true}"#;
        if let Err(e) = db.run_script(create_timestamp_index, Default::default()) {
            tracing::debug!("timestamp index may already exist: {:?}", e);
        }

        let create_project_index = r#":create context_metrics::project_path_index {ref: (project_path), compressed: true}"#;
        if let Err(e) = db.run_script(create_project_index, Default::default()) {
            tracing::debug!("project_path index may already exist: {:?}", e);
        }
    }

    if !existing_relations.contains("query_cache") {
        let create_query_cache = r#":create query_cache {cache_key: String, value_json: String, created_at: Int, ttl_seconds: Int, tool_name: String, project_path: String, metadata: String}"#;
        if let Err(e) = db.run_script(create_query_cache, Default::default()) {
            eprintln!("Failed to create query_cache: {:?}", e);
        }

        let create_key_index = r#":create query_cache::cache_key_index {ref: (cache_key), compressed: true, unique: true}"#;
        if let Err(e) = db.run_script(create_key_index, Default::default()) {
            tracing::debug!("cache_key index may already exist: {:?}", e);
        }

        let create_tool_index =
            r#":create query_cache::tool_name_index {ref: (tool_name), compressed: true}"#;
        if let Err(e) = db.run_script(create_tool_index, Default::default()) {
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
        if let Err(e) = db.run_script(create_svc, Default::default()) {
            tracing::warn!("Failed to create service_metadata: {:?}", e);
        }
        let svc_indexes = [
            r#":create service_metadata::svc_name_index {ref: (service_name), compressed: true}"#,
            r#":create service_metadata::svc_env_index {ref: (env), compressed: true}"#,
        ];
        for idx in &svc_indexes {
            if let Err(e) = db.run_script(idx, Default::default()) {
                tracing::debug!("service_metadata index note: {:?}", e);
            }
        }
    }

    // Create teams table for shared graph management
    if !existing_relations.contains("teams") {
        let create_teams = r#":create teams {id: String, name: String, description: String, owner_id: String, created_at: Int, updated_at: Int, graph_read_users: String, graph_write_users: String, members: String}"#;
        if let Err(e) = db.run_script(create_teams, Default::default()) {
            tracing::warn!("Failed to create teams: {:?}", e);
        }
        let team_indexes = [r#":create teams::owner_index {ref: (owner_id), compressed: true}"#];
        for idx in &team_indexes {
            if let Err(e) = db.run_script(idx, Default::default()) {
                tracing::debug!("teams index note: {:?}", e);
            }
        }
    }

    // Create team_invites table for onboarding workflow
    if !existing_relations.contains("team_invites") {
        let create_invites = r#":create team_invites {token: String, team_id: String, email: String?, role: String, created_by: String, created_at: Int, expires_at: Int, accepted: Bool, accepted_by: String?}"#;
        if let Err(e) = db.run_script(create_invites, Default::default()) {
            tracing::warn!("Failed to create team_invites: {:?}", e);
        }
        let invite_indexes = [
            r#":create team_invites::team_index {ref: (team_id), compressed: true}"#,
            r#":create team_invites::token_index {ref: (token), compressed: true, unique: true}"#,
        ];
        for idx in &invite_indexes {
            if let Err(e) = db.run_script(idx, Default::default()) {
                tracing::debug!("team_invites index note: {:?}", e);
            }
        }
    }

    Ok(())
}

fn run_migrations(
    db: &CozoDb,
    existing_relations: &std::collections::HashSet<String>,
) -> Result<(), Box<dyn std::error::Error>> {
    // Create migrations tracking table if not exists
    if !existing_relations.contains("migrations") {
        let create_migrations = r#":create migrations {id: String, applied_at: Int}"#;
        if let Err(e) = db.run_script(create_migrations, Default::default()) {
            tracing::warn!("Failed to create migrations table: {:?}", e);
        }
    }

    // Get already-applied migrations
    let applied: std::collections::HashSet<String> = db
        .run_script("?[id] := *migrations[id, _]", Default::default())
        .map(|r| {
            r.rows
                .iter()
                .filter_map(|row| row.first().and_then(|v| v.as_str().map(String::from)))
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
        if let Err(e) = db.run_script(create_knowledge, Default::default()) {
            tracing::warn!("Migration 001 failed (may already exist): {:?}", e);
        }

        // Create indexes
        let indexes = [
            r#":create knowledge_entries::type_index {ref: (knowledge_type), compressed: true}"#,
            r#":create knowledge_entries::element_index {ref: (element_qualified), compressed: true}"#,
            r#":create knowledge_entries::env_index {ref: (environment), compressed: true}"#,
            r#":create knowledge_entries::author_index {ref: (author), compressed: true}"#,
        ];
        for idx in &indexes {
            if let Err(e) = db.run_script(idx, Default::default()) {
                tracing::debug!("Index creation note: {:?}", e);
            }
        }

        record_migration(db, "001_knowledge_entries", now)?;
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
            if db.run_script(query, Default::default()).is_ok() {
                return arity;
            }
        }
    }

    let query = format!(":schema {}", relation);
    db.run_script(&query, Default::default())
        .map(|r| r.rows.len())
        .unwrap_or(0)
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
    match current {
        11 => {
            db.run_script(REPAIR_LEGACY_CODE_ELEMENTS_11_TO_13, Default::default())?;
        }
        12 => {
            db.run_script(REPAIR_LEGACY_CODE_ELEMENTS_12_TO_13, Default::default())?;
        }
        _ => {
            tracing::warn!(
                "code_elements schema has unsupported arity {}; canonical repair only supports legacy 11- or 12-column schema",
                current
            );
            return Ok(());
        }
    }
    tracing::info!("code_elements :replace successful");
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
    db.run_script(REPAIR_LEGACY_RELATIONSHIPS_5_TO_6, Default::default())?;
    tracing::info!("relationships :replace successful");
    Ok(())
}

fn ensure_incidents_table(db: &CozoDb) -> Result<(), Box<dyn std::error::Error>> {
    let existing = db
        .run_script("::relations", Default::default())
        .map(|r| {
            r.rows
                .iter()
                .filter_map(|row| row.first().and_then(|v| v.as_str().map(String::from)))
                .collect::<std::collections::HashSet<_>>()
        })
        .unwrap_or_default();
    if existing.contains("incidents") {
        return Ok(());
    }
    let create_incidents = r#":create incidents {id: String, env: String, title: String, severity: String, occurred_at: Int, resolved_at: Int?, root_cause: String, resolution: String, affected_services: String, trigger_pattern: String?, prevention: String?, tags: String, author: String, linked_ticket: String?}"#;
    db.run_script(create_incidents, Default::default())?;
    for idx in &[
        r#":create incidents::env_index {ref: (env), compressed: true}"#,
        r#":create incidents::severity_index {ref: (severity), compressed: true}"#,
        r#":create incidents::author_index {ref: (author), compressed: true}"#,
    ] {
        if let Err(e) = db.run_script(idx, Default::default()) {
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
    db.run_script(query, params)?;
    Ok(())
}

fn validate_code_elements_schema(db: &CozoDb) -> Result<(), Box<dyn std::error::Error>> {
    let schema_query = r#":schema code_elements"#;
    match db.run_script(schema_query, Default::default()) {
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
    match db.run_script(schema_query, Default::default()) {
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
