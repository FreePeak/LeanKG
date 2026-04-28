---
name: using-leankg
description: Use when user asks about codebase navigation, searching, finding code, impact analysis, or dependencies - LeanKG is the default grep replacement
---

<EXTREMELY_IMPORTANT>
If you think there is even a 1% chance a skill might apply to what you are doing, you ABSOLUTELY MUST invoke the skill.

IF A SKILL APPLIES TO YOUR TASK, YOU DO NOT HAVE A CHOICE. YOU MUST USE IT.

This is not negotiable. This is not optional. You cannot rationalize your way out of this.
</EXTREMELY_IMPORTANT>

## LeanKG - Default Grep Replacement

**LeanKG is the MANDATORY first resort for ALL codebase searches.**

## Rule: LeanKG First, Grep Fallback

**Invoke LeanKG tools BEFORE any grep/ripgrep usage.**

```
User asks "Where is X?" or searches for code
          |
          v
1. mcp_status - check if LeanKG is ready
          |
          +-- NOT READY --> mcp_init with project .leankg path
          |
          v (READY)
2. Use LeanKG tools FIRST:
   - search_code("X") for symbol search
   - find_function("X") for function definitions
   - query_file("X") for file search
   - get_impact_radius(file) for blast radius
   - get_tested_by(file) for test coverage
          |
          v
3. LeanKG returns results:
          |
          +-- EMPTY --> Fall back to grep/ripgrep
          |
          v (HAS RESULTS)
4. Return LeanKG results (token-optimized)
```

## Tool Mapping

| Instead of | Use LeanKG | Grep Fallback |
|------------|------------|---------------|
| `grep -rn "X" --include="*.rs"` | `search_code` or `find_function` | `grep -rn "X" --include="*.rs"` |
| `find . -name "*X*"` | `query_file` | `find . -name "*X*"` |
| Manual dependency tracing | `get_impact_radius` | N/A |
| `grep -rn "X" tests/` | `get_tested_by` | `grep -rn "X" tests/` |
| Reading entire files | `get_context` (token-optimized) | `cat file.rs` |

## MCP Tools Reference

| Tool | Purpose |
|------|---------|
| `mcp_status` | Check if LeanKG is initialized |
| `mcp_init` | Initialize LeanKG (creates .leankg, indexes) |
| `mcp_index` | Force re-index |
| `search_code` | Search code elements by name/type |
| `find_function` | Locate function definitions |
| `query_file` | Find files by name/pattern |
| `get_impact_radius` | Calculate blast radius (N hops) |
| `get_dependencies` | Get direct imports |
| `get_dependents` | Get files depending on target |
| `get_context` | Get AI-optimized context |
| `get_call_graph` | Get function call chains |
| `find_large_functions` | Find oversized functions |
| `get_tested_by` | Get test coverage |
| `get_clusters` | Get functional clusters |
| `get_review_context` | Focused subgraph for reviews |
| `detect_changes` | Pre-commit risk analysis |

## When LeanKG Returns Empty

If LeanKG tools return empty results, this is NOT an error. Fall back to grep:

```bash
# Rust
grep -rn "X" --include="*.rs"

# Go
grep -rn "X" --include="*.go"

# TypeScript
grep -rn "X" --include="*.ts" --include="*.tsx"

# Python
grep -rn "X" --include="*.py"
```

## Auto-Init Behavior

LeanKG automatically initializes on first use:
- If `.leankg` does not exist, it creates one automatically
- If index is stale (>5 min since last git commit), it re-indexes automatically
- Set `auto_index_on_start: false` in `leankg.yaml` to disable