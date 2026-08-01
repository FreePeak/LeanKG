//! PR-61 (US-GE-06 / FR-GE-06): selective LLM pass-2 — planner accepts
//! LLM-provided candidate rankings and merges them deterministically into
//! the rule-based plan. No LLM calls in Rust; YAML remains SoT.

use leankg::graph::planner::{plan_dag, plan_dag_with_hints, PlanEdge, PlanNode, ToolHint};
use leankg::mcp::tools::ToolRegistry;

fn tools() -> Vec<leankg::mcp::tools::ToolDefinition> {
    ToolRegistry::list_tools()
}

fn hint(tool: &str, rank: usize) -> ToolHint {
    ToolHint {
        tool: tool.to_string(),
        rank,
    }
}

// ---------------------------------------------------------------------------
// plan_dag_with_hints: deterministic LLM-hint merge
// ---------------------------------------------------------------------------

#[test]
fn hints_prepend_but_never_duplicate_plan_tools() {
    let hints = vec![hint("get_callers", 1), hint("query_graph", 2)];
    let dag = plan_dag_with_hints(
        "what breaks if I change src/main.rs",
        &tools(),
        None,
        &hints,
    )
    .expect("plan");
    let names: Vec<&str> = dag.nodes.iter().map(|n| n.tool.as_str()).collect();
    // Hint get_callers appears once; query_graph is already the join — no dup.
    assert_eq!(names.iter().filter(|t| **t == "get_callers").count(), 1);
    assert_eq!(names.iter().filter(|t| **t == "query_graph").count(), 1);
    // Hinted tools must come before non-hinted intent tools.
    let callers_idx = names.iter().position(|t| *t == "get_callers").unwrap();
    let context_idx = names.iter().position(|t| *t == "get_context").unwrap();
    assert!(callers_idx < context_idx, "hinted tools must lead");
}

#[test]
fn hints_preserve_their_relative_rank_order() {
    let hints = vec![hint("get_clusters", 1), hint("get_cluster_context", 2)];
    let dag =
        plan_dag_with_hints("overview of the auth module", &tools(), None, &hints).expect("plan");
    let positions: Vec<(usize, &str)> = dag
        .nodes
        .iter()
        .enumerate()
        .filter(|(_, n)| n.tool == "get_clusters" || n.tool == "get_cluster_context")
        .map(|(i, n)| (i, n.tool.as_str()))
        .collect();
    assert_eq!(positions.len(), 2);
    assert!(
        positions[0].0 < positions[1].0 && positions[0].1 == "get_clusters",
        "rank 1 hint must precede rank 2 hint, got {positions:?}"
    );
}

#[test]
fn hints_never_emit_unavailable_tools() {
    let mut available = tools();
    available.retain(|t| t.name != "get_callers");
    let hints = vec![hint("get_callers", 1), hint("get_traceability", 2)];
    let dag = plan_dag_with_hints("find god nodes", &available, None, &hints).expect("plan");
    assert!(
        !dag.nodes.iter().any(|n| n.tool == "get_callers"),
        "unavailable hinted tool must be dropped"
    );
    assert!(
        dag.nodes.iter().any(|n| n.tool == "get_traceability"),
        "available hinted tool must be kept"
    );
}

#[test]
fn hints_unknown_tool_dropped_without_error() {
    let hints = vec![hint("does_not_exist_tool", 1)];
    let dag = plan_dag_with_hints("find god nodes", &tools(), None, &hints).expect("plan");
    assert!(!dag.nodes.iter().any(|n| n.tool == "does_not_exist_tool"));
}

#[test]
fn no_hints_is_exactly_rule_plan() {
    let plain = plan_dag("find god nodes", &tools(), None).expect("plain");
    let hinted = plan_dag_with_hints("find god nodes", &tools(), None, &[]).expect("hinted");
    assert_eq!(plain, hinted, "empty hints must not change the plan");
}

#[test]
fn empty_goal_with_hints_is_empty_dag() {
    let hints = vec![hint("get_callers", 1)];
    let dag = plan_dag_with_hints("", &tools(), None, &hints).expect("plan");
    assert!(dag.nodes.is_empty() && dag.edges.is_empty());
}

#[test]
fn hinted_dag_keeps_single_join_and_json_contract() {
    let hints = vec![hint("get_traceability", 1)];
    let dag = plan_dag_with_hints("trace requirement FR-01", &tools(), None, &hints).expect("plan");
    let joins: Vec<&PlanNode> = dag.nodes.iter().filter(|n| n.join).collect();
    assert_eq!(joins.len(), 1, "exactly one join node");
    let json = dag.to_json();
    let v: serde_json::Value = serde_json::from_str(&json).expect("valid JSON");
    assert!(v["nodes"].is_array() && v["edges"].is_array());
    assert_eq!(v["goal"], "trace requirement FR-01");
}

#[test]
fn hinted_dag_is_deterministic() {
    let hints = vec![hint("get_callers", 2), hint("semantic_search", 1)];
    let a = plan_dag_with_hints("find god nodes", &tools(), None, &hints).expect("a");
    let b = plan_dag_with_hints("find god nodes", &tools(), None, &hints).expect("b");
    assert_eq!(a, b);
    assert_eq!(a.to_json(), b.to_json());
}

#[test]
fn hint_tools_that_duplicate_join_stay_join() {
    // query_graph as a hint must not create a second join node.
    let hints = vec![hint("query_graph", 1)];
    let dag = plan_dag_with_hints("find dead code", &tools(), None, &hints).expect("plan");
    let joins: Vec<&PlanNode> = dag.nodes.iter().filter(|n| n.join).collect();
    assert_eq!(joins.len(), 1);
    assert_eq!(joins[0].tool, "query_graph");
}

#[test]
fn hint_edges_flow_into_join() {
    let hints = vec![hint("get_traceability", 1), hint("find_related_docs", 2)];
    let dag = plan_dag_with_hints("trace FR-01", &tools(), None, &hints).expect("plan");
    let join_idx = dag.nodes.iter().position(|n| n.join).expect("join");
    // Every non-join node must reach the join via an edge.
    for n in &dag.nodes {
        if n.id as usize == join_idx {
            continue;
        }
        assert!(
            dag.edges
                .iter()
                .any(|e| e.from == n.id && e.to == join_idx as u32),
            "node {} must feed the join, edges: {:?}",
            n.tool,
            dag.edges
        );
    }
}

#[test]
fn plan_edge_type_importable() {
    // Compile-time sanity: PlanEdge/PlanNode exported with the hint API.
    let dag = plan_dag_with_hints("find god nodes", &tools(), None, &[hint("get_callers", 1)])
        .expect("plan");
    let _e: Option<&PlanEdge> = dag.edges.first();
    let _n: Option<&PlanNode> = dag.nodes.first();
}
