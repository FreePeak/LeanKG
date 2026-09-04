# LeanKG vs “Graph Engineering with Claude” 14-step roadmap

**Date:** 2026-07-21  
**Source scaffold:** reconstructed notes from Codez (@0xCodez) X article preview (Jul 20, 2026) — full article login-walled; step titles are **inferred**, not quotes.  
**Local reconstruction:** `/tmp/opencode/graph-engineering-with-claude.md`  
**Product IDs:** `US-GE-*` / `FR-GE-*` / `REL-064` — see [`docs/prd.md`](../prd.md) §1.2 / §3.20 / §5.23 and [`docs/prd-task-tracker.md`](../prd-task-tracker.md).

## Thesis of the roadmap (preview + scaffold)

1. Multi-step agents default to a **straight line** (step blocks step).
2. The fix is a **graph-shaped execution model**: planner fans out work; agents share a persistent knowledge graph; results can be reused across runs.
3. Sequenced with an earlier “agent harness” post: harness (rules/subagents/hooks) + graph memory underneath.

## Fit summary

| Inferred step theme | Fit | LeanKG today | Adapt? |
|---------------------|-----|--------------|--------|
| 1 Straight-line problem | Out of scope | Memory layer, not orchestrator | Positioning only |
| 2 Planner node | **Missing** | `orchestrate` / `agent_focus` — no goal→DAG | Optional thin planner |
| 3 Typed nodes & edges | **Strong** | CodeElement + Relationship + ontology | Schema curriculum |
| 4 Tree-sitter ingest | **Strong** | Core indexer | Document pass-1 boundary |
| 5 Semantic / LLM pass-2 | Partial | Embeddings + YAML ontology; LLM workflow extract deferred | Selective LLM extract (Could) |
| 6 Entity resolution | Partial | `qualified_name` + `typed_resolve` | Cross-alias merge |
| 7 Community detection | Partial | Louvain / precomputed clusters | Cluster-first agent UX |
| 8 Embeddings | **Strong** | HNSW, semantic_search, day-2 resume | Mega OOM harden |
| 9 MCP query tools | **Strong** | Large MCP surface | Surface rationalization |
| 10 Wire into harness | Partial | Skills/rules/diary; not shipped harness kit | Overlaps US-GF-17 |
| 11 Invalidation | Partial | Incremental index, ontology watch, embed resume | Staleness budgets |
| 12 Debug graph drift | Partial | `kg_self_test`, status, reports | Graph-health narratives |
| 13 Self-improving loop | Partial | diary / knowledge / `report_query_outcome` | Close outcome→graph→plan |
| 14 Graph architect role | Out of scope | Docs/PRD | Optional playbook |

## Verdict

- **Adapt** the curriculum as **education + positioning**: LeanKG already is the persistent code/knowledge graph + MCP half (steps ~3–4, 8–9).
- **Do not** rebuild Claude’s harness inside LeanKG unless that becomes an explicit product goal (packaging stays with US-GF-17).
- **Highest ROI gaps:** graph-aware planner/DAG, entity resolution, cluster-first navigation, closed write-back self-improve loop.

## Explicit non-goals

- Replacing Cursor/Claude orchestration with a LeanKG-owned multi-agent runtime.
- OpenTrace-style full GitHub/Linear/K8s/trace graph as the core product (code-first remains).
- Full LLM auto-extraction of all workflows from arbitrary code (still Could Have; YAML SoT for procedural ontology).
