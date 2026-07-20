# Root Cause Analysis: Mega concept_search / query_graph / get_clusters

**Date:** 2026-07-20  
**Evidence:** [`docs/reports/ce03fd8-docker-mcp-full-tool-test-2026-07-20.md`](../docs/reports/ce03fd8-docker-mcp-full-tool-test-2026-07-20.md)  
**Branch:** `feature/mega-concept-query-safe`  
**Related:** FR-SEM-07 fixed HNSW hydration only — orthogonal paths.

## Issue Description

On `/workspace-other` (~641k elements, ~3.9 GiB effective cgroup):

| Tool | Symptom | Bug? |
|------|---------|------|
| `concept_search` | RemoteDisconnected ~22s / Timeout 180s | **Yes** |
| `query_graph` | Timeout 120s; repeated `all_elements()` WARN | **Yes** |
| `get_clusters` | Empty + refuse error | **No** — intentional live-Louvain gate |

## Problematic Code

### concept_search — full-table code_ref load

`src/ontology/query.rs` `resolve_code_refs` → `load_indexed_code_elements()` materializes all non-ontology `*code_elements` (~641k), then O(refs × N) match. Fallback `search_code_elements_by_name` does the same.

### query_graph — repeated full scans

`resolve_to_qualified` → `all_elements()` per synonym variant.  
`expand_from_seeds` / `shortest_path` → `all_relationships()` + full adjacency map.

### get_clusters — intentional refuse

`handler.rs` refuses when `count_elements > LEANKG_MAX_CLUSTER_ELEMENTS` (50k). Live Louvain still needs full dumps (`clustering.rs`).

## Logic Flow (failure)

```
MCP concept_search
  → OntologyQueryEngine::concept_search
  → resolve_code_refs
  → load_indexed_code_elements  *** ~641k Vec ***
  → RSS spike / restart

MCP query_graph(question)
  → resolve_seed_terms → resolve_to_qualified → all_elements() × N
  → expand_from_seeds → all_relationships()
  → timeout under memory pressure
```

## Root Cause

Unbounded materialization of element and/or relationship tables on the MCP request path. Host RAM ceiling amplifies symptoms; it is not the fix.

## Chosen Fix (best solution)

Same class as FR-SEM-07:

1. **FR-ONT-MEGA-01:** Keyed / path-prefixed Cozo queries for `code_refs`; `search_by_name_typed` for fallback. Ban `load_indexed_code_elements` on hot path.
2. **FR-GF-MEGA-01:** Keyed `resolve_to_qualified`; frontier-local `get_relationships_involving_elements_fast` for BFS/`shortest_path`.
3. **FR-CL-MEGA-01:** On mega, serve precomputed `cluster_id`/`cluster_label` from DB; keep refuse for live Louvain.

## Trace / Logging Points

- `TRACE concept_search: resolve_code_refs refs=N method=keyed`
- `TRACE query_graph: resolve_to_qualified keyed hit/miss`
- `TRACE expand_from_seeds: frontier=N rels_fetched=M` (never `all_relationships`)
- Warn if `all_elements`/`all_relationships` called from these paths (should be zero)

## Suggested Additional Logging

Debug timestamps around keyed fetches; warn if any single frontier fetch returns the `:limit` cap (possible truncation).

---

## Fix verification (REL-055)

Live Docker smoke 2026-07-20: mega `concept_search` ~0.9s, `query_graph` ~12.6s, `get_clusters` ~1.2s under ~3.9 GiB cgroup; zero `all_elements`/`all_relationships` WARNs. Evidence: [`rel-055-mega-concept-query-clusters-2026-07-20.md`](rel-055-mega-concept-query-clusters-2026-07-20.md).

Latency follow-ups applied after first hang: stop-word connection verbs; mega longer seed aliases; outbound-only frontier edges on mega; skip live shortest_path on mega; frontier/depth caps.
