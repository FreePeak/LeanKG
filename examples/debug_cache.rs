use leankg::db::models::{CodeElement, Relationship};

fn main() {
    let tmp = tempfile::TempDir::new().unwrap();
    let db_path = tmp.path().join("debug_cache_test.db");
    let db = leankg::db::schema::init_db(&db_path).unwrap();
    let graph = leankg::graph::GraphEngine::new(db);

    let elem_b = CodeElement {
        qualified_name: "src/b.rs::mod_b".to_string(),
        element_type: "module".to_string(),
        name: "mod_b".to_string(),
        file_path: "src/b.rs".to_string(),
        line_start: 1,
        line_end: 10,
        language: "rust".to_string(),
        ..Default::default()
    };
    graph.insert_element(&elem_b).unwrap();
    graph.insert_relationship(&Relationship {
        id: None,
        source_qualified: "src/a.rs".to_string(),
        target_qualified: "src/b.rs::mod_b".to_string(),
        rel_type: "imports".to_string(),
        confidence: 1.0,
        metadata: serde_json::json!({}),
    }).unwrap();

    match graph.get_dependencies("src/a.rs") {
        Ok(deps) => println!("get_dependencies returned {} elements", deps.len()),
        Err(e) => println!("get_dependencies error: {:?}", e),
    }
}
