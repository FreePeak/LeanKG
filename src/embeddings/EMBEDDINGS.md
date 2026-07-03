# Embeddings Module

Semantic retrieval for LeanKG: dense-vector search over `code_elements` with
cross-encoder reranking and graph-aware traversal. Gated behind the
`embeddings` Cargo feature; off by default to keep the default binary slim.

## Why this exists

Code search by exact name (the rest of LeanKG) is exact but blind — it can't
answer "where do we do embedding inference" without already knowing the answer.
This module adds a path for natural-language queries: embed every code element
with a sentence transformer, store the vectors in CozoDB's native HNSW index,
retrieve the top-K candidates for a query, then rerank with a cross-encoder.
A final graph traversal stage enriches the seeds with their immediate neighbors
so the result is a small subgraph, not just a list of files.

## Feature gate

Add `--features embeddings` to every cargo invocation:

```bash
cargo build  --release --features embeddings
cargo run    --release --features embeddings -- embed --init
cargo test             --features embeddings
```

The flag pulls in `fastembed` (ONNX Runtime inference for embedder + reranker).
Vectors are stored in CozoDB's native HNSW index — no extra native deps.

## File map

| File | Role |
|------|------|
| `mod.rs` | Public re-exports. The crate surface for `embeddings::*`. |
| `models.rs` | `Embedder` (BGE-small-en-v1.5, 384-dim) and `Reranker` (bge-reranker-v2-m3) wrappers around fastembed. Also `init_models()` for `embed --init`. |
| `text_blob.rs` | Per-element text blob construction + SHA-256 content hash. Caps blobs at 1500 chars (≈512 BPE tokens). |
| `state.rs` | `embedding_state` CozoDB table + `embedding_vectors` relation + HNSW index. Helpers: mark_stale, list_all, upsert_fresh, delete_state_rows. |
| `build.rs` | Orchestrates the `embed` CLI: incremental vs full rebuild, orphan reap. Writes to `embedding_vectors` via `:put`. |

The retrieval pipeline (retrieve → rerank → traverse) lives in `src/retrieval/`
and the MCP tool `kg_semantic_context` lives in `src/mcp/`. They consume this
module; they don't define embedding policy.

## Data model

Two CozoDB relations, both under the `.leankg/leankg.db` SQLite file:

1. **`embedding_state`** — one row per tracked CodeElement, keyed by
   `qualified_name` (=> separator makes it the only key column so `:put`
   upserts correctly). Columns:
   `qualified_name: String => usearch_key: Int, content_hash: String, state: String, embedded_at: String`.
   `state` is `"fresh"` after embed, `"stale"` after the indexer touches the element.
   The `usearch_key` column is a legacy field kept at `0` — CozoDB HNSW keys
   on `qualified_name` directly, so the SHA-256-derived u64 indirection is
   no longer needed. It will be dropped in a future schema migration.

2. **`embedding_vectors`** — vector store, also keyed by `qualified_name`.
   `:create embedding_vectors {qualified_name: String => vector: <F32; 384>}`.
   The HNSW index `vec_idx` is created via:
   ```
   ::hnsw create embedding_vectors:vec_idx {
       dim: 384, dtype: F32, fields: [vector],
       distance: Cosine, ef_construction: 20, m: 50,
       extend_candidates: false, keep_pruned_connections: false
   }
   ```

The HNSW index is the source of truth for vector data. `embedding_state`
tracks freshness for incremental builds. Both live in CozoDB, so they share
the same transactional semantics as the rest of the KG.

## Embed pipeline (`embed` command)

Implemented in `build.rs::run`. Default mode is **incremental**; `--full`
re-embeds every element regardless of state.

```
1. graph.all_elements() -> Vec<CodeElement>
2. Build work list: each element -> WorkItem { qualified_name, blob, hash }
   (text_blob::classify() skips clusters/processes/etc.)
3. Diff against embedding_state:
   Incremental: embed if no state row, state != "fresh", or content_hash differs
   Full:        embed everything in the work list
4. For each chunk (default 32):
     embedder.embed(chunk) -> Vec<Vec<f32>>
     :put embedding_vectors {qualified_name => vector} (chunked at 500 rows
       per CozoDB query to keep pest parser input bounded)
     collect into fresh_rows
5. state::upsert_fresh(fresh_rows)  -- marks rows fresh, stamps content_hash
6. Orphan reap: existing_state.keys() NOT IN work_qns
     remove_vectors(orphan_qns)  -- :rm embedding_vectors
     state::delete_state_rows(orphan_rows)
```

`upsert_fresh`, `mark_stale_for_qualified_names`, and `delete_state_rows` all
chunk their CozoDB writes at 500 rows per query (see `UPSERT_CHUNK` in
`state.rs`) to keep pest parser input bounded on large repos.

## Retrieval pipeline

Lives in `src/retrieval/pipeline.rs`. Four stages:

1. **ANN retrieve** — embed query → `~embedding_vectors:vec_idx { ... | query: vec([...]), k, ef, bind_distance: dist }`. Returns `(qualified_name, distance)` pairs directly.
2. **Filter** — drop worktree paths (`.worktrees/`, `.claude/worktrees/`,
   `.opencode/worktrees/`) unless `--include-worktrees`; drop elements whose
   `env` doesn't match the requested env.
3. **Cross-encoder rerank** — bge-reranker-v2-m3 scores `(query, blob_excerpt)`
   pairs; truncate to `--rerank-top-n` (default 10).
4. **Graph enrichment (Stage 4)** — for each seed, traverse 1-hop neighbors
   via `relationships`. Disable with `--no-traverse`.

The CLI front-end is `leankg semantic-context "<query>"` (see `src/cli/mod.rs`
for flags). MCP front-end is the `kg_semantic_context` tool.

## Why native CozoDB HNSW (vs. usearch sidecar)

Earlier versions stored vectors in a `usearch` sidecar (`.leankg/embeddings.usearch`)
because the vendored CozoDB 0.2.2 predates HNSW support (HNSW landed in
CozoDB 0.6.0). Now that LeanKG is on CozoDB 0.7.6, vectors live natively in
CozoDB:

- **Transactional consistency.** Vectors and state share the same CozoDB
  transaction, so a crash mid-embed can't leave state marked fresh while
  vectors are missing (a known footgun with the old sidecar approach).
- **No second persistence artifact.** The vector store is part of the SQLite
  DB file, not a separate file that needs separate save/load + integrity
  management.
- **Single source of truth.** `embedding_vectors` is just another relation —
  no SHA-256-derived u64 indirection, no `qn_map` reverse lookup, no key
  collisions to worry about.
- **Built on CozoDB's official HNSW impl.** Same code path CozoDB itself
  ships and tests; no separate ANN library to track.

## Operational notes

### Memory: batch size, not thread count

`fastembed 4.9.1` calls `available_parallelism()` internally for ORT's
intra-op thread count and does **not** expose a public override. We bound
peak RSS via batch size instead:

| `--batch-size` | Approx peak RSS (10-core Mac) | When to use |
|---------------|-------------------------------|------------|
| 32 (default)  | ~1.3 GB                       | Workstation |
| 8             | ~730 MB                       | Memory-pressured host |
| 4             | ~400 MB                       | 1-vCPU container |

### Reranker failure is non-fatal

If the cross-encoder fails to load (model cache corrupt, ONNX init error), the
retrieval pipeline returns ANN-order top-N with `RerankerStatus::Fallback`.
The diagnostic counter in `semantic-context --debug` shows which path ran.

## First-time setup

```bash
# 1. Build with the feature
cargo build --release --features embeddings

# 2. Pre-download models (~2.3 GB) so the first embed doesn't pay the cost
./target/release/leankg embed --init

# 3. Build the index
./target/release/leankg embed                    # incremental (default)
./target/release/leankg embed --full             # force re-embed everything

# 4. Query
./target/release/leankg semantic-context "embedding inference" --debug
```

Models cache to `~/Library/Caches/leankg/models/` (macOS),
`~/.cache/leankg/models/` (Linux), or `%LOCALAPPDATA%\leankg\models` (Windows).

## Testing

Integration tests in `/tests/embeddings_state_e2e.rs` require the feature flag
but do NOT require the fastembed model cache — they exercise only the
state-table helpers against a fresh CozoDB instance. Run with
`cargo test --features embeddings --test embeddings_state_e2e`.

End-to-end validation (real `embed --full` + `semantic-context`) is the
responsibility of the release checklist, not the unit-test suite.
