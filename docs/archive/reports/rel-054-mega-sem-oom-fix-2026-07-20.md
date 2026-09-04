# REL-054 — Mega-graph HNSW semantic OOM fix evidence (FR-SEM-07)

**Date:** 2026-07-20  
**Branch:** `feature/mega-sem-oom`  
**Commits:** `670e202` (docs P0) → `d14dd8d` (keyed hydration) → `e8bb475` (`has_any` gate)  
**Image:** `leankg-leankg:mega-sem-oom` (built from worktree + vendor; tagged `latest` for compose)  
**Transport:** HTTP MCP `http://localhost:9699`  
**Projects:** `/workspace` (small), `/workspace-other` (mega ~641k elements)

Prior failure evidence: [`main-a89a2cc-docker-mega-tool-test-2026-07-20.md`](main-a89a2cc-docker-mega-tool-test-2026-07-20.md).

---

## 1. Verdict

| Gate | Result |
|------|--------|
| Mega `semantic_search` (×2) | **PASS** — `method: hnsw+rerank`, HTTP kept |
| Mega `kg_semantic_context` | **PASS** — ~58 s total (`retrieve` ~36 s), seeds + traverse |
| Logs contain `all_elements()` during smoke | **False** (count `0`) |
| `/workspace` `semantic_search` HNSW | **PASS** (GREEN, no regression) |
| `/health` after mega pair | **ok** |
| Tracker | `US-SEM-06` / `FR-SEM-07` / `REL-054` → **DONE** |

**Root fix:** HNSW seed hydration no longer calls `GraphEngine::all_elements()`. It uses `get_elements_by_qualified_names` (keyed, includes `env`). Cheap `:limit 1` `has_any` replaces `list_all` gates; `index_size()`/`list_all` skipped when `ann_top_k` is set.

---

## 2. Smoke results

| Call | Project | Outcome | Notes |
|------|---------|---------|-------|
| `semantic_search` query≈payment refund… | `/workspace-other` | PASS | `ann_top_k_used: 50`, 10 hits |
| `kg_semantic_context` query≈access rights | `/workspace-other` | PASS | `latency_ms.total: 58389`, `reranker: bge-reranker-v2-m3` |
| `semantic_search` query≈grpc interceptor | `/workspace-other` | PASS | `hnsw+rerank` |
| `semantic_search` query≈mcp semantic… | `/workspace` | PASS | top hit `./src/mcp/handler.rs::semantic_search` |

Peak RSS samples during mega path (docker stats, cgroup denom **~3.894 GiB**): ~2.6–3.3 GiB while tools completed. No `all_elements()` warn in container logs for the smoke window.

### Residual note (not FR-SEM-07 regression)

After several successive HNSW+rerank loads under the ~3.9 GiB effective OrbStack ceiling, Docker recorded one later `oom` / exit `137` while stacking an extra `/workspace` call (models still resident). That is **residual model RSS headroom**, not the pre-fix mega dump. Health recovered; `/workspace` HNSW re-verified GREEN after restart. Compose `mem_limit: 6g` still does not raise the observed stats denominator above ~3.9 GiB on this host.

---

## 3. Code changes (summary)

1. `GraphEngine::get_elements_by_qualified_names` — keyed QN fetch **with `env`**.
2. `SemanticRetrievalPipeline::fetch_elements_batch` — keyed only (ban `all_elements`).
3. Skip `index_size()` when `opts.ann_top_k.is_some()`.
4. `embeddings::state::has_any` — `:limit 1`; used by `embeddings_index_available` and `kg_semantic_context`.

Unit/`hnsw_recall_e2e` (embeddings feature) green on the fix branch before Docker smoke.

---

## 4. Rebuild recipe (no host bind paths)

```bash
# Assemble Linux build context (vendor must be a real directory, not a broken symlink)
rsync -a --exclude target --exclude .git \
  .worktrees/feature/mega-sem-oom/ /tmp/leankg-mega-sem-oom-build/
# copy vendor/ into build context as needed
docker build -f Dockerfile.rocksdb -t leankg-leankg:mega-sem-oom /tmp/leankg-mega-sem-oom-build
docker tag leankg-leankg:mega-sem-oom leankg-leankg:latest
docker compose -f docker-compose.rocksdb.yml -f docker-compose.override.yml \
  --env-file .dockerfile up -d --force-recreate --no-build leankg
```

Do **not** `docker cp` a macOS Mach-O `leankg` into the Linux container.

---

*REL-054 closed 2026-07-20 on `feature/mega-sem-oom`.*
