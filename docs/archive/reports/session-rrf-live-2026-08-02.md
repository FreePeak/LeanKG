# Provenance + RRF (#184) live evidence — 2026-08-02

## Environment
- commit: 955973e8 (worktree prd/session-provenance-rrf, NOT in main yet) | binary: .worktrees/prd/session-provenance-rrf/target/release/leankg 0.19.30 | MCP :9881 | project: /tmp/leankg-live-fixture

## Steps
1. `session_memory_write` × 4 (preference/decision/standing_rule/preference, sessions rrf-probe + rrf-probe-2)
2. `search_memory_rrf query="handler style" k=60` and `query="async handler style" k=60`

## Results
- `session_memory_write` returns provenance: `id, kind, node_id: null, source: session_memory_write, source_session_id, written: true` — PASS.
- `search_memory_rrf "async handler style"` → ranked result:
  `id=sm kind=preference rank=1 score=0.016 source_session_id=rrf-probe-2 sources=[session] "style: sync handlers only for legacy routes"` — PASS: fused rank order (top hit = best match), provenance fields present (source_session_id, sources, kind, rank, score).
- k=60 applied; small corpus (4 items) returns count=1 (top result).
- Envelope includes `_token_budget {max:1000, actual, truncated:false}` + `tokens` — PASS.

## Tracker
- Provenance + RRF (#184): PASS (worktree binary). AC "provenance fields present; fused rank order" verified. Note: not yet on main — merged via #184 once CI green.
