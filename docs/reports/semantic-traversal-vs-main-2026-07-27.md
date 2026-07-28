# Semantic Search A/B: `main` (hnsw+rerank) vs `feat/semantic-search-ontology-traversal` (hnsw+ontology-traverse)

**Date:** 2026-07-27
**Scope:** Does the new top-down ontology traversal (FR-SEM-08) make `semantic_search`
return **better** results than the `main` baseline, on LeanKG's own repo?

## TL;DR (verdict)

**No — on this codebase the new traversal does not improve semantic results, and in one
case makes them strictly worse.** The feature's machinery works (it correctly classifies
upper nodes, partitions pools, normalizes and merges), but **the graph it runs on has no
traversable edges from upper nodes (docs / workflows / classes) down to function targets**,
so the traversal discovers **0 functions on every query tested** (`traversed_function_count: 0`).

The *only* functions the feature returns are the `source: "direct"` pool — i.e. exactly the
functions `main` already ranked. The traversal adds nothing, and by demoting non-function
upper hits (docs, workflows) out of `results`, the feature **drops relevant context that
`main` surfaced.**

| Query | main `method` | feat `method` | main top type | feat traversal result | Net effect |
|-------|---------------|---------------|---------------|------------------------|------------|
| `refund` | hnsw+rerank | hnsw+ontology-traverse | 4 docs | 4 upper seeds → **0 fn** | **Worse** (main returned 4 doc hits; feat returns `results: []`) |
| `impact radius calculation` | hnsw+rerank | hnsw+ontology-traverse | 8 docs + 1 workflow | 17 upper seeds → **0 fn** | **Neutral-to-worse** (feat drops the workflow + docs; keeps same direct fns as main) |

Root cause is a **graph-quality gap, not a code bug**: the indexer emits doc↔code edges at
*file* granularity (`./src/api/handlers.rs -> docs/…`, type `documented_by`) and the workflow
ontology nodes carry **no `code_refs`** and no DB edges, so `downward_rule_for` finds no path
to functions. Details + evidence below.

---

## 1. Setup

| Item | Value |
|------|-------|
| Repo | LeanKG self-indexed (`/home/ubuntu/projects/LeanKG`) |
| `main` binary | `0.19.11`, `--features embeddings`, `method: hnsw+rerank` |
| feature binary | `0.19.12` (`feat/semantic-search-ontology-traversal`), `method: hnsw+ontology-traverse` |
| Graph | 8 232 code elements, 49 207 relationships, 126 docs / 1 986 sections, ontology (12 workflows, 57 steps) |
| Vectors | 10 610 (BGE-small-en-v1.5 INT8), HNSW in CozoDB; reranker bge-reranker-v2-m3 |
| Both binaries | read the **same** `.leankg` graph (schema unchanged between branches — fair A/B) |
| Host | 1 vCPU, ~2.3 GB free RAM (severe constraint — see §6) |

**Branch divergence:** feature is 2 commits ahead of `main`, purely additive
(`6b75af7` + a `.gitignore` chore). No schema/indexing changes.

## 2. How the feature is supposed to help

Old `semantic_search` (`main`): HNSW retrieve → cross-encoder rerank → return top-N **of any
node type** (functions, classes, docs, workflows, …) by `rerank_score`.

New `semantic_search` (feature): after rerank, partition hits into:
- **direct** pool — HNSW hits that are themselves functions (keep `rerank_score`)
- **upper** pool — HNSW hits that are class / doc / workflow / concept / … — each becomes a
  **seed** for a downward BFS (`downward_rule_for` per type) to the functions that *implement*
  the intent, re-ranked by a composite embed `"{upper_name}\n{func_blob}"`
- **other** — dropped

Direct + traversed are min-max normalized to `[0,1]` and merged by `rank_score`. Each result
carries `source: "direct" | "traversed"`. The intended win: when a query matches a *doc /
workflow / class* strongly but no individual function, the traversal walks down to the
implementing functions and surfaces them.

## 3. Methodology

`semantic_search` is MCP-only (no CLI one-shot). Each query was driven over `mcp-stdio`
JSON-RPC (`initialize` → `notifications/initialized` → `tools/call semantic_search {query,
limit:10}`) against both binaries, identical arguments, identical shared graph. Responses are
TOON-formatted; fields extracted programmatically.

A matched pair was obtained for two queries (`refund`, `impact radius calculation`). Other
queries in the battery timed out on this host (see §6) — but the two that completed are
representative and the traversal outcome (`0 functions`) is consistent across **all** queries
observed, including partial/debug runs.

## 4. Results — matched A/B

### Q1: `refund`

| | `main` (hnsw+rerank) | `feature` (hnsw+ontology-traverse) |
|---|---|---|
| `results` | **4** (2 `doc_section`, 2 `document`) | **0** (empty) |
| `total_estimate` | 4 | 0 |
| top hit | `docs/.../ui-v2-sidebar-nav-loadmore-deep-test-2026-07-21.md::Fixes shipped` (rerank −10.66) | — |
| traversal | n/a | upper_seeds=**4**, direct_fn=0, **traversed_fn=0** |
| `upper_matches` | — | same 4 doc nodes main returned |

**Interpretation:** "refund" matches four report/spec docs that *mention* refund, but the
underlying code has no `domain_entity:refund` node with code links, and the matched docs have
no downward edges to functions. `main` hands the user those four docs (at least *some* signal);
the feature demotes them to `upper_matches`, finds no functions, and returns an **empty
result set**.

➡️ **Feature is worse here** — it discards the only matches without replacing them.

### Q2: `impact radius calculation`

| | `main` (hnsw+rerank) | `feature` (hnsw+ontology-traverse) |
|---|---|---|
| `results` | **10** (8 `doc_section`, 1 `workflow`, 1 `method`) | **10** (all functions/methods, all `source: direct`) |
| `total_estimate` | 48 | 31 |
| top hit | `docs/.../semantic-search-mcp-verification-2026-07-17.md::4. Graph-enriched semantic context` (rerank +1.09) | `./src/embed/vis-network.min.js::_determinePixelRatio` (rank 1.0) |
| traversal | n/a | upper_seeds=**17** (15 doc_section, 1 `workflow:impact_analysis_flow`, 1 class), direct_fn=31, **traversed_fn=0** |
| `upper_matches` | — | the workflow + docs (incl. the very `impact_analysis_flow` workflow the traversal is built for) |

**Functions in feature `results`** (all `source: direct` — i.e. main would have ranked the
same functions once docs were filtered out):
- `./src/graph/context.rs::estimate_tokens` ← only Rust function actually related
- 9× `./src/embed/vis-network.min.js::*` (`_determinePixelRatio`, `calculateLabelSize`, …) ← **noise** (minified JS physics-layout methods, semantically near "calculation/distance" but irrelevant to impact radius)

**Interpretation:** The 17 upper seeds include the *ideal* traversal origin — the
`ontology://local:default:workflow:impact_analysis_flow:v1` workflow plus a doc section titled
*"4.4 Impact Operations (get_impact_radius)"*. The traversal **should** walk from these to
`get_impact_radius` / impact-radius helpers in `src/graph/`. It returns **0 functions** from
any of them. The only functions returned are direct HNSW hits — and they're dominated by
minified-JS noise.

➡️ **Feature is neutral-to-worse:** it keeps the (noisy) direct functions but **drops the
workflow + 8 docs** that main surfaced, without the traversal recovering the genuinely
relevant `get_impact_radius`.

## 5. Root cause: why traversal finds 0 functions

The traversal code is correct (it checks both outgoing *and* incoming edges, dedups by shortest
hop, respects env + the per-type edge whitelist). The problem is the **graph has no usable
upper→function edges**:

1. **Doc↔code edges are file-granular, not function-granular.** All 815 `documented_by` edges
   are of the form `./src/api/handlers.rs -> docs/…` — i.e. a **File** node → doc. When the
   traversal reaches a doc's neighbor, it lands on a `File` element. `File` is neither a
   function target nor an expandable upper type for the next hop, so the BFS dead-ends.
   Confirmed via `leankg query documented_by --kind rel` (all 815 rows are file→doc).

2. **Ontology workflow/concept nodes have no code links.** `workflow:impact_analysis_flow` has
   no outgoing edges to functions and its `metadata.code_refs` is empty, so both the BFS path
   and the `code_refs` fallback (`resolve_code_refs_fallback`) find nothing. Relationship-type
   tally from the export shows the ontology edges that *do* exist (`has_step: 57`, `next_step:
   45`, `has_failure_mode: 94`) connect workflow↔step, **not** workflow↔code.

3. **Edge-type tally** (`leankg export` → regex): `calls 30974, contains 7734, has_property
   1961, documented_by 815, references 815, imports 472, …` — there is **no edge type linking
   docs/workflows/domain_entities to functions at function granularity.** The only function-
   bearing upper type that *could* traverse is `class`/`struct` (via `contains`/`defines`), and
   in Q2 one `class` upper seed did appear — but it contributed 0 traversed functions too (the
   one class reached had no method children indexed, or fanout caps intervened).

**Conclusion:** on a freshly-indexed LeanKG repo, the FR-SEM-08 traversal has no edges to walk.
The feature cannot improve results until the indexer emits function-level `documented_by` /
`references` edges and/or populates workflow `code_refs`.

## 6. Caveats / environment

- **1 vCPU host.** `semantic_search` over MCP takes ~35 s for a trivial query and **~110 s**
  for a full 50-candidate rerank + traversal on this box (bge-reranker-v2-m3 is a ~568 M-param
  model). Several battery queries exceeded tolerable timeouts; the two completed pairs above are
  representative and the `traversed_function_count: 0` outcome was uniform across every query
  observed (including partial / `semantic-context --debug` runs).
- **Score scales are not comparable** across binaries (cross-encoder logits on main vs.
  min-max-normalized merge on feature). Comparison is on *which nodes* rank where, not on score
  magnitudes.
- **A pre-existing embed bug was patched to unblock this test.** `embeddings::state::upsert_fresh`
  interpolated `qualified_name` into a Datalog query string; a QN containing characters that
  `serde_json::Value::String` emits raw broke the CozoDB parser at a fixed byte offset mid-batch,
  crashing `leankg embed --full` at ~6 700/10 610 vectors. Fix: switched `upsert_fresh` to the
  parameterized `db.import_relations` path (mirroring the safe `upsert_vectors`). **This bug is
  unrelated to FR-SEM-08 — it exists on `main` too** (the file is unchanged on the feature
  branch). The patch is in the working tree (`src/embeddings/state.rs`), uncommitted.

## 7. Recommendation

The traversal feature is **correctly implemented but cannot demonstrate value on this graph**.
Before merging/claiming a semantic-quality win:

1. **Close the graph gap** (indexer work, not retrieval work):
   - Emit `documented_by` / `references` edges at **function** granularity (link the specific
     function/method a doc section describes, not just the file).
   - Populate `metadata.code_refs` on workflow / domain_entity / failure_mode nodes during
     indexing (or via `add_ontology_concept` / `add_ontology_workflow` linking), so the
     `code_refs` fallback has something to resolve.
2. **Re-run this A/B** on a graph with those edges; expect `traversed_function_count > 0` and
   functions like `get_impact_radius` appearing under Q2 via `source: "traversed"`.
3. **Until then, consider preserving non-function upper hits** in `results` (e.g. top-2 upper
   nodes as a "related context" tail) so the feature never returns an *empty* set where `main`
   returned *something* — the Q1 `refund` regression is a direct consequence of discarding them.

The feature should be evaluated on a graph rich enough to exercise it; on LeanKG's self-index
today it is inert at best and a (small) regression at worst.
