# Deep test: dbl-click / navigate back / Load more + sidebar

**Date:** 2026-07-21  
**URL:** `http://127.0.0.1:5174/?project=/workspace`  
**Branch worktree:** `feature/ui-v2-service-expand`

## Bugs confirmed (before fix)

1. Sidebar listed **File** nodes only → empty/sparse when page is mostly Function/Method.
2. Folders section buried under filters → looked like “no folders”.
3. Navigate **Overview** cleared useful tree (topology = 1 Service with `/workspace` path → empty after normalize).
4. **Load more** merged graph but sidebar did not grow folders (no path synthesis from symbols; no session sync).

## Fixes shipped

| Fix | Detail |
|-----|--------|
| Path synthesis | `buildExplorerTree` uses **all** element `filePath`s |
| Session sidebar | `sessionExplorerNodes` kept when returning to Overview |
| Auto-expand | `defaultExpandedPaths` opens `src` (+ one child level) after load/merge |
| UX | Folders & files **first**; Filters collapsed |
| Mount strip | `/workspace/...` → relative for tree |

## Browser evidence

| Step | Result |
|------|--------|
| Boot expand | **9 folders · 80 files**; `src` first + expanded; Load more visible |
| Dbl-click `src` | Breadcrumb `Overview / src`; tree → **2 folders · 29 files** under src |
| Overview back | Sidebar **kept** src tree (29 files) — session OK |
| Dbl-click `src` again | Load more returns |
| Load more (+200) | Tree **29 → 31 files**; new folder **`src/cli`** auto-expanded (`mod.rs`, `shell_runner.rs`) |

## Vitest

`file-tree.test.ts` + `graph-merge.test.ts` — **6 passed** (includes Function-path merge → `src` folders).

## Follow-up (known)

Expand `path=src` uses regex `.*src/.*`, which also matches `examples/**/src/**` (pollutes src drill). Prefer path-prefix match `./src/` in a later fix.
