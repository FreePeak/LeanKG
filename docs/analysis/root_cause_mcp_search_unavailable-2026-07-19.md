# Root Cause Analysis: LeanKG search / lookup unavailable

**Date:** 2026-07-19  
**Symptom:** `search_code`, `find_function`, and other lookup tools appear broken (empty reply, connection reset, or Cursor MCP timeout).  
**Status:** Root cause identified; fixes in `fix/mcp-boot-search`.

## Issue Description

Agents and users could not search or look up code through LeanKG MCP on `:9699`. Health checks failed inside the container (`curl: (7) Failed to connect to 127.0.0.1:9699`), and host clients saw empty replies / connection resets.

## Evidence

| Observation | Detail |
|-------------|--------|
| Container | `Up … (unhealthy)` while port published |
| Health log | `Couldn't connect to 127.0.0.1:9699` (MCP not listening yet) |
| Process table | PID stuck on `leankg ontology sync --path <mcp-project>/ontology` for minutes at ~100% CPU |
| Entrypoint order | Ontology sync runs **before** `leankg mcp-http` (`entrypoint.sh`) |
| Amplifier | `LEANKG_EMBED_BACKGROUND=1` on mega-graph (~640k elements) calls `all_elements()`, RSS soft-cap pauses, container restarts → hits blocking sync again |
| After killing stuck sync | `/health` → `{"status":"ok"}`; `search_code` / `find_function` return results |

## Logic Flow (broken)

```
entrypoint start
  → index_if_needed (skip if RocksDB exists)
  → ontology sync (BLOCKING, opens mega RocksDB)  ← hang / multi-minute delay
  → mcp-http listen                               ← never reached while hung
  → /health fails → search tools appear "broken"
```

## Problematic Code Chunk

```bash
# entrypoint.sh (before fix)
if [ -n "$ONTOLOGY_SOURCE_DIR" ]; then
    ( cd "$MCP_PROJECT" && leankg ontology sync --path "$ONTOLOGY_SOURCE_DIR" )
fi
exec leankg mcp-http ...
```

```rust
// embeddings/build.rs (before fix) — spawn path
let total = graph.all_elements().map(|v| v.len()).unwrap_or(0);
```

## Root Cause

1. **Primary:** Boot ontology sync is synchronous and can hang or run for minutes on large RocksDB projects, so MCP never binds and all search/lookup fails.
2. **Amplifier:** In-process background embed on mega-graphs materializes the full element list, stresses memory/locks, causes unhealthy restarts, and re-enters the blocking sync.

This is an **availability / boot-ordering** failure, not a broken `search_code` algorithm. Once MCP is listening, ontology-first discovery with name fallback returns results.

## Suggested Fix (implemented)

1. `entrypoint.sh`: default ontology sync with **timeout** (45s), skip when marker is fresh, support `LEANKG_ONTOLOGY_SYNC_ON_BOOT=skip|force|timeout`.
2. MCP server: skip `LEANKG_EMBED_BACKGROUND` on mega-graphs unless `LEANKG_EMBED_BACKGROUND_MEGA=1`.
3. Background embed: use `count_elements()` instead of `all_elements()` for the initial total.

## Additional Logging

- Entrypoint warns on timeout and still starts mcp-http.
- MCP logs a clear warn when background embed is skipped on mega-graphs.

## Recovery (ops)

```bash
# Prefer search availability over in-process mega embed
# In local compose override: LEANKG_EMBED_BACKGROUND=0
# After deploying entrypoint fix: LEANKG_ONTOLOGY_SYNC_ON_BOOT=timeout (default)
docker compose -f docker-compose.rocksdb.yml -f docker-compose.override.yml --env-file .dockerfile up -d --force-recreate
curl -sS http://localhost:9699/health
```

## Verification

- `/health` returns ok within seconds of recreate
- `search_code(query="main", project="/workspace")` returns `count > 0`
- `find_function(name="main", project="/workspace")` returns functions
