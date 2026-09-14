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

## What this harness does and does not answer

It measures **primitive costs in-process** (`ns/op`, `allocs/op`), not the
MCP round trip. When a report needs an end-to-end figure, it must come from
`scripts/ttfv_smoke.sh` (cold happy path, CI-gated) or
`benchmarks/cross_tool/` (multi-tool, token/quality arms) — never by summing
these numbers. Cells the harness cannot measure stay "not measured
(harness scope: primitive costs)" rather than extrapolated.

The Rust comparison columns died with the Rust tree (b83206a1); the
superseded cross-tool recipe and its table template live on in
`docs/archive/`.

## Makefile

The root `Makefile` wires this in; no separate target is needed:

```make
go-bench:
	cd go && go test ./benchmark/ab/ -bench=. -benchmem -run=^$
```

## Honesty note: the vector scan is not ANN

`SearchVectors` (internal/store/vectors.go) performs an **in-process
brute-force cosine scan** — O(n) over every vector of the model per
query, documented in-code with its ceiling (~0.15 s at 100k x 384 on
M2 Pro; upgrade path: sqlite-vec, later PostgreSQL+pgvector via
`OpenBackend`). The PostgreSQL backend answers the same query with pgvector
HNSW (sublinear; ~4 ms top-50 at 10k x 384 measured on the pre-cutover
engine). These numbers are therefore **not** equivalent quantities: the
benchmark makes the sqlite-brute-force-vs-HNSW tradeoff measurable instead
of hiding it. When reporting, compare at matched n (1k vs 1k) and label the
sqlite cell "exact cosine scan" — never "ANN".

## A/B record + score harness (FR-ZCP-08)

`harness.go` + `abrun/` (CLI) harden the cross-tool runners
(`run_kilo_ab_final.sh`, `run_kilo_ab_test.sh`, shared
`scripts/kilo_ab_common.sh`):

- **Pinned provenance** — every trial row carries the 40-hex corpus
  commit, per-tool commit SHAs, and the 64-hex prompt-template hash;
  `abrun record` refuses unpinned rows (the run aborts, nothing is
  recorded).
- **>=3 trials/arm** — `MinTrialsPerArm = 3`; `Aggregate` fails any arm
  (overall or per task) below the floor; arm figures are
  median-of-per-task-medians.
- **Judge-blind scorer** — `BlindGroups`/`JudgePrompt` build the judge
  view from question + shuffled anonymized answers only; arm identity
  is structurally unexpressible. `JudgeBlind` unblinds only at report
  time. `JudgeFunc` is a scripted seam — tests stub it, the CLI execs
  any `-judge` command reading the prompt on stdin and answering a
  JSON `{"A": n, ...}` of 0-6 rubric totals.
- **zg pitfalls checklist** — `Checklist` computes trials_min3,
  prompt_identical, corpus_pinned, model_uniform, pins_recorded,
  tool_access_smoke, no_leakage from run metadata; the results JSON
  carries provenance + per-arm prompt hashes + medians + checklist.

```sh
# runner flow (see scripts/kilo_ab_common.sh):
ab_resolve_pins <ver> <baseline-tmpl> <leankg-tmpl>   # or ABORT
ab_record_trial runs.jsonl <arm> <task> <n> <tokens> <question> <answer-file> <raw-out>
ab_score runs.jsonl report.json                       # fails below the trial floor
```
