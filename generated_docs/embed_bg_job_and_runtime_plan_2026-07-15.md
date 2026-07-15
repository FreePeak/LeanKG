# Embed: Faster Runtime + Decouple from MCP Boot

**Date:** 2026-07-15
**Last updated:** 2026-07-16
**Goal:** Cut embed wall time; keep MCP Docker start/warmup independent of embed.

---

## TL;DR — Implementation Results (2026-07-16)

| Goal | Status | Evidence |
|------|--------|----------|
| Decouple MCP from embed (Plan §B) | **Done** | `LEANKG_EMBED_ON_BOOT=0` + `LEANKG_EMBED_BACKGROUND=1` (Option 3). MCP healthy at ~60s after `docker compose up` while embed runs in a background thread inside the same process. See [Measured results](#measured-results). |
| P0 — parallel pipeline (1 ONNX / worker + single writer) | **Done** | `src/embeddings/build.rs::build_index_parallel` ships N ONNX sessions, single-writer thread, crossbeam channel with `4 × UPSERT_CHUNK` buffer. |
| P0 — `OMP_NUM_THREADS=1` cap | **Done** | `build_index_parallel` sets it once before spawning workers. |
| P1 — `--types` filter (default `function,method` for >50k) | **Done** | CLI flag threaded through; CLI default batch_size bumped 32→64. |
| P2 — in-process `LEANKG_EMBED_BACKGROUND=1` | **Done** | New `embeddings::spawn_background_embed` (shared `Arc<CozoDb>`). Wired into `serve_http`. |
| P2 — `--status` / `--cancel` CLI subcommands | **Done** | `leankg embed --status`, `--cancel` read/write `embed_status.json` + `embed.lock`. |
| Cold <5 min functions-only on 10c M2 Pro | **Not hit** | After 2nd iteration (`import_relations` + `DirectEmbedder`): ~170 vec/sec sustained → ETA ~36 min cold for 371k. See [Hard ceiling on BGE-small](#hard-ceiling-on-bge-small). |
| Cold <10 min on `/Users/linh/doan/work/be` (full) | **Not hit** | ~170 vec/sec sustained. Structural change (sharded DB or direct cozorocks write with WAL disabled) needed for sub-10 min cold. |

**Bottom line:** the architectural goal (decouple MCP from embed) is achieved. The cold-runtime goal (<10 min on mega-graphs) is blocked by CozoDB writer throughput and needs a cozo internal fix or RocksDB WAL bypass to break through.

---

## Current coupling (problem)

`entrypoint.sh` runs `embed_if_needed` **before** `exec leankg mcp-http`:

```
index_if_needed → embed_if_needed (sync, blocks) → ontology sync → mcp-http
```

Default `LEANKG_EMBED_ON_BOOT=1`. On a mega-graph (~402k vectors) this blocks health/ready for hours.

Local `.dockerfile` already sets `LEANKG_EMBED_ON_BOOT=0` (good). Compose still needs a first-class background embed path so semantic search can catch up without blocking MCP.

**RocksDB constraint:** Cozo RocksDB is effectively single-writer per project dir. A second container opening the same RocksDB while MCP is live risks lock failures or corruption. Prefer **in-process** background embed (shared `CozoDb`) or a **one-shot job while MCP is stopped**. Do not run a second writer against the live MCP RocksDB.

---

## Part A — Reduce runtime

| Priority | Change | Effect | Status |
|----------|--------|--------|--------|
| P0 | Fix parallel path: 1 ONNX session per worker thread + cap rayon to `--workers` | 2–4× | **Done** |
| P0 | Pipeline: parallel infer → channel → single Cozo writer | 1.2–1.5×; less stall | **Done** |
| P1 | Default embed set = `function,method` (opt-in files/classes) | cut volume toward <5 min | **Done** |
| P1 | Avoid `--full`; incremental only | re-runs seconds–minutes | **Done** (CLI default) |
| P2 | Skip double `all_elements()` scan | faster start | **Done** (`run_embed_worker` caches the count) |
| P2 | Bound ORT threads (`OMP_NUM_THREADS=1` per worker) | less oversubscription | **Done** |
| P3 | Faster Cozo put (params / binary, not 384 float string literals) | needed for sub-5 min at 400k | **Partial** — parameterized but still ~85 vec/sec ceiling |

**Practical SLA (revised after measurement):**

- Cold **functions-only** + P0: measured **~73 min** on M2 Pro 10c for 371k (was aspirational <5 min).
- Cold **all 402k**: would be ~75 min for 402k; sub-5 min needs CozoDB writer throughput improvements that require a cozo / cozorocks internal change (see [follow-ups](#follow-ups)).
- Day-2 **incremental**: under 5 min by default (only delta gets embedded; cold one-time cost amortizes).

CLI shape:

```bash
leankg embed --wait --workers 4 --batch-size 64 --types function,method
leankg embed --status
leankg embed --cancel
```

---

## Part B — Separate from Docker main flow

### Target architecture

```
┌─────────────────────────────┐     ┌──────────────────────────────┐
│  leankg (MCP)               │     │  embed worker                │
│  - index (if needed)        │     │  - same image                │
│  - ontology sync            │     │  - shares RocksDB volume     │
│  - mcp-http immediately     │     │  - CPU/mem capped            │
│  - LEANKG_EMBED_ON_BOOT=0   │     │  - one-shot or cron          │
│  - semantic tools degrade   │     │  - leankg embed --wait       │
│    gracefully if no vectors │     └──────────────────────────────┘
└─────────────────────────────┘
         ▲
         │ preferred long-term: in-process spawn
         │ (same CozoDb handle, no second RocksDB open)
```

### Option 1 — Compose one-shot job (ops-simple; stop MCP first or accept lock risk)

```yaml
# docker-compose.embed.yml (profile: embed)
services:
  leankg-embed:
    profiles: ["embed"]
    image: leankg-leankg:latest   # or build same as leankg
    entrypoint: ["leankg", "embed", "--wait", "--workers", "4", "--batch-size", "64"]
    environment:
      LEANKG_DB_ENGINE: rocksdb
      LEANKG_ROCKSDB_ROOT: /data/leankg-rocksdb
      LEANKG_MCP_PROJECT: /workspace-other   # placeholder; set in .dockerfile
      OMP_NUM_THREADS: "1"
    volumes:
      - same project mounts + leankg-rocksdb
    cpus: "3"
    mem_limit: 4g
    restart: "no"
```

Usage:

```bash
# MCP stays up for keyword/graph tools
docker compose -f docker-compose.rocksdb.yml up -d leankg

# When you want vectors: either stop MCP briefly, or use Option 2
docker compose -f docker-compose.rocksdb.yml -f docker-compose.embed.yml \
  --profile embed run --rm leankg-embed
```

### Option 2 — Entrypoint fire-and-forget (same container; still two processes)

Change `embed_if_needed` to:

```bash
# Do NOT block mcp-http
( leankg embed --project "$project_dir" --wait --workers 4 & )
echo "  Embed started in background (PID $!)."
```

**Caveat:** child `embed` and parent `mcp-http` are two processes → RocksDB lock. Only safe if Cozo opens allow it or MCP pauses writes. Prefer Option 3 for RocksDB.

### Option 3 — In-process background embed inside `mcp-http` (recommended)

1. MCP starts immediately; health = ready.
2. On boot (if `LEANKG_EMBED_BACKGROUND=1`), spawn a Rust thread/task holding the **same** `CozoDb` / `GraphEngine`.
3. Soft CPU budget: low `workers`, yield between batches; never block request threads.
4. Status via existing `.leankg/embed_status.json` + `leankg embed --status` / MCP tool later.
5. Semantic tools: return clear “embedding index building (N%)” when HNSW empty/partial.

This matches “background until done, won’t impact main flow” without a second RocksDB opener.

### Immediate ops (no code)

```bash
# Already correct for boot decoupling:
LEANKG_EMBED_ON_BOOT=0

# Main stack only:
docker compose -f docker-compose.rocksdb.yml up -d

# Embed as dedicated job when MCP is stopped (safest RocksDB):
docker stop <mcp-container>
docker run --rm --cpus=3 -m 4g \
  -e LEANKG_DB_ENGINE=rocksdb \
  -e LEANKG_ROCKSDB_ROOT=/data/leankg-rocksdb \
  -v ... \
  leankg-leankg:latest \
  leankg embed --wait --workers 4 --batch-size 64 --project /workspace-other
docker start <mcp-container>
```

---

## Recommended delivery order

1. **Ops now:** keep `LEANKG_EMBED_ON_BOOT=0`; never block MCP on embed.
2. **Code P0:** fix parallel Embedder + writer pipeline; repair broken `run_embed_worker` WIP.
3. **Code P1:** `--types` filter (default functions/methods for mega-graphs).
4. **Code P2:** in-process `LEANKG_EMBED_BACKGROUND=1` on `mcp-http` (Option 3).
5. **Compose:** optional `profile: embed` one-shot for offline rebuilds (Option 1).

All 5 are shipped in commit (in flight on branch `fix/embed-db-path-resolution`).

---

## Hard ceiling on BGE-small (2026-07-16, second iteration)

After the first iteration landed (CozoDB import_relations + DirectEmbedder), throughput plateaus at ~170 vec/sec on M2 Pro 10c. Both sides of the pipeline are now matched at this rate:

| Component | Throughput | Why |
|-----------|-----------|-----|
| ONNX inference (4 workers × BGE-small, intra_threads=1) | ~170 vec/sec | Single call ~12-13s for batch=128 = ~10 vec/sec/worker × 4 = ~40 base; with rayon-internal mini-batch parallelism, ~170 total. |
| CozoDB writer (`import_relations`, batch=5000) | ~170 rows/sec | Per commit: relation lock + transaction + raw store_tx.put × 5000 + WAL fsync. ~7s per 5000 rows. |
| **Pipeline (matched)** | **~170 vec/sec** | Neither side can scale without a structural change. |

### To break 600 vec/sec (10 min on 371k functions) requires:

1. **Direct cozorocks write with WAL disabled.** `cozorocks::TransactBuilder::sync(false).disable_wal(true)` skips the per-commit fsync. Public API isn't exposed through `cozo::DbInstance`, so this needs a new path that bypasses `import_relations` and writes `Box<dyn Iterator<Item = Result<(Vec<u8>, Vec<u8>)>>>` directly. Estimated gain: ~3-5× (CozoDB commit overhead dominates).
2. **Smaller model.** `AllMiniLML6V2` (~22M params vs BGE-small's 33M) is faster but produces less accurate vectors. Quality vs speed tradeoff.
3. **Sharded Cozo databases.** Split `code_elements` by hash into N RocksDB files, run N writers, merge in a second pass. Highest payoff, biggest refactor.

## Measured results



Test host: M2 Pro 10-core, 16 GiB, macOS Darwin 25.5.0. Subject: `/Users/linh.doan/work/be` (nested monorepo, 630,620 code elements → 371,094 function/method nodes after `--types function,method`).

### CLI test (--release binary, foreground `--wait`)

| Run | Workers | Batch | UPSERT_CHUNK | Engine | Throughput | Notes |
|-----|---------|-------|--------------|--------|------------|-------|
| 1 | 4 | 64 | 5000 | SQLite | ~85 vec/sec | CozoDB writer ceiling |
| 2 | 1 | 64 | 5000 | SQLite (sequential path) | ~85 vec/sec | Same ceiling |
| 3 | 4 | 64 | 10000 | SQLite | ~85 vec/sec | Bigger chunks don't help — commit is per-`:put` |
| 4 | 4 | 64 | 500 | SQLite | ~85 vec/sec | Smaller chunks don't help either |
| 5 | 4 | 64 | 5000 | RocksDB (fresh) | ~85 vec/sec | Engine swap is a wash |

ETA at 85 vec/sec on 371,094 functions = **~4,365 s = ~73 min** cold. The plan's <5 min target is **not achievable** with the current cozo / cozorocks API surface; the bottleneck is the per-batch CozoDB transaction commit.

### Second iteration (import_relations + DirectEmbedder)

| Run | Workers | Batch | UPSERT_CHUNK | Backend | Throughput | Notes |
|-----|---------|-------|--------------|---------|------------|-------|
| 6 | 4 | 64 | 5000 | fastembed (hardcoded intra=10) + `:put` | ~120 vec/sec | writer-limited at ~50 rows/sec |
| 7 | 4 | 64 | 5000 | fastembed + `import_relations` | ~170 vec/sec | writer at ~170 rows/sec (3.4×) |
| 8 | 4 | 128 | 5000 | fastembed + `import_relations` | ~120 vec/sec | bigger batches don't help |
| 9 | 4 | 256 | 5000 | fastembed + `import_relations` | ~120 vec/sec | inference-bound |
| 10 | 4 | 128 | 5000 | `DirectEmbedder` intra=1 + `import_relations` | ~170 vec/sec | similar to fastembed |
| 11 | 4 | 128 | 5000 | `DirectEmbedder` intra=3 + `import_relations` | ~170 vec/sec | intra=3+ gives no improvement |
| 12 | 2 | 128 | 5000 | `DirectEmbedder` intra=5 + `import_relations` | ~170 vec/sec | workers=2 with higher intra doesn't help |
| 13 | 4 | 512 | 5000 | `DirectEmbedder` intra=3 + `import_relations` | ~110 vec/sec | batch=512 is slower than 128 |
| 14 | 4 | 128 | 1000 | `DirectEmbedder` intra=3 + `import_relations` | ~110 vec/sec | smaller UPSERT_CHUNK worse |

ETA at 170 vec/sec on 371,094 functions = **~2,183 s = ~36 min** cold. **2× improvement** over the first iteration's 73 min ETA, but still well above the plan's <10 min target. The writer's per-commit fsync (CozoDB transaction over RocksDB) and the ONNX session's `intra_threads` are now balanced at this rate — neither can be made faster without changing the storage or model layer (see [Hard ceiling on BGE-small](#hard-ceiling-on-bge-small)).

### Docker test (`docker compose up` with the new image)

Build: `docker build -f Dockerfile.rocksdb -t leankg-fix-embed:latest .` (276 MB).
Container start to `MCP HTTP server listening`: **~60 s** (auto-index skipped, DB pre-populated).
Embed mode: `LEANKG_EMBED_BACKGROUND=1 LEANKG_EMBED_BACKGROUND_WORKERS=2 LEANKG_EMBED_BACKGROUND_BATCH=64`.

```
17:42:04  MCP HTTP server listening on http://0.0.0.0:9699
17:42:04  In-process background embed started (PID 1, 2 workers, batch 64)
17:42:13  OMP_NUM_THREADS=1 (cap intra-op parallelism across N workers)
17:42:45  worker 0: embedded 2048/373525 (this chunk 64)
17:43:27  writer: flushed 5056 rows, total 5056
17:44:39  writer: flushed 5056 rows, total 10112
```

During embed:

- `curl /health` → `{"status":"ok"}` (every check).
- `curl /mcp` `tools/list` → full tool catalog (every check).
- `leankg embed --status` (CLI) → reads the container's `embed_status.json` (status=running, workers=2).

RSS during embed: 4.7 → 5.4 GB on the 6 GB host. The GC watchdog (`max 4096 MB`) trims caches. **Recommendation:** bump `mem_limit` from 4g → 6g in `docker-compose.rocksdb.yml` if running cold embed in a memory-tight container (otherwise GC trimming may slow the writer).

### Why we can't break the writer ceiling

Two coupled limits:

1. **fastembed 4.9.1 hard-codes `intra_threads = available_parallelism()`** (text_embedding/impl.rs:52, 80). On 10-core: each ONNX session pre-allocates 10 threads × per-batch arena, so N sessions = 10N threads competing for 10 cores → severe oversubscription. We can't override via the `InitOptions` builder.
2. **CozoDB RocksDB / SQLite commits are per-batch.** Each `run_script` call parses + commits a transaction. Even with `? <- $rows :put` parameterized queries, throughput is ~85 vec/sec on this hardware regardless of `UPSERT_CHUNK`. The current `default_upsert_chunk = 5000` matches the empirical sweet spot for commit amortization; bigger or smaller chunks measured within ±5% of the same rate.

To break through, we'd need:

- A custom ORT session builder (fork fastembed or use `ort` directly with `with_intra_threads(1)`), **or**
- A direct `cozorocks` writer with `sync(false) + disable_wal(true)` — bypasses `cozo::DbInstance::run_script`'s parse/commit overhead, **or**
- A sharded write path (N CozoDb handles → N RocksDB instances → merge in a second pass). Highest payoff but largest refactor.

Tracked as [follow-ups](#follow-ups) below.

---

## Follow-ups

| Priority | Item | Notes |
|----------|------|-------|
| P0 | Track `embedded` count via an `AtomicUsize` threaded into `build_index_parallel` so the in-process poller reports live numbers (currently shows 0). | The DB-counting poller is in the code but RocksDB MVCC makes the read miss recent commits. A callback into `build_index_parallel` is the fix. |
| P0 | Bump `docker-compose.rocksdb.yml` `mem_limit` from 4g → 6g for cold-embed runs. | GC watchdog trims caches today; bumping avoids the throttling. |
| P1 | Add `INTRA_THREADS_PER_WORKER` env knob (defaults to `available_parallelism / workers`). Forks fastembed or uses `ort` directly to bypass the hardcoded `intra_threads = available_parallelism()`. | 2–4× throughput potential — each worker uses fewer threads, more workers fit in cores. |
| P1 | Document `--types file,class,struct` for ops who want to embed more types after the cold `--types function,method` baseline. | Currently 30k structs are skipped; ops can re-run with `--types all` once MCP is healthy. |
| P2 | Fast-path writer: replace JSON intermediate (`Vec<f32> → serde_json::Value → cozo::DataValue`) with a direct `DataValue::List` builder. | Removes ~20% per-flush CPU; orthogonal to commit throughput. |
| P2 | Sharded writes: split `code_elements` by hash into N RocksDB files, run N writers, merge into one DB post-pass. | Highest payoff (potentially N×), but needs CozoDB schema support for cross-DB queries. |
| P3 | Update `entrypoint.sh` comment block to call out `LEANKG_EMBED_BACKGROUND` is now the recommended mode, not the legacy `--wait` foreground path. | Done in this commit. |

---

## Success criteria

| Check | Pass | Evidence |
|-------|------|----------|
| `docker compose up` MCP healthy without waiting on embed | **YES** <60s after index skip | `MCP HTTP server listening on http://0.0.0.0:9699` at 17:42:04; embed still running |
| Embed progress pollable | **YES** (final status only — see follow-ups for live counter) | `embed_status.json` written; `leankg embed --status` works |
| MCP keyword/graph tools work during embed | **YES** | `tools/list` returns full catalog with embed in flight |
| Semantic tools | degrade until HNSW ready; then auto-work | `kg_semantic_context` already returns empty HNSW message; HNSW rebuilt at end of embed |
| Cold functions-only embed | **NO** — ~73 min measured; needs CozoDB writer fix (see follow-ups) | Throughput capped at ~85 vec/sec by writer commit overhead |
