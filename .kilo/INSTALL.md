# Installing LeanKG for Kilo Code

## Prerequisites

- [Kilo Code](https://kilo.ai) installed

## Installation

### Option 1: Extension Fetch (Recommended)

Tell Kilo Code:

```
Fetch and follow instructions from https://raw.githubusercontent.com/FreePeak/LeanKG/refs/heads/main/.kilo/INSTALL.md
```

### Option 2: Manual MCP Setup

Add to `~/.config/kilo/kilo.json`:

```json
{
  "$schema": "https://kilo.ai/config.json",
  "mcp": {
    "leankg": {
      "type": "local",
      "command": ["leankg", "mcp-stdio", "--watch"],
      "enabled": true
    }
  }
}
```

## Verify Installation

Start a new session and ask:

```
What breaks if I change src/main.rs?
```

LeanKG should automatically use `get_impact_radius` to calculate the blast radius.

## What It Does

LeanKG automatically injects knowledge graph tools into your agent context:

- **Impact Analysis** - `get_impact_radius` calculates blast radius before changes
- **Code Search** - `search_code`, `find_function`, `query_file` find code instantly
- **Test Coverage** - `get_tested_by` shows what tests cover any element
- **Call Graphs** - `get_call_graph` shows function call chains
- **Context Generation** - `get_context` provides token-optimized file context

## Workflow: LeanKG First, Grep Fallback

**MANDATORY: Use LeanKG First**

Before ANY codebase search, you MUST:

1. Check LeanKG status: `mcp_status leankg`
2. If not initialized: `mcp_init leankg { path: "/project/path/.leankg" }`
3. Use LeanKG tools first
4. **Only if LeanKG returns empty, fall back to grep**

| Instead of | Use LeanKG |
|------------|------------|
| `grep -rn "X" --include="*.rs"` | `search_code("X")` or `find_function("X")` |
| `find . -name "*X*"` | `query_file("*X*")` |
| Manual tracing | `get_impact_radius(file, 3)` |
| `grep -rn "X" tests/` | `get_tested_by({ file: "src/X.rs" })` |

## Quick Usage

```bash
# Check if LeanKG is ready
mcp_status leankg

# Initialize for your project
mcp_init leankg { path: "/path/to/your/project/.leankg" }

# Ask questions like:
# "What breaks if I change auth.rs?"
# "Where is the login function?"
# "What tests cover the payment module?"
```

## Updating

LeanKG updates when you update the MCP configuration or restart Kilo.

To update the binary:

```bash
cargo install leankg
```

## Troubleshooting

### MCP server not connecting

1. Ensure LeanKG binary is installed: `cargo install leankg`
2. Check the binary is in your PATH: `which leankg`
3. Verify `kilo.json` syntax is valid

### Empty results from LeanKG

Run initialization:

```
mcp_init leankg { path: "/your/project/.leankg" }
```

## Getting Help

- Issues: https://github.com/FreePeak/LeanKG/issues
- Docs: https://github.com/FreePeak/LeanKG/blob/main/README.md
