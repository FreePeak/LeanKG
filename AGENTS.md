# LeanKG — Agent Context

**Tech stack:** Go 1.25 (module `github.com/FreePeak/LeanKG/go`) + SQLite (WAL, default) + PostgreSQL/pgvector (opt-in) + official MCP go-sdk. v4.6.0: the Rust tree has been removed; this module is the codebase.

## Build & Test

```bash
make go-build            # CGO_ENABLED=0 binaries into go/bin/ (leankg, leankg-embed)
make go-test             # go test ./... -count=1  (15 packages)
make go-vet              # go vet ./...
make go-bench            # benchmark/ab suite
make dual-engine         # sqlite + live-PostgreSQL acceptance gate (needs :5433 pgvector)
make go-ui-assets        # re-sync ui build into internal/web/embed after a ui-v2 rebuild
```

Store contract: `go/internal/store/backend.go` (Backend interface; SQLite = *Store, PostgreSQL = PGStore). PG tests gate on `LEANKG_TEST_PG_URL` (local fixture: docker pgvector :5433, creds postgres/postgres, db leankg).

## CLI Quick Reference

|Command|Purpose|
|---|---|
|`leankg serve --http :9699 --rest :8080 --memory`|MCP HTTP + REST + memory (add `--rpc :9090` ConnectRPC, `--ui :8081` dashboard, `--read-only` reader role)|
|`leankg index <dir>`|Index a repository (3-signal incremental)|
|`leankg writer --project <dir>`|Index-once + fsnotify reconcile loop (pairs with `serve --read-only`)|
|`leankg query <text> [--kind name\|impact] [--depth N]`|CLI query (L1 exact → L2 fuzzy fallback; graph impact)|
|`leankg-embed run\|full`|Incremental / stamp-guarded full embed (provider via `LEANKG_EMBED_*`)|
|`leankg-embed export\|import`|NDJSON offsite flow (pairs with `scripts/embed_batch.py`)|
|`leankg connect\|install --target claude\|cursor\|codex\|gemini\|opencode\|omp`|Client wiring (+ `--register-cwd` SessionStart hook)|
|`leankg doctor --project <dir>`|Store diagnostics|

## SoT pairing

Narrative + ACs: [`docs/prd.md`](docs/prd.md) (v4.6.0 parity ledger + DEFERRED items). Statuses: [`docs/prd-task-tracker.md`](docs/prd-task-tracker.md).

## Development workflow

1. Update `docs/prd.md` + `docs/prd-task-tracker.md` (the only two live docs)
2. Implement in `go/` — Backend consumers take `store.Backend`, never a concrete store
3. `make go-build && make go-test && make go-vet`
4. Commit (one feature per commit; no AI attribution); main is protected → PRs only
5. `scripts/test-dual-engine.sh` for storage-layer changes

## Key source files

|File|Purpose|
|---|---|
|`go/cmd/leankg/`|serve/index/writer/query/doctor/connect/install|
|`go/cmd/leankg-embed/`|embedding pipeline binary|
|`go/internal/store/`|Backend interface, SQLite + PostgreSQL implementations|
|`go/internal/core/`|3-tool envelope + L0–L3 ladder|
|`go/internal/index/`, `docindex/`|extractors (regex ceiling until tree-sitter)|
|`go/internal/memory/`|full-markdown memory + mnemopi banks|
|`go/internal/embed/`|Provider port, ModelStamp guards, pipeline|
|`go/internal/graph/`|impact/path/callers/callees/context/explain|
|`go/internal/mcp/`, `rest/`, `rpc/`, `web/`|transports + UI|
|`go/CONTRACTS.md`|original wave contracts (historical)|

## Multi-project setup

MCP HTTP `?project=` walks to the nearest `.leankg`; `LEANKG_DB_ENGINE=postgres` + `LEANKG_PG_URL` switch storage. Never paste personal host paths into commits.

*Last updated: 2026-09-10 (Go-only cutover; Rust commands removed)*
