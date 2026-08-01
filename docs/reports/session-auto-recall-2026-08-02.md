# US-SM-02 / FR-SM-04..06: Opt-in auto-recall into `get_overview_context` — smoke evidence

**Date:** 2026-08-02
**Tracker:** `US-SM-02` (→ DONE after merge) / `FR-SM-04` / `FR-SM-05` / `FR-SM-06` — closes `US-GE-05` / `FR-GE-05`
**PRD:** §3.28 US-SM-02 AC, §5.32 FR-SM-04..06

## Summary

`get_overview_context` opt-in (`recall=true`, default **OFF**) injects top-K ranked session lessons from a ranked lessons index at `.leankg/sessions/recall_index.jsonl`. Recall is timeout-bounded (≤5s), per-item (400 chars) and total (3000 chars) budgeted; on timeout / empty index / budgets exhausted injection is skipped — the overview response never blocks or fails because of recall.

## Implementation

- `src/session/mod.rs` — `Lesson`, `RecallStore` (dedup by content SHA-256 key before write, FR-SM-04), `recall_for_overview` (pure budgeted snapshot), `recall_for_overview_bounded` (worker-thread + `recv_timeout` ≤5s, FR-SM-06). Constants: `DEFAULT_RECALL_ENABLED=false` (FR-SM-05), `DEFAULT_RECALL_K=5`, `DEFAULT_RECALL_TIMEOUT_SECS=5`, `RECALL_ITEM_CHAR_BUDGET=400`, `RECALL_TOTAL_CHAR_BUDGET=3000`.
- `src/mcp/handler.rs` `get_overview_context` (thin arm) — reads `recall` flag, loads index, bounded-injects into `session_lessons` key.

## Test evidence (all gates green)

```bash
cargo fmt --all -- --check        # pass
cargo clippy --all -- -D warnings # pass
cargo test --lib                  # 778 passed, 0 failed
cargo test session                # 18 passed, 0 failed (5 new recall tests)
cargo test --test mcp_tools_redundancy_tests  # 50 passed, 0 failed (4 new MCP tests)
```

### Red phase (TDD) — failures before implementation

- `session_offload::overview_opt_in_on_injects_lessons_and_respects_budgets` — FAILED: `session_lessons` absent.
- `session_offload::recall_dedups_across_sources_and_top_k_respects_rank` — FAILED: dedup/top-K unenforced.

### Green phase — key tests

| Test | Assertion |
|------|-----------|
| `overview_opt_in_off_has_no_recall_key` | `recall` unset or `false` → response identical to today, no `session_lessons` key |
| `overview_opt_in_on_injects_lessons_and_respects_budgets` | `recall=true` → lessons injected, total ≤3000 chars |
| `overview_opt_in_on_with_empty_index_skips_injection` | no index → no `session_lessons` |
| `recall_dedups_across_sources_and_top_k_respects_rank` | same text from LESSONS.md + knowledge → 1 entry (first write wins); >K lessons → top-K only |
| `recall_for_overview_bounded_times_out_and_skips_injection` | 100k-lesson load with 10ms timeout returns within <5s (skips injection) |
| `recall_index_dedups_by_content_before_write` | duplicate text across sources not re-appended |

### Timeout bound (FR-SM-06)

`recall_for_overview_bounded` runs the snapshot on a worker thread and waits `recv_timeout(Duration::from_secs(5))`; timeout → `None` → no injection. Slow-recall test (`recall_for_overview_bounded_times_out_and_skips_injection`) asserts wall-clock < 5s with a 100k-lesson input under a 10ms timeout.

## Files changed

| File | Change |
|------|--------|
| `src/session/mod.rs` | +`Lesson` / `RecallStore` / `recall_for_overview[_bounded]` + 5 unit tests |
| `src/mcp/handler.rs` | `get_overview_context` opt-in recall arm (thin) |
| `tests/mcp_tools_redundancy_tests.rs` | 4 new MCP tests in `session_offload` module |

## Notes / deferred

- Index currently seeded only via the lib seam (`RecallStore`); wiring `report_query_outcome` / `agent_diary_write` / `add_knowledge` to append lessons automatically is **US-SM-03/04** (provenance + RRF) — out of scope here.
- Timeout uses a worker thread (no async runtime in handler path); acceptable for a 5s-bound.
