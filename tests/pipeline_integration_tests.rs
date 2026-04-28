use leankg::db::schema::init_db;
use leankg::graph::cache::QueryCache;
use leankg::graph::GraphEngine;
use leankg::indexer::extractor::EntityExtractor;
use leankg::indexer::parser::ParserManager;
use tempfile::TempDir;

#[tokio::test(flavor = "multi_thread")]
async fn test_full_ast_to_graph_pipeline() {
    let tmp = TempDir::new().unwrap();
    let db = init_db(tmp.path().join("full_pipe.db").as_path()).unwrap();
    let cache = QueryCache::new(60, 100);
    let graph = GraphEngine::with_cache(db, cache);

    // We create a dummy rust code file dynamically via AST extraction
    let source_code = r#"
        fn orchestrate() {
            start_engine();
            flush_cache();
        }
    "#;

    let mut parser_manager = ParserManager::new();
    parser_manager.init_parsers().unwrap();
    let parser = parser_manager.get_parser_for_language("rust").unwrap();
    let tree = parser.parse(source_code, None).unwrap();

    let extractor = EntityExtractor::new(source_code.as_bytes(), "src/main.rs", "rust");
    let (elements, relationships) = extractor.extract(&tree);

    // Source elements insert correctly
    graph.insert_elements(&elements).unwrap();
    graph.insert_relationships(&relationships).unwrap();

    // Graph Engine native logic verifier: tree sitter extracts raw calls as unresolved!
    let relations = graph
        .get_relationships_for_target("__unresolved__start_engine")
        .unwrap();
    assert_eq!(relations.len(), 1);
    assert_eq!(relations[0].source_qualified, "src/main.rs::orchestrate");
}

#[tokio::test(flavor = "multi_thread")]
async fn test_pipeline_reindex_overwrite() {
    let tmp = TempDir::new().unwrap();
    let db = init_db(tmp.path().join("reindex.db").as_path()).unwrap();
    let cache = QueryCache::new(60, 100);
    let graph = GraphEngine::with_cache(db, cache.clone());

    let mut parser_manager = ParserManager::new();
    parser_manager.init_parsers().unwrap();

    let source_v1 = r#"
        fn process() {
            v1_call();
        }
    "#;

    let tree_v1 = parser_manager
        .get_parser_for_language("rust")
        .unwrap()
        .parse(source_v1, None)
        .unwrap();
    let (elements_v1, rels_v1_extract) =
        EntityExtractor::new(source_v1.as_bytes(), "src/app.rs", "rust").extract(&tree_v1);

    graph.insert_elements(&elements_v1).unwrap();
    graph.insert_relationships(&rels_v1_extract).unwrap();

    let rels_v1 = graph.get_relationships("src/app.rs::process").unwrap();
    assert_eq!(rels_v1.len(), 1);
    assert_eq!(rels_v1[0].target_qualified, "__unresolved__v1_call");

    // Now simulate an overwrite by re-indexing!
    let source_v2 = r#"
        fn process() {
            v2_call();
        }
    "#;

    // Automatically trigger GraphEngine removals imitating pipeline logic
    graph.remove_elements_by_file("src/app.rs").unwrap();
    graph
        .remove_relationships_by_source("src/app.rs::process")
        .unwrap();

    let tree_v2 = parser_manager
        .get_parser_for_language("rust")
        .unwrap()
        .parse(source_v2, None)
        .unwrap();
    let (elements_v2, rels_v2_extract) =
        EntityExtractor::new(source_v2.as_bytes(), "src/app.rs", "rust").extract(&tree_v2);

    graph.insert_elements(&elements_v2).unwrap();
    graph.insert_relationships(&rels_v2_extract).unwrap();

    let rels_v2 = graph.get_relationships("src/app.rs::process").unwrap();
    assert_eq!(rels_v2.len(), 1);
    assert_eq!(
        rels_v2[0].target_qualified, "__unresolved__v2_call",
        "Re-indexing securely mapped the new AST edges!"
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn test_pipeline_project_structure() {
    let tmp = TempDir::new().unwrap();
    let db = init_db(tmp.path().join("structure.db").as_path()).unwrap();
    let cache = QueryCache::new(60, 100);
    let graph = GraphEngine::with_cache(db, cache);

    // We mock index_files_parallel process
    let files = vec!["src/app.rs".to_string(), "src/utils/math.rs".to_string()];

    let (structure_elements, structure_rels) =
        leankg::indexer::generate_physical_structure("leankg_repo", &files);

    assert_eq!(structure_elements.len(), 5); // 1 Project + 2 Folders + 2 Files
    assert!(structure_elements
        .iter()
        .any(|e| e.element_type == "Project" && e.qualified_name == "leankg_repo"));
    assert!(structure_elements
        .iter()
        .any(|e| e.element_type == "File" && e.qualified_name == "src/app.rs"));
    assert!(structure_elements
        .iter()
        .any(|e| e.element_type == "File" && e.qualified_name == "src/utils/math.rs"));
    assert!(structure_elements
        .iter()
        .any(|e| e.element_type == "Folder" && e.qualified_name == "src"));
    assert!(structure_elements
        .iter()
        .any(|e| e.element_type == "Folder" && e.qualified_name == "src/utils"));

    // root -> src, src -> utils, src -> app.rs, utils -> math.rs
    assert_eq!(structure_rels.len(), 4);

    graph.insert_elements(&structure_elements).unwrap();
    graph.insert_relationships(&structure_rels).unwrap();

    let project_nodes = graph.search_by_type("Project").unwrap();
    assert_eq!(project_nodes.len(), 1);

    let file_nodes = graph.search_by_type("File").unwrap();
    assert_eq!(file_nodes.len(), 2);
}

#[tokio::test(flavor = "multi_thread")]
async fn test_pipeline_execution_flow() {
    let tmp = TempDir::new().unwrap();
    let db = init_db(tmp.path().join("exec_flow.db").as_path()).unwrap();
    let cache = QueryCache::new(60, 100);
    let graph = GraphEngine::with_cache(db, cache);

    let mut all_elements = vec![
        leankg::db::models::CodeElement {
            qualified_name: "src/main.rs::main".to_string(),
            element_type: "function".to_string(),
            name: "main".to_string(),
            file_path: "src/main.rs".to_string(),
            ..Default::default()
        },
        leankg::db::models::CodeElement {
            qualified_name: "src/net.rs::handle_request".to_string(),
            element_type: "function".to_string(),
            name: "handle_request".to_string(),
            file_path: "src/net.rs".to_string(),
            ..Default::default()
        },
        leankg::db::models::CodeElement {
            qualified_name: "src/db.rs::save".to_string(),
            element_type: "function".to_string(),
            name: "save".to_string(),
            file_path: "src/db.rs".to_string(),
            ..Default::default()
        },
    ];

    let mut all_relationships = vec![
        leankg::db::models::Relationship {
            id: None,
            source_qualified: "src/main.rs::main".to_string(),
            target_qualified: "src/net.rs::handle_request".to_string(),
            rel_type: "calls".to_string(),
            confidence: 1.0,
            metadata: serde_json::json!({}),
        },
        leankg::db::models::Relationship {
            id: None,
            source_qualified: "src/net.rs::handle_request".to_string(),
            target_qualified: "src/db.rs::save".to_string(),
            rel_type: "calls".to_string(),
            confidence: 1.0,
            metadata: serde_json::json!({}),
        },
    ];

    let config = leankg::indexer::process_processor::ProcessConfig {
        max_trace_depth: 5,
        max_branching: 2,
        max_processes: 10,
        min_steps: 3,
    };

    let process_result = leankg::indexer::process_processor::detect_processes(
        &all_elements,
        &all_relationships,
        Some(config),
    );

    assert_eq!(
        process_result.process_elements.len(),
        1,
        "Should detect 1 process array"
    );
    assert_eq!(process_result.process_elements[0].element_type, "process");
    assert_eq!(
        process_result.process_elements[0].name,
        "Main \u{2192} Save"
    );

    all_elements.extend(process_result.process_elements);
    all_relationships.extend(process_result.process_relationships);

    // Test insertion to CozoDB works for the newly modified RelationshipTypes
    graph.insert_elements(&all_elements).unwrap();
    graph.insert_relationships(&all_relationships).unwrap();

    let processes_in_db = graph.search_by_type("process").unwrap();
    assert_eq!(processes_in_db.len(), 1);

    let step_rels = graph
        .get_relationships_for_target(&processes_in_db[0].qualified_name)
        .unwrap();

    // 3 steps + 1 entry_point_of = 4
    assert_eq!(step_rels.len(), 4);
    assert!(step_rels.iter().any(|r| r.rel_type == "entry_point_of"));
    assert!(step_rels.iter().any(|r| r.rel_type == "step_in_process"));
}

#[tokio::test(flavor = "multi_thread")]
async fn test_pipeline_config_and_frameworks() {
    let source_json = r#"{
        "dependencies": {
            "react": "^18.0.0",
            "next": "^13.0.0"
        }
    }"#;
    let config_extractor = leankg::indexer::ConfigExtractor::new(
        source_json.as_bytes(),
        "package.json",
        "package_json",
    );
    let (mut elements, mut relationships) = config_extractor.extract();

    assert_eq!(elements.len(), 1); // The package.json config file itself
    assert_eq!(relationships.len(), 2); // 2 dependencies

    // Now trigger framework detection
    let (fw_elements, fw_rels) =
        leankg::indexer::FrameworkDetector::detect_frameworks(&elements, &relationships);
    elements.extend(fw_elements);
    relationships.extend(fw_rels);

    // React and Next.js should both be detected
    let frameworks: Vec<_> = elements
        .iter()
        .filter(|e| e.element_type == "framework")
        .collect();
    assert_eq!(frameworks.len(), 2);

    let fw_names: Vec<_> = frameworks.iter().map(|e| e.name.as_str()).collect();
    assert!(fw_names.contains(&"React"));
    assert!(fw_names.contains(&"Next.js"));

    // Check uses_framework relationship
    assert_eq!(
        relationships
            .iter()
            .filter(|r| r.rel_type == "uses_framework")
            .count(),
        2
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn test_pipeline_constructor_inference() {
    let source_ts = r#"
        class ApiClient {
            constructor() {
                this.baseUrl = "http://localhost";
                this.timeout = 5000;
            }
        }
    "#;

    let mut parser_manager = ParserManager::new();
    parser_manager.init_parsers().unwrap();
    let parser = parser_manager
        .get_parser_for_language("typescript")
        .unwrap();
    let tree = parser.parse(source_ts, None).unwrap();

    let extractor = EntityExtractor::new(source_ts.as_bytes(), "api.ts", "typescript");
    let (elements, relationships) = extractor.extract(&tree);

    // Check if properties baseUrl and timeout were correctly inferred
    let properties: Vec<_> = elements
        .iter()
        .filter(|e| e.element_type == "property")
        .collect();
    assert_eq!(
        properties.len(),
        2,
        "Should infer 2 properties from this.assignments in constructor"
    );

    let prop_names: Vec<_> = properties.iter().map(|e| e.name.as_str()).collect();
    assert!(prop_names.contains(&"baseUrl"));
    assert!(prop_names.contains(&"timeout"));

    // Check their parent_qualified
    for p in properties {
        assert_eq!(p.parent_qualified.as_deref(), Some("ApiClient"));
    }
}
