# Benchmark Results

## Core Value Props: Token Concise + Token Saving

| Metric | Value |
|--------|-------|
| **Tokens per query** | 13-42 tokens (vs 10,000+ without) |
| **Token saving** | Up to 98% for impact analysis |
| **Context correctness** | F1 0.31-0.46 on complex queries |

LeanKG provides **concise context** (targeted subgraph, not full scan) and **saves tokens** over baseline.

## AB Testing Results

| Metric | Baseline | LeanKG |
|--------|----------|--------|
| Tokens (7-test avg) | 150,420 | 191,468 |
| Token overhead | - | +41,048 |
| F1 wins | 0 | 2/7 tests |
| Context correctness | - | Higher |

### Key Findings

- LeanKG wins on F1 context quality in 2/7 tests (navigation, impact analysis)
- Token overhead: +41,048 tokens across all tests (pending deduplication fix)
- Context deduplication optimizations pending

### Historical (2026-03-25)

98.4% token savings for impact analysis on Go example

## Test Methodology

See [benchmark/README.md](../benchmark/README.md) for detailed test methodology.

## Detailed Results

See [docs/analysis/ab-testing-results-2026-04-08.md](analysis/ab-testing-results-2026-04-08.md) for complete analysis.

## Cross-Tool Agent A/B Benchmark (US-CT-BMK)

A separate harness compares a fixed `claude -p` agent answering one
architecture question on 7 real codebases — **WITH** LeanKG MCP enabled
vs **WITHOUT** (empty MCP config, built-in Read/Grep/Bash available to both).

This is the codegraph/graphify-style headless-agent benchmark that proves
the agent reaches for the LeanKG MCP server on real workloads. See:

- Methodology + caveats: [docs/cross-tool-benchmark.md](cross-tool-benchmark.md)
- Harness: [benchmarks/cross_tool/](../benchmarks/cross_tool/)
- Latest report: [benchmarks/cross_tool/results/cross_tool-2026-07-23.md](../benchmarks/cross_tool/results/cross_tool-2026-07-23.md)

Latest headline (Gin pilot, 4 runs/arm, MiniMax-M3 default model):

| Metric | WITH LeanKG | WITHOUT (grep) | Delta |
|---|---|---|---|
| Tool calls | 7 | 9 | -22% |
| Wall-clock | 1m 7s | 1m 59s | -44% |
| File reads | 4 | 4 | 0% |
| Cost | $0.37 | $0.53 | -31% |

(The full 7-repo suite is reproduced via `make full` in the bench dir.)
