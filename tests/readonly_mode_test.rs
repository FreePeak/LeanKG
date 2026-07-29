//! Read-only mode tests.
//!
//! Phase 1 of the RocksDB lock-contention plan: query-only MCP servers should
//! be able to open a `GraphEngine` without taking RocksDB's LOCK and reject
//! any tool that mutates state. These tests exercise the public surface:
//!
//! * `leankg::db::schema::init_db_readonly` — opens a SQLite DB in `mode=ro`
//! * `leankg::graph::GraphEngine::open_readonly` — wraps the above
//! * `leankg::mcp::server::MCPServer::is_write_tool` — write-tool whitelist
//! * `leankg::mcp::server::MCPServer` — rejects write tools in read-only mode
//!
//! Read-write paths are not tested here (the default constructor and existing
//! MCP tests already cover them).

use leankg::db::schema::{init_db, init_db_readonly, RocksDbTuning};
use leankg::graph::GraphEngine;
use std::sync::Mutex;
use tempfile::TempDir;

/// `LEANKG_ROCKSDB_*` env vars are process-global, so the two tuning tests
/// must run serially. Without this guard, `cargo test` parallel runs leak
/// env vars between them.
static TUNING_ENV_LOCK: Mutex<()> = Mutex::new(());

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
        leankg::db::schema::run_script(&db, r#":create probe {name: String}"#, Default::default())
            .expect("probe relation create must succeed");
        leankg::db::schema::run_script(
            &db,
            r#"?[name] <- [['alpha'], ['beta']] :put probe {name}"#,
            Default::default(),
        )
        .expect("probe put must succeed");
    }

    let ro_db = init_db_readonly(&db_path).expect("init_db_readonly must succeed on populated DB");
    let rows =
        leankg::db::schema::run_script(&ro_db, r#"?[name] := *probe[name]"#, Default::default())
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
        leankg::db::schema::run_script(&db, r#":create probe {name: String}"#, Default::default())
            .expect("probe create must succeed");
    }

    let ge = GraphEngine::open_readonly(&db_path).expect("GraphEngine::open_readonly must succeed");
    let _ = ge.db(); // smoke-check the handle is callable
}

#[test]
fn rocksdb_tuning_from_env_defaults() {
    // No env vars set; expect the documented production defaults.
    let _guard = TUNING_ENV_LOCK.lock().unwrap();
    let t = RocksDbTuning::default();
    assert_eq!(t.max_open_files, -1);
    assert_eq!(t.block_cache_size_mb, 2048);
    assert_eq!(t.bloom_filter_bits, 10);
    assert_eq!(t.write_buffer_size_mb, 256);

    let from_env = RocksDbTuning::from_env();
    assert_eq!(from_env.max_open_files, t.max_open_files);
    assert_eq!(from_env.block_cache_size_mb, t.block_cache_size_mb);
    assert_eq!(from_env.bloom_filter_bits, t.bloom_filter_bits);
    assert_eq!(from_env.write_buffer_size_mb, t.write_buffer_size_mb);
}

#[test]
fn rocksdb_tuning_from_env_overrides() {
    // Set env vars and confirm they override the defaults. The struct holds
    // values only for logging today (CozoDB 0.7.x cannot apply them yet) so
    // we only assert the parse path works.
    let _guard = TUNING_ENV_LOCK.lock().unwrap();
    std::env::set_var("LEANKG_ROCKSDB_MAX_OPEN_FILES", "512");
    std::env::set_var("LEANKG_ROCKSDB_BLOCK_CACHE_MB", "1024");
    std::env::set_var("LEANKG_ROCKSDB_BLOOM_BITS", "8");
    std::env::set_var("LEANKG_ROCKSDB_WRITE_BUFFER_MB", "128");

    let t = RocksDbTuning::from_env();
    assert_eq!(t.max_open_files, 512);
    assert_eq!(t.block_cache_size_mb, 1024);
    assert_eq!(t.bloom_filter_bits, 8);
    assert_eq!(t.write_buffer_size_mb, 128);

    // Cleanup so other tests see the defaults.
    std::env::remove_var("LEANKG_ROCKSDB_MAX_OPEN_FILES");
    std::env::remove_var("LEANKG_ROCKSDB_BLOCK_CACHE_MB");
    std::env::remove_var("LEANKG_ROCKSDB_BLOOM_BITS");
    std::env::remove_var("LEANKG_ROCKSDB_WRITE_BUFFER_MB");
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

#[tokio::test]
async fn mcpserver_rejects_write_tools_in_read_only_mode() {
    use leankg::db::schema::init_db;
    use leankg::mcp::server::MCPServer;
    use serde_json::Map;

    // Seed an index so the server has a real .leankg to point at.
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg");
    std::fs::create_dir_all(&db_path).unwrap();
    let db = init_db(&db_path).expect("init_db must succeed");
    // init_db already creates code_elements via init_schema; insert one row.
    leankg::db::schema::run_script(
        &db,
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <- [['src/main.rs::main', 'function', 'main', 'src/main.rs', 1, 10, 'rust', null, null, null, '{}', 'local', 'procedural']] :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
        Default::default(),
    )
    .expect("code_elements put must succeed");
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

#[tokio::test]
async fn mcpserver_allows_read_tools_in_read_only_mode() {
    use leankg::db::schema::init_db;
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
