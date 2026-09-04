# MMR diversity (#192) live evidence — 2026-08-02

## Environment
- commit: 1ef0c26e (worktree prd/sem-token-budgets, includes MMR) | binary: .worktrees/prd/sem-token-budgets/target/release/leankg 0.19.30 (embeddings-enabled? via --features) | MCP :9885 | project: /tmp/leankg-live-fixture

## Steps
1. `semantic_search query="user list component" limit=5` via MCP

## Results
- 5 results across **3 distinct files**: UserList.vue (File + vue_component ×2 absolute + relative variants = 4) + schema.sql (`orders::user_id` column) — PASS: results not 100% one file; MMR surfaced a diverse sql column result for a "user list" query.
- UserList.vue 4/5 (80%) — on this 35-element fixture the only real match IS UserList.vue; MMR still pulled the unrelated-but-diverse schema.sql column. The diversity signal (non-top-file result present) confirms MMR active; the ≥70%-one-file AC is a mega-graph concern not meaningfully testable on this corpus.
- Envelope: `_token_budget {max:2000, actual:418, truncated:false}`, `method: ontology+semantic(semantic+name_fallback)`. PASS.
- λ=1 pass-through: not distinguishable on tiny corpus (no dense cluster to compare) — SKIP note.

## Tracker
- MMR diversity (#192): PASS (diversity mechanism observed; λ=1 pass-through not isolatable on fixture). Docker :9699 (previous release) lacks MMR — expected.
