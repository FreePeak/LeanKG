# Installing LeanKG for OpenCode

## Prerequisites

- [OpenCode.ai](https://opencode.ai) installed

## Installation

Add LeanKG to the `plugins` array in your `opencode.json` (global or project-level):

```json
{
  "plugins": ["leankg@git+https://github.com/FreePeak/LeanKG.git"]
}
```

Restart OpenCode. The plugin auto-installs and LeanKG tools auto-activate on every prompt.

## Verify Installation

Start a new session and ask:

```
What breaks if I change src/main.rs?
```

LeanKG should automatically use `get_impact_radius` to calculate the blast radius.

## What It Does

The plugin:

1. **Injects LeanKG bootstrap** - Adds LeanKG tool awareness to every conversation
2. **Registers skills directory** - Discovers `using-leankg` skill automatically
3. **Enables MCP tools** - LeanKG MCP server tools become available

### Tools Available

- **Impact Analysis** - `get_impact_radius` calculates blast radius before changes
- **Code Search** - `search_code`, `find_function`, `query_file` find code instantly
- **Test Coverage** - `get_tested_by` shows what tests cover any element
- **Call Graphs** - `get_call_graph` shows function call chains
- **Context Generation** - `get_context` provides token-optimized file context
- **Clusters** - `get_clusters` shows functional code communities

## Workflow: LeanKG First, Grep Fallback

**MANDATORY: Use LeanKG First**

Before ANY codebase search, you MUST:

1. Check LeanKG status: `mcp_status`
2. If not initialized: `mcp_init({ path: "/project/path/.leankg" })`
3. Use LeanKG tools first
4. **Only if LeanKG returns empty, fall back to grep**

| Instead of | Use LeanKG |
|------------|------------|
| `grep -rn "X" --include="*.rs"` | `search_code("X")` or `find_function("X")` |
| `find . -name "*X*"` | `query_file("*X*")` |
| Manual tracing | `get_impact_radius(file, 3)` |
| `grep -rn "X" tests/` | `get_tested_by({ file: "src/X.rs" })` |

## LeanKG Skill

LeanKG includes a skill that enforces the grep-fallback pattern:

```
use skill tool to load leankg/using-leankg
```

The skill ensures LeanKG is always the first resort.

## Updating

LeanKG updates automatically when you restart OpenCode.

To pin a specific version:

```json
{
  "plugins": ["leankg@git+https://github.com/FreePeak/LeanKG.git#v0.2.0"]
}
```

## Manual Installation (Alternative)

If the plugin system doesn't work:

1. Ensure LeanKG binary is installed: `cargo install leankg`
2. Add to your `~/.config/opencode/opencode.json`:

```json
{
  "mcpServers": {
    "leankg": {
      "command": "leankg",
      "args": ["mcp-stdio", "--watch"]
    }
  }
}
```

## Troubleshooting

### Plugin not loading

1. Check OpenCode logs: `opencode run --print-logs "hello" 2>&1 | grep -i leankg`
2. Verify the plugin line in your `opencode.json`
3. Make sure you're running a recent version of OpenCode

### LeanKG tools not found

1. Run `mcp_status` to check if LeanKG MCP server is connected
2. If not initialized, run `mcp_init`
3. Check LeanKG binary is in your PATH

### Empty results from LeanKG

This is normal if the codebase hasn't been indexed. Run:

```
mcp_init({ path: "/your/project/.leankg" })
```

## Getting Help

- Issues: https://github.com/FreePeak/LeanKG/issues
- Docs: https://github.com/FreePeak/LeanKG/blob/main/README.md
