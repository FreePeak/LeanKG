# UI v2 production cutover evidence

**Date:** 2026-07-21  
**PRD:** [`docs/prd.md`](../prd.md) §5.19 · IDs `US-UI2-07` / `FR-UI2-09` / `REL-057`  
**Prior parity:** [`ui-v2-gitnexus-parity-2026-07-20.md`](ui-v2-gitnexus-parity-2026-07-20.md)  
**Screenshots:** [`ui-v2-screenshots-2026-07-20.md`](ui-v2-screenshots-2026-07-20.md)

---

## Verdict

**ui-v2 is the default embedded explorer** for `leankg serve`, Docker Option A (`:8080`), and onrender. Legacy `ui/` is source-only and not copied into `src/embed/`.

---

## Evidence

| Check | Result |
|-------|--------|
| Embedded assets | `src/embed/` contains ui-v2 build (`index.html`, `assets/`, `favicon.svg`) |
| Build stamp | `src/embed/ui-build.json` → `{"ui":"ui-v2","rev":"2026-07-21-onrender-rca2",...}` |
| `leankg serve` | Serves embedded UI at `http://localhost:8080/` + `/api/*` REST |
| Docker compose | `LEANKG_SERVE_HTTP=1` starts serve alongside MCP (`docker-compose.rocksdb.yml`) |
| onrender | Multi-stage Dockerfile bakes demo index + ui-v2 embed; live demo at https://leankg.onrender.com |
| README screenshots | Force / Tree / Circles / code panel / search / Query FAB / mega-skip gate |

---

## Smoke commands

```bash
# Local serve (from repo root with indexed .leankg)
cargo run --release -- serve
curl -sf http://localhost:8080/api/index/status | jq .
open http://localhost:8080/

# Docker (published image or compose)
curl -sf http://localhost:8080/api/index/status
curl -sf http://localhost:9699/health

# ui-v2 dev (hot reload; proxies /api → :8080)
cd ui-v2 && npm run dev
```

**Expected:** `/api/index/status` returns `element_count > 0` when indexed; browser loads ui-v2 shell (Force layout, filters, status bar). Mega-graph projects show skip gate with "Load graph anyway".

---

## Rebuild embed (after ui-v2 source changes)

```bash
cd ui-v2 && npm run build
rm -rf ../src/embed/*
cp -r dist/* ../src/embed/
echo '{"ui":"ui-v2","rev":"'$(date +%Y-%m-%d)'","source":"local-main"}' > ../src/embed/ui-build.json
cargo build --release
```

---

## Known follow-ups (not blocking cutover)

- Query FAB NL mode → `query_graph` (`US-UI2-06` / `FR-UI2-08`) — Wave 3
- Cluster legend + ops panels (`FR-UI2-10` / `FR-UI2-11`) — P2
