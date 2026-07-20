# LeanKG UI v2 — Screenshot report

**Date:** 2026-07-20  
**Branch:** `feature/ui-v2`  
**Backend:** `leankg serve --port 8080` (REST `/api/*`, not Docker MCP `:9699`)  
**Frontend:** `ui-v2` Vite on `:5173` / Playwright Chromium  
**Fixture URL:** `/?path=src/cli` (bounded expand, ~500 nodes) unless noted

## How captured

Automated Playwright screenshots against a live `leankg serve` + Vite proxy. Graph loaded via path-scoped expand (`?path=src/cli`) to stay under API caps.

## Matrix

| # | File | View |
|---|------|------|
| 01 | [01-force-src.png](screenshots/01-force-src.png) | Force layout, connected, Loaded 500 |
| 02 | [02-tree-src.png](screenshots/02-tree-src.png) | Tree layout |
| 03 | [03-circles-src.png](screenshots/03-circles-src.png) | Circles layout |
| 04 | [04-query-panel.png](screenshots/04-query-panel.png) | Query FAB expanded (input + Run) |
| 05 | [05-search.png](screenshots/05-search.png) | Header search `cli` |
| 06 | [06-mega-skip.png](screenshots/06-mega-skip.png) | `?skipGraph=1` mega-graph skip banner |
| 07 | [07-code-panel.png](screenshots/07-code-panel.png) | Code panel for `src/cli/mod.rs` |

## Screenshots

### 01 — Force layout

![Force layout](screenshots/01-force-src.png)

### 02 — Tree layout

![Tree layout](screenshots/02-tree-src.png)

### 03 — Circles layout

![Circles layout](screenshots/03-circles-src.png)

### 04 — Query panel

![Query panel](screenshots/04-query-panel.png)

### 05 — Search

![Search](screenshots/05-search.png)

### 06 — Mega-graph skip

![Mega-graph skip](screenshots/06-mega-skip.png)

### 07 — Code panel

![Code panel](screenshots/07-code-panel.png)

## Related

- Parity / Vitest + Playwright matrix: [`ui-v2-gitnexus-parity-2026-07-20.md`](ui-v2-gitnexus-parity-2026-07-20.md)
- ERD: [`../erd/ui-v2-erd.md`](../erd/ui-v2-erd.md)
