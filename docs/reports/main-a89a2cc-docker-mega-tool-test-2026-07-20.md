# LeanKG Docker MCP — post-merge `main` mega-graph tool validation

**Date:** 2026-07-20  
**Commit:** `a89a2cc` — `feat(mcp): embed_control idle resume + full tool redundancy audit (#86)`  
**Binary:** `leankg 0.19.1`  
**Transport:** HTTP MCP `http://localhost:9699/mcp`  
**Container:** `leankg-leankg-1` (image rebuilt from local `main`)  
**Tools registered:** 84 (includes `embed_control`)

| Project arg (container) | Role |
|-------------------------|------|
| `/workspace` | LeanKG source (small graph) |
| `/workspace-other` | Mega-graph monorepo mount (~641k elements) |

Host bind paths and personal mount names are omitted; all examples use container placeholders only.

---

## 1. Executive summary

| Area | Verdict | Notes |
|------|---------|-------|
| Pull + Docker rebuild + health | **PASS** | `main` @ `a89a2cc`; `/health` ok; MCP listens with `LEANKG_SKIP_FRESHNESS_CHECK` |
| Find / lookup (`search_code`, `find_function`, `query_file`) | **PASS** on mega | User-like queries return ranked hits in seconds |
| Ontology (`concept_search`, `kg_ontology_status`, `kg_context`) | **PASS** | Concept path works; auth query returned matched concepts + code refs |
| Flow / graph (`get_callers`, `get_call_graph`, deps/impact, architecture) | **PASS** | Callers for `CreateOrder` returned truncated list; architecture/schema bounded |
| `semantic_search` / `kg_semantic_context` on mega | **FAIL (OOM)** | HNSW path peaks ~3.3–3.4 GiB then disconnects; container restart |
| Same semantic tools on `/workspace` | **PASS** | `method: hnsw+rerank`, `reranker_active: true` |
| `embed_control` idle resume | **PASS** | Arm → silent 70 s → `skipped_fresh: 147175`, `embedded: 0`, ~2.7 s, vectors ~147420 |

**Ship note:** Keyword find/lookup, ontology, and flow tools are usable on the mega mount. **Do not treat mega `semantic_search` as safe** under the current OrbStack/cgroup memory headroom (~3.9 GiB observed in `docker stats` even when compose `mem_limit` is 6–10 g). Prefer `concept_search` → `search_code` / `find_function` on mega until HNSW stops calling `all_elements()` (or memory headroom is raised).

---

## 2. Environment

### 2.1 Rebuild

```bash
git pull origin main
docker compose -f docker-compose.rocksdb.yml -f docker-compose.override.yml \
  --env-file .dockerfile up --build -d
```

### 2.2 Boot knobs (values redacted to placeholders)

| Variable | Value used |
|----------|------------|
| `LEANKG_DB_ENGINE` | `rocksdb` |
| `LEANKG_PROJECT_DIRS` | `/workspace,/workspace-other,…` |
| `LEANKG_MCP_PROJECT` | `/workspace-other` |
| `LEANKG_SKIP_FRESHNESS_CHECK` | `1` |
| `LEANKG_EMBED_ON_BOOT` | `0` |
| `LEANKG_EMBED_BACKGROUND` | `0` |

Compose defaults: `mem_limit: 6g`, `mem_reservation: 3g`, `cpus: "6"`. A temporary local override bump to `10g` did **not** raise the `docker stats` denominator (~3.894 GiB); mega HNSW still OOMed.

### 2.3 Graph sizes (`mcp_status` include_counts)

| Metric | `/workspace-other` |
|--------|--------------------|
| elements | **640 998** |
| files | 28 249 |
| functions | 390 301 |
| classes | 32 208 |
| relationships | **1 124 496** |
| storage | RocksDB under `/data/leankg-rocksdb/projects/workspace-other-…` |

---

## 3. Focused results (user-like queries)

Prefer-order validated where possible: `concept_search` → `semantic_search` → `search_code`; find via `find_function`.

### 3.1 Find / lookup

| Test | Tool | Latency | Status | Notes |
|------|------|---------|--------|-------|
| Status + counts | `mcp_status` | ~3.7 s | PASS | Mega counts as above |
| `CreateOrder` | `search_code` | ~5.1 s | PASS | `count: 5`, `method: semantic+name_fallback` |
| `RefundPayment` | `search_code` | ~5.7 s | PASS | `count: 4` |
| `CreateOrder` | `find_function` | ~0.3 s | PASS | Multiple defs (token budget truncated) |
| `ProcessRefund` | `find_function` | ~0.7 s | PASS | Empty `functions: []` (name absent) |
| `*handler*.go` | `query_file` | ~7.7 s | PASS | `count: 0` on this mount/pattern |

### 3.2 Ontology

| Test | Tool | Latency | Status | Notes |
|------|------|---------|--------|-------|
| `user authentication login` | `concept_search` | ~7.4 s | PASS | `concept_match_count: 1`, `code_ref_count: 3`, no fallback |
| `refund failure` | `concept_search` | ~7.0 s | PASS | No concept match (`code_ref_count: 0`); still ok response |
| Coverage | `kg_ontology_status` | ~0.7 s | PASS | e.g. `domain_entity: 16`, `workflow: 10`, `failure_mode: 76` |
| `checkout refund` | `kg_context` | ~0.8 s | PASS | Low confidence / empty expand on mega query |

### 3.3 Flow / graph

| Test | Tool | Latency | Status | Notes |
|------|------|---------|--------|-------|
| Callers of `CreateOrder` | `get_callers` | ~27 s | PASS | 15 callers returned (truncated) |
| Call graph depth 1 | `get_call_graph` | ~1.3 s | PASS | Rel edges present |
| `go.mod` deps / impact | `get_dependencies` / `get_impact_radius` | ~1–2 s | PASS | Empty/zero for root `go.mod` (expected if no edges) |
| Explain / architecture / schema | `explain_node`, `get_architecture`, `get_graph_schema` | ~3–7 s | PASS | Bounded `max_items` |

### 3.4 Semantic (HNSW) — careful

| Project | Tool | Latency | Status | Evidence |
|---------|------|---------|--------|----------|
| `/workspace-other` | `semantic_search` (“refund failure…”) | ~106 s then drop | **FAIL** | `RemoteDisconnected`; prior run `OOMKilled=true`; mem peak ~3.37 GiB; logs: `all_elements()` + skip elements_cache (640998) |
| `/workspace` | `semantic_search` (“how does MCP handle tools”) | ~9.7 s | **PASS** | `method: hnsw+rerank`, `ann_candidate_count: 50`, hits in `src/mcp/server.rs` |
| `/workspace` | `kg_semantic_context` (“embedding control”) | ~7.7 s | **PASS** | Traversal from embed build seeds |

**Root symptom:** mega HNSW path still triggers deprecated `all_elements()` and memory spike (ONNX + vector index + element dump) that exceeds effective cgroup headroom.

---

## 4. `embed_control` idle resume

Sequence on `/workspace-other` (no MCP traffic during idle wait):

1. `action=status` — prior completed run: `skipped_fresh: 147175`, `vectors_existing: 147420`, `phase: idle`
2. `action=on` `mode=partial` `full=false` — `phase: waiting_idle` (“armed; will start when MCP idle…”)
3. Silent sleep **70 s**
4. `action=status` — `phase: completed`, `elapsed_s ≈ 2.74`, `embedded: 0`, `skipped_fresh: 147175`, `to_embed: 0`, `stale: 0`
5. `action=off` — cooperative cancel; armed cleared

**Verdict:** Day-2 zero-dirty resume behaves correctly (no full rebuild, honest skip counts, MCP stays healthy).

---

## 5. Aggregate focused suite

| Metric | Value |
|--------|-------|
| Calls in focused suite | 25 |
| PASS | 24 |
| FAIL | 1 (`semantic_search` on mega) |
| Final `/health` | ok |
| Restarts during suite | 1 (after mega HNSW OOM/disconnect) |

Earlier broader smoke (before memory monitoring) also saw cascade failures after the first HNSW OOM — expected once HTTP drops.

---

## 6. Recommendations

1. **Agents on mega:** prefer `concept_search` → `search_code` / `find_function`; avoid `semantic_search` / `kg_semantic_context` until mega-safe.
2. **Engineering:** remove `all_elements()` from HNSW / retrieval seed hydration; keep ANN + paginated element fetch only.
3. **Ops:** OrbStack/`docker stats` showed ~3.9 GiB effective limit despite compose `mem_limit` 6–10 g — raise VM/cgroup headroom if mega HNSW must run in-process.
4. **`embed_control`:** safe to arm for incremental resume on this volume; zero-dirty completes in a few seconds after idle gate.

---

*Report written 2026-07-20. Paths sanitized to `/workspace` / `/workspace-other` / `svc-x` placeholders only.*
