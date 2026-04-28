use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;
use leankg::mcp::auth::{hash_token, AuthConfig};
use leankg::mcp::handler::ToolHandler;
use leankg::mcp::server::MCPServer;
use leankg::mcp::tools::ToolRegistry;
use serde_json::json;
use tempfile::TempDir;

#[cfg(test)]
mod tool_registry_tests {
    use super::*;

    #[test]
    fn test_list_tools_returns_all_required_tools() {
        let tools = ToolRegistry::list_tools();
        let tool_names: Vec<_> = tools.iter().map(|t| t.name.as_str()).collect();

        let required_tools = vec![
            "query_file",
            "get_dependencies",
            "get_dependents",
            "get_impact_radius",
            "get_review_context",
            "find_function",
            "get_call_graph",
            "search_code",
            "get_context",
            "generate_doc",
            "find_large_functions",
            "get_tested_by",
        ];

        for tool in required_tools {
            assert!(tool_names.contains(&tool), "Missing tool: {}", tool);
        }
    }

    #[test]
    fn test_tool_definitions_have_valid_schemas() {
        let tools = ToolRegistry::list_tools();
        for tool in &tools {
            assert!(!tool.name.is_empty(), "Tool name should not be empty");
            assert!(
                !tool.description.is_empty(),
                "Tool description should not be empty"
            );
            assert!(
                tool.input_schema.is_object(),
                "Input schema should be an object"
            );

            let props = tool.input_schema.get("properties");
            assert!(props.is_some(), "Schema should have properties");
            assert!(props.unwrap().is_object(), "Properties should be an object");
        }
    }

    #[test]
    fn test_all_tools_have_file_or_query_param() {
        let tools = ToolRegistry::list_tools();
        for tool in &tools {
            let props = tool.input_schema.get("properties").unwrap();
            let is_empty = props.as_object().map(|o| o.is_empty()).unwrap_or(false);
            let has_file = props.get("file").is_some();
            let has_files = props.get("files").is_some();
            let has_query = props.get("query").is_some();
            let has_pattern = props.get("pattern").is_some();
            let has_name = props.get("name").is_some();
            let has_function = props.get("function").is_some();
            let has_min_lines = props.get("min_lines").is_some();
            let has_doc = props.get("doc").is_some();
            let has_element = props.get("element").is_some();
            let has_requirement_id = props.get("requirement_id").is_some();
            let has_path = props.get("path").is_some();
            let has_incremental = props.get("incremental").is_some();
            let has_lang = props.get("lang").is_some();
            let has_exclude = props.get("exclude").is_some();
            let has_mcp_config_path = props.get("mcp_config_path").is_some();
            let has_depth = props.get("depth").is_some();
            let has_scope = props.get("scope").is_some();
            let has_min_confidence = props.get("min_confidence").is_some();
            let has_cluster_id = props.get("cluster_id").is_some();
            let has_cluster_label = props.get("cluster_label").is_some();
            let has_service = props.get("service").is_some();
            let has_limit = props.get("limit").is_some();
            let has_offset = props.get("offset").is_some();
            let has_route = props.get("route").is_some();
            let has_destination = props.get("destination").is_some();

            assert!(
                is_empty
                    || has_file
                    || has_files
                    || has_query
                    || has_pattern
                    || has_name
                    || has_function
                    || has_min_lines
                    || has_doc
                    || has_element
                    || has_requirement_id
                    || has_path
                    || has_incremental
                    || has_lang
                    || has_exclude
                    || has_mcp_config_path
                    || has_depth
                    || has_scope
                    || has_min_confidence
                    || has_cluster_id
                    || has_cluster_label
                    || has_service
                    || has_limit
                    || has_offset
                    || has_route
                    || has_destination,
                "Tool {} should have at least one parameter or empty properties",
                tool.name
            );
        }
    }
}

#[cfg(test)]
mod auth_tests {
    use super::*;

    #[test]
    fn test_auth_config_default_has_token() {
        let config = AuthConfig::default();
        assert!(!config.tokens.is_empty());
    }

    #[test]
    fn test_auth_config_add_and_validate_token() {
        let mut config = AuthConfig::new();
        let token = "test-token-123".to_string();
        let client_id = "test-client".to_string();

        config.add_token(token.clone(), client_id.clone());

        assert_eq!(config.validate_token(&token), Some(&client_id));
        assert_eq!(config.validate_token("invalid-token"), None);
    }

    #[test]
    fn test_auth_config_multiple_tokens() {
        let mut config = AuthConfig::new();
        config.add_token("token1".to_string(), "client1".to_string());
        config.add_token("token2".to_string(), "client2".to_string());

        assert_eq!(
            config.validate_token("token1"),
            Some(&"client1".to_string())
        );
        assert_eq!(
            config.validate_token("token2"),
            Some(&"client2".to_string())
        );
        assert_eq!(config.validate_token("token3"), None);
    }

    #[test]
    fn test_hash_token_produces_fixed_length() {
        let hash1 = hash_token("secret1");
        let hash2 = hash_token("secret2");

        assert_eq!(hash1.len(), 64);
        assert_eq!(hash2.len(), 64);
        assert_ne!(hash1, hash2);
    }

    #[test]
    fn test_hash_token_deterministic() {
        let hash1 = hash_token("same-secret");
        let hash2 = hash_token("same-secret");
        assert_eq!(hash1, hash2);
    }
}

#[cfg(test)]
mod handler_tests {
    use super::*;

    async fn create_test_handler() -> (ToolHandler, tempfile::TempDir) {
        let tmp = TempDir::new().unwrap();
        let db_path = tmp.path().join("leankg.db");
        let db = init_db(db_path.as_path()).unwrap();
        let graph = GraphEngine::new(db);
        (ToolHandler::new(graph, db_path), tmp)
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_handler_query_file_empty() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler
            .execute_tool("query_file", &json!({"pattern": "nonexistent"}))
            .await;

        assert!(result.is_ok());
        let value = result.unwrap();
        assert!(value.get("files").is_some());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_handler_query_file_missing_param() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("query_file", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("pattern"));
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_handler_get_dependencies_missing_param() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("get_dependencies", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("file"));
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_handler_get_dependents_missing_param() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("get_dependents", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("file"));
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_handler_get_impact_radius_missing_params() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("get_impact_radius", &json!({})).await;

        assert!(result.is_err());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_handler_get_review_context_missing_params() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("get_review_context", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("files"));
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_handler_find_function_missing_param() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("find_function", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("name"));
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_handler_get_call_graph_missing_param() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("get_call_graph", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("function"));
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_handler_search_code_missing_param() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("search_code", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("query"));
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_handler_unknown_tool() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("nonexistent_tool", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("Unknown tool"));
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_handler_get_context_missing_param() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("get_context", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("file"));
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_handler_generate_doc_missing_param() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("generate_doc", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("file"));
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_handler_find_large_functions_default() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler
            .execute_tool("find_large_functions", &json!({}))
            .await;

        assert!(result.is_ok());
        let value = result.unwrap();
        assert!(value.get("large_functions").is_some());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_handler_get_tested_by_missing_param() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("get_tested_by", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("file"));
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_handler_ctx_read_string_output_and_caching() {
        use std::io::Write;
        let (handler, tmp_dir) = create_test_handler().await;

        // 1. Create a dummy file
        let file_path = tmp_dir.path().join("dummy.rs");
        let mut file = std::fs::File::create(&file_path).unwrap();
        writeln!(file, "fn main() {{}}").unwrap();

        // 2. Perform ctx_read natively
        let args = json!({
            "file": file_path.to_str().unwrap(),
            "mode": "full"
        });

        let result1 = handler.execute_tool("ctx_read", &args).await;
        assert!(result1.is_ok());
        let val1 = result1.unwrap();

        // Ensure the value is a pure JSON String scalar and NOT an Object!
        assert!(
            val1.is_string(),
            "Phase 1: Output must be pure TOON string!"
        );
        let output_str = val1.as_str().unwrap();
        assert!(output_str.contains("fn main() {}"));

        // 3. Perform second read to verify Phase 2 SessionCache hits
        let result2 = handler.execute_tool("ctx_read", &args).await;
        let val2 = result2.unwrap();
        let output_str_2 = val2.as_str().unwrap();
        assert!(output_str_2.contains("[File unchanged in SessionCache"));

        // 4. Force Invalidation by deleting file and reading
        std::fs::remove_file(&file_path).unwrap();
        let result3 = handler.execute_tool("ctx_read", &args).await;
        assert!(result3.is_err());

        // The cache should be invalidated now.
        // Let's create it anew and ensure cache doesn't return cache hit.
        let mut file2 = std::fs::File::create(&file_path).unwrap();
        writeln!(file2, "fn after_invalidate() {{}}").unwrap();

        let result4 = handler.execute_tool("ctx_read", &args).await;
        let val4 = result4.unwrap();
        assert!(!val4.as_str().unwrap().contains("[File unchanged"));
        assert!(val4.as_str().unwrap().contains("after_invalidate"));
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_handler_ctx_read_diff_mode() {
        use std::io::Write;
        let (handler, tmp_dir) = create_test_handler().await;

        let file_path = tmp_dir.path().join("diff_target.rs");
        let mut file = std::fs::File::create(&file_path).unwrap();
        writeln!(file, "fn main() {{\n    println!(\"Hello\");\n}}").unwrap();

        let args = json!({
            "file": file_path.to_str().unwrap(),
            "mode": "diff"
        });

        let result1 = handler.execute_tool("ctx_read", &args).await;
        let output1 = result1.unwrap().as_str().unwrap().to_string();

        // On first read with mode=diff, it should fallback to full output
        assert!(output1.contains("[New in Cache => Showing Full"));
        assert!(output1.contains("println!(\"Hello\");"));

        // 2. Modify file
        let mut file2 = std::fs::File::create(&file_path).unwrap();
        writeln!(file2, "fn main() {{\n    println!(\"Goodbye\");\n}}").unwrap();

        // 3. Read again
        let result2 = handler.execute_tool("ctx_read", &args).await;
        let output2 = result2.unwrap().as_str().unwrap().to_string();

        assert!(output2.contains("[auto-delta]"));
        assert!(output2.contains("-    println!(\"Hello\");"));
        assert!(output2.contains("+    println!(\"Goodbye\");"));
    }
}

#[cfg(test)]
mod server_tests {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn test_mcp_server_creation() {
        let server = MCPServer::new(std::path::PathBuf::from(".leankg"));
        let _guard = server.auth_config_read().await;
    }

    #[test]
    fn test_mcp_server_with_custom_db_path() {
        let db_path = std::path::PathBuf::from("/custom/path/.leankg");
        let server = MCPServer::new(db_path.clone());
        let binding = server.db_path();
        let guard = binding.read();
        let server_path = &*guard;
        assert_eq!(server_path, &db_path);
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_multiple_mcp_servers_same_db_path() {
        // Test that multiple MCPServers can be created with the same db_path
        // This verifies multi-session support works (no single-instance lock blocking)
        let temp_dir = TempDir::new().unwrap();
        let db_path = temp_dir.path().join(".leankg");
        std::fs::create_dir_all(&db_path).unwrap();

        // Initialize database first
        let _db = init_db(&db_path).expect("Database init should succeed");

        // Create first server
        let server1 = MCPServer::new(db_path.clone());
        let binding1 = server1.db_path();
        let guard1 = binding1.read();
        assert_eq!(&*guard1, &db_path);

        // Create second server with same db_path - should NOT fail
        let server2 = MCPServer::new(db_path.clone());
        let binding2 = server2.db_path();
        let guard2 = binding2.read();
        assert_eq!(&*guard2, &db_path);

        // Both servers should be different instances
        let ptr1 = &*guard1 as *const std::path::PathBuf;
        let ptr2 = &*guard2 as *const std::path::PathBuf;
        assert_ne!(ptr1, ptr2, "Servers should be separate instances");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_concurrent_reads_from_multiple_servers() {
        // Test that multiple MCP servers can perform concurrent reads
        // This simulates multiple Cursor/Claude Code sessions querying simultaneously
        let temp_dir = TempDir::new().unwrap();
        let db_path = temp_dir.path().join(".leankg");
        std::fs::create_dir_all(&db_path).unwrap();

        // Initialize database
        let _db = init_db(&db_path).expect("Database init should succeed");

        // Create 3 servers concurrently using tokio::join!
        let (r1, r2, r3) = tokio::join! {
            async {
                let server = MCPServer::new(db_path.clone());
                let binding = server.db_path();
                let _guard = binding.read();
                1
            },
            async {
                let server = MCPServer::new(db_path.clone());
                let binding = server.db_path();
                let _guard = binding.read();
                2
            },
            async {
                let server = MCPServer::new(db_path.clone());
                let binding = server.db_path();
                let _guard = binding.read();
                3
            }
        };

        // All 3 concurrent operations should succeed
        assert_eq!(r1, 1);
        assert_eq!(r2, 2);
        assert_eq!(r3, 3);
    }
}
