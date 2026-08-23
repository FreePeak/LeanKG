//! PR-40 conversation mining (US-MP-03 / FR-MP-09..13).
//!
//! TDD seams:
//! 1. Parser per format (claude / chatgpt / slack fixture JSON)
//! 2. Type classification (decision / preference / milestone / problem)
//! 3. `decided_about` edge creation
//! 4. CLI end-to-end with TempDir project

use leankg::conversation_indexer::{
    self, ConversationFormat, MinedItem, MinedItemKind, MiningResult,
};
use leankg::db::backend::init_db;
use leankg::graph::GraphEngine;
use std::path::PathBuf;
use tempfile::TempDir;

const FIXTURES: &str = "tests/fixtures/conversations";

fn fixture(name: &str) -> PathBuf {
    PathBuf::from(FIXTURES).join(name)
}

fn with_test_graph<F>(callback: F)
where
    F: FnOnce(&GraphEngine, &TempDir),
{
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("test.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db.clone());
    callback(&graph, &tmp);
}

// ---------------------------------------------------------------- seam 1: parsers

#[test]
fn parses_claude_export_json() {
    let items =
        conversation_indexer::mine_file(&fixture("claude_export.json"), ConversationFormat::Claude)
            .expect("claude fixture should parse");

    assert_eq!(items.len(), 3, "decision + preference + decision expected");
    let kinds: Vec<&str> = items.iter().map(|i| i.kind.as_str()).collect();
    assert!(kinds.contains(&"decision"), "got: {kinds:?}");
    assert!(kinds.contains(&"preference"), "got: {kinds:?}");
    assert!(items[0].verbatim.contains("JWT"), "raw verbatim expected");
    assert_eq!(items[0].source, "claude", "source format expected");
}

#[test]
fn parses_chatgpt_export_json() {
    let items = conversation_indexer::mine_file(
        &fixture("chatgpt_export.json"),
        ConversationFormat::ChatGpt,
    )
    .expect("chatgpt fixture should parse");

    assert_eq!(items.len(), 2, "milestone + decision expected");
    let kinds: Vec<&str> = items.iter().map(|i| i.kind.as_str()).collect();
    assert!(kinds.contains(&"milestone"), "got: {kinds:?}");
    assert!(kinds.contains(&"decision"), "got: {kinds:?}");
    assert!(items[0].verbatim.contains("PostgreSQL"));
    assert_eq!(items[0].source, "chatgpt");
}

#[test]
fn parses_slack_export_json() {
    let items =
        conversation_indexer::mine_file(&fixture("slack_export.json"), ConversationFormat::Slack)
            .expect("slack fixture should parse");

    assert_eq!(items.len(), 3, "decision + problem + preference expected");
    let kinds: Vec<&str> = items.iter().map(|i| i.kind.as_str()).collect();
    assert!(kinds.contains(&"decision"), "got: {kinds:?}");
    assert!(kinds.contains(&"problem"), "got: {kinds:?}");
    assert!(kinds.contains(&"preference"), "got: {kinds:?}");
    assert!(items.iter().any(|i| i.verbatim.contains("gRPC")));
    assert_eq!(items[0].source, "slack");
}

#[test]
fn parses_directory_of_exports() {
    let dir = fixture(".");
    let result = conversation_indexer::mine_directory(&dir, ConversationFormat::Claude)
        .expect("directory should be readable");
    // Only *.json matching the format's shape is mined; claude fixture only.
    assert!(result.items.len() >= 2, "got {}", result.items.len());
    assert_eq!(result.sources, 1, "one claude fixture file");
}

#[test]
fn rejects_unknown_format() {
    let err = conversation_indexer::mine_file(
        &fixture("claude_export.json"),
        ConversationFormat::Unknown,
    );
    assert!(err.is_err(), "unknown format must error");
}

// ---------------------------------------------------------------- seam 2: classification

#[test]
fn classifies_decision() {
    assert_eq!(
        MinedItemKind::classify("Decision: switch to Redis for caching"),
        MinedItemKind::Decision
    );
    assert_eq!(
        MinedItemKind::classify("We decided to use RS256 for signing."),
        MinedItemKind::Decision
    );
    assert_eq!(
        MinedItemKind::classify("we will use gRPC for inter-service communication."),
        MinedItemKind::Decision
    );
}

#[test]
fn classifies_preference() {
    assert_eq!(
        MinedItemKind::classify("Preference: prefer async/await style in handlers"),
        MinedItemKind::Preference
    );
    assert_eq!(
        MinedItemKind::classify("I prefer protobuf over JSON for internal APIs."),
        MinedItemKind::Preference
    );
}

#[test]
fn classifies_milestone() {
    assert_eq!(
        MinedItemKind::classify("Milestone: migration completed by end of Q3"),
        MinedItemKind::Milestone
    );
    assert_eq!(
        MinedItemKind::classify("Goal: get 100% test coverage by release"),
        MinedItemKind::Milestone
    );
}

#[test]
fn classifies_problem() {
    assert_eq!(
        MinedItemKind::classify("Problem: the batch job keeps timing out at 5 minutes"),
        MinedItemKind::Problem
    );
    assert_eq!(
        MinedItemKind::classify("The batch job keeps timing out at 5 minutes"),
        MinedItemKind::Problem
    );
}

#[test]
fn falls_back_to_general_for_unmatched_text() {
    assert_eq!(
        MinedItemKind::classify("Let's check the weather tomorrow"),
        MinedItemKind::General
    );
}

// ---------------------------------------------------------------- seam 3: graph persistence + edges

#[test]
fn indexes_mined_nodes_and_decided_about_edges() {
    with_test_graph(|graph, tmp| {
        let project = tmp.path().join("proj");
        std::fs::create_dir_all(&project).unwrap();

        let items = vec![
            MinedItem {
                kind: MinedItemKind::Decision,
                verbatim: "Decision: adopt RS256 JWT for gateway auth".to_string(),
                source: "claude".to_string(),
                participants: vec!["alice".to_string()],
                timestamp: "2026-07-15T10:00:00Z".to_string(),
                topic: "auth".to_string(),
                code_targets: vec!["src/gateway.rs::handle_auth".to_string()],
            },
            MinedItem {
                kind: MinedItemKind::Preference,
                verbatim: "Preference: async/await in handlers".to_string(),
                source: "claude".to_string(),
                participants: vec![],
                timestamp: "".to_string(),
                topic: "style".to_string(),
                code_targets: vec![],
            },
        ];

        let result = conversation_indexer::index_items(graph, &project, items).unwrap();
        assert_eq!(result.elements_indexed, 2);
        assert_eq!(result.relationships_created, 1, "one decided_about edge");

        // Elements persisted with the mined types
        let elements = graph.all_elements().unwrap();
        let decision = elements
            .iter()
            .find(|e| e.element_type == "decision")
            .expect("decision element type must exist");
        assert_eq!(decision.name, "auth");
        assert!(
            decision
                .qualified_name
                .starts_with("conversations/proj/decision/"),
            "qualified_name {} should carry the mined-type prefix",
            decision.qualified_name
        );
        assert!(
            decision
                .metadata
                .get("verbatim")
                .and_then(|v| v.as_str())
                .is_some(),
            "raw verbatim stored in metadata"
        );

        // decided_about edge: decision node -> code element
        let rels = graph.all_relationships().unwrap();
        let edge = rels
            .iter()
            .find(|r| r.rel_type == "decided_about")
            .expect("decided_about edge must exist");
        assert_eq!(edge.target_qualified, "src/gateway.rs::handle_auth");
        assert_eq!(edge.source_qualified, decision.qualified_name);
    });
}

#[test]
fn reindex_is_idempotent() {
    with_test_graph(|graph, tmp| {
        let project = tmp.path().join("proj");
        std::fs::create_dir_all(&project).unwrap();

        let items = vec![MinedItem {
            kind: MinedItemKind::Decision,
            verbatim: "Decision: use Redis".to_string(),
            source: "slack".to_string(),
            participants: vec![],
            timestamp: "".to_string(),
            topic: "cache".to_string(),
            code_targets: vec!["src/cache.rs::init".to_string()],
        }];

        conversation_indexer::index_items(graph, &project, items.clone()).unwrap();
        conversation_indexer::index_items(graph, &project, items).unwrap();

        let elements = graph.all_elements().unwrap();
        let decisions: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "decision")
            .collect();
        assert_eq!(decisions.len(), 1, "second run must not duplicate nodes");

        let rels = graph.all_relationships().unwrap();
        let edges: Vec<_> = rels
            .iter()
            .filter(|r| r.rel_type == "decided_about")
            .collect();
        assert_eq!(edges.len(), 1, "second run must not duplicate edges");
    });
}

// ---------------------------------------------------------------- seam 4: CLI end-to-end

#[test]
fn cli_subcommand_parses_flags() {
    use clap::Parser;
    use leankg::cli::CLICommand;

    #[derive(Parser)]
    struct TestArgs {
        #[command(subcommand)]
        command: CLICommand,
    }

    let args = TestArgs::try_parse_from([
        "leankg",
        "mine-conversations",
        "--format",
        "claude",
        "--project",
        "/tmp/proj",
        "--input",
        "/tmp/chats",
    ])
    .unwrap();
    match args.command {
        CLICommand::MineConversations {
            format,
            project,
            input,
        } => {
            assert_eq!(format, "claude");
            assert_eq!(project, "/tmp/proj");
            assert_eq!(input, "/tmp/chats");
        }
        _ => panic!("expected MineConversations command"),
    }

    // Invalid format is rejected by clap value_parser
    assert!(TestArgs::try_parse_from([
        "leankg",
        "mine-conversations",
        "--format",
        "bogus",
        "--project",
        "p",
        "--input",
        "i",
    ])
    .is_err());
}

#[test]
fn cli_mine_conversations_e2e_with_tempdir() {
    with_test_graph(|graph, tmp| {
        // Simulate a project dir with .leankg already initialized
        let project = tmp.path().join("proj");
        std::fs::create_dir_all(project.join(".leankg")).unwrap();
        let db_path = project.join(".leankg");
        let db = init_db(db_path.as_path()).unwrap();
        let g2 = GraphEngine::new(db.clone());
        let _ = graph; // keep with_test_graph signature stable

        // Seed one code element the decision can point at
        let elem = leankg::db::models::CodeElement {
            qualified_name: "src/gateway.rs::handle_auth".to_string(),
            element_type: "function".to_string(),
            name: "handle_auth".to_string(),
            file_path: "src/gateway.rs".to_string(),
            line_start: 1,
            line_end: 5,
            language: "rust".to_string(),
            parent_qualified: None,
            cluster_id: None,
            cluster_label: None,
            metadata: serde_json::json!({}),
            ..Default::default()
        };
        g2.insert_element(&elem).unwrap();

        let result = conversation_indexer::mine_into_project(
            &project,
            &fixture("claude_export.json"),
            ConversationFormat::Claude,
        )
        .expect("CLI-level mine must succeed");
        assert!(result.items.len() >= 2);
        assert!(result.elements_indexed >= 2);
    });
}

#[test]
fn mining_result_summary_roundtrip() {
    let result = MiningResult {
        items: vec![MinedItem {
            kind: MinedItemKind::Problem,
            verbatim: "Problem: OOM in batch".to_string(),
            source: "slack".to_string(),
            participants: vec![],
            timestamp: "".to_string(),
            topic: "batch".to_string(),
            code_targets: vec![],
        }],
        sources: 1,
        elements_indexed: 1,
        relationships_created: 0,
    };
    let s = result.summary();
    assert!(s.contains("Mined 1 item"));
    assert!(s.contains("problem"));
}
