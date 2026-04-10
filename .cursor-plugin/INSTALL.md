# Installing LeanKG for Cursor

## Prerequisites

- [Cursor](https://cursor.sh) installed

## Installation

Install LeanKG for Cursor using the one-line installer:

```bash
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- cursor
```

This installs:
1. LeanKG binary to `~/.local/bin`
2. LeanKG MCP server to `~/.cursor/mcp.json` (global, available in all projects)
3. LeanKG plugin to `~/.cursor/plugins/leankg/` with:
   - **Skill** - `skills/using-leankg/SKILL.md` for mandatory LeanKG-first workflow
   - **Rule** - `rules/leankg-rule.mdc` with auto-trigger for code search
   - **Agents** - `agents/leankg-agents.md` with LeanKG tool instructions
   - **Commands** - `commands/leankg-commands.md` for leankg:* commands
   - **Hooks** - `hooks/session-start` to bootstrap LeanKG context

## What It Does

The plugin automatically injects LeanKG knowledge graph tools into your agent context:

- **Impact Analysis** - Calculate blast radius before making changes
- **Code Search** - Find functions, files, dependencies instantly
- **Test Coverage** - Know what tests cover any code element
- **Call Graphs** - Understand function call chains
- **Context Generation** - Get AI-optimized context for any file

## Auto-Trigger Behavior

LeanKG activates automatically for code search and navigation:

- **Rule** `leankg-rule.mdc` - `alwaysApply: true` with `priority: 10` auto-triggers for code patterns
- **Skill** `using-leankg` - Invoked when detecting code search/navigation context
- **Hook** `session-start` - Injects LeanKG bootstrap context on session start

## Updating

Run the installer again to update LeanKG:

```bash
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- cursor
```

This updates all LeanKG plugin files without removing your other Cursor skills.

## Quick Usage

```
# Check if LeanKG is ready
mcp_status

# Initialize for your project
mcp_init({ path: "/path/to/your/project/.leankg" })

# Ask questions like:
# "What breaks if I change auth.rs?"
# "Where is the login function?"
# "What tests cover the payment module?"
```

## Manual Installation

If the installer doesn't work, manually add to `~/.cursor/mcp.json`:

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