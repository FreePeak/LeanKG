# REL-062: MCP Wave 1a hard-delete evidence

**Date:** 2026-07-21  
**Branch:** `feature/mcp-surf-hard-delete`  
**Tracker:** `US-SURF-06`, `US-SURF-07`, `FR-SURF-07`..`FR-SURF-11`, `REL-062`

## Summary

Hard-deleted MCP tools `wake_up` and `search_by_environment` from `ToolRegistry` and handlers. Kept internal `GraphEngine::wake_up_summary()` for `get_overview_context`. Synced agent-facing docs, install hooks, smoke lists, and plugin manifests to the reduced prefer-order.

## `tools/list` delta

| Surface | Count | `wake_up` | `search_by_environment` | `get_overview_context` |
|---------|------:|-----------|-------------------------|------------------------|
| Docker MCP `:9699` (pre-redeploy, 2026-07-21) | 84 | present | present | present |
| Worktree binary `mcp-http :9700` (post-change) | 82 | **absent** | **absent** | present |

Delta: **−2** registered tools. Docker `:9699` requires image/binary redeploy to pick up the new registry.

## Live checks (worktree `target/release/leankg mcp-http --port 9700`)

```text
tools/list → wake_up ABSENT, search_by_environment ABSENT, get_overview_context OK
tools/call wake_up → unknown-tool / error (not in registry)
tools/call get_overview_context → success
```

## Unit / matrix tests

```bash
cargo test --release --test redundant_tools_matrix
cargo test --release --test redundant_tools_matrix --features embeddings
cargo test --release --test mcp_tools_redundancy_tests
cargo test --release -p leankg --lib mcp::tools
```

All green in worktree on 2026-07-21.

## Grep-clean agent surfaces (preferred refs)

Checked paths from Wave 1a plan; only **hard-removed** list mentions remain:

```bash
rg -n 'wake_up|search_by_environment|mcp_hello|mcp_impact|get_doc_for_file|find_clones' \
  AGENTS.md CLAUDE.md docs/mcp-tools.md docs/agentic-instructions.md \
  instructions/using-leankg scripts/install.sh scripts/mcp-smoke-tools.py \
  .cursor-plugin .opencode .claude-plugin
```

No preferred-call guidance for deleted tools outside explicit forbidden / hard-removed sections.

## Prefer-order (canonical post–Wave 1a)

| Chain | Tools |
|-------|-------|
| Overview | `get_overview_context` → optional `load_layer` → `get_architecture` |
| Search | `concept_search` → `semantic_search` → `search_code` |
| Env | `env=` on search / `kg_*` |
| File context | `get_context` |

**Hard-removed set:** `mcp_hello`, `mcp_impact`, `get_doc_for_file`, `find_clones`, `wake_up`, `search_by_environment`

## Smoke / install

- `scripts/mcp-smoke-tools.py`: removed `wake_up` from `MUTATING`, `search_by_environment` from `MEGA_GRAPH_HEAVY`
- `scripts/install.sh`: `forbidden_actions` + prefer-order hooks updated (overview + `env=`)
