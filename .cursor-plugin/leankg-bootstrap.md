# LeanKG - Lightweight Knowledge Graph

LeanKG is a lightweight knowledge graph for codebase understanding. It indexes code, builds dependency graphs, calculates impact radius, and exposes everything via MCP for AI tool integration.

## MCP Tools

LeanKG provides these MCP tools for codebase navigation and analysis:

| Tool | Purpose |
|------|---------|
| `mcp_status` | Check if LeanKG is initialized and ready |
| `mcp_init` | Initialize LeanKG for a project |
| `mcp_index` | Index codebase |
| `search_code` | Search code elements by name/type |
| `find_function` | Locate function definitions |
| `query_file` | Find files by name/pattern |
| `get_impact_radius` | Calculate blast radius of changes (N hops) |
| `get_dependencies` | Get direct imports of a file |
| `get_dependents` | Get files depending on target |
| `get_context` | Get AI-optimized context for a file |
| `get_call_graph` | Get function call chains |
| `find_large_functions` | Find oversized functions |
| `get_tested_by` | Get test coverage for a function/file |
| `get_doc_for_file` | Get documentation for a file |
| `get_traceability` | Get full traceability chain |
| `get_code_tree` | Get codebase structure |
| `get_doc_tree` | Get documentation tree |
| `get_clusters` | Get functional clusters |
| `detect_changes` | Pre-commit risk analysis |

## MANDATORY 4-Tier Search Fallback Chain

**VIOLATION OF THIS CHAIN IS UNACCEPTABLE. ALWAYS follow this exact order:**

```
Tier 1: LeanKG MCP Server (leankg_mcp_* tools)
    |       mcp_status → search_code / find_function / query_file
    |       If MCP down/error → Tier 2
    v
Tier 2: leankg CLI command
    |       leankg query "X" --kind name
    |       If empty/error → Tier 3
    v
Tier 3: rtk (rich toolkit grep)
    |       rtk grep "X" --path .
    |       If empty → Tier 4
    v
Tier 4: grep/rg (ABSOLUTE LAST RESORT)
```

| Instead of | Use LeanKG |
|------------|------------|
| grep/ripgrep for "where is X?" | `search_code` or `find_function` |
| glob + content search for tests | `get_tested_by` |
| Manual dependency tracing | `get_impact_radius` or `get_dependencies` |
| Reading entire files | `get_context` (token-optimized) |

## Auto-Init Behavior

LeanKG automatically initializes on first use:
- If `.leankg` does not exist, it creates one automatically
- If index is stale (>5 min since last git commit), it re-indexes automatically
- Set `auto_index_on_start: false` in `leankg.yaml` to disable

## Quick Commands

```bash
# Index a codebase
leankg init
leankg index ./src

# Calculate impact radius
leankg impact src/main.rs 3

# Start MCP server
leankg mcp-stdio
```