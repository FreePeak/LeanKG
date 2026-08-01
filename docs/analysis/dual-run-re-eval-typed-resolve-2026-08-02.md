# Dual-run re-evaluation after typed-resolve Phase (US-CBM-D3 / FR-D04)

**Date:** 2026-08-02
**Status:** Evaluation complete — no dual-run engine required
**IDs:** `US-CBM-D3`, `FR-D04` (tracker rows flip DONE)

## Question

Track D ("Dual-run escape hatch") asked whether LeanKG should keep a
second, CBM-style resolver running alongside the in-process indexer to
backfill `typed` edges. `FR-D04` re-evaluates that after "Phase 3 typed
resolve" — i.e. after the in-process hybrid resolver shipped.

## What Phase 3 actually shipped

| Layer | Status | Evidence |
|-------|--------|----------|
| `resolution_method` + `confidence` metadata on CALLS edges | DONE | `src/indexer/call_graph.rs` — `name` / `name_file_hint` / `unresolved` at index time |
| In-process hybrid typed resolve (Go/TS/Swift/ObjC) | DONE | `src/lsp/hybrid.rs` `apply_typed_resolve` — `resolution_method=typed`, `hybrid_tier=in_process`, no child process (`never_spawns_process_on_resolve`) |
| Cross-file type registry | DONE | `src/lsp/type_registry.rs` — `(module, name)`, unique-name, `(type, method)` lookups |
| Python + Rust join hybrid | DONE (FR-B06, this wave) | `src/lsp/hybrid.rs` `language_from_file` now maps `py` / `rs`; `init --with-lsp` writes `typed_resolve` incl. detected python/rust |
| External LSP bridge (spawned servers) | DONE | `src/lsp/{bridge,client,config}.rs` — `resolve_with_lsp` MCP, `leankg lsp-resolve` CLI, prefab `lsp:` block (REL-039) |

## Evaluation

### 1. The escape hatch is already documented and does not need a second engine

- `FR-D02` (documented CBM escape hatch when confidence low / language
  unsupported) is **DONE**. The hatch is: keep LeanKG-first skills, and use
  the external LSP bridge (`resolve_with_lsp` / `lsp-resolve`) when the
  in-process resolver cannot type an edge — no second index, no dual graph.
- `FR-D03` (no auto-install of CBM into `.mcp.json`) is **DONE** — the
  hatch is opt-in tooling, not a runtime dependency.

### 2. Dual-run would duplicate the registry work the indexer already does

The in-process hybrid resolver builds `TypeRegistry` from the same indexed
`CodeElement`s it upgrades (`src/indexer/mod.rs:849-858`). A dual-run engine
would parse the same files a second time through a second parser (CBM's
C-style resolver) to produce edges the registry already resolves in one
pass — pure duplication with no new recall for the languages in scope.

### 3. The one remaining gap is breadth, not mechanism

`apply_typed_resolve` still only runs when `typed_resolve` is active, and
the *registry* covers every indexed language (Python/Rust now included).
The real residual gap is call-edge extraction quality inside
`extract_calls_with_resolution` (same-file/method hints), not a missing
second resolver. That is a per-language extractor depth question
(`FR-B03..B05` deepen), not a dual-run architecture question.

### 4. Cost/benefit

| Option | Cost | Benefit |
|--------|------|---------|
| Dual-run second resolver (CBM-style) | second parser in the binary, ~same codebase to maintain, memory during index | none measured — registry covers T1/T2 languages today |
| Deepen per-language call extraction + hybrid registry | focused per-language work | typed edges without a second engine; agents keep one tool wall |

**Decision:** no dual-run engine. Keep the external-LSP escape hatch
(documentation + `resolve_with_lsp`), keep hybrid in-process resolve as the
default typed path, and treat residual call-graph depth as extractor work.

## Tracker

- `US-CBM-D3` → DONE (evaluation above)
- `FR-D04` → DONE (evaluation above)
- `FR-C06` (per-language quality tier template) → partially closed by
  `docs/tech-stack.md` tiers (US-CBM-C3); scale report stays open
