// Verify correct parameters for all tools
use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;
use leankg::mcp::handler::ToolHandler;
use serde_json::json;

#[tokio::test(flavor = "multi_thread")]
async fn test_verify_correct_params() {
    let db_path = std::path::PathBuf::from(".leankg");
    let db = init_db(db_path.as_path()).expect("Failed to init db");
    let graph = GraphEngine::new(db);
    let handler = ToolHandler::new(graph, db_path);

    println!("=== Testing query_file with correct patterns ===");
    // query_file uses contains(), so use partial path
    let q1 = handler
        .execute_tool("query_file", &json!({"pattern": "main.rs"}))
        .await
        .unwrap();
    let files1 = q1
        .get("files")
        .and_then(|f| f.as_array())
        .map(|a| a.len())
        .unwrap_or(0);
    println!("query_file 'main.rs': {} files", files1);

    let q2 = handler
        .execute_tool("query_file", &json!({"pattern": "lib.rs"}))
        .await
        .unwrap();
    let files2 = q2
        .get("files")
        .and_then(|f| f.as_array())
        .map(|a| a.len())
        .unwrap_or(0);
    println!("query_file 'lib.rs': {} files", files2);

    // Test with .rs (without wildcard)
    let q3 = handler
        .execute_tool("query_file", &json!({"pattern": ".rs"}))
        .await
        .unwrap();
    let files3 = q3
        .get("files")
        .and_then(|f| f.as_array())
        .map(|a| a.len())
        .unwrap_or(0);
    println!("query_file '.rs': {} files", files3);

    println!("\n=== Testing get_dependencies with correct paths ===");
    // Find a file that has imports
    let deps1 = handler
        .execute_tool("get_dependencies", &json!({"file": "./src/api/auth.rs"}))
        .await
        .unwrap();
    let deps_count1 = deps1
        .get("dependencies")
        .and_then(|d| d.as_array())
        .map(|a| a.len())
        .unwrap_or(0);
    println!("get_dependencies './src/api/auth.rs': {} deps", deps_count1);

    let deps2 = handler
        .execute_tool("get_dependencies", &json!({"file": "./src/api/mod.rs"}))
        .await
        .unwrap();
    let deps_count2 = deps2
        .get("dependencies")
        .and_then(|d| d.as_array())
        .map(|a| a.len())
        .unwrap_or(0);
    println!("get_dependencies './src/api/mod.rs': {} deps", deps_count2);

    println!("\n=== Testing get_callers with correct paths ===");
    // Find a function that has callers
    let callers1 = handler
        .execute_tool(
            "get_callers",
            &json!({"function": "./src/api/auth.rs::auth_middleware"}),
        )
        .await
        .unwrap();
    let callers_count1 = callers1
        .get("callers")
        .and_then(|c| c.as_array())
        .map(|a| a.len())
        .unwrap_or(0);
    println!(
        "get_callers './src/api/auth.rs::auth_middleware': {} callers",
        callers_count1
    );

    let cg1 = handler
        .execute_tool(
            "get_call_graph",
            &json!({"function": "./src/api/auth.rs::auth_middleware", "depth": 2}),
        )
        .await
        .unwrap();
    let calls_count1 = cg1
        .get("calls")
        .and_then(|c| c.as_array())
        .map(|a| a.len())
        .unwrap_or(0);
    println!(
        "get_call_graph './src/api/auth.rs::auth_middleware': {} calls",
        calls_count1
    );

    println!("\n=== Testing get_files_for_doc with correct doc name ===");
    // Use a doc that exists
    let ffd1 = handler
        .execute_tool("get_files_for_doc", &json!({"doc": "docs/AGENTS.md"}))
        .await
        .unwrap();
    let ffd_count1 = ffd1
        .get("files")
        .and_then(|f| f.as_array())
        .map(|a| a.len())
        .unwrap_or(0);
    println!("get_files_for_doc 'docs/AGENTS.md': {} files", ffd_count1);

    let ffd2 = handler
        .execute_tool("get_files_for_doc", &json!({"doc": "docs/prd.md"}))
        .await
        .unwrap();
    let ffd_count2 = ffd2
        .get("files")
        .and_then(|f| f.as_array())
        .map(|a| a.len())
        .unwrap_or(0);
    println!("get_files_for_doc 'docs/prd.md': {} files", ffd_count2);

    // Also test documented_by relationship
    println!("\n=== Testing documented_by relationships ===");
    let dbf1 = handler
        .execute_tool("get_doc_for_file", &json!({"file": "./src/api/auth.rs"}))
        .await
        .unwrap();
    let docs_count = dbf1
        .get("documents")
        .and_then(|d| d.as_array())
        .map(|a| a.len())
        .unwrap_or(0);
    println!("get_doc_for_file './src/api/auth.rs': {} docs", docs_count);
}
