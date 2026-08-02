# Smoke gates (#173) live evidence — 2026-08-02

## Environment
- commit: 8c77b22b | script: scripts/mcp-smoke-tools.py --check-only-ontology | MCP: :9699 Docker | project: /workspace-be (662k elements)

## Steps
1. `LEANKG_SMOKE_PROJECT=/workspace-be python3 scripts/mcp-smoke-tools.py --check-only-ontology`

## Results
- Ontology gates (FR-A03) 4/4 PASS:
  - `kg_self_test all_ok: true`
  - `kg_ontology_status concept+procedural counts present`
  - `kg_trace_workflow workflow=leankg-index-and-query traceable`
  - `ontology_control(status) sync status readable`
- Routing gates (FR-A06) 3/3 PASS (guard-refuse on mega-graph):
  - `find_route` / `get_screen_args` / `get_nav_callers` → guard-refuse, status ok
- Recipes (FR-B50) 14/14 PASS (≥10 required): count_elements, count_relationships, by_language, by_element_type, calls_edges, imports_edges, tested_by_edges, docs_elements, ontology_nodes, knowledge_count, vector_count, orphan_elements, longest_functions, incident_count.

## Tracker
- Smoke gates (#173): PASS. Ontology 4/4 + routing 3/3 + recipes 14/14 on the 662k-element workspace-be graph.
