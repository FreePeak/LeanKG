# GE cluster-first (#179) live evidence — 2026-08-02

## Environment
- commit: 8c77b22b | binary: target/release/leankg 0.19.31 (local) + Docker MCP :9699 (0.19.31)
- project: /workspace-be (Docker, 662k elements) + /tmp/leankg-live-fixture (local, 35 elements)

## Steps
1. `get_clusters` / `get_cluster_context` via Docker MCP on /workspace-be
2. `detect-clusters` CLI on local fixture
3. `get_clusters` via web API on fixture

## Results (mega-graph /workspace-be)
- `get_clusters limit=5` → `{"clusters": [], "error": "Live Louvain refused: graph has 662378 elements (max 50000). No precomputed cluster_id rows found. Run offline cluster assign ...", "source": "precomputed"}` — PASS: bounded, refuses full-scan on mega-graph with actionable hint (no full scan).
- `get_cluster_context cluster_id=0` → `{"error": "get_cluster_context refused: graph has 662378 elements (max 50000 for full-scan tools)", "hint": "Use concept_search, semantic_search, or search_code ...", "max_full_scan": 50000}` — PASS: cluster-scoped guard + hint.

## Results (fixture 35 elements)
- `detect-clusters` → 18+ clusters assigned, "Cluster assignments saved to the database" — PASS.
- Web API `GET /api/graph/clusters` → cluster nodes (e.g. `cluster:/tmp/leankg-live-fixture/src/frontend` label `frontend (8)`) — PASS.

## Tracker
- GE cluster-first (#179): PASS. Mega-graph guarded (no full scan, ≤50000 max); small graph clusters computed + served. Note: workspace-be has no precomputed cluster_id rows — offline `detect-clusters` required for mega-graph cluster views (by design).
