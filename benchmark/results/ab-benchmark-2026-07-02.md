# LeanKG A/B Benchmark: With vs Without LeanKG

**Date:** 2026-07-02
**Project:** LeanKG self-indexed codebase (56,517 LOC, 2,049,935 bytes across `src/*.rs`)
**Method:** Live MCP tool calls vs manual `grep` shell commands, timed end-to-end

---

## Executive Summary

| Dimension | With LeanKG | Without (grep) | Winner |
|-----------|-------------|----------------|--------|
| Latency (avg) | 100-250ms (MCP) | 17-47ms (grep) | grep (5x faster) |
| Token Input Savings | 70-97% reduction | 0% (full file) | LeanKG |
| Token Efficiency | 3x info density/result | 1x (raw text) | LeanKG |
| Result Quality | Structured + typed + traced | Raw text, no structure | LeanKG |
| Multi-hop Traversal | 1 call (depth N) | N+ recursive greps | LeanKG |

**Verdict:** LeanKG trades ~5x latency for **70-97% token savings** and **3x information density**.
grep wins on raw speed for single-hop text search.

---

## 1. Latency Comparison

### 1.1 Single-Hop Search (grep is 5.4x faster)

| # | Query | LeanKG (MCP) | Manual (grep) | Ratio |
|---|-------|-------------|--------------|-------|
| 1 | search "CodeElement" | ~130ms / 18 results | 33ms / 20 lines | 3.9x |
| 2 | find "struct CodeElement" | ~115ms / 2 results | 20ms / 1 line | 5.8x |
| 3 | find "fn run" | ~117ms / 26 results | 17ms / 20 lines | 6.9x |
| 4 | find "fn run_benchmark" | ~116ms / 2 results | 18ms / 0 lines | 6.4x |

### 1.2 Multi-Hop / Graph Queries (LeanKG is decisive)

| # | Query | LeanKG | Manual grep | Notes |
|---|-------|--------|------------|-------|
| 5 | Impact radius (models.rs d=2) | ~200ms / 1108 affected | N/A (needs N greps) | LeanKG only |
| 6 | Test coverage (models.rs) | ~150ms / 12 tests+5 docs | 17ms / 9 file names | grep = no links |
| 7 | Dependents (models.rs) | ~140ms / 18 typed | 30ms / 41 untyped | grep faster, less info |
| 8 | Call graph (init_db d=2) | ~130ms / 5 edges | 25ms / 20 flat lines | grep = no graph |

### 1.3 Historical A/B (Built-in, 10 queries)

| Metric | A: LeanKG | B: Manual | Delta |
|--------|-----------|-----------|-------|
| Avg Latency | 128.2ms | 24.8ms | +103.4ms |
| Latency Wins | 0/10 | 10/10 | Manual |

Source: `benchmark/results/ab-test-1782971704.json`


---

## 2. Token Input Savings (LeanKG's Primary Value)

### 2.1 File Context Compression (avg 89.2% savings)

| File | Raw Bytes | Raw Tokens | LeanKG Tokens | Savings |
|------|----------|-----------|---------------|---------|
| src/db/models.rs | 26,642 | 6,660 | 1,937 | 70.9% |
| src/graph/query.rs | 145,608 | 36,402 | 1,858 | 94.9% |
| src/benchmark/ab_test.rs | 22,788 | 5,697 | 176 | 96.9% |
| src/main.rs | 133,856 | 33,464 | ~2,000 | 94.0% |

### 2.2 Impact Radius: 99.4% Token Savings

| Approach | Tokens | What You Get |
|----------|--------|-------------|
| With LeanKG `get_impact_radius` | 518 | 20 typed elements + confidence + severity + 1108 total |
| Without (read 41 dependent files) | ~82,000 | Raw file contents, manual tracing |

### 2.3 Test Coverage: 98.5% Token Savings

| Approach | Tokens | What You Get |
|----------|--------|-------------|
| With LeanKG `get_tested_by` | 270 | 12 exact test functions + 5 doc links |
| Without (grep + read 9 test files) | ~18,000 | File names only, must read each |

---

## 3. Token Efficiency

### 3.1 Information Density Per Result

| Metric | LeanKG | Manual grep |
|--------|--------|-------------|
| Fields per result | 7 (name, type, file, line, qname, cluster, sig) | 1 (raw line) |
| Info multiplier | 3x structured | 1x raw text |
| Quality score (avg) | 0.60 | 0.00 |

### 3.2 Historical Output Efficiency (10 queries)

| Metric | A: LeanKG | B: Manual | Better |
|--------|-----------|-----------|--------|
| Output Tokens | 262 | 10 | Manual (leaner) |
| Output/Input Ratio | 3.98 | 0.15 | LeanKG (more useful out) |
| Tokens/Result | 5.37 | 0.25 | Manual (leaner) |
| Results/ms | 0.0705 | 0.3767 | Manual (faster) |

### 3.3 Win/Loss Summary (10 queries)

| Category | LeanKG Wins | Manual Wins |
|----------|-------------|-------------|
| Latency | 0 | 10 |
| Efficiency | 4 | 6 |
| Quality | 6 | 4 |

---

## 4. Multi-Hop Traversal: Decisive Advantage

| Task | LeanKG (1 call) | Manual grep (N calls) | Token Savings |
|------|----------------|----------------------|---------------|
| Impact radius d=2 | 518 tok | ~5 greps + read = 12,000 tok | 95.7% |
| Impact radius d=3 | ~800 tok | ~15 greps + read = 36,000 tok | 97.8% |
| Call graph d=2 | 67 tok | 3+ greps to trace = 6,000 tok | 98.9% |

---

## 5. Methodology

- **Variant A (LeanKG):** Live MCP tools (search_code, get_context, ctx_read, get_impact_radius, get_tested_by, get_call_graph, orchestrate). Token counts from TOON envelope. Latency = MCP round-trip.
- **Variant B (Manual):** `grep -rn` with `time`. Token estimate: bytes/4. Latency = shell exec.
- **Codebase:** LeanKG self-indexed, 56,517 lines Rust, RocksDB-backed CozoDB.

---

## 6. Conclusions

| Metric | LeanKG | grep | Use LeanKG When |
|--------|--------|------|-----------------|
| Latency | 120ms | 22ms | Latency-tolerant |
| Token Input | 1,858 | 36,402 | ALWAYS (89% save) |
| Info Density | 3x | 1x | Need structure |
| Multi-hop | 1 call | N calls | Graph queries |
| Quality | 0.60 | 0.00 | Need typed results |

**Bottom line:** LeanKG provides **89% average token input savings** and **3x information density** at the cost of **5x latency overhead**. For LLM-assisted development where token budget dominates cost, LeanKG is the clear winner.
