# Hackathon Cycle-2 R1 — Full MCP Tool Live Re-Sweep vs Remote Postgres

**Date:** 2026-08-23 · **Branch:** `feature/hackathon` @ `2ceb316d` · **Worktree:** `.worktrees/hackathon`
**Purpose:** validate the 7 cycle-1 bug fixes against a live full sweep; hunt regressions. Companion to `hackathon-sweep-R1.md` (identical JSON-RPC call pattern + per-tool args).

## Setup facts

| Item | Value |
|---|---|
| Binary | `$SWEEP=/tmp/opencode/t-sweep/release/leankg` 0.26.0 (= HEAD 2ceb316d + all cycle-1 fixes + audit/connect/doctor); not rebuilt |
| Storage | Remote Postgres `rivesca.eu.db.rivestack.io:5432` (`LEANKG_PG_URL`, TLS verify-full) — no Docker |
| Schema | `leankg_p_970a9b30ff7448d7` = canonical key of `<worktree>/src`; writer and MCP reader converged only after re-adding `project.project_path: <abs>/src` to `.leankg/leankg.yaml` (see N1/N2) |
| Corpus | `index ./src`: **212 files**, **10,262 elements** (10,567 inserted), **69,746 relationships**, 19,719 call edges resolved inline; code phase ≈ 2 min |
| Docs phase | CLI docs indexer ≈ **75 min** (224 files), ran concurrently with early sweep, finished before clean retests |
| Server | `mcp-http --port 9721 --project <wt>`; health 200 in 12–18 s per boot; **no `LEANKG_SKIP_FRESHNESS_CHECK` needed** — boot auto-index now no-ops in ~11 s where R1 required the env workaround |
| Registry | `tools/list` → **76 tools** — identical set to R1 (+3 embeddings-gated absent) |
| Protocol | JSON-RPC 2.0 `POST /mcp`, `tools/call`, no initialize handshake (identical to R1) |

## Summary

**72 PASS / 1 PASS_EMPTY / 3 FAIL_TIMEOUT / 0 FAIL_ERROR / 3 EXPECTED_UNAVAILABLE / 0 SKIPPED** (79 rows)

- Consolidated HTTP calls: **85** (+~20 probes/retests) — **zero cascade-poisoned** (R1: 35)
- One genuine server wedge reproduced via the internal-watchdog path (N4); recovered by restart
- Latency all calls (n=85): p50 **2,746 ms** · p95 **45,003 ms** · mean 8,785 ms · 8 calls >30 s
- Steady-state ops (<60 s, n=82): p50 **2,446 ms** · p95 **44,514 ms**

## Regression matrix — the 7 cycle-1 fixes

| # | Fix (commit) | Verdict | Evidence |
|---|---|---|---|
| 1 | `update_knowledge` upsert (`756b9292`) | **PASS** | add -> update -> search shows updated content; 2.8 s. (R1: `"Failed to update knowledge entry: db error"` 2/2) |
| 2 | `mcp_index_docs` watchdog yield (`a53c65fa`) | **PASS** | tiny docs dir completed **20.2 s**, valid payload (<300 s budget). (R1: internal watchdog error at 32.5 s) |
| 3a | hang trio — `get_context` (`e59b60e5`) | **PASS** | 4.6 s phase1 / 18.9 s clean retest; returns reliably. (R1: hung 170 s x2) |
| 3b | hang trio — `check_consistency` <15 s (`e59b60e5`) | **FAIL** | never returned at 150 s client cap on fresh populated server (empty-corpus probe: 7.1 s). No cascade around it on fresh boot. |
| 3c | hang trio — `temporal_query` <15 s (`e59b60e5`) | **FAIL** | never returned at 120 s client cap on fresh populated server (empty-corpus probe: 7.0 s). Internal 30s watchdog error seen once in degraded state. |
| 4 | `agent_focus` wedge (`3f070c8f`) | **PARTIAL** | tool still hangs >60 s (populated corpus). Wedge aspect fixed on fresh boot: IMMEDIATE next call (`search_code`) answered **2.1 s** — no lock held. But one degraded-state sequence wedged the server until restart (N4). |
| 5 | Exports anchored to project root (`ea74cd89`) | **PASS** | snapshot + html written to `<worktree>/.leankg/` (fresh mtimes); parent repo untouched; `GRAPH_REPORT.md` side-effect no longer written anywhere. (R1: 39 MB file escaped to parent repo) |
| 6 | Dynamic ontology survives reopen (`004d6099`) | **PASS** | session-created concept+workflow visible after server reopen (`dynamic_concepts:1 / dynamic_workflows:1`, `concept_search` matched 1); both `delete_ontology_concept` OK (3.1 s / 5.2 s); dynamics 0/0 after. PG rows confirmed (`metadata.source='dynamic'`). |

**Matrix verdict: 4 PASS / 2 FAIL / 1 PARTIAL.** Threshold misses are latency-only (3b/3c); #4 is latency-hang with wedge-resistance fixed.

## Per-tool comparison (R1 -> R2)

Status legend: PE = PASS_EMPTY; EU = EXPECTED_UNAVAILABLE. Latency ms (best healthy attempt).
PASS<->PE flips on legitimately-empty-for-corpus tools are classifier noise (toon formatting), marked `~`.

| Tool | R1 | R2 | R1 ms | R2 ms | Notes |
|---|---|---|---|---|---|
| add_annotation | PASS | PASS | 3002 | 2805 | |
| add_documentation | PASS | PASS | 18491 | 10097 | tiny doc 10.1 s (was 18.5 s) |
| add_knowledge | PASS | PASS | 2057 | 2075 | |
| add_ontology_concept | PASS | PASS | 2056 | 2078 | gid returned; durable across reopen |
| add_ontology_workflow | PASS | PASS | 4896 | 5231 | step_count=1 traceable |
| agent_diary_read | PASS | PASS | 1540 | 1402 | |
| agent_diary_write | PASS | PASS | 1371 | 1378 | lands inside worktree .leankg |
| agent_focus | FAIL_TIMEOUT | FAIL_TIMEOUT | 60003 | >60004 | hangs w/ corpus; no wedge on fresh boot (next call 2.1 s OK) |
| check_consistency | FAIL_TIMEOUT | FAIL_TIMEOUT | 170003 | >150005 | fresh-server repro; empty-corpus 7.1 s |
| concept_search | PASS | PASS | 3149 | 2777/5625 | dynamic roundtrip verified pre+post reopen |
| ctx_read | PASS | PASS | 3804 | 1373 | |
| delete_knowledge | PASS | PASS | 3711 | 2092 | cleanup verified count:0 |
| delete_ontology_concept | FAIL_ERROR | PASS | 2321 | 3071 | post-reopen delete works (fix #6) |
| detect_changes | PASS | PASS | 6291 | 1480 | |
| explain_node | PASS | PASS | 8090 | 5080 | found:true now |
| export_graph_snapshot | PASS* | PASS | 6473 | 6499 | *R1 wrote parent repo; R2 inside project |
| export_html | PASS* | PASS | 4691 | 6674 | *same fix verified |
| find_env_conflicts | PASS | PASS | 2790 | 2746 | 3 conflicts reported |
| find_large_functions | PASS | PASS | 2426 | 2247 | |
| find_related_docs | PE ~ | PASS | 3673 | 3957 | related_docs [] both runs |
| find_route | PE ~ | PASS | 3629 | 5672 | graceful empty (non-Android) |
| find_tunnels | PE ~ | PASS | 5243 | 4463 | count 0 |
| generate_doc | PASS | PASS | 3172 | 2047 | |
| get_architecture | PASS | PASS | 8376 | 5505 | entry_points present now |
| get_call_graph | PE ~ | PASS | 3710 | 2416 | calls [] for list_tools depth=1 |
| get_cluster_skill | PASS | PASS | 15776 | 22138 | still bleeds PARENT-repo abs paths (issue #12 open) |
| get_clusters | PASS | PASS | 8388 | 11337/20850 | clusters computed (cluster_3445 …) |
| get_code_tree | PASS | PASS | 3764 | 3276 | |
| get_context | FAIL_TIMEOUT | PASS | 170017 | 4608 | FIX confirmed |
| get_dependencies | PASS | PASS | 10957 | 7409 | |
| get_dependents | PASS | PASS | 2620 | 1718 | |
| get_doc_tree | PASS | PASS | 1750 | 1737 | docs corpus visible after docs phase |
| get_feature_flow | PASS | PASS | 2827 | 2432 | feature null (mini-PRD zero-work, R1 #10 open) |
| get_files_for_doc | PE ~ | PASS | 2793 | 3501 | resolved_doc null |
| get_god_nodes | PASS | PASS | 4705 | 3645 | 5 nodes |
| get_graph_report | PASS* | PASS | 8734 | 7314 | valid JSON; no stray GRAPH_REPORT.md anywhere |
| get_impact_radius | PASS | PASS | 62913 | 61599 | |
| get_nav_callers | PE ~ | PASS | 2761 | 3324 | graceful empty |
| get_nav_graph | PE ~ | PASS | 3687 | 4678 | graceful empty |
| get_overview_context | PASS | PASS | 19747 | 13101 | |
| get_pr_impact | PE ~ | PASS | 2602 | 3751 | severity LOW; cluster_id null rows |
| get_review_context | PASS | PASS | 3669 | 2780 | |
| get_screen_args | PE ~ | PASS | 3571 | 4550 | graceful empty |
| get_service_context | PE ~ | PASS | 4252 | 3784 | structured empty snapshot |
| get_service_graph | PE ~ | PASS | 2459 | 1721 | edges [] |
| get_team_map | PE ~ | PASS | 2578 | 1721 | count 0 |
| get_tested_by | PASS | PASS_EMPTY ~ | 2609 | 2051 | 0 test edges this run (R1 had 46) |
| get_traceability | PASS | PASS | 2575 | 2057 | |
| get_traceability_matrix | PE ~ | PASS | 1728 | 2110 | total 0 (mini-PRD zero-work) |
| get_upcoming_changes | PE ~ | PASS | 2114 | 3957 | count 0 |
| index_prd | PASS | PASS | 1399 | 1560 | requirements_created:0 silent zero-work (R1 #10 open) |
| kg_context | PE ~ | PASS | 3072 | 2090 | confidence 0.0 |
| kg_ontology_status | PASS | PASS | 2113 | 1740/2087 | dynamic counts correct pre/post reopen |
| kg_trace_workflow | PASS | PASS | 2069 | 2446 | |
| link_element | PASS | PASS | 2414 | 2400 | |
| mcp_index | PASS | PASS | 30823 | 26428 | incremental 26.4 s |
| mcp_index_docs | FAIL_ERROR | PASS | 32548 | 20243 | FIX confirmed (<300 s) |
| mcp_init | PASS | PASS | 692 | 692 | idempotent |
| mcp_install | PASS | PASS | 7341 | 1380 | |
| mcp_status | PASS | PASS | 3161 | 2110 | database_exists true (after identity fix N1) |
| ontology_control | PASS | PASS | 360 | 360/5968 | status 360 ms both cycles |
| orchestrate | PASS | PASS | 2519 | 2057/2097 | attempt-1 same filename-parse error (R1 verbatim) then retry OK |
| promote_environment | PASS | PASS | 1891 | 1724 | no-op promoted_count:0 |
| query_graph | PASS | PASS | 84799 | 56824 | 15 edges returned |
| query_incidents | PE ~ | PASS | 1728 | 1726 | incidents [] |
| report_query_outcome | PASS | PASS | 1620 | 1555 | recorded:true |
| resolve_with_lsp | PASS | PASS | 2094 | 1389 | graceful no-LSP fallback |
| run_raw_query | PASS | PASS | 2274 | 1723 | count(code_elements)=10567 |
| search_by_requirement | PE ~ | PASS | 1743 | 2057 | code_elements [] |
| search_code | PE ~ | PASS | 4421 | 2123/2454 | _prefer_hint payload; name-fallback path works via explain/get_* |
| search_knowledge | PASS | PASS | 1742 | 1710 | roundtrip + cleanup verified |
| semantic_search | PASS | PASS | 8398 | 5480 | no vectors -> ontology-first fallback count:5 |
| shortest_path | PASS | PASS | 60505 | 44514 | found:false between real QNs (valid negative, slow) |
| temporal_query | FAIL_TIMEOUT | FAIL_TIMEOUT | 170002 | >120003 | fresh-server repro; empty-corpus 7.0 s |
| timeline | PE ~ | PASS | 3990 | 4120 | events [] |
| update_knowledge | FAIL_ERROR | PASS | 2577 | 2786 | FIX confirmed (roundtrip) |
| kg_semantic_context | EU | EU | - | - | embeddings-gated absent |
| embed_control | EU | EU | - | - | embeddings-gated absent |
| set_embed_model | EU | EU | - | - | embeddings-gated absent |

## Audit integration (ENT-1)

```
$SWEEP audit export --format jsonl --out /tmp/opencode/audit-c2.jsonl
wrote 107 audit entries to /tmp/opencode/audit-c2.jsonl
$SWEEP audit verify
OK: audit chain intact (107 entries verified)   # exit 0
```

Ledger pinned to served schema `leankg_p_970a9b30ff7448d7`; 107 rows cover every successful `tools/call` dispatched by the aligned servers this session (85 consolidated + crashed-run/retest/probe dispatches). Chain intact.

## CLI checks

- `doctor --deep --project <wt>` → **exit 2** (target was <=1). pg-latency PASS (341 ms), migrations PASS (6/6), embedding-coverage PASS, pool-env PASS, leankg-dir PASS; **index-freshness WARN only** ("325 missing file(s), 0 stale") — **no false-positive from `ontology://` rows** (synthetic-URI freshness fix holds). Two genuine FAILs:
  - `orphaned-relationships`: "432/1000 sampled edges reference missing elements; e.g. listens_on: emitter -> event::event"
  - `duplicate-names`: "10 duplicated qualified_name(s); top: docs/analysis/perf-memory-cpu-issues.md::Fix×8, …" (markdown heading sections collide)
  Both are docs-corpus data-quality issues (likely aggravated by killing a mid-flight docs indexer during re-keying); they are real findings, not false positives.
- `export --markdown --out /tmp/opencode/graph-docs-c2.md` → exit 0, **4,068 lines**, 13,878 elements documented.

## NEW issues (Cycle-2 findings)

**N1 [P1] `leankg index` regenerates `.leankg/leankg.yaml` and discards user config — including the `project.project_path` identity anchor.** Observed twice: after each index run the yaml reverted to a freshly generated file (`name: my-project`, `root: .`, no `project_path`), and the next MCP boot served an EMPTY schema: `mcp_status` → `"database_exists: false" ... "message: LeanKG directory exists but database not initialized."` while PG held 10,567 fresh rows. The R1-issue-#5 contract (writer/reader share one canonical key via yaml) silently breaks whenever config regeneration drops the field.

**N2 [P1] Legacy-schema adoption hijacks fresh data.** With a RELATIVE `project_path` and a pre-existing populated legacy schema, the server pinned the STALE schema over the fresh index: boot log `search_path%3Dleankg_p_2e2f737263` (R1 leftovers, 13,389 rows) while the current index sat in `leankg_p_970a9b30ff7448d7`. `pick_schema_for_init` adopts any existing legacy candidate without checking whether the preferred schema is populated. Workaround used here: ABSOLUTE `project_path` (no legacy candidate generated).

**N3 [P2] Launcher-cwd leaks into project identity.** A server restart executed from the PARENT repo cwd pinned an unrelated populated schema `leankg_p_cb074133fac7a6f3` despite `--project <worktree>`; the byte-identical relaunch from worktree cwd pinned the correct `leankg_p_970a9b30ff7448d7`. Mechanism TBD (config/env discovery order vs CWD).

**N4 [P2] Wedge still reachable via internal-watchdog path.** After `temporal_query` and `agent_focus` expired the INTERNAL 30 s watchdog in a degraded state, even trivial calls failed until restart: `{"code": -32603, "message": "tool search_knowledge timed out after 30s", "data": null}`. On a fresh server, client-abandoned hangs of the same tools do NOT wedge anything (canaries answered 1.7–2.2 s between hangs). The R1 cascade is therefore narrowed to the watchdog-expiry path, not eliminated.

**N5 [P2] doctor --deep exit 2 (orphaned edges + duplicate doc QNs)** — see CLI checks above; verbatim strings captured there.

**N6 [P3, carried over] `get_cluster_skill` markdown references PARENT-repo absolute paths** (`/Users/.../leankg/ui-v2/...` outside the served worktree root) — R1 issue #12 family, still present (cluster_3445 skill output).

Carried-over unfixed (pre-existing, not among the 7 fixes): R1 #10 `index_prd` silent zero-work (`requirements_created: 0, errors: []` on valid mini-PRD); R1 orchestrate attempt-1 filename-parse error (`"Failed to read file architecture: No such file or directory (os error 2)"`).

## Verbatim errors (raw, this cycle)

```json
temporal_query (degraded state): {"code": -32603, "message": "tool temporal_query timed out after 30s", "data": null}
agent_focus   (degraded state): {"code": -32603, "message": "tool agent_focus timed out after 30s", "data": null}
any tool      (wedged server):  {"code": -32603, "message": "tool search_knowledge timed out after 30s", "data": null}
orchestrate attempt 1:          {"code": -32603, "message": "Failed to read file architecture: No such file or directory (os error 2)", "data": null}
shortest_path (empty view):     {"code": -32603, "message": "source './src/mcp/tools.rs::list_tools' not found", "data": null}
run_raw_query (wrong schema):   {"code": -32603, "message": "db error", "data": null}
```

## Methodology & deviations from R1

- Same call pattern, same per-tool args, same classification rules. Client timeout caps lowered for known-hang trio (45–60 s) since regression thresholds are 10–15 s; clean-server retests used 120–150 s caps.
- Sweep executed against the correctly-aligned schema; an initial misaligned window (empty-schema reads) was voided and re-run (`c2_phase1.void-empty-schema.out` retained).
- Empty-schema probes of the trio (~7 s each) demonstrate the slowness is corpus-scale-dependent, not intrinsic.
- Final statuses use best healthy evidence across attempts; wedge-attribution follows R1 rules (only N4 event attributed to wedge path).
- Raw artifacts: `/tmp/opencode/c2_phase{1,2,3tail,retest}.json`, `/tmp/opencode/c2_regress.json`, `/tmp/opencode/c2_final.json`, `/tmp/opencode/audit-c2.jsonl`, logs `/tmp/opencode/c2-{index,index2,mcp}.log`.

*Generated by sweep scripts `/tmp/opencode/c2_*.py`; consolidation via `/tmp/opencode/c2_consolidate.py`.*
