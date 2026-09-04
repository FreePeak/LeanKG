# RCA: UI v2 empty panel + viewport / smoothness (updated)

**Date:** 2026-07-20  
**Branch:** `feature/ui-v2`

## Fixes landed

| Issue | Root cause | Fix |
|-------|------------|-----|
| Graph empty / off-camera | Custom camera `setState` used graphology coords; Sigma v3 `animatedReset` uses a different home space | Fit via `resize()` + `animatedReset` only |
| Graph only in a strip | Canvas parent lacked height; Tree FA2 scramble | `h-full`/`min-h-0`, ResizeObserver; skip FA2 for tree/circles |
| ~490/500 nodes invisible | API types `property`/`function` not in defaults | PascalCase normalize + defaults include Property/Method/Class/… |
| Bare `/` → Loaded 0 | Abs project path → `./` folder query | Backend + `normalizeExpandPath` → `.` |
| Tree vertically flat | Short layer canvas | `CANVAS_HEIGHT=2200` |

## Verify

```bash
cd .worktrees/feature/ui-v2/ui-v2 && npm test   # 11 passed
# leankg serve --port 8080 (indexed checkout)
npm run dev
open 'http://127.0.0.1:5173/?path=src/cli'
```

Hard-refresh the browser so Vite picks up the new `useSigma` / defaults.

Screenshots: `08-tree-full-viewport.png`, `09-force-full-viewport.png`, `10-circles-full-viewport.png`
