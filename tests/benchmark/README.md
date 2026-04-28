# LeanKG Benchmark Testing

End-to-end testing framework for LeanKG MCP tools via Kilo AI agent.

## Latest Results (2026-04-21) - Clean Benchmark

| Category | Tests | LeanKG F1 Wins | Token Delta |
|----------|-------|----------------|-------------|
| Navigation | 4 | 2 | -19,216 |
| Implementation | 3 | 1 | +22,001 |
| Impact | 3 | TIMEOUT | N/A |
| Debugging | 3 | TIMEOUT | N/A |

**Total:** 13 tests, LeanKG wins F1 on 3 tests, +23,885 token overhead (completed tests)

See [results/clean-benchmark-2026-04-21.md](results/clean-benchmark-2026-04-21.md) for full analysis.

### Key Findings
- **Token savings claims NOT validated**: "13-42 tokens" and "98% reduction" are marketing, not benchmark data
- **Complex queries timeout**: Impact and debugging tasks timeout at 120s, returning 0 tokens
- **Mixed quality results**: LeanKG wins on F1 in 3/10 completed tests

## Structure

```
tests/benchmark/
├── Makefile              # make benchmark commands
├── src/
│   ├── lib.rs           # Module exports
│   ├── mcp_tools.rs     # MCP tool unit tests
│   └── token_tracker.rs # Token tracking utilities
├── prompts/
│   └── queries.yaml     # Test queries with expected outcomes
├── scripts/
│   ├── run_benchmark.sh
│   ├── extract_tokens.py
│   └── compare_results.py
└── results/             # Generated results
```

## Commands

```bash
# Run all benchmarks
make -f tests/benchmark/Makefile benchmark

# Run MCP tool unit tests only
make -f tests/benchmark/Makefile benchmark-mcp

# Run E2E tests (manual Kilo interaction)
make -f tests/benchmark/Makefile benchmark-e2e

# Generate comparison report
make -f tests/benchmark/Makefile benchmark-ab

# Clean results
make -f tests/benchmark/Makefile benchmark-clean
```

## Kilo E2E Testing

1. Ensure LeanKG is indexed: `cargo run -- index ./src`
2. Start Kilo: `kilo`
3. Run queries and export sessions: `kilo export <session_id> > results/<query_id>.json`
4. Compare results: `python3 scripts/compare_results.py results/`

## Metrics

- Token savings (LeanKG vs baseline grep)
- Correctness (100% match to expected files/concepts)
- Tool invocation verification
