# Embed model switch smoke (optional ops)

Multi-model embedding DB lets you flip `LEANKG_EMBED_ACTIVE_MODEL` between
providers (local ONNX BGE, OpenAI-compatible TEI/API) **without** deleting
other models' vector collections.

**Local merge gate:** `cargo test --features embeddings` in the feature
worktree. This smoke script is **optional ops** confirmation on a GPU host
or with a free embed API — not required to merge.

Related: `docs/embed-multi-model.md` (registry + env vars) and
`src/embeddings/registry.rs` (built-in model ids).

---

## Free / OpenAI-compatible embedding servers

Use with `LEANKG_EMBED_PROVIDER=openai`. Register each backend as its own
`model_id` in `embedding_models` (never mix dims in one collection).

| Option | Cost | Base URL | Typical dim | Best for |
|--------|------|----------|-------------|----------|
| **[Jina AI](https://jina.ai/embeddings/)** | Free trial (~1M–10M tokens) | `https://api.jina.ai/v1` | 1024 (`jina-embeddings-v3`) | **Recommended** laptop API smoke (no GPU) |
| **[Voyage AI](https://www.voyageai.com/)** | 200M free tokens | Voyage OpenAI-style API | 1024 (MRL) | Large free quota |
| **[Cohere Compatibility API](https://docs.cohere.com/docs/compatibility-api)** | Trial (~1k calls/mo) | `https://api.cohere.ai/compatibility/v1` | 256–1536 (`embed-v4.0`) | Alternate hosted |
| **[HF TEI](https://huggingface.co/docs/text-embeddings-inference/quick_tour)** | Self-host (GPU) | `http://<host>:8080/v1` | model-native (Qwen3-4B → **2560**) | GPU server Qwen sidecar |
| **[LocalEmbed](https://github.com/heshinth/LocalEmbed)** / fastembed Docker | Self-host CPU | `http://localhost:8000/v1` | often 384 (BGE) | CPU OpenAI stub |
| **[llm-mock](https://github.com/axium-lab/llm-mock)** | Free | hosted or local | configurable | Deterministic fake vectors only |

**Locked recommendation for this feature:**

1. **Unit tests:** `FakeEmbedProvider` + in-process TCP stub (no network).
2. **Optional laptop smoke:** Jina free key → `jina-embeddings-v3-1024`.
3. **GPU server smoke:** TEI serving `Qwen/Qwen3-Embedding-4B` (2560-d) ↔ local BGE (384-d).

---

## TEI bring-up (GPU host)

```bash
# Hugging Face Text Embeddings Inference — OpenAI-compatible /v1/embeddings
docker run --gpus all -p 8080:80 -v "$PWD/tei-data:/data" --pull always \
  ghcr.io/huggingface/text-embeddings-inference:1.7 \
  --model-id Qwen/Qwen3-Embedding-4B

# Health: curl -sf http://127.0.0.1:8080/health
# Embed:  curl -sf http://127.0.0.1:8080/v1/embeddings \
#   -H 'Content-Type: application/json' \
#   -d '{"model":"Qwen/Qwen3-Embedding-4B","input":"ping"}'
```

Then run the smoke script with defaults (`TEI_BASE=http://127.0.0.1:8080/v1`,
`TEI_DIM=2560`).

---

## Run the smoke script

From the repo root of this checkout:

```bash
# Postgres: the local dev container (leankg-pg-phase0, pgvector pg18, :5433)
# or any PG reachable via LEANKG_PG_URL.
export LEANKG_PG_URL="postgresql://postgres:postgres@localhost:5433/leankg"
# or: export PSQL="docker exec leankg-pg-phase0 psql -U postgres -d leankg"

# Build the CLI with embeddings and run the smoke (fixture, A→B→A switch,
# row-count assertions against Postgres).
./scripts/smoke-embed-model-switch.sh
```

The script resolves the CLI itself: `LEANKG_BIN` if set, else
`leankg-worker` / `leankg` on PATH, else `cargo run --release --features
embeddings --quiet --` from this repo.

### Environment variables

| Variable | Default | Meaning |
|----------|---------|---------|
| `MODEL_A` | `bge-small-en-v1.5-384` | Local registry id (ONNX) |
| `MODEL_B` | `qwen3-emb-4b-2560` | API/TEI registry id |
| `TEI_BASE` | `http://127.0.0.1:8080/v1` | OpenAI-compatible base URL |
| `TEI_MODEL` | `Qwen/Qwen3-Embedding-4B` | Model name sent to `/embeddings` |
| `TEI_DIM` | `2560` | Expected vector length from API |
| `LEANKG_EMBED_API_KEY` | `unused` | API key (required for Jina/Voyage/Cohere) |
| `FIXTURE_PROJECT` | `/tmp/leankg-embed-switch-fixture` | Tiny Rust tree to index/embed |
| `LEANKG_BIN` | auto (`leankg-worker`, `leankg`, or `cargo run`) | CLI binary |
| `LEANKG_PG_URL` / `PSQL` | — | Postgres for row counts |
| `SKIP_INDEX` | `0` | Set `1` if fixture already indexed |
| `SKIP_SEMANTIC` | `0` | Set `1` to skip optional MCP semantic_search |
| `EMBED_TYPES` | `function` | Limit embed scope for speed |
| `EMBED_WORKERS` / `EMBED_BATCH_SIZE` | `1` / `16` | Passed to `embed --wait` |

During embed steps the script also sets:

- `LEANKG_EMBED_ACTIVE_MODEL`, `LEANKG_EMBED_PROVIDER`
- For API: `LEANKG_EMBED_API_BASE_URL`, `LEANKG_EMBED_API_KEY`,
  `LEANKG_EMBED_API_MODEL`, `LEANKG_EMBED_API_DIM`
- For local: `LEANKG_EMBED_FAST`, `LEANKG_EMBED_MODEL` (default `bge-q`)

### Free hosted alternate (no GPU)

The built-in registry already contains `jina-embeddings-v3-1024`:

```bash
export JINA_API_KEY="..."   # https://jina.ai/?sui=apikey
export MODEL_B=jina-embeddings-v3-1024
export TEI_BASE=https://api.jina.ai/v1
export TEI_MODEL=jina-embeddings-v3
export TEI_DIM=1024
export LEANKG_EMBED_API_KEY="$JINA_API_KEY"
./scripts/smoke-embed-model-switch.sh
```

### Assertions (stable)

1. Embed under MODEL_A (local) → row count &gt; 0.
2. Switch to MODEL_B (API), cold embed → MODEL_B &gt; 0 **and** MODEL_A unchanged.
3. Switch pointer back to MODEL_A → both counts unchanged.
4. Optional: `semantic_search` via MCP on `:9699` when server is up.

Row counts use `embedding_vectors.model_id` when present, else
`embedding_vectors_<model_id_sanitized>`, with a legacy fallback for
pre-migration single-table installs (warns once).

---

## What this is not

- Not a substitute for `cargo test --features embeddings` (registry,
  migrations, ANN filter).
- Not a recall-quality benchmark (free APIs differ from BGE semantically).
- Not required on CI or laptop without TEI/Jina.

For production cold embed and MCP/worker split, see
`docs/deploy-server-with-cold-embed.md`.
