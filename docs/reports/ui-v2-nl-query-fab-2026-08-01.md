# Smoke Report: Wave 3 NL Query FAB (FR-UI2-08 / US-UI2-06)

**Date:** 2026-08-01  
**Branch:** `agent/wave3-nl-query-fab-9aa3`  
**CLI version:** 0.19.27 (pre-bump)

## Intent

Query FAB default mode runs NL `query_graph` via `POST /api/query-graph`; Advanced mode keeps raw Cozo on `POST /api/query`.

## Unit tests

```bash
cd ui-v2 && npm test
# 7 files / 35 tests passed (incl. query-fab-mode + QueryFAB dual-mode)

cargo test --bin leankg query_graph_api
# 6 passed (validate + deserialize + execute_query_graph TempDir)
```

## Live fixture

```bash
# /tmp/wave3-nl-fab-smoke — tiny Rust auth/db graph
leankg init --path /tmp/wave3-nl-fab-smoke
leankg index ./src   # 9 elements, 15 relationships
leankg serve --port 8080 --project /tmp/wave3-nl-fab-smoke
```

### NL `POST /api/query-graph`

```bash
curl -sS -X POST http://127.0.0.1:8080/api/query-graph \
  -H 'Content-Type: application/json' \
  -d '{"question":"what connects auth to the database?","token_budget":2000,"max_depth":3}'
```

**Result:** `success: true`; seeds include `./src/auth.rs::authenticate` and `./src/db.rs::query_db`; neighborhood nodes/edges returned.

### Blank question rejected

```json
{"success":false,"data":null,"error":"question must not be empty"}
```

### Advanced `POST /api/query` still works

Raw Cozo `?[qn, name] := *code_elements{...}` returns rows (auth/db symbols present).

### Embedded UI assets

After `ui-v2` build → `src/embed/` + `cargo build --release`, served JS contains:

- `query-mode-nl`
- `Natural language query`
- `/api/query-graph`

## Tracker

| ID | Status |
|----|--------|
| `FR-UI2-08` | DONE |
| `US-UI2-06` | DONE |

## Follow-ups (not blocking Wave 3 product AC)

- Wave 4: `US-MG-02` / `FR-MG-03` single-repo expand
- Playwright e2e extended for mode toggles (runs when `:8080` + `:5173` up)

### OnRender deploy exit 101 (ops — blocks live demo refresh)

Live Render build fails at:

```text
cargo build --release --features embeddings … exit code: 101
```

**RCA:** [`root_cause_onrender_embeddings_exit101-2026-08-01.md`](root_cause_onrender_embeddings_exit101-2026-08-01.md)

| # | Follow-up | Status |
|--:|-----------|--------|
| F1 | Dockerfile: `libssl-dev` + `pkg-config` (embeddings → hf-hub → openssl-sys); extra RSS guards; bump `UI_EMBED_REV`; clear Render cache | **In this PR** |
| F2 | CI gate: embeddings Docker/`cargo build --features embeddings` when Dockerfile/Cargo.lock change | Open |
| F3 | Prebuilt image → Render pull (avoid 8 GB compile) | Open |
| F4 | Performance build pipeline if Starter still OOMs after Swift/ObjC | Open |
| F5 | Wave 4 single-repo expand (next P1 product) | Open |
