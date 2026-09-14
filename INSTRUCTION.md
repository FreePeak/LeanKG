# LeanKG MCP Server Setup - Lazy People's Guide

> **v4.6.0:** the implementation is 100% Go (`go/`). Build: `make go-build` · Test: `make go-test` · See `AGENTS.md` for the current workflow.


**TL;DR:** Copy-paste one command and you're done.

---

## One-Command Install (Recommended)

Run this for your AI tool:

```bash
# 1. Install the binary (builds leankg + leankg-embed from source; needs Go >= 1.25)
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install-go.sh | bash

# 2. Wire your AI tool's MCP config
leankg install --target cursor      # claude-code | cursor | codex | gemini | opencode | omp
```

That's it. The installer puts `leankg` in `~/.local/bin`; `leankg install` writes the stdio MCP entry for the client you name.

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
      "args": ["serve", "--stdio"]
    }
  }
}
```

**3. Build the index once, from the project root:**

```bash
leankg index .
```

`serve` does not watch files. Run `leankg writer` in a background terminal to keep the index fresh as you edit.

**4. Restart your AI tool or run `/reload`**

---

## Option 2: HTTP Transport

Use this if you want remote access or multiple tools sharing the same LeanKG instance.

### Step 1: Start the server (keep this terminal open)

```bash
leankg serve --http :9699
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
| Start stdio mode | `leankg serve --stdio` |
| Start HTTP mode | `leankg serve --http :9699` |
| Check status | `leankg status` |
| Index a directory | `leankg index <dir>` |
| Keep index fresh (watcher) | `leankg writer` |
| Kill all | `pkill -9 -f leankg` |

---

## Troubleshooting

**"command not found: leankg"**
```bash
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install-go.sh | bash
```

**"database is locked"**
```bash
pkill -9 -f leankg
leankg serve --stdio
```

**"connection refused"** (HTTP mode)
```bash
# Check if server is running
lsof -i :9699

# If not, start it
leankg serve --http :9699
```

---

## For AI Agents (Auto-Setup)

If you're an AI agent setting up LeanKG for a user:

```bash
# 1. Install the binary
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install-go.sh | bash

# 2. Build the index in the project (no separate init step; indexing creates the store)
cd <project-path>
leankg index .

# 3. Wire the MCP config, then tell the user to restart their AI tool
leankg install --target opencode      # claude-code | cursor | codex | gemini | opencode | omp
```

What each step does:
- `scripts/install-go.sh` builds and installs the `leankg` and `leankg-embed` binaries (default `~/.local/bin`)
- `leankg index .` creates `.leankg/` and indexes the project
- `leankg install --target <client>` writes the stdio MCP entry for that client