# Graphify vs LeanKG Competitive Comparison

**Date:** 2026-07-13
**Sources:** [Graphify-Labs/graphify](https://github.com/Graphify-Labs/graphify) (v8 / v0.9.13), LeanKG `docs/prd.md`, `README.md`, `docs/mcp-tools.md`
**Purpose:** Evidence for PRD v3.3 Graphify-inspired enhancements (US-GF / FR-GF)

---

## Positioning

| Dimension | Graphify | LeanKG |
|-----------|----------|--------|
| Core pitch | Turn any folder (code + docs + media) into a queryable concept graph; query instead of grep | Local-first code knowledge graph for AI agents: impact, context, traceability, token compression |
| Stack | Python + NetworkX + tree-sitter; optional LLM for docs/media | Rust + CozoDB/RocksDB + tree-sitter; MCP-first |
| Output model | Portable `graph.json` + `graph.html` + `GRAPH_REPORT.md` | Persistent DB (`.leankg` / RocksDB) + MCP tools + Web UI |
| Deploy | Stdio MCP or shared HTTP MCP Docker; Neo4j/FalkorDB push | `mcp-stdio` / `mcp-http`; Docker RocksDB multi-project compose |
| Stars (approx) | ~83k | Smaller niche product |

---

## Capability Matrix

| Capability | Graphify | LeanKG | Gap for LeanKG |
|------------|----------|--------|----------------|
| AST code extract (local, no LLM) | Yes (~36 grammars) | Yes (~10 full + partial) | Language breadth |
| Edge confidence tags | `EXTRACTED` / `INFERRED` / `AMBIGUOUS` on every edge | `resolution_method` + impact severity; not the same three-way label UX | Unify/surface edge provenance |
| Shortest path A→B | `graphify path A B` | No dedicated tool | **Missing** |
| Explain node | `graphify explain` (degree, community, neighbors) | Neighbors via deps/callers; no unified explain | **Missing** |
| NL scoped subgraph query | `graphify query "..."` | `orchestrate`, `semantic_search`, `search_code` | No path-oriented NL subgraph tool |
| God / hub nodes | Explicit in report | Hotspots in `get_architecture` | Weaker productization |
| Communities | Leiden + labels | Leiden clusters (`get_clusters`) | Parity (LeanKG has) |
| Architecture report artifact | `GRAPH_REPORT.md` (god nodes, surprises, suggested Qs) | Wiki / architecture MCP | Report genre missing |
| Rationale nodes (`# WHY:`, ADRs) | First-class | Annotations + docs; no auto WHY extraction | **Missing** |
| Docs in graph | Markdown + links | Doc indexer + traceability | LeanKG stronger on req↔code |
| Multi-modal (PDF/image/video) | Yes (LLM/semantic pass) | No | Out of core scope unless prioritized |
| SQL / live Postgres schema | Yes | Terraform/CI; no live DB introspect | Optional gap |
| Impact / blast radius | PR impact via `prs` | `get_impact_radius`, `detect_changes` | LeanKG stronger |
| Token compression | Budgeted query subgraphs | TOON, RTK, 8 read modes | LeanKG stronger |
| Ontology / business logic | Concept communities | Ontology + annotations + traceability | LeanKG stronger |
| Microservice topology | Package/MCP config nodes | `service_calls` + service UI | LeanKG stronger |
| Team deploy (multi-project HTTP) | Shared HTTP MCP + API key | RocksDB Docker multi-project | LeanKG stronger on multi-repo server |
| Commit-friendly graph artifacts | `graphify-out/` + merge driver | DB files (not merge-friendly) | Portable snapshot gap |
| Work memory / reflect | `save-result`, `reflect` → LESSONS | Metrics; no outcome feedback loop | **Missing** |
| PR triage / merge-order risk | `graphify prs --conflicts` | `detect_changes` only | **Missing** |
| Assistant install matrix | 20+ platforms | ~7–8 (Cursor, Claude, OpenCode, Gemini, Kilo, Codex, Antigravity) | Breadth gap |
| Always-on graph-first hooks | PreToolUse / AGENTS.md rules | Claude hooks + Cursor rules + skills | Near parity on Claude |

---

## What Graphify Does Better (LeanKG should enhance)

1. **Graph primitives for agents:** `path`, `explain`, `query` are the three verbs agents need for "how do X and Y connect?"
2. **Honest edges:** Every edge carries EXTRACTED vs INFERRED vs AMBIGUOUS.
3. **Report as product:** One markdown artifact that surfaces god nodes, surprising cross-module links, and suggested questions.
4. **Design rationale as graph:** WHY/NOTE/HACK comments and ADR refs become nodes linked to code.
5. **PR + community merge risk:** Graph communities drive review triage and conflict detection.
6. **Learning loop:** Record whether a Q&A path was useful; reflect into lessons that bias future queries.
7. **Portable team graph:** Commit `graph.json` so clones start warm; merge driver avoids conflict markers.

## What LeanKG Already Does Better (do not regress)

1. Token-optimized MCP responses (TOON / RTK / compression modes).
2. Requirement ↔ doc ↔ code traceability and business-logic annotations.
3. Microservice / DNS-aware service graphs (with confidentiality constraints).
4. Pre-commit risk (`detect_changes`) and severity-graded impact radius.
5. Persistent queryable store (CozoDB/RocksDB) vs ephemeral NetworkX JSON.
6. Multi-project RocksDB HTTP deploy for teams.
7. Optional embed → rerank → traverse semantic pipeline.

## Deploy Comparison (team server)

| Concern | Graphify | LeanKG |
|---------|----------|--------|
| Shared HTTP MCP | `python -m graphify.serve --transport http` | `leankg mcp-http` + Docker RocksDB compose |
| Auth | Bearer / API key | `MCP_HTTP_AUTH` |
| Multi-repo | Global graph registry (`graphify global`) | `LEANKG_PROJECT_DIRS` + registry |
| Storage | `graph.json` volume | RocksDB volume / per-project SQLite |
| Index freshness | Hook + `--update` / `--watch` | Watcher + hooks + auto-index on start |

**Verdict:** LeanKG deploy story is competitive. Priority is agent query UX and edge provenance, not rewriting deploy.

---

## Recommended MoSCoW (feeds PRD US-GF)

| Priority | Enhancement | Why |
|----------|-------------|-----|
| Must | Shortest path, explain node, NL subgraph query | Direct Graphify agent UX parity |
| Must | Edge confidence labels (EXTRACTED/INFERRED/AMBIGUOUS) | Trust + LLM reasoning quality |
| Must | God-node ranking surfaced in MCP/CLI | Architecture orientation |
| Should | GRAPH_REPORT.md generator | Shareable architecture brief |
| Should | WHY/NOTE/ADR rationale nodes | Explains *why* code exists |
| Should | PR impact + community conflict triage | Merge-order risk |
| Should | Work-memory / reflect loop | Compounds context quality |
| Could | Broader language extractors | Corpus coverage |
| Could | Portable graph snapshot + merge driver | Team commit workflow |
| Could | Live SQL schema ingest | App+DB one graph |
| Won't (now) | Full multi-modal PDF/video pipeline | Diverts from code-agent focus |

---

## References

- Graphify README: https://github.com/Graphify-Labs/graphify
- Graphify ARCHITECTURE.md: https://raw.githubusercontent.com/Graphify-Labs/graphify/v8/ARCHITECTURE.md
- Graphify BENCHMARKS.md: https://raw.githubusercontent.com/Graphify-Labs/graphify/v8/BENCHMARKS.md
- LeanKG enhancement analysis (other competitors): `docs/analysis/enhancement-analysis-2026-07-09.md`

---

*Last updated: 2026-07-13*
