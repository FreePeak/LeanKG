# LeanKG 3D Graph Explorer (graph-ui/)

Track E standalone 3D graph explorer SPA (FR-E01..E05). Separate from the 2D
explorers in `ui/` and `ui-v2/` — own package, own Vite config, own build.

## Stack

Vite + React 19 + Three.js + @react-three/fiber + @react-three/drei + Vitest.

## Backend API used

| Endpoint | Purpose | FR |
|---|---|---|
| `GET /api/graph/layout3d?iterations&seed` | Deterministic seeded 3D positions + bounds | FR-E01 |
| `GET /api/graph/data` | Element nodes + edges (node detail source) | FR-E03 |
| `GET /api/graph/clusters` | Directory clusters for coloring + legend | FR-E04 |

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

## Features

- FR-E01 3D scene — nodes as spheres, edges as line segments from layout3d
- FR-E02 orbit camera controls (OrbitControls, damping)
- FR-E03 node click → detail panel (element type, file, degree, id)
- FR-E04 cluster coloring — stable color per directory cluster
- FR-E05 lazy layout — computed only after "Load 3D layout" click
