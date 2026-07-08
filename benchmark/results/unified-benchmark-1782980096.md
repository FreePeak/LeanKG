# LeanKG Unified A/B Benchmark Report

**Date:** 1782980096  
**Project:** `/Users/linh.doan/work/harvey/freepeak/leankg`  

## Codebase

| Metric | Value |
|--------|-------|
| Files (src/*.rs) | 112 |
| Lines | 57149 |
| Bytes | 2081873 |
| Indexed Elements | 16602 |
| Indexed Relationships | 45374 |

## Executive Summary

| Metric | With LeanKG | Without (grep) | Delta | Winner |
|--------|-------------|----------------|-------|--------|
| Avg Latency (ms) | 3786.3 | 25.2 | 3761.1 | Manual |
| Total Input Tokens | 210 | 300 | -90 | LeanKG |
| Total Output Tokens | 239 | 141 | 98 | Manual |
| Total Tokens | 449 | 441 | 8 | Manual |
| Avg Tokens/Result | 2.09 | 6.39 | -4.30 | LeanKG |

### Key Metrics

| Metric | Value |
|--------|-------|
| Input Token Savings | 30.0% |
| Output Token Overhead | 69.5% |
| Total Token Savings | -1.8% |
| Latency Overhead | 14938.8% |

## Results by Complexity

| Complexity | Cases | A Latency | B Latency | A Input | B Input | A Output | B Output | Input Savings | Latency Overhead |
|------------|-------|-----------|-----------|---------|---------|----------|----------|---------------|------------------|
| simple | 4 | 20.4ms | 20.2ms | 12 | 13 | 30 | 1 | 11.3% | 1.0% |
| medium | 7 | 29.3ms | 16.9ms | 11 | 17 | 5 | 1 | 33.9% | 74.0% |
| complex | 8 | 8956.6ms | 34.9ms | 11 | 16 | 10 | 16 | 34.1% | 25547.1% |

## Win/Loss Summary

| Category | LeanKG Wins | Manual Wins |
|----------|-------------|-------------|
| Latency | 5 | 14 |
| Tokens | 8 | 11 |
| Efficiency | 11 | 8 |

## Per-Case Results

| ID | Category | Complexity | Tool | Query | A ms | A in | A out | A res | B ms | B in | B out | B res | Lat W | Tok W | Eff W |
|----|----------|------------|------|-------|------|-------|-------|-------|------|-------|-------|-------|-------|-------|-------|
| S1 | search | simple | search_code | CodeElement | 25 | 11 | 7 | 17 | 16 | 14 | 1 | 44 | Manual | Manual | Manual |
| S2 | search | simple | search_code | GraphEngine | 26 | 11 | 7 | 17 | 17 | 14 | 1 | 21 | Manual | Manual | Manual |
| S3 | find | simple | find_function | run | 7 | 12 | 55 | 50 | 24 | 12 | 1 | 41 | LeanKG | Manual | Manual |
| S4 | find | simple | find_function | init_db | 24 | 13 | 50 | 10 | 25 | 13 | 1 | 4 | LeanKG | Manual | Manual |
| M1 | search_typed | medium | search_code_typed | function:search | 8 | 13 | 4 | 50 | 22 | 16 | 1 | 50 | LeanKG | Manual | Manual |
| M2 | search_typed | medium | search_code_typed | class:GraphEngine | 23 | 13 | 3 | 2 | 25 | 18 | 1 | 1 | LeanKG | LeanKG | LeanKG |
| M3 | context | medium | get_context | src/db/models.rs | 18 | 10 | 10 | 0 | 8 | 10 | 1 | 1 | Manual | Manual | LeanKG |
| M4 | context | medium | get_context | src/graph/query.rs | 18 | 10 | 10 | 0 | 8 | 11 | 1 | 1 | Manual | Manual | LeanKG |
| M5 | dependencies | medium | get_dependencies | src/db/models.rs | 68 | 9 | 3 | 0 | 8 | 13 | 1 | 2 | Manual | LeanKG | LeanKG |
| M6 | dependents | medium | get_dependents | src/db/models.rs | 71 | 8 | 3 | 19 | 27 | 15 | 1 | 1 | Manual | LeanKG | LeanKG |
| M7 | tested_by | medium | get_tested_by | src/db/models.rs | 0 | 15 | 5 | 19 | 21 | 35 | 1 | 0 | LeanKG | LeanKG | Manual |
| C1 | impact | complex | get_impact_radius | src/db/models.rs:2 | 32221 | 15 | 8 | 1114 | 35 | 14 | 1 | 42 | Manual | Manual | LeanKG |
| C2 | impact | complex | get_impact_radius | src/graph/query.rs:2 | 38078 | 16 | 8 | 928 | 39 | 14 | 1 | 8 | Manual | Manual | LeanKG |
| C3 | callgraph | complex | get_call_graph | init_db:2 | 287 | 10 | 5 | 5 | 30 | 12 | 1 | 61 | Manual | Manual | Manual |
| C4 | callers | complex | get_callers | search_by_name_typed | 300 | 8 | 2 | 14 | 28 | 16 | 1 | 16 | Manual | LeanKG | LeanKG |
| C5 | ontology | complex | concept_search | benchmark testing | 162 | 12 | 6 | 0 | 54 | 16 | 1 | 1 | Manual | Manual | LeanKG |
| C6 | ontology | complex | kg_context | impact radius calculation | 419 | 15 | 5 | 5 | 64 | 23 | 1 | 1 | Manual | LeanKG | LeanKG |
| C7 | ontology | complex | ontology_status | ontology status | 30 | 5 | 4 | 65 | 10 | 14 | 1 | 93 | Manual | LeanKG | LeanKG |
| C8 | overview | complex | wake_up | project overview | 155 | 4 | 44 | 4 | 19 | 20 | 123 | 22 | Manual | LeanKG | Manual |

## Analysis

- **Input Token Savings: 30.0%** - LeanKG reduces input tokens by pre-computing structured graph data.
- **Latency Overhead: 14938.8%** - LeanKG MCP round-trip costs more than local grep.
- **Output Token Overhead** - LeanKG returns structured metadata (typed elements), making output larger but 3x more information-dense.
- **Complex queries** (impact radius, call graphs, ontology) are where LeanKG excels - grep cannot compute these in a single call.
