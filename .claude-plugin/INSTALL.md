# Installing LeanKG for Claude Code

## Prerequisites

- [Claude Code](https://claude.ai/code) installed

## Installation

Superpowers is available via the official Claude plugin marketplace:

```
/plugin install leankg@claude-plugins-official
```

Or register the marketplace first:

```
/plugin marketplace add FreePeak/leankg-marketplace
/plugin install leankg@leankg-marketplace
```

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

LeanKG updates automatically when you update the plugin:

```
/plugin update leankg
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