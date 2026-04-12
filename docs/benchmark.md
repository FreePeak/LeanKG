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
