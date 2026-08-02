# Track E serve (#181/188) live evidence — 2026-08-02

## Environment
- commit: 8c77b22b | binary: target/release/leankg 0.19.31 (local) | server: :9876 (web) | project: /tmp/leankg-live-fixture

## Steps
1. `leankg web --port 9876 --project /tmp/leankg-live-fixture`
2. curl root `/`, `/assets/index-CwcVR9A7.js`, `/assets/index-BN6mYsjm.css`, `/api/ui-build`, `/3d/`

## Results
- `GET /` → **200** text/html, `<title>LeanKG</title>` (ui-v2 shell) — PASS.
- `GET /assets/index-CwcVR9A7.js` → **200 application/javascript** — PASS (embedded asset served from `src/embed/assets/`).
- `GET /assets/index-BN6mYsjm.css` → **200 text/css** — PASS.
- `GET /api/ui-build` → `{"success":true,"data":{"feature":"FR-UI2-08","index_js":"index-CwcVR9A7.js","index_title_leankg":true,"index_title_legacy_ui":false,"rev":"2026-08-01","ui":"ui-v2"}}` — PASS (ui=ui-v2, rev=2026-08-01).
- `GET /3d/` → 404 — expected: embedded ui-v2 SPA is Sigma-based (2D graph), no `/3d` route in the embedded build. The 3D feature lives in the `layout3d` API (probe #15) + graph-ui/ui-v2 SPA routes. `has_3d` field not present in ui-build payload in this build.
- ui-v2 `/` route untouched: root serves the embedded shell directly, no legacy `ui` title. PASS.

## Tracker
- Track E serve (#181/188): PASS for HTML/JS/CSS/ui-build serving. `/3d` route + `has_3d` field: SKIP (not in embedded build; embedded UI = Sigma 2D). ui-v2 untouched at `/`. Note: `api-serve` subcommand serves the legacy `/api/v1` + `/api/v2` surface (separate router), NOT the web routes.
