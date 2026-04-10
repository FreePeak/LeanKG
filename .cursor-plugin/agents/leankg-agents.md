# LeanKG Agent Instructions

## MANDATORY: Use LeanKG First

## LeanKG Tools Usage

### MANDATORY RULE - ALWAYS USE LEANKG FIRST

**Before doing ANY codebase search, navigation, or analysis, you MUST use LeanKG tools.**

### This Is Not Optional

When a user asks about the codebase, you MUST:
1. Check if LeanKG is available via `mcp_status`
2. If LeanKG is not initialized, run `mcp_init` first
3. Use the appropriate LeanKG tool for the task
4. **ONLY after LeanKG is exhausted** may you fall back to other methods

### Why This Matters

| Instead of | Use LeanKG |
|------------|------------|
| grep/ripgrep for "where is X?" | `search_code` or `find_function` |
| glob + content search for tests | `get_tested_by` |
| Manual dependency tracing | `get_impact_radius` or `get_dependencies` |
| Reading entire files | `get_context` (token-optimized) |

### LeanKG Tools Quick Reference

| Tool | Use Case |
|------|----------|
| `mcp_status` | Check if LeanKG is ready |
| `search_code` | Find code by name/type |
| `find_function` | Locate function definition |
| `query_file` | Find file by name/pattern |
| `get_impact_radius` | Blast radius before changes |
| `get_call_graph` | Understand function flow |
| `get_dependencies` | Get direct imports |
| `get_dependents` | Get files depending on target |
| `get_tested_by` | Find related tests |
| `get_context` | Minimal AI context (token-optimized) |
| `get_review_context` | Focused subgraph for reviews |

### Example Decision Flow

**User: "Where is the auth function?"**
```
1. mcp_status -> confirmed ready
2. search_code("auth") OR find_function("auth")
3. Return result from LeanKG
```

**User: "What would break if I change main.rs?"**
```
1. mcp_status -> confirmed ready
2. get_impact_radius("src/main.rs", depth=3)
3. Return affected elements
```

### Important Notes

- LeanKG maintains a **knowledge graph** of your codebase
- `get_impact_radius` calculates blast radius - always check before changes
- `get_context` returns token-optimized output (~99% token savings)
- Tools are pre-indexed and **much faster** than runtime grep/search
- If LeanKG returns empty/incomplete results, THEN you may use fallback methods