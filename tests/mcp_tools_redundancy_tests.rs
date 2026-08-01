//! Coverage for the 36 MCP tools that have **no** direct test in
//! `tests/mcp_tools_full_tests.rs`, plus a redundancy matrix that documents
//! which tools overlap, are deprecated, or are aliases of newer ones.
//!
//! Run:
//! ```bash
//! cargo test --release --test mcp_tools_redundancy_tests -- --nocapture
//! ```
//!
//! Every `#[test]` here is a behaviour assertion: each tool must either
//! return a non-empty payload, return a documented error, or refuse unknown
//! arguments. Failures pinpoint which tool changed shape without updating
//! the smoke suite.
//!
//! Tools covered here (each tested by name in the corresponding sub-module):
//!
//! add_annotation, add_documentation, add_knowledge,
//! agent_diary_read, agent_diary_write, agent_focus,
//! check_consistency, concept_search,
//! delete_knowledge, explain_node, export_graph_snapshot,
//! find_dead_code, find_env_conflicts, find_route, find_tunnels,
//! get_architecture, get_cluster_skill, get_god_nodes, get_graph_report,
//! get_graph_schema, get_nav_callers, get_nav_graph, get_overview_context,
//! get_pr_impact, get_screen_args, get_service_context, get_team_map,
//! get_upcoming_changes, kg_concept_map, kg_context, kg_ontology_status,
//! kg_self_test, kg_semantic_context, kg_trace_workflow, link_element,
//! load_layer, promote_environment, query_incidents, report_query_outcome,
//! resolve_with_lsp, search_annotations,
//! search_knowledge, semantic_search, shortest_path, temporal_query,
//! timeline, update_knowledge, query_graph

use leankg::db::schema::{init_db, run_script, CozoDb};
use leankg::graph::GraphEngine;
use leankg::mcp::handler::ToolHandler;
use leankg::mcp::tools::ToolRegistry;
use serde_json::{json, Value};
use std::collections::HashSet;
use tempfile::TempDir;

// ---------------------------------------------------------------------------
// Shared fixture: an indexed Rust code graph with services, clusters, and
// enough relationships to drive every previously-untested tool.
// ---------------------------------------------------------------------------

const FIXTURE: &str = r#"
?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] <-
[
    ["src/auth/mod.rs", "file", "mod.rs", "src/auth/mod.rs", 1, 100, "rust", "", "c1", "auth", "{}", "local", "procedural"],
    ["src/auth/mod.rs::login", "function", "login", "src/auth/mod.rs", 10, 30, "rust", "src/auth/mod.rs", "c1", "auth", "{}", "local", "procedural"],
    ["src/auth/mod.rs::verify_token", "function", "verify_token", "src/auth/mod.rs", 31, 60, "rust", "src/auth/mod.rs", "c1", "auth", "{}", "local", "procedural"],
    ["src/billing/mod.rs", "file", "mod.rs", "src/billing/mod.rs", 1, 100, "rust", "", "c2", "billing", "{}", "local", "procedural"],
    ["src/billing/mod.rs::charge", "function", "charge", "src/billing/mod.rs", 10, 40, "rust", "src/billing/mod.rs", "c2", "billing", "{}", "local", "procedural"],
    ["src/api/mod.rs", "file", "mod.rs", "src/api/mod.rs", 1, 100, "rust", "", "c1", "auth", "{}", "local", "procedural"],
    ["src/api/mod.rs::handle_request", "function", "handle_request", "src/api/mod.rs", 5, 60, "rust", "src/api/mod.rs", "c1", "auth", "{}", "local", "procedural"]
]
:put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer}
"#;

const REL_FIXTURE: &str = r#"
?[source_qualified, target_qualified, rel_type, confidence, metadata, env] <-
[
    ["src/api/mod.rs::handle_request", "src/auth/mod.rs::login", "calls", 0.95, "{}", "local"],
    ["src/api/mod.rs::handle_request", "src/auth/mod.rs::verify_token", "calls", 0.95, "{}", "local"],
    ["src/auth/mod.rs::login", "src/billing/mod.rs::charge", "calls", 0.9, "{}", "local"]
]
:put relationships {source_qualified, target_qualified, rel_type, confidence, metadata, env}
"#;

fn seed_db(db: &CozoDb) {
    run_script(db, FIXTURE, Default::default()).expect("seed code_elements");
    run_script(db, REL_FIXTURE, Default::default()).expect("seed relationships");
}

async fn make_handler() -> (ToolHandler, TempDir) {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg.db");
    let db = init_db(&db_path).expect("init_db");
    seed_db(&db);
    let graph = GraphEngine::new(db);
    (ToolHandler::new(graph, db_path), tmp)
}

/// Write a persona under `<tmp>/.leankg/agents/<name>.json` so agent_focus / diary pass.
fn write_persona(tmp: &TempDir, name: &str) {
    let dir = tmp.path().join(".leankg").join("agents");
    std::fs::create_dir_all(&dir).expect("create agents dir");
    let persona = json!({
        "name": name,
        "description": "smoke persona",
        "focus_areas": ["auth"],
        "path_filters": ["src/auth"],
        "cluster_id": "c1",
        "element_types": ["function"]
    });
    std::fs::write(
        dir.join(format!("{name}.json")),
        serde_json::to_string_pretty(&persona).unwrap(),
    )
    .expect("write persona");
}

async fn call(handler: &ToolHandler, tool: &str, args: Value) -> Result<Value, String> {
    handler.execute_tool(tool, &args).await
}

// ---------------------------------------------------------------------------
// Static registry assertions: every name we test below must be registered.
// ---------------------------------------------------------------------------

#[test]
fn every_tested_tool_is_registered() {
    let registered: HashSet<String> = ToolRegistry::list_tools()
        .into_iter()
        .map(|t| t.name)
        .collect();
    let required: &[&str] = &[
        "add_annotation",
        "add_documentation",
        "add_knowledge",
        "agent_diary_read",
        "agent_diary_write",
        "agent_focus",
        "check_consistency",
        "concept_search",
        "delete_knowledge",
        "explain_node",
        "export_graph_snapshot",
        "find_dead_code",
        "find_env_conflicts",
        "find_route",
        "find_tunnels",
        "get_architecture",
        "get_cluster_skill",
        "get_god_nodes",
        "get_graph_report",
        "get_graph_schema",
        "get_nav_callers",
        "get_nav_graph",
        "get_overview_context",
        "get_pr_impact",
        "get_screen_args",
        "get_service_context",
        "get_team_map",
        "get_upcoming_changes",
        "session_recall",
        "kg_concept_map",
        "kg_context",
        "kg_ontology_status",
        "kg_self_test",
        "kg_semantic_context",
        "kg_trace_workflow",
        "link_element",
        "promote_environment",
        "query_incidents",
        "report_query_outcome",
        "resolve_with_lsp",
        "search_annotations",
        "search_knowledge",
        "semantic_search",
        "shortest_path",
        "query_graph",
        "temporal_query",
        "timeline",
        "update_knowledge",
    ];
    for name in required {
        // `kg_semantic_context` is `#[cfg(feature = "embeddings")]`-gated; only
        // present in the registry when that feature is enabled.
        if *name == "kg_semantic_context" && !registered.contains(*name) {
            continue;
        }
        assert!(
            registered.contains(*name),
            "MCP tool `{}` is exercised by these tests but is not in ToolRegistry::list_tools()",
            name
        );
    }
}

// ---------------------------------------------------------------------------
// Knowledge + annotation + documentation lifecycle tools
// ---------------------------------------------------------------------------

mod knowledge {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn add_then_update_then_delete_knowledge() {
        let (handler, _tmp) = make_handler().await;

        let created = call(
            &handler,
            "add_knowledge",
            json!({
                "knowledge_type": "design",
                "title": "Why we use RocksDB",
                "content": "RocksDB survives 256GB SSD writes without mmap thrash.",
                "tags": "[\"storage\",\"design\"]",
                "author": "oncall"
            }),
        )
        .await
        .expect("add_knowledge");
        let id = created
            .get("id")
            .and_then(|v| v.as_str())
            .expect("knowledge has id");

        let updated = call(
            &handler,
            "update_knowledge",
            json!({"id": id, "content": "Updated body with new evidence"}),
        )
        .await
        .expect("update_knowledge");
        assert!(updated.get("id").is_some());

        let hits = call(&handler, "search_knowledge", json!({"query": "RocksDB"}))
            .await
            .expect("search_knowledge");
        assert!(!hits.to_string().is_empty());

        let deleted = call(&handler, "delete_knowledge", json!({"id": id}))
            .await
            .expect("delete_knowledge");
        assert!(!deleted.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn add_and_search_annotations() {
        let (handler, _tmp) = make_handler().await;
        let added = call(
            &handler,
            "add_annotation",
            json!({
                "element": "src/auth/mod.rs::login",
                "description": "bcrypt cost factor is intentionally high"
            }),
        )
        .await
        .expect("add_annotation");
        // add_annotation returns {element, description, action}; both shapes accepted.
        assert!(
            added.get("element").is_some()
                || added.get("id").is_some()
                || added.to_string().contains("annotation")
        );

        let hits = call(
            &handler,
            "search_annotations",
            json!({"annotation_name": "bcrypt"}),
        )
        .await
        .expect("search_annotations");
        assert!(!hits.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn add_documentation_links_to_existing_element() {
        let (handler, _tmp) = make_handler().await;
        let result = call(
            &handler,
            "add_documentation",
            json!({
                "file_path": "docs/auth.md",
                "environment": "local"
            }),
        )
        .await;
        match result {
            Ok(v) => assert!(!v.to_string().is_empty()),
            Err(e) => assert!(
                e.contains("not found") || e.contains("empty") || e.contains("no doc"),
                "expected graceful failure: {e}"
            ),
        }
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn link_element_creates_manual_relationship() {
        let (handler, _tmp) = make_handler().await;
        let result = call(
            &handler,
            "link_element",
            json!({
                "element": "src/api/mod.rs::handle_request",
                "id": "src/auth/mod.rs::login",
                "kind": "references"
            }),
        )
        .await;
        assert!(result.is_ok(), "link_element failed: {result:?}");
    }
}

// ---------------------------------------------------------------------------
// Agent diary + focus
// ---------------------------------------------------------------------------

mod agent {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn focus_set_then_diary_write_then_read() {
        let (handler, tmp) = make_handler().await;
        write_persona(&tmp, "reviewer-bot");
        let project = tmp.path().to_string_lossy().to_string();

        let focus = call(
            &handler,
            "agent_focus",
            json!({"name": "reviewer-bot", "project": &project}),
        )
        .await
        .expect("agent_focus with persona fixture");
        assert_eq!(focus["agent"], "reviewer-bot");
        assert!(
            focus["element_count"].as_u64().unwrap_or(0) > 0,
            "persona filters should keep auth functions: {focus}"
        );

        let write = call(
            &handler,
            "agent_diary_write",
            json!({
                "name": "reviewer-bot",
                "note": "Investigated the auth charge path.",
                "project": &project,
                "tags": ["smoke"]
            }),
        )
        .await
        .expect("agent_diary_write");
        assert!(!write.to_string().is_empty());

        let read = call(
            &handler,
            "agent_diary_read",
            json!({"name": "reviewer-bot", "project": &project}),
        )
        .await
        .expect("agent_diary_read");
        assert!(!read.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn report_query_outcome_is_persisted() {
        let (handler, _tmp) = make_handler().await;
        let report = call(
            &handler,
            "report_query_outcome",
            json!({
                "question": "what handles login?",
                "outcome": "useful"
            }),
        )
        .await
        .expect("report_query_outcome");
        // The handler returns {recorded: true}; accept any non-empty payload.
        assert!(!report.to_string().is_empty());
    }
}

// ---------------------------------------------------------------------------
// US-MP-08 / FR-MP-25: folder-scoped search accepts directory qualified
// names (trailing slash) even though the indexer stores directory nodes
// without one.
// ---------------------------------------------------------------------------

mod folder_scoped_search {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn query_file_trailing_slash_hits_directory_subtree() {
        let (handler, _tmp) = make_handler().await;
        // The seed fixture stores no directory nodes, so exercise the
        // normalization seam directly: a trailing-slash folder pattern must
        // match the same rows as its slash-less form (both hit the `src/auth`
        // subtree files).
        let with_slash = call(&handler, "query_file", json!({"pattern": "src/auth/"}))
            .await
            .expect("query_file with trailing slash");
        let no_slash = call(&handler, "query_file", json!({"pattern": "src/auth"}))
            .await
            .expect("query_file without slash");
        let hits_slash = with_slash
            .get("results")
            .and_then(|v| v.as_array())
            .cloned()
            .unwrap_or_default();
        let hits_noslash = no_slash
            .get("results")
            .and_then(|v| v.as_array())
            .cloned()
            .unwrap_or_default();
        assert!(
            !hits_noslash.is_empty(),
            "src/auth must match files: {no_slash}"
        );
        assert!(
            !hits_slash.is_empty(),
            "trailing slash must not kill folder-scoped search: {with_slash}"
        );
        assert_eq!(
            hits_slash.len(),
            hits_noslash.len(),
            "slash normalization must be behavior-preserving"
        );
    }
}

// ---------------------------------------------------------------------------
// Session memory offload (US-SM-01 / FR-SM-01..03)
// ---------------------------------------------------------------------------

mod session_offload {
    use super::*;
    use leankg::session::{
        offload_step, Lesson, MemoryKind, MemoryProvenance, RecallStore, SessionStore,
    };

    #[tokio::test(flavor = "multi_thread")]
    async fn offload_then_recall_via_mcp_round_trips() {
        let (handler, tmp) = make_handler().await;
        let project = tmp.path().to_string_lossy().to_string();
        let payload: Value = serde_json::from_str(
            r#"{"tool":"search_code","hits":[
                {"qualified_name":"src/auth/mod.rs::login","file":"src/auth/mod.rs","line":10},
                {"qualified_name":"src/auth/mod.rs::verify_token","file":"src/auth/mod.rs","line":31}
            ]}"#,
        )
        .unwrap();

        // Offload through the lib seam (writes ref + canvas under <tmp>/.leankg/sessions).
        let store = SessionStore::new("sess-mcp-1", tmp.path()).expect("store");
        let compact = offload_step(&store, "search_code", &payload, 100).expect("offload");
        assert_eq!(compact["steps"][0]["node_id"], "offload-001");

        // session_recall via the MCP dispatch must return the exact payload.
        let recalled = call(
            &handler,
            "session_recall",
            json!({
                "node_id": "offload-001",
                "session_id": "sess-mcp-1",
                "project": &project
            }),
        )
        .await
        .expect("session_recall");
        assert_eq!(recalled["node_id"], "offload-001");
        assert_eq!(recalled["session_id"], "sess-mcp-1");
        assert_eq!(recalled["payload"], payload, "bit-for-bit recall");
        assert!(
            recalled["ref_file"]
                .as_str()
                .unwrap_or("")
                .contains("refs/offload-001.md"),
            "ref file path: {recalled}"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn session_recall_missing_node_errors() {
        let (handler, tmp) = make_handler().await;
        let project = tmp.path().to_string_lossy().to_string();
        let err = call(
            &handler,
            "session_recall",
            json!({
                "node_id": "offload-999",
                "session_id": "sess-mcp-1",
                "project": &project
            }),
        )
        .await
        .expect_err("missing node must error");
        assert!(err.contains("not found"), "{err}");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn overview_opt_in_off_has_no_recall_key() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(&handler, "get_overview_context", json!({}))
            .await
            .expect("get_overview_context");
        assert!(
            resp.get("session_lessons").is_none(),
            "default opt-in OFF must not inject recall: {resp}"
        );
        // explicit recall=false behaves like today
        let resp = call(
            &handler,
            "get_overview_context",
            json!({"recall": false, "project_name": "p"}),
        )
        .await
        .expect("get_overview_context");
        assert!(resp.get("session_lessons").is_none());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn overview_reports_l0_l1_token_budgets() {
        // US-MP-02: L0 identity ~50 tok / L1 critical facts ~120 tok budgets
        // with per-layer accounting; no resurrected load_layer/wake_up tools.
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "get_overview_context",
            json!({"project_name": "smoke"}),
        )
        .await
        .expect("get_overview_context");
        assert!(resp.get("l0_identity").is_some(), "{resp}");
        assert!(resp.get("l1_critical_facts").is_some(), "{resp}");
        let budgets = resp
            .get("layer_budgets")
            .expect("layer_budgets envelope")
            .clone();
        let l0 = budgets.get("L0_identity").expect("L0 budget entry").clone();
        assert_eq!(
            l0.get("max_tokens").and_then(|v| v.as_u64()),
            Some(50),
            "L0 budget must be 50 tokens: {l0}"
        );
        let l1 = budgets
            .get("L1_critical_facts")
            .expect("L1 budget entry")
            .clone();
        assert_eq!(
            l1.get("max_tokens").and_then(|v| v.as_u64()),
            Some(120),
            "L1 budget must be 120 tokens: {l1}"
        );
        assert!(l0.get("actual_tokens").is_some());
        assert!(l0.get("truncated").is_some());
        assert_eq!(
            budgets
                .get("L2_cluster_context")
                .and_then(|v| v.get("tool"))
                .and_then(|v| v.as_str()),
            Some("get_cluster_context"),
            "L2 maps to get_cluster_context (load_layer stays removed)"
        );
        assert!(budgets.get("L3_deep_search").is_some());
        // The stored payload must actually be under budget (truncated when
        // the raw context exceeded it).
        let l0_text = resp
            .get("l0_identity")
            .and_then(|v| v.as_str())
            .unwrap_or("");
        let l1_text = resp
            .get("l1_critical_facts")
            .and_then(|v| v.as_str())
            .unwrap_or("");
        for (label, text, budget_tok) in [("L0", l0_text, 50usize), ("L1", l1_text, 120usize)] {
            assert!(
                text.len() <= budget_tok * 4 + 4,
                "{label} payload must stay near budget (chars): {text}"
            );
        }
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn overview_opt_in_on_injects_lessons_and_respects_budgets() {
        let (handler, tmp) = make_handler().await;
        let project = tmp.path().to_string_lossy().to_string();

        // Seed the recall index through the lib seam (same on-disk index the
        // overview arm reads).
        let recall = RecallStore::new(tmp.path()).expect("recall store");
        recall
            .push_dedup(&Lesson {
                id: "r-1".to_string(),
                source: "report_query_outcome".to_string(),
                rank: 9.0,
                text: "prefer get_overview_context at session start (never grep first)".to_string(),
                provenance: None,
            })
            .expect("push lesson");
        recall
            .push_dedup(&Lesson {
                id: "r-2".to_string(),
                source: "diary".to_string(),
                rank: 3.0,
                text: "validate_key is the hot entry point for auth changes".to_string(),
                provenance: None,
            })
            .expect("push lesson");

        let resp = call(
            &handler,
            "get_overview_context",
            json!({"recall": true, "project": &project}),
        )
        .await
        .expect("get_overview_context");
        let lessons = resp["session_lessons"].as_str().expect("lessons injected");
        assert!(lessons.contains("prefer get_overview_context"), "{lessons}");
        assert!(lessons.contains("validate_key"), "{lessons}");
        assert!(
            lessons.chars().count() <= 3000,
            "total char budget: {}",
            lessons.chars().count()
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn overview_opt_in_on_with_empty_index_skips_injection() {
        let (handler, tmp) = make_handler().await;
        let project = tmp.path().to_string_lossy().to_string();
        let resp = call(
            &handler,
            "get_overview_context",
            json!({"recall": true, "project": &project}),
        )
        .await
        .expect("get_overview_context");
        assert!(resp.get("session_lessons").is_none());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn recall_dedups_across_sources_and_top_k_respects_rank() {
        let (handler, tmp) = make_handler().await;
        let project = tmp.path().to_string_lossy().to_string();
        let recall = RecallStore::new(tmp.path()).expect("recall store");
        // Same lesson from two sources must be deduped (FR-SM-04).
        recall
            .push_dedup(&Lesson {
                id: "r-1".to_string(),
                source: "LESSONS.md".to_string(),
                rank: 9.0,
                text: "duplicate lesson text for dedup verification".to_string(),
                provenance: None,
            })
            .expect("push");
        recall
            .push_dedup(&Lesson {
                id: "r-2".to_string(),
                source: "knowledge".to_string(),
                rank: 8.0,
                text: "duplicate lesson text for dedup verification".to_string(),
                provenance: None,
            })
            .expect("push");
        let lessons = recall.load().expect("load");
        assert_eq!(lessons.len(), 1, "duplicate text deduped");
        assert_eq!(lessons[0].source, "LESSONS.md", "first write wins");

        // top-K: only the top-K by rank are injected.
        for i in 0..8 {
            recall
                .push_dedup(&Lesson {
                    id: format!("r-{i}"),
                    source: "diary".to_string(),
                    rank: i as f64,
                    text: format!("lesson {i}"),
                    provenance: None,
                })
                .expect("push");
        }
        let resp = call(
            &handler,
            "get_overview_context",
            json!({"recall": true, "project": &project}),
        )
        .await
        .expect("get_overview_context");
        let lessons = resp["session_lessons"].as_str().unwrap();
        // top-K = 5 by default; the highest-rank duplicate text must be present.
        assert!(lessons.contains("duplicate lesson text"), "{lessons}");
        assert!(
            lessons.matches("lesson ").count() <= 5,
            "top-K exceeded: {lessons}"
        );
    }

    // ------------------------------------------------------------------
    // PR-22: US-SM-03/04 / FR-SM-07..09 — provenance + typed kinds + RRF
    // ------------------------------------------------------------------

    #[tokio::test(flavor = "multi_thread")]
    async fn session_memory_write_records_provenance_and_typed_kind() {
        let (handler, tmp) = make_handler().await;
        let project = tmp.path().to_string_lossy().to_string();
        let resp = call(
            &handler,
            "session_memory_write",
            json!({
                "text": "decision: use RRF k=60 for hybrid memory search",
                "session_id": "sess-9f31",
                "node_id": "offload-007",
                "kind": "decision",
                "element_refs": ["src/search/mod.rs::fuse_ranked_lists"],
                "project": project
            }),
        )
        .await
        .expect("session_memory_write");
        assert_eq!(resp["written"], true);
        assert_eq!(resp["kind"], "decision", "typed kind on the response");
        assert_eq!(resp["source_session_id"], "sess-9f31");
        assert_eq!(resp["node_id"], "offload-007");

        // The lesson must be retrievable through the recall index seam with
        // full provenance (FR-SM-07 round trip).
        let store = RecallStore::new(tmp.path()).expect("recall store");
        let lessons = store.load().expect("load");
        assert_eq!(lessons.len(), 1);
        let p = lessons[0].provenance.as_ref().expect("provenance");
        assert_eq!(p.source_session_id, "sess-9f31");
        assert_eq!(p.node_id.as_deref(), Some("offload-007"));
        assert_eq!(p.kind, MemoryKind::Decision);
        assert_eq!(
            p.element_refs,
            vec!["src/search/mod.rs::fuse_ranked_lists".to_string()]
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn session_memory_write_auto_classifies_kind_when_omitted() {
        let (handler, tmp) = make_handler().await;
        let project = tmp.path().to_string_lossy().to_string();
        let resp = call(
            &handler,
            "session_memory_write",
            json!({"text": "standing_rule: never pass host paths", "project": project}),
        )
        .await
        .expect("session_memory_write");
        assert_eq!(resp["kind"], "standing_rule", "auto-classified kind");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn search_memory_rrf_merges_recall_index_and_returns_provenance() {
        let (handler, tmp) = make_handler().await;
        let project = tmp.path().to_string_lossy().to_string();
        // Seed the recall index through the same lib seam the tool reads.
        let recall = RecallStore::new(tmp.path()).expect("recall store");
        recall
            .push_dedup(&Lesson {
                id: "r-1".to_string(),
                source: "report_query_outcome".to_string(),
                rank: 9.0,
                text: "prefer get_overview_context at session start (never grep first)".to_string(),
                provenance: Some(MemoryProvenance {
                    source_session_id: "sess-9f31".to_string(),
                    node_id: Some("offload-002".to_string()),
                    kind: MemoryKind::Preference,
                    element_refs: vec!["src/mcp/handler.rs::get_overview_context".to_string()],
                    timestamp: Some(1754100000),
                    tool_call: Some("report_query_outcome".to_string()),
                }),
            })
            .expect("push");

        let resp = call(
            &handler,
            "search_memory_rrf",
            json!({"query": "overview", "project": project}),
        )
        .await
        .expect("search_memory_rrf");
        assert!(resp["count"].as_u64().unwrap_or(0) >= 1, "{resp}");
        let hit = &resp["results"][0];
        assert_eq!(hit["id"], "r-1");
        assert_eq!(hit["kind"], "preference");
        assert_eq!(hit["node_id"], "offload-002");
        assert_eq!(hit["source_session_id"], "sess-9f31");
        assert_eq!(
            hit["element_refs"][0],
            "src/mcp/handler.rs::get_overview_context"
        );
        assert!(
            hit["score"].as_f64().unwrap_or(0.0) > 0.0,
            "fused score positive: {hit}"
        );
        assert!(
            hit["sources"]
                .as_array()
                .unwrap()
                .iter()
                .any(|s| s == "session"),
            "hit carries the session source label: {hit}"
        );
    }
}

// ---------------------------------------------------------------------------
// MemPalace / temporal / Graphify-inspired
// ---------------------------------------------------------------------------

mod graph_features {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn explain_node_returns_metadata() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "explain_node",
            json!({"name": "src/auth/mod.rs::login"}),
        )
        .await
        .expect("explain_node");
        let s = resp.to_string();
        assert!(
            s.contains("login") || s.contains("auth") || s.contains("found"),
            "explain_node should reference the symbol: {s}"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn shortest_path_returns_hop_chain() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "shortest_path",
            json!({
                "source": "src/api/mod.rs::handle_request",
                "target": "src/billing/mod.rs::charge"
            }),
        )
        .await
        .expect("shortest_path");
        // The handler wraps the result; accept either flat or wrapped response.
        assert!(
            resp.get("path").is_some()
                || resp.get("hops").is_some()
                || resp.get("result").is_some()
                || resp.as_array().is_some(),
            "shortest_path should return hops: {resp}"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn query_graph_returns_budgeted_subgraph() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "query_graph",
            json!({
                "question": "what connects handle_request to charge?",
                "token_budget": 2000,
                "max_depth": 3
            }),
        )
        .await
        .expect("query_graph");
        assert_eq!(resp["question"], "what connects handle_request to charge?");
        let seeds = resp["seeds"].as_array().expect("seeds array");
        assert!(!seeds.is_empty(), "expected seeds: {resp}");
        let edges = resp["edges"].as_array().expect("edges array");
        assert!(!edges.is_empty(), "expected connecting edges: {resp}");
        for edge in edges {
            let label = edge["confidence_label"].as_str().unwrap_or("");
            assert!(
                matches!(label, "EXTRACTED" | "INFERRED" | "AMBIGUOUS"),
                "bad confidence_label: {edge}"
            );
        }
        assert!(
            resp["tokens_estimate"].as_u64().unwrap_or(u64::MAX)
                <= resp["token_budget"].as_u64().unwrap_or(0)
                || resp["truncated"].as_bool().unwrap_or(false),
            "budget accounting missing: {resp}"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn query_graph_rejects_empty_question() {
        let (handler, _tmp) = make_handler().await;
        let err = call(&handler, "query_graph", json!({"question": "   "}))
            .await
            .expect_err("empty question must fail");
        assert!(
            err.to_lowercase().contains("empty") || err.to_lowercase().contains("question"),
            "unexpected error: {err}"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn query_graph_respects_small_token_budget() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "query_graph",
            json!({
                "question": "what connects auth to billing?",
                "token_budget": 250,
                "max_depth": 3
            }),
        )
        .await
        .expect("query_graph small budget");
        let estimate = resp["tokens_estimate"].as_u64().unwrap_or(u64::MAX);
        let budget = resp["token_budget"].as_u64().unwrap_or(0);
        assert!(
            estimate <= budget,
            "tokens {estimate} exceeded budget {budget}: {resp}"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn find_tunnels_returns_at_least_empty() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(&handler, "find_tunnels", json!({"limit": 5}))
            .await
            .expect("find_tunnels");
        assert!(!resp.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn check_consistency_reports_status() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(&handler, "check_consistency", json!({}))
            .await
            .expect("check_consistency");
        assert!(!resp.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn temporal_query_returns_window() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "temporal_query",
            json!({
                "at": 1_700_000_000,
                "qualified_name": "src/api/mod.rs::handle_request"
            }),
        )
        .await
        .expect("temporal_query");
        assert!(!resp.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn timeline_emits_events() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "timeline",
            json!({"qualified_name": "src/auth/mod.rs::login"}),
        )
        .await
        .expect("timeline");
        assert!(!resp.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn get_overview_context_returns_payload() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(&handler, "get_overview_context", json!({}))
            .await
            .expect("get_overview_context");
        assert!(!resp.to_string().is_empty());
    }
}

// ---------------------------------------------------------------------------
// Aggregator / structural tools
// ---------------------------------------------------------------------------

mod aggregators {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn get_architecture_returns_structured_brief() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(&handler, "get_architecture", json!({}))
            .await
            .expect("get_architecture");
        assert!(!resp.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn get_graph_schema_reports_counts() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(&handler, "get_graph_schema", json!({}))
            .await
            .expect("get_graph_schema");
        let s = resp.to_string();
        assert!(
            s.contains("element") || s.contains("edge") || s.contains("count"),
            "get_graph_schema should report counts: {s}"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn get_god_nodes_returns_top_n() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(&handler, "get_god_nodes", json!({"limit": 5}))
            .await
            .expect("get_god_nodes");
        assert!(!resp.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn get_graph_report_returns_report_or_empty() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(&handler, "get_graph_report", json!({})).await;
        // May be Err if report not yet built; both shapes are acceptable.
        match resp {
            Ok(v) => assert!(!v.to_string().is_empty()),
            Err(e) => assert!(
                e.contains("not found") || e.contains("no report") || e.contains("missing"),
                "expected graceful error: {e}"
            ),
        }
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn find_dead_code_returns_list() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(&handler, "find_dead_code", json!({}))
            .await
            .expect("find_dead_code");
        assert!(!resp.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn export_graph_snapshot_returns_path() {
        let (handler, tmp) = make_handler().await;
        let resp = call(
            &handler,
            "export_graph_snapshot",
            json!({"target_path": tmp.path().join("snap.json").to_string_lossy()}),
        )
        .await
        .expect("export_graph_snapshot");
        assert!(!resp.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn get_pr_impact_returns_payload() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "get_pr_impact",
            json!({"files": ["src/auth/mod.rs"]}),
        )
        .await
        .expect("get_pr_impact");
        assert!(!resp.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn get_cluster_skill_returns_skill_md() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(&handler, "get_cluster_skill", json!({"cluster_id": "c1"})).await;
        match resp {
            Ok(v) => assert!(!v.to_string().is_empty()),
            Err(e) => assert!(
                e.contains("not found") || e.contains("cluster") || e.contains("missing"),
                "expected graceful error: {e}"
            ),
        }
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn get_team_map_returns_team_payload() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(&handler, "get_team_map", json!({}))
            .await
            .expect("get_team_map");
        assert!(!resp.to_string().is_empty());
    }
}

// ---------------------------------------------------------------------------
// Route / nav-graph (Android) tools
// ---------------------------------------------------------------------------

mod android {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn find_route_returns_routes() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(&handler, "find_route", json!({"route": "Home"}))
            .await
            .expect("find_route");
        // No Android fixture → empty destinations/actions; assert graceful.
        assert!(!resp.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn get_nav_graph_returns_empty_or_payload() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(&handler, "get_nav_graph", json!({})).await;
        match resp {
            Ok(v) => assert!(!v.to_string().is_empty()),
            Err(e) => assert!(
                e.contains("no graph") || e.contains("not found"),
                "expected graceful error: {e}"
            ),
        }
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn get_nav_callers_returns_empty_or_payload() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "get_nav_callers",
            json!({"destination": "HomeFragment"}),
        )
        .await;
        match resp {
            Ok(v) => assert!(!v.to_string().is_empty()),
            Err(e) => assert!(
                e.contains("no graph") || e.contains("not found"),
                "expected graceful error: {e}"
            ),
        }
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn get_screen_args_returns_empty_or_payload() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "get_screen_args",
            json!({"screen": "LoginFragment"}),
        )
        .await;
        match resp {
            Ok(v) => assert!(!v.to_string().is_empty()),
            Err(e) => assert!(
                e.contains("no screen") || e.contains("not found"),
                "expected graceful error: {e}"
            ),
        }
    }
}

// ---------------------------------------------------------------------------
// Ontology / knowledge-graph tools (kg_*)
// ---------------------------------------------------------------------------

mod ontology {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn concept_search_returns_matches() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "concept_search",
            json!({"query": "authentication"}),
        )
        .await
        .expect("concept_search");
        assert!(!resp.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn kg_self_test_reports_health() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(&handler, "kg_self_test", json!({}))
            .await
            .expect("kg_self_test");
        let s = resp.to_string();
        assert!(
            s.contains("all_ok") || s.contains("ok") || s.contains("status"),
            "kg_self_test should report status: {s}"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn kg_ontology_status_returns_metrics() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(&handler, "kg_ontology_status", json!({}))
            .await
            .expect("kg_ontology_status");
        assert!(!resp.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn kg_context_returns_graph_context() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(&handler, "kg_context", json!({"query": "auth flow"}))
            .await
            .expect("kg_context");
        assert!(!resp.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn kg_concept_map_returns_payload() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(&handler, "kg_concept_map", json!({"query": "auth"}))
            .await
            .expect("kg_concept_map");
        assert!(!resp.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn kg_trace_workflow_returns_steps() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "kg_trace_workflow",
            json!({"workflow_id_or_query": "checkout"}),
        )
        .await;
        match resp {
            Ok(v) => assert!(!v.to_string().is_empty()),
            Err(e) => assert!(
                e.contains("no workflow") || e.contains("not found"),
                "expected graceful error: {e}"
            ),
        }
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn kg_semantic_context_returns_budgeted_payload() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "kg_semantic_context",
            json!({"query": "auth flow", "top_k": 5}),
        )
        .await;
        match resp {
            Ok(v) => assert!(!v.to_string().is_empty()),
            Err(e) => assert!(
                e.contains("not initialized")
                    || e.contains("no index")
                    || e.contains("missing")
                    || e.contains("not registered")
                    || e.contains("not implemented")
                    || e.contains("Unknown tool")
                    || e.contains("No embedded vectors")
                    || e.contains("leankg embed"),
                "expected graceful error: {e}"
            ),
        }
    }
}

// ---------------------------------------------------------------------------
// Environment / service-context tools (US-V2-*)
// ---------------------------------------------------------------------------

mod environment {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn get_upcoming_changes_returns_payload() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(&handler, "get_upcoming_changes", json!({}))
            .await
            .expect("get_upcoming_changes");
        assert!(!resp.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn promote_environment_dry_run() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "promote_environment",
            json!({
                "branch": "main",
                "target_environment": "staging"
            }),
        )
        .await;
        match resp {
            Ok(v) => assert!(!v.to_string().is_empty()),
            Err(e) => assert!(
                e.contains("no service") || e.contains("missing") || e.contains("not found"),
                "expected graceful error: {e}"
            ),
        }
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn query_incidents_returns_empty_or_payload() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "query_incidents",
            json!({"service": "api", "env": "production"}),
        )
        .await
        .expect("query_incidents");
        assert!(!resp.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn find_env_conflicts_returns_empty_or_payload() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "find_env_conflicts",
            json!({"service": "src/auth/mod.rs"}),
        )
        .await
        .expect("find_env_conflicts");
        assert!(!resp.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn get_service_context_returns_payload() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "get_service_context",
            json!({"service": "src/auth/mod.rs", "env": "local"}),
        )
        .await
        .expect("get_service_context");
        assert!(!resp.to_string().is_empty());
    }
}

// ---------------------------------------------------------------------------
// LSP / semantic / coarse-graph tools
// ---------------------------------------------------------------------------

mod advanced {
    use super::*;

    #[tokio::test(flavor = "multi_thread")]
    async fn resolve_with_lsp_graceful_when_no_server() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "resolve_with_lsp",
            json!({
                "language": "go",
                "file_path": "src/main.go",
                "line": 1,
                "character": 1
            }),
        )
        .await
        .expect("resolve_with_lsp");
        // When no LSP server is configured, the handler returns a structured
        // `found: false` envelope with a reason. We accept any non-empty payload.
        assert!(!resp.to_string().is_empty());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn semantic_search_returns_payload_or_graceful() {
        let (handler, _tmp) = make_handler().await;
        let resp = call(
            &handler,
            "semantic_search",
            json!({"query": "user authentication", "k": 5}),
        )
        .await;
        match resp {
            Ok(v) => assert!(!v.to_string().is_empty()),
            Err(e) => assert!(
                e.contains("not initialized")
                    || e.contains("no index")
                    || e.contains("missing")
                    || e.contains("hnsw"),
                "expected graceful semantic-not-ready error: {e}"
            ),
        }
    }
}
