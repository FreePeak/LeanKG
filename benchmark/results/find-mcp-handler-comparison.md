# Benchmark Comparison: find-mcp-handler

## With LeanKG
- Total Tokens: 37786
- Input: 162
- Cached: 37331
- Files Referenced: ["src/mcp/server.rs", "src/main.rs", "src/mcp/handler.rs", "src/mcp/tools.rs", "src/mcp/watcher.rs", "src/mcp/toon.rs", "src/mcp/tracking_db.rs", "src/mcp/tracker.rs", "src/mcp/mod.rs", "src/mcp/auth.rs", "src/auth.rs", "tests/mcp_tests.rs"]

## Without LeanKG
- Total Tokens: 38911
- Input: 14349
- Cached: 24485
- Files Referenced: ["src/mcp/server.rs", "src/mcp/tools.rs", "src/web/mod.rs", "src/orchestrator/mod.rs", "src/orchestrator/intent.rs", "src/mcp/handler.rs", "src/mcp/mod.rs", "src/indexer/xml_layout.rs", "src/graph/nl_query.rs", "src/embed/assets/index-HQiHwNOl.js", "src/embed/assets/index-CyE2Athb.js", "src/embed/assets/index-Bt5agME-.js", "src/db/models.rs", "src/api/mod.rs", "src/watcher/mod.rs", "src/auth.rs", "src/main.rs"]

## Overhead
- Token Delta: -1125
