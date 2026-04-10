---
name: using-leankg
description: Use for ANY code search, navigation, or finding code logic - "where is X", "find logic Y", "how does Z work", impact analysis, dependencies. LeanKG is MANDATORY first.
---

<EXTREMELY_IMPORTANT>
STRICT ENFORCEMENT: You MUST use LeanKG tools FIRST before ANY other method.

1. ALWAYS invoke `mcp_status` first to check LeanKG readiness
2. If MCP UNAVAILABLE, fall back to CLI: `leankg query "name" --kind name`
3. If NOT ready, invoke `mcp_init` or `mcp_index` to initialize
4. Use LeanKG tools for ALL searches
5. ONLY fall back to RTK or grep if LeanKG returns EMPTY results

**NO EXCEPTIONS. NO RATIONALIZATION. Use LeanKG first.**
</EXTREMELY_IMPORTANT>

## MANDATORY Search Flow: LeanKG -> CLI Fallback -> RTK -> Grep

```
When user asks "where is X", "find logic Y", "how does Z work", etc:
           |
           v
1. mcp_status (ALWAYS check first)
           |
           +-- MCP UNAVAILABLE --> CLI fallback (see below)
           |
           v
2. search_code("X") or find_function("X") or query_file("X")
           |
           +-- Results returned --> Use get_context(file) to read content
           |
           v (EMPTY)
3. CLI fallback: leankg query "X" --kind name
           |
           v (EMPTY)
4. rtk grep "X" --path .
           |
           v (EMPTY)
5. grep -rn "X" --include="*.rs"
```

## MCP Server Unavailable - CLI Fallback

If LeanKG MCP tools fail (connection refused, not available), use CLI:

```bash
# Check status
leankg status

# Index codebase
leankg index ./src

# Query by name
leankg query "function_name" --kind name

# Query by pattern
leankg query "*.rs" --kind pattern

# Get impact radius
leankg impact src/main.rs 3
```

## LeanKG MCP Tools (Use in this order)

| Step | Tool | When to Use |
|------|------|-------------|
| 1 | `mcp_status` | ALWAYS check first |
| 2 | `search_code("X")` | Find code by name/type |
| 3 | `find_function("X")` | Locate function definitions |
| 4 | `query_file("*X*")` | Find files by name |
| 5 | `get_impact_radius(file)` | Blast radius for changes |
| 6 | `get_context(file)` | READ file content (token-optimized) |
| 7 | `get_dependencies(file)` | Get imports |
| 8 | `get_tested_by(file)` | Find tests |

## Critical: After search_code returns file paths

**IMPORTANT:** When `search_code` returns results with file paths:
1. Use `get_context(file_path)` to READ the actual file content
2. Do NOT just report the file paths - show the code

## RTK Fallback (Only if LeanKG EMPTY)

```bash
rtk grep "search term" --path .
rtk file "pattern" --path .
```

## Grep Fallback (LAST RESORT, only if RTK EMPTY too)

```bash
grep -rn "X" --include="*.rs"
```

## Common Triggers for LeanKG

| User says... | LeanKG tool |
|--------------|-------------|
| "where is X" | `search_code("X")` or `find_function("X")` |
| "find the logic" | `search_code("logic_name")` |
| "how does X work" | `get_context(file)` after search_code |
| "what calls X" | `get_call_graph("X")` |
| "what breaks if I change X" | `get_impact_radius("X")` |
| "find all files named X" | `query_file("X")` |