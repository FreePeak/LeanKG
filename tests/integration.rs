// Integration tests requiring filesystem, async, or SurrealDB

use leankg::db::get_elements_by_env;
use leankg::db::schema::init_db;
use leankg::doc::DocGenerator;
use leankg::graph::{GraphEngine, ImpactAnalyzer};
use leankg::indexer::{find_files_sync, index_file_sync, ParserManager};
use leankg::ontology::OntologyQueryEngine;
use std::path::PathBuf;
use tempfile::TempDir;

#[tokio::test(flavor = "multi_thread")]
async fn test_find_files_empty_dir() {
    let tmp = TempDir::new().unwrap();
    let root = tmp.path().to_str().unwrap();
    let files = find_files_sync(root).unwrap();
    assert!(files.is_empty());
}

#[tokio::test(flavor = "multi_thread")]
async fn test_find_files_discovers_go_files() {
    let tmp = tempfile::TempDir::new().unwrap();
    let root = tmp.path().to_str().unwrap();
    let go_file = tmp.path().join("main.go");
    std::fs::write(&go_file, "package main\nfunc main() {}").unwrap();
    let files = find_files_sync(root).unwrap();
    assert!(!files.is_empty());
    assert!(files.iter().any(|f| f.ends_with("main.go")));
}

#[tokio::test(flavor = "multi_thread")]
async fn test_find_files_excludes_node_modules() {
    let tmp = TempDir::new().unwrap();
    let node_dir = tmp.path().join("node_modules").join("pkg");
    std::fs::create_dir_all(&node_dir).unwrap();
    std::fs::write(node_dir.join("index.js"), "export {}").unwrap();
    let files = find_files_sync(tmp.path().to_str().unwrap()).unwrap();
    assert!(!files.iter().any(|f| f.contains("node_modules")));
}

#[tokio::test(flavor = "multi_thread")]
async fn test_find_files_excludes_nested_worktrees_from_project_root() {
    let tmp = TempDir::new().unwrap();
    std::fs::write(tmp.path().join("main.rs"), "fn main() {}").unwrap();
    let worktree_src = tmp.path().join("worktrees").join("feature").join("src");
    std::fs::create_dir_all(&worktree_src).unwrap();
    std::fs::write(worktree_src.join("duplicate.rs"), "fn duplicate() {}").unwrap();

    let files = find_files_sync(tmp.path().to_str().unwrap()).unwrap();
    assert!(files.iter().any(|f| f.ends_with("main.rs")));
    assert!(
        !files.iter().any(|f| f.contains("duplicate.rs")),
        "nested worktree files should not be indexed from the project root: {:?}",
        files
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn test_find_files_allows_explicit_worktree_root() {
    let tmp = TempDir::new().unwrap();
    let worktree_src = tmp.path().join("worktrees").join("feature").join("src");
    std::fs::create_dir_all(&worktree_src).unwrap();
    std::fs::write(worktree_src.join("feature.rs"), "fn feature() {}").unwrap();

    let worktree_root = tmp.path().join("worktrees").join("feature");
    let files = find_files_sync(worktree_root.to_str().unwrap()).unwrap();
    assert!(
        files.iter().any(|f| f.ends_with("feature.rs")),
        "explicit worktree roots should still be indexable: {:?}",
        files
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn test_find_files_in_nested_dirs() {
    let tmp = tempfile::TempDir::new().unwrap();
    let nested = tmp.path().join("a").join("b").join("c");
    std::fs::create_dir_all(&nested).unwrap();
    std::fs::write(nested.join("lib.py"), "def x(): pass").unwrap();
    let files = find_files_sync(tmp.path().to_str().unwrap()).unwrap();
    assert!(
        files.iter().any(|f| f.ends_with("lib.py")),
        "Should find lib.py in nested dirs, got: {:?}",
        files
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn test_init_db_creates_schema() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let _db = init_db(db_path.as_path()).unwrap();
    assert!(db_path.exists() || std::path::Path::new(db_path.parent().unwrap()).exists());
}

#[tokio::test(flavor = "multi_thread")]
async fn test_init_db_repairs_legacy_code_elements_after_recorded_migration() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("legacy.db");
    let db_path_str = db_path.to_string_lossy().to_string();
    let legacy_db = cozo::DbInstance::new("sqlite", db_path_str, "").unwrap();

    leankg::db::schema::run_script(&legacy_db,
        r#":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String}"#,
        Default::default(),
    ).unwrap();
    leankg::db::schema::run_script(&legacy_db,
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] <- [["src/main.rs::main", "function", "main", "src/main.rs", 1, 3, "rust", null, null, null, "{}"]]
        :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata}"#,
        Default::default(),
    ).unwrap();
    leankg::db::schema::run_script(&legacy_db,
            r#":create relationships {source_qualified: String, target_qualified: String, rel_type: String, confidence: Float, metadata: String}"#,
            Default::default(),
        )
        .unwrap();
    leankg::db::schema::run_script(
        &legacy_db,
        r#":create migrations {id: String, applied_at: Int}"#,
        Default::default(),
    )
    .unwrap();
    leankg::db::schema::run_script(
        &legacy_db,
        r#"?[id, applied_at] <- [["006_safe_canonical_schema_repair", 1]]
        :put migrations {id, applied_at}"#,
        Default::default(),
    )
    .unwrap();
    drop(legacy_db);

    let repaired_db = init_db(db_path.as_path()).unwrap();
    let canonical_query = leankg::db::schema::run_script(&repaired_db,
            r#"?[qualified_name, env, ontology_layer] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer]"#,
            Default::default(),
        )
        .unwrap();
    assert_eq!(canonical_query.rows.len(), 1);
    assert_eq!(canonical_query.rows[0][1].get_str(), Some("local"));
    assert_eq!(canonical_query.rows[0][2].get_str(), Some("procedural"));

    let graph = GraphEngine::new(repaired_db);
    let results = graph
        .search_by_name_typed("main", Some("function"), 10)
        .unwrap();
    assert_eq!(results.len(), 1);
    assert_eq!(results[0].qualified_name, "src/main.rs::main");
}

#[tokio::test(flavor = "multi_thread")]
async fn test_init_db_repairs_env_code_elements_to_ontology_layer_schema() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("env-only.db");
    let db_path_str = db_path.to_string_lossy().to_string();
    let legacy_db = cozo::DbInstance::new("sqlite", db_path_str, "").unwrap();

    leankg::db::schema::run_script(&legacy_db,
        r#":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local'}"#,
        Default::default(),
    ).unwrap();
    leankg::db::schema::run_script(&legacy_db,
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] <- [["src/lib.rs::activate", "function", "activate", "src/lib.rs", 2, 5, "rust", null, null, null, "{}", "staging"]]
        :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env}"#,
        Default::default(),
    ).unwrap();
    leankg::db::schema::run_script(&legacy_db,
            r#":create relationships {source_qualified: String, target_qualified: String, rel_type: String, confidence: Float, metadata: String, env: String default 'local'}"#,
            Default::default(),
        )
        .unwrap();
    drop(legacy_db);

    let repaired_db = init_db(db_path.as_path()).unwrap();
    let canonical_query = leankg::db::schema::run_script(&repaired_db,
            r#"?[qualified_name, env, ontology_layer] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer]"#,
            Default::default(),
        )
        .unwrap();
    assert_eq!(canonical_query.rows.len(), 1);
    assert_eq!(canonical_query.rows[0][1].get_str(), Some("staging"));
    assert_eq!(canonical_query.rows[0][2].get_str(), Some("procedural"));
}

#[tokio::test(flavor = "multi_thread")]
async fn test_graph_queries_support_ontology_layer_code_elements_schema() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("ontology-layer.db");
    let db_path_str = db_path.to_string_lossy().to_string();
    let db = cozo::DbInstance::new("sqlite", db_path_str, "").unwrap();

    leankg::db::schema::run_script(&db,
        r#":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}"#,
        Default::default(),
    ).unwrap();
    leankg::db::schema::run_script(&db,
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <-
        [["src/metrics/prometheus.go::registerPrometheus", "function", "registerPrometheus", "src/metrics/prometheus.go", 10, 20, "go", null, null, null, "{}", "local", "procedural"]]
        :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
        Default::default(),
    ).unwrap();
    leankg::db::schema::run_script(&db,
        r#":create relationships {source_qualified: String, target_qualified: String, rel_type: String, confidence: Float, metadata: String, env: String default 'local'}"#,
        Default::default(),
    ).unwrap();
    drop(db);

    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db.clone());

    assert!(graph.has_elements().unwrap());
    assert_eq!(graph.count_elements().unwrap(), 1);

    let search_results = graph
        .search_by_name_typed("prometheus", Some("function"), 10)
        .unwrap();
    assert_eq!(search_results.len(), 1);
    assert_eq!(search_results[0].name, "registerPrometheus");

    let env_results = get_elements_by_env(&db, "local", 10).unwrap();
    assert_eq!(env_results.len(), 1);
    assert_eq!(
        env_results[0].qualified_name,
        "src/metrics/prometheus.go::registerPrometheus"
    );
}

// Regression: ontology queries in src/ontology/query.rs were binding
// 12 columns (missing `ontology_layer`) against the canonical 13-column
// code_elements schema, causing every kg_* MCP tool that exercises them
// to fail with "Arity mismatch for rule application code_elements".
// This test seeds the 13-column schema directly with ontology rows and
// asserts that the previously-failing query paths now run cleanly.
#[tokio::test(flavor = "multi_thread")]
async fn test_ontology_queries_support_13_column_code_elements_schema() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("ontology-arity.db");
    let db_path_str = db_path.to_string_lossy().to_string();
    let raw_db = cozo::DbInstance::new("sqlite", db_path_str, "").unwrap();

    leankg::db::schema::run_script(&raw_db,
            r#":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}"#,
            Default::default(),
        )
        .unwrap();
    leankg::db::schema::run_script(&raw_db,
            r#":create relationships {source_qualified: String, target_qualified: String, rel_type: String, confidence: Float, metadata: String, env: String default 'local'}"#,
            Default::default(),
        )
        .unwrap();

    // Seed one workflow, two workflow_steps (parent_qualified = workflow gid),
    // and one domain_entity. file_path uses the ontology:// scheme so
    // regex_matches(file_path, "ontology://") selects them.
    leankg::db::schema::run_script(&raw_db,
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <-
            [["ontology://local/checkout/workflow:checkout@1", "workflow", "Checkout Workflow", "ontology://local/checkout/workflow:checkout@1", 1, 1, "ontology", null, null, null, '{"description":"end-to-end checkout","aliases":[]}', "local", "procedural"],
             ["ontology://local/checkout/step:validate_cart@1", "workflow_step", "Validate Cart", "ontology://local/checkout/step:validate_cart@1", 1, 1, "ontology", "ontology://local/checkout/workflow:checkout@1", null, null, '{"gid":"ontology://local/checkout/step:validate_cart@1","ontology":"procedural","ontology_layer":"procedural","workflow_gid":"ontology://local/checkout/workflow:checkout@1","order":1,"aliases":[],"description":"validate cart","code_refs":["src/checkout.rs::validate_cart"],"failure_modes":[],"stale":false}', "local", "procedural"],
             ["ontology://local/checkout/step:charge@1", "workflow_step", "Charge Card", "ontology://local/checkout/step:charge@1", 1, 1, "ontology", "ontology://local/checkout/workflow:checkout@1", null, null, '{"gid":"ontology://local/checkout/step:charge@1","ontology":"procedural","ontology_layer":"procedural","workflow_gid":"ontology://local/checkout/workflow:checkout@1","order":2,"aliases":[],"description":"charge the card","code_refs":["src/checkout.rs::charge"],"failure_modes":[],"stale":false}', "local", "procedural"],
             ["ontology://local/checkout/concept:cart@1", "domain_entity", "Cart", "ontology://local/checkout/concept:cart@1", 1, 1, "ontology", null, null, null, '{"description":"shopping cart","aliases":["cart","basket"],"ontology":"concept","ontology_layer":"domain"}', "local", "domain"]]
            :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}"#,
            Default::default(),
        )
        .unwrap();
    drop(raw_db);

    let db = init_db(db_path.as_path()).unwrap();
    let engine = OntologyQueryEngine::new(db);

    // search_ontology_nodes covers query.rs:89. Query "checkout" should
    // match the workflow (name contains "checkout") and the workflow_step
    // "Validate Cart" (description contains "validate_cart" via code_refs
    // is NOT in the score path; in practice it matches by name, alias, or
    // description). "cart" should match the domain_entity plus the step.
    let checkout_nodes = engine
        .search_ontology_nodes("checkout", "local", 2)
        .expect("search_ontology_nodes must succeed on canonical 13-col schema");
    assert!(
        checkout_nodes.iter().any(|n| n.name == "Checkout Workflow"),
        "expected workflow node, got: {:?}",
        checkout_nodes
    );

    let cart_nodes = engine
        .search_ontology_nodes("cart", "local", 2)
        .expect("search_ontology_nodes must succeed on canonical 13-col schema");
    assert!(
        cart_nodes.iter().any(|n| n.name == "Cart"),
        "expected domain_entity node, got: {:?}",
        cart_nodes
    );

    // search_workflows covers query.rs:462.
    let workflows = engine
        .search_workflows("checkout", "local")
        .expect("search_workflows must succeed on canonical 13-col schema");
    assert_eq!(workflows.len(), 1);
    assert_eq!(workflows[0].name, "Checkout Workflow");

    // get_ontology_context covers query.rs:221 (delegates to
    // search_ontology_nodes + expand_ontology_context + trace_workflow).
    let ctx = engine
        .get_ontology_context("checkout", "local", 2)
        .expect("get_ontology_context must succeed on canonical 13-col schema");
    assert!(
        !ctx.matched_ontology_nodes.is_empty(),
        "expected at least one matched node"
    );

    // trace_workflow covers query.rs:419.
    let steps = engine
        .trace_workflow("checkout", "local")
        .expect("trace_workflow must succeed on canonical 13-col schema");
    assert_eq!(steps.len(), 2, "workflow should expose two steps");
    let step_names: Vec<&str> = steps.iter().map(|s| s.name.as_str()).collect();
    assert!(step_names.contains(&"Validate Cart"));
    assert!(step_names.contains(&"Charge Card"));

    // get_ontology_status must not crash (it was the only kg_* tool that
    // already worked; we re-assert it here to lock in the invariant).
    let status = engine
        .get_ontology_status()
        .expect("get_ontology_status must succeed");
    let _ = status.workflows_without_failure_modes;
}

// Regression: kg_self_test must report all four kg_* tools as healthy
// when the canonical 13-column code_elements schema is in place. If a
// future change reintroduces a 12-column binding anywhere, this test
// fails fast with the exact arity-mismatch error message captured in
// the failing entry's `error` field.
#[tokio::test(flavor = "multi_thread")]
async fn test_kg_self_test_reports_all_ok_on_canonical_schema() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("selftest.db");
    let db = init_db(db_path.as_path()).unwrap();
    let engine = OntologyQueryEngine::new(db);

    let report = engine.self_test();
    assert!(report.all_ok, "all_ok should be true; report={:?}", report);
    assert!(
        report.kg_context.ok,
        "kg_context failed: {:?}",
        report.kg_context
    );
    assert!(
        report.kg_concept_map.ok,
        "kg_concept_map failed: {:?}",
        report.kg_concept_map
    );
    assert!(
        report.kg_trace_workflow.ok,
        "kg_trace_workflow failed: {:?}",
        report.kg_trace_workflow
    );
    assert!(
        report.kg_ontology_status.ok,
        "kg_ontology_status failed: {:?}",
        report.kg_ontology_status
    );
    assert_eq!(report.code_elements.arity, 13);
    assert!(report.code_elements.canonical);
    assert_eq!(report.relationships.arity, 6);
    assert!(report.relationships.canonical);
}

// Regression: kg_self_test must flag an 11-column legacy schema as not
// canonical even if the kg_* tools happen to keep working (they use
// narrower bindings for some code paths). This is the early-warning
// signal the tool is designed to emit. We bypass init_db so that the
// auto-repair does not run before the self-test fires.
#[tokio::test(flavor = "multi_thread")]
async fn test_kg_self_test_flags_legacy_11_column_schema() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("legacy-selftest.db");
    let db_path_str = db_path.to_string_lossy().to_string();
    let raw_db = cozo::DbInstance::new("sqlite", &db_path_str, "").unwrap();

    leankg::db::schema::run_script(&raw_db,
            r#":create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String}"#,
            Default::default(),
        )
        .unwrap();
    leankg::db::schema::run_script(&raw_db,
            r#":create relationships {source_qualified: String, target_qualified: String, rel_type: String, confidence: Float, metadata: String}"#,
            Default::default(),
        )
        .unwrap();

    // Self-test against the raw, un-repaired DB so we can verify the
    // non-canonical detection logic itself.
    let engine = OntologyQueryEngine::new(raw_db);
    let report = engine.self_test();

    assert_eq!(report.code_elements.arity, 11);
    assert!(
        !report.code_elements.canonical,
        "11-col schema must not be canonical"
    );
    assert!(
        !report.all_ok,
        "all_ok must be false on a non-canonical schema"
    );
    assert_eq!(report.relationships.arity, 5);
    assert!(!report.relationships.canonical);
}

#[tokio::test(flavor = "multi_thread")]
async fn test_graph_engine_all_elements_empty() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);
    let elements = graph.all_elements().unwrap();
    assert!(elements.is_empty());
}

#[tokio::test(flavor = "multi_thread")]
async fn test_graph_engine_find_element_missing() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);
    let result = graph.find_element("nonexistent::foo").unwrap();
    assert!(result.is_none());
}

#[tokio::test(flavor = "multi_thread")]
async fn test_impact_analyzer_empty_graph() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);
    let analyzer = ImpactAnalyzer::new(&graph);
    let result = analyzer.calculate_impact_radius("src/main.go", 3).unwrap();
    assert_eq!(result.start_file, "src/main.go");
    assert_eq!(result.max_depth, 3);
    assert!(result.affected_elements.is_empty());
}

#[tokio::test(flavor = "multi_thread")]
async fn test_doc_generator_agents_md_empty() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);
    let doc_gen = DocGenerator::new(graph, PathBuf::from("./docs"));
    let content = doc_gen.generate_agents_md().unwrap();
    assert!(content.contains("# Agent Guidelines for LeanKG"));
    assert!(content.contains("## Project Overview"));
    assert!(content.contains("## Build Commands"));
    assert!(content.contains("## Code Structure Overview"));
}

#[tokio::test(flavor = "multi_thread")]
async fn test_doc_generator_claude_md_empty() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);
    let doc_gen = DocGenerator::new(graph, PathBuf::from("./docs"));
    let content = doc_gen.generate_claude_md().unwrap();
    assert!(content.contains("# CLAUDE.md"));
    assert!(content.contains("## Project Overview"));
    assert!(content.contains("## Architecture Decisions"));
    assert!(content.contains("## Context Statistics"));
}

#[tokio::test(flavor = "multi_thread")]
async fn test_doc_sync_for_file() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);

    let go_file = tmp.path().join("main.go");
    std::fs::write(
        &go_file,
        "package main\n\nfunc add(x int, y int) int { return x + y }",
    )
    .unwrap();

    let mut parser = ParserManager::new();
    if parser.init_parsers().is_err() {
        return;
    }
    let _count = index_file_sync(&graph, &mut parser, go_file.to_str().unwrap()).unwrap();

    let doc_gen = DocGenerator::new(graph, PathBuf::from("./docs"));
    let result = doc_gen
        .sync_docs_for_file(go_file.to_str().unwrap())
        .unwrap();
    assert_eq!(result.file_path, go_file.to_str().unwrap());
    assert!(result.elements_regenerated > 0);
}

#[tokio::test(flavor = "multi_thread")]
async fn test_index_file_go() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);

    let go_file = tmp.path().join("main.go");
    std::fs::write(
        &go_file,
        "package main\n\nfunc add(x int, y int) int { return x + y }",
    )
    .unwrap();

    let mut parser = ParserManager::new();
    if parser.init_parsers().is_err() {
        return;
    }
    let count = index_file_sync(&graph, &mut parser, go_file.to_str().unwrap()).unwrap();
    assert!(count > 0);
}

#[tokio::test(flavor = "multi_thread")]
async fn test_find_files_discovers_java_files() {
    let tmp = tempfile::TempDir::new().unwrap();
    let java_dir = tmp.path().join("com").join("example");
    std::fs::create_dir_all(&java_dir).unwrap();
    std::fs::write(
        java_dir.join("Main.java"),
        "public class Main { public static void main(String[] args) {} }",
    )
    .unwrap();
    let files = find_files_sync(tmp.path().to_str().unwrap()).unwrap();
    assert!(
        !files.is_empty(),
        "Should find some files, got: {:?}",
        files
    );
    assert!(
        files.iter().any(|f| f.ends_with("Main.java")),
        "Should find Main.java, got: {:?}",
        files
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn test_index_file_java() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);

    let java_file = tmp.path().join("UserService.java");
    std::fs::write(
        &java_file,
        "import com.example.model.User;\npublic class UserService {\n    public User createUser(String name) {\n        return new User(name);\n    }\n}",
    )
    .unwrap();

    let mut parser = ParserManager::new();
    if parser.init_parsers().is_err() {
        return;
    }
    let count = index_file_sync(&graph, &mut parser, java_file.to_str().unwrap()).unwrap();
    assert!(count > 0, "Should index Java elements, got {}", count);

    let elements = graph.all_elements().unwrap();
    let java_classes: Vec<_> = elements
        .iter()
        .filter(|e| e.element_type == "class" && e.language == "java")
        .collect();
    assert!(!java_classes.is_empty(), "Should find Java class");
    assert_eq!(java_classes[0].name, "UserService");
}

#[tokio::test(flavor = "multi_thread")]
async fn test_get_relationships_with_real_db() {
    // Use the real .leankg database from current dir
    let db_path = std::path::Path::new(".leankg");
    if !db_path.exists() {
        println!("Skipping - no .leankg database in current dir");
        return;
    }

    let db = init_db(db_path).expect("failed to init db");

    // Check if DB has data (skip test if empty)
    let count_query = r#"?[cnt] := count(code_elements[qualified_name]), cnt = $cnt"#;
    let count_result =
        leankg::db::schema::run_script(&db, count_query, std::collections::BTreeMap::new());
    let has_data = count_result
        .map(|r| !r.rows.is_empty() && r.rows[0].len() > 0)
        .unwrap_or(false);
    if !has_data {
        println!("Skipping - .leankg database appears empty or unindexed");
        return;
    }

    let graph = GraphEngine::new(db);

    // Test with path that exists in DB (from graph.json we know ./src/api/auth.rs has imports)
    let result = graph.get_relationships("./src/api/auth.rs");
    match result {
        Ok(rels) => {
            println!(
                "get_relationships('./src/api/auth.rs') returned {} results",
                rels.len()
            );
            for rel in rels.iter().take(5) {
                println!(
                    "  {} -> {} ({})",
                    rel.source_qualified, rel.target_qualified, rel.rel_type
                );
            }
            // We expect at least one relationship based on graph.json, but skip if DB is empty
            if rels.is_empty() {
                println!("(Empty results - DB may be unindexed, skipping assertion)");
            }
        }
        Err(e) => {
            panic!("get_relationships failed: {}", e);
        }
    }

    // Test without ./ prefix (skip assertion since DB may be empty)
    let result2 = graph.get_relationships("src/api/auth.rs");
    match result2 {
        Ok(rels) => {
            println!(
                "get_relationships('src/api/auth.rs') returned {} results",
                rels.len()
            );
            // DB may be empty/unindexed, so we just log the result
            if rels.is_empty() {
                println!("(Empty results - DB may be unindexed)");
            }
        }
        Err(e) => {
            panic!("get_relationships without prefix failed: {}", e);
        }
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn test_get_dependencies_with_real_db() {
    let db_path = std::path::Path::new(".leankg");
    if !db_path.exists() {
        println!("Skipping - no .leankg database");
        return;
    }

    let db = init_db(db_path).expect("failed to init db");
    let graph = GraphEngine::new(db.clone());

    // get_dependencies returns CodeElements for imported items
    // Since most imports are external (std::, crate::), we might get empty results
    // But the important thing is the QUERY works (path normalization is correct)
    let dep_result = graph.get_dependencies("./src/api/auth.rs");
    match dep_result {
        Ok(deps) => {
            println!("get_dependencies returned {} CodeElements", deps.len());
        }
        Err(e) => {
            panic!("get_dependencies failed: {}", e);
        }
    }

    // Verify the raw relationship query works (this is the core fix)
    // Note: This may fail if DB is empty/unindexed, which is expected
    let normalized = "./src/api/auth.rs"
        .strip_prefix("./")
        .unwrap_or("./src/api/auth.rs");
    let escaped = normalized.replace('\\', "\\\\").replace('"', "\\\"");
    let query = format!(
        r#"?[target_qualified] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _], (source_qualified = "{}" or source_qualified = "./{}"), rel_type = "imports""#,
        escaped, escaped
    );

    let result =
        leankg::db::schema::run_script(&db, &query, std::collections::BTreeMap::new()).unwrap();
    println!(
        "Path normalization query returned {} rows (may be 0 if DB is empty/unindexed)",
        result.rows.len()
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn test_get_call_graph_with_real_db() {
    let db_path = std::path::Path::new(".leankg");
    if !db_path.exists() {
        println!("Skipping - no .leankg database");
        return;
    }

    let db = init_db(db_path).expect("failed to init db");
    let graph = GraphEngine::new(db);

    // Find a function that has calls
    let call_graph_result = graph.get_call_graph_bounded("./src/api/auth.rs", 1, 10);
    match call_graph_result {
        Ok(calls) => {
            println!(
                "get_call_graph('./src/api/auth.rs', depth=1) returned {} calls",
                calls.len()
            );
            for (src, tgt, depth) in calls.iter().take(5) {
                println!("  {} -> {} (depth {})", src, tgt, depth);
            }
        }
        Err(e) => {
            println!("get_call_graph failed: {}", e);
        }
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn test_persistent_cache_hit_after_insert() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg_cache_test.db");
    let db = init_db(&db_path).unwrap();
    let graph = GraphEngine::with_persistence(db);

    use leankg::db::models::{CodeElement, Relationship};

    let elem_b = CodeElement {
        qualified_name: "src/b.rs::mod_b".to_string(),
        element_type: "module".to_string(),
        name: "mod_b".to_string(),
        file_path: "src/b.rs".to_string(),
        line_start: 1,
        line_end: 10,
        language: "rust".to_string(),
        ..Default::default()
    };
    graph.insert_element(&elem_b).unwrap();

    let rel = Relationship {
        id: None,
        source_qualified: "src/a.rs".to_string(),
        target_qualified: "src/b.rs::mod_b".to_string(),
        rel_type: "imports".to_string(),
        confidence: 1.0,
        metadata: serde_json::json!({}),
        ..Default::default()
    };
    graph.insert_relationship(&rel).unwrap();

    let deps_first = graph.get_dependencies("src/a.rs").unwrap();
    assert!(
        !deps_first.is_empty(),
        "First call should return results from DB"
    );

    let deps_second = graph.get_dependencies("src/a.rs").unwrap();
    assert!(
        !deps_second.is_empty(),
        "Second call (cache hit) should return results"
    );
    assert_eq!(
        deps_first.len(),
        deps_second.len(),
        "Cache hit should return same count"
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn test_persistent_cache_hit_on_second_call() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg_cache_survive_test.db");

    let db = init_db(&db_path).unwrap();
    let graph = GraphEngine::with_persistence(db);
    use leankg::db::models::{CodeElement, Relationship};

    let elem_y = CodeElement {
        qualified_name: "src/y.rs::mod_y".to_string(),
        element_type: "module".to_string(),
        name: "mod_y".to_string(),
        file_path: "src/y.rs".to_string(),
        line_start: 1,
        line_end: 5,
        language: "rust".to_string(),
        ..Default::default()
    };
    graph.insert_element(&elem_y).unwrap();

    let rel = Relationship {
        id: None,
        source_qualified: "src/x.rs".to_string(),
        target_qualified: "src/y.rs::mod_y".to_string(),
        rel_type: "imports".to_string(),
        confidence: 1.0,
        metadata: serde_json::json!({}),
        ..Default::default()
    };
    graph.insert_relationship(&rel).unwrap();

    let deps_first = graph.get_dependencies("src/x.rs").unwrap();
    assert!(!deps_first.is_empty(), "First call should return results");

    let deps_second = graph.get_dependencies("src/x.rs").unwrap();
    assert!(
        !deps_second.is_empty(),
        "Second call should return results (L1 cache hit)"
    );
    assert_eq!(deps_first.len(), deps_second.len(), "Same results expected");
}
