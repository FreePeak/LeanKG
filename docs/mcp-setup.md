# MCP Server Setup

LeanKG exposes a Model Context Protocol (MCP) server that AI tools can connect to.

## Automated Setup (Recommended)

Use the install script to install and configure MCP for your AI tool:

```bash
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- <target>
```

See [Installation](../README.md#installation) for supported targets.

## Manual Setup

### OpenCode AI

Add to `~/.config/opencode/opencode.json`:

```json
{
  "mcp": {
    "leankg_dev": {
      "type": "local",
      "command": ["leankg", "mcp-stdio", "--watch"],
      "enabled": true
    }
  }
}
```

### Cursor AI

Add to `~/.cursor/mcp.json`:

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

### Claude Code / Claude Desktop

Add to `~/.config/claude/settings.json`:

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

### Gemini CLI

Add to `~/.config/gemini-cli/mcp.json`:

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

### Google Antigravity

Add to `~/.gemini/antigravity/mcp_config.json`:

```json
{
  "mcpServers": [
    {
      "name": "leankg",
      "transport": "stdio",
      "command": "leankg",
      "args": ["mcp-stdio", "--watch"],
      "enabled": true
    }
  ]
}
```

### Kilo Code

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

## Starting the MCP Server

```bash
# Stdio mode with auto-indexing (for local AI tools)
leankg mcp-stdio --watch

# Stdio mode without auto-indexing
leankg mcp-stdio
```

## Auto-Initialization

When the MCP server starts without an existing LeanKG project, it automatically initializes and indexes the current directory. This provides a "plug and play" experience for AI tools.

## Auto-Indexing

When the MCP server starts with an existing LeanKG project, it checks if the index is stale (by comparing git HEAD commit time vs database file modification time). If stale, it automatically runs incremental indexing to ensure AI tools have up-to-date context.

## Fallback

If the MCP server reports "LeanKG not initialized", manually run `leankg init` in your project directory, then restart the AI tool.
