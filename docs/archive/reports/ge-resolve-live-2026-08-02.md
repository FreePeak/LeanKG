# GE entity resolve (#177) live evidence — 2026-08-02

## Environment
- commit: 8c77b22b | binary: target/release/leankg 0.19.31 (local) | library: `src/graph/entity_resolve.rs` (resolve_alias)

## Steps
1. `cargo test --release --lib entity_resolve` — 11 tests
2. Reviewed `resolve_alias` (src/graph/query.rs:6022) → `entity_resolve::resolve` (src/graph/entity_resolve.rs:128)

## Results
- `ambiguous_alias_returns_ranked_list_not_silent_pick` — ok: ambiguous alias returns ranked list, no silent pick. PASS (AC core).
- `resolve_exact_returns_none_for_fuzzy_only` — ok: exact-first, fuzzy-only → None (no false pick). PASS.
- `resolve_is_deterministic` / `type_rank_breaks_collisions_deterministically` — ok: deterministic ranking. PASS.
- `case_insensitive_fallback_before_prefix` / `slash_qualified_alias_matches_basename` / `unknown_alias_returns_empty` — ok: fallback + basename + empty for unknown. PASS.
- **11 passed, 0 failed** (out of 794 lib tests).

## Tracker
- GE entity resolve (#177): PASS via unit tests (11/11). Note: `resolve_alias` is library-internal (no CLI/MCP surface yet); CLI `lsp-resolve` covers LSP-based resolution separately (probe #23).
