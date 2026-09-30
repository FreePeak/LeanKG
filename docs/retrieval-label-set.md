# Retrieval label set — the questions the fusion is measured against

Ground truth is the `qualified_name` that actually answers the question, read by
hand from the source. A label counts only if the element is in the graph, so
this file is also a check that the indexer found the thing.

Every question is phrased the way an agent phrases it, NOT as the identifier
alone: an agent that already had `Store.Space` would call `action=exact`. The
set exists to measure the L3 fusion, so a label must not be answerable at L1.

## Method

```
go build -o /tmp/leankg ./cmd/leankg
LEANKG_EMBED_PROVIDER=local LEANKG_EMBED_BASE_URL=http://127.0.0.1:9101/v1 \
LEANKG_EMBED_MODEL=bge-small-en-v1.5-f16.gguf LEANKG_EMBED_DIMS=384 \
  python3 scripts/retrieval-bench.py
```

Reports, per arm and fused: `in-pool` (the label appeared in that arm at all),
`top-1 / top-3 / top-10`. The fused number is what a user sees; the per-arm
numbers say WHICH arm to fix, which is the whole question when the fused score
is bad.

## Only extractable symbols are labelled

The indexer's Go extractor emits types, functions, methods and consts it can see;
unexported helpers, interface methods and named constants are **not**
`code_elements` rows, so a label pointing at one could never be retrieved by any
arm and would measure nothing but the indexer's extractor. Every label below was
checked against the live store with `select count(*) from code_elements where
qualified_name=...` — a label that returns 0 is replaced, not tolerated.

## Where the answers were

Each label is annotated with the file and line range, so a disputed label is
checkable rather than arguable.

## Labels

| # | question | ground-truth qualified_name | where |
|---|---|---|---|
| 1 | reciprocal rank fusion of ranked lists | `internal/store/pg_fts.go::FuseRRF` | pg_fts.go |
| 2 | the weighted variant of rank fusion | `internal/store/pg_fts.go::FuseRRFWeighted` | pg_fts.go |
| 3 | the Store constructor that opens sqlite | `internal/store/store.go::Open` | store.go |
| 4 | report how many bytes the freelist holds | `internal/store/maintenance.go::Store.Space` | maintenance.go |
| 5 | hand free pages back one page at a time | `internal/store/maintenance.go::Store.IncrementalVacuum` | maintenance.go |
| 6 | adopt incremental vacuum in the header | `internal/store/maintenance.go::Store.ensureIncrementalVacuum` | maintenance.go |
| 7 | resolve the configured sqlite store path | `internal/store/standalone.go::StandaloneDBPath` | standalone.go |
| 8 | list the files the indexer would take | `internal/index/walk.go::SupportedFiles` | walk.go |
| 9 | the vector rung of the retrieval ladder | `internal/core/core.go::Engine.rungSemantic` | core.go |
| 10 | freshness label from watermark and snapshot | `internal/store/backend.go::Freshness` | backend.go |
| 11 | find one element by exact identifier | `internal/store/elements.go::Store.FindExact` | elements.go |
| 12 | take the single-flight lock for embedding | `cmd/leankg-embed/main.go::lock` | main.go |
| 13 | the one tool that opens a project store | `internal/projects/projects.go::Config.open` | projects.go |
| 14 | resolve a short name to a qualified name for graph verbs | `internal/core/core.go::Engine.resolveGraphSeed` | core.go |
| 15 | which function the ladder routes before the keyword rung | `internal/core/core.go::Engine.rungSemantic` | core.go |
| 16 | how a batch that fails validation is handled | `internal/embed/provider.go::validateBatch` | provider.go |
| 17 | hash one element's content for the embed state | `internal/embed/provider.go::contentHashHex` | provider.go |
| 18 | compose the model stamp for a provider | `internal/embed/run.go::StampOf` | run.go |
| 19 | which arm does a weighted fusion read its weight from | `internal/store/pg_fts.go::FuseRRFWeights.armWeight` | pg_fts.go |
| 20 | the fraction of elements that hold vectors | `internal/embed/run.go::coverage` | run.go |
| 21 | where the memory search reads from | `internal/memory/search.go::ftsQuery` | search.go |
| 22 | rank a single arm's list of document keys | `internal/store/pg_fts.go::RankList` | pg_fts.go |
| 23 | which type is one fused result row | `internal/store/pg_fts.go::FusedHit` | pg_fts.go |
| 24 | cut a string to n runes for the sidecar budget | `internal/embed/run.go::truncateRunes` | run.go |
| 25 | the keyword search over the FTS index | `internal/store/elements.go::Store.FindFuzzy` | elements.go |
| 26 | the pressure plate that runs vacuum on a ticker | `internal/maintain/maintain.go::Pass` | maintain.go |
| 27 | which function reports a store's byte accounting | `internal/store/maintenance.go::Store.Space` | maintenance.go |
| 28 | the graph traversal that returns a blast radius | `internal/graph/graph.go::Impact` | graph.go |
| 29 | the fraction of dirty items a run embedded | `internal/embed/run.go::coverage` | run.go |
| 30 | truncate the text sent to a provider | `cmd/leankg/featureverbs.go::truncateRunes` | featureverbs.go |

## Interpreting a run

- **`in-pool 0` on both arms** is not a fusion problem. The element was never
  ranked by anybody; the fix is upstream (what text is embedded, or what the
  indexer extracted).
- **`in-pool` on one arm only, wrong rank** is a fusion problem, and the sweep
  in `scripts/retrieval-bench.py --sweep` measures whether any weight pair fixes
  it.
- **A tie at rank 1 across arms** is decided by arm order, not by meaning. See
  the v4.14.2 note in `docs/prd.md`: a same-arm-count tie implies an equal best
  rank, so there is no evidence-based tie-break available and the honest lever
  is a weight, not a sort.
