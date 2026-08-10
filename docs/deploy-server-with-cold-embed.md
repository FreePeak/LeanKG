# LeanKG Server Deployment with Cold Embedding

Production deployment of LeanKG as **two processes** against shared Postgres +
pgvector: a long-lived **query-only MCP** (`leankg-mcp`) and a pipeline
**worker** (`leankg-worker`) that owns index / cold embed. MCP stays up and
answers RO queries while the worker indexes or embeds (MVCC; readers are not
blocked by the index advisory lock).

The compat `leankg` binary still works as a thin facade; prefer the split
binaries in production.

## Architecture

```
┌──────────────────────────────────┐
│   leankg-worker (one-shot/cron)  │  index + cold embed (local ONNX or API)
│   ─ leankg-worker index /app     │
│   ─ leankg-worker embed --wait   │
└──────────────┬───────────────────┘
               │ writes (RW + advisory lock)
               ▼
┌──────────────────────────────────┐    ┌─────────────────────────┐
│   leankg-mcp (HTTP :9699)        │◀──▶│  leankg-db (pgvector)   │
│   query-only, --read-only        │    │  pgdata volume           │
│   no auto-index / no bulk embed  │    │                          │
└──────────────────────────────────┘    └─────────────────────────┘
```

| Binary | Role | PG access | Owns |
|--------|------|-----------|------|
| `leankg-mcp` | HTTP/stdio MCP, **read-only** | RO pool | Query tools; query-time embed of the search string (+ optional rerank) |
| `leankg-worker` | Pipeline | RW + `index_advisory_lock` | `index`, `watch`, `embed`, bulk COPY into `embedding_vectors` |

Two containers (or host processes), one shared DB. The worker runs once per
index cycle (cold start, cron, or git hook); the MCP server runs continuously
and does **not** stop when the worker runs.

## 1. Build the embed-enabled image

The Hub image `freepeak/leankg:latest` is built without `--features=embeddings`
(slim). Cold local ONNX embed requires the embeddings feature. Build it locally:

```bash
docker build -f Dockerfile.embed -t freepeak/leankg:embeddings .
```

> **Build network note:** Docker build must reach `deb.debian.org` to fetch
> `clang`, `libclang-dev`, `libssl-dev`. If your build environment blocks
> these mirrors (e.g. air-gapped, sandboxed), pre-stage the apt cache on
> the host or use an internal Debian mirror.

For API-only embedding (`LEANKG_EMBED_PROVIDER=openai`), the slim image can
work without ONNX if query-time and bulk embed both use the HTTP provider.

## 2. Compose stack

`docker-compose.yml` (committed) — service + DB:

```yaml
services:
  postgres:
    image: pgvector/pgvector:pg18
    container_name: leankg-db
    restart: unless-stopped
    environment:
      POSTGRES_DB: leankg
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: ${LEANKG_DB_PASSWORD}
    ports:
      - "5432:5432"
    volumes:
      - leankg-pgdata:/var/lib/postgresql
      - ./pg-conf/postgresql.conf:/etc/postgresql/postgresql.conf:ro
    command: ["postgres", "-c", "config_file=/etc/postgresql/postgresql.conf"]

  leankg-worker:
    # One-shot: runs index + embed, then exits.
    # Re-run via cron / on git push to refresh the index + re-embed.
    # MCP stays up the whole time — do not stop leankg-mcp.
    image: freepeak/leankg:embeddings
    profiles: ["embed"]        # opt-in: `docker compose --profile embed run --rm leankg-worker`
    restart: "no"
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      LEANKG_PG_URL: postgresql://postgres:${LEANKG_DB_PASSWORD}@postgres:5432/leankg
      # Embed provider: local (default, needs embeddings feature) or openai
      LEANKG_EMBED_PROVIDER: local
      # LEANKG_EMBED_PROVIDER: openai
      # LEANKG_EMBED_API_BASE_URL: https://api.openai.com/v1
      # LEANKG_EMBED_API_KEY: ${LEANKG_EMBED_API_KEY}
      # LEANKG_EMBED_API_MODEL: text-embedding-3-small
      # LEANKG_EMBED_API_DIM: "384"   # must equal VEC_DIM
      LEANKG_EMBED_FAST: "1"
      LEANKG_EMBED_MODEL: bge-q
      LEANKG_EMBED_MAX_SEQ: "128"
      LEANKG_EMBED_MAX_MB: "0"     # unlimited on the worker (separate container)
      LEANKG_INSERT_BATCH_SIZE: "20000"
    volumes:
      - ${LEANKG_PROJECT_DIR}:/app
      - ${LEANKG_MODELS_CACHE_DIR}:/root/.cache/leankg
    # entrypoint examples (override per run):
    #   leankg-worker index /app
    #   leankg-worker embed --wait --project /app

  leankg-mcp:
    # Long-lived query-only MCP. No auto-index, no bulk/background embed.
    # Corpus refresh is owned entirely by leankg-worker.
    image: freepeak/leankg:embeddings
    container_name: leankg-mcp
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
    ports:
      - "9699:9699"
    mem_limit: 12g
    environment:
      LEANKG_PG_URL: postgresql://postgres:${LEANKG_DB_PASSWORD}@postgres:5432/leankg
      LEANKG_MCP_PROJECT: /app
      LEANKG_AUTO_INDEX: "0"           # index owned by leankg-worker
      LEANKG_EMBED_BACKGROUND: "0"     # bulk embed owned by leankg-worker
      LEANKG_EMBED_AUTO_ARM: "0"
      # Query-time embed of the search string (same provider as worker recommended)
      LEANKG_EMBED_PROVIDER: local
      LEANKG_EMBED_FAST: "1"
      LEANKG_EMBED_MODEL: bge-q
      LEANKG_EMBED_MAX_SEQ: "128"
      LEANKG_EMBED_MAX_BLOB_CHARS: "500"
      LEANKG_EMBED_MAX_MB: "512"
      LEANKG_ONTOLOGY_SYNC_ON_BOOT: timeout
      LEANKG_ONTOLOGY_SYNC_TIMEOUT_SECS: "45"
      LEANKG_MCP_TOOL_TIMEOUT_SECS: "300"
      LEANKG_SERVE_HTTP: "0"
    command: ["leankg-mcp", "mcp-http", "--port", "9699", "--project", "/app"]
    volumes:
      - ${LEANKG_PROJECT_DIR}:/app
      - ${LEANKG_MODELS_CACHE_DIR}:/root/.cache/leankg

volumes:
  leankg-pgdata:
```

`pg-conf/postgresql.conf`:

```conf
max_wal_size = 4GB
min_wal_size = 1GB
shared_buffers = 2GB
checkpoint_timeout = 30min
checkpoint_completion_target = 0.9
maintenance_work_mem = 256MB
wal_compression = on
effective_cache_size = 6GB
random_page_cost = 1.1
```

`.env`:

```env
LEANKG_DB_PASSWORD=change-me
LEANKG_PROJECT_DIR=/srv/code/be
LEANKG_MODELS_CACHE_DIR=/srv/leankg/models
```

### Embed provider env (worker + MCP query-time)

| Variable | Meaning |
|----------|---------|
| `LEANKG_EMBED_PROVIDER` | `local` (default) or `openai` |
| `LEANKG_EMBED_API_BASE_URL` | OpenAI-compatible base URL (required for `openai`) |
| `LEANKG_EMBED_API_KEY` | API key (required for `openai`) |
| `LEANKG_EMBED_API_MODEL` | Model id |
| `LEANKG_EMBED_API_DIM` | Must equal vector dim **384** or init fails |

## 3. First bootstrap

```bash
# 1. Bring up the DB.
docker compose up -d postgres

# 2. Cold index + embed via the worker (MCP not required yet).
docker compose --profile embed run --rm --entrypoint leankg leankg-worker \
  migrate
docker compose --profile embed run --rm leankg-worker \
  leankg-worker index /app
docker compose --profile embed run --rm leankg-worker \
  leankg-worker embed --init --project /app    # downloads models (local provider)
docker compose --profile embed run --rm leankg-worker \
  leankg-worker embed --wait --project /app \
  --workers 8 --batch-size 128 \
  --types function,method

# 3. Start the long-lived query-only MCP server.
docker compose up -d leankg-mcp
curl -fsS http://localhost:9699/health
```

Host / local-dev equivalent (no Docker):

```bash
# Terminal A — query-only MCP (stays up)
leankg-mcp mcp-http --port 9699 --project /path/to/repo

# Terminal B — pipeline when needed (MCP keeps answering)
leankg-worker index /path/to/repo
leankg-worker embed --wait --project /path/to/repo
```

## 4. Refresh cycle (cron / git hook)

Cold embed is idempotent but slow (10-60 min on a mega-graph). Run the
**worker** on a schedule; leave `leankg-mcp` running:

```bash
# /etc/cron.d/leankg-reindex
# 02:00 daily: full re-index + cold embed. Uses LEANKG_FORCE_REINDEX=1
# to wipe and rebuild. Set to 0 (or omit) for incremental.
# Do NOT stop leankg-mcp — RO queries continue during the job.
0 2 * * *  cd /srv/leankg && \
  LEANKG_FORCE_REINDEX=1 \
  docker compose --profile embed run --rm leankg-worker \
    bash -c "leankg-worker index /app && leankg-worker embed --wait --project /app"
```

For git-push triggered refresh, hook the same worker command in the deploy
pipeline.

## 4b. Live embed (MCP stays up)

On Postgres, embedding and indexing run **while MCP stays up**. The worker
holds the index advisory lock only for the index job; MCP’s RO pool continues
to serve `mcp_status` / `search_code` / semantic tools (readers may see slight
MVCC lag until the worker commits).

```bash
# Build the embed-capable image if using local ONNX.
docker build --build-arg LEANKG_FEATURES=embeddings -t freepeak/leankg:embeddings .

# Trigger a cold embed against the live DB — do not restart leankg-mcp.
docker run --rm --network host \
  -e LEANKG_PG_URL="postgresql://postgres:postgres@localhost:5432/leankg" \
  -e LEANKG_EMBED_PROVIDER=local \
  -e LEANKG_EMBED_FAST=1 -e LEANKG_EMBED_MODEL=bge-q \
  -e LEANKG_EMBED_MAX_MB=2048 -e LEANKG_EMBED_MAX_BLOB_CHARS=500 \
  -e OMP_NUM_THREADS=1 \
  -v leankg-models:/root/.cache/leankg \
  --entrypoint leankg-worker freepeak/leankg:embeddings \
  embed --wait --project /app --workers 2 --batch-size 64
```

Verify: `/health` stays `ok` throughout, and
`SELECT count(*) FROM embedding_vectors` grows.

> **Do not** re-enable `LEANKG_EMBED_BACKGROUND` / `LEANKG_EMBED_AUTO_ARM` on
> `leankg-mcp` for production refresh — that couples serving to bulk embed.
> Use `leankg-worker` instead. Compat `leankg mcp-http` without `--read-only`
> still exists for legacy single-process setups.

## 5. Why this split works

The old `LEANKG_DOCKER_SETUP=1` entrypoint mode blocks `mcp-http` start on
embed completion. That ties uptime to embed duration (bad for SLOs).

Splitting into two binaries / containers:

| Concern | `leankg-worker` | `leankg-mcp` |
|---|---|---|
| Index | ✔ | — (RO; no auto-index) |
| Cold / bulk embed | ✔ | — (query-time embed of search string only) |
| Serve MCP | — | ✔ (read-only tools) |
| Lifecycle | one-shot / cron / watch | long-lived |
| Memory | unlimited | capped (12g) |
| Restart on edit | yes | no |

MCP never auto-indexes, never file-watch reindexes, and never arms background
corpus embed. The worker owns those pipelines.

## 6. Operations

- **Logs:** `docker compose logs -f leankg-mcp` (server) / `... leankg-worker` (pipeline).
- **Restart MCP without losing embeddings:** `docker compose restart leankg-mcp`. Embeds persist in pg.
- **Schema migrate (rare):** `docker compose --profile embed run --rm --entrypoint leankg leankg-worker migrate` (compat `leankg`; not on `leankg-worker` CLI).
- **Doctor:** `docker compose exec leankg-mcp leankg doctor`.
- **Backups:** snapshot `leankg-pgdata` volume. Models cache in `LEANKG_MODELS_CACHE_DIR` is re-downloadable.
- **Local restart script:** repo `scripts/restart-leankg-mcp.sh` starts MCP with auto-index/embed off; run `leankg-worker` separately for refresh.

## 7. Multi-project

For multi-repo deployments, mount each project + run the worker per project
sequentially (or parallel workers against the same DB — advisory lock
serializes concurrent index; embed is multi-writer). Leave MCP running.

```bash
docker compose --profile embed run --rm leankg-worker \
  bash -c 'for p in /app /app-be; do
             leankg-worker index "$p"
             leankg-worker embed --wait --project "$p"
           done'
```

MCP mounts both projects and serves per-request via `project=` arg.
