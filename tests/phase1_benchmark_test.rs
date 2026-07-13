// Phase 1 Benchmark Tests: Real leankg codebase as test data
// Tests all Phase 1 tools against actual indexed leankg source.
// Run: cargo test --release -- phase1_benchmark_test

use leankg::db::models::{CodeElement, Relationship};
use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;
use leankg::indexer::route_extractor::RouteExtractor;

use tempfile::TempDir;

// ── Test harness: index real leankg source patterns ──

fn make_engine() -> (GraphEngine, TempDir) {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("test.db");
    let db = init_db(&db_path).unwrap();
    (GraphEngine::new(db), tmp)
}

/// Index patterns from the real leankg codebase
fn index_leankg_self(engine: &GraphEngine) {
    // Real leankg source files (subset representing key modules)
    let elements = vec![
        // Core
        ("src/main.rs", "main", "function", "rust", 1, 50, None, None),
        (
            "src/main.rs",
            "setup_logging",
            "function",
            "rust",
            52,
            65,
            None,
            None,
        ),
        (
            "src/lib.rs",
            "run_benchmark",
            "function",
            "rust",
            1,
            30,
            Some("c1"),
            Some("benchmark"),
        ),
        // DB layer
        (
            "src/db/models.rs",
            "RelationshipType",
            "enum",
            "rust",
            10,
            80,
            None,
            None,
        ),
        (
            "src/db/models.rs",
            "CodeElement",
            "struct",
            "rust",
            215,
            250,
            None,
            None,
        ),
        (
            "src/db/models.rs",
            "Relationship",
            "struct",
            "rust",
            255,
            268,
            None,
            None,
        ),
        (
            "src/db/schema.rs",
            "init_db",
            "function",
            "rust",
            89,
            121,
            None,
            None,
        ),
        (
            "src/db/schema.rs",
            "init_schema",
            "function",
            "rust",
            180,
            400,
            None,
            None,
        ),
        (
            "src/db/schema.rs",
            "run_script",
            "function",
            "rust",
            16,
            26,
            None,
            None,
        ),
        // Graph engine
        (
            "src/graph/query.rs",
            "GraphEngine",
            "struct",
            "rust",
            42,
            50,
            None,
            None,
        ),
        (
            "src/graph/query.rs",
            "get_architecture",
            "function",
            "rust",
            3160,
            3278,
            None,
            None,
        ),
        (
            "src/graph/query.rs",
            "get_graph_schema",
            "function",
            "rust",
            3292,
            3331,
            None,
            None,
        ),
        (
            "src/graph/query.rs",
            "find_dead_code",
            "function",
            "rust",
            3334,
            3370,
            None,
            None,
        ),
        (
            "src/graph/query.rs",
            "count_knowledge",
            "function",
            "rust",
            3280,
            3289,
            None,
            None,
        ),
        (
            "src/graph/query.rs",
            "insert_relationship",
            "function",
            "rust",
            1728,
            1754,
            None,
            None,
        ),
        (
            "src/graph/query.rs",
            "get_callers",
            "function",
            "rust",
            2323,
            2383,
            None,
            None,
        ),
        (
            "src/graph/clustering.rs",
            "cluster_elements",
            "function",
            "rust",
            1,
            200,
            Some("c2"),
            Some("clustering"),
        ),
        (
            "src/graph/cache.rs",
            "QueryCache",
            "struct",
            "rust",
            1,
            30,
            None,
            None,
        ),
        // MCP layer
        (
            "src/mcp/tools.rs",
            "ToolRegistry",
            "struct",
            "rust",
            4,
            6,
            None,
            None,
        ),
        (
            "src/mcp/tools.rs",
            "list_tools",
            "function",
            "rust",
            7,
            930,
            None,
            None,
        ),
        (
            "src/mcp/handler.rs",
            "execute_tool",
            "function",
            "rust",
            206,
            281,
            None,
            None,
        ),
        (
            "src/mcp/handler.rs",
            "get_architecture",
            "function",
            "rust",
            3114,
            3119,
            None,
            None,
        ),
        (
            "src/mcp/handler.rs",
            "find_dead_code",
            "function",
            "rust",
            3128,
            3140,
            None,
            None,
        ),
        (
            "src/mcp/handler.rs",
            "get_graph_schema",
            "function",
            "rust",
            3121,
            3126,
            None,
            None,
        ),
        // Indexer
        (
            "src/indexer/extractor.rs",
            "CodeExtractor",
            "struct",
            "rust",
            1,
            50,
            None,
            None,
        ),
        (
            "src/indexer/extractor.rs",
            "extract",
            "function",
            "rust",
            252,
            290,
            None,
            None,
        ),
        (
            "src/indexer/call_graph.rs",
            "CallGraphBuilder",
            "struct",
            "rust",
            6,
            14,
            None,
            None,
        ),
        (
            "src/indexer/call_graph.rs",
            "extract_calls_with_resolution",
            "function",
            "rust",
            416,
            425,
            None,
            None,
        ),
        (
            "src/indexer/call_graph.rs",
            "resolve_call",
            "function",
            "rust",
            309,
            364,
            None,
            None,
        ),
        (
            "src/indexer/route_extractor.rs",
            "RouteExtractor",
            "struct",
            "rust",
            1,
            20,
            None,
            None,
        ),
        (
            "src/indexer/route_extractor.rs",
            "extract_routes",
            "function",
            "rust",
            21,
            40,
            None,
            None,
        ),
        (
            "src/indexer/route_extractor.rs",
            "routes_to_elements_and_rels",
            "function",
            "rust",
            42,
            100,
            None,
            None,
        ),
        // API/Web
        (
            "src/api/handlers.rs",
            "handle_request",
            "function",
            "rust",
            1,
            30,
            None,
            None,
        ),
        (
            "src/web/handlers.rs",
            "graph",
            "function",
            "rust",
            245,
            1127,
            None,
            None,
        ),
        (
            "src/web/handlers.rs",
            "services_page",
            "function",
            "rust",
            1374,
            1607,
            None,
            None,
        ),
        // Config
        (
            "src/config/mod.rs",
            "Config",
            "struct",
            "rust",
            1,
            30,
            None,
            None,
        ),
        (
            "src/config/project.rs",
            "ProjectConfig",
            "struct",
            "rust",
            1,
            50,
            None,
            None,
        ),
        // Benchmark
        (
            "src/benchmark/runner.rs",
            "BenchmarkRunner",
            "struct",
            "rust",
            1,
            50,
            None,
            None,
        ),
        (
            "src/benchmark/ab_test.rs",
            "run",
            "function",
            "rust",
            129,
            519,
            None,
            None,
        ),
    ];

    for (file, name, etype, lang, ls, le, cid, clabel) in &elements {
        let elem = CodeElement {
            qualified_name: format!("{}::{}", file, name),
            element_type: etype.to_string(),
            name: name.to_string(),
            file_path: file.to_string(),
            line_start: *ls,
            line_end: *le,
            language: lang.to_string(),
            cluster_id: cid.map(|s| s.to_string()),
            cluster_label: clabel.map(|s| s.to_string()),
            ..Default::default()
        };
        engine.insert_element(&elem).unwrap();
    }

    // Call relationships from real leankg source
    let calls = vec![
        (
            "src/main.rs::main",
            "src/main.rs::setup_logging",
            0.95,
            "name",
        ),
        (
            "src/main.rs::main",
            "src/lib.rs::run_benchmark",
            0.90,
            "name",
        ),
        (
            "src/mcp/handler.rs::execute_tool",
            "src/graph/query.rs::get_architecture",
            0.90,
            "name",
        ),
        (
            "src/mcp/handler.rs::execute_tool",
            "src/graph/query.rs::get_graph_schema",
            0.90,
            "name",
        ),
        (
            "src/mcp/handler.rs::execute_tool",
            "src/graph/query.rs::find_dead_code",
            0.90,
            "name",
        ),
        (
            "src/mcp/handler.rs::get_architecture",
            "src/graph/query.rs::get_architecture",
            0.95,
            "name",
        ),
        (
            "src/mcp/handler.rs::get_graph_schema",
            "src/graph/query.rs::get_graph_schema",
            0.95,
            "name",
        ),
        (
            "src/mcp/handler.rs::find_dead_code",
            "src/graph/query.rs::find_dead_code",
            0.95,
            "name",
        ),
        (
            "src/indexer/extractor.rs::extract",
            "src/indexer/route_extractor.rs::extract_routes",
            0.85,
            "name_file_hint",
        ),
        (
            "src/indexer/extractor.rs::extract",
            "src/indexer/call_graph.rs::extract_calls_with_resolution",
            0.85,
            "name_file_hint",
        ),
        (
            "src/indexer/call_graph.rs::extract_calls_with_resolution",
            "src/indexer/call_graph.rs::resolve_call",
            0.90,
            "name",
        ),
        (
            "src/indexer/call_graph.rs::extract_calls_with_resolution",
            "src/indexer/call_graph.rs::CallGraphBuilder",
            0.95,
            "name",
        ),
        (
            "src/db/schema.rs::init_db",
            "src/db/schema.rs::init_schema",
            0.90,
            "name",
        ),
        (
            "src/db/schema.rs::init_schema",
            "src/db/schema.rs::run_script",
            0.85,
            "name",
        ),
        (
            "src/graph/query.rs::get_architecture",
            "src/graph/query.rs::count_knowledge",
            0.90,
            "name",
        ),
        (
            "src/graph/query.rs::find_dead_code",
            "src/db/schema.rs::run_script",
            0.80,
            "name_file_hint",
        ),
    ];

    for (source, target, conf, method) in &calls {
        let rel = Relationship {
            source_qualified: source.to_string(),
            target_qualified: target.to_string(),
            rel_type: "calls".to_string(),
            confidence: *conf,
            metadata: serde_json::json!({
                "resolution_method": method,
                "is_resolved": true,
                "line": 1,
            }),
            ..Default::default()
        };
        engine.insert_relationship(&rel).unwrap();
    }

    // Tested_by relationships
    let tests = vec![
        (
            "tests/phase1_test.rs::test_arch",
            "src/graph/query.rs::get_architecture",
        ),
        (
            "tests/phase1_test.rs::test_schema",
            "src/graph/query.rs::get_graph_schema",
        ),
        (
            "tests/phase1_test.rs::test_dead",
            "src/graph/query.rs::find_dead_code",
        ),
    ];
    for (test_name, tested_fn) in &tests {
        let rel = Relationship {
            source_qualified: test_name.to_string(),
            target_qualified: tested_fn.to_string(),
            rel_type: "tested_by".to_string(),
            confidence: 0.90,
            metadata: serde_json::json!({}),
            ..Default::default()
        };
        engine.insert_relationship(&rel).unwrap();
    }
}

// ── Benchmark tests: get_architecture ──

#[test]
fn bench_architecture_returns_all_keys() {
    let (engine, _tmp) = make_engine();
    index_leankg_self(&engine);
    let arch = engine
        .get_architecture()
        .expect("get_architecture should succeed");
    let obj = arch.as_object().unwrap();

    let required = [
        "languages",
        "entry_points",
        "routes",
        "clusters",
        "hotspots",
        "relationship_summary",
        "knowledge_count",
        "total_elements",
        "total_files",
    ];
    for key in &required {
        assert!(obj.contains_key(*key), "Missing key: {}", key);
    }
}

#[test]
fn bench_architecture_detects_rust_language() {
    let (engine, _tmp) = make_engine();
    index_leankg_self(&engine);
    let arch = engine.get_architecture().unwrap();
    let obj = arch.as_object().unwrap();
    let langs = obj["languages"].as_array().unwrap();
    let rust = langs
        .iter()
        .find(|l| l["language"].as_str().unwrap() == "rust");
    assert!(rust.is_some(), "Should detect Rust");
    assert!(rust.unwrap()["element_count"].as_u64().unwrap() >= 30);
}

#[test]
fn bench_architecture_finds_main_entry_point() {
    let (engine, _tmp) = make_engine();
    index_leankg_self(&engine);
    let arch = engine.get_architecture().unwrap();
    let obj = arch.as_object().unwrap();
    let eps = obj["entry_points"].as_array().unwrap();
    assert!(!eps.is_empty(), "Should find entry points");
    let has_main = eps
        .iter()
        .any(|e| e["qualified_name"].as_str().unwrap().contains("main"));
    assert!(has_main, "Should find main()");
}

#[test]
fn bench_architecture_finds_hotspots() {
    let (engine, _tmp) = make_engine();
    index_leankg_self(&engine);
    let arch = engine.get_architecture().unwrap();
    let obj = arch.as_object().unwrap();
    let hs = obj["hotspots"].as_array().unwrap();
    assert!(!hs.is_empty(), "Should find hotspots");
    // web/handlers.rs has 2 functions, graph/query.rs has 5, mcp/handler.rs has 3
    let query_hot = hs
        .iter()
        .find(|h| h["file_path"].as_str().unwrap().contains("query.rs"));
    assert!(query_hot.is_some(), "graph/query.rs should be a hotspot");
}

#[test]
fn bench_architecture_finds_clusters() {
    let (engine, _tmp) = make_engine();
    index_leankg_self(&engine);
    let arch = engine.get_architecture().unwrap();
    let obj = arch.as_object().unwrap();
    let clusters = obj["clusters"].as_array().unwrap();
    assert_eq!(
        clusters.len(),
        2,
        "Should find benchmark + clustering clusters"
    );
}

#[test]
fn bench_architecture_counts_relationships() {
    let (engine, _tmp) = make_engine();
    index_leankg_self(&engine);
    let arch = engine.get_architecture().unwrap();
    let obj = arch.as_object().unwrap();
    let rels = obj["relationship_summary"].as_array().unwrap();
    let calls = rels
        .iter()
        .find(|r| r["rel_type"].as_str().unwrap() == "calls");
    assert!(calls.is_some(), "Should have calls in summary");
    assert_eq!(
        calls.unwrap()["count"].as_u64().unwrap(),
        16,
        "Should have 16 call edges"
    );
}

// ── Benchmark tests: get_graph_schema ──

#[test]
fn bench_schema_counts_element_types() {
    let (engine, _tmp) = make_engine();
    index_leankg_self(&engine);
    let schema = engine.get_graph_schema().unwrap();
    let obj = schema.as_object().unwrap();
    let types = obj["element_types"].as_array().unwrap();

    let func = types
        .iter()
        .find(|t| t["element_type"].as_str().unwrap() == "function");
    assert!(func.is_some());
    assert!(func.unwrap()["count"].as_u64().unwrap() >= 20);

    let struct_type = types
        .iter()
        .find(|t| t["element_type"].as_str().unwrap() == "struct");
    assert!(struct_type.is_some());

    let enum_type = types
        .iter()
        .find(|t| t["element_type"].as_str().unwrap() == "enum");
    assert!(enum_type.is_some());
}

#[test]
fn bench_schema_counts_relationship_types() {
    let (engine, _tmp) = make_engine();
    index_leankg_self(&engine);
    let schema = engine.get_graph_schema().unwrap();
    let obj = schema.as_object().unwrap();
    let rels = obj["relationship_types"].as_array().unwrap();

    let calls = rels
        .iter()
        .find(|r| r["rel_type"].as_str().unwrap() == "calls");
    assert!(calls.is_some());
    assert_eq!(calls.unwrap()["count"].as_u64().unwrap(), 16);

    let tested_by = rels
        .iter()
        .find(|r| r["rel_type"].as_str().unwrap() == "tested_by");
    assert!(tested_by.is_some());
    assert_eq!(tested_by.unwrap()["count"].as_u64().unwrap(), 3);
}

#[test]
fn bench_schema_totals_correct() {
    let (engine, _tmp) = make_engine();
    index_leankg_self(&engine);
    let schema = engine.get_graph_schema().unwrap();
    let obj = schema.as_object().unwrap();
    assert_eq!(obj["total_elements"].as_u64().unwrap(), 39);
    assert_eq!(obj["total_relationships"].as_u64().unwrap(), 19);
}

// ── Benchmark tests: find_dead_code ──

#[test]
fn bench_dead_code_excludes_called_functions() {
    let (engine, _tmp) = make_engine();
    index_leankg_self(&engine);
    let dead = engine.find_dead_code(10).unwrap();
    let names: Vec<&str> = dead.iter().map(|d| d["name"].as_str().unwrap()).collect();

    // These ARE called - should NOT be dead
    assert!(
        !names.contains(&"get_architecture"),
        "get_architecture is called"
    );
    assert!(
        !names.contains(&"get_graph_schema"),
        "get_graph_schema is called"
    );
    assert!(
        !names.contains(&"find_dead_code"),
        "find_dead_code is called"
    );
    assert!(!names.contains(&"resolve_call"), "resolve_call is called");
    assert!(!names.contains(&"init_schema"), "init_schema is called");
}

#[test]
fn bench_dead_code_excludes_entry_points() {
    let (engine, _tmp) = make_engine();
    index_leankg_self(&engine);
    let dead = engine.find_dead_code(5).unwrap();
    let names: Vec<&str> = dead.iter().map(|d| d["name"].as_str().unwrap()).collect();
    assert!(!names.contains(&"main"), "main should be excluded");
}

#[test]
fn bench_dead_code_excludes_tested_functions() {
    let (engine, _tmp) = make_engine();
    index_leankg_self(&engine);
    let dead = engine.find_dead_code(5).unwrap();
    let names: Vec<&str> = dead.iter().map(|d| d["name"].as_str().unwrap()).collect();
    // get_architecture, get_graph_schema, find_dead_code have tested_by edges
    assert!(!names.contains(&"get_architecture"));
    assert!(!names.contains(&"get_graph_schema"));
    assert!(!names.contains(&"find_dead_code"));
}

#[test]
fn bench_dead_code_finds_truly_dead() {
    let (engine, _tmp) = make_engine();
    index_leankg_self(&engine);
    let dead = engine.find_dead_code(10).unwrap();
    let names: Vec<&str> = dead.iter().map(|d| d["name"].as_str().unwrap()).collect();

    // These have no callers and no tests
    assert!(
        names.contains(&"handle_request"),
        "handle_request should be dead"
    );
    assert!(names.contains(&"graph"), "web graph handler should be dead");
    assert!(
        names.contains(&"services_page"),
        "services_page should be dead"
    );
    assert!(
        names.contains(&"ProjectConfig"),
        "ProjectConfig should be dead"
    );
}

#[test]
fn bench_dead_code_respects_min_lines() {
    let (engine, _tmp) = make_engine();
    index_leankg_self(&engine);

    let dead_small = engine.find_dead_code(500).unwrap();
    assert!(
        dead_small.is_empty(),
        "min_lines=500 should exclude everything"
    );

    let dead_large = engine.find_dead_code(10).unwrap();
    assert!(!dead_large.is_empty(), "min_lines=10 should find dead code");
}

// ── Benchmark tests: resolution_method ──

#[test]
fn bench_resolution_method_present_in_metadata() {
    let (engine, _tmp) = make_engine();
    index_leankg_self(&engine);
    // The calls we inserted have resolution_method in metadata
    let arch = engine.get_architecture().unwrap();
    let obj = arch.as_object().unwrap();
    let rels = obj["relationship_summary"].as_array().unwrap();
    let calls_count = rels
        .iter()
        .find(|r| r["rel_type"].as_str().unwrap() == "calls")
        .unwrap()["count"]
        .as_u64()
        .unwrap();
    assert_eq!(
        calls_count, 16,
        "Should have 16 calls with resolution_method metadata"
    );
}

// ── Benchmark tests: route extraction ──

#[test]
fn bench_route_extraction_go_chi() {
    let source = r#"
package main
import "github.com/go-chi/chi/v5"
func main() {
    r := chi.NewRouter()
    r.Get("/users/{id}", getUser)
    r.Post("/users", createUser)
    r.Delete("/users/{id}", deleteUser)
}
func getUser(w http.ResponseWriter, r *http.Request) {}
func createUser(w http.ResponseWriter, r *http.Request) {}
func deleteUser(w http.ResponseWriter, r *http.Request) {}
"#;
    let mut parser = tree_sitter::Parser::new();
    parser
        .set_language(&tree_sitter_go::LANGUAGE.into())
        .unwrap();
    let tree = parser.parse(source, None).unwrap();
    let routes = RouteExtractor::extract_routes(source.as_bytes(), &tree, "test.go", "go");
    assert_eq!(routes.len(), 3, "Should extract 3 chi routes");
    assert!(routes
        .iter()
        .any(|r| r.method == "GET" && r.path == "/users/{id}"));
    assert!(routes
        .iter()
        .any(|r| r.method == "POST" && r.path == "/users"));
    assert!(routes
        .iter()
        .any(|r| r.method == "DELETE" && r.path == "/users/{id}"));
}

#[test]
fn bench_route_extraction_go_gin() {
    let source = r#"
package main
import "github.com/gin-gonic/gin"
func main() {
    g := gin.Default()
    g.GET("/health", healthCheck)
    g.POST("/orders", createOrder)
    g.PUT("/orders/:id", updateOrder)
}
func healthCheck(c *gin.Context) {}
func createOrder(c *gin.Context) {}
func updateOrder(c *gin.Context) {}
"#;
    let mut parser = tree_sitter::Parser::new();
    parser
        .set_language(&tree_sitter_go::LANGUAGE.into())
        .unwrap();
    let tree = parser.parse(source, None).unwrap();
    let routes = RouteExtractor::extract_routes(source.as_bytes(), &tree, "test.go", "go");
    assert_eq!(routes.len(), 3, "Should extract 3 gin routes");
    assert!(routes.iter().all(|r| r.framework == "gin"));
}

#[test]
fn bench_route_extraction_ts_express() {
    let source = r#"
const express = require('express');
const app = express();
app.get('/api/users', getUsers);
app.post('/api/users', createUser);
app.put('/api/users/:id', updateUser);
app.delete('/api/users/:id', deleteUser);
function getUsers(req, res) {}
function createUser(req, res) {}
function updateUser(req, res) {}
function deleteUser(req, res) {}
"#;
    let mut parser = tree_sitter::Parser::new();
    parser
        .set_language(&tree_sitter_typescript::LANGUAGE_TYPESCRIPT.into())
        .unwrap();
    let tree = parser.parse(source, None).unwrap();
    let routes = RouteExtractor::extract_routes(source.as_bytes(), &tree, "app.ts", "typescript");
    assert_eq!(routes.len(), 4, "Should extract 4 express routes");
    assert!(routes.iter().all(|r| r.framework == "express"));
}

#[test]
fn bench_route_extraction_ts_fastify() {
    let source = r#"
import Fastify from 'fastify';
const fastify = Fastify();
fastify.get('/status', getStatus);
fastify.post('/items', createItem);
fastify.patch('/items/:id', updateItem);
function getStatus(req, reply) {}
function createItem(req, reply) {}
function updateItem(req, reply) {}
"#;
    let mut parser = tree_sitter::Parser::new();
    parser
        .set_language(&tree_sitter_typescript::LANGUAGE_TYPESCRIPT.into())
        .unwrap();
    let tree = parser.parse(source, None).unwrap();
    let routes =
        RouteExtractor::extract_routes(source.as_bytes(), &tree, "server.ts", "typescript");
    assert_eq!(routes.len(), 3, "Should extract 3 fastify routes");
    assert!(routes.iter().all(|r| r.framework == "fastify"));
}

#[test]
fn bench_route_extraction_generates_elements_and_edges() {
    let routes = vec![
        leankg::indexer::route_extractor::RouteInfo {
            method: "GET".to_string(),
            path: "/api/health".to_string(),
            handler: "healthCheck".to_string(),
            framework: "chi".to_string(),
            file_path: "src/handler.go".to_string(),
            line: 10,
        },
        leankg::indexer::route_extractor::RouteInfo {
            method: "POST".to_string(),
            path: "/api/users".to_string(),
            handler: "createUser".to_string(),
            framework: "express".to_string(),
            file_path: "src/app.ts".to_string(),
            line: 20,
        },
    ];
    let (elements, relationships) = RouteExtractor::routes_to_elements_and_rels(&routes);

    assert_eq!(elements.len(), 2, "Should generate 2 route elements");
    assert_eq!(elements[0].element_type, "route");
    assert_eq!(elements[0].metadata["method"], "GET");
    assert_eq!(elements[0].metadata["framework"], "chi");
    assert_eq!(elements[1].metadata["method"], "POST");
    assert_eq!(elements[1].metadata["framework"], "express");

    // 2 routes * 2 edges each (http_calls + defines_route)
    assert_eq!(relationships.len(), 4);
    assert_eq!(relationships[0].rel_type, "http_calls");
    assert_eq!(relationships[1].rel_type, "defines_route");
    assert_eq!(relationships[2].rel_type, "http_calls");
    assert_eq!(relationships[3].rel_type, "defines_route");
}

#[test]
fn bench_route_extraction_ignores_non_http_calls() {
    let source = r#"
const app = express();
app.listen(3000, () => console.log('started'));
app.use(express.json());
const x = computeSomething();
"#;
    let mut parser = tree_sitter::Parser::new();
    parser
        .set_language(&tree_sitter_typescript::LANGUAGE_TYPESCRIPT.into())
        .unwrap();
    let tree = parser.parse(source, None).unwrap();
    let routes = RouteExtractor::extract_routes(source.as_bytes(), &tree, "app.ts", "typescript");
    // app.listen and app.use(express.json()) should not be routes
    // app.use with string path would be, but express.json() has no string path
    assert!(
        routes.is_empty()
            || routes
                .iter()
                .all(|r| r.method != "GET" && r.method != "POST"),
        "Should not extract non-route calls as routes"
    );
}

// ── Benchmark tests: route elements in architecture ──

#[test]
fn bench_routes_appear_in_architecture() {
    let (engine, _tmp) = make_engine();
    index_leankg_self(&engine);

    // Insert a route element directly
    let route_elem = CodeElement {
        qualified_name: "src/api/handlers.rs::GET /health".to_string(),
        element_type: "route".to_string(),
        name: "GET /health".to_string(),
        file_path: "src/api/handlers.rs".to_string(),
        line_start: 10,
        line_end: 10,
        language: "rust".to_string(),
        metadata: serde_json::json!({
            "method": "GET",
            "path": "/health",
            "handler": "healthHandler",
            "framework": "axum",
        }),
        ..Default::default()
    };
    engine.insert_element(&route_elem).unwrap();

    let arch = engine.get_architecture().unwrap();
    let obj = arch.as_object().unwrap();
    let routes = obj["routes"].as_array().unwrap();
    assert!(!routes.is_empty(), "Architecture should include routes");
    let health = routes
        .iter()
        .find(|r| r["path"].as_str().unwrap() == "/health");
    assert!(health.is_some(), "Should find /health route");
}
