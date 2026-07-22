# Prefer-order narrative smoke (Wave 1b → discover-first)

**Date:** 2026-07-22  
**PRD:** [`docs/prd.md`](../prd.md) §5.9 · IDs `US-GF-14` / `FR-GF-22`

---

## Verdict

Agent-facing docs lead with **discover before connection verbs** for fuzzy / NL questions: `concept_search` → **`semantic_search`** before `query_graph`. Connection verbs (`shortest_path` / `explain_node` / `query_graph`) run only with known seeds or endpoints.

| File | Prefer-order section |
|------|----------------------|
| [`README.md`](../../README.md) | MCP & Agents → Prefer-order table |
| [`AGENTS.md`](../../AGENTS.md) | Prefer-order (discover before connection verbs) |
| [`CLAUDE.md`](../../CLAUDE.md) | LeanKG Tools Usage → Prefer-order |
| [`instructions/using-leankg/SKILL.md`](../../instructions/using-leankg/SKILL.md) | Discover before `query_graph` + BAN |
| [`instructions/leankg-tools.md`](../../instructions/leankg-tools.md) | Prefer-order table |
| [`instructions/cursor-rules/leankg-graph-first.mdc`](../../instructions/cursor-rules/leankg-graph-first.mdc) | Always-apply Cursor rule |
| [`scripts/install.sh`](../../scripts/install.sh) | Bootstrap + AGENTS template + Claude session hooks |

| Question type | First tools |
|---------------|-------------|
| Fuzzy / meaning / domain NL | `concept_search` → `semantic_search` → `search_code` |
| How A↔B? / known symbol / expand | `shortest_path` / `explain_node` / `query_graph` (after seeds) |

**BAN:** Do not call `query_graph` as the first NL discovery tool when embeddings/concepts may answer.
