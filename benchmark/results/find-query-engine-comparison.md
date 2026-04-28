# Benchmark Comparison: find-query-engine

## With LeanKG
- Total Tokens: 18494
- Input: 1279
- Cached: 16952
- Files Referenced: ["src/graph/query.rs", "src/mcp/handler.rs"]

## Without LeanKG
- Total Tokens: 21643
- Input: 21472
- Cached: 0
- Files Referenced: ["src/main.rs", "src/victim.rs", "src/bystander.rs", "src/mcp/tools.rs", "src/api/auth.rs", "src/api/mod.rs", "src/lib.rs", "src/a.rs", "src/x.rs", "src/handlers/mod.rs", "src/file1.rs", "src/file2.rs", "src/orchestrator/mod.rs", "src/mcp/handler.rs", "src/graph/query.rs", "src/graph/nl_query.rs", "src/graph/cache.rs", "src/doc/wiki.rs", "src/compress/response.rs", "tests/mcp_tests.rs", "tests/batched_insert_tests.rs", "tests/test_diagnose_empty4.rs", "tests/test_tools.rs", "tests/test_diagnose_empty.rs", "tests/test_diagnose_empty2.rs", "tests/test_all_tools_return_data.rs", "tests/mcp_tools_full_tests.rs", "tests/integration.rs", "tests/graph_cache_tests.rs"]

## Overhead
- Token Delta: -3149
