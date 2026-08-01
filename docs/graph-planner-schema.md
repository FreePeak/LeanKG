# Graph-aware planner (US-GE-02 / FR-GE-02) — DAG JSON schema

**PR-30** — branch `prd/ge-planner`. Pure, deterministic, rule-based planner: goal string → DAG of MCP tool steps with a join over the shared graph. No LLM calls. The harness (Cursor/Claude) executes the emitted DAG; LeanKG stays the memory layer.

**Entry point:** `plan_dag(goal: &str, available: &[ToolDefinition], project: Option<&str>) -> Option<PlanDag>` in `src/graph/planner.rs`. `available` = `ToolRegistry::list_tools()` (or a filtered subset); only tools present in the supplied catalog are emitted.

## Contract

```json
{
  "goal": "find god nodes",
  "project": "/workspace",
  "nodes": [
    {
      "id": 0,
      "tool": "get_overview_context",
      "input": "project overview",
      "stage": 1,
      "join": false
    },
    {
      "id": 1,
      "tool": "get_god_nodes",
      "input": "find god nodes",
      "stage": 2,
      "join": false
    },
    {
      "id": 2,
      "tool": "query_graph",
      "input": "find god nodes",
      "stage": 3,
      "join": true
    }
  ],
  "edges": [
    {"from": 0, "to": 1, "flow": "get_overview_context output feeds get_god_nodes input"},
    {"from": 1, "to": 2, "flow": "get_god_nodes output feeds query_graph input"},
    {"from": 0, "to": 2, "flow": "get_overview_context output joins shared graph context"}
  ],
  "join": "query_graph joins god-node candidates against shared graph relationships",
  "best_effort": false
}
```

## Fields

| Field | Type | Meaning |
|-------|------|---------|
| `goal` | string | The trimmed input goal. |
| `project` | string \| null | Shared `project=` context applied to every step (mandatory container path when talking to Docker MCP). |
| `nodes[].id` | int | Unique, sequential (0-based). |
| `nodes[].tool` | string | MCP tool name from the supplied catalog. |
| `nodes[].input` | string | Concrete argument: the goal itself, or a tool-typical default (`project overview` for the prefix step). |
| `nodes[].stage` | int | Execution stage; nodes sharing a stage may run in parallel. Strictly non-decreasing. |
| `nodes[].join` | bool | Exactly one node is the graph join point (`query_graph`, else the last step). |
| `edges[].from`/`to` | int | Data flow: `from` output feeds `to` input. Both always reference existing node ids; no duplicate (from,to) pairs. |
| `edges[].flow` | string | Human-readable label of the carried data. |
| `join` | string | Description of the join over shared elements/relationships. |
| `best_effort` | bool | `true` when the goal matched no known intent (fallback = overview + graph join). |

## Semantics

- **Deterministic:** same goal + same catalog → identical DAG (JSON-stable, covered by test `plan_is_deterministic`).
- **Empty/blank goal** → empty DAG (`nodes: []`, `edges: []`, no `join`).
- **Unknown goal** → best-effort plan: `get_overview_context` → `query_graph` join, `best_effort: true`.
- **Catalog filtering:** a tool missing from `available` is never emitted (e.g. `kg_semantic_context` absent without `embeddings` feature); the join falls back to the last emitted node.
- **Fan-out:** `semantic_search` + `concept_search` share one stage (parallel), joined by `query_graph` on the next stage.
- **Prefix:** `get_overview_context` is always stage 1 when available; a plan never contains duplicate tool steps.

## Rules (keyword → tool plan)

| Intent keys (lowercased substring match) | Tool steps | Join |
|------------------------------------------|-----------|------|
| god / hub / central / most-connected / connected | `get_god_nodes` → `query_graph` | god-node candidates vs shared graph relationships |
| dead / unused / orphan | `find_dead_code` → `get_callers` → `query_graph` | dead-code candidates + caller neighborhoods |
| impact / breaking / what breaks / change / refactor / break | `get_context` → `get_impact_radius` → `get_dependents` → `get_dependencies` → `query_graph` | dependents/dependencies into change-impact subgraph |
| test / coverage / tested | `query_file` → `get_tested_by` → `query_graph` | tested_by edges + element neighborhood |
| trace / requirement / traceability / fr- / us- / doc | `search_by_requirement` → `get_traceability` → `get_files_for_doc` → `find_related_docs` → `query_graph` | traceability chains + doc refs over shared elements |
| call / caller / callee / depend / who calls / what calls | `get_callers` → `get_call_graph` → `query_graph` | caller/callee hops into call subgraph |
| where is / where are / find / implemented / which / what is / how does | `semantic_search` ∥ `concept_search` → `query_file` → `query_graph` | search hits around discovered seeds |
| cluster / module / architecture / overview / component | `get_architecture` → `get_clusters` → `get_cluster_context` → `query_graph` | cluster neighborhoods over shared graph |
| large / complex / long function / big function | `find_large_functions` → `get_context` → `query_graph` | large-function candidates + graph context |

## Verification

- Unit: `tests/planner_tests.rs` (9 tests: god-node DAG + join, join description, best-effort, empty DAG, JSON schema validity, catalog filtering, determinism, parallel fan-out, edge labels).
- Unit (module): `src/graph/planner.rs` `#[cfg(test)]` (empty goal, catalog filtering).
- Gates: `cargo fmt --all -- --check`, `cargo clippy --all -- -D warnings`, `cargo test --lib` (775 passed), `cargo test planner` (9 passed).

Harness remains Cursor/Claude — this module ships the planner + schema only; no MCP tool is added in PR-30.
