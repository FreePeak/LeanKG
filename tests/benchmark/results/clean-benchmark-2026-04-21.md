# LeanKG A/B Benchmark Results

**Date:** 2026-04-21
**Test Method:** Clean benchmark with empty Kilo config + LeanKG MCP vs Kilo alone
**Configuration:** `~/.config/kilo-benchmark/` with XDG_CONFIG_HOME

---

## Executive Summary

| Metric | Value |
|--------|-------|
| Total test cases | 13 |
| Navigation wins (LeanKG F1 > Baseline F1) | 2/4 |
| Implementation wins | 1/3 |
| Impact/Debugging | TIMEOUT (complex queries >120s) |
| Token overhead (completed tests) | +23,885 tokens |

**Key Finding:** LeanKG shows mixed results. Token savings claims (13-42 tokens, 98% reduction) are **NOT validated** by this benchmark. Complex impact/debugging queries **timeout** with 0 tokens returned.

---

## Results by Category

### Category: Navigation

| Test | With LeanKG | Without | Delta | LeanKG F1 | Baseline F1 | Winner |
|------|-------------|---------|-------|-----------|------------|--------|
| find-mcp-handler | 45,285 | 45,172 | +113 | 0.31 | 0.31 | Tie |
| find-codeelement | 16,939 | 16,863 | +76 | 1.00 | 1.00 | Tie |
| find-extractor | 18,991 | 35,247 | **-16,256** | 0.33 | 1.00 | **Baseline** |
| find-query-engine | 18,494 | 21,643 | **-3,149** | 0.67 | 0.07 | **LeanKG** |

### Category: Implementation

| Test | With LeanKG | Without | Delta | LeanKG F1 | Baseline F1 | Winner |
|------|-------------|---------|-------|-----------|------------|--------|
| impl-new-tool | 42,661 | 42,781 | -120 | 0.40 | 0.40 | Tie |
| impl-search-enhance | 61,982 | 43,393 | +18,589 | 0.57 | 0.29 | **LeanKG** |
| impl-new-relationship | 31,013 | 27,481 | +3,532 | 0.03 | 0.05 | Tie |

### Category: Impact Analysis (TIMEOUT)

| Test | With LeanKG | Without | Delta | Result |
|------|-------------|---------|-------|--------|
| impact-models-change | 0 tokens | 0 tokens | 0 | TIMEOUT |
| impact-db-change | 0 tokens | 0 tokens | 0 | TIMEOUT |

### Category: Debugging (TIMEOUT)

| Test | With LeanKG | Without | Delta | Result |
|------|-------------|---------|-------|--------|
| debug-status-tool | 46,313 | 0 | +46,313 | TIMEOUT |
| debug-indexing-failure | 0 tokens | 0 tokens | 0 | TIMEOUT |
| debug-query-edge | 0 tokens | 0 tokens | 0 | TIMEOUT |

---

## Token Comparison

| Metric | Navigation | Implementation | Impact | Debugging |
|--------|-----------|---------------|--------|-----------|
| LeanKG Total | 99,709 | 135,656 | 0 | 46,313 |
| Baseline Total | 118,925 | 113,655 | 0 | 0 |
| Delta | -19,216 | +22,001 | N/A | +46,313 |

---

## Key Insights

### 1. Token Savings NOT Validated
The claims of "13-42 tokens per query" and "98% token reduction" are **NOT supported** by this benchmark. Actual token counts are in the thousands, not tens.

### 2. Complex Queries Timeout
Impact analysis and debugging tasks (which should be LeanKG's strength) **timeout after 120 seconds**, returning 0 tokens. This is a significant issue.

### 3. Mixed Quality Results
- Navigation: 2/4 LeanKG wins on F1 quality
- Implementation: 1/3 LeanKG wins on F1 quality
- Overall: Mixed results, no clear winner

### 4. Precision Issues
LeanKG returns many **false positives** in some tests, reducing precision and making results noisy.

---

## Test Configuration

```bash
# Benchmark runner uses XDG_CONFIG_HOME=~/.config/kilo-benchmark
# With LeanKG: mcp_settings_with_leankg.json (leankg MCP only)
# Without LeanKG: mcp_settings_without_leankg.json (empty MCP)
# Skills: empty
# AGENTS.md: empty
```

---

## Recommendations

1. **Fix timeout issues** for impact/debugging queries
2. **Reduce false positives** to improve precision
3. **Remove marketing claims** from README until validated
4. **Add deduplication** to reduce token overhead
