# Installing LeanKG for Cursor

## Prerequisites

- [Cursor](https://cursor.sh) installed

## Installation

In Cursor Agent chat, install from plugin marketplace:

```
/add-plugin leankg
```

Or search for "leankg" in the plugin marketplace.

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
leankg_mcp_status

# Initialize for your project
leankg_mcp_init({ path: "/path/to/your/project/.leankg" })

# Ask questions like:
# "What breaks if I change auth.rs?"
# "Where is the login function?"
# "What tests cover the payment module?"
```

## Updating

LeanKG updates automatically when you update the plugin.

## Manual Installation

If the marketplace doesn't work, add to `~/.cursor/mcp.json`:

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