# Smoke Report: Wave 3 NL Query FAB (FR-UI2-08 / US-UI2-06)

**Date:** 2026-08-01  
**Branch:** `agent/wave3-nl-query-fab-9aa3`  
**CLI version:** 0.19.27 (pre-bump)  
**PR:** [#160](https://github.com/FreePeak/LeanKG/pull/160)

## Intent

Query FAB default mode runs NL `query_graph` via `POST /api/query-graph`; Advanced mode keeps raw Cozo on `POST /api/query`.

---

## Test matrix

| Layer | Command / probe | Result | Notes |
|-------|-----------------|--------|-------|
| **Unit — ui-v2** | `cd ui-v2 && npm test` | **35 passed** | `query-fab-mode.test.ts` (9), `query-fab.test.tsx` (3) |
| **Unit — Rust API** | `cargo test --bin leankg query_graph_api` | **6 passed** | validate, deserialize, TempDir `execute_query_graph` |
| **Unit — lib** | `cargo test --lib` | **744 passed** | CI gate |
| **CI (PR #160)** | Format, Clippy, Test Suite, UI v2 Typecheck | **All green** | GitHub Actions run `30702142089` |
| **Live — NL API** | `curl POST /api/query-graph` | **PASS** | auth→db seeds on fixture |
| **Live — validation** | blank `question` | **PASS** | `question must not be empty` |
| **Live — Advanced** | `curl POST /api/query` raw Cozo | **PASS** | rows returned |
| **Live — embed assets** | served JS grep | **PASS** | `query-mode-nl`, `/api/query-graph` |
| **E2E Playwright** | `E2E=1 npm run test:e2e` | **Not run** | `shell-parity.spec.ts` extended for mode toggles; needs `:8080` + `:5173` |
| **OnRender deploy** | Render Docker build | **Pending ops** | Dockerfile fix in PR; `REL-ONRENDER-101` PARTIAL until cache clear + green deploy |

---

## Unit tests (re-run 2026-08-01)

```bash
cd ui-v2 && npm test
# 7 files / 35 tests passed

cargo test --bin leankg query_graph_api
# 6 passed

cargo test --lib
# 744 passed; 3 ignored
```

---

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

---

## Documents updated (this PR)

| Doc | Update |
|-----|--------|
| `docs/prd-task-tracker.md` + `.json` | Wave 3 DONE; Wave 4 CURRENT; OnRender ops row |
| `docs/prd.md` | CURRENT next; §1.1 queue; pain-point row |
| `docs/reports/ui-v2-nl-query-fab-2026-08-01.md` | This report |
| `docs/reports/root_cause_onrender_embeddings_exit101-2026-08-01.md` | OnRender exit 101 RCA |
| `docs/erd/ui-v2-erd.md` | `POST /api/query-graph` |
| `docs/web-ui.md` | Query FAB dual-mode |
| `ui-v2/README.md` | Query FAB dual-mode |
| `docs/reports/ui-v2-cutover-evidence-2026-07-21.md` | Wave 3 struck through |
| `docs/prs/pr-160-description.md` | PR body copy |

---

## Tracker

| ID | Status |
|----|--------|
| `FR-UI2-08` | DONE |
| `US-UI2-06` | DONE |
| `REL-ONRENDER-101` | PARTIAL (Dockerfile fix landed; live Render deploy pending) |

---

## Follow-ups (not blocking Wave 3 AC)

- Wave 4: `US-MG-02` / `FR-MG-03` single-repo expand
- Playwright e2e run with `E2E=1` (mode toggle assertion in `shell-parity.spec.ts`)
- OnRender: Manual Deploy + Clear build cache after merge
