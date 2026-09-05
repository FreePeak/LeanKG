<!-- GENERATED-BY: scripts/gen_tool_contract.sh --><!-- DO NOT EDIT BY HAND -->

# MCP Tool Contract

Regenerated from `src/mcp/tools.rs` (`ToolRegistry::list_tools`). **1 tools.**
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
| `leankg_context` | unreleased | beta | Tier: core. The one LeanKG tool. Ask any question about the indexed codebase — intent is auto-classified (semantic \| lexical \| impact \| graph \| files) and served by a capability ladder: L3 vector (ANN + rerank), L2 keyword (trigram fuzzy + ontology), L1 exact (identifier/regex + did-you-mean), L0 cold (guidance + background index). Degrades ranking, never availability; every response carries retrieval {rung, reason, freshness}. Direct capability access: pass `verb` with any former tool name (e.g. \"get_impact_radius\", \"query_graph\", \"mcp_status\") plus its usual arguments; omit `verb` for natural-language routing. | (none) |
