# LeanKG UI v2 — README

LeanKG’s graph explorer **uses the [GitNexus](https://github.com/abhigyanpatwari/GitNexus) UI shell** (`gitnexus-web`: 3-pane layout, Force / Tree / Circles, Sigma, mega-graph skip).  
The data plane is rewritten for LeanKG `leankg serve` REST (`/api/graph/*`, `/api/file`, `/api/search`) — not GitNexus APIs.  
Phase 1: exploring shell only — no browser LLM agent.

**Production path:** `npm run build` → copy `dist/*` into `src/embed/` → `leankg serve` / onrender / Docker `:8080` serve the embedded UI. Dev still uses Vite on `:5173`.

## Quick start

```bash
# Terminal A — REST + embedded UI (after assets are in src/embed/)
cargo run --release -- serve --port 8080
# open http://127.0.0.1:8080/?path=src/cli

# Terminal B — hot reload during UI work
cd ui-v2 && npm install && npm run dev
```

Open (Vite or embedded):

| URL | What you get |
|-----|----------------|
| http://127.0.0.1:8080/?path=src/cli | Embedded UI v2 (onrender / Docker serve) |
| http://127.0.0.1:5173/?path=src/cli | Vite dev — bounded expand (~500 nodes) |
| http://127.0.0.1:5173/?path=src | Larger subtree |
| http://127.0.0.1:5173/?skipGraph=1 | Mega-graph skip / topology overview |

Vite proxies `/api` → `127.0.0.1:8080`. Status should show **connected**.

**Not Docker MCP `:9699`** — UI talks REST (`/api/graph/*`, `/api/file`, `/api/search`), not MCP JSON-RPC.

### Refresh embedded assets

```bash
cd ui-v2 && npm run build
rm -rf ../src/embed/*
cp -r dist/* ../src/embed/
```

## One binary — MCP + REST

`leankg serve` embeds the built UI v2 and serves both surfaces:

| Port | Process | Clients |
|------|---------|---------|
| `9699` | `mcp-http` | Cursor / agents (MCP) |
| `8080` | `leankg serve` | **Embedded UI v2** + `/api` |

```bash
leankg serve
# open http://127.0.0.1:8080/?path=src/cli
```

sqlite is the default storage engine — point the UI at any project
checkout via `?path=` (same cwd `leankg serve` was started in).

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

## Navigation (US-UI2-10 / FR-UI2-12)

| Action | Result |
|--------|--------|
| Single-click File / Function / … | Code panel loads `/api/file` |
| Single-click Service / Folder / Directory | Code panel shows metadata only (no `/api/file`) |
| Double-click Service / Folder / Directory | `expand-service?all=true` **replaces** the canvas; breadcrumbs → Overview |

RCA: [`docs/reports/root_cause_api_file_service_folder_400.md`](../docs/reports/root_cause_api_file_service_folder_400.md).

## Features (Phase 1)

- Force / Tree / Circles layouts (Sigma + graphology)
- Left explore: node/edge filters, focus depth, file list
- Code panel on content-bearing select (`GET /api/file`); Service/Folder metadata only
- Double-click Service/Folder replaces graph via expand-service
- Header search (`GET /api/search`) + Query FAB: **NL** default (`POST /api/query-graph`) · **Advanced** raw graph query (`POST /api/query`) — FR-UI2-08
- Mega-graph skip gate + “Load graph anyway”
- URL: `?path=`, `?skipGraph=`, `?project=`, `?expand=1`

## Tests

```bash
npm test              # Vitest unit
E2E=1 npm run test:e2e  # Playwright (needs serve :8080)
```

## Provenance

**UI shell:** adapted from [GitNexus](https://github.com/abhigyanpatwari/GitNexus) `gitnexus-web` (Header, FileTree + filters, Force / Tree / Circles, Code panel, StatusBar, mega-graph skip).  
**LeanKG wiring:** `backend-client`, schema normalize, and `/api/*` against this repo.  
Legacy [`ui/`](../ui/) kept for reference; production embed is UI v2 (`src/embed/`).
