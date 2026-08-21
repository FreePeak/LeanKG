# LeanKG Hackathon Log — branch `feature/hackathon`

> Running log for the 7-day continuous build loop. Every round appends here.
> SoT for roadmap context: `docs/roadmap-tracker.md` §6. Infra rule: remote PG only (`LEANKG_PG_URL`), NEVER Docker.

**Started:** 2026-08-22 · **Base:** main @ 0b2ee2cc

## Round log

### R0 — Setup (2026-08-22)
- [x] Worktree `.worktrees/hackathon` on `feature/hackathon`
- [x] Baseline gates: build --release green (shared cargo-cache target dir); lib tests reported 1043✅ at R0
- Note: `target/release/leankg` lives in the shared cache (`~/.cache/cargo-target/leankg-target/release/leankg`), not the worktree — global `~/.cargo/config.toml` sets `target-dir`

### R1 — Full MCP tool live sweep vs remote PG (2026-08-22)
**Setup:** remote Postgres only (`LEANKG_PG_URL`, rivesca.eu.db.rivestack.io, TLS verify-full). `init` → `migrate` → `index ./src`: **201 files, 10,105 elements, 66,554 relationships**, 18,881 call edges inline (docs phase for 224 files took ~55 min over the WAN link). Served via `mcp-http --port 9701` with `LEANKG_SKIP_FRESHNESS_CHECK=1` (boot freshness check false-negatives and would auto-reindex otherwise). Gotcha: CLI index keyed schema `leankg_p_2e2f737263` (literal `"./src"`) while MCP resolved canonical-root hash `leankg_p_29b8df3febee8339` → empty project until `project_path: "./src"` set in leankg.yaml.

**Results:** registry = **76 tools served** (+3 embeddings-gated absent): **51 PASS / 18 PASS_EMPTY / 3 FAIL_ERROR / 4 FAIL_TIMEOUT / 3 EXPECTED_UNAVAILABLE** across 128 individual calls (35 poisoned by wedge cascades). Latency p50 4.9s / p95 90s all calls; steady-state ops p50 3.2s / p95 12s-ish.

**Detail report:** [`docs/analysis/hackathon-sweep-R1.md`](docs/analysis/hackathon-sweep-R1.md)

**Top issues (one-liners):**
1. [P0] Hung tool handler (add_documentation big doc; agent_focus w/ persona) is never cancelled → blocks ALL subsequent calls with "tool X timed out after 30s" until restart (repro ×2)
2. [P0] Dynamic ontology writes (add_ontology_concept/workflow) vanish after server restart — delete then says "Element not found"; contradicts "survive YAML re-syncs"
3. [P0] update_knowledge always fails: "Failed to update knowledge entry: db error" (repro 2/2)
4. [P1] export_graph_snapshot/export_html/get_graph_report wrote artifacts into PARENT repo `.leankg/` instead of served root (39MB snapshot escaped)
5. [P1] Project identity mismatch CLI-vs-MCP schema keys (see gotcha above) — silent empty project after successful index
6. [P1] Boot freshness check false negative ("db modified: 0") triggers unwanted boot-time re-index without LEANKG_SKIP_FRESHNESS_CHECK=1
7. [P2] Hang trio over remote PG: get_context (>150s ×2), temporal_query (>150s), check_consistency (>90s) — suspected N+1 @ ~500ms/query
8. [P2] Remote-PG latency: query_graph 84.8s, get_impact_radius 62.9s, shortest_path 60.5s, mcp_index incremental 30.8s
9. [P2] agent_focus returns raw -32603 persona-not-found without fixture; hangs with fixture
10. [P2] index_prd silently creates 0 requirements from valid mini-PRD headings (errors: [])
