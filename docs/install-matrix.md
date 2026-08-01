# Assistant Install Matrix (US-GF-15 / FR-GF-23)

One-command install for every supported AI coding tool. All targets install the
binary + MCP wiring + skills/agent docs via `scripts/install.sh`.

## Quick install

```bash
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- <target>
```

| Target      | Config written                                        | Skill / docs              | Hooks / rules            |
| ----------- | ----------------------------------------------------- | ------------------------- | ------------------------ |
| `cursor`    | `.cursor/mcp.json` (per-project) + `~/.cursor/mcp.json` | `~/.cursor/skills/using-leankg` + `~/.cursor/AGENTS.md` | graph-first rule + session hook |
| `claude`    | `~/.config/claude/settings.json` (`mcpServers.leankg`) | `~/.config/claude/CLAUDE.md` | PreToolUse nudge hooks   |
| `opencode`  | `.opencode.json` (per-project)                        | `~/.config/opencode/skills/using-leankg` + `AGENTS.md` | —                        |
| `codex`     | `~/.codex/config.toml` (`[mcp_servers.leankg]`)      | `~/.codex/skills/using-leankg` + `~/.codex/AGENTS.md` | —                        |
| `gemini`    | `~/.gemini/settings.json` or `gemini mcp add`         | `~/.gemini/skills/using-leankg` + `GEMINI.md` | —                        |
| `kilo`      | `~/.config/kilo/kilo.json`                            | `~/.config/kilo/skills/using-leankg` + `AGENTS.md` | —                        |
| `antigravity` | `~/.gemini/antigravity/`                            | `~/.gemini/antigravity/skills` | —                        |
| `docker`    | — (no binary)                                         | —                          | MCP HTTP `:9699`         |

## Manual equivalents

| Tool | File | Snippet |
| ---- | ---- | ------- |
| Codex CLI | `~/.codex/config.toml` | `[mcp_servers.leankg]\ncommand = "leankg"\nargs = ["mcp-stdio", "--watch"]` |
| Cursor | `~/.cursor/mcp.json` | `{"mcpServers": {"leankg": {"command": "leankg", "args": ["mcp-stdio", "--watch"]}}}` |
| Claude Code | `~/.config/claude/settings.json` | same shape under `mcpServers` |
| OpenCode | `~/.config/opencode/opencode.json` | `{"mcp": {"leankg_dev": {"type": "local", "command": ["leankg", "mcp-stdio", "--watch"]}}}` |
| Gemini | `gemini mcp add leankg leankg mcp-stdio --watch --scope user` | — |

After install, index a project and verify:

```bash
leankg init && leankg index ./src
curl -sf http://localhost:9699/health   # Docker MCP only
```

> **Docker MCP project paths:** when talking to the containerized MCP on
> `:9699`, pass the **container mount** as `project=` (e.g. `/workspace`), never
> a Mac host path. See [AGENTS.md](AGENTS.md).
