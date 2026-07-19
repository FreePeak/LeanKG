# REL-053 / FR-SURF-04/05 — MCP tool surface note (2026-07-19)

## Hard-delete recount (FR-SURF-03, prior PR #82)

| Checkpoint | `ToolRegistry::list_tools()` count |
|------------|-------------------------------------|
| Before hard-delete (baseline review 2026-07-18) | ≈ **85** |
| After removing `mcp_hello`, `mcp_impact`, `get_doc_for_file` | ≈ **82** |

Counts are from the live registry, not the obsolete “64 → 57” rumor.

## Soft-deprecate (this PR)

| Tool | Status | Replacement |
|------|--------|-------------|
| `wake_up` | Soft-deprecated (still listed) | `get_overview_context` — **not** `load_layer(L0)` alone |
| `search_by_environment` | Soft-deprecated (still listed) | `env=` on `search_code` / `semantic_search` / `concept_search` / `kg_*` |

Soft-deprecation does **not** shrink `tools/list` until a later hard-removal release.

## Hybrid LSP (FR-LSP-A..D / REL-039) — same release train

- `leankg init --with-lsp` writes prefab `lsp.servers` (catalog: gopls, typescript-language-server, pyright, …) and `indexer.typed_resolve: go,ts`.
- Indexing with `typed_resolve=go,ts` upgrades CALLS edges to `resolution_method=typed` via an **in-process** type registry (no gopls/tsserver spawn).
- External `resolve_with_lsp` / `leankg lsp-resolve` still use the JSON-RPC bridge when binaries are installed; empty yaml falls back to the catalog.
