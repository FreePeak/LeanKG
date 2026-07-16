# Validation Report: docker-compose Multi-Project Fix

**Date:** 2026-07-09
**Branch validated:** `fix/docker-compose-multi-project` @ `5891719`
**Image:** `leankg-leankg:latest` (existing; no rebuild needed -- fix is compose-only)
**Validator:** automation-qa skill

## Summary

7 of 7 acceptance criteria **PASS** after the fix is applied. One pre-existing data-version bug was uncovered during validation (CozoDB schema mismatch in stale secondary RocksDB); it was resolved by re-indexing from scratch. The container is now serving both `/workspace` and `/workspace-other` correctly via `?project=` URL routing, with full code search across both projects' indexes.

## Pre-existing data issue (resolved during validation)

The `/workspace-other` RocksDB at `/data/leankg-rocksdb/projects/workspace-other-6917453a1780` was originally created with a much older leankg binary using CozoDB v1, while the current binary (v0.17.8) uses CozoDB v0.7.6 which expects storage version 3. The first `mcp_status` call against `/workspace-other` crashed the MCP HTTP request handler with:

```
thread 'tokio-rt-worker' panicked at cozo-0.7.6/src/storage/rocks.rs:55:13:
assertion `left == right` failed: Unknown storage version 1
  left: 1
 right: 3
```

This is **not a regression** from the compose fix; the stale DB would have crashed any caller, but the original buggy compose config made it invisible because `/workspace-other` was never the default project, so users only triggered the crash if they explicitly used `?project=/workspace-other`. With the fix making `/workspace-other` the default, the bug surfaced immediately.

**Resolution:** Backed up the stale RocksDB to `/tmp/be-rocksdb-backup-stale-cozov1` inside the container, then wiped it and let `entrypoint.sh` re-index from scratch. The re-index completed in ~50 seconds and produced a fresh v3 schema.

**Recommendation:** Document this in `entrypoint.sh` -- the `index_if_needed()` function should check the storage schema version before deciding to skip re-indexing, otherwise stale CozoDB versions persist and crash on next open. Filed as a follow-up below.

## Acceptance Criteria Results

| AC | Description | Result | Evidence |
|---|---|---|---|
| AC-1 | Startup log shows `Starting MCP HTTP on port 9699 for project /workspace-other` | PASS | Container log line: `=== Starting MCP HTTP on port 9699 for project /workspace-other ===` |
| AC-2 | Entrypoint scans both `/workspace` and `/workspace-other` | PASS | Container log shows two `--- Project: ... ---` blocks, one per project |
| AC-3 | Both projects' RocksDB data appears under `$LEANKG_ROCKSDB_ROOT/projects/` | PASS | `docker exec ls /data/leankg-rocksdb/projects/` returns `workspace-c52ddf65534b` and `workspace-other-6917453a1780` |
| AC-4 | `/workspace-other` is bind-mounted inside container | PASS | `docker inspect leankg-leankg-1` mounts: `/Users/you/work/other-repo -> /workspace-other` (preserved from previous container; now exercised) |
| AC-5 | `/health` returns 200 OK; `/mcp` initialize returns 200 with valid JSON-RPC response | PASS | `curl /health` -> `{"status":"ok"}` (HTTP 200). `POST /mcp initialize` -> `{"serverInfo":{"name":"leankg","version":"0.17.8"}}` |
| AC-6 | `mcp_status` works against `/workspace-other` (default) | PASS | Returns `database: /workspace-other/./.leankg`, `storage_path: /data/leankg-rocksdb/projects/workspace-other-6917453a1780`, `index_populated: true` |
| AC-7 | `mcp_status` works against `/workspace` via `?project=/workspace` | PASS | Returns `database: /workspace/.leankg`, `storage_path: /data/leankg-rocksdb/projects/workspace-c52ddf65534b` |

## Functional Validation (search_code)

The most discriminating test: same query, two projects, different result sets -- proves the routing hits different RocksDB databases.

```
search_code(query="index", project=/workspace)  -> 2 results from LeanKG source tree
search_code(query="index", project=/workspace-other) -> 2 results from secondary Go monorepo
```

Sample secondary results (real, queryable):
```
./platform-core/be-activity-history/internal/models/activity_search.go:44  Index (property)
./platform-core/be-anywhere/routes/adminPanel/index.js                       index.js (File)
```

Sample `/workspace` results:
```
./benchmark/results/debug-indexing-failure-comparison.json  File
./src/benchmark/unified.rs:58                                indexed_elements (property)
```

The `?project=` query parameter correctly routes to the right RocksDB instance.

## Re-index Stats (secondary Go monorepo)

The full secondary index run after the stale-DB wipe:

| Metric | Value |
|---|---|
| Files parsed | 21,844 |
| Excluded | 24 (matches `node_modules` + `vendor` patterns) |
| Elements indexed | 603,664 (final unique: 577,153) |
| Relationships | 3,159,489 |
| Call edges resolved inline | 2,284,603 |
| Frameworks detected | 10 |
| Microservice calls detected | 0 (microservice-extractor config doesn't match secondary patterns -- pre-existing, out of scope) |
| Documents indexed | 13 / 224 sections / 777 doc relationships |
| Wall-clock time | ~50 seconds |

## Configuration that produced the working state

```bash
# From the worktree directory (worktree holds the fixed compose file)
docker compose -p leankg \
  -f docker-compose.rocksdb.yml \
  -f docker-compose.override.yml \
  --env-file .dockerfile \
  up -d --no-build

# Equivalent start command for the user's main checkout (after merge):
cd /Users/linh.doan/work/harvey/freepeak/leankg
docker compose \
  -f docker-compose.rocksdb.yml \
  -f docker-compose.override.yml \
  --env-file .dockerfile \
  up -d
```

`--no-build` was used because the fix is compose-only; the existing `leankg-leankg:latest` image is functionally correct. A `--build` would re-run `cargo build --release` against a partially-populated `vendor/` directory and fail (TLS handshake timeout fetching `rust:1-bookworm` from Docker Hub, plus missing vendored crates). This is an environment/build-system issue independent of the fix.

## Container Lifecycle Notes

- **Pre-existing container** (`leankg-leankg-1`, 46-min uptime) was stopped and removed via `docker compose -p leankg down` from the main checkout. The named `leankg_leankg-rocksdb` volume was preserved (no `down -v`).
- **New container** started against the same compose project name `leankg` and reuses the same volume, so existing `/workspace` RocksDB data was preserved.
- The secondary stale-DB wipe targeted only `/data/leankg-rocksdb/projects/workspace-other-6917453a1780` -- other projects in the volume (`app-f53b52ad6d21`, `svc-autos-ffa27e44fa4b`, etc.) are untouched.

## Recommendation

**SHIP.** The fix works end-to-end. All three original bugs (env override, comma-vs-space, override-file chain) are resolved, the multi-project routing is verified by both functional and behavioral tests, and the unexpected CozoDB version mismatch was a pre-existing data issue (not a code regression) that is now resolved.

## Follow-ups

1. **Add CozoDB schema version check to `entrypoint.sh`** -- the `index_if_needed()` function currently skips re-indexing when `manifest` or `data/CURRENT` exists, but doesn't validate the storage version. A v1 RocksDB next to a v3-binary will silently crash. Recommend: read `manifest` and verify `CozoDB version >= 3` before skipping; otherwise wipe and re-index.
2. **Image rebuild requires full `cargo vendor`** -- the `Dockerfile.rocksdb` does `COPY vendor/ ./vendor/`, but the local `vendor/` directory is sparse (`cc` + `console` only). A `cargo build --release` with `cargo vendor` would need to be re-run, or `--no-build` used as today.
3. **Microservice call detection for secondary returns 0** -- the `config/microservice-extractor.yaml` rules likely don't match secondary's gRPC patterns. Out of scope for this fix but worth a separate validation pass.
4. **Push `fix/docker-compose-multi-project` branch to origin** -- currently local only. Auto-review correctly deferred this; user must opt in.