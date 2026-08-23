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

### R2 — Bug fixes from sweep (2026-08-22)
Merged fix-mcp-layer (4 commits) + fix-engine-layer (3 commits); backend.rs identity logic unified in manual merge (0d4715aa).
| Bug | Fix | Commit |
|---|---|---|
| update_knowledge db error | pk_for_table knowledge_entries → ON CONFLICT upsert | 756b9292 |
| mcp_index_docs 30s timeout | spawn_blocking + 300s tool floor | a53c65fa |
| project-key mismatch CLI/MCP | canonical_project_root unification | ea74cd89 |
| exports escaping project dir | resolve_out_path anchored at request root | cd60aff2 |
| dynamic ontology "loss" | schema_candidates + legacy adoption; readonly URL sslmode fix | 004d6099 |
| hang trio N+1 (get_context 72s→2.5s) | batched IN-list hydration | e59b60e5 |
| agent_focus executor wedge | targeted queries + bounded pool wait (10s fail-fast) | 3f070c8f |
Gates: lib 1053✅ · fmt✅ · clippy✅ · build 0 warnings.
Latency after: get_context 72s→2.1-2.8s; temporal 4.5s; check_consistency 6-7s; agent_focus wedge gone (13ms warm).

### R3 — Feature wave complete (2026-08-22)
All 6 backlog items landed and merged to `feature/hackathon` @ d0dd213e → PR #247:
| Item | Feature | Evidence |
|---|---|---|
| H1 | leankg connect (4 clients) | 43 tests, fake-HOME live proof |
| H2 | ENT-1 audit log (hash chain) | chain verified live "5 entries"; <2ms overhead test |
| H3 | npm version parity | CI guard + wrapper 0.17.9→0.26.0 |
| H5 | quickstart smoke | 88s total vs 300s budget |
| H11 | export --markdown | 12,864-line deterministic docs |
| H9 | doctor --deep | 21 unit + 2 live-PG integration tests |

**H9 hardening found 3 real bugs during live verification:**
1. Synthetic `ontology://` URIs in code_elements counted as stale file paths → freshness check now skips URI schemes (+ samples in FAIL detail)
2. Path comparison broke on macOS /tmp symlink + relative spellings → normalize both sides against canonicalized root
3. `init_db_readonly` public-schema fallback served FOREIGN project rows to doctor → added `init_db_readonly_strict`; run_deep resolves root/src/yaml identities explicitly
Also: tracing logs moved to stderr so `--format json` stdout stays machine-parseable.

Gates: lib **1147✅** · fmt ✓ · clippy ✓ · build 0 warnings.

### Cycle-2 R1 — Full live re-sweep vs remote PG (2026-08-23)
Report: `docs/analysis/hackathon-sweep-R2.md` · binary `$SWEEP` 0.26.0 @ 2ceb316d · port 9721 · schema `leankg_p_970a9b30ff7448d7`.

**Counts:** 72 PASS / 1 PASS_EMPTY / 3 FAIL_TIMEOUT / 0 FAIL_ERROR / 3 EXPECTED_UNAVAILABLE · 85 calls, **0 cascade-poisoned** (R1: 35) · registry 76 tools identical to R1.
**Latency vs R1:** p50 4,896ms → **2,746ms** (−44%) · p95 90,002ms → **45,003ms** (−50%) · steady p50 3,172 → 2,446ms. Heavy-graph tail still 44–62s (shortest_path/query_graph/impact).
**Regression matrix (7 cycle-1 fixes):** update_knowledge roundtrip ✅ · mcp_index_docs 20.2s ✅ · get_context 4.6–18.9s ✅ · check_consistency ❌ (>150s fresh-server, populated corpus) · temporal_query ❌ (>120s) · agent_focus ⚠️ partial (hangs >60s but no wedge on fresh boot; wedge only via internal-watchdog path, N4) · exports anchored in project ✅ · dynamic ontology survives reopen + deletable ✅ (PG rows verified `source:'dynamic'`).
**Audit (ENT-1):** `audit export` → 107 entries; `audit verify` → "OK: audit chain intact (107 entries verified)", exit 0.
**CLI:** `export --markdown` exit 0, 4,068 lines. `doctor --deep` exit 2 (target ≤1): freshness WARN clean of `ontology://` noise (H9 hardening holds) but genuine FAILs — orphaned relationships 432/1000 sampled, duplicate doc-section QNs ×10.
**New issues:** N1 `index` regenerates leankg.yaml dropping `project.project_path` → identity split returns (empty-schema serving observed twice); N2 legacy-schema adoption hijacks fresh index when relative project_path + stale legacy schema exist; N3 launcher-CWD leaks into identity (restart from parent repo pinned foreign schema); N4 wedge reachable via internal-watchdog expiry path (client-abandoned hangs safe); N5 doctor orphan/dup findings above; N6 cluster_skill parent-path bleed carried over. R1 #10 index_prd zero-work + orchestrate filename-parse error also carried over.
Note: R3's warm-benchmarks (temporal 4.5s / check_consistency 6–7s / agent_focus 13ms) did NOT hold at full live scale on remote PG with 13.9k elements — hang trio is corpus-scale-dependent (empty-corpus probes ~7s each pass).

### Cycle 2 — re-sweep + identity/perf/data-quality waves (2026-08-22)
**R1 re-sweep:** 72 PASS / 1 PASS_EMPTY / 3 FAIL_TIMEOUT / **0 FAIL_ERROR** (R1: 3) · 0 cascade-poisoned (R1: 35) · p50 4896→2746ms, p95 90002→45003ms · audit chain intact (107 entries). Regression matrix 4 PASS/2 FAIL/1 PARTIAL. Report: docs/analysis/hackathon-sweep-R2.md.

| Wave | Fixes | Live evidence |
|---|---|---|
| R2a identity | yaml anchor preservation (serde skip_serializing bug); legacy schema adopted only when preferred empty; --project canonicalized at entrypoint; .leankg store outvotes stale root yaml | search_code count>0 from FOREIGN cwd after yaml corruption |
| R2b perf/wedge | token_budget truncate O(n²)→O(n) single-pass; agent_focus IN-list chunks 500→10k; 120s watchdog floor for graph scans | check_consistency **211s→5.8s**; temporal 12min-hang→7s; starvation canaries answer during heavy calls |
| R2c data quality | drop unresolvable call edges at generation; prune_dangling_relationships valve; docs hierarchy synthetic dir elements; per-doc heading QN #k counters; collapse dup file rows | doctor exit 2→1: orphans 432/1000→**0/72,699**; dup QNs 10→**0/14,091** |

Gates @ cycle-2 close: lib **1169✅** · fmt/clippy/build clean.
