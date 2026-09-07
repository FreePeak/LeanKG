<!-- GENERATED-BY: scripts/gen_tool_contract.sh --><!-- DO NOT EDIT BY HAND -->

# MCP Tool Contract

Regenerated from `src/mcp/tools.rs` (`ToolRegistry::list_tools`). **3 tools.**
To change the surface: edit the registry, run `scripts/gen_tool_contract.sh`, commit both.

## Stability tiers

- **stable** — input schema and output shape are contractual; breaking changes follow the deprecation policy below.
- **beta** — may change or be removed in any minor release; feedback welcome.
- New tools enter as **beta** (`since: unreleased`) and are promoted after one minor release without schema change.

## Deprecation policy

- Tool removal requires **2 minor releases** of deprecation notices (doc + tool description marked deprecated).
- A breaking input-schema change to a stable tool requires a **minor version bump treated as major-equivalent**, plus a release notice.
- Additive optional properties do not break the contract.

## Deprecation history

Removed tools, their removal release, and the surviving replacement surface.

| Tool | Removed in | Replacement |
|------|------------|-------------|
| `get_graph_report` | unreleased (v0.28) | get_god_nodes + get_architecture |
| `orchestrate` | unreleased (v0.28) | query_graph / kg_context / search_code |
| `search_by_requirement` | unreleased (v0.28) | get_traceability / get_traceability_matrix |

## Tools

| Tool | Tier | Since | Purpose | Input schema |
|------|------|-------|---------|--------------|
| `set` | unreleased | beta | Tier: core. Import a repository (or a directory of nested repos) into the knowledge graph and manage writes. Actions: `index` (full index of path, default when omitted), `incremental` (delta re-index), `attach` (register an already-indexed repo), `index_docs`, `install` (write client config), `add_knowledge`, `update_knowledge`, `delete_knowledge`, `add_annotation`, `add_documentation`, `link_element`, `add_ontology_concept`, `add_ontology_workflow`, `delete_ontology_concept`, `promote_environment`, `embed` (build HNSW vectors), `set_embed_model`, `agent_diary_write`, `report_query_outcome`, `agent_focus`, `index_prd`, `export_graph_snapshot`, `export_html`, `generate_doc`. Pass action-specific arguments as top-level fields. | `action:string, path:string, project:string` |
| `get` | unreleased | beta | Tier: core. Query the knowledge graph with multiple layers — the capability ladder auto-selects: L3 vector (ANN + rerank), L2 keyword (trigram fuzzy + ontology), L1 exact (identifier/regex + did-you-mean), L0 cold (guidance). Degrades ranking, never availability; every response carries retrieval {rung, reason, freshness}. With no `query`/`action`, serves the natural-language router. Direct capability access: pass `action` with any read capability (e.g. \"search_code\", \"get_impact_radius\", \"query_graph\", \"get_architecture\", \"explain_node\", \"kg_context\", \"temporal_query\") plus its usual arguments. | `query:string, action:string, layer:string, limit:integer, full:boolean, project:string` |
| `status` | unreleased | beta | Tier: core. Knowledge-graph health and inventory: index freshness, element/relationship counts, embedding coverage + model, indexing state (idle/indexing), storage backend (sqlite\|postgres), watch/vacuum status. Read-only; safe on cold or missing indexes. | `project:string` |
