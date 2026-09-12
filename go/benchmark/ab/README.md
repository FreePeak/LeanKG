# Go A/B Benchmark Harness (W7)

Go-side half of the Rust-vs-Go parity comparison: five `testing.B`
benchmarks measuring the primitive costs the A/B report needs. All
fixtures are generated from fixed seeds (PCG), so every run indexes and
queries byte-identical corpora.

## Running

From `go/`:

```sh
# full run, default benchtime (1s per benchmark)
go test ./benchmark/ab/ -bench=. -benchmem -run=^$

# quick smoke (acceptance gate: all green at 10 iterations)
go test ./benchmark/ab/ -bench=. -benchtime=10x -run=^$
```

Filter a single benchmark with `-bench=BenchmarkQueryL1Exact`.

| Benchmark | Measures | Fixture |
|---|---|---|
| `BenchmarkIndexDir100Files` | Cold `index.IndexDir`: regex extraction + batched element/relationship/file writes into a fresh SQLite store | 100 synthetic `.go` files (deterministic content) |
| `BenchmarkQueryL1Exact` | L1 rung: exact name/qualified-name SQL lookup (`FindExact`) | warm index over the same 100 files |
| `BenchmarkQueryL2Fuzzy` | L2 rung: tokenized FTS5 OR query + score re-rank (`FindFuzzy`) | warm index over the same 100 files |
| `BenchmarkSearchVectors1k` | L3 rung as shipped: in-process exact cosine, top-10, with `code_elements` hydration (`SearchVectors`) | 1,000 vectors x 384 dims |
| `BenchmarkMemoryFTSSearch` | Memory FTS5 search (per-line rows, unicode61 tokenizer) | 50 seeded memory files x 20 lines |

## Rust A/B comparison recipe

Capture the Rust numbers from the repo root (`benchmark/` at the top of
the checkout, not this directory):

1. **Cross-tool harness** — `benchmark/grep_vs_leankg/`
   (`baseline.json` = grep arm, `leankg.json` = Rust arm,
   `report.md` = prior A/B run) and `benchmark/results/` (prior
   `ab-test-*.json` / `ab-benchmark-*.md` runs). Re-run the Rust arm via
   that harness to refresh rather than quoting stale JSON.
2. **Already-measured headline numbers** (docs/go-rewrite-analysis.md
   §"Performance where it matters"; router warm figure also in
   docs/prd.md):
   - router latency: 2.31 s warm (14–31 s before the per-process
     embedder/rerank cache, PR #301)
   - HNSW top-50 ANN over 10k x 384: 4 ms, recall@50 = 1.0000
   - bulk embed COPY throughput: 7,695–9,579 vectors/s
   - TTFV (quickstart e2e smoke): 88 s vs the 300 s budget
3. **Go counterparts** — run this harness and read `ns/op` /
   `allocs/op`:
   - router rungs: `BenchmarkQueryL1Exact` + `BenchmarkQueryL2Fuzzy`
     (+ `BenchmarkSearchVectors1k` for the L3 leg) — these measure the
     primitive per-rung cost, not the full MCP round trip; say so when
     comparing against the 2.31 s router figure.
   - indexing: `BenchmarkIndexDir100Files` (ns/file = ns/op / 100).
   - vector scan: `BenchmarkSearchVectors1k` (scale linearly to 10k when
     comparing against the 4 ms HNSW number — see the honesty note).

## Reporting table template

```markdown
| Metric | Rust | Go | Ratio (Go/Rust) |
|---|---|---|---|
| Router, warm (L1 hit)            | 2.31 s   | <go rung ns/op sum>  | <ratio> |
| Top-50 vector query, 10k x 384   | 4 ms     | <SearchVectors1k ns/op x 10> | <ratio> |
| Bulk embed ingest                | 9.6k vec/s | <vectors/s from IndexDir/ UpsertVectors timing> | <ratio> |
| Cold index (per file)            | <rust>   | <IndexDir ns/op / 100> | <ratio> |
| L1 exact query                   | <rust>   | <QueryL1Exact ns/op>  | <ratio> |
| L2 fuzzy query                   | <rust>   | <QueryL2Fuzzy ns/op>  | <ratio> |
| Memory FTS search                | <rust>   | <MemoryFTSSearch ns/op> | <ratio> |
| TTFV quickstart                  | 88 s     | <go e2e smoke, not in this harness> | <ratio> |
```

Fill `<go ...>` cells from `-benchmem` runs; leave cells the harness does
not measure as "not measured (harness scope: primitive costs)" rather
than extrapolating.

## Makefile

Suggested target (integrator wires it into the root `Makefile`):

```make
go-bench:
	cd go && go test ./benchmark/ab/ -bench=. -benchmem -run=^$
```

## Honesty note: the vector scan is not ANN

`SearchVectors` (internal/store/vectors.go) performs an **in-process
brute-force cosine scan** — O(n) over every vector of the model per
query, documented in-code with its ceiling (~0.15 s at 100k x 384 on
M2 Pro; upgrade path: sqlite-vec, later PostgreSQL+pgvector via
`OpenBackend`). The Rust engine answers the same query with pgvector
HNSW (~4 ms top-50 at 10k x 384, sublinear). These numbers are therefore
**not** equivalent quantities: the benchmark makes the
sqlite-brute-force-vs-HNSW tradeoff measurable instead of hiding it.
When filling the table, compare at matched n (1k vs 1k), and label the
Go cell "exact cosine scan" — never "ANN".
