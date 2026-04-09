use leankg::db::schema::init_db;
use leankg::graph::cache::QueryCache;
use leankg::graph::GraphEngine;
use leankg::indexer::parser::ParserManager;
use leankg::indexer::extractor::EntityExtractor;
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
    let relations = graph.get_relationships_for_target("__unresolved__start_engine").unwrap();
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
    
    let tree_v1 = parser_manager.get_parser_for_language("rust").unwrap().parse(source_v1, None).unwrap();
    let (elements_v1, rels_v1_extract) = EntityExtractor::new(source_v1.as_bytes(), "src/app.rs", "rust").extract(&tree_v1);
    
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
    graph.remove_relationships_by_source("src/app.rs::process").unwrap();
    
    let tree_v2 = parser_manager.get_parser_for_language("rust").unwrap().parse(source_v2, None).unwrap();
    let (elements_v2, rels_v2_extract) = EntityExtractor::new(source_v2.as_bytes(), "src/app.rs", "rust").extract(&tree_v2);
    
    graph.insert_elements(&elements_v2).unwrap();
    graph.insert_relationships(&rels_v2_extract).unwrap();
    
    let rels_v2 = graph.get_relationships("src/app.rs::process").unwrap();
    assert_eq!(rels_v2.len(), 1);
    assert_eq!(rels_v2[0].target_qualified, "__unresolved__v2_call", "Re-indexing securely mapped the new AST edges!");
}
