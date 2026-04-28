# Benchmark Comparison: impl-search-enhance

## With LeanKG
- Total Tokens: 61982
- Input: 61616
- Cached: 0
- Files Referenced: ["src/mcp/handler.rs", "src/auth.rs", "src/main.rs", "src/graph/query.rs", "src/mcp/tools.rs"]

## Without LeanKG
- Total Tokens: 43393
- Input: 409
- Cached: 42808
- Files Referenced: ["src/mcp/server.rs", "src/mcp/tools.rs", "src/mcp/watcher.rs", "src/mcp/tracking_db.rs", "src/mcp/tracker.rs", "src/mcp/toon.rs", "src/mcp/mod.rs", "src/mcp/handler.rs", "src/mcp/auth.rs", "src/auth.rs", "src/main.rs", "src/graph/query.rs"]

## Overhead
- Token Delta: 18589
