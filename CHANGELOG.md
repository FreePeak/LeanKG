# Changelog

All notable changes to this project are documented in this file.

## [0.19.1] - 2026-07-17

### Fixed
- API auth: `auth_middleware` and `team_token_middleware` no longer
  panic when `ApiKeyStore` initialization fails (disk or permission
  error). They now return `500 Internal Server Error`, matching the
  existing `validate_key` error arm. Closes #70 (#78).
- Vector engine: avoid `i8` overflow in synthetic SQ8 patterning
  (centered value computed in `i32` before casting) so CI debug builds
  no longer panic on `% 254 as i8 - 127`.
- Vector engine: idle GC trims the heap only once per quiet period
  (honors `LEANKG_GC_POLL_SECS`) instead of re-trimming empty caches
  every 30s.
- Vector engine: idle RSS gate asserts the warm **delta** under
  `cargo test --lib` (debug builds blow past absolute 150MB), keeping
  the absolute check for lean bench processes.

### Added
- Vector engine P0 quality gate closed with A/B evidence (#80):
  - `Sq8Nsw` layer-0 search over in-RAM SQ8 — measured 1M ANN
    P95≈0.065ms (Neon), gated `cargo bench --default` at 1M
    (FR-VE-BENCH-Q).
  - ≥80% modeled I/O cut vs `mmap`, SQ8 recall≥90% @ `efSearch=50`,
    1M corpus under 2GB (live RSS≈567MB) — FR-VE-BENCH-IO/RECALL/OOM.
  - Idle warm SQ8 NSW RSS≈89MB (<150MB) and ANN+JSON time-to-context
    P95≈0.094ms (<100ms) — US-VE-01/02.
  - `cargo bench --bench vector_engine_ab` now writes
    `target/vector_engine_ab_result.json` for gate/live injection
    (FR-VE-BENCH-AB).
  - `evaluate_gate` flips `ready_for_default=true` and
    `preferred_ann_backend=local_engine` when
    `LEANKG_VE_GATE_FULL=1` and all Q/IO/RECALL/OOM/AB floors pass.
- `tests/vector_engine_e2e.rs` — P0 gate paths covered end-to-end.
- README polished to product landing style (CodeGraph-style
  get-started, agent badges, why/how, measured A/B results).
- Semantic MCP verification captured as PRD v3.7.1 backlog (US-SEM /
  FR-SEM enhancements for a later sprint).

### Changed
- Rebuilt and republished Docker image `freepeak/leankg:0.19.1` (also
  tagged `latest`).

## [0.19.0] - 2026-07-17

### Added
- Local-first vector graph engine (v3.7 P0): new `src/vector_engine/`
  module with tiered storage (`tier1` hot cache, `tier2` warm HNSW,
  `tier3` cold RocksDB), SIMD-accelerated distance kernels, dual-write
  reconciliation, background GC, and `gate`-based fallback routing
  (FR-VE-RT-MEM / FR-VE-BENCH-OOM, PRD §5.14).
- `vector_engine_ab` benchmark harness for A/B testing the new engine
  against the legacy in-memory path under realistic query mixes.
- `engine.recovery` path that rehydrates tier1/tier2 from RocksDB on
  restart without blocking MCP startup.

### Changed
- Rebuilt and republished Docker image `freepeak/leankg:0.19.0` (also
  tagged `latest`).

## [0.18.2] - 2026-07-16

### Fixed
- Docker MCP no longer enables background embed by default (it dropped
  HNSW and broke `semantic_search` on mega-graphs).
- INT8 fast path warms the Xenova cache before ensuring quantized ONNX;
  MCP-safe worker/batch caps when callers request ≤2 workers / ≤32 batch.
- Offline embed profile: INT8, workers 8 / batch 128, soft RSS pause off,
  shared `leankg_models` volume, and multi-project mounts for
  `leankg-embed`.

### Added
- `scripts/embed-all-workspaces-then-mcp.sh` — offline embed all
  `LEANKG_PROJECT_DIRS`, then start MCP and verify `hnsw+rerank`.
- `scripts/docker-up.sh` and `install.sh … docker` — one-command Docker
  setup (index + embed + MCP) with no Rust install.
- Entrypoint passthrough for one-shot `embed` / `index` after auto-index.

### Changed
- Rebuilt and republished Docker image `freepeak/leankg:0.18.2` (also
  tagged `latest`).

## [0.18.1] - 2026-07-16

### Fixed
- Embedding fast path: correct HNSW route, MCP-decoupled lookup, and INT8
  quantisation option (`#76`).
- LeanKG graph workflow end-to-end (`#75`).

### Changed
- Rebuilt and republished Docker image `freepeak/leankg:0.18.1` (also
  tagged `latest`).

## [0.17.2] - 2026-06-06

### Fixed
- Indexer no longer reads files larger than 2 MiB (configurable via
  `LEANKG_MAX_FILE_SIZE`); stops the indexer from slurping checked-in
  binaries and huge generated XML/JSON into memory.
- Watcher debounce raised from 500 ms to 2 s and the event channel
  expanded to 4096; large bursts (e.g. `git pull`) now process in chunks
  with a 250 ms pause between batches instead of fork-bombing the DB.
- Watcher now skips minified JS/CSS, editor swap files, `.bak`, `.tmp`,
  `.pid`, `.lock` and a much longer list of build / generated dirs.
- Watcher now actually runs `VACUUM` on the SQLite `leankg.db` when the
  file exceeds the size cap, instead of only logging a warning. This
  bounds a previously unbounded growth problem (a single workspace had
  grown to 14 GB).
- Default `LEANKG_MMAP_SIZE` lowered from 256 MiB to 64 MiB. The
  previous default pushed containers past their memory limit and was
  the proximate cause of OOM kills (container exit 137).
- Default `mcp.auto_index_on_db_write` flipped to `false`; the previous
  default created reindex storms on every external DB write.

### Added
- `GraphEngine::vacuum()` to reclaim SQLite file space after large
  deletes.
- Docker compose now sets `mem_limit: 6g`, `mem_reservation: 4g`,
  `cpus: "4"`, `pids_limit: 4096`, and `restart: unless-stopped` so the
  container can no longer consume the entire host memory.
- New env tunables for the watcher: `LEANKG_WATCHER_DEBOUNCE_MS`,
  `LEANKG_WATCHER_BURST_LIMIT`, `LEANKG_WATCHER_BURST_PAUSE_MS`,
  `LEANKG_WATCHER_MAX_DB_SIZE`.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.15.1] - 2026-04-14

### Fixed
- Normalize glob patterns in exclude matching
- Use .gitignore files only for file traversal
- Apply config.project.root when indexing with '.'

### Changed
- Read config from .leankg/leankg.yaml in index_codebase()
- Default project.root changed from './src' to '.'

### Removed
- Dead should_ignore_path function

## [0.14.9] - 2026-04-14

### Fixed
- Correct byte string literal syntax in `test_detect_gradle_submodules` test (b#"..." → br#"...")

## [0.14.8] - 2026-04-14

### Fixed
- Inline call resolution during indexing (resolves `__unresolved__` calls in-memory, eliminates separate DB pass)
- Batch delete for resolved call edges (O(1) queries vs O(n) sequential deletes)
- ~6x speedup: 10s → 1.7s for indexing with 7926 call edges

## [0.14.7] - 2026-04-12

### Added
- Obsidian vault integration for annotation IDE
- Obsidian module with note generator and sync logic
- Watcher for live file monitoring
- CLI with obsidian subcommand
- New documentation: architecture.md, benchmark.md, metrics.md
- Dockerfile improvements for LeanKG indexing during build

### Changed
- Updated README with new UI architecture documentation
- Vite dev server integration for production deployments

### Fixed
- Dockerfile to build new Vite+React UI
- UI directory build copy issue
- WORKDIR setting in Dockerfile
- Preserved all elements for complete call graph
