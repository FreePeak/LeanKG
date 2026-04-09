use cozo::{Db, SqliteStorage};
use std::path::Path;

pub type CozoDb = Db<SqliteStorage>;

pub fn init_db(db_path: &Path) -> Result<CozoDb, Box<dyn std::error::Error>> {
    let db_file_path = if db_path.is_dir() {
        db_path.join("leankg.db")
    } else {
        db_path.to_path_buf()
    };

    let path_str = db_file_path.to_string_lossy().to_string();

    let db = cozo::new_cozo_sqlite(path_str)?;

    init_schema(&db)?;

    Ok(db)
}

fn init_schema(db: &CozoDb) -> Result<(), Box<dyn std::error::Error>> {
    let check_relations = r#"::relations"#;
    let relations_result = db.run_script(check_relations, Default::default())?;
    let existing_relations: std::collections::HashSet<String> = relations_result
        .rows
        .iter()
        .filter_map(|row| row.get(0).and_then(|v| v.as_str().map(String::from)))
        .collect();

    if !existing_relations.contains("code_elements") {
        let create_code_elements = r#":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String}"#;
        if let Err(e) = db.run_script(create_code_elements, Default::default()) {
            eprintln!("Failed to create code_elements: {:?}", e);
        }
    } else {
        // Validate schema has correct column count
        validate_code_elements_schema(db)?;
    }

    if !existing_relations.contains("relationships") {
        let create_relationships = r#":create relationships {source_qualified: String, target_qualified: String, rel_type: String, confidence: Float, metadata: String}"#;
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

        let create_cache_key_index =
            r#":create query_cache::cache_key_index {ref: (cache_key), compressed: true}"#;
        if let Err(e) = db.run_script(create_cache_key_index, Default::default()) {
            tracing::debug!("cache_key index may already exist: {:?}", e);
        }

        let create_tool_index =
            r#":create query_cache::tool_name_index {ref: (tool_name), compressed: true}"#;
        if let Err(e) = db.run_script(create_tool_index, Default::default()) {
            tracing::debug!("tool_name index may already exist: {:?}", e);
        }

        let create_project_path_index =
            r#":create query_cache::project_path_index {ref: (project_path), compressed: true}"#;
        if let Err(e) = db.run_script(create_project_path_index, Default::default()) {
            tracing::debug!("project_path index may already exist: {:?}", e);
        }
    }

    Ok(())
}

fn validate_code_elements_schema(db: &CozoDb) -> Result<(), Box<dyn std::error::Error>> {
    // Get schema info for code_elements
    let schema_query = r#":schema code_elements"#;
    match db.run_script(schema_query, Default::default()) {
        Ok(result) => {
            // Count the rows (each row is a column definition)
            let column_count = result.rows.len();
            const EXPECTED_COLUMNS: usize = 11;
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
