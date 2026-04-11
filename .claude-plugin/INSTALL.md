# Installing LeanKG for Claude Code

## Prerequisites

- [Claude Code](https://claude.ai/code) installed

## Installation

Run the one-line installer:

```bash
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- claude
```

This configures MCP in `~/.config/claude/settings.json` and adds LeanKG instructions to `~/.config/claude/CLAUDE.md`.

## What It Does

The plugin automatically injects LeanKG knowledge graph tools into your agent context:

- **Impact Analysis** - Calculate blast radius before making changes
- **Code Search** - Find functions, files, dependencies instantly
- **Test Coverage** - Know what tests cover any code element
- **Call Graphs** - Understand function call chains
- **Context Generation** - Get AI-optimized context for any file

## Quick Usage

```
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

```bash
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- update
```

## Manual Installation

If marketplace doesn't work, add to `~/.config/claude/settings.json`:

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