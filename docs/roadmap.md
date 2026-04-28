# Roadmap

## Phase 2 -- Enhanced MCP Tools (GitNexus-Inspired)

Based on analysis of GitNexus architecture, LeanKG is adopting **precomputed relational intelligence**: structure computed at index time, not at query time. This converts LeanKG from a raw-edge graph query engine into a high-confidence context engine.

| Feature | Status | Description |
|---------|--------|-------------|
| **Confidence Scoring** | Planned | Add confidence scores (0.0-1.0) to relationships based on resolution quality. Impact analysis distinguishes "WILL BREAK" from "MAY BE AFFECTED" |
| **Pre-Commit Change Detection** | Planned | New `detect_changes` tool shows affected symbols and risk level before committing |
| **Multi-Repo Registry** | Planned | Global registry at `~/.leankg/registry.json` so one MCP config serves all projects |
| **Community Detection** | Planned | Auto-detect functional clusters using graph algorithms (Leiden-inspired) |
| **Cluster-Grouped Search** | Planned | `search_code` results include cluster membership for architectural context |
| **Enhanced Context** | Planned | `get_context` returns cluster, dependents_count, dependencies_count in one call |

## Phase 3 -- Intelligence Features

| Feature | Status | Description |
|---------|--------|-------------|
| **Cluster-Level Skills** | Planned | Auto-generate SKILL.md per functional cluster for targeted AI agent context |
| **MCP Resources** | Planned | Read-only URIs for repos, clusters, schema -- overview without tool calls |
| **Wiki Generation** | Planned | LLM-powered documentation from graph structure |

## Future Features

| Feature | Description |
|---------|-------------|
| **Semantic Search** | AI-powered code search using embeddings |
| **Security Analysis** | Detect vulnerable dependencies and patterns |
| **Cost Estimation** | Cloud resource cost tracking via pipeline data |

## Completed Features

| Feature | Version | Description |
|---------|---------|-------------|
| **Embedded Web UI** | v1.14 | Web UI embedded in LeanKG binary via Axum. No external server dependency |
| **Doc-to-Code Traceability** | v1.0 | Index docs/ directory, map doc references to code elements |
| **Business Logic Tagging** | v1.0 | Annotate code elements with business logic descriptions and link to features |
| **Incremental Indexing** | v1.0 | Track changes and extract only delta updates via file watcher |

## References

- [GitNexus Analysis](../analysis/gitnexus-analysis-2026-03-27.md)
- [GitNexus Enhancements PRD](../requirement/prd-leankg-gitnexus-enhancements.md)
- [Core PRD](../requirement/prd-leankg.md)
- [HLD](../design/hld-leankg.md)
