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

    Ok(())
}
