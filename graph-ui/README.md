# LeanKG 3D Graph Explorer (graph-ui/)

Track E standalone 3D graph explorer SPA (FR-E01..E05 + FR-E30..E43). Separate
from the 2D explorers in `ui/` and `ui-v2/` — own package, own Vite config,
own build. Served from the LeanKG binary at `/3d/` (FR-E41/E43).

## Stack

Vite + React 19 + Three.js + @react-three/fiber + @react-three/drei + Vitest.

## Backend API used

| Endpoint | Purpose | FR |
|---|---|---|
| `GET /api/graph/layout3d?iterations&seed` | Deterministic seeded 3D positions + bounds | FR-E01 |
| `GET /api/graph/data` | Element nodes + edges (node detail source) | FR-E03 |
| `GET /api/graph/clusters` | Directory clusters for coloring + legend | FR-E04 |
| `GET /api/file?path=…` | Code context snippet in node detail | FR-E30 |
| `GET /api/index/status` | Element/relationship counts for stats | FR-E33 |
| `GET /api/projects` | Registry + LEANKG_PROJECT_DIRS project list | FR-E33/E36 |
| `POST /api/project/switch` | Switch active project (multi-repo) | FR-E36 |
| `GET /api/ui-build` | Advertises `has_3d` + `/3d` route | FR-E43 |

Layout fetch is on-demand (button) — FR-E05. Graph data + clusters load
eagerly for the detail panel and legend.

## Run

Requires `leankg serve` on `:8080` (Vite dev proxy forwards `/api` there).

```bash
npm install
npm run dev      # http://localhost:5174
npm test         # vitest (jsdom)
npm run build    # tsc -b && vite build
```

Embed into the Rust binary (`/3d/` on `leankg serve`):

```bash
bash scripts/embed-graph-ui.sh
```

## Features

- FR-E01 3D scene — nodes as spheres, edges as line segments from layout3d
- FR-E02 orbit camera controls (OrbitControls, damping)
- FR-E03 node click → detail panel (element type, file, degree, id)
- FR-E04 cluster coloring — stable color per directory cluster
- FR-E05 lazy layout — computed only after "Load 3D layout" click
- FR-E30 node detail enrichment — relationship counts by type + source snippet
- FR-E31 edge-type filter panel — toggle visibility per relationship type
- FR-E32/E38 display settings — bloom, edge brightness, labels, density
- FR-E33 project selector + stats + search panel
- FR-E34 URL routing — tab + project params survive refresh
- FR-E35 hover highlight — dims non-related nodes/edges
- FR-E36 history/undo — bounded selection history with undo/redo
- FR-E37 export/share — JSON snapshot download + share link
- FR-E39 loading/progress overlay, FR-E40 error banner with retry
- FR-E41 keyboard shortcuts (f/s/l/h//, z/y, Escape)
- FR-E42 responsive layout (panels collapse under 900px)
- FR-E43 accessibility (roles, focus-visible, reduced motion)
