# Layout3d (#171) live evidence — 2026-08-02

## Environment
- commit: 8c77b22b | binary: target/release/leankg 0.19.31 (local) | server: :9876 (web) | project: /tmp/leankg-live-fixture (34 elements, 49 rels)

## Steps
1. `curl 'http://localhost:9876/api/graph/layout3d?seed=42'` twice → `/tmp/l1.json`, `/tmp/l2.json`
2. `curl 'http://localhost:9876/api/graph/layout3d?seed=7'` → `/tmp/l7.json`
3. Python compare: node lists, bounds, finiteness.

## Results
- seed=42 run1 == seed=42 run2: **identical** (34 nodes, same x/y/z) — PASS deterministic.
- Bounds: all x/y/z within [0,1] — PASS unit-cube.
- Finite: all |v| < 1e9 — PASS.
- seed=7 differs from seed=42 — PASS seed sensitivity (layout varies by seed).

## Tracker
- Layout3d (#171): PASS. Note: layout is computed by `crate::graph::layout3d::layout3d` (seeded, deterministic); embedded ui-v2 renders Sigma 2D graph, the 3D positions feed the projection API.
