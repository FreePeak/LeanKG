// US-GE-02 / FR-GE-02: graph-aware planner — goal -> MCP tool DAG with graph join.
// Pure, deterministic, no LLM. Harness (Cursor/Claude) executes the emitted DAG.

use leankg::graph::planner::{plan_dag, PlanEdge, PlanNode};
use leankg::mcp::tools::ToolRegistry;

fn tools() -> Vec<leankg::mcp::tools::ToolDefinition> {
    ToolRegistry::list_tools()
}

#[test]
fn god_nodes_goal_emits_dag_with_join() {
    let dag = plan_dag("find god nodes", &tools(), None).expect("plan must succeed");
    let names: Vec<&str> = dag.nodes.iter().map(|n| n.tool.as_str()).collect();
    assert!(
        names.contains(&"get_god_nodes"),
        "god-node plan must include get_god_nodes, got {names:?}"
    );
    assert!(
        names.contains(&"query_graph"),
        "god-node plan must include query_graph join, got {names:?}"
    );
    let join_nodes: Vec<&PlanNode> = dag.nodes.iter().filter(|n| n.join).collect();
    assert_eq!(join_nodes.len(), 1, "exactly one graph join point");
    assert_eq!(join_nodes[0].tool, "query_graph");
    assert!(
        dag.edges.iter().any(|e| e.from == 1 && e.to == 2),
        "god_nodes output must feed the query_graph join, edges: {:?}",
        dag.edges
    );
}

#[test]
fn god_nodes_goal_join_mentions_shared_graph() {
    let dag = plan_dag("find god nodes", &tools(), None).expect("plan must succeed");
    let join = dag.join.expect("join must be described");
    assert!(
        join.contains("graph") || join.contains("element") || join.contains("shared"),
        "join description must mention the shared graph, got: {join}"
    );
}

#[test]
fn unknown_goal_is_best_effort_plan() {
    let dag = plan_dag("make the website faster", &tools(), None).expect("plan must succeed");
    assert!(dag.best_effort, "unknown goal must be marked best_effort");
    assert!(!dag.nodes.is_empty(), "best-effort plan must not be empty");
    assert!(
        dag.nodes.iter().any(|n| n.tool == "get_overview_context"),
        "best-effort plan should start with overview context"
    );
}

#[test]
fn empty_goal_emits_empty_dag() {
    let dag = plan_dag("", &tools(), None).expect("empty goal must not error");
    assert!(dag.nodes.is_empty());
    assert!(dag.edges.is_empty());
    let dag = plan_dag("   ", &tools(), None).expect("blank goal must not error");
    assert!(dag.nodes.is_empty());
}

#[test]
fn dag_json_schema_is_valid() {
    let dag = plan_dag(
        "what is the impact of changing auth/login.rs",
        &tools(),
        Some("p1"),
    )
    .expect("plan must succeed");
    let json = dag.to_json();
    let v: serde_json::Value = serde_json::from_str(&json).expect("output must be valid JSON");
    assert_eq!(v["goal"], "what is the impact of changing auth/login.rs");
    assert_eq!(v["project"], "p1");
    assert!(v["nodes"].is_array() && !v["nodes"].as_array().unwrap().is_empty());
    assert!(v["edges"].is_array());
    let ids: Vec<i64> = v["nodes"]
        .as_array()
        .unwrap()
        .iter()
        .map(|n| n["id"].as_i64().unwrap())
        .collect();
    let unique: std::collections::HashSet<_> = ids.iter().collect();
    assert_eq!(unique.len(), ids.len(), "node ids must be unique");
    for edge in v["edges"].as_array().unwrap() {
        let from = edge["from"].as_i64().unwrap();
        let to = edge["to"].as_i64().unwrap();
        assert!(
            ids.contains(&from),
            "edge from {from} must reference a node"
        );
        assert!(ids.contains(&to), "edge to {to} must reference a node");
    }
}

#[test]
fn plan_only_uses_available_tools() {
    let mut available = tools();
    available.retain(|t| t.name != "query_graph");
    let dag = plan_dag("find god nodes", &available, None).expect("plan must succeed");
    assert!(
        !dag.nodes.iter().any(|n| n.tool == "query_graph"),
        "plan must not emit unavailable tool query_graph"
    );
    assert!(
        dag.nodes.iter().any(|n| n.tool == "get_god_nodes"),
        "get_god_nodes still available and must be used"
    );
}

#[test]
fn plan_is_deterministic() {
    let a = plan_dag("what breaks if I change src/main.rs", &tools(), None).expect("plan a");
    let b = plan_dag("what breaks if I change src/main.rs", &tools(), None).expect("plan b");
    assert_eq!(a, b, "same goal must produce identical DAG");
    assert_eq!(a.to_json(), b.to_json());
}

#[test]
fn parallel_search_stage_fans_out_before_join() {
    let dag = plan_dag("where is the refund flow implemented", &tools(), None)
        .expect("plan must succeed");
    let search: Vec<&PlanNode> = dag
        .nodes
        .iter()
        .filter(|n| n.tool == "semantic_search" || n.tool == "concept_search")
        .collect();
    assert_eq!(
        search.len(),
        2,
        "search fan-out must run concept_search + semantic_search"
    );
    assert!(
        search[0].stage == search[1].stage,
        "fan-out tools must share a stage for parallel execution"
    );
    let join: Vec<&PlanNode> = dag.nodes.iter().filter(|n| n.join).collect();
    assert_eq!(join.len(), 1);
    assert!(
        join[0].stage > search[0].stage,
        "join must run after the fan-out stage"
    );
}

#[test]
fn edge_carries_data_flow_label() {
    let dag = plan_dag("find god nodes", &tools(), None).expect("plan must succeed");
    assert!(
        dag.edges.iter().all(|e: &PlanEdge| !e.flow.is_empty()),
        "every edge must describe the data flow it carries"
    );
}
