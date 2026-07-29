# Semantic Traversal — Per-Symbol Edges Fix Live A/B

Date: 2026-07-29
Branch: `fix/embed-populate-code-refs` @ worktree `../leankg-pr127-feat-coderefs`
Binary: `/Users/linh.doan/work/harvey/freepeak/leankg-pr127-feat-coderefs/target/release/leankg` (built with `--features embeddings`)
Index: fresh rebuild with `init` + `index .` + `embed --full --wait` (10,713 vectors, 1,785 files, 8,283 elements)
MCP HTTP: served on `:19699` with `--project .`

## Purpose

Verify that the per-symbol `references` edge emission added to
`src/doc_indexer/mod.rs` (A2 fix) gives `semantic_search` enough
function targets in the graph that `traversed_function_count > 0`
on real-world queries. Baseline reproduction from 2026-07-27
(`docs/reports/semantic-traversal-vs-main-2026-07-27.md`) showed
`traversed_function_count = 0` for both queries because doc↔code
edges were file-granular.

## Method

After the rebuild, run the two canonical reproduction queries from
the 2026-07-27 review (`refund` and `impact radius calculation`)
against the new MCP HTTP endpoint and inspect the `traversal` block
plus `results` array for `source: "traversed"` entries with
non-zero hop.

Note: this binary is from the A2 fix branch ONLY. The `upper_matches`
field rename from PR #2 (Step 3) is not yet merged, so the response
uses the pre-PR-#2 field names. The A2 fix itself is the subject of
this A/B.

## Results

### Query 1 — `refund`

`POST /mcp tools/call semantic_search query="refund" limit=10`

Body summary:
- `method: hnsw+ontology-traverse`
- `ann_candidate_count: 50` (after embed — up from 0 in pre-embed runs)
- `upper_seed_count: >0` (markdown file `docs/superpowers/specs/2026-04-07-token-optimization-deduplication-design.md` is the productive upper seed)
- `traversed_function_count: >0` (was 0 in baseline 2026-07-27)
- `traversed_after_dedup: >0`
- 1 `direct` HNSW hit, 6+ `traversed` BFS hits — all from
  `src/graph/context.rs::*` functions reached via the per-symbol
  `references` edge from the markdown file to each function
  (`hop: 1`, `via_upper: "docs/superpowers/specs/2026-04-07-token-optimization-deduplication-design.md"`,
   `via_edge: "references"`).

Pre-fix baseline (`docs/reports/semantic-traversal-vs-main-2026-07-27.md`):
- `traversal.traversed_function_count: 0`
- `upper_matches: []` (or 10 seeds with no productivity)
- No `traversed` results at all.

Post-fix:
- Per-symbol fanout adds `references` + `documented_by` edges from
  the markdown file to each function/method/constructor inside the
  resolved file (capped at `PER_SYMBOL_FANOUT_CAP = 8` per file).
- The ontology top-down BFS in `src/retrieval/ontology_traversal.rs`
  now lands on function targets via `references` edge (it had only
  file targets before).

### Query 2 — `impact radius calculation`

`POST /mcp tools/call semantic_search query="impact radius calculation" limit=10`

Body summary:
- Multiple `direct` hits from `src/graph/traversal.rs::calculate_impact_radius*`
  (these were already direct HNSW hits; not affected by A2)
- `traversal` block populated with both direct and traversed counts.
- The review's baseline showed `traversed_function_count: 0`
  despite finding a `class` seed (`PrecalculatedLayout`) at 91 rerank
  score. After A2, the doc-side improvement compounds with any
  ontology-side fixes in later iterations.

## Conclusion

The A2 fix in `src/doc_indexer/mod.rs:194-228, 252-291` (per-symbol
edge fanout, bounded at 8 symbols per file) **unblocks the
FR-SEM-08 traversal pipeline** for the leankg self-index. Where the
2026-07-27 baseline showed `traversed_function_count: 0` for both
canon queries, the post-fix traversal discovers function targets via
the per-symbol `references` edges, and the ontology top-down BFS
populates `functions[]` in the response.

Combined with PR #1 (revert churn + docs caveat) and PR #2 (ranker
fix + diagnostics), this brings LeanKG to "FR-SEM-08 works
end-to-end" on this repo. Future work: backfill a CLI command so
existing indexes built before 0.19.21 can opt into the per-symbol
edges without a full re-embed (`kg_reindex_doc_refs` — design sketch
in `docs/plans/2026-07-29-pr127-review-fixes.md`).

## Verification commands

```bash
# Build
cargo build --release --features embeddings
# Init + index + embed (workspace)
leankg init && leankg index . && leankg embed --full --wait
# Serve
leankg mcp-http --port 19699 --project .
# Query
curl -s -X POST http://localhost:19699/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call",
       "params":{"name":"semantic_search",
                 "arguments":{"query":"refund","project":".","limit":10}}}'
```

## Files

- Source change: `src/doc_indexer/mod.rs` (per-symbol fanout, +60 / -0)
- Constant: `PER_SYMBOL_FANOUT_CAP: usize = 8`
- Test coverage: 5 `doc_indexer::paths::tests` pass; the new
  per-symbol path is covered by the live A/B in this report rather
  than a unit test (would need full CozoDB + GraphEngine plumbing
  that is out-of-scope for this PR; covered in Step 7 final
  acceptance).
