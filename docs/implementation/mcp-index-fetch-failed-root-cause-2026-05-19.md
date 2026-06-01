# MCP Index Fetch Failed Root Cause Report

Date: 2026-05-19
Status: Fix implemented and validated in `fix/mcp-index-fetch-failed`
Owner: LeanKG local MCP integration

## Summary

Cursor reports `{"error":"fetch failed"}` when calling LeanKG MCP index over HTTP. The error is not a normal JSON-RPC tool error from LeanKG. It is a client-side fetch failure caused by the HTTP request timing out, the connection being closed, or the server process becoming unhealthy while the request is in flight.

The immediate failure pattern is tied to long-running synchronous index work inside the MCP HTTP request path, stale/conflicting MCP server processes on port `9699`, SQLite/CozoDB write contention during index and call-edge resolution, and a legacy 11-column Cozo schema in `/Users/linh.doan/work/be/.leankg` that made core search tools fail with `Arity mismatch for rule application code_elements`.

## Evidence

- Cursor project MCP config points to `http://localhost:9699/mcp?project=/Users/linh.doan/work/harvey/freepeak/leankg`.
- Port `9699` was actually served by a debug binary started for another project:
  - `/Users/linh.doan/work/harvey/freepeak/leankg/target/debug/leankg mcp-http --port 9699 --project /Users/linh.doan/work/be`
- Stale session metadata under `.leankg/.leankg_sessions/` pointed to a different release server PID.
- Manual HTTP JSON-RPC calls to `mcp_status` succeeded but took several seconds.
- Manual HTTP JSON-RPC call to `mcp_index` for one file took `8.385s` and reported `142325` call edges resolved.
- Logs contained:
  - `Cannot start a runtime from within a runtime` at `src/mcp/server.rs:512`
  - `database is locked` from Cozo SQLite storage
  - `PoisonError`
  - `Address already in use (os error 48)`
- Cursor validation report from `/Users/linh.doan/work/be/platform-core/be-diagram/food/performance/leankg-mcp-validation-report.md` showed:
  - `mcp_index`: `fetch failed`
  - `search_code`: `MCP error -32603: Arity mismatch for rule application code_elements`
  - `find_function`: `MCP error -32603: Arity mismatch for rule application code_elements`
- Reproduction against `/Users/linh.doan/work/be` with the fixed binary before schema repair confirmed:
  - `leankg query main --kind name` failed with `Required arity: 11, number of arguments given: 12`.
  - The DB had legacy `code_elements` and `relationships` arities despite the migration ledger recording the canonical repair.

## Root Causes

1. `mcp_index` performs blocking index work in the MCP request path.
   - File: `src/mcp/handler.rs`
   - Function: `mcp_index`
   - It scans files, indexes them synchronously, then always runs full call-edge resolution.

2. `resolve_call_edges()` is global and expensive.
   - File: `src/graph/query.rs`
   - Function: `resolve_call_edges`
   - It loads all unresolved call relationships and all functions, then deletes and reinserts relationship batches.
   - Even a one-file index can trigger a large full-database resolution pass.

3. Full-repo indexing can include nested worktrees and build/cache directories.
   - File: `src/indexer/mod.rs`
   - Function: `find_files_sync`
   - This duplicates graph data and makes explicit MCP indexing much slower than the active checkout requires.

4. HTTP `tools/call` performs auto-index checks before every tool execution when URL `project` is present.
   - File: `src/mcp/server.rs`
   - Function: `process_jsonrpc_request`
   - This can make unrelated tools inherit index latency and write contention.

5. Server session and port-lock handling has unsafe async/runtime behavior.
   - File: `src/mcp/server.rs`
   - Function: `try_acquire_port_lock`
   - It creates a Tokio runtime and calls `block_on` from code that can run inside an existing runtime, causing panics.

6. Multiple MCP server instances can point at different projects while sharing port/session state.
   - This caused Cursor to hit a server launched for `/Users/linh.doan/work/be` while URL-level routing attempted to use `/Users/linh.doan/work/harvey/freepeak/leankg`.

7. `--project` and URL `?project=` were not consistently applied.
   - File: `src/mcp/server.rs`
   - Functions: `find_project_root`, `handle_mcp_request`, `find_leankg_for_path`
   - Server startup used process cwd for project discovery, and relative tool paths were not always resolved against the URL project.

8. Database write concurrency is not guarded for HTTP index and watcher activity.
   - Concurrent index/write operations can trigger SQLite lock errors and poison Cozo internals.

9. Canonical schema migration could be marked applied while the database stayed legacy.
   - File: `src/db/schema.rs`
   - Migration `006_safe_canonical_schema_repair` only ran when absent from the migration table.
   - The old repair used bare `:replace`, which Cozo rejects with `Program has no entry` because `:replace` cannot omit a query.
   - Result: current graph queries used 12-column `code_elements[..., env]`, while `/Users/linh.doan/work/be/.leankg` still had 11 columns.

10. `mcp_status` performed full counts by default.
   - File: `src/mcp/handler.rs`
   - On large databases, readiness checks could spend too long counting files/functions/classes and look like a stalled MCP call.

## Implementation Plan

Work will be done in a new git worktree:

```text
./worktrees/fix-mcp-index-fetch-failed
```

Planned branch:

```text
fix/mcp-index-fetch-failed
```

### Phase 1: Stop Per-Request Index Surprises

- [x] Remove `ensure_project_indexed()` from generic HTTP `tools/call`.
- [x] Only run auto-index on server startup or explicit index requests.
- [x] Preserve project routing for query tools without triggering writes before every call.
- [x] Always resolve relative HTTP tool path arguments against URL `?project=`.
- [x] Use configured `db_path` parent as MCP server project root when `--project` is provided.

### Phase 2: Make Index Safer for HTTP MCP

- [x] Honor `incremental` in `mcp_index`.
- [x] Add `resolve_calls` with default `false` so MCP index calls do not always perform global call-edge resolution.
- [x] Report whether call resolution ran or was skipped in the tool result.
- [x] Exclude nested worktrees and common generated/cache directories during default file discovery.
- [x] Improve per-file error reporting for skipped files instead of only counting skipped files.

### Phase 3: Fix Session/Lock Runtime Panic

- [x] Replace `block_on` inside `try_acquire_port_lock` with a blocking health check that does not create a nested Tokio runtime.
- [ ] Make stale lock cleanup more deterministic across multiple project-specific session directories.

### Phase 4: Serialize DB Writes

- [x] Add an MCP-server-level mutex for write-heavy MCP tools and dirty-write-triggered reindex.
- [ ] Extend the same serialization boundary to watcher reindex paths if watcher and HTTP run in the same process.

**Note**: Remaining items (Phase 3 stale lock cleanup, Phase 4 watcher serialization) require architectural changes to pass watcher a shared write lock. These are low priority since all critical functionality works and tests pass.

### Phase 5: Verification

- [x] `cargo check`
- [x] `cargo build`
- [x] `cargo test test_mcp_index -- --nocapture`
- [x] `cargo test mcp_server -- --nocapture`
- [x] `cargo test test_find_files -- --nocapture`
- [x] `cargo test test_init_db_repairs_legacy_code_elements_after_recorded_migration -- --nocapture`
- [x] `cargo test test_mcp_status -- --nocapture`
- [x] `git diff --check`
- [x] Manual HTTP smoke test against a clean single-server setup on port `9802`.
  - `GET /health` returned ok.
  - HTTP JSON-RPC `mcp_index {"path":"src"}` returned a normal tool result with `resolve_calls: false`.
  - HTTP JSON-RPC `mcp_status` returned the temp project database path and populated counts.
  - HTTP JSON-RPC `search_code {"query":"main"}` returned the indexed function.
  - Concurrent HTTP JSON-RPC `mcp_index` calls returned normal tool results with no `database is locked` or `fetch failed`.
- [x] Manual HTTP validation against `/Users/linh.doan/work/be` on port `9807`.
  - First fixed CLI startup repaired `code_elements` from 11 to 12 columns and `relationships` from 5 to 6 columns, preserving rows and setting `env = "local"`.
  - HTTP JSON-RPC `mcp_status {}` returned `initialized: true`, `index_populated: true`, and `database_exists: true` without full counts.
  - HTTP JSON-RPC `search_code {"query":"main","limit":3}` returned normal results.
  - HTTP JSON-RPC `find_function {"name":"main"}` returned normal results.
  - HTTP JSON-RPC `mcp_index {"path":"platform-core/be-activity-history/cmd/client","resolve_calls":false}` returned `success: true` and indexed one file.
- [x] Replaced the stale Cursor-facing process on port `9699`.
  - Old PID `99390` was `/Users/linh.doan/work/harvey/freepeak/leankg/target/debug/leankg`.
  - New process is running from `./worktrees/fix-mcp-index-fetch-failed/target/debug/leankg` in tmux session `leankg-mcp-9699-fixed`.
  - Revalidated `mcp_status`, `search_code`, and `mcp_index` over `http://127.0.0.1:9699/mcp?project=/Users/linh.doan/work/be`.

## Implemented File Map

- `src/mcp/server.rs`
  - Removed per-request project auto-index from HTTP `tools/call`.
  - Added `write_lock` to serialize write-heavy MCP tools.
  - Replaced nested-runtime health check in `try_acquire_port_lock`.
  - Fixed `--project` project-root detection and URL `?project=` relative path routing.
- `src/mcp/handler.rs`
  - Made `mcp_index.incremental` active.
  - Added `resolve_calls` behavior with default false.
  - Made `mcp_status` a lightweight readiness check by default; full counts require `include_counts: true`.
- `src/mcp/tools.rs`
  - Added `resolve_calls` to the `mcp_index` tool schema.
  - Added `include_counts` to the `mcp_status` tool schema.
- `src/db/schema.rs`
  - Added arity probes for `code_elements` and `relationships`.
  - Runs canonical repair even if the migration ledger says migration 006 already applied.
  - Replaced invalid bare `:replace` with data-preserving `:replace` queries that add `env = "local"` to legacy rows.
- `src/graph/query.rs`
  - Added a fast `has_elements` readiness query.
  - Fixed malformed `get_top_level_directories` and service schema discovery queries.
- `src/indexer/mod.rs`
  - Excluded nested worktrees, `.leankg`, VCS, build, and dependency cache directories relative to the requested index root.
- `tests/cli_tests.rs`
  - Updated `CLICommand::Index` patterns with `..` so newer fields do not break test compilation.
- `tests/integration.rs`
  - Added regression coverage for a legacy DB that already recorded migration 006 but still has 11-column `code_elements`.

## Immediate Operational Workaround

Until the fixed binary is the one Cursor launches:

1. Kill duplicate `leankg mcp-http` processes.
2. Remove stale `.leankg/.leankg_sessions/*`.
3. Start exactly one server for the active project.
4. Set `mcp.auto_index_on_start: false` in `.leankg/leankg.yaml` if Cursor must stay responsive.
5. Run full indexing from CLI outside Cursor MCP requests.

For the BE workspace, the database schema has already been repaired by the fixed binary during validation on 2026-05-19. The old process on port `9699` was replaced with the fixed worktree binary and revalidated over HTTP.
