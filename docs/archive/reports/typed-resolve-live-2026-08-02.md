# Python/Rust typed resolve (#191) live evidence — 2026-08-02

## Environment
- commit: 8c77b22b | binary: target/release/leankg 0.19.31 (local) | project: /tmp/leankg-typed-fixture (py + rs)

## Steps
1. `init --with-lsp` on py+rs fixture → `Prefab lsp: 45 servers; indexer.typed_resolve=go,ts`
2. `cargo test --release --lib typed_resolve` (config gating + bridge + hybrid tests)
3. `lsp-resolve` py/rs (no LSP server installed → clean fallback message)
4. `resolve_with_lsp` MCP → `found: false, reason: "no LSP server configured for this language (caller should fall back to tree-sitter typed resolve)"`
5. Index fixture → 4 elements, 7 relationships (all `contains`)

## Results
- `typed_resolve` gating tests: `off` disables all, `all` enables all, `go,ts` enables only go/ts — PASS (9/9 tests incl. hybrid upgrade for objc/swift/unresolved calls).
- `typed_resolve: all` in leankg.yaml accepted (CSV + aliases py/rs) — PASS config.
- LSP resolve without installed server → clean "No LSP server configured for 'python'... Falling back to tree-sitter" + MCP `reason: no LSP server configured... caller should fall back to tree-sitter typed resolve` — PASS graceful.
- **Python call edges: NOT extracted** in this build for `OrderService.fetch → process_order` — fixture shows only `contains` rels, no `calls`. The CallGraphBuilder supports `function_definition`/`call_expression` node kinds + `self` receiver resolution, but python bare-call-in-method edges didn't materialize in this index run. **Probe finding** (needs follow-up: verify python call extraction path in `extract_calls_with_resolution` for the `fetch → process_order` pattern).

## Tracker
- Python/Rust typed resolve (#191): PARTIAL. typed_resolve flag breadth + graceful LSP fallback PASS (9/9 tests). Python `calls` edge extraction for `fetch → process_order` FAIL in live fixture (only `contains` rels). Rust `calls` not probed with installed rust-analyzer (not installed). Follow-up: python call-graph extraction path.
