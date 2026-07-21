# Root Cause: UI tree loads wrong mount (freepeak / multi-repo) for `?project=/workspace`

**Date:** 2026-07-21  
**PR:** [#92](https://github.com/FreePeak/LeanKG/pull/92)

## Issue Description

With Docker Option A and three binds (`/workspace` = LeanKG, plus multi-repo mounts), opening

`http://127.0.0.1:5174/?project=/workspace`

showed freepeak-style trees (`claude-mem`, `camo-prop-hunt`, …) instead of LeanKG (`src/`, `Cargo.toml`, …).

## Root Causes

1. **Entrypoint cwd = MCP project**  
   `entrypoint.sh` did `cd "$LEANKG_MCP_PROJECT"` then `leankg serve &`. When MCP points at a multi-repo mount, **serve’s default graph was that mount**, not `/workspace`.

2. **Non-atomic `/api/project/switch`**  
   `switch_project` updated `current_project_path` **before** successfully opening the target RocksDB. On lock failure the label said `/workspace` while `graph_engine` still held a sibling mount’s DB. Expand/status then disagreed with `?project=`.

3. **Fire-and-forget switch + forced reindex**  
   The HTTP handler returned success immediately and reindexed in a background thread, racing MCP’s RocksDB locks and risking cross-tree pollution.

4. **Ghost paths**  
   Some RocksDB keys still contain relative paths that do not exist under the active root; expand listed them until filtered.

## Fix

| Change | Behavior |
|--------|----------|
| `LEANKG_SERVE_PROJECT` + `leankg serve --project` | Entrypoint starts serve on `/workspace` by default (MCP keeps its own `--project`) |
| Atomic `switch_project` | Drop old DB → open new → update paths; rollback on failure |
| Sync switch API | Await open; skip auto-reindex when `element_count > 0` unless `reindex: true` |
| Expand ghost filter | Drop nodes whose `file_path` is missing under the active project root |
| UI boot | Surface switch errors / path mismatch |

## Verify (after rebuild container)

```bash
curl -sS -X POST http://127.0.0.1:8080/api/project/switch \
  -H 'Content-Type: application/json' \
  -d '{"path":"/workspace"}'
curl -sS http://127.0.0.1:8080/api/index/status
# project_path=/workspace, element_count ≈ LeanKG size (not multi-repo tens of thousands)

curl -sS 'http://127.0.0.1:8080/api/graph/expand-service?path=.&all=true' \
  | python3 -c 'import sys,json,collections;...'
# top dirs should include src/, docs/, … not claude-mem/
```

Use container placeholders only (`/workspace`, `/workspace-other`, `/workspace-freepeak`).
