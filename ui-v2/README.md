# LeanKG UI v2 — README

GitNexus-inspired graph explorer for LeanKG (`leankg serve` REST on `:8080`).  
Phase 1: exploring shell only — no browser LLM agent.

## Quick start

```bash
# Terminal A — from an indexed LeanKG checkout (uses cwd’s .leankg)
cargo run --release -- serve --port 8080
# or: ./target/release/leankg serve --port 8080

# Terminal B
cd ui-v2 && npm install && npm run dev
```

Open:

| URL | What you get |
|-----|----------------|
| http://127.0.0.1:5173/?path=src/cli | Bounded expand (~500 nodes) — **recommended** |
| http://127.0.0.1:5173/?path=src | Larger subtree |
| http://127.0.0.1:5173/?skipGraph=1 | Mega-graph skip / topology overview |
| http://127.0.0.1:5173/ | Auto-expand project root (needs current `serve` binary) |

Vite proxies `/api` → `127.0.0.1:8080`. Status should show **connected**.

**Not Docker MCP `:9699`** — UI talks REST (`/api/graph/*`, `/api/file`, `/api/search`), not MCP JSON-RPC.

## Screenshots

Fresh captures (Force / Tree / Circles / Query / Search / Mega-skip / Code panel):

→ [`docs/reports/ui-v2-screenshots-2026-07-20.md`](../docs/reports/ui-v2-screenshots-2026-07-20.md)

| Force | Tree | Circles |
|-------|------|---------|
| ![Force](../docs/reports/screenshots/01-force-src.png) | ![Tree](../docs/reports/screenshots/02-tree-src.png) | ![Circles](../docs/reports/screenshots/03-circles-src.png) |

Viewport / smoothness RCA:

→ [`docs/reports/ui-v2-empty-panel-smoothness-rca-2026-07-20.md`](../docs/reports/ui-v2-empty-panel-smoothness-rca-2026-07-20.md)

## Features (Phase 1)

- Force / Tree / Circles layouts (Sigma + graphology)
- Left explore: node/edge filters, focus depth, file list
- Code panel on file/node select (`GET /api/file`)
- Header search (`GET /api/search`) + Query FAB (`POST /api/query`)
- Mega-graph skip gate + “Load graph anyway”
- URL: `?path=`, `?skipGraph=`, `?project=`, `?expand=1`

## Tests

```bash
npm test              # Vitest unit
E2E=1 npm run test:e2e  # Playwright (needs serve :8080)
```

## Provenance

Shell / Tree / Circles / Sigma patterns adapted from GitNexus `gitnexus-web`.  
`backend-client`, schema normalize, and LeanKG wiring written for this repo.  
Legacy [`ui/`](../ui/) + `src/embed/` unchanged until cutover.
