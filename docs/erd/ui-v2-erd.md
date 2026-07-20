# ERD: LeanKG UI v2 (GitNexus Shell Adapted)

## Overview

**Feature**: UI v2 — 2D graph explorer rebuilt from GitNexus web shell  
**Location**: `ui-v2/` (Phase 1; existing `ui/` + `src/embed/` unchanged until cutover)  
**Backend**: `leankg serve` REST on `:8080`  
**Out of scope**: Track E 3D `graph-ui/` (R3F), browser LLM agent (Phase 2)

## Problem Statement

Current `ui/` is a single-pane force graph with limited search and no Tree/Circles layouts. GitNexus `gitnexus-web` provides a proven 3-pane exploring shell that LeanKG should adapt without adopting GitNexus APIs or agent chat in Phase 1.

## Solution Summary

Copy GitNexus exploring UX (Header, FileTree+Filters, Sigma Force/Tree/Circles, Code panel, StatusBar, mega-graph skip) and rewrite the data plane against LeanKG `/api/*`.

## Scope

- **In**: `ui-v2/**`, PRD/tracker/AGENTS docs, Vitest + Playwright parity proof
- **Out**: `ui/**`, `src/embed/**` cutover, LangChain agent, analyze/upload, Teams UI

## Acceptance Criteria

- [ ] AC1: Force/Tree/Circles layouts render against `leankg serve`
- [ ] AC2: Filters default to Service/Folder/File/Function (US-MG-04)
- [ ] AC3: Node select loads `/api/file` into code panel
- [ ] AC4: Server search + QueryFAB work
- [ ] AC5: Mega-graph skip + Load anyway
- [ ] AC6: Vitest + Playwright Phase-1 matrix green; parity report committed

## API Map

| UI need | Endpoint |
|---------|----------|
| Index status | `GET /api/index/status` |
| Project switch | `POST /api/project/switch` |
| Topology | `GET /api/graph/service-topology` |
| Expand | `GET /api/graph/expand-service?path=&all=true` |
| Children | `GET /api/graph/children?parent=` |
| Clusters | `GET /api/graph/clusters` |
| File | `GET /api/file?path=` |
| Search | `GET /api/search?q=` |
| Query | `POST /api/query` |
| Graph data | `GET /api/graph/data` |

## Sequence Flow

```
User opens ui-v2
  -> probe /api/index/status
  -> GET service-topology
  -> if multi-service: show topology
  -> else / on expand: decideSkipGraph(nodeCount)
       -> skip: overview mode + Load anyway
       -> load: expand-service -> Sigma Force|Tree|Circles
  -> select node -> GET /api/file
  -> search -> GET /api/search -> highlight
```

## Cutover (later)

```bash
cd ui-v2 && npm run build
cp -r dist/* ../src/embed/
cargo build --release
```

Phase 1 does **not** replace `src/embed/`.

## Changelog

| Date | Change |
|------|--------|
| 2026-07-20 | Initial ERD for UI v2 Phase 1 |
