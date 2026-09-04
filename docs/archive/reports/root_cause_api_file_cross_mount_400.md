# Root Cause: `/api/file` HTTP 400 for indexed paths missing on active mount

**Date:** 2026-07-21  
**PR:** [#92](https://github.com/FreePeak/LeanKG/pull/92)

## Issue Description

UI v2 CodePanel called:

```text
GET /api/file?path=./claude-mem/plugin/ui/viewer-bundle.js
```

and received **HTTP 400** with a generic client error (`HTTP 400 /api/file?…`), even though the file exists on another Docker mount.

## Problematic Flow

```text
Active project: /workspace
Graph (RocksDB for /workspace) contains file_path=./claude-mem/...
Disk under /workspace: path missing (os error 2)
Disk under /workspace-freepeak: file exists
api_get_file → only joins current_project_path → not found
ApiResponse::IntoResponse → always StatusCode::BAD_REQUEST (400)
UI fetchJson → throw `HTTP 400 ${path}` (drops JSON error body)
```

## Root Causes

1. **Stale / cross-mount graph rows:** Expanding `.` on `/workspace` returned hundreds of `claude-mem/*` elements from the workspace RocksDB key even though those files are not on the `/workspace` bind. The bytes live under `/workspace-freepeak` (listed in `LEANKG_PROJECT_DIRS`).
2. **Single-root file resolve:** `api_get_file` only resolved under `current_project_path`, so sibling mounts were never probed.
3. **Status + client opacity:** All `ApiResponse` failures mapped to HTTP 400; UI discarded the JSON `error` field and showed only the status line.

## Fix

1. `src/web/file_resolve.rs` — clean relative path; try primary root then each `LEANKG_PROJECT_DIRS` entry; return typed Directory / Outside / NotFound.
2. `api_get_file` — use resolve helper; **404** for missing, **400** for directory, **403** for escape; keep success payload shape.
3. `ui-v2` `fetchJson` — surface `body.error` on non-OK responses.

## Trace / verification

```bash
# Before: 400 File not found under /workspace only
# After (with LEANKG_PROJECT_DIRS including /workspace-freepeak):
curl -sS -w '\nHTTP:%{http_code}\n' \
  'http://127.0.0.1:8080/api/file?path=.%2Fclaude-mem%2Fplugin%2Fui%2Fviewer-bundle.js'
# → 200 + content when sibling mount has the file

cargo test -p leankg --lib file_resolve
cd ui-v2 && npm test
```

## Follow-ups (not in this PR)

- Reindex `/workspace` so its graph matches the leanKG tree (remove foreign `claude-mem` rows).
- Optional: expand-service filter for paths missing on the active root.

Use container placeholders (`/workspace`, `/workspace-other`, `/workspace-freepeak`) — never personal host bind paths.
