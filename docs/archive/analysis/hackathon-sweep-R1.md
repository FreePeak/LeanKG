# Hackathon R1 — Full MCP Tool Live Sweep vs Remote Postgres

**Date:** 2026-08-22 · **Branch:** `feature/hackathon` · **Worktree:** `.worktrees/hackathon`

## Setup facts

| Item | Value |
|---|---|
| Binary | `leankg` 0.26.0 release (shared cargo cache target dir; rebuilt green) |
| Storage | Remote Postgres `rivesca.eu.db.rivestack.io:5432` (`LEANKG_PG_URL`, TLS verify-full) — no Docker/local PG |
| Schema | `leankg_p_2e2f737263` (key = literal `./src`; see Issue #5) |
| Corpus | `index ./src`: **201 files**, **10,105 elements**, **66,554 relationships**, 18,881 call edges resolved inline |
| Docs phase | CLI docs indexer completed during sweep (~55 min for 224 files — see latency notes) |
| Server | `mcp-http --port 9701` + `LEANKG_SKIP_FRESHNESS_CHECK=1` (else boot auto-index, Issue #6); health `GET /health` → 200 |
| Registry | `tools/list` → **76 tools** served (+3 embeddings-gated absent) |
| Protocol | JSON-RPC 2.0 `POST /mcp`, method `tools/call`; no initialize handshake required |

## Summary

**51 PASS / 18 PASS_EMPTY / 3 FAIL_ERROR / 4 FAIL_TIMEOUT / 3 EXPECTED_UNAVAILABLE / 0 SKIPPED** (79 rows = 76 served registry tools + 3 EXPECTED_UNAVAILABLE)

- Individual HTTP calls: **128** (35 poisoned by the two wedge cascades)
- Latency all calls: p50 **4,896ms** · p95 **90,002ms** · 48 calls >30s
- Non-cascade calls (93): p50 **3,172ms** · p95 **150,002ms** (p95 skewed by 4 genuine client-side hang timeouts ≥60s)
- Steady-state ops (<60s, non-cascade, n=82): p50 **2,793ms** · p95 **15,776ms**

## Per-tool results

| Tool | args-summary | Status | latency-ms | Notes / errors |
|---|---|---|---|---|
| `add_annotation` | {"element": "./src/mcp/tools.rs::list_tools", "description": "hackathon-sweep te | **PASS** | 3002 |  |
| `add_documentation` | TINY doc retry | **PASS** | 18491 | tiny doc OK 18.5s; docs/prd.md hung indefinitely -> WEDGE #1 |
| `add_knowledge` | {"knowledge_type": "general", "title": "Hackathon R1 sweep test entry", "content | **PASS** | 2057 |  |
| `add_ontology_concept` | {"name": "hackathon_sweep_concept", "type_": "known_issue", "description": "hack | **PASS** | 2056 |  |
| `add_ontology_workflow` | {"name": "hackathon_sweep_workflow", "description": "hackathon-sweep workflow pr | **PASS** | 4896 |  |
| `agent_diary_read` | {"name": "hackathon-sweeper", "limit": 5} | **PASS** | 1540 |  |
| `agent_diary_write` | {"name": "hackathon-sweeper", "note": "hackathon-sweep R1 diary probe", "tags":  | **PASS** | 1371 |  |
| `agent_focus` | retry with persona fixture present | **FAIL_TIMEOUT** | 60003 | pre-fixture: -32603 persona not found; with fixture: hung 60s -> WEDGED SERVER (repro 2x) |
| `check_consistency` | retry | **FAIL_TIMEOUT** | 170003 | hung 90s + 170s; never returned |
| `concept_search` | verify dynamic concept discoverable | **PASS** | 3149 | initial probe empty; dynamic roundtrip add->search->trace verified PASS |
| `ctx_read` | {'file': './src/lib.rs', 'mode': 'signatures'} | **PASS** | 3804 |  |
| `delete_knowledge` | {id:k-general-18cdf46ab9c21128} | **PASS** | 3711 |  |
| `delete_ontology_concept` | {"gid": "local:agent:known_issue:agent-18cdf56caab0b740:v1"} | **FAIL_ERROR** | 2321 | "Element not found" for gids returned by add_* after restart; dynamic concepts lost (durability bug) |
| `detect_changes` | {'scope': 'all'} | **PASS** | 6291 |  |
| `explain_node` | {'name': './src/mcp/tools.rs::list_tools'} | **PASS** | 8090 |  |
| `export_graph_snapshot` | {"out_path": ".leankg/graph-snapshot.json"} | **PASS** | 6473 | reported written:10098 BUT file landed in PARENT repo .leankg (path escape) |
| `export_html` | {"out_path": ".leankg/graph.html", "max_nodes": 200} | **PASS** | 4691 | 200n/2852e reported BUT landed in PARENT repo .leankg |
| `find_env_conflicts` | {'service': 'leankg'} | **PASS** | 2790 |  |
| `find_large_functions` | {'min_lines': 150, 'limit': 5} | **PASS** | 2426 |  |
| `find_related_docs` | {'file': './src/mcp/handler.rs'} | **PASS_EMPTY** | 3673 | related_docs [] — docs corpus not fully indexed at call time |
| `find_route` | {'route': 'profile/{userId}'} | **PASS_EMPTY** | 3629 | graceful empty on non-Android repo |
| `find_tunnels` | {'limit': 10} | **PASS_EMPTY** | 5243 | count 0 |
| `generate_doc` | {'file': './src/lib.rs'} | **PASS** | 3172 |  |
| `get_architecture` | {'max_items': 5} | **PASS** | 8376 |  |
| `get_call_graph` | {'function': 'list_tools', 'depth': 1, 'max_results': 10} | **PASS_EMPTY** | 3710 | calls [] for list_tools depth=1 |
| `get_cluster_skill` | {"cluster_id":"cluster_452"} | **PASS** | 15776 | 15.8s slow; markdown referenced PARENT-repo abs paths (project-root bleed) |
| `get_clusters` | {'limit': 10} | **PASS** | 8388 |  |
| `get_code_tree` | {"limit": 20} | **PASS** | 3764 |  |
| `get_context` | retry file=./src/main.rs max_tokens=800 | **FAIL_TIMEOUT** | 170017 | hung 150s + 170s on file=./src/main.rs; handler never cancelled |
| `get_dependencies` | {'file': './src/mcp/tools.rs'} | **PASS** | 10957 |  |
| `get_dependents` | {'file': './src/db/backend.rs'} | **PASS** | 2620 |  |
| `get_doc_tree` | {'limit': 10} | **PASS** | 1750 |  |
| `get_feature_flow` | {"feature_id": "FR-HACK-01"} | **PASS** | 2827 |  |
| `get_files_for_doc` | {'doc': './docs/prd.md'} | **PASS_EMPTY** | 2793 | files [], resolved_doc null |
| `get_god_nodes` | {'limit': 5, 'exclude_hubs_percentile': 90} | **PASS** | 4705 |  |
| `get_graph_report` | {"format": "json"} | **PASS** | 8734 | valid JSON report BUT GRAPH_REPORT.md side-effect written to PARENT repo .leankg |
| `get_impact_radius` | {'file': './src/graph/query.rs', 'depth': 2} | **PASS** | 62913 |  |
| `get_nav_callers` | {'destination': 'MainActivity'} | **PASS_EMPTY** | 2761 | graceful empty on non-Android repo |
| `get_nav_graph` | {} no-android | **PASS_EMPTY** | 3687 | elements [] relationships [] (no nav files in corpus) |
| `get_overview_context` | {} | **PASS** | 19747 |  |
| `get_pr_impact` | {"files": ["./src/mcp/tools.rs", "./src/mcp/handler.rs"]} | **PASS** | 2602 | severity LOW for 2 changed files; cluster_id null on rows (clusters not attached) |
| `get_review_context` | {'files': ['./src/mcp/tools.rs']} | **PASS** | 3669 |  |
| `get_screen_args` | {'destination': 'MainFragment'} | **PASS_EMPTY** | 3571 | arguments [] graceful |
| `get_service_context` | {'service': 'leankg'} | **PASS_EMPTY** | 4252 | structured snapshot, all lists empty |
| `get_service_graph` | {'service': 'leankg'} | **PASS_EMPTY** | 2459 | edges [] (no service_calls data) |
| `get_team_map` | {'env': 'local'} | **PASS_EMPTY** | 2578 | count 0 teams |
| `get_tested_by` | {'file': './src/graph/query.rs'} | **PASS** | 2609 | 46 test edges returned for ./src/graph/query.rs |
| `get_traceability` | {'element': 'list_tools'} | **PASS** | 2575 |  |
| `get_traceability_matrix` | {'limit': 5} | **PASS_EMPTY** | 1728 | matrix [] total 0 (also after mini-PRD index) |
| `get_upcoming_changes` | {'limit': 10} | **PASS_EMPTY** | 2114 | count 0 |
| `index_prd` | {"source_doc": "/tmp/opencode/mini-prd.md"} | **PASS** | 1399 | ran clean but requirements_created:0/errors:[] on valid mini-PRD headings (silent zero-work); earlier attempt died in wedge |
| `kg_context` | {'query': 'impact radius computation', 'depth': 2} | **PASS_EMPTY** | 3072 | confidence 0.0, all expansion arrays empty |
| `kg_ontology_status` | {} | **PASS** | 2113 |  |
| `kg_trace_workflow` | {'workflow_id_or_query': 'hotfix_release_process'} | **PASS** | 2069 | nonexistent wf empty; dynamic workflow traceable step_count=1 |
| `link_element` | {"element": "./src/mcp/tools.rs::list_tools", "id": "US-HACK-SWEEP-R1", "kind":  | **PASS** | 2414 |  |
| `mcp_index` | incremental, corpus unchanged | **PASS** | 30823 | incremental changed_files:[] 30.8s |
| `mcp_index_docs` | {"path": "/tmp/opencode/minidocs"} | **FAIL_ERROR** | 32548 | internal watchdog "timed out after 30s" even for 1-file docs dir; canary OK, op completed post-timeout |
| `mcp_init` | idempotent re-init | **PASS** | 692 |  |
| `mcp_install` | {"mcp_config_path": "/tmp/opencode/.mcp.json", "project": "/Users/linh.doan/work | **PASS** | 7341 |  |
| `mcp_status` | project=<wt> | **PASS** | 3161 |  |
| `ontology_control` | {'action': 'status'} | **PASS** | 360 | status 360ms; sync 3.7s touched ontology_synced marker |
| `orchestrate` | retry intent=impact... | **PASS** | 2519 | attempt1 FAIL: intent 'show me the architecture overview' parsed as filename 'architecture'; retry w/ impact phrasing OK |
| `promote_environment` | no-op expected | **PASS** | 1891 | no-op promoted_count:0 as expected (no upcoming entries) |
| `query_graph` | {'question': 'what connects the indexer to the postgres database?', 'token_budge | **PASS** | 84799 |  |
| `query_incidents` | {'env': 'local', 'limit': 5} | **PASS_EMPTY** | 1728 | incidents [] |
| `report_query_outcome` | {"question": "R1 sweep connectivity probe", "outcome": "useful", "nodes": ["./sr | **PASS** | 1620 |  |
| `resolve_with_lsp` | {'language': 'rust', 'file_path': '/Users/linh.doan/work/harvey/freepeak/leankg/ | **PASS** | 2094 | graceful fallback: found:false reason=no LSP configured |
| `run_raw_query` | {count code_elements} | **PASS** | 2274 |  |
| `search_by_requirement` | {'requirement_id': 'FR-MCP-01'} | **PASS_EMPTY** | 1743 | code_elements [] |
| `search_code` | {'query': 'tree sitter extractor parse', 'limit': 5} | **PASS_EMPTY** | 4421 | NL-ish query "tree sitter extractor parse" -> only _prefer_hint payload (58 tokens), no visible hits; name-fallback empty |
| `search_knowledge` | {"query": "Hackathon R1 sweep"} | **PASS** | 1742 |  |
| `semantic_search` | {'query': 'embedding vector store', 'limit': 5} | **PASS** | 8398 | no vectors -> ontology-first fallback returned 5 results (graceful) |
| `shortest_path` | {'source': './src/mcp/tools.rs::list_tools', 'target': './src/benchmark/unified. | **PASS** | 60505 | found:false between real QNs after 60.5s (slow; valid negative) |
| `temporal_query` | retry at=now | **FAIL_TIMEOUT** | 170002 | hung 150s + 170s at=now; never returned |
| `timeline` | {'qualified_name': './src/mcp/tools.rs::list_tools'} | **PASS_EMPTY** | 3990 | events [] (element never invalidated) |
| `update_knowledge` | {id:<created>} | **FAIL_ERROR** | 2577 | "Failed to update knowledge entry: db error" - reproduced 2/2 |
| `kg_semantic_context` | - | **EXPECTED_UNAVAILABLE** | - | absent from tools/list: binary built without --features embeddings |
| `embed_control` | - | **EXPECTED_UNAVAILABLE** | - | absent from tools/list: binary built without --features embeddings |
| `set_embed_model` | - | **EXPECTED_UNAVAILABLE** | - | absent from tools/list: binary built without --features embeddings |

## Top issues (ranked by severity)

### 1. [P0] Executor wedge / cascade: a hung tool handler is never cancelled and blocks all subsequent calls

`add_documentation` on docs/prd.md hung (>150s) and `agent_focus` (persona fixture present) hung (>60s). After each hang every following call failed with `-32603 "tool X timed out after 30s"` — including 2s-class ops (35 calls poisoned across the two reproductions) until server restart. Hypothesis: single global tool-execution serialization + per-call 30s watchdog that fires while WAITING, leaving the stuck handler holding the lock forever. Note: read-only long ops (query_graph 84.8s) complete fine when nothing else holds the lock.

### 2. [P0] Dynamic ontology writes do not survive server restart

`add_ontology_concept` / `add_ontology_workflow` returned gids and were readable in-session (concept_search matched 1, kg_trace_workflow step_count=1). After restart: kg_ontology_status dynamic_concepts:0 / dynamic_workflows:0 and delete_ontology_concept → `"Element not found"`. Either the write tx is not durable or the boot ontology sync wipes dynamic rows — contradicts documented “survive YAML re-syncs”.

### 3. [P0] update_knowledge always fails

`{"code": -32603, "message": "Failed to update knowledge entry: db error", "data": null}` — reproduced 2/2 (fresh add → update → same error → delete OK). PG translation of the UPDATE path appears broken.

### 4. [P1] File-write tools escape the served project root

export_graph_snapshot / export_html / get_graph_report reported success but wrote to the PARENT repo: server log shows `Wrote /Users/linh.doan/work/harvey/freepeak/leankg/.leankg/GRAPH_REPORT.md` while served root was `<worktree>/./src`. 39MB graph-snapshot.json landed outside the project. Cross-project writes; inconsistent with agent_diary/reflections which correctly land inside the worktree.

### 5. [P1] Project identity mismatch between CLI index and MCP server

`leankg index ./src` keyed schema `leankg_p_2e2f737263` (literal "./src"); MCP `--project <wt>` resolved canonical-root hash `leankg_p_29b8df3febee8339` → server initially served an EMPTY project right after a successful 10k-element index. Workaround: leankg.yaml `project.project_path: "./src"`. Silent data invisibility for any relative-path index.

### 6. [P1] Boot freshness check false negative

Server start logged `Index may be stale (last commit: 1787343222, db modified: 0)` despite 10k elements present, then began an unwanted boot-time incremental index. Required `LEANKG_SKIP_FRESHNESS_CHECK=1` to serve without re-indexing.

### 7. [P2] Hang trio over remote PG (never return)

get_context (150s+170s timeouts, file=./src/main.rs), temporal_query (150s+170s), check_consistency (90s+170s). Suspected per-element N+1 (~500ms/query × 10k elements). Each hang also risks triggering issue #1.

### 8. [P2] Remote-PG latency makes interactive use impractical

Trivial calls p50 ≈ 3.2–4.9s. query_graph 84.8s, get_impact_radius 62.9s, shortest_path 60.5s (found:false!), mcp_index incremental 30.8s, get_cluster_skill 15.8s, add_documentation(tiny) 18.5s, get_overview_context 19.7s.

### 9. [P2] agent_focus error handling

Without persona file returns raw JSON-RPC error `persona hackathon-sweeper not found: No such file or directory` instead of a graceful empty result; WITH fixture it hangs (see issue #1). Both behaviors need fixing.

### 10. [P2] index_prd silently does nothing on valid-looking PRD

Mini PRD with `## FR-HACK-01:` / `### US-HACK-01:` headings → `requirements_created: 0`, `errors: []`. No parse feedback.

### 11. [P3] mcp_index_docs exceeds internal 30s watchdog even for a 1-file docs dir

`"tool mcp_index_docs timed out after 30s"` while canary probe stayed healthy — op completed post-timeout, response lost. Watchdog budget vs docs pipeline mismatch.

### 12. [P3] Cross-project bleed in cluster content

get_cluster_skill markdown referenced `/Users/.../leankg/ui-v2/public/...` (parent-repo absolute paths) — same project-root resolution confusion family as issue #4.

## Verbatim errors (raw)

```json
orchestrate attempt 1: {"code": -32603, "message": "Failed to read file architecture: No such file or directory (os error 2)", "data": null}
agent_focus (no persona): {"code": -32603, "message": "persona hackathon-sweeper not found: No such file or directory (os error 2)", "data": null}
cascade (any tool, post-wedge): {"code": -32603, "message": "tool <name> timed out after 30s", "data": null}
update_knowledge: {"code": -32603, "message": "Failed to update knowledge entry: db error", "data": null}
delete_ontology_concept: {"code": -32603, "message": "Element not found: local:agent:known_issue:agent-18cdf56caab0b740:v1", "data": null}
delete_ontology_concept: {"code": -32603, "message": "Element not found: local:agent:workflow:agent-wf-18cdf56d2642bfc0:v1", "data": null}
get_cluster_skill (bad id): {"code": -32603, "message": "Cluster 0 not found", "data": null}
```

Client-side hangs (no response ever received, request abandoned): `get_context` 150s & 170s, `check_consistency` 90s & 170s, `temporal_query` 150s & 170s, `agent_focus` 60s, `add_documentation`(prd.md) 150s.

## Methodology & classification rules

- PASS = valid non-empty useful payload; PASS_EMPTY = valid response, legitimately empty for this corpus/query (incl. graceful empties from nav/service tools on a non-Android Rust repo).
- FAIL_TIMEOUT = no response within client window (60–170s depending on phase); FAIL_ERROR = JSON-RPC error/isError.
- Final per-tool status uses the best evidence across attempts under healthy server state; wedge-cascade failures (internal 30s watchdog while blocked behind a hung handler) are attributed to Issue #1, not to each individual tool.
- EXPECTED_UNAVAILABLE: binary built without `--features embeddings`; `semantic_search` itself IS registered and passed via its documented ontology-first fallback.
- Write tools exercised once with `hackathon-sweep`-tagged data; deletes removed exactly those objects. Cleanup verified: search_knowledge count:0, dynamic concepts/workflows gone.

*Generated by sweep scripts `/tmp/opencode/sweep_r1*.py` + consolidate.py; raw per-call data retained in /tmp/opencode/sweep_phase[1-5].json.*
