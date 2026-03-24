use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;
use leankg::mcp::auth::{hash_token, AuthConfig};
use leankg::mcp::handler::ToolHandler;
use leankg::mcp::protocol::{MCPRequest, MCPResponse};
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
            let has_file = props.get("file").is_some();
            let has_query = props.get("query").is_some();
            let has_pattern = props.get("pattern").is_some();
            let has_name = props.get("name").is_some();
            let has_function = props.get("function").is_some();
            let has_min_lines = props.get("min_lines").is_some();

            assert!(
                has_file || has_query || has_pattern || has_name || has_function || has_min_lines,
                "Tool {} should have at least one parameter",
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
mod protocol_tests {
    use super::*;

    #[test]
    fn test_mcp_request_deserialization() {
        let json_str = r#"{
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {"name": "test"}
        }"#;

        let request: MCPRequest = serde_json::from_str(json_str).unwrap();
        assert_eq!(request.jsonrpc, "2.0");
        assert_eq!(request.id, Some(1));
        assert_eq!(request.method, "tools/call");
        assert!(request.params.is_some());
    }

    #[test]
    fn test_mcp_response_success_serialization() {
        let response = MCPResponse::success(Some(42), json!({"result": "success"}));

        let json_str = serde_json::to_string(&response).unwrap();
        assert!(json_str.contains("\"jsonrpc\":\"2.0\""));
        assert!(json_str.contains("\"id\":42"));
        assert!(json_str.contains("\"result\""));
    }

    #[test]
    fn test_mcp_response_error_serialization() {
        let response = MCPResponse::error(Some(1), -32600, "Invalid request".to_string());

        let json_str = serde_json::to_string(&response).unwrap();
        assert!(json_str.contains("\"jsonrpc\":\"2.0\""));
        assert!(json_str.contains("\"error\""));
        assert!(json_str.contains("-32600"));
    }

    #[test]
    fn test_mcp_request_without_id() {
        let json_str = r#"{
            "jsonrpc": "2.0",
            "method": "ping"
        }"#;

        let request: MCPRequest = serde_json::from_str(json_str).unwrap();
        assert_eq!(request.id, None);
        assert_eq!(request.method, "ping");
    }

    #[test]
    fn test_mcp_request_without_params() {
        let json_str = r#"{
            "jsonrpc": "2.0",
            "id": 5,
            "method": "ping"
        }"#;

        let request: MCPRequest = serde_json::from_str(json_str).unwrap();
        assert_eq!(request.id, Some(5));
        assert!(request.params.is_none());
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
        (ToolHandler::new(graph), tmp)
    }

    #[tokio::test]
    async fn test_handler_query_file_empty() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler
            .execute_tool("query_file", &json!({"pattern": "nonexistent"}))
            .await;

        assert!(result.is_ok());
        let value = result.unwrap();
        assert!(value.get("files").is_some());
    }

    #[tokio::test]
    async fn test_handler_query_file_missing_param() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("query_file", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("pattern"));
    }

    #[tokio::test]
    async fn test_handler_get_dependencies_missing_param() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("get_dependencies", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("file"));
    }

    #[tokio::test]
    async fn test_handler_get_dependents_missing_param() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("get_dependents", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("file"));
    }

    #[tokio::test]
    async fn test_handler_get_impact_radius_missing_params() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("get_impact_radius", &json!({})).await;

        assert!(result.is_err());
    }

    #[tokio::test]
    async fn test_handler_get_review_context_missing_params() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("get_review_context", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("files"));
    }

    #[tokio::test]
    async fn test_handler_find_function_missing_param() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("find_function", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("name"));
    }

    #[tokio::test]
    async fn test_handler_get_call_graph_missing_param() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("get_call_graph", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("function"));
    }

    #[tokio::test]
    async fn test_handler_search_code_missing_param() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("search_code", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("query"));
    }

    #[tokio::test]
    async fn test_handler_unknown_tool() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("nonexistent_tool", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("Unknown tool"));
    }

    #[tokio::test]
    async fn test_handler_get_context_missing_param() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("get_context", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("file"));
    }

    #[tokio::test]
    async fn test_handler_generate_doc_missing_param() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("generate_doc", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("file"));
    }

    #[tokio::test]
    async fn test_handler_find_large_functions_default() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler
            .execute_tool("find_large_functions", &json!({}))
            .await;

        assert!(result.is_ok());
        let value = result.unwrap();
        assert!(value.get("large_functions").is_some());
    }

    #[tokio::test]
    async fn test_handler_get_tested_by_missing_param() {
        let (handler, _tmp) = create_test_handler().await;

        let result = handler.execute_tool("get_tested_by", &json!({})).await;

        assert!(result.is_err());
        assert!(result.unwrap_err().contains("file"));
    }
}

#[cfg(test)]
mod server_tests {
    use super::*;

    #[tokio::test]
    async fn test_mcp_server_creation() {
        let server = MCPServer::new(std::path::PathBuf::from(".leankg"));
        let _guard = server.auth_config_read().await;
    }

    #[test]
    fn test_mcp_server_with_custom_db_path() {
        let db_path = std::path::PathBuf::from("/custom/path/.leankg");
        let server = MCPServer::new(db_path.clone());
        assert_eq!(server.db_path(), &db_path);
    }
}
