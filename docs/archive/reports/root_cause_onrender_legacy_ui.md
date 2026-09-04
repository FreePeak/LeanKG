# Root Cause Analysis: leankg.onrender.com still serves legacy UI after PR #92

**Date:** 2026-07-21  
**Live:** https://leankg.onrender.com/  
**Symptom:** Homepage is old Vite app (`<title>ui</title>`, `/assets/index-CNdjW_ZL.js`), not ui-v2 (`<title>LeanKG</title>`, Outfit fonts, Folders & files).

---

## Evidence

| Source | Title | JS asset |
|--------|-------|----------|
| **Live OnRender** | `ui` | `index-CNdjW_ZL.js` |
| **`origin/main` `src/embed/index.html`** | `LeanKG` | `index-B10b4eUt.js` |
| **Git history match for live assets** | `ui` | Commit era **#44 / massive-graph (Apr 2026)** (`e2629b9` / `658d253`) |

`cf-cache-status: DYNAMIC` → not a simple Cloudflare HTML cache of a new binary.

`GET /api/index/status` works on live → process is `leankg web` / serve, but **binary was compiled with ancient rust_embed bytes**.

---

## Root causes (ranked)

### RC-1 — Render runtime image never rebuilt (primary)

`render.yaml` + `Dockerfile` on `main` already bake **ui-v2** into `src/embed/` before `cargo build`.  
After merge of PR #92, live still serves Apr-2026 assets ⇒ the Render service is still running an **old Docker image** (auto-deploy off, deploy failed, or build used **stale layer cache** without invalidating the UI stage).

**Ops fix:** Manual Deploy + **Clear build cache** on the `leankg` service (Render CLI token was expired in this environment).

### RC-2 — Release workflow still bakes legacy `ui/` (latent)

`.github/workflows/release.yml` still does:

```bash
cd ui && bun run build && cp -r dist/* ../src/embed/
```

Tag releases would ship **legacy UI** into binaries even while Docker/OnRender Dockerfiles use ui-v2. Does not explain *current* OnRender alone (uses `Dockerfile`), but will re-poison embeds on the next `v*` tag.

### RC-3 — `find_ui_dist_path()` still prefers `ui/dist`

`src/main.rs::find_ui_dist_path` looks for `ui/dist` / `LEANKG_UI_DIST`.  
`web::start_server` currently **ignores** `_ui_dist_path` and always uses `embed::get` — so this is not the live OnRender path today, but it is a footgun for local/`LEANKG_UI_DIST` setups.

### RC-4 — No deploy fingerprint

There was no `/api/ui-build` (or similar) to prove which UI binary is live, so “merged but still old” looked like an app bug instead of a **stuck deploy**.

---

## Code path (correct when image is fresh)

```text
Dockerfile → npm run build (ui-v2) → cp dist → src/embed/
       → cargo build (rust_embed compiles bytes into leankg)
CMD leankg web → web::root_handler → embed::get("index.html")
```

---

## Fixes in this change set

1. `release.yml` → build **ui-v2** into `src/embed/`
2. `find_ui_dist_path` → prefer `ui-v2/dist`
3. Write `src/embed/ui-build.json` in Docker; expose `GET /api/ui-build`
4. `Cache-Control: no-store` on HTML shell
5. Bump `UI_EMBED_REV` to force Docker layer invalidation
6. Ontology: concepts + workflows for ui-v2 / OnRender / expand pagination

---

## Acceptance

- [ ] Live `/` title is `LeanKG` and assets match current embed (or `/api/ui-build` reports `ui=ui-v2`)
- [ ] Tag release workflow no longer builds `ui/`
- [ ] Ontology sync lists new concepts/workflows
