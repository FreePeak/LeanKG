# Graph-first install smoke (Wave 1c)

**Date:** 2026-07-21  
**PRD:** [`docs/prd.md`](../prd.md) §5.9 / §5.20 · IDs `US-GF-17` / `FR-GF-24`

---

## Verdict

`scripts/install.sh` installs always-on graph-first guidance for Cursor and Claude Code:

| Target | Artifact | Behavior |
|--------|----------|----------|
| Cursor | `.cursor/rules/leankg-graph-first.mdc` + `~/.cursor/rules/leankg-graph-first.mdc` | `alwaysApply: true` — three verbs + HTTP gate |
| Cursor | `~/.cursor/plugins/leankg/leankg-bootstrap.md` + sessionStart hook | Injects bootstrap at session start |
| Claude Code | `~/.claude/plugins/leankg/hooks/hooks.json` | PreToolUse nudge/block when `:9699` healthy |
| Claude Code | `pretooluse.mjs` | Blocks Bash grep/rg; nudges Read on source files (`LEANKG_STRICT_READ=1` to deny) |
| All agents | `instructions/using-leankg/SKILL.md` via `install_leankg_skill` | Three verbs before prefer-order |

Template source: [`instructions/cursor-rules/leankg-graph-first.mdc`](../../instructions/cursor-rules/leankg-graph-first.mdc)

---

## Smoke commands

```bash
# From LeanKG repo root (uses local templates)
LEANKG_REPO_ROOT="$PWD" bash scripts/install.sh cursor
test -f .cursor/rules/leankg-graph-first.mdc || test -f "$HOME/.cursor/rules/leankg-graph-first.mdc"

LEANKG_REPO_ROOT="$PWD" bash scripts/install.sh claude
test -f "$HOME/.claude/plugins/leankg/hooks/pretooluse.mjs"
grep -q 'pretooluse.mjs' "$HOME/.claude/plugins/leankg/hooks/hooks.json"

# Health gate (Docker MCP)
curl -sf --max-time 2 http://localhost:9699/health && echo ok
```

---

## Strict Read mode (optional)

```bash
export LEANKG_STRICT_READ=1
# Claude PreToolUse denies Read on indexed source extensions when :9699 is healthy
```

Default is **nudge** (additionalContext) so agents can still Read when graph context is insufficient.
