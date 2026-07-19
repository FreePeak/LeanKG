# Roadmap

## Phase 1 -- Structural Parity vs codebase-memory-mcp

**Canonical PRD:** [`prd.md`](prd.md) Section 3.11 / 5.10 (v3.5-unified SoT)

| Feature | PRD ID | Status | Description |
|---------|--------|--------|-------------|
| `resolution_method` + confidence on calls | FR-B01, FR-B02 | **Done** | `name` / `name_file_hint` / `unresolved`; `typed` reserved |
| Default call resolve on index | FR-A05 / FR-B07 | **Done** | Soft-fail name resolve; never blocks index |
| `get_architecture` MCP | FR-B20 | **Done** | Languages, entry points, routes, clusters, hotspots, counts |
| `get_graph_schema` MCP | FR-B21 | **Done** | Element + relationship type counts |
| `find_dead_code` MCP | FR-B23 | **Done** | Zero callers, no `tested_by`; `min_lines` filter |
| Route + `http_calls` extractors | FR-B10..B12, B14 | **Done** | Go chi/gin/echo; TS express/fastify (`route_extractor.rs`) |
| `typed_resolve` feature flag | FR-B08 | Not done | With typed resolve (Phase 3) |
| Typed resolve Go/TS | FR-B03..B05 | Not done | Hybrid-LSP-style MVP |
| Architecture token budget | FR-B22 | **Done** | Per-section `max_items` truncation + `truncated_sections` metadata |
| Clones / cross-repo | FR-B30..B33 | Not done | Rel-type stubs only in `models.rs` |
| Event edges | FR-B15 | Not done | EMITS / LISTENS_ON |
| 3D graph UI (Track E) | FR-E* | Not done | New `graph-ui/`; keep 2D `ui/` |

## Phase 2 -- Enhanced MCP Tools (GitNexus-Inspired)

> Status corrected 2026-07-13: most items below already shipped in earlier releases; remaining work is Cluster SKILL.md / MCP Resources.

| Feature | Status | Description |
|---------|--------|-------------|
| **Confidence Scoring** | **Done** | Numeric confidence + severity on impact |
| **Pre-Commit Change Detection** | **Done** | `detect_changes` MCP tool |
| **Multi-Repo Registry** | **Done** | Global registry + multi-project HTTP |
| **Community Detection** | **Done** | Leiden clusters + `get_clusters` |
| **Cluster-Grouped Search** | **Done** | Search includes cluster context |
| **Enhanced Context** | **Done** | `get_context` / `get_review_context` / `orchestrate` |
| **Cluster-Level Skills** | Planned | US-GN-07 |
| **MCP Resources** | Planned | US-GN-08 |

## Phase 3 -- Intelligence / Typed Resolve

| Feature | Status | Description |
|---------|--------|-------------|
| **Typed call resolve Go+TS** | Planned | FR-B03..B05, FR-B08 |
| **CBM comparison + scale report** | Planned | FR-C06, FR-C07, FR-D04 |
| **Wiki Generation** | **Done** | Markdown wiki from structure |
| **Wake-up context** | **Done** | `wake_up` MCP |

## Phase 4 -- Graphify Agent Graph Parity

PRD: [`prd.md`](prd.md) Section 3.10 · Analysis: [`analysis/graphify-comparison-2026-07-13.md`](analysis/graphify-comparison-2026-07-13.md)

| Feature | PRD ID | Status | Description |
|---------|--------|--------|-------------|
| Shortest path | US-GF-01 / FR-GF-01..02 | Planned | MCP `shortest_path` + CLI `leankg path` |
| Explain node | US-GF-02 / FR-GF-03..04 | Planned | Single-call node dossier |
| NL subgraph query | US-GF-03 / FR-GF-05..06 | Done | MCP `query_graph` + CLI `graph-query` / `query --kind subgraph` |
| Edge provenance labels | US-GF-04 / FR-GF-07..09 | Planned | EXTRACTED / INFERRED / AMBIGUOUS |
| God-node ranking | US-GF-05 / FR-GF-10..12 | Planned | Index-time hub importance |
| GRAPH_REPORT.md | US-GF-06 / FR-GF-13..14 | Planned | Architecture brief |
| Rationale nodes | US-GF-07 / FR-GF-15..16 | Planned | WHY/NOTE/HACK + ADR |
| PR community triage | US-GF-08 / FR-GF-17..18 | Planned | Merge-order risk |
| Work-memory reflect | US-GF-09 / FR-GF-19 | Planned | Query outcomes → lessons |
| Portable snapshot | US-GF-11 / FR-GF-20 | Planned | Merge-friendly export |

## Future Features

| Feature | Description |
|---------|-------------|
| **Semantic Search** | Embeddings pipeline (optional feature; Docker OOTB = FR-C01) |
| **Language breadth** | Selective expansion (US-GF-10 / FR-C05) — not 158-lang chase |
| **Live SQL schema** | US-GF-12 |
| **3D graph galaxy UI** | Track E (US-CBM-E*) |
| **Security Analysis** | Vulnerable dependency patterns |
| **Cost Estimation** | Cloud resource cost via pipeline data |

## Completed Features

| Feature | Version | Description |
|---------|---------|-------------|
| **Structural aggregators** | 0.17.x | `get_architecture`, `get_graph_schema`, `find_dead_code` |
| **HTTP routes** | 0.17.x | `route` + `http_calls` extractors |
| **Embedded Web UI** | v1.14 | 2D Web UI embedded via Axum |
| **Doc-to-Code Traceability** | v1.0 | Index docs/, map references |
| **Business Logic Tagging** | v1.0 | Annotations linked to features |
| **Incremental Indexing** | v1.0 | Delta updates via watcher |

## References

- [Consolidated PRD + HLD](prd.md) (single source of truth)
- [Graphify Comparison](analysis/graphify-comparison-2026-07-13.md)
- [Architecture overview](architecture.md)
- [MCP Tools](mcp-tools.md)
- [CLI Reference](cli-reference.md)
