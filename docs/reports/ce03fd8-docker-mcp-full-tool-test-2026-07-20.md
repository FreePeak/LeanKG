# LeanKG Docker MCP — post-merge `main` full tool validation

**Date:** 2026-07-20  
**Commit:** `ce03fd8` — `fix: mega HNSW semantic_search OOM (FR-SEM-07 / REL-054) (#87)`  
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

| Area | `/workspace` | `/workspace-other` |
|------|--------------|--------------------|
| Pull + rebuild + health | **PASS** — fast-forward `a89a2cc` → `ce03fd8`; `/health` ok | same |
| Status / find / lookup | **PASS** | **PASS** |
| Ontology (`kg_*`, concept) | **PASS** | **PARTIAL** — first `concept_search` disconnected; retry timed out |
| Semantic HNSW (`semantic_search`, `kg_semantic_context`) | **PASS** (`hnsw+rerank`) | **PASS** (~42–63 s, `hnsw+rerank`) |
| Flow / deps / impact / architecture / schema | **PASS** | **PASS** |
| Mega-refuse (`get_clusters`) | n/a | **PASS** (expected refuse) |
| `query_graph` | **PASS** with `question=` | **FAIL/TIMEOUT** on mega (still hits deprecated `all_elements()`) |
| `embed_control` status | **PASS** | **PASS** |

**Overall (scripted suite):** **35 / 38 PASS** on first pass (wrong `query_graph` arg counted as 2 fails; 1 mega `concept_search` disconnect). After param fix: small `query_graph` PASS; mega `query_graph` / `concept_search` still timed out under ~3.9 GiB cgroup.

**Headline:** FR-SEM-07 holds on mega — `semantic_search` ×2 + `kg_semantic_context` complete without the old HNSW `all_elements()` OOM. Residual risk: other tools (notably mega `query_graph`) still call `all_elements()`; stacked load can restart the container under the observed ~3.9 GiB ceiling.

---

## 2. Environment

```bash
git pull --ff-only origin main   # → ce03fd8
docker compose -f docker-compose.rocksdb.yml -f docker-compose.override.yml \
  --env-file .dockerfile build leankg
docker compose … up -d --force-recreate --no-build leankg
```

| Knob | Value |
|------|--------|
| `LEANKG_PROJECT_DIRS` | `/workspace,/workspace-other,…` |
| `LEANKG_MCP_PROJECT` | `/workspace-other` |
| `LEANKG_SKIP_FRESHNESS_CHECK` | `1` |
| Observed mem ceiling (`docker stats`) | **~3.894 GiB** (compose `mem_limit` 6g does not raise this on this host) |

Peak RSS during suite: **~3.16 GiB**. After suite: ~0.9–1 GiB. `RestartCount` ended at **1** (during mega `concept_search` disconnect); `OOMKilled=false` after recovery.

---

## 3. Results by category

### 3.1 `/workspace` (small)

| Tool | Latency | Status | Notes |
|------|--------:|--------|-------|
| `mcp_status` | 0.5 s | PASS | indexed |
| `search_code` / `find_function` / `query_file` | &lt;0.2 s | PASS | |
| `concept_search` / `kg_ontology_status` / `kg_context` | &lt;0.5 s | PASS | |
| `semantic_search` | 11.4 s | PASS | `hnsw+rerank` |
| `kg_semantic_context` | 9.2 s | PASS | |
| `get_callers` / `get_call_graph` / deps / impact | &lt;3 s | PASS | |
| `get_architecture` / `get_graph_schema` / `find_dead_code` | &lt;1 s | PASS | |
| `query_graph` (`question=…`) | 2.5 s | PASS | (first attempt failed: used `query=` — client error) |
| `embed_control` status | 1.9 s | PASS | idle / not armed |

### 3.2 `/workspace-other` (mega)

| Tool | Latency | Status | Notes |
|------|--------:|--------|-------|
| `mcp_status` | 4.2 s | PASS | ~32k classes (counts present) |
| `search_code` CreateOrder | 13.0 s | PASS | |
| `find_function` CreateOrder | 0.6 s | PASS | |
| `query_file` payment | 9.6 s | PASS | |
| `concept_search` authentication | 21.9 s | **FAIL** | `RemoteDisconnected`; retry 180 s **Timeout** |
| `kg_ontology_status` / `kg_context` | &lt;1 s | PASS | |
| `semantic_search` refund | 42.5 s | PASS | `hnsw+rerank` |
| `semantic_search` gRPC interceptor | 43.0 s | PASS | `hnsw+rerank` |
| `kg_semantic_context` access rights | 62.6 s | PASS | |
| `get_callers` / `get_call_graph` CreateOrder | 4–31 s | PASS | |
| `get_dependencies` / `get_impact_radius` | 1.5–60 s | PASS | |
| `get_architecture` / `get_graph_schema` | 3–7 s | PASS | |
| `get_clusters` | 0.7 s | PASS | mega refuse (expected) |
| `get_code_tree` | 0.6 s | PASS | bounded response |
| `query_graph` (`question=…`) | 120 s | **FAIL** | Timeout; logs show repeated `all_elements()` WARN |
| `embed_control` status | 3.2 s | PASS | |

---

## 4. Semantic / FR-SEM-07 check

| Check | Result |
|-------|--------|
| Mega `semantic_search` | **PASS** ×2 |
| Mega `kg_semantic_context` | **PASS** |
| Small HNSW regression | **PASS** |
| HNSW path still dumping full graph via `all_elements()` | **No evidence** on semantic calls in this suite’s success path |

`all_elements()` **does** still appear in logs from **other** tools (notably mega `query_graph` retries). That is outside FR-SEM-07’s HNSW hydration fix.

---

## 5. Failures / follow-ups

1. **`query_graph` API:** required arg is `question` (not `query`). Small graph OK; mega path still slow/unsafe under memory pressure (`all_elements()`).
2. **Mega `concept_search`:** flaky under ~3.9 GiB — disconnect then timeout; worth a dedicated investigation (not the HNSW seed dump).
3. **Host cgroup ceiling ~3.9 GiB:** stacked embedding/rerank + heavy graph tools can still restart the container; prefer spacing heavy calls or raising real memory headroom.

---

## 6. Verdict

Ship status for **post-#87 main**: keyword find/lookup, ontology status/context, flow/structure, and **mega HNSW semantic tools** are usable. Treat mega `query_graph` and heavy mega `concept_search` as **not yet safe** under current memory headroom.

Raw JSON suite: `/tmp/ce03fd8-mcp-smoke.json` (local, sanitized).

*Report generated 2026-07-20 after rebuild from `ce03fd8`.*
