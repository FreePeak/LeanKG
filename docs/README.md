# Knowledge Graph Documentation

## LeanKG

Lightweight, local-first knowledge graph for AI-assisted development.

## Index

| Document | Description |
|----------|-------------|
| [requirement/prd-leankg.md](./requirement/prd-leankg.md) | Product Requirements Document (EN) |
| [design/hld-leankg.md](./design/hld-leankg.md) | High Level Design Document |
| [analysis/implementation-status-2026-03-24.md](./analysis/implementation-status-2026-03-24.md) | MVP Implementation Status |
| [analysis/ab-testing-results-2026-04-08.md](./analysis/ab-testing-results-2026-04-08.md) | AB Testing Results (2026-04-08) |
| [analysis/mcp-server-test-results-2026-03-25.md](./analysis/mcp-server-test-results-2026-03-25.md) | MCP Server Test Results |
| [feature-testing-progress.md](./feature-testing-progress.md) | Feature Testing Progress |

## Quick Links

- **Tech Stack**: Rust + CozoDB (embedded SQLite-backed relational-graph) + tree-sitter
- **Features**: Code indexing, impact radius analysis, auto documentation, MCP server
- **Target**: AI coding tools (Cursor, OpenCode, Claude Code)
- **Benchmark**: [benchmark/README.md](../benchmark/README.md) | [AB Testing Results](./analysis/ab-testing-results-2026-04-08.md)