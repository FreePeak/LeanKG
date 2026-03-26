# @leankg/mcp-server

LeanKG - Lightweight Knowledge Graph MCP Server for AI-Assisted Development

## Installation

```bash
npm install -g @leankg/mcp-server
```

## Quick Start

```bash
# Initialize LeanKG in your project
leankg init

# Index your codebase
leankg index ./src

# Start MCP server (for AI tools)
leankg serve
```

## MCP Server Configuration

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

### Cursor

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

### OpenCode

Add to `~/.opencode/mcp.json`:

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

## Supported Platforms

- macOS (x64, ARM64)
- Linux (x64, ARM64)
- Windows (x64)

## Building from Source

If pre-built binaries are not available for your platform:

```bash
cargo install leankg
```

Or build manually:

```bash
git clone https://github.com/anomalyco/LeanKG.git
cd LeanKG
cargo build --release
cp target/release/leankg ~/.npm-global/bin/leankg
```

## License

MIT
