//! Live end-to-end tests for Swift / Objective-C demo fixtures.
//!
//! Indexes real files under `tests/fixtures/{swift,objc}` into a GraphEngine
//! via `index_file_sync` and asserts entities, heritage, and call edges.

use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;
use leankg::indexer::{find_files_sync, index_file_sync, ParserManager};
use leankg::lsp::{apply_typed_resolve, TypeRegistry};
use std::path::{Path, PathBuf};
use tempfile::TempDir;

const SWIFT_FIXTURES: &str = "tests/fixtures/swift";
const OBJC_FIXTURES: &str = "tests/fixtures/objc";

fn fixture_path(dir: &str, name: &str) -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join(dir).join(name)
}

fn copy_fixtures(src_dir: &str, dest: &Path) {
    let src = Path::new(env!("CARGO_MANIFEST_DIR")).join(src_dir);
    for entry in std::fs::read_dir(&src).expect("fixtures dir") {
        let entry = entry.unwrap();
        let target = dest.join(entry.file_name());
        std::fs::copy(entry.path(), &target).expect("copy fixture");
    }
}

fn open_graph(tmp: &TempDir) -> (GraphEngine, ParserManager) {
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);
    let mut parser = ParserManager::new();
    let _ = parser.init_parsers();
    (graph, parser)
}

#[tokio::test(flavor = "multi_thread")]
async fn live_swift_demo_indexes_heritage_and_calls() {
    let tmp = TempDir::new().unwrap();
    let project = tmp.path().join("demo");
    std::fs::create_dir_all(&project).unwrap();
    copy_fixtures(SWIFT_FIXTURES, &project);

    let (graph, mut parser) = open_graph(&tmp);

    let session = project.join("Session.swift");
    let auth = project.join("Authenticating.swift");
    let n1 = index_file_sync(&graph, &mut parser, session.to_str().unwrap()).unwrap();
    let n2 = index_file_sync(&graph, &mut parser, auth.to_str().unwrap()).unwrap();
    assert!(n1 > 0, "Session.swift should index elements");
    assert!(n2 > 0, "Authenticating.swift should index elements");

    let elements = graph.all_elements().unwrap();
    assert!(
        elements
            .iter()
            .any(|e| e.language == "swift" && e.name == "Session" && e.element_type == "class"),
        "Session class missing: {:?}",
        elements
            .iter()
            .map(|e| (&e.name, &e.element_type))
            .collect::<Vec<_>>()
    );
    assert!(elements
        .iter()
        .any(|e| e.language == "swift" && e.name == "Authenticating"));
    assert!(elements
        .iter()
        .any(|e| e.language == "swift" && e.name == "start"));

    let rels = graph.all_relationships().unwrap();
    assert!(
        rels.iter().any(|r| {
            r.rel_type == "extends"
                && r.source_qualified.contains("Session")
                && r.target_qualified == "NSObject"
        }),
        "Session should extend NSObject"
    );
    assert!(
        rels.iter().any(|r| {
            r.rel_type == "implements"
                && r.source_qualified.contains("Session")
                && r.target_qualified == "Authenticating"
        }),
        "Session should implement Authenticating"
    );
    assert!(
        rels.iter().any(|r| {
            r.rel_type == "calls"
                && r.source_qualified.contains("start")
                && r.target_qualified.contains("authenticate")
        }),
        "start should call authenticate, got calls={:?}",
        rels.iter()
            .filter(|r| r.rel_type == "calls")
            .map(|r| (&r.source_qualified, &r.target_qualified))
            .collect::<Vec<_>>()
    );

    // Hybrid typed resolve on live CALLS.
    let mut calls: Vec<_> = rels.into_iter().filter(|r| r.rel_type == "calls").collect();
    let registry = TypeRegistry::from_elements(&elements);
    let upgraded = apply_typed_resolve(&mut calls, &registry, "swift,objc");
    assert!(
        upgraded > 0,
        "at least one Swift CALL should upgrade to typed"
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn live_objc_demo_indexes_heritage_selectors_and_message_sends() {
    let tmp = TempDir::new().unwrap();
    let project = tmp.path().join("demo");
    std::fs::create_dir_all(&project).unwrap();
    copy_fixtures(OBJC_FIXTURES, &project);

    let (graph, mut parser) = open_graph(&tmp);

    for name in ["Greetable.h", "Greeter.h", "Greeter.m"] {
        let path = project.join(name);
        let n = index_file_sync(&graph, &mut parser, path.to_str().unwrap()).unwrap();
        assert!(n > 0, "{name} should index elements, got {n}");
    }

    let elements = graph.all_elements().unwrap();
    assert!(elements
        .iter()
        .any(|e| e.language == "objc" && e.name == "Greeter"));
    assert!(elements
        .iter()
        .any(|e| e.language == "objc" && e.name == "Greetable"));
    assert!(
        elements
            .iter()
            .any(|e| e.element_type == "method" && e.name == "setName:age:"),
        "expected setName:age: selector, methods={:?}",
        elements
            .iter()
            .filter(|e| e.element_type == "method")
            .map(|e| &e.name)
            .collect::<Vec<_>>()
    );

    let rels = graph.all_relationships().unwrap();
    assert!(
        rels.iter().any(|r| {
            r.rel_type == "extends"
                && r.source_qualified.contains("Greeter")
                && r.target_qualified == "NSObject"
        }),
        "Greeter should extend NSObject"
    );
    assert!(
        rels.iter().any(|r| {
            r.rel_type == "implements"
                && r.source_qualified.contains("Greeter")
                && r.target_qualified == "Greetable"
        }),
        "Greeter should implement Greetable"
    );
    assert!(
        rels.iter().any(|r| {
            r.rel_type == "calls"
                && r.source_qualified.contains("sayHello")
                && r.target_qualified.contains("setup")
        }),
        "sayHello should message-send setup, calls={:?}",
        rels.iter()
            .filter(|r| r.rel_type == "calls")
            .map(|r| (&r.source_qualified, &r.target_qualified))
            .collect::<Vec<_>>()
    );
    assert!(
        rels.iter()
            .any(|r| { r.rel_type == "calls" && r.target_qualified.contains("log:level:") }),
        "expected log:level: message send"
    );

    let mut calls: Vec<_> = rels.into_iter().filter(|r| r.rel_type == "calls").collect();
    let registry = TypeRegistry::from_elements(&elements);
    let upgraded = apply_typed_resolve(&mut calls, &registry, "swift,objc");
    assert!(
        upgraded > 0,
        "at least one ObjC CALL should upgrade to typed"
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn live_find_files_discovers_demo_fixtures() {
    let swift = find_files_sync(
        Path::new(env!("CARGO_MANIFEST_DIR"))
            .join(SWIFT_FIXTURES)
            .to_str()
            .unwrap(),
    )
    .unwrap();
    assert!(swift.iter().any(|f| f.ends_with("Session.swift")));
    assert!(swift.iter().any(|f| f.ends_with("Authenticating.swift")));

    let objc = find_files_sync(
        Path::new(env!("CARGO_MANIFEST_DIR"))
            .join(OBJC_FIXTURES)
            .to_str()
            .unwrap(),
    )
    .unwrap();
    assert!(objc.iter().any(|f| f.ends_with("Greeter.m")));
    assert!(objc.iter().any(|f| f.ends_with("Greeter.h")));
    assert!(objc.iter().any(|f| f.ends_with("Greetable.h")));
}

#[test]
fn demo_swift_fixture_files_exist() {
    assert!(fixture_path(SWIFT_FIXTURES, "Session.swift").is_file());
    assert!(fixture_path(SWIFT_FIXTURES, "Authenticating.swift").is_file());
}

#[test]
fn demo_objc_fixture_files_exist() {
    assert!(fixture_path(OBJC_FIXTURES, "Greeter.m").is_file());
    assert!(fixture_path(OBJC_FIXTURES, "Greeter.h").is_file());
    assert!(fixture_path(OBJC_FIXTURES, "Greetable.h").is_file());
}
