# LeanKG Cold Embed Report — 3 Workspaces

Date: 2026-07-17 (UTC+7); verification addendum 2026-07-18
Operator: Docker MCP HTTP path (`leankg-leankg-1`, port 9699)
Mode: Offline cold embed (`embed --wait` via `docker run`, single-writer RocksDB) + live MCP semantic probes

> **Path hygiene:** Host bind examples use placeholders only. Never commit personal monorepo host paths (`/Users/…/work/be`, `/workspace-be`, etc.).

## Scope

| Project | Container path | Source (placeholder) | RocksDB path (container) |
|---|---|---|---|
| LeanKG primary | `/workspace` | `$PWD` (this repo) | `/data/leankg-rocksdb/projects/workspace-<hash>` |
| Side monorepo | `/workspace-other` | `/Users/you/work/other-repo` | `/data/leankg-rocksdb/projects/workspace-other-<hash>` |
| Freepeak polyrepo | `/workspace-freepeak` | sibling freepeak tree | `/data/leankg-rocksdb/projects/workspace-freepeak-<hash>` |

`.dockerfile` declares `LEANKG_PROJECT_DIRS=/workspace,/workspace-other,/workspace-freepeak`.
`docker-compose.override.yml` (gitignored) provides side-repo binds + mega-graph resource overrides.

## Pre-flight

- Docker MCP container was found crash-looping when a declared mount was missing.
  Cause: previous start did not pick up binds in `docker-compose.override.yml`.
- Verified mounts after restart (3 binds + 2 named volumes).
- `mcp_status(project=/workspace)` → `index_populated: true`, storage_engine=rocksdb.

## Embed runs

Embed plan §"Part B" sequence:
1. `docker compose -f docker-compose.rocksdb.yml stop leankg` (single-writer safety)
2. `docker run --rm` per workspace with `--project` flag, INT8 fast path
3. `docker compose -f docker-compose.rocksdb.yml start leankg`

Resource envelope for offline embed / multi-project MCP (v3.7.3):
- **cpus: `"6"`**
- **mem_reservation: `3g`**
- Offline embed `mem_limit: 10g`; multi-project MCP serving mega-graphs: **`mem_limit: 6g`** (override; base single-project Local survival remains documentable at 2g)

Common env vars per run:
```
LEANKG_DB_ENGINE=rocksdb
LEANKG_ROCKSDB_ROOT=/data/leankg-rocksdb
LEANKG_MMAP_SIZE=67108864
LEANKG_EMBED_FAST=1
LEANKG_EMBED_MODEL=bge-q
LEANKG_EMBED_MAX_SEQ=128
LEANKG_EMBED_MAX_BLOB_CHARS=500
LEANKG_EMBED_MAX_MB=0
OMP_NUM_THREADS=1
RUST_LOG=leankg=info
```

Command:
```
leankg embed --wait --project <workspace> --workers 8 --batch-size 128 --types function,method
```

## Cold embed results (function,method this session)

| Project | Items embedded this run | HNSW rebuild | Orphans | Exit code |
|---|---|---|---|---|
| /workspace | 64 | 1.84 s | 0 | 0 |
| /workspace-other | 2660 (length-sorted) | n/a (output truncated by harness) | n/a | 0 |
| /workspace-freepeak | 137 | 12.60 s | 0 | 0 |

Note: the **2,660** `function,method` items for `/workspace-other` this cold run are a **strict subset** of total stored vectors — the remainder came from prior background / earlier-session embed runs.

## Direct vector store verification (2026-07-18)

`run_raw_query` count of `embedding_vectors` rows (schema:
`{ qualified_name: String => vector: <F32; 384> }`, BGE-small-en-v1.5 INT8, HNSW
`embedding_vectors:vec_idx` registered):

| Project | Embeddings stored |
|---|---:|
| `/workspace` | **3,271** |
| `/workspace-other` | **146,977** |
| `/workspace-freepeak` | **14,110** |

## End-to-end `semantic_search` (HNSW + rerank)

| Project | Query | Status | Top hit (sanitized) |
|---|---|---|---|
| `/workspace` | `embeddings engine HNSW` | OK | `src/embeddings/models.rs::new` |
| `/workspace-freepeak` | `CLI handler` | OK | `leankg/src/mcp/handler.rs::execute_tool` (rerank ≈ −4.36) |
| `/workspace-other` | `handler` | OK | `platform-core/svc-*/routes/.../handler.js::booking` |

All three returned cleanly in **&lt; 10 s** after MCP resource / auto-index fixes below.

## Why the large monorepo needed more resources

The **~147k-embedding** mega-graph was OOM-killing the previous **2g** MCP `mem_limit` mid-`semantic_search` (`socket closed unexpectedly`).

**Local override pattern** (gitignored `docker-compose.override.yml` + `.dockerfile` — placeholders only):

```yaml
services:
  leankg:
    environment:
      LEANKG_AUTO_INDEX: "0"   # override rocksdb.yml default =1
    mem_limit: 6g
    mem_reservation: 3g
    cpus: "6"
    volumes:
      - /Users/you/work/other-repo:/workspace-other
      - /Users/you/work/freepeak:/workspace-freepeak
```

Also set `LEANKG_AUTO_INDEX=0` in local `.dockerfile`. After recreate, MCP idle RSS ≈ **165 MiB** and all three semantic probes succeed.

Product knobs (tracked):
- `LEANKG_SKIP_FRESHNESS_CHECK=1` — skip MCP freshness auto-index (FR-MG-AUTO-01)
- `LEANKG_AUTO_INDEX=0` — skip entrypoint cold index scan
- `mcp.auto_index_on_start: false` in project `leankg.yaml`

## Day-2 embed resume

Re-running `embed --wait` on an unchanged graph is incremental (near-zero new items). Aligns with PRD §3.15 / §5.16.

## Open issue — mega-graph freshness vs RocksDB mtime

Freshness compares git HEAD vs `.leankg/leankg.db` mtime; RocksDB writes under `LEANKG_ROCKSDB_ROOT` do not bump `leankg.db`, so large mounts can look perpetually stale and trigger incremental reindex on every start. Mitigate with the knobs above; follow-up is RocksDB-manifest mtime (FR-MG-AUTO-01).

## Files referenced

- `docker-compose.embed.yml` — offline embed: `cpus: "6"`, `mem_reservation: 3g`, `mem_limit: 10g`
- `docker-compose.rocksdb.yml` — MCP defaults (multi-project envelope documented / overridable)
- gitignored local: `.dockerfile`, `docker-compose.override.yml`
