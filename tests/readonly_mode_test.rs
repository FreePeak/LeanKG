//! Read-only mode tests.
//!
//! Phase 1 of the RocksDB lock-contention plan: query-only MCP servers should
//! be able to open a `GraphEngine` without taking RocksDB's LOCK and reject
//! any tool that mutates state. These tests exercise the public surface:
//!
//! * `leankg::db::backend::init_db_readonly` — PG read-only connection
//! * `leankg::graph::GraphEngine::open_readonly` — wraps the above
//! * `leankg::mcp::server::MCPServer::is_write_tool` — write-tool whitelist
//! * `leankg::mcp::server::MCPServer` — rejects write tools in read-only mode
//!
//! Read-write paths are not tested here (the default constructor and existing
//! MCP tests already cover them).

use leankg::db::backend::{init_db, init_db_readonly};
use leankg::graph::GraphEngine;
use tempfile::TempDir;

#[test]
fn init_db_readonly_succeeds_on_fresh_sqlite_path() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("readonly.db");

    let db = init_db_readonly(&db_path).expect("init_db_readonly must succeed on fresh path");
    drop(db); // explicit close — CozoDb doesn't expose close()
}

#[test]
fn init_db_readonly_can_read_after_init_db_writes() {
    // First open as RW and create a relation, then open RO and read it back.
    // This mirrors the "query-only replica" use case.
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("share.db");

    {
        let db = init_db(&db_path).expect("init_db must succeed");
        db.run_script(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [["probe::alpha", "symbol", "alpha", "probe.rs", 1, 1, "rust", null, null, null, "{}", "local", "procedural"], ["probe::beta", "symbol", "beta", "probe.rs", 1, 1, "rust", null, null, null, "{}", "local", "procedural"]] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
            Default::default(),
        )
        .expect("code_elements put must succeed");
    }

    let ro_db = init_db_readonly(&db_path).expect("init_db_readonly must succeed on populated DB");
    let rows = ro_db
        .run_script(r#"?[name] := *code_elements[name]"#, Default::default())
        .expect("probe read must succeed in RO mode");
    let names: Vec<String> = rows
        .rows
        .iter()
        .filter_map(|r| r.first().and_then(|v| v.get_str().map(String::from)))
        .collect();
    assert!(
        names.contains(&"alpha".to_string()) && names.contains(&"beta".to_string()),
        "expected to read both alpha + beta from read-only handle, got {:?}",
        names
    );
}

#[test]
fn graph_engine_open_readonly_constructs_successfully() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("engine_ro.db");

    // Populate first so the engine has something to query
    {
        let db = init_db(&db_path).expect("init_db must succeed");
        db.run_script(r#":create probe {name: String}"#, Default::default())
            .expect("probe create must succeed");
    }

    let ge = GraphEngine::open_readonly(&db_path).expect("GraphEngine::open_readonly must succeed");
    let _ = ge.db(); // smoke-check the handle is callable
}

#[test]
fn mcpserver_is_write_tool_classifies_correctly() {
    use leankg::mcp::server::MCPServer;
    // Write tools (must be rejected in read-only mode).
    for name in [
        "mcp_init",
        "mcp_index",
        "mcp_index_docs",
        "add_knowledge",
        "update_knowledge",
        "delete_knowledge",
        "add_annotation",
        "link_element",
        "add_documentation",
        "promote_environment",
        "embed_control",
        "ontology_control",
        // Completeness audit — mutators previously missing from WRITE_TOOLS.
        "mcp_embed",
        "index_prd",
        "agent_diary_write",
        "report_query_outcome",
        "export_graph_snapshot",
        "export_html",
        "generate_doc",
        "mcp_install",
        "add_ontology_concept",
        "add_ontology_workflow",
        "delete_ontology_concept",
    ] {
        assert!(
            MCPServer::is_write_tool(name),
            "{} must be classified as a write tool",
            name
        );
    }
    // Read tools (must still work in read-only mode).
    for name in [
        "search_code",
        "find_function",
        "get_context",
        "query_file",
        "get_dependencies",
        "get_dependents",
        "kg_context",
        "kg_concept_map",
        "kg_trace_workflow",
        "kg_ontology_status",
        "mcp_status",
    ] {
        assert!(
            !MCPServer::is_write_tool(name),
            "{} must NOT be classified as a write tool",
            name
        );
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn mcpserver_rejects_all_write_tools_in_read_only_mode() {
    use leankg::db::backend::init_db;
    use leankg::mcp::server::MCPServer;
    use serde_json::Map;

    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg");
    std::fs::create_dir_all(&db_path).unwrap();
    let db = init_db(&db_path).expect("init_db must succeed");
    drop(db);

    let server = MCPServer::new(db_path).with_read_only(true);

    for name in MCPServer::write_tool_names() {
        let err = server
            .execute_tool_pub(name, Map::new())
            .await
            .expect_err(&format!(
                "write tool '{}' must be rejected in read-only mode",
                name
            ));
        assert!(
            err.contains("read-only mode"),
            "expected read-only error for '{}', got: {}",
            name,
            err
        );
    }
}

#[test]
fn mcpserver_list_tools_omits_writes_when_read_only() {
    use leankg::mcp::server::MCPServer;
    use std::collections::HashSet;

    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg");
    std::fs::create_dir_all(&db_path).unwrap();

    let server = MCPServer::new(db_path).with_read_only(true);
    let listed: HashSet<String> = server.list_tool_names().into_iter().collect();
    let writes: HashSet<&str> = MCPServer::write_tool_names().iter().copied().collect();

    let overlap: Vec<&String> = listed
        .iter()
        .filter(|n| writes.contains(n.as_str()))
        .collect();
    assert!(
        overlap.is_empty(),
        "RO list_tools must omit all write tools; found: {:?}",
        overlap
    );
    assert!(
        listed.contains("search_code") && listed.contains("mcp_status"),
        "RO list_tools must still include read tools"
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn mcpserver_rejects_write_tools_in_read_only_mode() {
    use leankg::db::backend::init_db;
    use leankg::mcp::server::MCPServer;
    use serde_json::Map;

    // Seed schema only — RO gate short-circuits before tool dispatch/DB writes.
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg");
    std::fs::create_dir_all(&db_path).unwrap();
    let db = init_db(&db_path).expect("init_db must succeed");
    drop(db);

    let server = MCPServer::new(db_path.clone()).with_read_only(true);
    assert!(server.is_read_only(), "server should report read_only=true");

    // Try a known write tool — must return a clear read-only error.
    let err = server
        .execute_tool_pub("add_annotation", Map::new())
        .await
        .expect_err("write tool must be rejected in read-only mode");
    assert!(
        err.contains("read-only mode"),
        "expected read-only error, got: {}",
        err
    );

    // And confirm the helper is exposed on the type (so Subagent A can call
    // the same gate from any future write path).
    assert!(MCPServer::is_write_tool("add_annotation"));
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn mcpserver_allows_read_tools_in_read_only_mode() {
    use leankg::db::backend::init_db;
    use leankg::mcp::server::MCPServer;
    use serde_json::Map;

    // Empty DB — read tools should still be dispatched (and either succeed
    // with empty results or fail with a normal "no data" error, NOT the
    // read-only guard).
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg");
    std::fs::create_dir_all(&db_path).unwrap();
    let db = init_db(&db_path).expect("init_db must succeed");
    drop(db);

    let server = MCPServer::new(db_path).with_read_only(true);

    // `search_code` is a read tool — must NOT trip the read-only guard.
    let result = server
        .execute_tool_pub("search_code", Map::new())
        .await
        .map_err(|e| e.to_string());
    if let Err(msg) = result {
        assert!(
            !msg.contains("read-only mode"),
            "read tool must not be blocked by read-only gate, got: {}",
            msg
        );
    }
}
