---
name: using-leankg
description: Use for ANY code search, navigation, or finding code logic - "where is X", "find logic Y", "how does Z work", impact analysis, dependencies. LeanKG is MANDATORY first.
---

<EXTREMELY_IMPORTANT>
STRICT ENFORCEMENT: You MUST follow the 4-Tier Search Fallback Chain EXACTLY.

1. ALWAYS start with `mcp_status` to check LeanKG readiness
2. If NOT ready, invoke `mcp_init` or `mcp_index` to initialize
3. Use LeanKG MCP tools for ALL searches (Tier 1)
4. ONLY fall back to `leankg` CLI if MCP fails (Tier 2)
5. ONLY fall back to `rtk` if CLI fails (Tier 3)
6. ONLY fall back to `grep`/`rg` as ABSOLUTE LAST RESORT (Tier 4)

**NO EXCEPTIONS. NO RATIONALIZATION. Follow the chain.**
</EXTREMELY_IMPORTANT>

## MANDATORY 4-Tier Search Fallback Chain

```
When user asks "where is X", "find logic Y", "how does Z work", etc:
           |
           v
TIER 1: LeanKG MCP Server (leankg_mcp_* tools)
           |
           mcp_status (ALWAYS check first)
           search_code("X") or find_function("X") or query_file("X")
           |
           +-- Results found --> get_context(file) to read content --> DONE
           +-- MCP down/error --> TIER 2
           |
           v
TIER 2: leankg CLI command
           |
           leankg query "X" --kind name
           |
           +-- Results found --> DONE
           +-- Empty/error --> TIER 3
           |
           v
TIER 3: rtk (rich toolkit grep)
           |
           rtk grep "X" --path .
           rtk file "pattern" --path .
           |
           +-- Results found --> DONE
           +-- Empty --> TIER 4
           |
           v
TIER 4: grep/rg (ABSOLUTE LAST RESORT)
           |
           rg "X"
           grep -rn "X" --include="*.ext"
```

## ABSOLUTE BANS

- NEVER skip directly to grep/rg when LeanKG is available
- NEVER use Glob to find code files before checking LeanKG
- NEVER use Grep tool before exhausting Tiers 1-3
- The ONLY exception: searching non-code files (config, docs, data files)

## Tier 1: LeanKG MCP Tools (Use in this order)

| Step | Tool | When to Use |
|------|------|-------------|
| 1 | `mcp_status` | ALWAYS check first |
| 2 | `search_code("X")` | Find code by name/type |
| 3 | `find_function("X")` | Locate function definitions |
| 4 | `query_file("*X*")` | Find files by name |
| 5 | `get_impact_radius(file)` | Blast radius for changes |
| 6 | `get_context(file)` | READ file content (token-optimized) |
| 7 | `get_dependencies(file)` | Get imports |
| 8 | `get_dependents(file)` | Get reverse dependencies |
| 9 | `get_tested_by(file)` | Find tests |
| 10 | `get_callers("func")` | Find who calls a function |
| 11 | `get_call_graph("func")` | Full call graph |

## Tier 2: leankg CLI (Only if MCP fails)

```bash
leankg status
leankg query "X" --kind name
leankg impact file 3
```

## Tier 3: rtk Fallback (Only if leankg CLI empty)

```bash
rtk grep "X" --path .
rtk file "pattern" --path .
```

## Tier 4: grep/rg (ABSOLUTE LAST RESORT)

```bash
rg "X"
grep -rn "X" --include="*.ext"
```

## Critical: After search_code returns file paths

**IMPORTANT:** When `search_code` returns results with file paths:
1. Use `get_context(file_path)` to READ the actual file content
2. Do NOT just report the file paths - show the code

## Common Triggers

| User says... | Start with |
|--------------|------------|
| "where is X" | `search_code("X")` or `find_function("X")` |
| "find the logic" | `search_code("logic_name")` |
| "how does X work" | `get_context(file)` after search_code |
| "what calls X" | `get_callers("X")` or `get_call_graph("X")` |
| "what breaks if I change X" | `get_impact_radius("X")` |
| "find all files named X" | `query_file("X")` |
| "who imports X" | `get_dependents("X")` |
| "what does X depend on" | `get_dependencies("X")` |
