# MCP Tool Redundancy + Removal-Impact Audit (2026-07-20)

**Branch / PR:** `feature/mcp-embed-control`  
**Registry:** 83 live tools with `embeddings` feature (`tools/list`); 81 without (`embed_control` + `kg_semantic_context` are `#[cfg(feature = "embeddings")]`)  
**Matrix:** [`tests/redundant_tools_matrix.rs`](../../tests/redundant_tools_matrix.rs) — every **registered** tool classified exactly once (embeddings-gated rows `cfg`'d)  
**Scope:** Original audit was analysis-only; **`find_clones` hard-deleted 2026-07-20** (non-strategic + mega-graph refuse). Soft-deprecated `wake_up` / `search_by_environment` still registered.  
**Tests:** `cargo test --release --test redundant_tools_matrix` and `--features embeddings`
## Summary

| Category | Count | Action |
|----------|------:|--------|
| SoftDeprecated | 2 | Keep registered; hard-delete later after grace |
| Complementary | 24 | Keep both sides of overlap |
| DomainSpecific | 18 | Keep (different domain / prefer-order) |
| KeepUnique | 39 | Keep (`find_clones` removed from this bucket) |
| AlreadyRemoved (asserted absent) | 4 | `mcp_hello`, `mcp_impact`, `get_doc_for_file`, `find_clones` |

Prior chat triage listed overlaps but did not classify all 84 or quantify AI rules/skills blast radius. This report closes that gap.

## Prefer-order (agents)

**Search:** `concept_search` → `semantic_search` → `search_code`  
**Semantic context:** `semantic_search` → `kg_semantic_context` → `kg_context`  
**Overview:** `get_overview_context` (not `wake_up`; not `load_layer(L0)` alone)  
**File context (skills):** `get_context` preferred in `using-leankg`; `ctx_read` is complementary (compression modes)

## SoftDeprecated — removal impact

### `wake_up` → `get_overview_context`

| Surface | Hits | Notes |
|---------|------|-------|
| Schema | DEPRECATED (FR-SURF-04) | Points to `get_overview_context` |
| Repo `src/` / `tests/` / `docs/` | ~18 files | Mostly docs + matrix + smoke MUTATING list |
| `AGENTS.md` / `CLAUDE.md` | 0 direct agent prefer | Prefer-order uses search stack, not wake_up |
| `~/.cursor/skills/using-leankg` | **0** | Safe for agent migration |
| `~/.claude/skills/using-leankg` | **0** | |
| `~/.cursor/rules/*` (`leankg-first`, `mandatory-workflow-loop`) | **0** | Rules use `mcp_status` / `search_code` / `get_context` |
| Repo plugin skills (`.cursor-plugin`, `.opencode`, `.claude-plugin`) | **0** for wake_up | |
| Mega-graph risk | Low | Replacement is lighter than full `get_architecture` |

**Recommended action:** Soft-deprecate window continues. **Hard-delete candidate** after release note + smoke list cleanup (`scripts/mcp-smoke-tools.py` still lists `wake_up` under MUTATING). Migration must **not** use `load_layer(L0)` alone.

### `search_by_environment` → `env=` on primary search / `kg_*`

| Surface | Hits | Notes |
|---------|------|-------|
| Schema | DEPRECATED (FR-SURF-05) | Points to `env=` |
| Repo files | ~13 | Docs, matrix, smoke MEGA_GRAPH_HEAVY |
| AI skills / rules | **0** | Agents never directed here by using-leankg |
| Mega-graph risk | Medium if agents fall back to full-env scans | Prefer `env=` on paginated search tools |

**Recommended action:** Soft-deprecate continues. Hard-delete after confirming `env=` on `search_code` / `semantic_search` / `concept_search` / `kg_*` (already present in schemas). Remove from smoke heavy list when deleted.

## Complementary overlaps (keep — do not delete)

| Pair / group | Distinction | Skills impact |
|--------------|-------------|----------------|
| `get_context` ↔ `ctx_read` | Graph context vs compression-mode file read | **`get_context` heavily used** in using-leankg + rules (~72 files). Do **not** soft-deprecate `get_context`. `ctx_read` has 0 skill hits — niche keep. |
| `orchestrate` ↔ `query_graph` | Intent router+cache vs NL connection subgraph | Neither in using-leankg core path; keep both |
| `get_doc_structure` ↔ `get_doc_tree` | List vs hierarchy | Named in AGENTS/CLAUDE MCP tables; merge pending **FR-SURF-06** (mega-safe first) — **not** a delete |
| `get_callers` ↔ `get_call_graph` | Inbound vs outbound | Keep; not a subset |
| `get_nav_*` / `find_route` / `get_screen_args` | Android Navigation domain | Domain-specific keep |
| Cluster trio | list / context / SKILL.md | Keep |
| `mcp_init` ↔ `mcp_install` | Project vs client config | Keep (PRD: do not hide bootstrap) |
| Overview stack | `get_overview_context` / `load_layer` / `get_architecture` | Keep; wake_up soft-deprecated within this stack |

## Search / semantic stack (keep)

Prefer-order documented in AGENTS.md and tool schemas. **Do not merge.**  
`search_code` appears in skills/rules (~109 files) — load-bearing.

## Already removed (FR-SURF-03)

| Removed | Replacement |
|---------|-------------|
| `mcp_hello` | `kg_self_test` + `mcp_status` |
| `mcp_impact` | `get_impact_radius` |
| `get_doc_for_file` | `find_related_docs` |

Asserted absent by `redundant_tools_matrix`.

## HardDeleteCandidate (this pass)

**None executed.** Closest future candidates (skills/rules = 0 hits):

1. `wake_up`
2. `search_by_environment`

Prerequisite before hard delete: update smoke script + docs; optional skill note that overview uses `get_overview_context`.

## Full classification (84)

See `TOOL_CLASSIFICATION` in [`tests/redundant_tools_matrix.rs`](../../tests/redundant_tools_matrix.rs). Summary kinds:

- SoftDeprecated: `wake_up`, `search_by_environment`
- All other registered tools: KeepUnique | Complementary | DomainSpecific as coded in the matrix

## AI rules / skills checklist (mandatory)

| Surface | Result for SoftDeprecated candidates |
|---------|----------------------------------------|
| `~/.cursor/skills/using-leankg/SKILL.md` | No `wake_up` / `search_by_environment` |
| `~/.claude/skills/using-leankg/SKILL.md` | Same |
| `~/.cursor/rules/leankg-first.mdc` | Uses `search_code` / `get_context` only |
| `~/.cursor/rules/mandatory-workflow-loop.mdc` | Same |
| `~/.cursor/rules/AGENTS.md` | Tool tables; no soft-deprecate tools as preferred |
| Repo `.cursor/rules/skill-auto-invoke.mdc` | Maps to using-leankg skill |
| Repo `.cursor-plugin` / `.opencode` / `.claude-plugin` skills | Prefer `search_code` / `get_context` |

**Conclusion:** Soft-deprecating (and later hard-deleting) `wake_up` and `search_by_environment` does **not** require skill/rule patches for agent prefer-paths. Hard-deleting `get_context` or `search_code` would break agents — never recommend.

## Out of scope (unchanged)

- Implementing hard deletes
- FR-SURF-06 doc-structure merge code
- Separate chore PR

## Follow-up (optional later PR)

1. Hard-delete `wake_up` + `search_by_environment` after one release grace  
2. FR-SURF-06 mega-safe merge of doc structure tools  
3. Expand `docs/mcp-tools.md` full catalog (prefer-order section added in this PR; full 84-row table can grow incrementally)
