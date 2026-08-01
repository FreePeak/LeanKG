// US-GE-02 / FR-GE-02: graph-aware planner.
// Goal string -> DAG of MCP tool steps with a join over the shared graph.
// Pure, deterministic, rule-based (keyword match over the MCP tool catalog).
// No LLM calls. The harness (Cursor/Claude) executes the emitted DAG.
//
// US-GE-06 / FR-GE-06: selective LLM pass-2 — the planner accepts optional
// LLM-provided tool candidate rankings (`ToolHint`) and merges them into the
// rule plan deterministically. The harness supplies the hints; LeanKG never
// calls an LLM and YAML remains the source of truth.

use crate::mcp::tools::ToolDefinition;
use serde::Serialize;

/// One MCP tool step in the plan.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct PlanNode {
    pub id: u32,
    pub tool: String,
    /// A concrete argument to pass (extracted from the goal or tool-typical).
    pub input: String,
    /// Execution stage; nodes sharing a stage may run in parallel.
    pub stage: u32,
    /// True when this node is the graph join point (shared element/relationship queries).
    pub join: bool,
}

/// Data-flow edge: `from` output feeds `to` input.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct PlanEdge {
    pub from: u32,
    pub to: u32,
    pub flow: String,
}

/// The plan DAG. Serialized as the FR-GE-02 JSON contract.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct PlanDag {
    pub goal: String,
    pub project: Option<String>,
    pub nodes: Vec<PlanNode>,
    pub edges: Vec<PlanEdge>,
    /// Text description of the join point over the shared graph.
    pub join: Option<String>,
    /// True when the goal matched no known intent (best-effort overview plan).
    pub best_effort: bool,
}

impl PlanDag {
    /// Serialize the DAG to the JSON contract emitted to the harness.
    pub fn to_json(&self) -> String {
        serde_json::to_string_pretty(self).unwrap_or_default()
    }
}

/// A rule: keyword tokens matching a goal -> ordered tool plan with a join.
struct Rule<'a> {
    keys: &'a [&'a str],
    tools: &'a [&'a str],
    join_desc: &'a str,
}

const RULES: &[Rule] = &[
    Rule {
        keys: &[
            "god",
            "hub",
            "central",
            "most-connected",
            "most connected",
            "connected",
        ],
        tools: &["get_god_nodes", "query_graph"],
        join_desc: "query_graph joins god-node candidates against shared graph relationships",
    },
    Rule {
        keys: &["dead", "unused", "orphan"],
        tools: &["find_dead_code", "get_callers", "query_graph"],
        join_desc:
            "query_graph joins dead-code candidates with caller neighborhoods from the shared graph",
    },
    Rule {
        keys: &[
            "impact",
            "breaking",
            "what breaks",
            "change",
            "refactor",
            "break",
        ],
        tools: &[
            "get_context",
            "get_impact_radius",
            "get_dependents",
            "get_dependencies",
            "query_graph",
        ],
        join_desc: "query_graph joins dependents/dependencies into a shared change-impact subgraph",
    },
    Rule {
        keys: &["test", "coverage", "tested"],
        tools: &["query_file", "get_tested_by", "query_graph"],
        join_desc: "query_graph joins tested_by edges with the element's shared graph neighborhood",
    },
    Rule {
        keys: &["trace", "requirement", "traceability", "fr-", "us-", "doc"],
        tools: &[
            "search_by_requirement",
            "get_traceability",
            "get_files_for_doc",
            "find_related_docs",
            "query_graph",
        ],
        join_desc: "query_graph joins traceability chains and doc refs over shared elements",
    },
    Rule {
        keys: &[
            "call",
            "caller",
            "callee",
            "depend",
            "who calls",
            "what calls",
        ],
        tools: &["get_callers", "get_call_graph", "query_graph"],
        join_desc: "query_graph joins caller/callee hops into a shared call subgraph",
    },
    Rule {
        keys: &[
            "where is",
            "where are",
            "find",
            "implemented",
            "which",
            "what is",
            "how does",
        ],
        tools: &[
            "semantic_search",
            "concept_search",
            "query_file",
            "query_graph",
        ],
        join_desc:
            "query_graph joins search hits into a shared subgraph around the discovered seeds",
    },
    Rule {
        keys: &["cluster", "module", "architecture", "overview", "component"],
        tools: &[
            "get_architecture",
            "get_clusters",
            "get_cluster_context",
            "query_graph",
        ],
        join_desc: "query_graph joins cluster neighborhoods over the shared graph",
    },
    Rule {
        keys: &["large", "complex", "long function", "big function"],
        tools: &["find_large_functions", "get_context", "query_graph"],
        join_desc: "query_graph joins large-function candidates with their shared graph context",
    },
];

/// One LLM-supplied tool candidate (US-GE-06 / FR-GE-06). Rank 1 = most
/// preferred; hints are merged deterministically — never called from Rust.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct ToolHint {
    pub tool: String,
    /// 1 = strongest preference; ties break by tool name.
    pub rank: usize,
}

/// Tools that always appear at the head of a plan (before intent-specific nodes).
const PREFIX_TOOLS: &[&str] = &["get_overview_context"];

/// Search tools that fan out in parallel (same stage).
const PARALLEL_GROUP: &[&str] = &["semantic_search", "concept_search"];

fn has(goal: &str, keys: &[&str]) -> bool {
    let g = goal.to_lowercase();
    keys.iter().any(|k| g.contains(k))
}

/// Turn a natural-language goal into a DAG of MCP tool steps.
/// Deterministic: same goal + same catalog -> same DAG.
/// Unknown goals still yield a best-effort plan (overview + graph join).
/// Empty/blank goals yield an empty DAG.
pub fn plan_dag(
    goal: &str,
    available: &[ToolDefinition],
    project: Option<&str>,
) -> Option<PlanDag> {
    plan_dag_with_hints(goal, available, project, &[])
}

/// US-GE-06 / FR-GE-06: plan with optional LLM-supplied tool hints.
/// Hints are merged deterministically: known tools only (unavailable or
/// prefix tools are dropped); no duplicates (a hinted tool already in the
/// rule plan appears once); hinted tools lead the intent stage ordered by
/// rank then tool name; empty hints equal plain [`plan_dag`] (bit-for-bit
/// identical output). Pure — no LLM calls, no I/O.
pub fn plan_dag_with_hints(
    goal: &str,
    available: &[ToolDefinition],
    project: Option<&str>,
    hints: &[ToolHint],
) -> Option<PlanDag> {
    let goal = goal.trim();
    if goal.is_empty() {
        return Some(PlanDag {
            goal: String::new(),
            project: project.map(|p| p.to_string()),
            nodes: Vec::new(),
            edges: Vec::new(),
            join: None,
            best_effort: false,
        });
    }

    let names: Vec<&str> = available.iter().map(|t| t.name.as_str()).collect();
    let matched = RULES.iter().find(|r| has(goal, r.keys));
    let picks: Vec<&str> = match matched {
        Some(r) => r.tools.to_vec(),
        None => vec!["get_overview_context", "query_graph"],
    };
    // Only emit tools that exist in the supplied catalog, and skip tools the
    // prefix stage already emitted (avoid duplicate steps).
    let picks: Vec<&str> = picks
        .into_iter()
        .filter(|t| names.contains(t) && !PREFIX_TOOLS.contains(t))
        .collect();

    // FR-GE-06: merge LLM hints — known, non-prefix tools only; hinted tools
    // lead the intent stage in (rank, tool-name) order; rule-plan tools that
    // are NOT hinted follow in rule order. Dedup keeps one node per tool.
    let mut hinted: Vec<String> = Vec::new();
    if !hints.is_empty() {
        let mut sorted_hints: Vec<ToolHint> = hints.to_vec();
        sorted_hints.sort_by(|a, b| a.rank.cmp(&b.rank).then_with(|| a.tool.cmp(&b.tool)));
        for h in sorted_hints {
            if names.contains(&h.tool.as_str())
                && !PREFIX_TOOLS.contains(&h.tool.as_str())
                && !picks.contains(&h.tool.as_str())
                && !hinted.iter().any(|t| t == &h.tool)
            {
                hinted.push(h.tool.clone());
            }
        }
        let rest: Vec<&str> = picks
            .iter()
            .copied()
            .filter(|t| !hinted.iter().any(|h| h == t))
            .collect();
        let mut merged: Vec<String> = Vec::with_capacity(hinted.len() + rest.len());
        merged.extend(hinted.iter().cloned());
        merged.extend(rest.iter().map(|t| t.to_string()));
        let picks_ref: Vec<&str> = merged.iter().map(|s| s.as_str()).collect();
        return build_dag(goal, project, matched, &picks_ref, names.as_slice());
    }

    build_dag(goal, project, matched, &picks, names.as_slice())
}

/// Shared DAG builder: prefix stage, intent stage, single join, data-flow
/// edges into the join. `picks` is the final (already filtered) tool list.
fn build_dag(
    goal: &str,
    project: Option<&str>,
    matched: Option<&Rule>,
    picks: &[&str],
    names: &[&str],
) -> Option<PlanDag> {
    let mut nodes: Vec<PlanNode> = Vec::new();
    let mut edges: Vec<PlanEdge> = Vec::new();
    let mut stage = 1u32;
    let mut prev_stage_last: Option<u32> = None;

    let mut push = |tool: &str, input: &str, join: bool, new_stage: bool| {
        let id = nodes.len() as u32;
        if new_stage {
            prev_stage_last = nodes.last().map(|n| n.id);
            stage += 1;
        }
        nodes.push(PlanNode {
            id,
            tool: tool.to_string(),
            input: input.to_string(),
            stage,
            join,
        });
        if let Some(src) = prev_stage_last {
            if src != id {
                edges.push(PlanEdge {
                    from: src,
                    to: id,
                    flow: format!("{} output feeds {} input", nodes[src as usize].tool, tool),
                });
            }
        }
    };

    // Prefix tools — stage 1.
    for t in PREFIX_TOOLS {
        if names.contains(t) {
            push(t, "project overview", false, false);
        }
    }

    // Intent-specific tools. Parallel group members share one stage; all else sequential.
    for (i, t) in picks.iter().enumerate() {
        let is_join = i + 1 == picks.len();
        let new_stage = i == 0 || !PARALLEL_GROUP.contains(t);
        push(t, goal, is_join, new_stage);
    }

    // Ensure exactly one join node: the query_graph step, else the last step.
    let join_idx = nodes
        .iter()
        .position(|n| n.tool == "query_graph")
        .unwrap_or_else(|| nodes.len().saturating_sub(1));
    for (i, n) in nodes.iter_mut().enumerate() {
        n.join = i == join_idx;
    }
    // Data-flow edges from every other step into the join (skip pairs already
    // connected by a sequential feed edge).
    for n in &nodes {
        if n.id as usize == join_idx {
            continue;
        }
        let connected = edges
            .iter()
            .any(|e| e.from == n.id && e.to == join_idx as u32);
        if !connected {
            edges.push(PlanEdge {
                from: n.id,
                to: join_idx as u32,
                flow: format!("{} output joins shared graph context", n.tool),
            });
        }
    }

    let join_desc = matched
        .map(|r| r.join_desc)
        .unwrap_or("query_graph joins all step outputs over the shared element/relationship graph")
        .to_string();
    let best_effort = matched.is_none();
    Some(PlanDag {
        goal: goal.to_string(),
        project: project.map(|p| p.to_string()),
        nodes,
        edges,
        join: Some(join_desc),
        best_effort,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mcp::tools::ToolRegistry;

    fn tools() -> Vec<ToolDefinition> {
        ToolRegistry::list_tools()
    }

    #[test]
    fn unit_empty_goal_empty_dag() {
        let dag = plan_dag("", &tools(), None).unwrap();
        assert!(dag.nodes.is_empty() && dag.edges.is_empty());
    }

    #[test]
    fn unit_join_only_uses_available_tools() {
        let mut available = tools();
        available.retain(|t| t.name != "query_graph");
        let dag = plan_dag("find god nodes", &available, None).unwrap();
        assert!(dag.nodes.iter().all(|n| n.tool != "query_graph"));
    }
}
