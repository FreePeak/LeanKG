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

## Docker (Option A — MCP + REST in one container)

RocksDB compose publishes **both** ports and starts `leankg serve` in the background before MCP:

| Port | Process | Clients |
|------|---------|---------|
| `9699` | `mcp-http` | Cursor / agents (MCP) |
| `8080` | `leankg serve` | UI v2 Vite proxy (`/api`) |

```bash
docker compose -f docker-compose.rocksdb.yml --env-file .dockerfile up -d --build
# Host UI
cd ui-v2 && npm run dev
# open http://127.0.0.1:5173/?path=src/cli
```

Same RocksDB env (`LEANKG_DB_ENGINE`, `LEANKG_ROCKSDB_ROOT`) and `LEANKG_MCP_PROJECT` cwd — UI sees the Docker index. Disable REST with `LEANKG_SERVE_HTTP=0` in `.dockerfile` if you only need MCP.

## Screenshots

Fresh captures (Force / Tree / Circles / Query / Search / Mega-skip / Code panel):

→ [`docs/reports/ui-v2-screenshots-2026-07-20.md`](../docs/reports/ui-v2-screenshots-2026-07-20.md)

| Force | Tree | Circles |
|-------|------|---------|
| ![Force](../docs/reports/screenshots/01-force-src.png) | ![Tree](../docs/reports/screenshots/02-tree-src.png) | ![Circles](../docs/reports/screenshots/03-circles-src.png) |

| Query | Search | Mega-skip | Code panel |
|-------|--------|-----------|------------|
| ![Query](../docs/reports/screenshots/04-query-panel.png) | ![Search](../docs/reports/screenshots/05-search.png) | ![Mega skip](../docs/reports/screenshots/06-mega-skip.png) | ![Code](../docs/reports/screenshots/07-code-panel.png) |

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
