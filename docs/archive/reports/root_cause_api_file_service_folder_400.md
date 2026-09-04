# Root Cause: Service/Folder → HTTP 400 `/api/file` + missing replace-graph

**Date:** 2026-07-21  
**IDs:** US-UI2-03 (tightened), US-UI2-10, FR-UI2-12, REL-057

## Issue Description

In UI v2, selecting a Service or Folder node called `GET /api/file?path=<directory>`. The API only reads file contents, so Axum returned HTTP 400. Multi-service topology also lacked double-click drill-in that **replaces** the canvas with that service’s expand-service subgraph (legacy `ui/` behavior).

## Problematic Code

- `ui-v2/src/components/CodePanel.tsx` — `readFile(filePath)` for any non-empty path
- `ui-v2/src/App.tsx` — click only `setSelectedId`; no `expandService` on Service/Folder activation
- `src/web/handlers.rs` `api_get_file` — `read_to_string` on directories → OS error / not found; all failures → HTTP 400

## Logic Flow (before)

```text
Topology graph → click Service → select → CodePanel → /api/file(directory) → 400
```

## Logic Flow (after)

```text
Topology → single-click Service → CodePanel metadata (no /api/file)
Topology → double-click Service → expand-service?all=true → REPLACE kg + breadcrumbs
Content node → single-click → /api/file source
```

## Root Cause

1. UI treated directory `filePath` as a source file.
2. UI v2 never wired replace-graph expand on Service/Folder activation.
3. PRD US-UI2-03 previously said any node select opens `/api/file`.

## Fix

- `node-kinds.ts` gate + CodePanel skip for containers
- Double-click → `expandService` replace + breadcrumb Overview
- `api_get_file` directory message + absolute-under-project normalize

## Trace / verification

- Vitest: `ui-v2/test/unit/node-kinds.test.ts`
- Manual: multi-service topology → double-click service → new node counts; single-click service → no `/api/file` 400

Use container path placeholders (`/workspace`, `/workspace-other`) in examples — never personal mount nicknames.
