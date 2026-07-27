# Ontology-Guided Top-Down Traversal for `semantic_search`

Date: 2026-07-27
Status: Implemented
Scope: Add an **ontology-guided top-down traversal** stage to `semantic_search` (and `kg_semantic_context`) that walks from high-level "upper" nodes down to the **function** nodes implementing a natural-language intent, ranked by a composite embedding.

## Problem

`semantic_search` ran HNSW vector retrieve → cross-encoder rerank and returned seeds, with **no graph traversal at all** (only `kg_semantic_context` traversed, and its `traverse_seeds` was purely additive — neighbor nodes in a separate array, no score fusion). A query like "where is refund failure handled" would return whichever functions / classes / docs happened to be nearest in embedding space, but never the function that lives one hop *below* a strongly-matching class or doc unless that function's own blob already matched.

## Design

Two-pool, per-pool-ranked, then merged:

1. **Direct pool** — HNSW hits that are themselves functions (`function` / `method` / `constructor`). These already carry a cross-encoder `rerank_score`; we keep it untouched.
2. **Traversed pool** — HNSW hits that are *upper* nodes (`class`, `struct`, `interface`, `trait`, `module`, `file`, `document`, `doc_section`, `workflow`, `workflow_step`, `decision_point`, `failure_mode`, `domain_entity`, `service`, `api_endpoint`, `data_store`, `known_issue`, `playbook`, `playbook_step`, `team_knowledge`). Each upper seed drives a downward BFS to function targets via a per-type rule, then every discovered function is re-ranked against the original intent by a **composite** embedding of `"{upper_name}\n{function_blob}"`.

The two pools use incomparable score scales (cross-encoder logits vs cosine similarity), so each pool is min-max normalized to `[0,1]` and the merged list is interleaved by `rank_score`. The original score is preserved under its source-specific key (`rerank_score` for direct, `composite_score` for traversed) so agents can see *why* a function ranked where it did.

### Per-type downward rule (`downward_rule_for`)

This is the "ontology distance from a node type to a function node" — a hop budget + allowed edge types + fanout cap, parallel to the additive `traverse_rule_for` but heading toward functions and **terminating** at function targets (we do not expand *through* a function to its callees — that would explode the frontier and dilute the signal).

| Upper type | Hops | Edges | Fanout |
| --- | --- | --- | --- |
| `class` / `struct` / `interface` / `trait` / `module` | 1 | `contains`, `defines`, `has_method`, `has_property` | 12 |
| `file` | 1 | `FILE_EDGES ∪ {contains, defines}` | 12 |
| `document` / `doc_section` | 1 | `references`, `documented_by` | 10 |
| `workflow` | 2 | `WORKFLOW_EDGES` (`has_step`, `next_step`, `implemented_by`, …) | 15 |
| `workflow_step` / `decision_point` / `failure_mode` | 1 | `STEP_EDGES ∪ {implemented_by}` | 12 |
| `domain_entity` / `service` / `api_endpoint` / `data_store` | 2 | `CONCEPT_EDGES` | 12 |
| `known_issue` / `playbook` / `playbook_step` / `team_knowledge` | 1 | `ISSUE_EDGES` | 8 |
| other / unknown | 1 | `documented_by`, `documents_concept` | 5 |

### `code_refs` fallback

Concept / workflow / workflow_step nodes often store their code links in `metadata.code_refs` rather than as DB edges. When the BFS yields nothing for one of these types, the engine reads `metadata.code_refs` and resolves each ref via the same keyed path-prefix lookups `OntologyQueryEngine::resolve_code_refs` uses (`find_element`, `find_elements_by_file_path_prefix`, with `file::symbol` splitting). Bounded per-seed, no full-table scan (mega-graph safe per FR-ONT-MEGA-01).

### Composite scoring

`composite_text(upper_name, func_blob) = "{upper_name}\n{func_blob}"`. All discovered functions for one query are embedded in a single batched `Embedder::embed` call; the query vector is reused from the retrieval pipeline (`SemanticRetrievalPipeline::last_query_vector()`) so the intent is embedded **exactly once** per request. Cosine similarity (vectors are L2-normalized by the embedder, so cosine = dot product).

## What changed

| File | Change |
| --- | --- |
| `src/retrieval/ontology_traversal.rs` | **NEW** — `traverse_to_functions`, `downward_rule_for`, `score_functions`, `composite_text`, `cosine`, `UpperSeed`, `DiscoveredFunction`, `FUNCTION_TARGET_TYPES`, `UPPER_TYPES`, `GLOBAL_FUNCTION_CAP`, unit tests. |
| `src/retrieval/mod.rs` | Re-export the new public API. |
| `src/retrieval/pipeline.rs` | Stash + expose `last_query_vector()` so the composite stage reuses the query embed. |
| `src/mcp/handler.rs` | `run_hnsw_semantic_search`: partition seeds → traverse → composite-score → normalize → merge/dedup → new response shape (`method: "hnsw+ontology-traverse"`, `source` / `via_upper` / `rank_score`, `upper_matches[]`, `traversal` diagnostics). `kg_semantic_context`: add `functions[]` array + `ontology_traversal` diagnostics. |
| `docs/mcp-tools.md` | Document the new `semantic_search` method, `functions[]` array, and response fields. |

## Non-goals / guardrails

- Did **not** modify the additive `traverse_seeds` / `traverse_rule_for` (`src/graph/traversal.rs`) — they have 4 callers (`src/main.rs` ×2, `kg_semantic_context`, tests) and a different purpose (type-agnostic neighborhood expansion). `kg_semantic_context` keeps its additive `traversed[]` array; the new `functions[]` array is additive.
- Did **not** change `FilterPolicy` semantics — upper types are already kept by `ALWAYS_INCLUDE_TYPES`; the handler just partitions them by role.
- Did **not** introduce a second query embed call per request — the pipeline's cached vector is reused.
- Hard caps: `GLOBAL_FUNCTION_CAP = 80`, per-type fanout (5–15), single batched composite embed.
- Backward compatible: `semantic_search` response is a strict superset of the old shape (all old fields preserved, new fields added). `kg_semantic_context` adds a sibling `functions[]` array.

## Verification

- `cargo build --features embeddings` clean.
- `cargo test --features embeddings` — new `ontology_traversal` unit tests cover: `downward_rule_for` per type, target/upper classification, indexer-noise filtering, `cosine` on identical/orthogonal/opposite vectors, `composite_text` join, ontology-GID display-name extraction, dedup keeping shortest hop, longer-hop-not-overwriting.
