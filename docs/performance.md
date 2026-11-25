# Performance

## Measured results

The figures below come from the project's own A/B benchmark and are labelled
with their source. They were measured on the archived Rust-era vector-engine
gate and have not been re-run against the current Go engine for this
document. Treat them as directional evidence, not a current guarantee.

| Metric | Result | Source |
| --- | --- | --- |
| Input tokens | −65.0% | [`docs/archive/prd.md`](archive/prd.md) (100 tasks, A/B) |
| Tool calls | −84.6% | [`docs/archive/prd.md`](archive/prd.md) (100 tasks, A/B) |
| Speedup for agents | 2.50× | [`docs/archive/prd.md`](archive/prd.md) (100 tasks, A/B) |
| Cold time-to-first-value (Go) | 17.8 s with a cold build cache on the project's macOS ARM64 runner; CI budget 300 s | [`.github/workflows/ci.yml`](../.github/workflows/ci.yml), [`docs/prd.md`](prd.md) (warm 4.9 s, fully cold 21.6 s) |

The mechanism behind the first three rows is the same: an agent asks for a
targeted subgraph — callers, dependents, impact, tests, docs — instead of
running grep-and-read loops over whole files. Fewer bytes and fewer round
trips mean lower cost and lower latency. How the ladder produces that
subgraph is described in [Architecture](architecture.md).

## Reproducing the benchmark

The A/B harness lives in [`benchmark/ab`](../benchmark/ab) and is run with:

```bash
make go-bench
```

It contains seeded `testing.B` suites for cold indexing, the L1/L2 ladder and
graph traversal. Benchmarks are local or nightly tools and do not run in CI;
the `go-engine` CI job runs only the in-process test suite, with a 300 s
budget.

The cold time-to-first-value figure comes from the `ttfv` CI job, which
measures index plus first query on a clean cache.

## Archived analyses

- [Competitive analysis](archive/competitive-analysis.md)
- [ROI comparison](archive/reports/leankg-vs-graphify-company-roi-2026-07-21.md)
- [Research matrix](archive/analysis/leankg-competitive-research-and-improvement-strategy-2026-08-02.md)

## Reading the numbers

Two effects dominate cost in an agent session: the number of tool calls and
the amount of source text pulled into the context window. LeanKG reduces both
by returning a compressed, ranked subgraph and by letting the agent read one
element or one file slice instead of a directory. Exact and keyword matches
need no embedding round trip; semantic search adds one provider call for the
query vector, then a vector comparison in the store.

On a cold store, the first query pays indexing and embedding cost. Warm
stores with fresh vectors pay only the query path, which is why `leankg
status` reports `fresh`, `possibly_stale` or `cold` alongside every answer.
