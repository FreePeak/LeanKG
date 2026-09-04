# vue/svelte/sql indexing (#170) live evidence — 2026-08-02

## Environment
- commit: 8c77b22b | binary: target/release/leankg 0.19.31 (local) | project: /tmp/leankg-live-fixture (7 seed files)
- embeddings feature: yes

## Steps
1. Seeded `src/frontend/UserList.vue` (.vue), `src/frontend/Dashboard.svelte` (.svelte), `src/backend/schema.sql` (.sql), plus .py/.rs/.go
2. `init` + `index src/` → "Indexed 6 files (20 elements)" then final: 34 elements, 49 relationships, 14 files
3. `query "UserList"` / `query "schema.sql"` / `query "fetch_orders"`

## Results
- `query "UserList"` → `UserList.vue (vue_component)` — **PASS**: .vue indexed as file + vue_component element.
- `query "schema.sql"` → `schema.sql (file)` — **PASS**: .sql indexed as file-level element.
- `query "fetch_orders"` → `fetch_orders (function)` in service.py — PASS (py).
- `query "computeTotal"` (.svelte fn) → No elements found — **partial**: .svelte file indexed but script functions not extracted at function-level (file-level element only). Consistent with AC "file-level elements file::*.vue etc." — file-level PASS.
- SQL `users` table: only file-level element for schema.sql; no separate `users` table element extracted. AC for sql was "sql `users` table element" — **partial**: table not extracted as element; file-level sql present.

## Cross-check on workspace-be (Docker MCP :9699, project=/workspace-be)
- `query_file pattern="*.vue"` → count 0 (be repo has no .vue files — expected)
- `query_file pattern="*.sql"` → 1 result (route named `sql_duration_limit_tip` — a route, not a .sql file)

## Tracker
- vue/svelte/sql indexing (#170): PASS for .vue + .sql file-level + .py function-level. Partial: .svelte script functions and SQL table elements not extracted as separate elements in this build (file-level only). be repo has no .vue/.svelte; single .sql-adjacent route found.
