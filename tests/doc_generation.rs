use leankg::db::models::{BusinessLogic, CodeElement, Relationship};
use leankg::db::schema::init_db;
use leankg::doc::{DocGenerator, DocSyncResult, DocTrackingInfo, TemplateEngine};
use leankg::graph::GraphEngine;
use std::path::PathBuf;
use tempfile::TempDir;

#[tokio::test]
async fn test_doc_generator_comprehensive_agents_md() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);
    let doc_gen = DocGenerator::new(graph, PathBuf::from("./docs"));

    let content = doc_gen.generate_agents_md().unwrap();

    assert!(content.contains("# Agent Guidelines for LeanKG"));
    assert!(content.contains("## Project Overview"));
    assert!(content.contains("**Tech Stack**: Rust 1.70+"));
    assert!(content.contains("## Build Commands"));
    assert!(content.contains("cargo build"));
    assert!(content.contains("cargo test"));
    assert!(content.contains("## Testing Guidelines"));
    assert!(content.contains("## Code Structure Overview"));
    assert!(content.contains("This codebase contains 0 elements"));
}

#[tokio::test]
async fn test_doc_generator_comprehensive_claude_md() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);
    let doc_gen = DocGenerator::new(graph, PathBuf::from("./docs"));

    let content = doc_gen.generate_claude_md().unwrap();

    assert!(content.contains("# CLAUDE.md"));
    assert!(content.contains("## Project Overview"));
    assert!(content.contains("## Architecture Decisions"));
    assert!(content.contains("### Knowledge Graph Storage"));
    assert!(content.contains("### Code Indexing"));
    assert!(content.contains("### Documentation Generation"));
    assert!(content.contains("## Context Statistics"));
    assert!(content.contains("- **Total elements**: 0"));
    assert!(content.contains("- **Total relationships**: 0"));
    assert!(content.contains("## Context Guidelines for AI"));
}

#[tokio::test]
async fn test_doc_sync_result_structure() {
    let result = DocSyncResult {
        file_path: "src/main.rs".to_string(),
        elements_regenerated: 5,
        relationships_updated: 10,
        regenerated_elements: vec![
            "src/main.rs::main".to_string(),
            "src/main.rs::init".to_string(),
        ],
    };

    assert_eq!(result.file_path, "src/main.rs");
    assert_eq!(result.elements_regenerated, 5);
    assert_eq!(result.relationships_updated, 10);
    assert_eq!(result.regenerated_elements.len(), 2);
}

#[tokio::test]
async fn test_doc_tracking_info_structure() {
    let element = CodeElement {
        qualified_name: "src/main.rs::main".to_string(),
        element_type: "function".to_string(),
        name: "main".to_string(),
        file_path: "src/main.rs".to_string(),
        line_start: 1,
        line_end: 10,
        language: "rust".to_string(),
        parent_qualified: None,
        metadata: serde_json::json!({}),
        ..Default::default()
    };

    let relationship = Relationship {
        id: None,
        source_qualified: "src/main.rs::main".to_string(),
        target_qualified: "src/lib.rs::init".to_string(),
        rel_type: "imports".to_string(),
        confidence: 1.0,
        metadata: serde_json::json!({}),
    };

    let annotation = BusinessLogic {
        id: None,
        element_qualified: "src/main.rs::main".to_string(),
        description: "Main entry point for the application".to_string(),
        user_story_id: Some("US-001".to_string()),
        feature_id: Some("F-001".to_string()),
    };

    let tracking_info = DocTrackingInfo {
        element,
        relationships: vec![relationship],
        annotation: Some(annotation),
        generated_from: vec!["src/main.rs".to_string()],
    };

    assert_eq!(tracking_info.element.qualified_name, "src/main.rs::main");
    assert_eq!(tracking_info.relationships.len(), 1);
    assert!(tracking_info.annotation.is_some());
    assert_eq!(tracking_info.generated_from.len(), 1);
}

#[tokio::test]
async fn test_template_engine_render_template() {
    use std::collections::HashMap;

    let template = "Hello {{name}}, you are a {{role}}.";
    let mut vars = HashMap::new();
    vars.insert("name".to_string(), "Alice".to_string());
    vars.insert("role".to_string(), "developer".to_string());

    let result = TemplateEngine::render_template(template, &vars);
    assert_eq!(result, "Hello Alice, you are a developer.");
}

#[tokio::test]
async fn test_template_engine_render_template_missing_var() {
    use std::collections::HashMap;

    let template = "Hello {{name}}, your email is {{email}}.";
    let mut vars = HashMap::new();
    vars.insert("name".to_string(), "Bob".to_string());

    let result = TemplateEngine::render_template(template, &vars);
    assert_eq!(result, "Hello Bob, your email is {{email}}.");
}

#[tokio::test]
async fn test_template_engine_default_agents_template() {
    let template = TemplateEngine::get_default_agents_template();
    assert!(template.contains("# Agent Guidelines for {{project_name}}"));
    assert!(template.contains("## Project Overview"));
    assert!(template.contains("{{project_description}}"));
    assert!(template.contains("## Build Commands"));
    assert!(template.contains("cargo build"));
    assert!(template.contains("## Code Structure Overview"));
}

#[tokio::test]
async fn test_template_engine_default_claude_template() {
    let template = TemplateEngine::get_default_claude_template();
    assert!(template.contains("# CLAUDE.md"));
    assert!(template.contains("## Project Overview"));
    assert!(template.contains("{{project_description}}"));
    assert!(template.contains("## Architecture Decisions"));
    assert!(template.contains("{{architecture_decisions}}"));
    assert!(template.contains("## Context Statistics"));
    assert!(template.contains("{{element_count}}"));
    assert!(template.contains("{{relationship_count}}"));
}

#[tokio::test]
async fn test_doc_generator_with_elements() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);

    let element = CodeElement {
        qualified_name: "src/main.rs::main".to_string(),
        element_type: "function".to_string(),
        name: "main".to_string(),
        file_path: "src/main.rs".to_string(),
        line_start: 1,
        line_end: 20,
        language: "rust".to_string(),
        parent_qualified: None,
        metadata: serde_json::json!({}),
        ..Default::default()
    };

    graph.insert_elements(&[element]).unwrap();

    let doc_gen = DocGenerator::new(graph, PathBuf::from("./docs"));
    let content = doc_gen.generate_agents_md().unwrap();

    assert!(content.contains("# Agent Guidelines for LeanKG"));
    assert!(content.contains("This codebase contains 1 elements"));
    assert!(content.contains("## Code Structure Overview"));
    assert!(content.contains("### Functions"));
    assert!(content.contains("src/main.rs::main"));
}

#[tokio::test]
async fn test_doc_generator_regenerate_for_file() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);

    let element = CodeElement {
        qualified_name: "src/main.rs::main".to_string(),
        element_type: "function".to_string(),
        name: "main".to_string(),
        file_path: "src/main.rs".to_string(),
        line_start: 1,
        line_end: 20,
        language: "rust".to_string(),
        parent_qualified: None,
        metadata: serde_json::json!({}),
        ..Default::default()
    };

    graph.insert_elements(&[element]).unwrap();

    let doc_gen = DocGenerator::new(graph, PathBuf::from("./docs"));
    let result = doc_gen.regenerate_for_file("src/main.rs").unwrap();

    assert_eq!(result.len(), 1);
    assert_eq!(result[0], "src/main.rs::main");
}

#[tokio::test]
async fn test_doc_generator_tracking_info() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);

    let element = CodeElement {
        qualified_name: "src/main.rs::main".to_string(),
        element_type: "function".to_string(),
        name: "main".to_string(),
        file_path: "src/main.rs".to_string(),
        line_start: 1,
        line_end: 20,
        language: "rust".to_string(),
        parent_qualified: None,
        metadata: serde_json::json!({}),
        ..Default::default()
    };

    graph.insert_elements(&[element]).unwrap();

    let doc_gen = DocGenerator::new(graph, PathBuf::from("./docs"));
    let tracking = doc_gen.get_doc_tracking_info("src/main.rs::main").unwrap();

    assert!(tracking.is_some());
    let info = tracking.unwrap();
    assert_eq!(info.element.qualified_name, "src/main.rs::main");
}

#[tokio::test]
async fn test_doc_generator_tracking_info_not_found() {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db);
    let doc_gen = DocGenerator::new(graph, PathBuf::from("./docs"));

    let tracking = doc_gen.get_doc_tracking_info("nonexistent::foo").unwrap();
    assert!(tracking.is_none());
}
