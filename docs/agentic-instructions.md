# LeanKG Agentic Instructions

LeanKG instructs AI coding agents to prefer its MCP tools **when the HTTP server is healthy**, then fall back to editor search. Prefer-order matches [docs/mcp-tools.md](mcp-tools.md).

## How It Works

1. Agent checks `curl -sf http://localhost:9699/health`
2. If healthy → `mcp_status(project=…)` then prefer-order discover → exact tools
3. If unhealthy → `Grep` / `Glob` / `Read` only (no `mcp_init`, no CLI burn)
4. Install embeds `instructions/using-leankg/SKILL.md` and agent docs via `scripts/install.sh`

## Setup

### Docker (Recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/docker-up.sh | bash
curl http://localhost:9699/health
```

Point MCP at `http://localhost:9699/mcp?project=/workspace` (container mount, not a host Mac path).

Multi-project: set `LEANKG_MCP_PROJECT=/workspace-other` (or another container bind) when running `install.sh` for Cursor.

### Local agent install

```bash
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- cursor
# or: claude | opencode | gemini | …
```

From a checkout:

```bash
bash scripts/install.sh cursor
```

## What Agents Should Do

| Task | Prefer (HTTP up) | Fallback (HTTP down / empty) |
|------|------------------|------------------------------|
| NL / domain “where is auth?” | `concept_search` → `semantic_search` | Grep |
| Exact symbol name | `search_code` / `find_function` | Grep |
| Read hit | `get_context` | Read |
| Blast radius | `get_impact_radius` | Manual |
| Tests | `get_tested_by` | Grep |

**Decision flow:**

```
User asks about codebase
  → curl :9699/health
       fail → Grep/Glob/Read (STOP LeanKG)
       ok   → mcp_status(project=/workspace)
            → concept_search / semantic_search / search_code
            → get_context / impact / deps on hits
            → if empty → Grep/Glob/Read
```

## Prefer-order (FR-SURF-02)

- Search: `concept_search` → `semantic_search` → `search_code`
- Semantic context: `semantic_search` → `kg_semantic_context` → `kg_context`
- File context: `get_context` (default); `ctx_read` for compression modes

## Canonical skill

Source of truth: [`instructions/using-leankg/SKILL.md`](../instructions/using-leankg/SKILL.md)

Shared local installs often symlink `~/.cursor/skills` → `~/.ai-tools/skills`. `install.sh` refreshes `using-leankg` from that canonical file (or GitHub raw) and no longer keeps the old “STRICT ENFORCEMENT / mcp_init / RTK” template.
