# LeanKG UI v2 — Screenshot report

**Date:** 2026-07-20 (updated after viewport/filter fixes)  
**Branch:** `feature/ui-v2`  
**Backend:** `leankg serve --port 8080` (REST `/api/*`, not Docker MCP `:9699`)  
**Frontend:** `ui-v2` Vite + Playwright Chromium  
**Fixture:** `/?path=src/cli` (~500 nodes) unless noted

## How to reproduce

```bash
# indexed LeanKG checkout
./target/release/leankg serve --port 8080   # or worktree binary

cd ui-v2 && npm run dev
# optional: node scripts/capture-screenshots.mjs
```

## Matrix

| # | File | View |
|---|------|------|
| 01 | [01-force-src.png](screenshots/01-force-src.png) | Force layout (full viewport) |
| 02 | [02-tree-src.png](screenshots/02-tree-src.png) | Tree layout |
| 03 | [03-circles-src.png](screenshots/03-circles-src.png) | Circles layout |
| 04 | [04-query-panel.png](screenshots/04-query-panel.png) | Query FAB open |
| 05 | [05-search.png](screenshots/05-search.png) | Header search `cli` |
| 06 | [06-mega-skip.png](screenshots/06-mega-skip.png) | `?skipGraph=1` |
| 07 | [07-code-panel.png](screenshots/07-code-panel.png) | Code panel + graph |
| 08–10 | `08`/`09`/`10-*-full-viewport.png` | Same layouts (post-fix duplicates) |

## Screenshots

### 01 — Force

![Force](screenshots/01-force-src.png)

### 02 — Tree

![Tree](screenshots/02-tree-src.png)

### 03 — Circles

![Circles](screenshots/03-circles-src.png)

### 04 — Query panel

![Query](screenshots/04-query-panel.png)

### 05 — Search

![Search](screenshots/05-search.png)

### 06 — Mega-graph skip

![Mega skip](screenshots/06-mega-skip.png)

### 07 — Code panel

![Code panel](screenshots/07-code-panel.png)

## Related

- Smoothness / viewport RCA: [`ui-v2-empty-panel-smoothness-rca-2026-07-20.md`](ui-v2-empty-panel-smoothness-rca-2026-07-20.md)
- Parity: [`ui-v2-gitnexus-parity-2026-07-20.md`](ui-v2-gitnexus-parity-2026-07-20.md)
- ERD: [`../erd/ui-v2-erd.md`](../erd/ui-v2-erd.md)
