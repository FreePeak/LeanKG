# Multi-Model Embedding

LeanKG can switch the embedding model **without wiping existing vector
collections**. A model registry (`embedding_models`) maps each `model_id` to a
provider kind, dimension, and its own vector/state tables; the active model
is a pointer (`LEANKG_EMBED_ACTIVE_MODEL`, default BGE 384-d) read at
embed/query time. Each model gets its own collection so ANN never mixes
dimensions.

The feature lives in `src/embeddings/`:

| File | Role |
|------|------|
| `registry.rs` | Built-in model registry (`EmbeddingModelEntry`), active-model resolution, table-per-model naming |
| `provider.rs` | `EmbedProvider` trait, `OpenAiCompatibleProvider` (blocking reqwest), `LocalOnnxProvider` (384-d only), `FakeEmbedProvider`, factories |
| `state.rs` | `ensure_embedding_state_table` / `ensure_model_collections` (create the active model's state+vector relations + HNSW index), `drop_hnsw_index` / `create_hnsw_index` |

## Architecture

```
LEANKG_EMBED_ACTIVE_MODEL ──┐
LEANKG_EMBED_PROVIDER ──────┤
LEANKG_EMBED_API_* ─────────┤  resolve at embed + query time
                            ▼
              registry (src/embeddings/registry.rs)
         model_id → provider kind, dims, collection names
                            │
        ┌───────────────────┴────────────────────┐
        ▼                                       ▼
LocalOnnxProvider                         OpenAiCompatibleProvider
(ONNX, 384-d only)                        (POST /v1/embeddings, any dim)
        │                                       │
        ▼                                       ▼
 embedding_vectors                embedding_vectors_<model_id>
 embedding_state                  embedding_state_<model_id>
```

One active model per process: embed writes into the active model's collection,
`SemanticRetrievalPipeline` queries `~<active_vectors_relation>:vec_idx`.
Pointing the pointer at another model re-targets both write and query paths
without touching any other collection.

## Built-in model registry

`src/embeddings/registry.rs::builtin_registry()`; mirrored by migration
`002` in Postgres (`embedding_models` table):

| model_id | Provider | API model name | Dims | Vector table | State table |
|----------|----------|----------------|------|--------------|-------------|
| `bge-small-en-v1.5-384` (default) | local ONNX | `bge-small-en-v1.5` | 384 | `embedding_vectors` (legacy) | `embedding_state` (legacy) |
| `qwen3-emb-4b-2560` | openai | `Qwen/Qwen3-Embedding-4B` | 2560 | `embedding_vectors_qwen3_emb_4b_2560` | `embedding_state_qwen3_emb_4b_2560` |
| `jina-embeddings-v3-1024` | openai | `jina-embeddings-v3` | 1024 | `embedding_vectors_jina_embeddings_v3_1024` | `embedding_state_jina_embeddings_v3_1024` |

The default BGE model keeps the legacy `embedding_vectors` / `embedding_state`
names for backward compatibility; other models get `<table>_<model_id>` with
`-` sanitized to `_`. `LEANKG_EMBED_ACTIVE_MODEL` with an id not in the
registry is a config error.

## Environment variables

### Model selection

| Variable | Default | Meaning |
|----------|---------|---------|
| `LEANKG_EMBED_ACTIVE_MODEL` | `bge-small-en-v1.5-384` | Which registry entry is active (write + query target) |

### Provider selection

| Variable | Default | Meaning |
|----------|---------|---------|
| `LEANKG_EMBED_PROVIDER` | `local` | `local` (or `onnx`) / `openai` (or `api`, `openai-compatible`). Overrides the registry entry's provider kind |
| `LEANKG_EMBED_API_BASE_URL` | — | Base URL of the OpenAI-compatible server (required for `openai`) |
| `LEANKG_EMBED_API_KEY` | — | Bearer token (required for `openai`) |
| `LEANKG_EMBED_API_MODEL` | `bge-small-en-v1.5` | Model name sent in the `/v1/embeddings` body |
| `LEANKG_EMBED_API_DIM` | registry dim | Expected vector length; response mismatches are an error |

### Local ONNX tuning

| Variable | Default | Meaning |
|----------|---------|---------|
| `LEANKG_EMBED_DIRECT` | `1` | `1` = DirectEmbedder (preferred), `0` = fastembed `Embedder` fallback |
| `LEANKG_EMBED_DIRECT_INTRA` | `1` | ORT intra-op threads for DirectEmbedder |
| `LEANKG_EMBED_FAST` | — | Quantized-ONNX toggle for the fastembed path (silences the FP32 fallback note when `0`) |
| `LEANKG_EMBED_MODEL` | `bge` | fastembed model variant name |
| `LEANKG_EMBED_MAX_MB` | — | Soft embed memory budget (MB); `0` = no cap |
| `LEANKG_EMBED_MAX_SEQ` | — | Max sequence length (set into the ORT session env) |
| `LEANKG_EMBED_BACKGROUND` | — | `1` = in-process background embed for `mcp-http` mode |

### HNSW knobs

| Variable | Default | Meaning |
|----------|---------|---------|
| `LEANKG_HNSW_M` | `50` | Index build-time `m` (max connections per node). Raise for recall, lower for RSS. Clamped 4–256 |
| `LEANKG_HNSW_EF_CONST` | `20` | Index build-time `ef_construction` (1–2000). PG path forces `>= 2*m`; also emitted as the `hnsw.ef_construction` GUC on per-`:put` writes to `embedding_vectors` |
| `LEANKG_HNSW_EF` | k | Query-time `ef` (absolute, overrides default). No re-index needed |

CLI flags for the `embed` command: `--batch-size` (default 32), `--workers`
(default 2), `--types` (comma-separated element types, e.g. `function,method`).

## Switching providers

`LEANKG_EMBED_ACTIVE_MODEL` + `LEANKG_EMBED_PROVIDER` decide the provider;
`LEANKG_EMBED_API_*` configure the OpenAI-compatible path.

### Local ONNX (default, no network)

```bash
export LEANKG_EMBED_ACTIVE_MODEL=bge-small-en-v1.5-384   # default anyway
export LEANKG_EMBED_PROVIDER=local
cargo run --release --features embeddings -- embed --init
cargo run --release --features embeddings -- embed --wait --project /path/to/repo
```

### OpenAI-compatible API (TEI / Jina / Voyage / Cohere, self-hosted)

Any server speaking `POST /v1/embeddings` works — a self-hosted TEI serving
`Qwen/Qwen3-Embedding-4B`, or a hosted free API (see
`docs/embed-model-switch-smoke.md` for the option table).

```bash
export LEANKG_EMBED_ACTIVE_MODEL=qwen3-emb-4b-2560
export LEANKG_EMBED_PROVIDER=openai
export LEANKG_EMBED_API_BASE_URL=http://127.0.0.1:8080/v1
export LEANKG_EMBED_API_KEY=sk-...        # "unused" for local TEI
export LEANKG_EMBED_API_MODEL=Qwen/Qwen3-Embedding-4B
export LEANKG_EMBED_API_DIM=2560          # optional; registry dim is the default
cargo run --release --features embeddings -- embed --wait --project /path/to/repo
```

Example for Jina's hosted API (no GPU needed):

```bash
export LEANKG_EMBED_ACTIVE_MODEL=jina-embeddings-v3-1024
export LEANKG_EMBED_PROVIDER=openai
export LEANKG_EMBED_API_BASE_URL=https://api.jina.ai/v1
export LEANKG_EMBED_API_KEY="$JINA_API_KEY"
export LEANKG_EMBED_API_MODEL=jina-embeddings-v3
```

Query-time embedding uses the same env: `semantic-context` /
`kg_semantic_context` embed the query with the active model and ANN-query the
active collection.

## Migration notes

Postgres migration `002_multi_model_embed` (registered in
`src/db/pg/migrations.rs`, applied by `leankg migrate` or automatically on
writer `init_db`) creates:

- `embedding_models` — registry table, seeded with the three built-in entries
  (`ON CONFLICT DO NOTHING`).
- `embedding_active` — `scope` / `model_id` pointer, seeded to
  `bge-small-en-v1.5-384`.
- Per-model vector + state tables with HNSW indexes for the non-legacy
  models (`embedding_vectors_qwen3_emb_4b_2560`, `embedding_state_qwen3_emb_4b_2560`,
  and the jina pair), so each dim is backed by its own `vector(n)` column.

Legacy single-model installs stay on `embedding_vectors` /
`embedding_state` (BGE 384-d), which are the default model's tables — no
data movement. The Rust registry (`builtin_registry()`) is the runtime source
of truth; the PG `embedding_models` table is the durable record for operators
and row-count tooling (the smoke script falls back from `model_id` column to
`embedding_vectors_<model_id>` table to legacy single-table counts).

## Limitations

- **Local ONNX is 384-d only.** `LocalOnnxProvider::new` errors unless the
  active model's dim is 384. Other dims (Qwen 2560, Jina 1024) require the
  OpenAI-compatible path.
- **Switching models requires re-embedding the target collection.** The
  pointer flip is read at embed/query time, but vectors are written by
  explicit `embed` runs. An un-embedded model id simply has an empty
  collection (ANN queries return nothing). The pointer preserves **existing**
  collections — it never migrates or copies rows between them.
- **No automatic dimension migration.** Each collection is fixed at its
  table's `vector(n)` / `<F32; dim>`; to change a model's dim you cold-embed
  into a new model_id.
- `LEANKG_EMBED_PROVIDER` can override the registry's provider kind for a
  given model (e.g. serve BGE 384-d from an API); the active model's
  registry dim still wins for validation.

## Testing

```bash
cargo test --features embeddings --test multi_model_embed_tests
```

Registry unit tests live in `src/embeddings/registry.rs` (active-model
resolution, legacy-vs-per-model naming, dim validation). Ops-level A → B → A
collection-preservation smoke: `docs/embed-model-switch-smoke.md` +
`scripts/smoke-embed-model-switch.sh`.
