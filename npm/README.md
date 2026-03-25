# LeanKG NPM Package

This directory contains the npm package for LeanKG that allows installation without requiring Rust.

## Publishing to npm

1. Build the Rust binary for all platforms:
```bash
# macOS x64
cargo build --release --target x86_64-apple-darwin
# macOS ARM64
cargo build --release --target aarch64-apple-darwin
# Linux x64
cargo build --release --target x86_64-unknown-linux-gnu
# Linux ARM64
cargo build --release --target aarch64-unknown-linux-gnu
```

2. Create distribution packages:
```bash
# For each platform, create a tar.gz with the binary
tar -czf leanKG-macos-x64.tar.gz -C target/x86_64-apple-darwin/release leanKG
tar -czf leanKG-macos-arm64.tar.gz -C target/aarch64-apple-darwin/release leanKG
tar -czf leanKG-linux-x64.tar.gz -C target/x86_64-unknown-linux-gnu/release leanKG
tar -czf leanKG-linux-arm64.tar.gz -C target/aarch64-unknown-linux-gnu/release leanKG
```

3. Create GitHub releases and upload the tarballs

4. Update `install.js` with the correct repo URL

5. Publish to npm:
```bash
cd npm
npm publish --access public
```

## Setting Up MCP Clients

After installing via npm, configure your AI tool to use LeanKG MCP server:

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

### Google Antigravity (Gemini CLI)

```bash
# Using Gemini CLI's native MCP support
gemini mcp add leankg leankg mcp-stdio --watch
```

### Anti Gravity

Add to your Anti Gravity MCP config:
```json
{
  "servers": {
    "leankg": {
      "command": "leankg",
      "args": ["mcp-stdio", "--watch"]
    }
  }
}
```

## Usage

```bash
# Initialize LeanKG in your project
leankg init

# Index your codebase
leankg index ./src

# Start MCP server
leankg serve

# Or use stdio mode for AI tools
leankg mcp-stdio --watch
```
