# Embedding Retrieve → Rerank → KG Traverse — Unified Plan

Date: 2026-06-30
Status: Draft
Scope: Add vector retrieval + cross-encoder rerank + adaptive graph traversal on top of the existing ontology layer and current keyword/graph retrieval.

## Origin and Reconciliation

This plan unifies and supersedes the embedding-deferred sections of two prior docs:

- `docs/design/hybrid-retrieval-reranking.md` — Phase 4 (Optional Embeddings). The hybrid retriever architecture (parallel retrievers → RRF fusion → graph-aware rerank) stays as the future direction for *multi-channel* retrieval. This plan delivers the **embedding channel** that the hybrid design flagged as optional.
- `docs/planning/2026-05-17-ontology-semantic-search-mvp.md` — Future Enhancement "embeddings for semantic alias matching". The ontology layer described there is now implemented (`src/ontology/`); this plan adds the embedding-backed semantic match that the MVP explicitly deferred.

What this plan is **not** doing:
- Not replacing existing `semantic_search` / `kg_context` keyword and regex matching.
- Not building the full parallel-retriever + RRF fusion pipeline from `hybrid-retrieval-reranking.md`. That remains a separate follow-on.
- Not introducing Graphiti, bi-temporal history, or hosted vector databases.

## Locked Decisions (from 2026-06-30 design review)

| Decision | Choice |
| --- | --- |
| Runtime stack | All-Rust in-process: `fastembed` (embeddings + cross-encoder rerank, ONNX-backed) + `usearch` (ANN). No external services. `ort` was originally listed as a separate reranker dep but `fastembed::TextRerank` covers `bge-reranker-v2-m3` natively, so the dedicated `ort` dep was dropped during Phase 0. |
| What gets embedded | Text blob (qualified_name + name + doc/signature) **and** ontology description + aliases. No code body. No offline GNN embeddings. |
| Traversal policy | Adaptive: hops depend on seed `element_type`. Workflow/procedural seeds → 2 hops; function/file/concept seeds → 1 hop. Fanout cap per seed. |
| Plan handling | Extend the two existing docs into this unified plan (done). |
| Model placement (Q1) | Lazy-download to `~/.cache/leankg/models/`, SHA-256 verified. `embed --init` pre-downloads both models explicitly. |
| Worktree filter (Q2) | Default-on: exclude `**/.worktrees/**`, `**/.claude/worktrees/**`, `**/.opencode/worktrees/**` at the ANN stage. Opt-in flag `include_worktrees: bool = false`. |
| Index freshness (Q3) | `index` and `embed` are separate. `index` marks touched CodeElements as stale in a new `embedding_state` CozoDB table. `embed` (default) does **incremental** work: only nodes whose `content_hash` changed or whose state is `stale`/`missing`. No auto-embed inside `index`. |
| Reranker failure (Q4) | Option A — ANN-only fallback with `diagnostics.reranker = "fallback_ann"`. Model missing / load failure / OOM all fall back, never refuse. |

## Architecture

```
[query text]
   │
   ▼  Stage 1 — Embed query (fastembed, BGE-small or jina-code)
[query_vec]
   │
   ▼  Stage 2 — ANN retrieve (usearch, cosine, top-K=50)
[top-50 candidate node IDs]
   │
   ▼  Stage 3 — Cross-encoder rerank (fastembed::TextRerank, bge-reranker-v2-m3)
[top-N=10 seed nodes]
   │
   ▼  Stage 4 — Adaptive KG traversal (CozoDB Datalog)
[seeds + 1-2 hop neighbors + edges]
   │
   ▼  Stage 5 — Optional final rerank on union; compress; return MCP payload
[enriched context]
```

Stages 1–3 are new. Stage 4 reuses the existing CozoDB graph. Stage 5 reuses the compression logic already in `kg_context`.

## Data Model

### What gets a vector

Every `CodeElement` row that is one of:

- `element_type` in `{file, function, class, module}` (code nodes) — embed the **code text blob**
- `element_type` in `{domain_entity, service, api_endpoint, data_store, workflow, workflow_step, decision_point, failure_mode, playbook, playbook_step, known_issue, team_knowledge}` (ontology nodes) — embed the **ontology text blob**

Docs files (when `documents` rows exist with prose) — embed the **doc text blob** (title + heading path + first paragraph).

### Text blob construction

Code blob:

```
qualified_name + "\n" + name + "\n" + doc_comment (if any) + "\n" + signature
```

No function body. Bounded length: truncate at 512 tokens (embedding model max).

Ontology blob:

```
name + "\n" + aliases.join(", ") + "\n" + metadata.description + "\n" + element_type
```

Doc blob:

```
heading_path.join(" / ") + "\n" + title + "\n" + first_paragraph
```

### Vector storage

Sidecar ANN index file, not a CozoDB table:

```
.leankg/
  embeddings.usearch        # vectors keyed by CodeElement.id (i32)
  embeddings.meta.json      # model_id, dim, metric, element_type counts, build timestamp
```

Rationale: CozoDB has no native HNSW; `usearch` gives sub-ms cosine search in pure Rust. The `CodeElement.id` → vector mapping is the only bridge. Rebuilding the index is cheap (fastembed is CPU-friendly).

### Incremental embedding & staleness

Embedding a 10k-node repo takes minutes; rebuilding from scratch on every `index` run is unacceptable. The design is incremental:

**CozoDB side table `embedding_state`** (new, in the same CozoDB file):

```
embedding_state {
  code_element_id: i64,         # FK to code_elements.id
  content_hash: String,         # SHA-256 of the text blob at last embed
  state: String,                # "fresh" | "stale" | "missing"
  embedded_at: String,          # ISO 8601 timestamp
}
```

**Marking stale.** The `index` command, after upserting `code_elements` rows, runs a single Datalog statement that flips `embedding_state.state` to `"stale"` for every `code_element_id` it touched (inserts, updates, deletes). Deleted CodeElements get `"stale"` too — `embed` will reap them from usearch and drop the state row.

**Incremental `embed`.** Default `embed` behavior:

1. Read all `code_elements` rows.
2. For each, compute the text blob and its `content_hash`.
3. Compare against `embedding_state.content_hash`:
   - No state row → `missing` → embed, insert state row.
   - Hash differs → `stale` → embed, update state row.
   - Hash matches and `state = "fresh"` → skip.
   - Hash matches and `state = "stale"` → re-embed (handles "touched but content unchanged" cases cheaply).
4. For state rows whose `code_element_id` no longer exists in `code_elements` → remove the vector from usearch, delete the state row.
5. Mark all touched rows `state = "fresh"`, write `embedded_at`.
6. Persist `embeddings.usearch` + `embeddings.meta.json`.

For a typical re-index that touches 50–200 nodes, `embed` runs in seconds, not minutes.

**Full rebuild.** `cargo run --release -- embed --full` ignores state and re-embeds every CodeElement. Use after model swap, usearch corruption, or version upgrade.

**Model pre-download.** `cargo run --release -- embed --init` downloads both the embedding model (`bge-small-en-v1.5`) and the reranker (`bge-reranker-v2-m3` ONNX) to `~/.cache/leankg/models/`, verifies SHA-256, and exits without touching the index. Recommended setup step. If skipped, lazy-download fires on first use of each model.

### Embedding model

Default: `BAAI/bge-small-en-v1.5` (384-dim, fast, general text).
Optional config swap: `jinaai/jina-embeddings-v2-base-code` (code-aware, 768-dim) for code-heavy deployments.

Model choice is stored in `embeddings.meta.json` so retrieval knows which model to use for the query vector.

## Adaptive Traversal Rules

Stage 4 runs a bounded CozoDB Datalog traversal per seed node. Hop count and edge filter depend on the seed's `element_type`:

| Seed type | Hops | Allowed edge types | Fanout cap (per hop) |
| --- | --- | --- | --- |
| `workflow` | 2 | `has_step`, `next_step`, `branches_to`, `implemented_by`, `entry_point_of`, `step_in_process`, `has_failure_mode` | 20 |
| `workflow_step`, `decision_point`, `failure_mode` | 2 | `next_step`, `branches_to`, `implemented_by`, `handled_by_playbook`, `has_failure_mode`, `resolved_by_playbook` | 15 |
| `domain_entity`, `service`, `api_endpoint`, `data_store` | 1 | `owns_concept`, `implements_concept`, `exposes_endpoint`, `reads_from`, `writes_to`, `documents_concept`, `has_known_issue` | 15 |
| `known_issue`, `playbook`, `team_knowledge` | 1 | `has_known_issue`, `resolved_by_playbook`, `documents_concept` | 10 |
| `function`, `class` | 1 | `calls`, `imports`, `references`, `tested_by`, `documented_by`, `implements_concept` | 10 |
| `file`, `module` | 1 | `imports`, `references`, `tested_by`, `documented_by`, `contains`, `defines` | 10 |
| `doc` / other | 1 | `documented_by` (reverse), `documents_concept` | 5 |

Global caps:
- Total traversed neighbors across all seeds: 60 (dedup by `qualified_name`).
- Traversal skips nodes already in the seed set.
- Edges to nodes outside the active `env` are filtered.

## MCP Tool Contract

New tool `kg_semantic_context`. Kept separate from `kg_context` so the existing keyword-based flow stays the default; agents can opt in.

```
kg_semantic_context(
  query: string,
  env?: string = "local",         // metadata pre-filter
  top_k?: number = 50,            // ANN retrieve depth
  rerank_top_n?: number = 10,     // cross-encoder keep depth
  traverse?: bool = true,         // toggle Stage 4
  final_rerank?: bool = true,     // toggle Stage 5 on union
  debug?: bool = false            // include diagnostics
)
```

Response shape (Stage 5 union):

```json
{
  "query": "where is refund failure handled",
  "env": "local",
  "intent_hint": "explain_flow",
  "seeds": [
    {
      "qualified_name": "local:checkout-service:workflow:checkout:v1",
      "element_type": "workflow",
      "final_score": 0.88,
      "ann_rank": 3,
      "rerank_score": 0.88,
      "matched_blob_excerpt": "Checkout flow ... authorize payment ..."
    }
  ],
  "traversed": [
    {
      "qualified_name": "local:checkout-service:workflow_step:authorize_payment:v1",
      "element_type": "workflow_step",
      "via_edge": "has_step",
      "from_seed": "local:checkout-service:workflow:checkout:v1",
      "hop": 1
    }
  ],
  "edges": [
    { "source": "...", "target": "...", "rel_type": "has_step" }
  ],
  "diagnostics": {
    "ann_candidate_count": 50,
    "reranker": "bge-reranker-v2-m3",
    "embedder": "bge-small-en-v1.5",
    "traversal": { "hops_used": 2, "neighbors_traversed": 23, "capped": false },
    "latency_ms": { "embed": 4, "ann": 1, "rerank": 22, "traverse": 6, "total": 33 }
  }
}
```

When `debug=false`, drop `diagnostics`, `matched_blob_excerpt`, and edge list. Compress to fit MCP token budget using the same logic as `kg_context`.

## Implementation Phases

### Phase 0 — Dependencies and feature gate

1. Add Cargo deps under a new feature `embeddings`: `fastembed` (covers embed + rerank), `usearch`. `ort` was originally listed but dropped — `fastembed::TextRerank` covers the reranker natively.
2. Gate all new modules behind `#[cfg(feature = "embeddings")]` so default builds stay slim.
3. Document ONNX runtime requirements in `docs/mcp-setup.md` (fastembed bundles ONNX runtime via its own deps).

### Phase 1 — `src/embeddings/` module

1. `src/embeddings/mod.rs` — `Embedder` trait, factory.
2. `src/embeddings/text_blob.rs` — code/ontology/doc blob builders (table above).
3. `src/embeddings/index.rs` — `usearch::Index` wrapper: `build_from_code_elements`, `load`, `save`, `search`, `remove`, supports incremental add/remove.
4. `src/embeddings/build.rs` — orchestrate incremental build: read `code_elements`, compute text blob hashes, diff against `embedding_state`, embed only changed/missing/stale, reap deleted, persist `.leankg/embeddings.usearch` + `.meta.json`.
5. `src/embeddings/state.rs` — `embedding_state` CozoDB table DDL + helpers (`mark_stale_for_ids`, `upsert_fresh`, `list_stale`, `list_orphans`).
6. `src/embeddings/models.rs` — lazy-download + SHA-256 verify to `~/.cache/leankg/models/`; `init_models()` for `embed --init`.
7. **Indexer hook.** Modify `src/indexer/` to call `embedding_state::mark_stale_for_ids` after upserting/deleting CodeElements during `index`. Behind `#[cfg(feature = "embeddings")]`.

CLI:
- `cargo run --release -- embed --init` — download models, no build.
- `cargo run --release -- embed` — incremental (default).
- `cargo run --release -- embed --full` — full rebuild.

### Phase 2 — `src/retrieval/` module

1. `src/retrieval/ann.rs` — embed query → `usearch` top-K → return `(CodeElement.id, score)[]`. Apply worktree path filter here (Q2 default-on).
2. `src/retrieval/rerank.rs` — `fastembed::TextRerank` with `RerankerModel::BGERerankerV2M3`, batch-score `(query, blob)` pairs, return reranked top-N. **On any failure** (model missing after lazy-download attempt, init error, inference OOM/panic) → return ANN-order top-N unchanged and set a `RerankerStatus::Fallback` flag on the result (Q4 option A).
3. `src/retrieval/pipeline.rs` — `SemanticRetrievalPipeline` struct with `retrieve(query, env, top_k, rerank_top_n) -> RetrievalResult { seeds, reranker_status, embeddings_stale }`.

No MCP wiring yet. Unit-testable end to end.

### Phase 3 — Adaptive traversal

1. Extend `src/ontology/query.rs` (or new `src/graph/traverse.rs`) with `traverse_seeds(seeds, env, rules) -> TraverseResult`.
2. Rules table encoded as Rust `match` on `element_type` (per table above).
3. CozoDB Datalog queries parameterized by `(hops, edge_types, fanout)`; reuse existing arity-correct patterns from `src/ontology/query.rs`.
4. Dedup, env filter, global cap.

### Phase 4 — MCP wiring

1. Add `kg_semantic_context` to `src/mcp/tools.rs` schema.
2. Handler in `src/mcp/handler.rs` calls pipeline → traverse → compress → return.
3. Add `debug` field passthrough.
4. Register in `kg_self_test` smoke flow.

### Phase 5 — CLI parity

```bash
cargo run --release -- embed --init                # pre-download both models (setup, no build)
cargo run --release -- embed                       # incremental rebuild (default, stale-only)
cargo run --release -- embed --full                # full rebuild (recovery / model swap)
cargo run --release -- semantic-context "query"    # one-shot CLI for testing
```

### Phase 6 — Tests

- Unit: text blob construction (per element_type), adaptive rule selection, dedup.
- Integration: small fixture repo, build index, run known queries, assert seed + traversed membership.
- Regression: ensure existing `kg_context` and `semantic_search` outputs are unchanged.
- Latency: budget assertions in `kg_self_test` (embed < 10ms, rerank < 50ms, traverse < 30ms on the fixture).

## File Touchpoints

| Area | Change |
| --- | --- |
| `Cargo.toml` | New `embeddings` feature, deps |
| `src/lib.rs` | Export `embeddings`, `retrieval` modules |
| `src/embeddings/*` | New |
| `src/retrieval/*` | New |
| `src/graph/traverse.rs` or `src/ontology/query.rs` | Add `traverse_seeds` |
| `src/mcp/tools.rs` | New `kg_semantic_context` schema |
| `src/mcp/handler.rs` | New handler, pipeline orchestration |
| `src/cli.rs` | New `embed` and `semantic-context` subcommands |
| `src/mcp/tools.rs::kg_self_test` | Add semantic smoke check |
| `docs/mcp-setup.md` | Document embedding deps and `embed` command |
| `docs/mcp-tools.md` | Document `kg_semantic_context` |

## Acceptance Criteria

- `cargo run --release -- embed` builds `.leankg/embeddings.usearch` from an indexed repo; idempotent on re-run.
- `kg_semantic_context("checkout refund failure", env="local")` returns at least one workflow/concept seed and at least one traversed neighbor (file/function/step).
- Adaptive hop rule respected: workflow seeds produce 2-hop traversed sets; function seeds produce 1-hop sets (verified via `debug=true` diagnostics).
- p95 total latency on a 5k-node repo < 150ms with embeddings enabled.
- Existing `kg_context`, `semantic_search`, `find_function`, `get_impact_radius` outputs unchanged.
- Default `cargo build --release` (without `embeddings` feature) still succeeds and produces no binary bloat.
- `kg_self_test` includes a semantic retrieval assertion.

## Resolved Questions (2026-06-30)

All four open questions settled before branch creation:

1. **Reranker model placement → lazy-download + `--init`.** Models live in `~/.cache/leankg/models/`, SHA-256 verified. `embed --init` is the explicit setup; lazy-download is the fallback for users who skip it.
2. **Worktree exclusion → default-on.** `**/.worktrees/**`, `**/.claude/worktrees/**`, `**/.opencode/worktrees/**` filtered at ANN stage. `include_worktrees: bool = false` opt-in.
3. **Index freshness → incremental embed via `embedding_state`.** `index` marks touched nodes stale; `embed` does incremental batch on the stale/missing/changed-hash set; `embed --full` ignores state for recovery. Query-time `diagnostics.embeddings_stale` flags a stale index but still serves.
4. **Reranker fallback → option A (ANN-only).** Any reranker failure (missing model, load failure, OOM, panic) drops Stage 3 and returns ANN-order top-N. `diagnostics.reranker = "fallback_ann"` flag makes degradation visible to agents.

## Future Enhancements (explicitly deferred)

- Parallel multi-channel retrieval + RRF fusion (the full `hybrid-retrieval-reranking.md` design).
- Offline structural GNN embeddings (node2vec / GraphSAGE via PyG) as a second vector channel.
- Code body embeddings for "find by implementation detail" queries.
- LLM-assisted query intent classification (replaces the lightweight deterministic intent hints).
- Cross-repo embedding shards for multi-repo deployments.

## Risks

| Risk | Mitigation |
| --- | --- |
| Model download size blocks first-run UX | Lazy download with progress, cache reuse, document offline path |
| Reranker latency dominates | Cap rerank input at top-K=50; batch in one `ort` call |
| Traversal returns noise | Edge-type filter per seed type, global cap, dedup |
| Embeddings miss exact identifiers | Keep `semantic_search` and `find_function` unchanged; agents choose tool |
| Index drift after re-indexing | Timestamp check + explicit `embed` step + warning in MCP response |
| `ort` / ONNX runtime portability | fastembed bundles ONNX runtime; document supported targets; fall back to ANN-only on load failure |
