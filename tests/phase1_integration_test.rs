// Phase 1 structural parity integration tests
// Tests using LeanKG codebase patterns as fixtures.
// Run with: cargo test --release -- phase1_integration_test

use leankg::db::models::{CodeElement, Relationship};
use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;
use tempfile::TempDir;

fn make_engine() -> (GraphEngine, TempDir) {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("test.db");
    let db = init_db(&db_path).unwrap();
    (GraphEngine::new(db), tmp)
}

fn populate_with_leankg_patterns(engine: &GraphEngine) {
    let elements = vec![
        ("src/main.rs", "main", "function", "rust", 1, 30),
        ("src/main.rs", "setup_logging", "function", "rust", 32, 40),
        ("src/lib.rs", "run_benchmark", "function", "rust", 1, 25),
        (
            "src/db/models.rs",
            "RelationshipType",
            "enum",
            "rust",
            10,
            72,
        ),
        (
            "src/db/models.rs",
            "CodeElement",
            "struct",
            "rust",
            215,
            250,
        ),
        ("src/db/schema.rs", "init_db", "function", "rust", 89, 121),
        (
            "src/db/schema.rs",
            "init_schema",
            "function",
            "rust",
            180,
            280,
        ),
        ("src/mcp/tools.rs", "ToolRegistry", "struct", "rust", 4, 6),
        ("src/mcp/tools.rs", "list_tools", "function", "rust", 7, 930),
        (
            "src/mcp/handler.rs",
            "execute_tool",
            "function",
            "rust",
            206,
            281,
        ),
        (
            "src/graph/query.rs",
            "get_architecture",
            "function",
            "rust",
            3160,
            3278,
        ),
        (
            "src/graph/query.rs",
            "get_graph_schema",
            "function",
            "rust",
            3292,
            3331,
        ),
        (
            "src/graph/query.rs",
            "find_dead_code",
            "function",
            "rust",
            3334,
            3370,
        ),
        (
            "src/graph/clustering.rs",
            "cluster_elements",
            "function",
            "rust",
            1,
            50,
        ),
        ("src/graph/cache.rs", "QueryCache", "struct", "rust", 1, 30),
        (
            "src/indexer/call_graph.rs",
            "CallGraphBuilder",
            "struct",
            "rust",
            6,
            14,
        ),
        (
            "src/indexer/call_graph.rs",
            "extract_calls_with_resolution",
            "function",
            "rust",
            416,
            425,
        ),
        (
            "src/indexer/extractor.rs",
            "extract_elements",
            "function",
            "rust",
            1,
            100,
        ),
        (
            "src/indexer/parser.rs",
            "parse_file",
            "function",
            "rust",
            1,
            50,
        ),
        (
            "src/api/handlers.rs",
            "handle_request",
            "function",
            "rust",
            1,
            30,
        ),
    ];

    for (file, name, etype, lang, ls, le) in &elements {
        let elem = CodeElement {
            qualified_name: format!("{}::{}", file, name),
            element_type: etype.to_string(),
            name: name.to_string(),
            file_path: file.to_string(),
            line_start: *ls,
            line_end: *le,
            language: lang.to_string(),
            cluster_id: Some(format!("cluster-{}", etype)),
            cluster_label: Some(etype.to_string()),
            ..Default::default()
        };
        engine.insert_element(&elem).unwrap();
    }

    let calls = vec![
        ("src/main.rs::main", "src/main.rs::setup_logging", 0.95),
        ("src/main.rs::main", "src/lib.rs::run_benchmark", 0.90),
        (
            "src/mcp/handler.rs::execute_tool",
            "src/graph/query.rs::get_architecture",
            0.90,
        ),
        (
            "src/mcp/handler.rs::execute_tool",
            "src/graph/query.rs::get_graph_schema",
            0.90,
        ),
        (
            "src/mcp/handler.rs::execute_tool",
            "src/graph/query.rs::find_dead_code",
            0.90,
        ),
        (
            "src/indexer/call_graph.rs::extract_calls_with_resolution",
            "src/indexer/call_graph.rs::CallGraphBuilder",
            0.95,
        ),
        (
            "src/indexer/extractor.rs::extract_elements",
            "src/indexer/parser.rs::parse_file",
            0.85,
        ),
        (
            "src/db/schema.rs::init_db",
            "src/db/schema.rs::init_schema",
            0.90,
        ),
    ];

    for (source, target, conf) in &calls {
        let rel = Relationship {
            source_qualified: source.to_string(),
            target_qualified: target.to_string(),
            rel_type: "calls".to_string(),
            confidence: *conf,
            metadata: serde_json::json!({"resolution_method": "name", "is_resolved": true, "line": 1}),
            ..Default::default()
        };
        engine.insert_relationship(&rel).unwrap();
    }

    let tests = vec![
        (
            "src/graph/query.rs::t1",
            "src/graph/query.rs::get_architecture",
        ),
        (
            "src/graph/query.rs::t2",
            "src/graph/query.rs::get_graph_schema",
        ),
        (
            "src/graph/query.rs::t3",
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

#[test]
fn test_get_architecture_on_leankg_patterns() {
    let (engine, _tmp) = make_engine();
    populate_with_leankg_patterns(&engine);
    let arch = engine
        .get_architecture()
        .expect("get_architecture should succeed");
    let obj = arch.as_object().unwrap();

    let langs = obj["languages"].as_array().unwrap();
    assert!(!langs.is_empty(), "Should detect at least one language");
    let rust_lang = langs
        .iter()
        .find(|l| l["language"].as_str().unwrap() == "rust");
    assert!(rust_lang.is_some(), "Should detect Rust");
    assert!(rust_lang.unwrap()["element_count"].as_u64().unwrap() > 0);

    let eps = obj["entry_points"].as_array().unwrap();
    let has_main = eps
        .iter()
        .any(|e| e["qualified_name"].as_str().unwrap().contains("main"));
    assert!(has_main, "Should find main as entry point");

    let hs = obj["hotspots"].as_array().unwrap();
    assert!(!hs.is_empty(), "Should find hotspots");

    let clusters = obj["clusters"].as_array().unwrap();
    assert!(!clusters.is_empty(), "Should find clusters");

    let rels = obj["relationship_summary"].as_array().unwrap();
    let calls_rel = rels
        .iter()
        .find(|r| r["rel_type"].as_str().unwrap() == "calls")
        .unwrap();
    assert_eq!(calls_rel["count"].as_u64().unwrap(), 8);

    assert_eq!(obj["total_elements"].as_u64().unwrap(), 20);
    assert!(obj["total_files"].as_u64().unwrap() > 0);
}

#[test]
fn test_get_graph_schema_on_leankg_patterns() {
    let (engine, _tmp) = make_engine();
    populate_with_leankg_patterns(&engine);
    let schema = engine
        .get_graph_schema()
        .expect("get_graph_schema should succeed");
    let obj = schema.as_object().unwrap();

    let types = obj["element_types"].as_array().unwrap();
    assert!(!types.is_empty());
    let func = types
        .iter()
        .find(|t| t["element_type"].as_str().unwrap() == "function");
    assert!(func.is_some());
    assert!(func.unwrap()["count"].as_u64().unwrap() >= 10);

    let struct_type = types
        .iter()
        .find(|t| t["element_type"].as_str().unwrap() == "struct");
    assert!(struct_type.is_some());
    let enum_type = types
        .iter()
        .find(|t| t["element_type"].as_str().unwrap() == "enum");
    assert!(enum_type.is_some());

    let rel_types = obj["relationship_types"].as_array().unwrap();
    let calls = rel_types
        .iter()
        .find(|t| t["rel_type"].as_str().unwrap() == "calls");
    assert!(calls.is_some());
    let tb = rel_types
        .iter()
        .find(|t| t["rel_type"].as_str().unwrap() == "tested_by");
    assert!(tb.is_some());

    assert_eq!(obj["total_elements"].as_u64().unwrap(), 20);
    assert_eq!(obj["total_relationships"].as_u64().unwrap(), 11);
}

#[test]
fn test_find_dead_code_on_leankg_patterns() {
    let (engine, _tmp) = make_engine();
    populate_with_leankg_patterns(&engine);
    let dead = engine.find_dead_code(5).unwrap();
    let dead_names: Vec<&str> = dead.iter().map(|d| d["name"].as_str().unwrap()).collect();

    assert!(
        !dead_names.contains(&"main"),
        "main should be excluded (entry point)"
    );
    assert!(
        !dead_names.contains(&"get_architecture"),
        "get_architecture should be excluded (called + tested)"
    );
    assert!(
        !dead_names.contains(&"find_dead_code"),
        "find_dead_code should be excluded (called + tested)"
    );
    assert!(
        !dead_names.contains(&"setup_logging"),
        "setup_logging should be excluded (called by main)"
    );
    assert!(
        !dead_names.contains(&"init_schema"),
        "init_schema should be excluded (called by init_db)"
    );
    assert!(
        !dead_names.contains(&"execute_tool"),
        "execute_tool should be excluded (calls many)"
    );

    // Some functions should be dead (no callers, no tests, > 5 lines)
    let has_dead = dead_names.contains(&"cluster_elements")
        || dead_names.contains(&"list_tools")
        || dead_names.contains(&"parse_file")
        || dead_names.contains(&"handle_request");
    assert!(
        has_dead,
        "Should find at least some dead functions, found: {:?}",
        dead_names
    );
}

#[test]
fn test_architecture_structure_contract() {
    let (engine, _tmp) = make_engine();
    let arch = engine.get_architecture().unwrap();
    let obj = arch.as_object().unwrap();
    for key in &[
        "languages",
        "entry_points",
        "routes",
        "clusters",
        "hotspots",
        "relationship_summary",
        "knowledge_count",
        "total_elements",
        "total_files",
    ] {
        assert!(obj.contains_key(*key), "Architecture missing key: {}", key);
    }
}

#[test]
fn test_graph_schema_structure_contract() {
    let (engine, _tmp) = make_engine();
    let schema = engine.get_graph_schema().unwrap();
    let obj = schema.as_object().unwrap();
    for key in &[
        "element_types",
        "relationship_types",
        "total_elements",
        "total_relationships",
    ] {
        assert!(obj.contains_key(*key), "Schema missing key: {}", key);
    }
}

#[test]
fn test_resolution_method_in_metadata() {
    let (engine, _tmp) = make_engine();
    let elem = CodeElement {
        qualified_name: "src/handler.rs::process".to_string(),
        element_type: "function".to_string(),
        name: "process".to_string(),
        file_path: "src/handler.rs".to_string(),
        line_start: 10,
        line_end: 30,
        language: "rust".to_string(),
        ..Default::default()
    };
    engine.insert_element(&elem).unwrap();

    let elem2 = CodeElement {
        qualified_name: "src/utils.rs::validate".to_string(),
        element_type: "function".to_string(),
        name: "validate".to_string(),
        file_path: "src/utils.rs".to_string(),
        line_start: 1,
        line_end: 15,
        language: "rust".to_string(),
        ..Default::default()
    };
    engine.insert_element(&elem2).unwrap();

    let rel = Relationship {
        source_qualified: "src/handler.rs::process".to_string(),
        target_qualified: "src/utils.rs::validate".to_string(),
        rel_type: "calls".to_string(),
        confidence: 0.95,
        metadata: serde_json::json!({"resolution_method": "typed", "is_resolved": true}),
        ..Default::default()
    };
    engine.insert_relationship(&rel).unwrap();

    let callers = engine
        .get_callers("validate", Some("src/utils.rs"))
        .unwrap();
    assert!(!callers.is_empty(), "Should find callers");
}
