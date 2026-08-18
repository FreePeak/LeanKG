// Diagnostic test to understand relationships and qualified names
use leankg::db::backend::init_db;
use leankg::graph::GraphEngine;
use leankg::mcp::handler::ToolHandler;
use serde_json::json;

#[tokio::test(flavor = "multi_thread")]
async fn test_diagnose_relationships() {
    let db_path = std::path::PathBuf::from(".leankg");
    let db = init_db(db_path.as_path()).expect("Failed to init db");
    let graph = GraphEngine::new(db.clone());
    let handler = ToolHandler::new(graph, db_path);

    // Check what relationship types exist
    println!("=== Checking relationships ===");

    // Get a real function with search_code
    let func_result = handler
        .execute_tool(
            "search_code",
            &json!({"query": "main", "element_type": "function"}),
        )
        .await
        .unwrap();
    println!("search_code('main', function): {}", func_result);

    // Try get_call_graph with the correct qualified name format
    println!("\n=== Testing get_call_graph with different formats ===");

    // Test with ./ prefix
    let callers1 = handler
        .execute_tool(
            "get_call_graph",
            &json!({"function": "./src/main.rs::main", "depth": 1}),
        )
        .await
        .unwrap();
    println!("get_call_graph './src/main.rs::main': {}", callers1);

    // Test with just lib.rs function
    let callers2 = handler
        .execute_tool(
            "get_call_graph",
            &json!({"function": "./src/lib.rs::new", "depth": 1}),
        )
        .await
        .unwrap();
    println!("get_call_graph './src/lib.rs::new': {}", callers2);

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

    // Check doc tree
    println!("\n=== Checking doc tree ===");
    let doc_tree = handler
        .execute_tool("get_doc_tree", &json!({}))
        .await
        .unwrap();
    println!("get_doc_tree: {}", doc_tree);

    // Check if README exists in docs
    let related = handler
        .execute_tool("find_related_docs", &json!({"file": "./src/main.rs"}))
        .await
        .unwrap();
    println!("find_related_docs './src/main.rs': {}", related);
}
