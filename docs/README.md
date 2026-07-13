# Knowledge Graph Documentation

## LeanKG

Lightweight, local-first knowledge graph for AI-assisted development.

## Index

| Document | Description |
|----------|-------------|
| **[prd.md](./prd.md)** | **Single source of truth** — consolidated PRD + HLD (v3.5-unified) |
| [roadmap.md](./roadmap.md) | Phased delivery status |
| [architecture.md](./architecture.md) | Lightweight C4 overview (details in `prd.md` §6) |
| [mcp-tools.md](./mcp-tools.md) | MCP tool reference |
| [cli-reference.md](./cli-reference.md) | CLI command reference |
| [analysis/graphify-comparison-2026-07-13.md](./analysis/graphify-comparison-2026-07-13.md) | Graphify competitive matrix |
| [analysis/enhancement-analysis-2026-07-09.md](./analysis/enhancement-analysis-2026-07-09.md) | Context enhancement analysis |

## Quick Links

- **Tech Stack**: Rust + CozoDB (SQLite / RocksDB) + tree-sitter
- **Features**: Code indexing, impact radius, ontology, MCP server, team env/incidents
- **Target**: AI coding tools (Cursor, OpenCode, Claude Code, Gemini, …)
- **PRD/HLD**: Edit only [`docs/prd.md`](./prd.md) — do not recreate split PRDs under `docs/requirement/` or `docs/design/hld-leankg.md`
