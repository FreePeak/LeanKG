# REL-055 — Mega-safe concept_search / query_graph / get_clusters

**Date:** 2026-07-20  
**Branch:** `feature/mega-concept-query-safe`  
**Image:** `leankg-leankg:mega-concept-query` (tagged `latest` for compose)  
**Transport:** HTTP MCP `http://localhost:9699`  
**Projects:** `/workspace` (small), `/workspace-other` (mega ~641k elements)

RCA: [`root_cause_mega_concept_query_clusters_2026-07-20.md`](root_cause_mega_concept_query_clusters_2026-07-20.md).  
Prior full-tool fail context: [`ce03fd8-docker-mcp-full-tool-test-2026-07-20.md`](ce03fd8-docker-mcp-full-tool-test-2026-07-20.md).

---

## 1. Verdict

| Gate | Result |
|------|--------|
| Mega `concept_search` | **PASS** ~0.9s |
| Mega `query_graph` | **PASS** ~12.6s (was timeout / disconnect) |
| Mega `get_clusters` | **PASS** ~1.2s — live Louvain refused; `source: precomputed` (+ offline-assign hint when empty) |
| `/workspace` `query_graph` | **PASS** ~0.7s (no regression) |
| Logs `all_elements()` / `all_relationships()` during smoke | **0** |
| RSS under ~3.9 GiB cgroup | ~161 MiB; `RestartCount=0`; `/health` ok |

Tracker: `US-MG-TOOL-01` / `FR-ONT-MEGA-01` / `FR-GF-MEGA-01` / `FR-CL-MEGA-01` / `REL-055` → **DONE**.

---

## 2. Smoke results

| Call | Project | Outcome | Notes |
|------|---------|---------|-------|
| `concept_search` query=authentication | `/workspace-other` | PASS | ~0.88–0.97s; name fallback hits |
| `get_clusters` | `/workspace-other` | PASS | precomputed path; empty + offline assign hint (no cluster_id rows yet) |
| `query_graph` “what connects auth to payment” | `/workspace-other` | PASS | ~12.6s; hops=1; edges returned |
| `query_graph` same question | `/workspace` | PASS | ~0.68s |

---

## 3. Fix summary (FR-SEM-07 class)

1. **FR-ONT-MEGA-01** — keyed / path-prefixed code_ref resolve; ban hot-path `load_indexed_code_elements`.
2. **FR-GF-MEGA-01** — keyed `resolve_to_qualified`; frontier-local edge fetch; mega outbound-only BFS (skip slow incoming scans); stop-word connection verbs; longer mega seed aliases (`auth`→`authentication`); skip live `shortest_path` on mega; depth/frontier caps.
3. **FR-CL-MEGA-01** — mega `get_clusters` serves precomputed `cluster_id` (refuse live Louvain).

Rejected as primary fix: raising container RAM; forever-refuse of prefer-order search/NL tools.

---

## 4. Rebuild recipe (placeholders only)

```bash
rsync -a --exclude target --exclude .git \
  .worktrees/feature/mega-concept-query-safe/ /tmp/leankg-mega-concept-query-build/
# ensure vendor/ is a real directory in the build context
docker build -f Dockerfile.rocksdb -t leankg-leankg:mega-concept-query /tmp/leankg-mega-concept-query-build
docker tag leankg-leankg:mega-concept-query leankg-leankg:latest
docker compose -f docker-compose.rocksdb.yml -f docker-compose.override.yml \
  --env-file .dockerfile up -d --force-recreate --no-build leankg
```

---

*REL-055 closed 2026-07-20 on `feature/mega-concept-query-safe`.*
