# LeanKG Server Deployment with Cold Embedding

Production deployment of the LeanKG MCP HTTP server with cold (offline) embed
pipeline. Postgres + pgvector is the single storage engine; offline INT8
embed runs in a one-shot worker before the long-lived MCP server starts.

## Architecture

```
┌──────────────────────────────┐
│   leankg-embed (one-shot)    │  cold INT8 embed, exits 0 when done
│   ─ leankg index /workspace  │
│   ─ leankg embed --wait      │
└──────────────┬───────────────┘
               │ writes
               ▼
┌──────────────────────────────┐    ┌─────────────────────────┐
│   leankg-mcp (HTTP :9699)    │◀──▶│  leankg-db (pgvector)   │
│   long-lived, serves MCP     │    │  pgdata volume           │
└──────────────────────────────┘    └─────────────────────────┘
```

Two containers, one shared DB volume. The embed worker runs once per index
cycle (cold start or `--force-reindex`); the MCP server runs continuously.

## 1. Build the embed-enabled binary + worker image

The Hub image `freepeak/leankg:latest` is built without `--features=embeddings`
(slim). Cold embed requires the embeddings feature. Build the binary
locally with the feature on, then assemble the slim worker image that
ships it:

```bash
cargo build --release --features=embeddings
mkdir -p .docker-build && cp target/release/leankg .docker-build/leankg
docker build -f Dockerfile.embed-worker -t freepeak/leankg:embed-worker .
```

> **Build network note:** the cargo build must reach `deb.debian.org` to
> fetch `clang`, `libclang-dev`, `libssl-dev` for fastembed. If your build
> environment blocks these mirrors (e.g. air-gapped, sandboxed), pre-stage
> the apt cache on the host or use an internal Debian mirror.

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

  leankg-embed:
    # One-shot: runs `leankg index ... && leankg embed --wait`, then exits.
    # Re-run via cron / on git push to refresh the index + re-embed.
    image: freepeak/leankg:embeddings
    profiles: ["embed"]        # opt-in: `docker compose --profile embed run --rm leankg-embed`
    restart: "no"
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      LEANKG_PG_URL: postgresql://postgres:${LEANKG_DB_PASSWORD}@postgres:5432/leankg
      LEANKG_EMBED_FAST: "1"
      LEANKG_EMBED_MODEL: bge-q
      LEANKG_EMBED_MAX_SEQ: "128"
      LEANKG_EMBED_MAX_MB: "0"     # unlimited on the worker (separate container)
      LEANKG_INSERT_BATCH_SIZE: "20000"
      MCP_HTTP_PORT: "9699"
    volumes:
      - ${LEANKG_PROJECT_DIR}:/workspace
      - ${LEANKG_MODELS_CACHE_DIR}:/root/.cache/leankg

  leankg-mcp:
    # Long-lived HTTP MCP server. Embed runs in-process and ARMS on idle.
    # For deterministic "embed complete before serving", run the embed
    # worker first (above), then start this service.
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
      LEANKG_MCP_PROJECT: /workspace
      LEANKG_AUTO_INDEX: "0"      # index is owned by leankg-embed worker
      LEANKG_EMBED_BACKGROUND: "1"
      LEANKG_EMBED_AUTO_ARM: "1"
      LEANKG_EMBED_FAST: "1"
      LEANKG_EMBED_MODEL: bge-q
      LEANKG_EMBED_MAX_SEQ: "128"
      LEANKG_EMBED_MAX_BLOB_CHARS: "500"
      LEANKG_EMBED_MAX_MB: "512"
      LEANKG_EMBED_BACKGROUND_WORKERS: "2"
      LEANKG_EMBED_BACKGROUND_BATCH: "128"
      LEANKG_EMBED_IDLE_AFTER_SECS: "30"
      LEANKG_EMBED_PARTIAL_BATCHES: "4"
      LEANKG_EMBED_PARTIAL_PAUSE_MS: "500"
      LEANKG_ONTOLOGY_SYNC_ON_BOOT: timeout
      LEANKG_ONTOLOGY_SYNC_TIMEOUT_SECS: "45"
      LEANKG_MCP_TOOL_TIMEOUT_SECS: "300"
      LEANKG_INSERT_BATCH_SIZE: "20000"
      LEANKG_SERVE_HTTP: "0"
    volumes:
      - ${LEANKG_PROJECT_DIR}:/workspace
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

## 3. First bootstrap

```bash
# 1. Bring up the DB.
docker compose up -d postgres

# 2. Cold embed + index. Runs once; exits 0 when both are done.
docker compose --profile embed run --rm leankg-embed \
  leankg migrate
docker compose --profile embed run --rm leankg-embed \
  leankg index /workspace
docker compose --profile embed run --rm leankg-embed \
  leankg embed --init --project /workspace    # downloads models
docker compose --profile embed run --rm leankg-embed \
  leankg embed --wait --project /workspace \
  --workers 8 --batch-size 128 \
  --types function,method

# 3. Start the long-lived MCP server.
docker compose up -d leankg-mcp
curl -fsS http://localhost:9699/health
```

## 4. Refresh cycle (cron / git hook)

Cold embed is idempotent but slow (10-60 min on a mega-graph). On the
production server, run it on a schedule, not on every request:

```bash
# /etc/cron.d/leankg-reindex
# 02:00 daily: full re-index + cold embed. Uses LEANKG_FORCE_REINDEX=1
# to wipe and rebuild. Set to 0 (or omit) for incremental.
0 2 * * *  cd /srv/leankg && \
  LEANKG_FORCE_REINDEX=1 \
  docker compose --profile embed run --rm leankg-embed \
    bash -c "leankg index /workspace && leankg embed --wait --project /workspace"
```

For git-push triggered refresh, hook the same command in the deploy pipeline.

## 4b. Live embed (no server stop)

On Postgres the RocksDB single-writer constraint is gone. Embedding and
indexing can run **while the MCP server stays up** — the server and a
throwaway embed container write the same `embedding_vectors` / tables via
`LEANKG_PG_URL`, and the index advisory lock is scoped to the `leankg index`
job (never blocks serving).

The only prerequisite: the image must be built with the embeddings feature
(`--build-arg LEANKG_FEATURES=embeddings`). Without it, `leankg embed` and
`embed_control` don't exist in the binary — the compose env
(`LEANKG_EMBED_BACKGROUND=1`/`AUTO_ARM=1`) silently no-ops.

```bash
# Build the embed-capable image (HTTPS apt mirror — sandbox-safe).
docker build --build-arg LEANKG_FEATURES=embeddings -t freepeak/leankg:embeddings .

# Trigger a cold embed against the live server's PG — server never stops.
# Models auto-download into the shared model volume on first run.
docker run --rm --network host \
  -e LEANKG_PG_URL="postgresql://postgres:postgres@localhost:5432/leankg" \
  -e LEANKG_EMBED_FAST=1 -e LEANKG_EMBED_MODEL=bge-q \
  -e LEANKG_EMBED_MAX_MB=2048 -e LEANKG_EMBED_MAX_BLOB_CHARS=500 \
  -e OMP_NUM_THREADS=1 \
  -v leankg-models:/root/.cache/leankg \
  --entrypoint leankg freepeak/leankg:embeddings \
  embed --wait --project /workspace --workers 2 --batch-size 64

# Or trigger in-process on the server itself (no extra container):
# set LEANKG_EMBED_BACKGROUND=1 + LEANKG_EMBED_AUTO_ARM=1 and the server
# auto-arms on idle, or call the embed_control MCP tool action=on.
```

Verify: `/health` stays `ok` throughout, and
`SELECT count(*) FROM embedding_vectors` grows.

## 5. Why this split works

The old `LEANKG_DOCKER_SETUP=1` entrypoint mode blocks `mcp-http` start on
embed completion. That ties uptime to embed duration (bad for SLOs).

Splitting into two containers:

| Concern | Worker | Server |
|---|---|---|
| Index | ✔ | — |
| Cold embed | ✔ | — |
| Serve MCP | — | ✔ |
| Lifecycle | one-shot | long-lived |
| Memory | unlimited | capped (12g) |
| Restart on edit | yes | no |

The MCP server still embeds in-process for incremental updates
(`LEANKG_EMBED_BACKGROUND=1` + `LEANKG_EMBED_AUTO_ARM=1`) — it picks up
newly-indexed elements on idle. Cold embed is the periodic full refresh,
not the runtime data path.

## 6. Operations

- **Logs:** `docker compose logs -f leankg-mcp` (server) / `... leankg-embed` (worker).
- **Restart server without losing embeddings:** `docker compose restart leankg-mcp`. Embeds persist in pg.
- **Schema migrate (rare):** `docker compose --profile embed run --rm leankg-embed leankg migrate`.
- **Doctor (stale fd / mmap):** `docker compose exec leankg-mcp leankg doctor`.
- **Backups:** snapshot `leankg-pgdata` volume. Models cache in `LEANKG_MODELS_CACHE_DIR` is re-downloadable.

## 7. Multi-project

For multi-repo deployments, mount each project + run worker per project
sequentially, or run workers in parallel against the same DB (Postgres
advisory lock prevents concurrent index; embed is multi-writer — the
server and a throwaway embed container can write `embedding_vectors`
concurrently against the same Postgres, so no `docker compose stop` is
needed to refresh embeddings).

```bash
docker compose --profile embed run --rm leankg-embed \
  bash -c 'for p in /workspace /workspace-be; do
             leankg index "$p"
             leankg embed --wait --project "$p"
           done'
```

MCP server mounts both projects and serves per-request via `project=` arg.
