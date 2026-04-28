// Diagnostic test to understand relationships and qualified names
use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;
use leankg::mcp::handler::ToolHandler;
use serde_json::json;

#[tokio::test(flavor = "multi_thread")]
async fn test_diagnose_relationships() {
    let db_path = std::path::PathBuf::from(".leankg");
    let db = init_db(db_path.as_path()).expect("Failed to init db");
    let graph = GraphEngine::new(db);
    let handler = ToolHandler::new(graph, db_path);

    // Check what relationship types exist
    println!("=== Checking relationships ===");

    // Get a real function with correct qualified name
    let func_result = handler
        .execute_tool("find_function", &json!({"name": "main"}))
        .await
        .unwrap();
    let functions = func_result
        .get("functions")
        .and_then(|f| f.as_array())
        .unwrap();
    println!("Found {} functions with 'main' name", functions.len());
    if !functions.is_empty() {
        if let Some(first) = functions.first() {
            println!(
                "First function qualified_name: {:?}",
                first.get("qualified_name")
            );
        }
    }

    // Try get_callers with the correct qualified name format
    println!("\n=== Testing get_callers with different formats ===");

    // Test with ./ prefix
    let callers1 = handler
        .execute_tool("get_callers", &json!({"function": "./src/main.rs::main"}))
        .await
        .unwrap();
    println!("get_callers './src/main.rs::main': {}", callers1);

    // Test with just lib.rs function
    let callers2 = handler
        .execute_tool("get_callers", &json!({"function": "./src/lib.rs::new"}))
        .await
        .unwrap();
    println!("get_callers './src/lib.rs::new': {}", callers2);

    // Try get_dependencies with correct path
    println!("\n=== Testing get_dependencies ===");
    let deps1 = handler
        .execute_tool("get_dependencies", &json!({"file": "./src/lib.rs"}))
        .await
        .unwrap();
    println!("get_dependencies './src/lib.rs': {}", deps1);

    let deps2 = handler
        .execute_tool("get_dependencies", &json!({"file": "./src/main.rs"}))
        .await
        .unwrap();
    println!("get_dependencies './src/main.rs': {}", deps2);

    // Check what files have relationships
    println!("\n=== Checking get_dependents ===");
    let dependents1 = handler
        .execute_tool("get_dependents", &json!({"file": "./src/lib.rs"}))
        .await
        .unwrap();
    println!("get_dependents './src/lib.rs': {}", dependents1);

    // Check doc structure
    println!("\n=== Checking doc structure ===");
    let doc_struct = handler
        .execute_tool("get_doc_structure", &json!({}))
        .await
        .unwrap();
    let docs = doc_struct
        .get("documents")
        .and_then(|d| d.as_array())
        .unwrap();
    println!("get_doc_structure: {} documents found", docs.len());
    if !docs.is_empty() {
        if let Some(first) = docs.first() {
            println!("First doc: {:?}", first);
        }
    }

    // Check if README exists in docs
    let related = handler
        .execute_tool("find_related_docs", &json!({"file": "./src/main.rs"}))
        .await
        .unwrap();
    println!("find_related_docs './src/main.rs': {}", related);
}
