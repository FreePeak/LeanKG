# Changelog

All notable changes to this project are documented in this file.

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
