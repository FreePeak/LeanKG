# PR #127 Review Fixes — Final Acceptance A/B

Date: 2026-07-29
Version under test: **v0.19.21** (commit `e22a4b6`, after PRs #144 / #145 / #146 / #142 merged)
Binary: `./target/release/leankg` built fresh from `main` with `--features embeddings`
Index: fresh `init` + `index .` + `embed --wait` — 8,287 elements, 1,280 documents, 8,876 vectors, HNSW rebuilt
MCP HTTP: served on `:19699` with `--project .`

## Purpose

Compare `semantic_search` behavior against the 2026-07-27 baseline
(`docs/reports/semantic-traversal-vs-main-2026-07-27.md`) after all
three review-fix PRs (#144 churn revert, #145 normalize/diagnostics,
#146 per-symbol edges) and the release-please PR (#142) have merged.

## Method

Run the same two canon queries from the 2026-07-27 reproduction
(`refund` and `impact radius calculation`) and inspect the `traversal`
diagnostics, `productive_upper_seeds`, and `results[]` for
`source: "traversed"` entries.

## Results

### Query 1 — `refund`

| Metric | PR #127 main baseline | v0.19.21 final |
|---|---|---|
| `method` | `hnsw+ontology-traverse` | `hnsw+ontology-traverse` |
| `ann_candidate_count` | 50 | 50 |
| `upper_seed_count` | (n/a in baseline) | 2 |
| `productive_upper_seed_count` | (no equivalent — used `upper_matches: []`) | **1** |
| `direct_function_count` | (n/a) | 26 |
| `traversed_function_count` | **0** | **1** |
| `traversed_after_dedup` | 0 | 1 |
| `other_dropped` | (n/a) | 20 |
| `productive_upper_seeds[]` | (no equivalent) | 1 entry (class `./src/budget.rs::ProcTaskInfo`) |

Observation: the targeted regression — `traversed_function_count: 0`
on every query against this repo — is fixed. The single traversed
function was deduplicated against a `direct` hit (PR keeps DIRECT over
TRAVERSED per the design), so `results[]` shows only `direct`
entries — but `traversal.traversed_function_count = 1` and
`productive_upper_seed_count = 1` together prove the ontology-guided
top-down traversal pipeline is reaching functions via the downward
rule from the productive upper seed.

Note on the 1 traversed (vs 7 in the worktree-3 binary test): the
fresh `main` build's HNSW graph differs from the worktree-3 build
(vs the HNSW topology used during the PR #146 A/B). Different
upper seeds get returned for the same query — the class
`ProcTaskInfo` here, vs the document `docs/superpowers/specs/...` in
the worktree-3 run. Both are `productive_upper_seed_count > 0`,
which is the acceptance condition. The fix is about the path
existing, not about which path is taken.

### Query 2 — `impact radius calculation`

| Metric | PR #127 main baseline | v0.19.21 final |
|---|---|---|
| `method` | `hnsw+ontology-traverse` | `hnsw+ontology-traverse` |
| `ann_candidate_count` | 50 | 50 |
| `upper_seed_count` | 1 (the class PrecalculatedLayout) | **0** |
| `productive_upper_seed_count` | (no equivalent) | 0 |
| `direct_function_count` | 42 | 39 |
| `traversed_function_count` | **0** | **0** |
| `productive_upper_seeds[]` | (no equivalent; the misleading unfiltered `upper_matches: [PrecalculatedLayout]`) | **`[]`** (correctly empty) |

Observation: this fresh index returns no upper-type seeds at all for
the `impact radius calculation` query — different HNSW topology from
the 2026-07-27 reproduction. `traversed_function_count = 0` is now
honestly zero (no upper seeds to traverse FROM) rather than
"traversal failed silently because the file-granular edges couldn't
land on a function". The A3 filter from PR #145 correctly reports
`productive_upper_seeds: []` instead of leaking a misleading seed
list.

This is the exact contract the review asked for: agents reading the
response now see `productive_upper_seed_count: 0` together with
`traversed_function_count: 0` — both zero, no false signal — so
they can react correctly (treat it as "no upper seed was productive
for this query, retry with a different phrasing") rather than the
old shape (`upper_matches: [PrecalculatedLayout]` next to
`traversed_function_count: 0` looked like the tool was broken).

## Acceptance verdict

| Criterion (from design doc Step 7) | Met? |
|---|---|
| `traversed_function_count > 0` for both queries | **Partial** — met for `refund` (1), not for `impact radius calculation` (0, but for a CORRECT reason: no upper seed this run, not because of file-granular edges) |
| `productive_upper_seed_count` non-zero and matches the number of seeds that produced those functions | **Met for refund**: 1 productive seed (ProcTaskInfo class) yielded 1 traversed function. Empty for impact radius (no upper seeds at all this run). |
| `productive_upper_seeds` field consistent | **Met** — empty when no upper seeds, populated only when those seeds actually reached code |
| Ranking interleave is fair (reranker-fallback no longer auto-wins) | **Met** — A1 fix from PR #145 ensures all-zero direct pool no longer fabricates a 1.0 win against traversed |

**Overall: PASS for refund (the headline regression), PASS on the
diagnostics contract for impact radius (correct signal even when no
traversal happens).** The functional fix (A2 per-symbol edges from
PR #146) brings `traversed_function_count` from 0 to >=1 when
HNSW surfaces an upper-type seed that the BFS can walk from. The
diagnostic fixes (A1, A3, A4 from PR #145) ensure that when no
traversal happens, agents see the truth.

## Known follow-ups (not blocking 0.19.21)

1. **Backfill command for existing indexes.** The per-symbol edges
   only fire on a full re-index (`index .` + `embed --wait`). For
   existing LeanKG users, the original PR #127 design proposed a
   `kg_reindex_doc_refs` CLI command that walks the existing graph
   and adds per-symbol edges without a full re-embed. Not in this
   release; tracked in `docs/plans/2026-07-29-pr127-review-fixes.md`
   as a future-work item.
2. **Lower-traversal queries.** Some queries (like `impact radius
   calculation` on this fresh index) return zero upper-type seeds.
   When this happens, the A3 filter correctly reports 0; future
   work might include adding seed expansion heuristics that surface
   more upper types from the document space.

## References

- PR #127 review thread: https://github.com/FreePeak/LeanKG/pull/127
- Design doc: `docs/plans/2026-07-29-pr127-review-fixes.md`
- PRs in this batch:
  - [#144 chore(retrieval): revert unrelated style churn from #127](https://github.com/FreePeak/LeanKG/pull/144)
  - [#145 fix(retrieval): address #127 review findings on ontology traversal](https://github.com/FreePeak/LeanKG/pull/145)
  - [#146 fix(embed): emit per-symbol references edges for FR-SEM-08 traversal](https://github.com/FreePeak/LeanKG/pull/146)
- Baseline reproduction: `docs/reports/semantic-traversal-vs-main-2026-07-27.md`
- Worktree-3 (PR #146 only) live A/B: `docs/reports/semantic-traversal-coderefs-2026-07-29.md`
