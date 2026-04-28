// Diagnostic test to understand why tools return empty
use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;
use leankg::mcp::handler::ToolHandler;
use serde_json::json;

#[tokio::test(flavor = "multi_thread")]
async fn test_diagnose_empty_tools() {
    let db_path = std::path::PathBuf::from(".leankg");
    let db = init_db(db_path.as_path()).expect("Failed to init db");
    let graph = GraphEngine::new(db);
    let handler = ToolHandler::new(graph, db_path);

    // First, let's see what data exists
    println!("=== Checking existing data ===");

    // Check what functions exist
    let search_result = handler
        .execute_tool("search_code", &json!({"query": "fn", "limit": 5}))
        .await
        .unwrap();
    println!("search_code 'fn' result: {}", search_result);

    // Check code_tree to see what files have functions
    let tree_result = handler
        .execute_tool("get_code_tree", &json!({}))
        .await
        .unwrap();
    println!(
        "get_code_tree result (first 500 chars): {}",
        &tree_result.to_string()[..500.min(tree_result.to_string().len())]
    );

    // Test query_file with different patterns
    println!("\n=== Testing query_file ===");
    let patterns = ["*.rs", "main", "src", "lib"];
    for pat in patterns {
        let result = handler
            .execute_tool("query_file", &json!({"pattern": pat}))
            .await
            .unwrap();
        let files = result
            .get("files")
            .map(|f| f.as_array().map(|a| a.len()).unwrap_or(0))
            .unwrap_or(0);
        println!("query_file pattern='{}': {} files", pat, files);
    }

    // Test get_dependencies with different files
    println!("\n=== Testing get_dependencies ===");
    let deps_result = handler
        .execute_tool("get_dependencies", &json!({"file": "src/lib.rs"}))
        .await
        .unwrap();
    println!("get_dependencies for src/lib.rs: {}", deps_result);

    // Check if there are any imports in the graph
    let deps_result2 = handler
        .execute_tool("get_dependencies", &json!({"file": "src/main.rs"}))
        .await
        .unwrap();
    println!("get_dependencies for src/main.rs: {}", deps_result2);

    // Test get_call_graph and get_callers
    println!("\n=== Testing call-related tools ===");

    // First find a real function
    let func_result = handler
        .execute_tool("find_function", &json!({"name": "main"}))
        .await
        .unwrap();
    println!("find_function 'main': {}", func_result);

    // Try to get call graph for a real function
    let cg_result = handler
        .execute_tool(
            "get_call_graph",
            &json!({"function": "src/main.rs::main", "depth": 2}),
        )
        .await
        .unwrap();
    println!("get_call_graph for src/main.rs::main: {}", cg_result);

    let callers_result = handler
        .execute_tool("get_callers", &json!({"function": "src/main.rs::main"}))
        .await
        .unwrap();
    println!("get_callers for src/main.rs::main: {}", callers_result);

    // Test get_files_for_doc
    println!("\n=== Testing get_files_for_doc ===");
    let ffd_result = handler
        .execute_tool("get_files_for_doc", &json!({"doc": "README.md"}))
        .await
        .unwrap();
    println!("get_files_for_doc for README.md: {}", ffd_result);

    // Check relationships to understand what data we have
    println!("\n=== Checking relationships ===");
    let all_elements = handler
        .execute_tool("search_code", &json!({"query": "lib", "limit": 1}))
        .await
        .unwrap();
    println!("Sample element: {}", all_elements);

    // Check if there are any documented_by relationships
    let related_result = handler
        .execute_tool("find_related_docs", &json!({"file": "src/lib.rs"}))
        .await
        .unwrap();
    println!("find_related_docs for src/lib.rs: {}", related_result);
}
