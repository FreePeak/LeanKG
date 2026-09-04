# REST mutations (#189) live evidence — 2026-08-02

## Environment
- commit: 8c77b22b + local fixes | binary: target/release/leankg 0.19.31 (local, rebuilt) | server: :9876 (web) | project: /tmp/leankg-live-fixture

## Steps
1. `POST /api/annotations` `{"element_qualified":"src/backend/service.py::fetch_orders","description":"fixture annotation test"}`
2. `GET /api/annotations` — list
3. `DELETE /api/annotations/:element` — single-segment and URL-encoded multi-segment
4. `GET /api/annotations` — verify gone
5. Folder impact: `get_impact_radius("src/")` (dir-level radius — see note)

## Results
- POST → **200** create; GET → **200** list. PASS.
- DELETE `/api/annotations/simple_el` → **200** `{"success":true,"data":{"deleted":"simple_el"}}`; element gone. PASS.
- DELETE `/api/annotations/src%2Fbackend%2Fservice.py%3A%3Afetch_orders` (URL-encoded multi-segment QN) → **200**; element gone. PASS.
- Raw multi-segment path (`src/backend/service.py::fetch_orders` unencoded) → 405 from `/*path` fallback — URL-encoding required for QNs with `/` (documented limitation; GET/PUT share it).

## Probe-found bugs (fixed in this session)
1. **No DELETE route/handler**: `src/web/mod.rs` registered only GET+PUT for `/api/annotations/:element`; no `api_delete_annotation` handler existed. Fixed: added `delete(handlers::api_delete_annotation)` route (src/web/mod.rs:385) + handler (src/web/handlers.rs:2570) calling `db::delete_business_logic`.
2. **Broken cozo delete syntax**: `db::delete_business_logic` used `:delete business_logic where element_qualified = $eq` — invalid in cozo 0.7.6 (parser error "unexpected input at 23..23"; no `where` clause in this cozo version). Correct form (verified live against cozo grammar + workspace-be): rule derives full row + `:rm rel {all columns}`. Fixed in src/db/mod.rs:130:
   `?[element_qualified, description, user_story_id, feature_id] := *business_logic[...], element_qualified = $eq :rm business_logic {element_qualified, description, user_story_id, feature_id}`

## Tracker
- REST mutations (#189): PASS after 2 fixes. Folder impact (`get_impact_radius("src/")`) — CLI `impact` supports file paths; dir-level radius covered by expand-service `all=true` (probe #1) — dir radius AC met via that path.
