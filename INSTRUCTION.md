# LeanKG MCP Server Setup - Lazy People's Guide

**TL;DR:** Copy-paste one command and you're done.

---

## One-Command Install (Recommended)

Run this for your AI tool:

```bash
# For Cursor
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- cursor

# For Claude Code
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- claude

# For OpenCode
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- opencode
```

That's it. The script does everything: installs binary, configures MCP, sets up hooks.

---

## Option 1: Stdio Transport (Default)

### Already installed via script above? Restart your AI tool and skip to [Verify](#verify-it-works).

### Manual Setup

**1. Find your AI tool's MCP config location:**

| Tool | Config File |
|------|-------------|
| Cursor | `~/.cursor/mcp.json` or `.cursor/mcp.json` in project |
| Claude Code | `~/.claude/settings.json` |
| OpenCode | `.opencode.json` in project |

**2. Add this to your config:**

```json
{
  "mcpServers": {
    "leankg": {
      "command": "leankg",
      "args": ["mcp-stdio", "--watch", "."]
    }
  }
}
```

**3. Restart your AI tool or run `/reload`**

---

## Option 2: HTTP/SSE Transport

Use this if you want remote access or multiple tools sharing the same LeanKG instance.

### Step 1: Start the server (keep this terminal open)

```bash
leankg mcp-http --port 9699
```

### Step 2: Configure your AI tool

**Cursor:**
```json
{
  "mcpServers": {
    "leankg": {
      "url": "http://localhost:9699/mcp"
    }
  }
}
```

**Claude Code** (`~/.claude/settings.json`):
```json
{
  "mcpServers": {
    "leankg": {
      "url": "http://localhost:9699/mcp"
    }
  }
}
```

**OpenCode** (`.opencode.json`):
```json
{
  "plugins": ["leankg"],
  "leankg": {
    "transport": "http",
    "url": "http://localhost:9699/mcp"
  }
}
```

### Step 3: Restart your AI tool

---

## Verify It Works

Ask your AI tool:
> "What's the status of leankg?"

You should see something like:
```
initialized: true,
elements: 1234,
relationships: 5678,
```

---

## Fix High RAM on macOS

If LeanKG uses too much memory, add this to your `~/.zshrc` or `~/.bashrc`:

```bash
export LEANKG_MMAP_SIZE=134217728
export LEANKG_CACHE_MAX_TOKENS=100000
```

Then restart your terminal.

---

## Uninstall / Cleanup

```bash
# Kill all LeanKG processes
pkill -9 -f leankg

# Remove config files
rm ~/.cursor/mcp.json
rm ~/.claude/settings.json
rm .opencode.json
rm -rf .leankg
```

---

## Quick Commands Reference

| What | Command |
|------|---------|
| Start stdio mode | `leankg mcp-stdio` |
| Start HTTP mode | `leankg mcp-http --port 9699` |
| Check status | `leankg status` |
| Reindex | `leankg mcp-index` |
| Kill all | `pkill -9 -f leankg` |

---

## Troubleshooting

**"command not found: leankg"**
```bash
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- opencode
```

**"database is locked"**
```bash
pkill -9 -f leankg
pkill -9 -f vite
leankg mcp-stdio
```

**"connection refused"** (HTTP mode)
```bash
# Check if server is running
lsof -i :9699

# If not, start it
leankg mcp-http --port 9699
```

---

## For AI Agents (Auto-Setup)

If you're an AI agent setting up LeanKG for a user:

```bash
# 1. Run the install script
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- opencode

# 2. Initialize in the project
cd <project-path>
leankg init
leankg index ./src

# 3. Tell user to restart their AI tool
```

The install script automatically:
- Installs the `leankg` binary
- Creates MCP config for stdio transport
- Sets up instructions for AI context
- Configures session hooks (Claude Code only)