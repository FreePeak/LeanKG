// Test all MCP tools against the real database
use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;
use leankg::mcp::handler::ToolHandler;
use serde_json::json;

#[tokio::test(flavor = "multi_thread")]
async fn test_all_mcp_tools() {
    let db_path = std::path::PathBuf::from(".leankg");
    let db = init_db(db_path.as_path()).expect("Failed to init db");
    let graph = GraphEngine::new(db);
    let handler = ToolHandler::new(graph, db_path);

    // First get a valid cluster_id to use for get_cluster_context
    let clusters_result = handler
        .execute_tool("get_clusters", &json!({}))
        .await
        .unwrap();
    let cluster_id = clusters_result
        .get("clusters")
        .and_then(|c| c.as_array())
        .and_then(|arr| arr.first())
        .and_then(|c| c.get("id"))
        .map(|id| id.to_string().trim_matches('"').to_string())
        .unwrap_or_else(|| "unknown".to_string());

    println!("Using cluster_id: {}", cluster_id);

    let tools = vec![
        ("mcp_hello", json!({})),
        ("mcp_status", json!({})),
        ("mcp_hello", json!({"format": "json"})),
        ("search_code", json!({"query": "main", "limit": 5})),
        ("find_function", json!({"name": "new"})),
        ("query_file", json!({"pattern": "main"})),
        ("get_dependencies", json!({"file": "src/lib.rs"})),
        ("get_dependents", json!({"file": "src/lib.rs"})),
        (
            "get_impact_radius",
            json!({"file": "src/main.rs", "depth": 2}),
        ),
        ("get_review_context", json!({"files": ["src/main.rs"]})),
        (
            "get_context",
            json!({"file": "src/main.rs", "mode": "full"}),
        ),
        (
            "get_call_graph",
            json!({"function": "src/main.rs::main", "depth": 2}),
        ),
        ("get_callers", json!({"function": "src/main.rs::main"})),
        ("find_large_functions", json!({"min_lines": 100})),
        ("get_tested_by", json!({"file": "src/main.rs"})),
        ("get_doc_for_file", json!({"file": "src/main.rs"})),
        ("get_files_for_doc", json!({"doc": "README.md"})),
        ("get_doc_structure", json!({})),
        ("get_traceability", json!({"element": "src/main.rs::main"})),
        ("search_by_requirement", json!({"requirement_id": "US-01"})),
        ("get_doc_tree", json!({})),
        ("get_code_tree", json!({})),
        ("find_related_docs", json!({"file": "src/main.rs"})),
        ("get_clusters", json!({})),
        ("get_cluster_context", json!({"cluster_id": cluster_id})),
        ("orchestrate", json!({"intent": "find main function"})),
        ("generate_doc", json!({"file": "src/main.rs"})),
        ("mcp_impact", json!({"file": "src/main.rs", "depth": 2})),
        ("detect_changes", json!({})),
        ("ctx_read", json!({"file": "src/main.rs", "mode": "full"})),
        (
            "run_raw_query",
            json!({"query": "?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] :limit 3"}),
        ),
        ("get_service_graph", json!({"service": "mcp"})),
    ];

    let mut passed = 0;
    let mut failed = 0;
    let mut empty = 0;

    for (tool_name, args) in tools {
        print!("Testing: {:30} ... ", tool_name);
        match handler.execute_tool(tool_name, &args).await {
            Ok(result) => {
                let is_empty = result.is_array() && result.as_array().unwrap().is_empty()
                    || result.is_object() && result.as_object().unwrap().is_empty()
                    || result
                        .get("elements")
                        .and_then(|e| e.as_array())
                        .map(|a| a.is_empty())
                        .unwrap_or(false)
                    || result
                        .get("results")
                        .and_then(|r| r.as_array())
                        .map(|a| a.is_empty())
                        .unwrap_or(false)
                    || result
                        .get("files")
                        .and_then(|f| f.as_array())
                        .map(|a| a.is_empty())
                        .unwrap_or(false)
                    || result
                        .get("functions")
                        .and_then(|f| f.as_array())
                        .map(|a| a.is_empty())
                        .unwrap_or(false)
                    || result
                        .get("large_functions")
                        .and_then(|f| f.as_array())
                        .map(|a| a.is_empty())
                        .unwrap_or(false)
                    || result
                        .get("dependencies")
                        .and_then(|d| d.as_array())
                        .map(|a| a.is_empty())
                        .unwrap_or(false)
                    || result
                        .get("dependents")
                        .and_then(|d| d.as_array())
                        .map(|a| a.is_empty())
                        .unwrap_or(false)
                    || result
                        .get("callers")
                        .and_then(|c| c.as_array())
                        .map(|a| a.is_empty())
                        .unwrap_or(false)
                    || result
                        .get("calls")
                        .and_then(|c| c.as_array())
                        .map(|a| a.is_empty())
                        .unwrap_or(false)
                    || result
                        .get("tests")
                        .and_then(|t| t.as_array())
                        .map(|a| a.is_empty())
                        .unwrap_or(false)
                    || result
                        .get("documents")
                        .and_then(|d| d.as_array())
                        .map(|a| a.is_empty())
                        .unwrap_or(false)
                    || result.get("initialized").is_some(); // mcp_status returns this

                if is_empty {
                    println!("EMPTY");
                    empty += 1;
                } else {
                    println!("OK");
                    passed += 1;
                }
            }
            Err(e) => {
                println!("ERROR: {}", e);
                failed += 1;
            }
        }
    }

    println!("\n=== Summary ===");
    println!("Passed: {}", passed);
    println!("Empty: {}", empty);
    println!("Failed: {}", failed);

    // Assert no failures
    assert_eq!(failed, 0, "Some tools failed: {}", failed);
}
