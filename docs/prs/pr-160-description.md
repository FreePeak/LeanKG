# PR #160 description (copy into GitHub PR body)

## Summary

Closes **P1 Wave 3** (NL Query FAB) and fixes the **OnRender embeddings Docker build exit 101** that blocked live demo deploys.

### Wave 3 — NL Query FAB (`FR-UI2-08` / `US-UI2-06`)

- Tracker sync: waves 2b/2c DONE; P1 CURRENT advances to Wave 4 after this
- TDD: Vitest dual-mode FAB + Rust `query_graph_api` unit tests
- Backend: `POST /api/query-graph` → `GraphEngine::query_graph`
- Frontend: Query FAB default = NL; Advanced = raw Cozo `/api/query`
- Live smoke: [`docs/reports/ui-v2-nl-query-fab-2026-08-01.md`](../reports/ui-v2-nl-query-fab-2026-08-01.md)
- ui-v2 rebuilt into `src/embed/`

### OnRender fix (same PR)

Failing step on Render:

```text
cargo build --release --features embeddings … exit code: 101
```

**RCA:** [`docs/reports/root_cause_onrender_embeddings_exit101-2026-08-01.md`](../reports/root_cause_onrender_embeddings_exit101-2026-08-01.md)

| Fix | Detail |
|-----|--------|
| `libssl-dev` + `pkg-config` | embeddings → hf-hub → native-tls → openssl-sys |
| RSS guards | `CARGO_INCREMENTAL=0`, `RUSTFLAGS=-C debuginfo=0` (plus existing `CARGO_BUILD_JOBS=1`, LTO off) |
| Cache bust | bump `UI_EMBED_REV` in `Dockerfile` + `render.yaml` |
| Hub parity | same apt packages in `Dockerfile.rocksdb` |

**Ops after merge:** Render → Manual Deploy → **Clear build cache**; verify `/api/ui-build` + `/api/index/status`.

### Open follow-ups (not this PR)

- F2: CI gate for embeddings Docker build
- F3: Prebuilt image → Render pull
- F4: Performance pipeline if Starter still OOMs
- F5: Wave 4 single-repo expand (`US-MG-02` / `FR-MG-03`)

## Type of Change

- [x] New feature
- [x] Bug fix (OnRender Docker build)
- [x] Documentation update

## Testing

- [x] Unit tests pass (`cargo test --lib` — 744)
- [x] `cargo test --bin leankg query_graph_api` (6)
- [x] `cd ui-v2 && npm test` (35)
- [x] Live `leankg serve` NL/Advanced curl smoke
- [x] CI green on this PR

## Checklist

- [x] Code follows project conventions
- [x] Self-review completed
- [x] Documentation updated (RCA + tracker + smoke reports)
- [x] No new warnings or errors (CI)

## Breaking Changes

None.

## Related Issues

Wave 3: `US-UI2-06` / `FR-UI2-08`  
OnRender: exit 101 on `cargo build --features embeddings`

## Additional Context

Prior OOM RCA: [`docs/reports/root_cause_render_build_oom_2026-07-21.md`](../reports/root_cause_render_build_oom_2026-07-21.md)
