# Auto-recall (#176) live evidence — 2026-08-02

## Environment
- commit: 8c77b22b | binary: target/release/leankg 0.19.31 (main) | MCP :9878 | project: /tmp/leankg-live-fixture

## Steps
1. `session_memory_write` × 5 (via RRF worktree server sharing the same recall_index.jsonl)
2. `get_overview_context` (default) and with `recall: true`
3. Reviewed `DEFAULT_RECALL_ENABLED` (session/mod.rs:28)

## Results
- Default (`recall` omitted): `DEFAULT_RECALL_ENABLED=false` — no `session_lessons` injected. PASS (opt-in default off).
- `recall: true`: `session_lessons` injected with 5 lessons (top-K=5, char-budget bounded, timeout-guarded): `GC probe memory`, `prefer async/await style`, `decision: gateway uses RS256 JWT`, `handlers should use async/await`, `style: sync handlers` — PASS (lessons injected; ≤5s timeout via `DEFAULT_RECALL_TIMEOUT_SECS`).
- Envelope: `l0_identity`, `l1_critical_facts`, `wake_up`, `session_lessons` all present.

## Tracker
- Auto-recall (#176): PASS. Default off, opt-in via `recall=true`; lessons loaded from `sessions/recall_index.jsonl` via `RecallStore`.
