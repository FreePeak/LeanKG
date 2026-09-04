# Heat promote (#183) live evidence — 2026-08-02

## Environment
- commit: 234cb2da (worktree prd/session-heat-promote, NOT in main yet) | binary: .worktrees/prd/session-heat-promote/target/release/leankg 0.19.30

## Steps
1. Reviewed `session/mod.rs` heat implementation: `heat_score` (frequency log1p + recency exp-decay half-life 1d), `MemoryIndex` (JSON state + rendered markdown), `record_recall` hook, `refresh` (no-write-on-unchanged), `top_k` (deterministic sort).
2. `cargo test --release --lib heat` — 3 tests.

## Results
- `heat_promotion_recall_path_bumps_heat` — ok: recall same session bumps heat (frequency + recency). PASS (AC: recall N times → heat accumulates).
- `heat_promotion_top_k_truncates_and_orders` — ok: top-K deterministic order by heat score. PASS.
- `heat_promotion_markdown_is_white_box` — ok: MEMORY_INDEX.md rendered white-box; writes only `.leankg/` files, never ontology YAML (FR-SM-11). PASS.
- **3 passed, 0 failed**.

## Tracker
- Heat promote (#183): PASS via unit tests (3/3). Note: MemoryIndex is wired only in tests in this worktree — no CLI/MCP caller on the heat binary (session_recall handler doesn't call `record_recall` yet). Production wiring pending; feature test-verified. Also note: session_recall on the heat worktree binary lacks `session_memory_write` tool (that's the RRF worktree's); heat path requires refs.
