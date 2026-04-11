# Installing LeanKG for Cursor

## Prerequisites

- [Cursor](https://cursor.sh) installed
- LeanKG binary installed (run without args to install)

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

## What LeanKG Does

- **Impact Analysis** - Calculate blast radius before making changes
- **Code Search** - Find functions, files, dependencies instantly
- **Test Coverage** - Know what tests cover any code element
- **Call Graphs** - Understand function call chains
- **Context Generation** - Get AI-optimized context for any file

## Auto-Trigger Behavior

LeanKG activates automatically for code search patterns:

- **Rule** `leankg-rule.mdc` - `alwaysApply: true` with `priority: 10` auto-triggers for code patterns
- **Skill** `using-leankg` - Invoked when detecting code search/navigation context
- **Hook** `session-start` - Injects LeanKG bootstrap context on session start

## Per-Project Fallback

If LeanKG MCP is not available for a project, the agent will ask:

> "Would you like to install LeanKG MCP server for this project?"

The agent can create a per-project `.cursor/mcp.json` if needed.

## Quick Usage

```bash
# Ask the agent in any project:
# "Where is the auth function?"
# "What breaks if I change payment.rs?"
# "What tests cover the user module?"
```

## Updating

```bash
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- cursor
```