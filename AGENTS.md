# LeanKG — Agent Context

**Tech stack:** Go 1.25 (module `github.com/FreePeak/LeanKG/go`) + SQLite (WAL, default) + PostgreSQL/pgvector (opt-in) + official MCP go-sdk. The Rust tree is removed; `go/` is the codebase.

## Build & Test

```bash
make go-build            # CGO_ENABLED=0 binaries into go/bin/ (leankg, leankg-embed)
make go-test             # go test ./... -count=1  (54 packages)
make go-vet              # go vet ./...
make go-bench            # benchmark/ab suite
make go-build-tstree     # build with the tree-sitter tier (-tags tstree, CGO)
make go-test-tstree      # test that same tier
make dual-engine         # sqlite + live-PostgreSQL acceptance gate (needs :5433 pgvector)
make go-ui-assets        # build ui-v2 and sync dist/ into go/internal/web/embed
```

Store contract: `go/internal/store/backend.go` (Backend interface; SQLite = *Store, PostgreSQL = PGStore). PG tests gate on `LEANKG_TEST_PG_URL` (local fixture: docker pgvector :5433, creds postgres/postgres, db leankg).

Test-layer policy (owner decision, 2026-09-14): **unit tests stay in-process
and fast** — no real provider/network calls, no sleeps; `go test ./...` is
the gate. Anything making a real embedding/LLM/PG call or measuring timing is
an **integration/e2e test** gated on an env var (`LEANKG_TEST_PG_URL`, live
sidecar) or its own job (`ttfv`); **benchmarks (`make go-bench`,
`benchmark/ab`) never run in CI** — they are explicit local/nightly tools.
The `go-engine` CI job runs only the in-process suite (both tag sets) with a
300 s budget.

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

Narrative + ACs: [`docs/prd.md`](docs/prd.md) (parity ledger + DEFERRED items). Statuses: [`docs/prd-task-tracker.md`](docs/prd-task-tracker.md).

**Current milestone (2026-09-14, PRD v4.11.0): M10 — the self-host dogfood loop.** Once the in-flight
refactor waves land, bring up the dynamic HTTP server (`leankg serve --http :9699 --rest :8080 --ui :8081 --memory`)
over this repository — index + embed + memorize — and use it to build/fix LeanKG itself (PRD §3.10 + §5.1 runbook)
before scaling to small nested-repo parents. Never bulk-index the `freepeak` portfolio root.

## Development workflow

1. Update `docs/prd.md` + `docs/prd-task-tracker.md` (the only two live docs)
2. Implement in `go/` — Backend consumers take `store.Backend`, never a concrete store
3. `make go-build && make go-test && make go-vet`
4. Commit (one feature per commit; no AI attribution); main is protected → PRs only
5. `scripts/test-dual-engine.sh` for storage-layer changes

## Release

One workflow: `.github/workflows/release.yml`. A merge to `main` makes
release-please open/update the release PR; merging *that* cuts `vX.Y.Z`, publishes
the GitHub Release and builds the four `leankg-<goos>-<goarch>.tgz` assets **in the
same run** — deliberately, because a tag pushed with `GITHUB_TOKEN` cannot trigger a
follow-up workflow (that is how v0.28.1–v0.30.0 shipped with no binaries).

- Version source of truth: `go/cmd/leankg/VERSION`, declared as release-please's
  `version-file`; `internal/mcp/server.go` keeps the `x-release-please-version`
  marker for its copy, and `internal/mcp/version_test.go` fails on drift.
- `.release-please-manifest.json` is keyed by package **path** (`"."`). A key of
  `main` is never read, and any non-version value (e.g. a `$schema` key) makes
  release-please throw while loading the manifest.
- `leankg update` polls `releases/latest`, so the release must be non-draft and
  marked latest; asset names and the archive layout are a contract with
  `go/internal/update`. `--latest` is claimed only when the version is at least
  the current latest, so re-publishing an older release cannot downgrade clients.
- `scripts/release_workflow_guards.py` (a CI step) pins three invariants of that
  workflow: no job may gate on `inputs.*` (empty in a job-level `if` on
  dispatch), publish must assert the asset count, and `--latest` must stay
  conditional. Each maps to a defect that shipped a green run with zero
  binaries — edit `release.yml` and CI will tell you which one you re-introduced.

## Key source files

|File|Purpose|
|---|---|
|`go/cmd/leankg/`|serve/index/writer/query/doctor/connect/install|
|`go/cmd/leankg-embed/`|embedding pipeline binary|
|`go/internal/store/`|Backend interface, SQLite + PostgreSQL implementations|
|`go/internal/core/`|3-tool envelope + L0–L3 ladder|
|`go/internal/index/`, `docindex/`, `prdindex/`|code / markdown-doc / PRD-requirement extractors (regex tier; tree-sitter behind `-tags tstree`)|
|`go/internal/memory/`|full-markdown memory + mnemopi banks|
|`go/internal/embed/`|Provider port, ModelStamp guards, pipeline|
|`go/internal/graph/`|impact/path/callers/callees/context/explain|
|`go/internal/mcp/`, `rest/`, `rpc/`, `web/`|transports + UI|
|`go/internal/tstree/`|vendored generated grammars (objc/dart/perl) — build-tag gated|

## Multi-project setup

MCP HTTP `?project=` walks to the nearest `.leankg`; `LEANKG_DB_ENGINE=postgres` + `LEANKG_PG_URL` switch storage. Never paste personal host paths into commits.

*Last updated: 2026-09-14 (post-cutover hygiene sweep, Go tree restructure, single-run release pipeline + its CI guards)*
