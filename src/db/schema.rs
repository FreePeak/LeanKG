use cozo::{Db, SqliteStorage};
use std::path::Path;

pub type CozoDb = Db<SqliteStorage>;

fn get_env_mmap_size() -> u64 {
    std::env::var("LEANKG_MMAP_SIZE")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(268435456) // Default 256MB
}

pub fn init_db(db_path: &Path) -> Result<CozoDb, Box<dyn std::error::Error>> {
    let db_file_path = if db_path.is_dir() {
        db_path.join("leankg.db")
    } else {
        db_path.to_path_buf()
    };

    let path_str = db_file_path.to_string_lossy().to_string();

    let db = cozo::new_cozo_sqlite(path_str)?;

    let mmap_size = get_env_mmap_size();
    tracing::info!(
        "SQLite mmap_size = {} (LEANKG_MMAP_SIZE={})",
        mmap_size,
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

fn init_schema(db: &CozoDb) -> Result<(), Box<dyn std::error::Error>> {
    let check_relations = r#"::relations"#;
    let relations_result = db.run_script(check_relations, Default::default())?;
    let existing_relations: std::collections::HashSet<String> = relations_result
        .rows
        .iter()
        .filter_map(|row| row.first().and_then(|v| v.as_str().map(String::from)))
        .collect();

    if !existing_relations.contains("code_elements") {
        let create_code_elements = r#":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local'}"#;
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

    // Migration 002: Add version columns to code_elements
    if !applied.contains("002_code_elements_versioning") {
        tracing::info!("Running migration 002_code_elements_versioning...");
        // Only apply if the table already existed (new tables get these columns at creation)
        if existing_relations.contains("code_elements") {
            let replace_code_elements = r#":replace code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, environment: String default 'production', branch: String? default null, version_tag: String? default null, indexed_at: Int default 0}"#;
            if let Err(e) = db.run_script(replace_code_elements, Default::default()) {
                tracing::warn!("Migration 002 code_elements replace failed: {:?}", e);
            }
        }
        record_migration(db, "002_code_elements_versioning", now)?;
    }

    // Migration 003: Add version columns to business_logic
    if !applied.contains("003_business_logic_versioning") {
        tracing::info!("Running migration 003_business_logic_versioning...");
        if existing_relations.contains("business_logic") {
            let replace_bl = r#":replace business_logic {element_qualified: String, description: String, user_story_id: String?, feature_id: String?, environment: String default 'production', branch: String? default null, author: String default '', updated_at: Int default 0}"#;
            if let Err(e) = db.run_script(replace_bl, Default::default()) {
                tracing::warn!("Migration 003 business_logic replace failed: {:?}", e);
            }
        }
        record_migration(db, "003_business_logic_versioning", now)?;
    }

    // Migration 004: Add env column and incidents table
    if !applied.contains("004_env_and_incidents") {
        tracing::info!("Running migration 004_env_and_incidents...");
        if existing_relations.contains("code_elements") {
            let replace_code_elements = r#":replace code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, environment: String default 'production', branch: String? default null, version_tag: String? default null, indexed_at: Int default 0, env: String default 'local'}"#;
            if let Err(e) = db.run_script(replace_code_elements, Default::default()) {
                tracing::warn!("Migration 004 code_elements replace failed: {:?}", e);
            }
        }
        if existing_relations.contains("relationships") {
            let replace_rel = r#":replace relationships {source_qualified: String, target_qualified: String, rel_type: String, confidence: Float, metadata: String, env: String default 'local'}"#;
            if let Err(e) = db.run_script(replace_rel, Default::default()) {
                tracing::warn!("Migration 004 relationships replace failed: {:?}", e);
            }
        }
        // Create incidents table
        let create_incidents = r#":create incidents {id: String, env: String, title: String, severity: String, occurred_at: Int, resolved_at: Int?, root_cause: String, resolution: String, affected_services: String, trigger_pattern: String?, prevention: String?, tags: String, author: String, linked_ticket: String?}"#;
        if let Err(e) = db.run_script(create_incidents, Default::default()) {
            tracing::warn!("Migration 004 incidents create failed: {:?}", e);
        }
        // Create indexes
        let incident_indexes = [
            r#":create incidents::env_index {ref: (env), compressed: true}"#,
            r#":create incidents::severity_index {ref: (severity), compressed: true}"#,
            r#":create incidents::author_index {ref: (author), compressed: true}"#,
        ];
        for idx in &incident_indexes {
            if let Err(e) = db.run_script(idx, Default::default()) {
                tracing::debug!("Incident index creation note: {:?}", e);
            }
        }
        record_migration(db, "004_env_and_incidents", now)?;
    }

    // Migration 005: Canonicalize graph schemas after experimental version columns.
    //
    // The Rust data model and query layer use env-scoped graph records with these
    // arities. Some earlier migrations expanded code_elements with environment,
    // branch, version_tag, and indexed_at columns, which makes existing query
    // destructuring fail. Keep version/team metadata inside metadata JSON.
    if !applied.contains("005_canonical_env_graph_schema") {
        tracing::info!("Running migration 005_canonical_env_graph_schema...");
        if existing_relations.contains("code_elements") {
            let replace_code_elements = r#":replace code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local'}"#;
            if let Err(e) = db.run_script(replace_code_elements, Default::default()) {
                tracing::warn!("Migration 005 code_elements replace failed: {:?}", e);
            }
        }
        if existing_relations.contains("relationships") {
            let replace_relationships = r#":replace relationships {source_qualified: String, target_qualified: String, rel_type: String, confidence: Float, metadata: String, env: String default 'local'}"#;
            if let Err(e) = db.run_script(replace_relationships, Default::default()) {
                tracing::warn!("Migration 005 relationships replace failed: {:?}", e);
            }
        }
        record_migration(db, "005_canonical_env_graph_schema", now)?;
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
            const EXPECTED_COLUMNS: usize = 12;
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
