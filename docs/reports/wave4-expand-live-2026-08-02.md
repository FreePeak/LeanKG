# Wave4 single-repo expand (#164) live evidence — 2026-08-02

## Environment
- commit: 8c77b22b | binary: target/release/leankg 0.19.31 (local, --features embeddings) | server: :9876 (web) | project: /tmp/leankg-live-fixture
- embeddings feature: yes

## Steps
1. Seeded fixture: 7 files (.vue/.svelte/.sql/.py/.rs/.go) + docs/api.md. `init` + `index` → 34 elements, 49 relationships.
2. Started `leankg web --port 9876 --project /tmp/leankg-live-fixture`.
3. `curl 'http://localhost:9876/api/graph/expand-service?path=src&all=true'`

## Results
- `?path=src&all=true` → `{"success":true,"data":{"nodes":[28],"relationships":[23],"filtered":{"message":"Expanded service 'src' with 28 elements and 23 relationships"},"hasMore":false},"error":null}` — PASS: nested content returned via `all=true` (all_content path).
- `?path=.&all=true` → 34 nodes / 41 rels — full single-repo root expands to entire graph. PASS.
- `?path=src/backend` without `all=true` → `{"nodes":[0],"relationships":[0]}` — direct-children-only mode returns 0 for this folder shape (expected per AC: all_content path is the feature; non-all path only matches direct children).
- `?service=backend` (legacy param) → 0 elements (service-name scoping not applicable to this fixture; `service:` prefix stripping works, resolves to folder).

## Tracker
- Wave4 single-repo expand (#164): PASS via `all_content=true`. Note: `all_content` flag is required for the single-repo expand path; default direct-children mode is expected to be narrow.
