# DOCJOIN symbol upgrade (#172) live evidence — 2026-08-02

## Environment
- commit: 8c77b22b | binary: target/release/leankg 0.19.31 (local) | project: /tmp/leankg-live-fixture
- docs/api.md contains `src/backend/service.py::fetch_orders` + `src/backend/gateway.rs::call_payments` refs

## Steps
1. `index-docs --path docs --project .`
2. Inspect relationships emitted (documented_by / references with granularity)

## Results
- `index-docs` emitted per-symbol edges: `docs/api.md --references--> src/backend/service.py::fetch_orders` with `{"granularity":"per-symbol","via_doc":"docs/api.md","via_edge":"references"}` — PASS symbol-level edge when unique.
- Inverse `fetch_orders --documented_by--> docs/api.md` with same metadata — PASS.
- Same for `gateway.rs::call_payments` (2nd symbol) — PASS.
- File-level edges also present: `service.py --documented_by--> docs/api.md`, `gateway.rs --documented_by--> docs/api.md` — PASS file-level fallback retained.
- All edges `confidence: 1.0`, `confidence_label: EXTRACTED`.

## Tracker
- DOCJOIN symbol upgrade (#172): PASS. Symbol-level `documented_by`/`references` edges created when the doc references `file::symbol` uniquely; file-level edges retained alongside. On workspace-be, `find_related_docs` resolved the file but returned `related_docs: []` (be docs not doc-indexed in the container index).
