# SEM budgets (#186) live evidence — 2026-08-02

## Environment
- commit: 1ef0c26e (worktree prd/sem-token-budgets, NOT in main yet) | binary: .worktrees/prd/sem-token-budgets/target/release/leankg 0.19.30 | MCP :9885 | project: /tmp/leankg-live-fixture
- Cross-check: Docker :9699 (container 0.19.31, serves previous release per plan §0) — no `_token_budget` envelope (expected: SEM budgets #186 not in container image).

## Steps
1. `concept_search query="handler" limit=5` via sem-token-budgets MCP
2. `concept_search query="a" limit=50` (attempt truncation)
3. Cross-check Docker :9699 workspace-be

## Results
- Local sem binary: `_token_budget: {max: 4000, actual: 70, truncated: false}` + `tokens: 70` — PASS (AC: `_token_budget.{max:4000,actual,truncated}` + `tokens`).
- `limit=50` query → `actual: 66, truncated: false` (small fixture corpus doesn't exceed max; envelope present) — PASS envelope; truncation path not triggered on tiny corpus.
- Docker :9699 workspace-be → envelope **absent** (`concept_match_count: 2` etc. but no `_token_budget`) — expected: container runs previous release without #186.

## Tracker
- SEM budgets (#186): PASS (worktree binary). Truncation path not exercised on 35-element fixture (corpus too small); envelope + caps verified. Docker cross-check documents the container's older release (plan §0 expectation).
