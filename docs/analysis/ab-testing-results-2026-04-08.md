# LeanKG AB Testing Results

**Date:** 2026-04-08
**Status:** TESTING COMPLETE
**Test Method:** End-to-end benchmark with Kilo and OpenCode CLI tools

---

## Executive Summary

| Metric | Value |
|--------|-------|
| Total test cases | 7 |
| Token overhead (LeanKG vs baseline) | +41,048 tokens |
| Token savings tests | 0/7 |
| F1 Quality wins (LeanKG) | 2/7 |
| F1 Quality wins (Baseline) | 0/7 |
| Ties | 5/7 |
| Unit tests | 14/14 passed |

**Key Finding:** LeanKG provides **better context correctness** (higher F1 scores) but at a **token overhead**. The deduplication optimizations from the 2026-04-07 spec may not be fully deployed.

---

## Test Results by Category

### Category: Navigation

| Test | With LeanKG | Without | Delta | Token Saved? | LeanKG F1 | Baseline F1 | Winner |
|------|-------------|---------|-------|--------------|-----------|------------|--------|
| find-mcp-handler | 41,316 | 18,731 | +22,585 | NO | 0.31 | 0.21 | **LeanKG** |
| find-codeelement | 16,696 | 16,689 | +7 | NO | 0.00 | 0.00 | Tie |
| find-extractor | 30,890 | 30,821 | +69 | NO | 0.14 | 0.14 | Tie |

### Category: Implementation

| Test | With LeanKG | Without | Delta | Token Saved? | LeanKG F1 | Baseline F1 | Winner |
|------|-------------|---------|-------|--------------|-----------|------------|--------|
| impl-new-tool | 22,229 | 22,020 | +209 | NO | 0.67 | 0.67 | Tie |

### Category: Impact

| Test | With LeanKG | Without | Delta | Token Saved? | LeanKG F1 | Baseline F1 | Winner |
|------|-------------|---------|-------|--------------|-----------|------------|--------|
| impact-models-change | 21,616 | 16,712 | +4,904 | NO | 0.46 | 0.00 | **LeanKG** |
| impact-db-change | 22,609 | 18,350 | +4,259 | NO | 0.00 | 0.00 | Tie |
| impact-handler-change | 36,112 | 27,097 | +9,015 | NO | 0.00 | 0.00 | Tie |

---

## Key Insights

### 1. Context Quality vs Token Trade-off

LeanKG wins on **F1 context correctness** in 2/7 tests:
- `find-mcp-handler`: LeanKG F1=0.31 vs Baseline F1=0.21
- `impact-models-change`: LeanKG F1=0.46 vs Baseline F1=0.00 (LeanKG found all 3 correct files, Baseline found 0)

However, this comes at a **token cost**:
- LeanKG uses **+41,048** more tokens than baseline across all tests
- No test showed token savings

### 2. Precision Issues

The `find-mcp-handler` test shows LeanKG returns many **false positives**:
```
Incorrect (false positives): ["src/mcp/tools.rs", "src/mcp_tools.rs", "src/mcp/watcher.rs", 
"src/auth.rs", "src/main.rs", "tests/mcp_tests.rs", "src/mcp/mod.rs", "src/mcp/auth.rs"]
```

This suggests the **deduplication fixes from 2026-04-07 spec** are not fully applied.

### 3. Testing Limitations

- **Kilo timeouts:** Complex queries (debugging, impact) timeout after 45-60 seconds
- **OpenCode context:** OpenCode doesn't expose stdout for file path parsing, so context quality is `(not available)`

---

## Root Cause Analysis

The token overhead suggests issues with:

1. **Context deduplication** - Same elements returned multiple times
2. **Over-fetching** - Returning too many files instead of minimal relevant set
3. **Missing RTK compression** - Context not being compressed before return

The 2026-04-07 spec for deduplication fixes is **not yet applied**.

---

## Recommendations

1. **Apply deduplication fix** - Implement HashSet-based deduplication in `traversal.rs` and `context.rs`
2. **Optimize context size** - Ensure only minimal relevant files are returned
3. **Apply RTK compression** - Use RTK-style compression for MCP responses
4. **Re-test after fixes** - Run benchmarks again to verify token savings

---

## Unit Test Results

```
Running tests/benchmark_context_parser_tests.rs
tests::test_quality_metrics_empty_expected ... ok
tests::test_quality_metrics_empty_actual ... ok
tests::test_quality_metrics_no_match ... ok
tests::test_quality_metrics_partial_match ... ok
tests::test_quality_metrics_perfect_match ... ok
tests::test_quality_metrics_verdict_excellent ... ok
tests::test_quality_metrics_verdict_good ... ok
tests::test_quality_metrics_verdict_moderate ... ok
tests::test_quality_metrics_verdict_poor ... ok
tests::test_context_parser_extracts_src_paths ... ok
tests::test_context_parser_deduplicates ... ok
tests::test_context_parser_handles_nested_paths ... ok
tests::test_context_parser_handles_tests_paths ... ok
tests::test_context_parser_handles_multiple_paths ... ok

test result: ok. 14 passed; 0 failed
```

---

## Files Changed During Testing

| File | Change |
|------|--------|
| `benchmark/results/*-comparison.json` | Updated with new test results |
| `benchmark/results/*-comparison.md` | Updated with new test results |

---

## Next Steps

1. [ ] Apply deduplication fix from `docs/superpowers/specs/2026-04-07-token-optimization-deduplication-design.md`
2. [ ] Verify deduplication with unit tests
3. [ ] Re-run AB benchmarks
4. [ ] Update README with corrected metrics

---

**Status:** PENDING FIXES - The context correctness (F1) is working, but token efficiency needs improvement.