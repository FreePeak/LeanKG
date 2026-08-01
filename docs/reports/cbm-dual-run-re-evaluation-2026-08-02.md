# FR-D04 / US-CBM-D3 — dual-run re-evaluation after typed-resolve Phase — 2026-08-02

## Scope

Re-evaluate the CBM "dual-run" option (optionally running
`codebase-memory-mcp` alongside LeanKG for workloads LeanKG does not
copy soon) **after** the hybrid typed-resolve phase.

Original Track D (from `prd-structural-parity-cbm.md` §15):

- FR-D01 — Skills remain LeanKG-first (DONE)
- FR-D02 — Documented CBM escape hatch when confidence low / lang
  unsupported (DONE, see §Conclusion)
- FR-D03 — No auto-install CBM into default freepeak `.mcp.json` (DONE)
- FR-D04 — Re-evaluate dual-run after Phase 3 (this doc)

## Phase-3 typed-resolve outcome (what changed)

LeanKG's typed-resolve phase landed in full (PRD §5.13, tracker
FR-LSP-A..D + REL-039):

| Capability | Status |
|------------|--------|
| In-process hybrid typed resolve (no spawn) | DONE — `src/lsp/hybrid.rs` + `src/lsp/type_registry.rs` |
| Go / TS typed CALLS edges (`resolution_method=typed`) | DONE — `typed_resolve=go,ts` |
| Python / Rust typed resolve | DONE (FR-B06, this batch) — `typed_resolve=py,rs` |
| Swift / Objective-C typed resolve | DONE — `typed_resolve=swift,objc` |
| External LSP bridge (gopls / tsserver / pyright / …) | DONE — `resolve_with_lsp` MCP + `leankg lsp-resolve` |
| Prefab `lsp:` block via `leankg init --with-lsp` | DONE (REL-039) |
| Call-edge resolution on index (default name resolve) | DONE — `resolve_call_edges_inline` + hybrid upgrade |

Original CBM dual-run trigger was: "when confidence low / lang
unsupported". Post-Phase-3:

- **Covered languages** now produce `typed` edges in-process for
  Go/TS/Python/Rust/Swift/ObjC — the languages that drove the
  "unsupported lang" case.
- **Low-confidence case:** edges that stay `unresolved` are *kept*
  (never dropped — FR-B07 fail-soft) and are visible via
  `get_architecture` resolution buckets; the escape hatch for those is
  the external LSP bridge, not a second indexer.

## Conclusion: dual-run NOT adopted

1. **Dual-index every repo by default** was already a non-goal
   (`prd-structural-parity-cbm.md` §13.4).
2. CBM's remaining exclusives (Pure-C speed, 158 langs, clone
   MinHash/LSH) are explicitly **Won't Have** (v3.6.2 — semantic HNSW
   only; 158-language parity not pursued).
3. The dual-run **confusion risk** (two sources of truth for call
   edges) outweighs the residual gap, which is now the documented
   escape hatch below.
4. Business-context moat (ontology, knowledge, env, incidents,
   Android, req↔code) is LeanKG-only; running CBM in parallel would
   not add it.

## Documented escape hatch (FR-D02, retained)

When an agent needs raw indexing speed on very large C/C++ codebases
(the one CBM strength in `docs/competitive-analysis.md`), the
documented guidance is: consider `codebase-memory-mcp` as a
**separate, opt-in** MCP server for that workload — never auto-installed
into `.mcp.json` (FR-D03) and never as the default resolver.

## Tracker

- FR-D04: DONE (re-evaluated; decision: no dual-run)
- US-CBM-D3: DONE (tied to FR-D04 evaluation)
