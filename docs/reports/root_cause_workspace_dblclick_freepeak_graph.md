# Root Cause: Double-click Workspace opens freepeak graph

**Date:** 2026-07-21  
**PR:** [#92](https://github.com/FreePeak/LeanKG/pull/92)

## Symptom

UI at `?project=/workspace` → double-click red **workspace** Service node → canvas fills with freepeak-style trees (`claude-mem`, `camo-prop-hunt`, …) instead of LeanKG (`src/`, `Cargo.toml`, …).

## Evidence (live Docker before hot-swap)

| Probe | Result |
|-------|--------|
| MCP `mcp_status(project=/workspace)` | **7850** elements — real LeanKG RocksDB |
| Serve `GET /api/index/status` | `project_path=/workspace` but **66118** elements |
| Serve `expand-service?path=.` | Top dirs = freepeak (`claude-mem` 259/500) |
| Serve `leankg serve --help` | **No `--project`** — container still on pre-fix binary |
| Topology node | `filePath: "/workspace"` → UI expands `.` |

## Root causes (stacked)

1. **Serve graph ≠ status path (desync)**  
   Old `/api/project/switch` set `current_project_path` to `/workspace` **before** successfully opening that RocksDB. Open often fails (`LOCK: Resource temporarily unavailable` while MCP holds another handle). UI/status show `/workspace`; `graph_engine` still serves the previously opened multi-repo / freepeak DB. Double-click expands `.` on **that** engine → freepeak tree.

2. **Entrypoint started serve under MCP cwd**  
   `cd $LEANKG_MCP_PROJECT` then `leankg serve` (no `--project`) so the default open DB was a multi-repo mount, not LeanKG `/workspace`.

3. **Container not rebuilt**  
   Fixes landed in PR #92 (`--project`, atomic switch, ghost filter) but the running image still had the old binary — so the bug reproduced until hot-swap / rebuild.

4. **Double-click path itself is fine**  
   Service `filePath=/workspace` correctly normalizes to `.`. The bug is **which RocksDB** `.` is expanded against, not the expand path string.

## Fix

| Layer | Change |
|-------|--------|
| Entrypoint | `leankg serve --project ${LEANKG_SERVE_PROJECT:-/workspace}` |
| `switch_project` | Drop DB → open → then update paths; error (no silent desync) |
| Switch API | Await open; skip auto-reindex when populated |
| Expand | Drop nodes missing on disk under active root |
| UI | Re-`switchProject(?project=)` before container double-click expand |

## Verify

```bash
leankg serve --help | grep project
curl -sS -X POST …/api/project/switch -d '{"path":"/workspace"}'
curl -sS …/api/index/status   # element_count ~7–8k for LeanKG, not 66k
curl -sS '…/expand-service?path=.&all=true'  # top dirs: src, docs, …
```
