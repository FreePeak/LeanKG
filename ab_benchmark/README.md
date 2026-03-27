# LeanKG A/B Testing Benchmark

## Overview

This benchmark compares LeanKG's context retrieval against standard baseline approaches to measure:
1. **Token Savings** - Reduction in context tokens
2. **Context Precision** - Noise reduction (exclusion of irrelevant content)
3. **Context Recall** - Sufficiency (inclusion of all necessary linked docs)

## Methodology

### Method A (Baseline)
Standard grep + file reading approach:
- Search for query terms across codebase
- Read entire files containing matches
- No relationship awareness
- Simulates non-LeanKG AI tools

### Method B (LeanKG)
MCP tool-based subgraph queries:
- Query knowledge graph for relevant elements
- Fetch targeted context with relationships
- Only returns connected nodes and edges

## Test Queries

Located in `prompts/queries.yaml`:
1. `ab-query-handler` - Find MCP handler implementation
2. `ab-code-element` - Locate CodeElement struct definition
3. `ab-dependency-graph` - Find query engine for dependency graph
4. `ab-indexing-flow` - Explain file indexing to DB storage flow
5. `ab-impact-analysis` - Code elements affected by CodeElement changes
6. `ab-call-graph` - Functions calling query engine
7. `ab-test-creation` - Tests for extractor module
8. `ab-context-retrieval` - Context retrieval mechanism
9. `ab-mcp-tools` - Available MCP tools
10. `ab-db-schema` - Database schema for code elements

## Usage

```bash
cd /Users/linh.doan/work/harvey/freepeak/.worktree/leankg-ab-benchmark

# Run the benchmark
./ab_benchmark/scripts/run_ab_test.sh

# Generate report
python3 ab_benchmark/scripts/generate_report.py ab_benchmark/results/

# View results
cat ab_benchmark/results/ab_results.csv
```

## Output Files

| File | Description |
|------|-------------|
| `ab_results.csv` | Summary metrics per query |
| `*_baseline.txt` | Raw baseline context for each query |
| `*_leankg.txt` | Raw LeanKG context for each query |
| `benchmark_report.md` | Formatted markdown report |

## Metrics

- **Token Efficiency**: `(baseline_tokens - leankg_tokens) / baseline_tokens * 100`
- **Precision**: LeanKG excludes irrelevant files/imports
- **Recall**: LeanKG includes linked docs and function signatures

## Prerequisites

- LeanKG indexed: `cargo run -- index ./src`
- kilo CLI installed
- Python with tiktoken: `pip install tiktoken`
