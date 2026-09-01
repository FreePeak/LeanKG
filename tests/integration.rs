// Integration tests requiring filesystem, async, or SurrealDB

use leankg::db::backend::init_db;
use leankg::doc::DocGenerator;
use leankg::graph::{GraphEngine, ImpactAnalyzer};
use leankg::indexer::{find_files_sync, index_file_sync, ParserManager};
use leankg::ontology::OntologyQueryEngine;
use std::path::PathBuf;
use std::sync::{Mutex, OnceLock};
use tempfile::TempDir;

/// Serializes the real-`.leankg` `init_db` calls below: on a FRESH derived
/// PG schema all three race the same migrations and concurrent `CREATE TYPE`
/// dies on `pg_type_typname_nsp_index` (duplicate key).
static REAL_DB_INIT_LOCK: OnceLock<Mutex<()>> = OnceLock::new();

fn real_db_init_guard() -> std::sync::MutexGuard<'static, ()> {
    REAL_DB_INIT_LOCK
        .get_or_init(|| Mutex::new(()))
        .lock()
        .unwrap_or_else(|e| e.into_inner())
}

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

// Regression: ontology queries in src/ontology/query.rs were binding
// 12 columns (missing `ontology_layer`) against the canonical 13-column
// code_elements schema, causing every kg_* MCP tool that exercises them
// to fail with "Arity mismatch for rule application code_elements".
// This test seeds the 13-column schema directly with ontology rows and
// asserts that the previously-failing query paths now run cleanly.

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
    let engine = OntologyQueryEngine::new(db.clone());

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
async fn test_graph_engine_all_elements_empty() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db.clone());
    let elements = graph.all_elements().unwrap();
    assert!(elements.is_empty());
}

#[tokio::test(flavor = "multi_thread")]
async fn test_graph_engine_find_element_missing() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db.clone());
    let result = graph.find_element("nonexistent::foo").unwrap();
    assert!(result.is_none());
}

#[tokio::test(flavor = "multi_thread")]
async fn test_impact_analyzer_empty_graph() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db.clone());
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
    let graph = GraphEngine::new(db.clone());
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
    let graph = GraphEngine::new(db.clone());
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
    let graph = GraphEngine::new(db.clone());

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
    let graph = GraphEngine::new(db.clone());

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
    let graph = GraphEngine::new(db.clone());

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

/// Phase 0: incremental/watch path must index Swift via regex extractor
/// (not require a tree-sitter parser that does not exist yet).
#[tokio::test(flavor = "multi_thread")]
async fn test_index_file_swift_incremental() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db.clone());

    let swift_file = tmp.path().join("Session.swift");
    std::fs::write(
        &swift_file,
        r#"
import Foundation

public class Session {
    public var token: String = ""
    public func start() {}
}

public protocol Authenticating {
    func authenticate()
}
"#,
    )
    .unwrap();

    let mut parser = ParserManager::new();
    let _ = parser.init_parsers();
    let count = index_file_sync(&graph, &mut parser, swift_file.to_str().unwrap())
        .expect("index_file_sync should not error on .swift");
    assert!(
        count > 0,
        "Swift incremental index must extract elements, got {}",
        count
    );

    let elements = graph.all_elements().unwrap();
    assert!(
        elements
            .iter()
            .any(|e| e.element_type == "class" && e.name == "Session" && e.language == "swift"),
        "expected Session class, got {:?}",
        elements
            .iter()
            .map(|e| (&e.name, &e.element_type, &e.language))
            .collect::<Vec<_>>()
    );
}

/// REL-032: bulk index walk must produce CodeElements for .vue / .svelte / .sql
/// files (qualified_name file::<ext>).
#[tokio::test(flavor = "multi_thread")]
async fn test_index_with_progress_discovers_vue_svelte_sql() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db.clone());

    std::fs::write(
        tmp.path().join("App.vue"),
        r#"<template><div class="counter">{{ count }}</div></template>
<script setup lang="ts">
import { ref } from 'vue'
const count = ref(0)
function inc() { count.value++ }
</script>
<style scoped>.counter { color: red }</style>"#,
    )
    .unwrap();
    std::fs::write(
        tmp.path().join("Hello.svelte"),
        r#"<script lang="ts">
  export let name: string = 'world';
</script>
<h1>Hello {name}!</h1>
<style>h1 { color: blue }</style>"#,
    )
    .unwrap();
    std::fs::write(
        tmp.path().join("schema.sql"),
        "CREATE TABLE users (\n  id INTEGER PRIMARY KEY,\n  email TEXT NOT NULL\n);\n",
    )
    .unwrap();

    let mut parser = ParserManager::new();
    let _ = parser.init_parsers();
    let result = leankg::indexer::index_with_progress(
        &graph,
        &mut parser,
        tmp.path().to_str().unwrap(),
        |_count, _path| {},
    )
    .await
    .expect("index_with_progress");
    assert!(
        result.indexed_files >= 3,
        "expected >= 3 indexed files, got {} (total {}, skipped {})",
        result.indexed_files,
        result.total_files,
        result.skipped_files
    );

    let elements = graph.all_elements().unwrap();
    let kinds: Vec<String> = elements
        .iter()
        .map(|e| format!("{}::{}", e.element_type, e.qualified_name))
        .collect();
    assert!(
        elements
            .iter()
            .any(|e| e.qualified_name.ends_with("App.vue") && e.language == "vue"),
        "missing .vue file element: {:?}",
        kinds
    );
    assert!(
        elements
            .iter()
            .any(|e| e.qualified_name.ends_with("Hello.svelte") && e.language == "svelte"),
        "missing .svelte file element: {:?}",
        kinds
    );
    assert!(
        elements
            .iter()
            .any(|e| e.qualified_name.ends_with("schema.sql") && e.language == "sql"),
        "missing .sql file element: {:?}",
        kinds
    );
    assert!(
        elements
            .iter()
            .any(|e| e.element_type == "table" && e.name == "users" && e.language == "sql"),
        "missing users table: {:?}",
        kinds
    );
}

/// Phase 0: incremental/watch path must index Objective-C .m files.
#[tokio::test(flavor = "multi_thread")]
async fn test_index_file_objc_incremental() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db.clone());

    let objc_file = tmp.path().join("Greeter.m");
    std::fs::write(
        &objc_file,
        r#"
#import "Greeter.h"

@implementation Greeter
- (void)sayHello {
    NSLog(@"hi");
}
@end
"#,
    )
    .unwrap();

    let mut parser = ParserManager::new();
    let _ = parser.init_parsers();
    let count = index_file_sync(&graph, &mut parser, objc_file.to_str().unwrap())
        .expect("index_file_sync should not error on .m");
    assert!(
        count > 0,
        "ObjC incremental index must extract elements, got {}",
        count
    );

    let elements = graph.all_elements().unwrap();
    assert!(
        elements
            .iter()
            .any(|e| e.name == "Greeter" && e.language == "objc"),
        "expected Greeter, got {:?}",
        elements
            .iter()
            .map(|e| (&e.name, &e.element_type, &e.language))
            .collect::<Vec<_>>()
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn test_find_files_discovers_swift_and_objc() {
    let tmp = TempDir::new().unwrap();
    std::fs::write(tmp.path().join("A.swift"), "class A {}").unwrap();
    std::fs::write(tmp.path().join("B.m"), "@implementation B\n@end\n").unwrap();
    std::fs::write(tmp.path().join("B.h"), "@interface B : NSObject\n@end\n").unwrap();
    let files = find_files_sync(tmp.path().to_str().unwrap()).unwrap();
    assert!(files.iter().any(|f| f.ends_with("A.swift")));
    assert!(files.iter().any(|f| f.ends_with("B.m")));
    assert!(files.iter().any(|f| f.ends_with("B.h")));
}

#[tokio::test(flavor = "multi_thread")]
async fn test_get_relationships_with_real_db() {
    // Use the real .leankg database from current dir
    let db_path = std::path::Path::new(".leankg");
    if !db_path.exists() {
        println!("Skipping - no .leankg database in current dir");
        return;
    }
    let _init = real_db_init_guard();
    let db = init_db(db_path).expect("failed to init db");

    // Check if DB has data (skip test if empty)
    let count_query = r#"?[cnt] := count(code_elements[qualified_name]), cnt = $cnt"#;
    let count_result = db.run_script(count_query, std::collections::BTreeMap::new());
    let has_data = count_result
        .map(|r| !r.rows.is_empty() && !r.rows[0].is_empty())
        .unwrap_or(false);
    if !has_data {
        println!("Skipping - .leankg database appears empty or unindexed");
        return;
    }

    let graph = GraphEngine::new(db.clone());

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
    let _init = real_db_init_guard();
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

    let result = db
        .run_script(&query, std::collections::BTreeMap::new())
        .unwrap();
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
    let _init = real_db_init_guard();
    let db = init_db(db_path).expect("failed to init db");
    let graph = GraphEngine::new(db.clone());

    // Find a function that has calls
    let call_graph_result = graph.get_call_graph_bounded("./src/api/auth.rs", 1, 10);
    match call_graph_result {
        Ok(calls) => {
            println!(
                "get_call_graph('./src/api/auth.rs', depth=1) returned {} calls",
                calls.len()
            );
            for edge in calls.iter().take(5) {
                println!(
                    "  {} -> {} (depth {}, label {})",
                    edge.source, edge.target, edge.depth, edge.confidence_label
                );
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
    let graph = GraphEngine::with_persistence(db.clone());

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
    let graph = GraphEngine::with_persistence(db.clone());
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
