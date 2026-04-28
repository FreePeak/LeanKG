---
description: Query LeanKG knowledge graph for code understanding. Use when exploring a codebase, finding functions, tracing dependencies, or calculating impact radius.
---

Use LeanKG tools to explore and understand code:

- `mcp_status` - Check if LeanKG is initialized
- `mcp_init` - Initialize LeanKG for a project: `mcp_init { path: "<project>/.leankg" }`
- `mcp_index` - Index a directory: `mcp_index { path: "<project>/src" }`
- `search_code` - Find code elements: `search_code { query: "<function_name>" }`
- `find_function` - Locate a function: `find_function { name: "<name>" }`
- `query_file` - Find files: `query_file { query: "<filename>" }`
- `get_impact_radius` - Calculate blast radius: `get_impact_radius { file: "<path>", depth: <n> }`
- `get_dependencies` - Get imports: `get_dependencies { file: "<path>" }`
- `get_dependents` - Get dependents: `get_dependents { file: "<path>" }`
- `get_context` - Get file context: `get_context { file: "<path>" }`
- `get_call_graph` - Get call chains: `get_call_graph { file: "<path>" }`
- `get_tested_by` - Get test coverage: `get_tested_by { file: "<path>" }`

**Rule: Use LeanKG first, before grep/ripgrep.**

LeanKG auto-initializes on first use. If `.leankg` does not exist, it creates one automatically. If index is stale (>5 min since last git commit), it re-indexes automatically.
