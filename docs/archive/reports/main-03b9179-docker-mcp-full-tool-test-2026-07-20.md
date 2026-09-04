# LeanKG Docker MCP — post-merge `main` full tool validation

**Date:** 2026-07-20  
**Commit:** `03b9179` — `fix: mega-safe concept_search, query_graph, get_clusters (REL-055) (#88)`  
**Binary:** `leankg 0.19.1` (image `leankg-leankg` rebuilt from local `main`)  
**Transport:** HTTP MCP `http://localhost:9699/mcp`  
**Container:** `leankg-leankg-1` (compose recreate after rebuild)  
**Tools registered:** 84  

| Project arg (container) | Role |
|-------------------------|------|
| `/workspace` | LeanKG source (small graph) |
| `/workspace-other` | Mega-graph mount (~641k elements) |

Host bind paths and personal mount names are omitted.

---

## 1. Executive summary

| Area | `/workspace-other` (mega) | Notes |
|------|---------------------------|-------|
| Pull + rebuild + health | **PASS** | `main` @ `03b9179`; `/health` ok; RocksDB resume (skip cold index) |
| Find / lookup | **PASS** | `search_code`, `find_function`, `query_file` |
| Ontology / concept | **PASS** | `concept_search`, `kg_ontology_status`, `kg_context`, `kg_self_test` |
| Semantic HNSW | **PASS** | `semantic_search` ×2 (~42 s), `kg_semantic_context` (~76 s) |
| `query_graph` | **PASS** | 14.4 s with `question=` (REL-055 mega path) |
| Flow / deps / impact / architecture | **PASS** | callers, deps, impact, architecture, schema |
| Mega-refuse | **PASS** | `get_clusters` / doc-tree refuse as designed |
| `embed_control` status | **PASS** | with `action=status` |
| Stacked heavy suite | **PARTIAL** | Back-to-back heavy tools caused **2 restarts** under ~3.9 GiB cgroup |

**Overall (merged first-wave + retest):** core agent tools on mega are usable. REL-055 holds for `concept_search` / `query_graph` / `get_clusters`. Residual risk: stacked full-graph tools (`get_graph_report`, `get_cluster_skill`, `check_consistency`, `find_dead_code`) still wedge or restart the container under the observed ~3.9 GiB ceiling.

---

## 2. Environment

```bash
git pull --ff-only origin main   # already at 03b9179
docker compose -f docker-compose.rocksdb.yml -f docker-compose.override.yml \
  --env-file .dockerfile build leankg
docker compose … up -d --force-recreate --no-build leankg
```

| Knob | Value |
|------|--------|
| `LEANKG_PROJECT_DIRS` | `/workspace,/workspace-other,…` |
| `LEANKG_MCP_PROJECT` | `/workspace-other` |
| `LEANKG_SKIP_FRESHNESS_CHECK` | `1` |
| `LEANKG_EMBED_ON_BOOT` / `LEANKG_EMBED_BACKGROUND` | `0` / `0` |
| Observed mem ceiling (`docker stats`) | **~3.894 GiB** |

### Graph size (`mcp_status` include_counts)

| Metric | Value |
|--------|------:|
| elements | **640 998** |
| files | 28 249 |
| functions | 390 301 |
| classes | 32 208 |
| relationships | **1 124 496** |

---

## 3. Method

1. **Wave A** — `scripts/mcp-smoke-tools.py` with `LEANKG_SMOKE_INCLUDE_HEAVY=1` against mega (alphabetical full registry). Crashed during `get_graph_report` → cascade connection resets for tools after that point (`RestartCount=1`).
2. **Wave B** — focused retest of cascade-failed + semantic/structure tools with correct args, 5 s pause after heavy calls. `semantic_search` / `kg_semantic_context` / `query_graph` **PASS**. Later `get_cluster_skill` 180 s timeout left MCP event-loop wedged (`RestartCount=2`, health fail); container force-recreated to restore service.
3. Mutating tools (`mcp_init`, `mcp_index`, knowledge writes, …) intentionally **SKIP**.

Raw logs (local): `/tmp/main-03b9179-mcp-full-smoke-*.txt`, `/tmp/main-03b9179-mcp-retest.json`.

---

## 4. Results by category (mega)

### 4.1 Core find / context — PASS

| Tool | Latency | Status |
|------|--------:|--------|
| `mcp_status` | 3.9 s | PASS (counts) |
| `search_code` CreateOrder | 1.4 s | PASS |
| `find_function` CreateOrder | 0.3 s | PASS |
| `query_file` `*payment*` | 1.9 s | PASS |
| `get_context` / `ctx_read` / deps / dependents | &lt;50 s | PASS (wave A) |
| `get_impact_radius` depth=1 | 46.9 s | PASS |
| `get_callers` / `get_call_graph` | 1–27 s | PASS |
| `get_architecture` / `get_graph_schema` | 3–8 s | PASS |
| `get_review_context` / `get_pr_impact` / `get_tested_by` | &lt;3 s | PASS |
| `orchestrate` | 0.7 s | PASS |
| `embed_control` `action=status` | 1.3 s | PASS |

### 4.2 Ontology / semantic — PASS (headline)

| Tool | Latency | Status | Notes |
|------|--------:|--------|-------|
| `concept_search` authentication | 0.7–30 s | PASS | REL-055 |
| `kg_ontology_status` / `kg_context` / `kg_self_test` | &lt;4 s | PASS | `all_ok: true` |
| `semantic_search` refund | 42.8 s | PASS | HNSW path |
| `kg_semantic_context` access rights | 75.5 s | PASS | |
| `semantic_search` gRPC interceptor | 41.4 s | PASS | 1 disconnect then retry OK |
| `query_graph` `question=…` | 14.4 s | PASS | REL-055 (no 120 s timeout) |

### 4.3 Mega-refuse / bounded — PASS (expected)

| Tool | Status | Notes |
|------|--------|-------|
| `get_clusters` | PASS | Live Louvain refused (~641k) |
| `get_cluster_context` / `get_doc_tree` / `get_doc_structure` / `get_nav_graph` | PASS | refuse or empty with element_count |
| `find_clones` | — | **REMOVED** | Hard-deleted 2026-07-20 (non-strategic; mega refuse) |
| `get_code_tree` | PASS | bounded |
| `find_large_functions` / `get_god_nodes` / `find_tunnels` | PASS | |

### 4.4 Unsafe / incomplete under stacked load

| Tool | Status | Notes |
|------|--------|-------|
| `get_graph_report` | **FAIL** | Wave A: remote close @ ~62 s → container restart |
| `get_cluster_skill` | **FAIL** | 180 s timeout; wedged MCP (CPU pegged, health fail) |
| `check_consistency` | **FAIL** | 180 s timeout (wave A) |
| `find_dead_code` | **FAIL** | 180 s timeout (wave A) |
| `shortest_path` | FAIL | fixture: target `main` not found (not a crash) |
| `run_raw_query` | FAIL | bad Cozo sample query in harness |
| Nav/timeline args | FAIL→N/A | harness used wrong param names (`destination`, `qualified_name`, `at`, …) — not retested after wedge |

Mutating tools: **SKIP** (14).

---

## 5. Stability

| Event | Evidence |
|-------|----------|
| Peak RSS during suite | ~3.2 GiB (wave A mem log) |
| Restarts during testing | **2** (`OOMKilled=false`) |
| Post-suite recovery | force-recreate; `/health` ok; `mcp_status` + `search_code` smoke OK |

Stacking full-graph dumps (`all_elements` / `all_relationships` WARNs in logs from `get_cluster_skill` / report paths) remains the main operational hazard under ~3.9 GiB.

---

## 6. Verdict

| Question | Answer |
|----------|--------|
| Rebuild from latest `origin/main`? | **Yes** — `03b9179`, image rebuilt, MCP healthy |
| Mega keyword find/lookup usable? | **Yes** |
| Mega semantic + REL-055 query/concept? | **Yes** |
| Safe to blast all 84 tools back-to-back? | **No** — skip/space `get_graph_report`, `get_cluster_skill`, `check_consistency`, `find_dead_code` on mega |
| Ship note | Core agent prefer-order (`concept_search` → `semantic_search` → `search_code` / `find_function`) is good on mega at this commit |

*Report generated 2026-07-20 after rebuild + dual-wave MCP validation on mega mount.*
