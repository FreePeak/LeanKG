# Per-Language Quality Tiers (FR-C06 / US-CBM-C3)

Selective language expansion with quality tiers. Every language claims a
tier **before** it can be called "supported"; Tier 1 reference entries
are Go and TypeScript (FR-C06 "Go/TS first").

## Tier definitions

| Tier | Walk wiring | Entities | Calls | Typed resolve | Route / heritage | Quality bar |
|------|-------------|----------|-------|---------------|------------------|-------------|
| **1 — Full** | `find_files_sync` + bulk + incremental | tree-sitter | `call_expression`/`method_invocation` resolved (name/name_file_hint/typed) | `resolution_method=typed` when `typed_resolve` includes the lang | yes (Go/TS routes; heritage where applicable) | Unit + e2e test per feature; no known false-positive hotspot |
| **2 — Indexed** | `find_files_sync` + bulk + incremental | regex or partial tree-sitter | calls extracted (may be lower fidelity) | optional | partial | Fixture e2e test; documented fidelity gaps |
| **3 — Parser only** | **not** in `find_files_sync` | parser available | none | no | no | Parser unit test only; no walk claim |

## Reference entries

### Go — Tier 1 (reference)

- Extensions: `.go` — parser: tree-sitter-go — walk: full
- Entities: functions, structs, interfaces, imports
- Calls: `call_expression` / `selector_expression`; same-package +
  cross-file resolution; `resolution_method=typed` via in-process
  hybrid (`src/lsp/hybrid.rs`)
- Routes: chi/gin/echo (FR-B11), `http_calls` edges
- Tests: `tests/hybrid_lsp_e2e.rs`, extractor unit tests

### TypeScript — Tier 1 (reference)

- Extensions: `.ts`, `.tsx` (+ `.js`/`.jsx`) — parser: tree-sitter-typescript — walk: full
- Entities: functions, classes, imports, exports
- Calls: `call_expression`; cross-module typed resolve via hybrid
- Routes: express/fastify (FR-B11), `http_calls` edges
- Tests: `tests/hybrid_lsp_e2e.rs`, extractor unit tests

### Python — Tier 1 (FR-B06)

- Extensions: `.py`, `.pyi` — parser: tree-sitter-python — walk: full
- Entities: functions, classes, decorators, imports (`import` /
  `from … import`)
- Calls: bare `call` node (function field) + `attribute` receivers;
  `resolution_method=typed` when `typed_resolve` includes `py`
- Tests: `src/indexer/call_graph.rs` unit tests, `tests/hybrid_lsp_e2e.rs`

### Rust — Tier 1 (FR-B06)

- Extensions: `.rs` — parser: tree-sitter-rust — walk: full
- Entities: functions (`function_item`), structs, traits, imports
  (`use_declaration`)
- Calls: `call_expression`; `resolution_method=typed` when
  `typed_resolve` includes `rs`
- Tests: `src/indexer/call_graph.rs` unit tests, `tests/hybrid_lsp_e2e.rs`

### Swift / Objective-C — Tier 2

- Regex entities + tree-sitter calls (`src/indexer/swift.rs`,
  `src/indexer/objc.rs`); `.h` sniff for ObjC; typed resolve when
  `typed_resolve` includes `swift`/`objc`
- Tests: `tests/swift_objc_live_tests.rs` fixture e2e

### Vue / Svelte / SQL — Tier 2

- Regex extractors wired into walk (REL-032); no tree-sitter grammar

## Tier 3 candidates (incremental, FR-C05)

Candidates for future expansion with tier notes: **C/C++** (parser
present, missing walk wiring), **C#**, **Ruby**, **PHP**, **Perl**,
**R**, **Elixir** (parsers present, not wired). Windows build support
is a separate Platform C item (FR-C08, P3).

## Promotion rule (NFR-10)

A language may be called "supported" only after it has a tier entry and
the tier's quality bar is met. New languages are documented with a tier
**before** landing in `find_files_sync`.
