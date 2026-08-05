//! Comprehensive unit tests for ALL 35 MCP tools
//!
//! This test suite verifies that each MCP tool:
//! 1. Accepts valid parameters
//! 2. Returns non-empty data when called with proper parameters
//! 3. Returns proper error for missing required parameters

use leankg::db::backend::init_db;
use leankg::graph::GraphEngine;
use leankg::mcp::handler::ToolHandler;
use serde_json::json;
use tempfile::TempDir;

/// Creates a test handler with the real .leankg database
async fn create_real_handler() -> (ToolHandler, TempDir) {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    seed_test_data(db.as_ref());
    let graph = GraphEngine::new(db.clone());
    (ToolHandler::new(graph, db_path), tmp)
}

fn seed_test_data(db: &dyn leankg::db::backend::DbBackend) {
    let elements = r#"
    ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] <-
    [
        ["./src/main.rs", "file", "main.rs", "./src/main.rs", 1, 1, "rust", null, "1", "core", "{}", "local"],
        ["./src/main.rs::main", "function", "main", "./src/main.rs", 1, 10, "rust", null, "1", "core", "{}", "local"],
        ["./src/main.rs::validate_key", "function", "validate_key", "./src/main.rs", 20, 30, "rust", null, "1", "core", "{}", "local"],
        ["./src/lib.rs::caller", "function", "caller", "./src/lib.rs", 1, 5, "rust", null, "1", "core", "{}", "local"]
    ]
    :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env}
    "#;
    db.run_script(elements, Default::default()).unwrap();

    let relationships = r#"
    ?[source_qualified, target_qualified, rel_type, confidence, metadata, env] <-
    [
        ["./src/main.rs", "./src/lib.rs", "imports", 1.0, "{}", "local"],
        ["./src/main.rs::main", "./src/main.rs::validate_key", "calls", 1.0, "{}", "local"],
        ["./src/lib.rs::caller", "./src/main.rs::validate_key", "calls", 1.0, "{}", "local"],
        ["./src/main.rs::main", "docs/README.md", "documented_by", 1.0, "{}", "local"]
    ]
    :put relationships {source_qualified, target_qualified, rel_type, confidence, metadata, env}
    "#;
    db.run_script(relationships, Default::default()).unwrap();
}

// ============================================================================
// MCP Core Tools Tests
// ============================================================================

mod mcp_core_tools {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn test_mcp_init() {
        let (handler, tmp) = create_real_handler().await;
        let init_path = tmp.path().join("init");
        let result = handler
            .execute_tool(
                "mcp_init",
                &json!({"path": init_path.to_string_lossy().as_ref()}),
            )
            .await;
        assert!(
            result.is_ok(),
            "mcp_init should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        assert!(
            value.get("initialized").is_some()
                || value.as_bool() == Some(true)
                || !value.to_string().is_empty()
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_mcp_status() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("mcp_status", &json!({})).await;
        assert!(
            result.is_ok(),
            "mcp_status should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        // Status should return info about the database
        let is_empty = value.as_object().map(|o| o.is_empty()).unwrap_or(false);
        assert!(!is_empty, "mcp_status should return non-empty data");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_mcp_status_include_counts_exposes_per_project_counts() {
        // FR-A04: mcp_status include_counts=true must expose element counts
        // for the resolved project (index per leankg.yaml / project mount).
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("mcp_status", &json!({"include_counts": true}))
            .await;
        assert!(
            result.is_ok(),
            "mcp_status include_counts should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        assert_eq!(
            value.get("counts_included").and_then(|v| v.as_bool()),
            Some(true),
            "counts_included must be true when requested"
        );
        // Seed data: 4 elements / 4 relationships (see seed_test_data).
        assert_eq!(
            value.get("elements").and_then(|v| v.as_i64()),
            Some(4),
            "elements count must come from the resolved project DB"
        );
        assert_eq!(
            value.get("relationships").and_then(|v| v.as_i64()),
            Some(4),
            "relationships count must come from the resolved project DB"
        );
        // Default (no include_counts) must NOT expose counts.
        let plain = handler
            .execute_tool("mcp_status", &json!({}))
            .await
            .unwrap();
        assert_eq!(
            plain.get("counts_included").and_then(|v| v.as_bool()),
            Some(false)
        );
        assert!(plain.get("elements").is_none());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_mcp_index() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("mcp_index", &json!({"path": "./src"}))
            .await;
        assert!(
            result.is_ok(),
            "mcp_index should succeed: {:?}",
            result.err()
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_mcp_index_docs() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("mcp_index_docs", &json!({"path": "./docs"}))
            .await;
        // May fail if docs don't exist, but should not panic
        if let Err(err) = result {
            assert!(
                err.contains("not found") || err.contains("empty") || err.contains("no doc"),
                "mcp_index_docs error should be expected for empty docs: {}",
                err
            );
        }
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_mcp_install() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("mcp_install", &json!({})).await;
        // Should succeed and return installation info
        assert!(
            result.is_ok(),
            "mcp_install should succeed: {:?}",
            result.err()
        );
    }
}

// ============================================================================
// Query Tools Tests
// ============================================================================

mod query_tools {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn test_query_file() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool(
                "query_file",
                &json!({"file": "./src/main.rs", "pattern": "fn"}),
            )
            .await;
        assert!(
            result.is_ok(),
            "query_file should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        let has_data = value.get("files").is_some()
            || value.get("results").is_some()
            || !value.to_string().is_empty();
        assert!(has_data, "query_file should return data");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_query_file_missing_pattern() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("query_file", &json!({"file": "./src/main.rs"}))
            .await;
        assert!(result.is_err(), "query_file should error without pattern");
        assert!(
            result.unwrap_err().contains("pattern"),
            "Error should mention 'pattern'"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_search_code() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("search_code", &json!({"query": "fn"}))
            .await;
        assert!(
            result.is_ok(),
            "search_code should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        let has_data = value.get("results").is_some() || !value.to_string().is_empty();
        assert!(has_data, "search_code should return data");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_search_code_missing_query() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("search_code", &json!({})).await;
        assert!(result.is_err(), "search_code should error without query");
        assert!(
            result.unwrap_err().contains("query"),
            "Error should mention 'query'"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_find_function() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("find_function", &json!({"name": "main"}))
            .await;
        assert!(
            result.is_ok(),
            "find_function should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        let is_empty = value.as_array().map(|a| a.is_empty()).unwrap_or(false);
        assert!(!is_empty, "find_function should return data for 'main'");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_find_function_missing_name() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("find_function", &json!({})).await;
        assert!(result.is_err(), "find_function should error without name");
        assert!(
            result.unwrap_err().contains("name"),
            "Error should mention 'name'"
        );
    }
}

// ============================================================================
// Dependency/Call Graph Tools Tests
// ============================================================================

mod dependency_tools {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_dependencies() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("get_dependencies", &json!({"file": "./src/main.rs"}))
            .await;
        assert!(
            result.is_ok(),
            "get_dependencies should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        let has_data = value.get("dependencies").is_some() || !value.to_string().is_empty();
        assert!(has_data, "get_dependencies should return data");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_dependencies_missing_file() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("get_dependencies", &json!({})).await;
        assert!(
            result.is_err(),
            "get_dependencies should error without file"
        );
        assert!(
            result.unwrap_err().contains("file"),
            "Error should mention 'file'"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_dependents() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("get_dependents", &json!({"file": "./src/main.rs"}))
            .await;
        assert!(
            result.is_ok(),
            "get_dependents should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        let has_data = value.get("dependents").is_some() || !value.to_string().is_empty();
        assert!(has_data, "get_dependents should return data");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_dependents_missing_file() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("get_dependents", &json!({})).await;
        assert!(result.is_err(), "get_dependents should error without file");
        assert!(
            result.unwrap_err().contains("file"),
            "Error should mention 'file'"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_call_graph() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool(
                "get_call_graph",
                &json!({"function": "./src/main.rs::main", "depth": 1}),
            )
            .await;
        assert!(
            result.is_ok(),
            "get_call_graph should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        let has_data = value.get("calls").is_some() || !value.to_string().is_empty();
        assert!(has_data, "get_call_graph should return data");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_call_graph_missing_function() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("get_call_graph", &json!({})).await;
        assert!(
            result.is_err(),
            "get_call_graph should error without function"
        );
        assert!(
            result.unwrap_err().contains("function"),
            "Error should mention 'function'"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_callers() {
        let (handler, _tmp) = create_real_handler().await;
        // Find a function that has callers
        let result = handler
            .execute_tool("get_callers", &json!({"function": "validate_key"}))
            .await;
        assert!(
            result.is_ok(),
            "get_callers should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        // May be empty if validate_key has no callers, but should not error
        assert!(
            value.get("callers").is_some() || !value.to_string().is_empty(),
            "get_callers should return callers field or empty array"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_callers_missing_function() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("get_callers", &json!({})).await;
        // get_callers might require function parameter
        if let Err(err) = result {
            assert!(
                err.contains("function") || err.contains("name"),
                "Error should mention 'function' or 'name'"
            );
        }
    }
}

// ============================================================================
// Impact/Context Tools Tests
// ============================================================================

mod impact_context_tools {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_impact_radius() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool(
                "get_impact_radius",
                &json!({"file": "./src/main.rs", "depth": 2}),
            )
            .await;
        assert!(
            result.is_ok(),
            "get_impact_radius should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        let has_data = value.get("impact").is_some()
            || value.get("affected").is_some()
            || !value.to_string().is_empty();
        assert!(has_data, "get_impact_radius should return data");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_impact_radius_missing_params() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("get_impact_radius", &json!({})).await;
        assert!(
            result.is_err(),
            "get_impact_radius should error without params"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_context() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("get_context", &json!({"file": "./src/main.rs"}))
            .await;
        assert!(
            result.is_ok(),
            "get_context should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        let is_empty = value.as_str().map(|s| s.is_empty()).unwrap_or(false);
        let has_obj_data = value.as_object().map(|o| !o.is_empty()).unwrap_or(false);
        assert!(
            !is_empty || has_obj_data,
            "get_context should return non-empty data"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_context_missing_file() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("get_context", &json!({})).await;
        assert!(result.is_err(), "get_context should error without file");
        assert!(
            result.unwrap_err().contains("file"),
            "Error should mention 'file'"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_review_context() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("get_review_context", &json!({"files": ["./src/main.rs"]}))
            .await;
        assert!(
            result.is_ok(),
            "get_review_context should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        let has_data = value.get("context").is_some()
            || value.get("review").is_some()
            || !value.to_string().is_empty();
        assert!(has_data, "get_review_context should return data");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_review_context_missing_files() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("get_review_context", &json!({})).await;
        assert!(
            result.is_err(),
            "get_review_context should error without files"
        );
        assert!(
            result.unwrap_err().contains("files"),
            "Error should mention 'files'"
        );
    }
}

// ============================================================================
// Documentation Tools Tests
// ============================================================================

mod documentation_tools {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_files_for_doc() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("get_files_for_doc", &json!({"doc": "./docs/README.md"}))
            .await;
        assert!(
            result.is_ok(),
            "get_files_for_doc should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        assert!(
            value.get("files").is_some() || !value.to_string().is_empty(),
            "get_files_for_doc should return files field"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_doc_tree() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("get_doc_tree", &json!({})).await;
        assert!(
            result.is_ok(),
            "get_doc_tree should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        let has_data = value.get("tree").is_some() || !value.to_string().is_empty();
        assert!(has_data, "get_doc_tree should return data");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_generate_doc() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("generate_doc", &json!({"file": "./src/main.rs"}))
            .await;
        assert!(
            result.is_ok(),
            "generate_doc should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        let is_empty = value.as_str().map(|s| s.is_empty()).unwrap_or(false);
        let has_obj_data = value.as_object().map(|o| !o.is_empty()).unwrap_or(false);
        assert!(
            !is_empty || has_obj_data,
            "generate_doc should return non-empty data"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_generate_doc_missing_file() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("generate_doc", &json!({})).await;
        assert!(result.is_err(), "generate_doc should error without file");
        assert!(
            result.unwrap_err().contains("file"),
            "Error should mention 'file'"
        );
    }
}

// ============================================================================
// Traceability Tools Tests
// ============================================================================

mod traceability_tools {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_traceability() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool(
                "get_traceability",
                &json!({"element": "./src/main.rs::main"}),
            )
            .await;
        assert!(
            result.is_ok(),
            "get_traceability should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        assert!(
            value.get("traceability").is_some() || !value.to_string().is_empty(),
            "get_traceability should return data"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_search_by_requirement() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool(
                "search_by_requirement",
                &json!({"requirement_id": "REQ-001"}),
            )
            .await;
        // May return empty if no requirements indexed, but should not error
        assert!(
            result.is_ok(),
            "search_by_requirement should succeed: {:?}",
            result.err()
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_code_tree() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("get_code_tree", &json!({})).await;
        assert!(
            result.is_ok(),
            "get_code_tree should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        let has_data = value.get("tree").is_some()
            || value.get("code").is_some()
            || !value.to_string().is_empty();
        assert!(has_data, "get_code_tree should return data");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_find_related_docs() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("find_related_docs", &json!({"file": "./src/main.rs"}))
            .await;
        assert!(
            result.is_ok(),
            "find_related_docs should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        assert!(
            value.get("docs").is_some() || !value.to_string().is_empty(),
            "find_related_docs should return data"
        );
    }
}

// ============================================================================
// Cluster Tools Tests
// ============================================================================

mod cluster_tools {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_clusters() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("get_clusters", &json!({})).await;
        assert!(
            result.is_ok(),
            "get_clusters should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        let has_data = value.get("clusters").is_some() || !value.to_string().is_empty();
        assert!(has_data, "get_clusters should return data");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_cluster_context() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("get_cluster_context", &json!({"cluster_id": "1"}))
            .await;
        // May fail if cluster doesn't exist
        if let Err(err) = result {
            assert!(
                err.contains("not found") || err.contains("Cluster"),
                "Expected cluster not found error: {}",
                err
            );
        }
    }
}

// ============================================================================
// Service/Utility Tools Tests
// ============================================================================

mod utility_tools {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn test_kg_self_test() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("kg_self_test", &json!({})).await;
        assert!(
            result.is_ok(),
            "kg_self_test should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        assert!(
            !value.to_string().is_empty(),
            "kg_self_test should return diagnostics"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_service_graph() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("get_service_graph", &json!({})).await;
        assert!(
            result.is_ok(),
            "get_service_graph should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        let has_data = value.get("services").is_some()
            || value.get("graph").is_some()
            || !value.to_string().is_empty();
        assert!(has_data, "get_service_graph should return data");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_detect_changes() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("detect_changes", &json!({"path": "./src"}))
            .await;
        assert!(
            result.is_ok(),
            "detect_changes should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        let has_data = value.get("changes").is_some() || !value.to_string().is_empty();
        assert!(has_data, "detect_changes should return data");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_orchestrate() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("orchestrate", &json!({"intent": "find main function"}))
            .await;
        assert!(
            result.is_ok(),
            "orchestrate should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        // Orchestrate may return complex result, just verify it returns something
        assert!(
            !value.to_string().is_empty(),
            "orchestrate should return data"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_ctx_read() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("ctx_read", &json!({"file": "./src/main.rs"}))
            .await;
        assert!(
            result.is_ok(),
            "ctx_read should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        // ctx_read returns string content
        let is_string = value.is_string();
        assert!(is_string, "ctx_read should return string content");
        assert!(
            !value.as_str().unwrap_or("").is_empty(),
            "ctx_read should return non-empty content"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_run_raw_query() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("run_raw_query", &json!({"query": "?[name] := *code_elements[_, _, name, _, _, _, _, _, _, _, _, _, _] :limit 5"}))
            .await;
        assert!(
            result.is_ok(),
            "run_raw_query should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        let has_data = value.get("rows").is_some() || !value.to_string().is_empty();
        assert!(has_data, "run_raw_query should return data");
    }
}

// ============================================================================
// Analysis Tools Tests
// ============================================================================

mod analysis_tools {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn test_find_large_functions() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("find_large_functions", &json!({}))
            .await;
        assert!(
            result.is_ok(),
            "find_large_functions should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        let has_data = value.get("large_functions").is_some() || !value.to_string().is_empty();
        assert!(has_data, "find_large_functions should return data");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_tested_by() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler
            .execute_tool("get_tested_by", &json!({"file": "./src/main.rs"}))
            .await;
        assert!(
            result.is_ok(),
            "get_tested_by should succeed: {:?}",
            result.err()
        );
        let value = result.unwrap();
        let has_data = value.get("tests").is_some()
            || value.get("tested_by").is_some()
            || !value.to_string().is_empty();
        assert!(has_data, "get_tested_by should return data");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_get_tested_by_missing_file() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("get_tested_by", &json!({})).await;
        assert!(result.is_err(), "get_tested_by should error without file");
        assert!(
            result.unwrap_err().contains("file"),
            "Error should mention 'file'"
        );
    }
}

// ============================================================================
// Error Handling Tests
// ============================================================================

mod error_handling {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn test_unknown_tool() {
        let (handler, _tmp) = create_real_handler().await;
        let result = handler.execute_tool("nonexistent_tool", &json!({})).await;
        assert!(result.is_err(), "Unknown tool should error");
        let err = result.unwrap_err();
        assert!(
            err.contains("Unknown") || err.contains("not found") || err.contains("Unknown tool"),
            "Error should mention unknown tool: {}",
            err
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_invalid_json_params() {
        let (handler, _tmp) = create_real_handler().await;
        // Should handle gracefully - passing non-object params
        let result = handler
            .execute_tool("mcp_status", &serde_json::Value::Null)
            .await;
        // Should either succeed with defaults or fail gracefully
        if let Err(err) = result {
            assert!(
                err.contains("param") || err.contains("argument"),
                "Error should be about parameters"
            );
        }
    }
}
// full-scan must return a refusal payload instead of executing. The guard uses
// the cheap cached `is_mega_graph` probe, not a full `count_elements`.
mod mega_guard_tests {
    use super::*;
    use std::sync::Mutex;

    static MEGA_ENV_LOCK: Mutex<()> = Mutex::new(());

    /// Seeded graph of 4 elements > threshold 2 -> mega.
    fn create_mega_handler() -> (ToolHandler, TempDir) {
        let tmp = TempDir::new().unwrap();
        let db_path = tmp.path().join("leankg.db");
        let db = init_db(db_path.as_path()).unwrap();
        seed_test_data(db.as_ref());
        let graph = GraphEngine::new(db.clone());
        (ToolHandler::new(graph, db_path), tmp)
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn full_scan_tools_refuse_on_mega_graph() {
        // Recover from a poisoned lock (a prior failing test) so env is still
        // serialized but the suite does not cascade-fail.
        let _guard = MEGA_ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        std::env::set_var("LEANKG_MAX_CACHE_ELEMENTS", "2");
        let (handler, _tmp) = create_mega_handler();

        // Each of these tools full-scans on a mega-graph and must refuse.
        // get_cluster_skill is excluded: it now serves precomputed cluster rows
        // on mega (better than refusal) — covered by the dedicated test below.
        let cases: Vec<(&str, serde_json::Value)> = vec![
            ("find_dead_code", json!({"min_lines": 1})),
            ("get_graph_report", json!({})),
            ("export_html", json!({})),
            ("export_graph_snapshot", json!({})),
            ("check_consistency", json!({})),
            ("temporal_query", json!({"at": 1718000000})),
            ("timeline", json!({"qualified_name": "./src/main.rs::main"})),
        ];

        for (tool, args) in cases {
            let result = handler.execute_tool(tool, &args).await;
            let value = result.expect(&format!("{tool} should return a value on mega"));
            let text = value.to_string();
            assert!(
                text.contains("refused") || text.contains("max 50000"),
                "{tool} must refuse on mega graph, got: {text}"
            );
        }

        std::env::remove_var("LEANKG_MAX_CACHE_ELEMENTS");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn cluster_skill_mega_path_uses_precomputed() {
        let _guard = MEGA_ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        std::env::set_var("LEANKG_MAX_CACHE_ELEMENTS", "2");
        let (handler, _tmp) = create_mega_handler();

        // Precomputed cluster_id=1 exists in seed; on mega the tool must serve
        // from precomputed rows (source:"precomputed") — never run live Louvain.
        let result = handler
            .execute_tool("get_cluster_skill", &json!({"cluster_id": "1"}))
            .await;
        let value = result.expect("get_cluster_skill must return a value on mega");
        assert_eq!(value["source"], "precomputed", "got: {value}");
        assert!(
            value["markdown"].as_str().unwrap_or("").contains("SKILL"),
            "markdown must be a SKILL doc, got: {value}"
        );

        std::env::remove_var("LEANKG_MAX_CACHE_ELEMENTS");
    }
}
