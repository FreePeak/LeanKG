# Three-verb narrative smoke (Wave 1b)

**Date:** 2026-07-21  
**PRD:** [`docs/prd.md`](../prd.md) §5.9 · IDs `US-GF-14` / `FR-GF-22`

---

## Verdict

Agent-facing docs now lead with **path · explain · query** before the full MCP catalog:

| File | Three-verb section |
|------|-------------------|
| [`README.md`](../../README.md) | MCP & Agents → Three verbs table |
| [`AGENTS.md`](../../AGENTS.md) | Three verbs first |
| [`CLAUDE.md`](../../CLAUDE.md) | LeanKG Tools Usage → Three verbs |
| [`instructions/using-leankg/SKILL.md`](../../instructions/using-leankg/SKILL.md) | Three verbs first |
| [`instructions/leankg-tools.md`](../../instructions/leankg-tools.md) | Three verbs table |
| [`scripts/install.sh`](../../scripts/install.sh) | Bootstrap + AGENTS template + Claude session hooks |

| Verb | MCP tool |
|------|----------|
| path | `shortest_path` |
| explain | `explain_node` |
| query | `query_graph` |

Discover prefer-order (`concept_search` → `semantic_search` → `search_code`) remains documented as secondary.
