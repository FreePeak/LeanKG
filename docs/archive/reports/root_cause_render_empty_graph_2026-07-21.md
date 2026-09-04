# Root Cause Analysis: leankg.onrender.com empty graph after deploy

**Date:** 2026-07-21  
**Live:** https://leankg.onrender.com/  
**Symptom:** UI loads (ui-v2) but graph is empty; `/api/index/status` shows `element_count: 0`, `project_path: "/"`.

---

## Evidence (live, post OOM fix deploy)

```json
GET /api/index/status
{
  "element_count": 0,
  "relationship_count": 0,
  "project_path": "/",
  "total_files": 0
}

GET /api/graph/data
{ "nodes": [], "relationships": [] }
```

```json
GET /api/ui-build
{ "ui": "ui-v2", "rev": "2026-07-21-onrender-rca3" }
```

UI and binary are fresh; **knowledge graph data is missing**.

---

## Root causes (ranked)

### RC-1 — Runtime image is binary-only (primary)

Multi-stage OOM fix (`2f9f7e6`) copied only `/usr/local/bin/leankg` into the runtime stage. No `src/`, no `.leankg/`, no `leankg.yaml`.

`leankg web` opens `db_path = project_root/.leankg`. With no baked index, the DB is empty.

### RC-2 — No `WORKDIR` → project resolves to `/` (contributing)

Runtime stage had no `WORKDIR`. Container CWD defaults to `/`. `resolve_serve_project` → `find_project_root()` returns `/` (no `.leankg` marker). Status API reports `project_path: "/"`.

### RC-3 — Demo index step dropped in earlier Dockerfile refactors (latent)

Commit `662a65f` (Apr 2026) correctly ran:

```dockerfile
RUN leankg init --path .leankg && leankg index /app/src
COPY --from=builder /app/.leankg /app/.leankg
WORKDIR /app
```

Later ui-v2 / embeddings refactors (`90e0f9d`, `e85acb2`) removed indexing. OOM fix preserved that gap.

### RC-4 — No runtime auto-index on `leankg web` (by design)

`web::start_server` loads existing `.leankg` but does **not** index on boot. Reindex only happens via `POST /api/project/switch` when the path is empty. OnRender has no source tree to index at runtime.

---

## Fix (implemented)

In `Dockerfile` builder stage (after `cargo build`):

```dockerfile
RUN leankg init --path .leankg \
    && leankg index src \
    && test -f .leankg/leankg.db
```

Runtime stage:

```dockerfile
WORKDIR /app
COPY --from=builder /app/src ./src
COPY --from=builder /app/ontology ./ontology
COPY --from=builder /app/.leankg ./.leankg
COPY --from=builder /app/leankg.yaml ./leankg.yaml
ENV LEANKG_SERVE_PROJECT=/app
```

Expected after deploy: ~6k elements, ~39k relationships (LeanKG `src/` only).

---

## RC-5 — Stale `?project=/` URL forces empty RocksDB (contributing)

ui-v2 reads `?project=` on boot and calls `POST /api/project/switch` with that path.
`?project=%2F` switches serve to filesystem root `/`, creates empty `/.leankg`, and
overrides any baked demo index at `/app`.

**UI fix:** `parseProjectParam` rejects `/` and `.`; backend `api_switch_path` rejects `/`.

---

## Acceptance

- [ ] `/api/index/status` → `project_path: "/app"`, `element_count > 0`
- [ ] `/api/graph/data` returns nodes
- [ ] UI shows service topology / expandable graph
- [ ] `https://leankg.onrender.com/` works without `?project=/`
- [ ] Build still passes Render Starter 8 GB cap (index step is << rustc peak)
