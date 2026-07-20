# Web UI

LeanKG ships **UI v2** (GitNexus-inspired explorer in `ui-v2/`). Production assets are built into `src/embed/` and served by `leankg serve` / `leankg web` (onrender, Docker `:8080`).

Legacy source under `ui/` is kept for reference only — it is **not** embedded anymore.

## Start Web UI

```bash
# REST API + embedded UI v2 (default port: 8080)
leankg serve
# or
leankg web --port 9000
```

Open **http://localhost:8080/?path=src/cli** (bounded expand is recommended).

Dev hot-reload: `cd ui-v2 && npm run dev` → http://localhost:5173 (proxies `/api` → `:8080`).

## Refresh embedded assets

```bash
cd ui-v2 && npm run build
rm -rf ../src/embed/*
cp -r dist/* ../src/embed/
cargo build --release
```

Docker / onrender builds run this automatically (see `Dockerfile` / `Dockerfile.rocksdb`).

## Features

Screenshots: [docs/reports/ui-v2-screenshots-2026-07-20.md](reports/ui-v2-screenshots-2026-07-20.md) · App notes: [ui-v2/README.md](../ui-v2/README.md)

- **Force / Tree / Circles** layouts (Sigma + graphology)
- **Filters + file tree** (US-MG-04 defaults)
- **Code panel** via `/api/file`
- **Search + Query FAB** via `/api/search` and `/api/query`
- **Mega-graph skip** gate with “Load graph anyway”

## Architecture

- **Frontend:** Vite + React + Tailwind (`ui-v2/`)
- **Backend:** Axum REST (`/api/*`)
- **Bundle:** `rust_embed` over `src/embed/`
