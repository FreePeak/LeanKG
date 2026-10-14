# LeanKG — Agent Context

**Tech stack:** Go 1.25 (module `github.com/FreePeak/LeanKG`) + SQLite (WAL, default) + PostgreSQL/pgvector (opt-in) + official MCP go-sdk. The Rust tree is removed; the repository root is the Go module.

## Build & Test

```bash
make go-build            # CGO_ENABLED=0 binaries into bin/ (leankg, leankg-embed)
make go-test             # go test ./... -count=1  (54 packages)
make go-vet              # go vet ./...
make go-bench            # benchmark/ab suite
make go-build-tstree     # build with the tree-sitter tier (-tags tstree, CGO)
make go-test-tstree      # test that same tier
make dual-engine         # sqlite + live-PostgreSQL acceptance gate (needs :5433 pgvector)
make go-ui-assets        # build ui-v2 and sync dist/ into internal/web/embed
```

Store contract: `internal/store/backend.go` (Backend interface; SQLite = *Store, PostgreSQL = PGStore). PG tests gate on `LEANKG_TEST_PG_URL` (local fixture: docker pgvector :5433, creds postgres/postgres, db leankg).

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
|`leankg index <dir> [--hook] [--no-embed]`|Index a repository (3-signal incremental). A git repo root with commits reads HEAD git objects (uncommitted/untracked/ignored files skipped; change detection by blob ID); a directory without its own `.git` or a repo with no commits keeps the filesystem scan. Then runs the incremental embed unless `--no-embed`; embed failure is reported, not fatal. `--hook` exits 0 without indexing when HEAD has more than `LEANKG_HOOK_MAX_FILES` (default 10000) indexable files (printing the manual `leankg index <dir>` to run instead), or when another index of the store is running; hook embed only attaches to a running local provider and skips (one `embed skipped:` line, exit 0) when more than `LEANKG_HOOK_MAX_EMBED` (default 500) elements need vectors|
|`leankg writer --project <dir>`|Index-once + fsnotify reconcile loop (pairs with `serve --read-only`). For a git-backed store a new commit (change of `.git/logs/HEAD`) triggers a reindex; saving without committing does not change the graph|
|`leankg query <text> [--kind name\|impact] [--depth N]`|CLI query (L1 exact → L2 fuzzy fallback; graph impact)|
|`leankg-embed run\|full`|Incremental / stamp-guarded full embed (provider via `LEANKG_EMBED_*`). `leankg index` now runs the incremental embed itself|
|`leankg-embed export\|import`|NDJSON offsite flow (pairs with `scripts/embed_batch.py`)|
|`leankg connect\|install --target claude\|cursor\|codex\|gemini\|opencode\|omp`|Client wiring (+ `--register-cwd` async SessionStart hook, matcher `startup\|resume`, running `leankg index --hook "$CLAUDE_PROJECT_DIR"`)|
|`leankg doctor --project <dir>`|Store diagnostics|
|`leankg dashboard [--addr 127.0.0.1:9701] [--no-open]`|Web dashboard (embedded `ui-dashboard`): per-session efficiency metrics, failures, memory, replay; `--format text\|json` keeps the usage tables|
|`leankg telemetry status\|enable\|disable\|purge\|link\|import-ab`|Consent-gated capture (off by default) and transcript linking; see `docs/telemetry.md`|
|`leankg metrics --session [--session-id ID]`|Latest (or named) agent session from the telemetry ledger|

## SoT pairing

Narrative + ACs: [`docs/prd.md`](docs/prd.md) (parity ledger + DEFERRED items). Statuses: [`docs/prd-task-tracker.md`](docs/prd-task-tracker.md).

**Current milestone (2026-09-14, PRD v4.11.0): M10 — the self-host dogfood loop.** Once the in-flight
refactor waves land, bring up the dynamic HTTP server (`leankg serve --http :9699 --rest :8080 --ui :8081 --memory`)
over this repository — index + embed + memorize — and use it to build/fix LeanKG itself (PRD §3.10 + §5.1 runbook)
before scaling to small nested-repo parents. Never bulk-index the `freepeak` portfolio root.

## Development workflow

1. Update `docs/prd.md` + `docs/prd-task-tracker.md` (the only two live docs)
2. Implement in `internal/` + `cmd/` — Backend consumers take `store.Backend`, never a concrete store
3. `make go-build && make go-test && make go-vet`
4. Commit (one feature per commit; no AI attribution); main is protected → PRs only
5. `scripts/test-dual-engine.sh` for storage-layer changes

## Release

One workflow: `.github/workflows/release.yml`. A merge to `main` makes
release-please open/update the release PR; merging *that* cuts `vX.Y.Z`, publishes
the GitHub Release and builds the four `leankg-<goos>-<goarch>.tgz` assets **in the
same run** — deliberately, because a tag pushed with `GITHUB_TOKEN` cannot trigger a
follow-up workflow (that is how v0.28.1–v0.30.0 shipped with no binaries).

- Version source of truth: `cmd/leankg/VERSION`, declared as release-please's
  `version-file`; `internal/mcp/server.go` keeps the `x-release-please-version`
  marker for its copy, and `internal/mcp/version_test.go` fails on drift.
- `.release-please-manifest.json` is keyed by package **path** (`"."`). A key of
  `main` is never read, and any non-version value (e.g. a `$schema` key) makes
  release-please throw while loading the manifest.
- The simple release strategy's releasable commit types exclude `chore`/`refactor`/`docs`:
  a merged `refactor!`/`BREAKING CHANGE` squash opens **no** release PR ("No user facing
  commits found" in the version job). When the cut must ride such a commit — e.g. the
  #403 module-root move, shipped as v0.33.0 — declare the version deliberately with an
  empty `Release-As: X.Y.Z` trailer commit; it forces the release PR on the next push.
- `leankg update` polls `releases/latest`, so the release must be non-draft and
  marked latest; asset names and the archive layout are a contract with
  `internal/update`. `--latest` is claimed only when the version is at least
  the current latest, so re-publishing an older release cannot downgrade clients.
- `scripts/release_workflow_guards.py` (a CI step) pins three invariants of that
  workflow: no job may gate on `inputs.*` (empty in a job-level `if` on
  dispatch), publish must assert the asset count, and `--latest` must stay
  conditional. Each maps to a defect that shipped a green run with zero
  binaries — edit `release.yml` and CI will tell you which one you re-introduced.

## Key source files

|File|Purpose|
|---|---|
|`cmd/leankg/`|serve/index/writer/query/doctor/connect/install|
|`cmd/leankg-embed/`|embedding pipeline binary|
|`internal/store/`|Backend interface, SQLite + PostgreSQL implementations|
|`internal/core/`|3-tool envelope + L0–L3 ladder|
|`internal/index/`, `docindex/`, `prdindex/`|code / markdown-doc / PRD-requirement extractors (regex tier; tree-sitter behind `-tags tstree`)|
|`internal/memory/`|full-markdown memory + mnemopi banks|
|`internal/embed/`|Provider port, ModelStamp guards, pipeline|
|`internal/graph/`|impact/path/callers/callees/context/explain|
|`internal/mcp/`, `rest/`, `rpc/`, `web/`|transports + UI|
|`internal/telemetry/`|per-user capture ledger (`$LEANKG_HOME/telemetry.db`), consent config, redaction, classifier; `metrics/` builds the dashboard reports, `report/` is the API contract|
|`internal/sessionlink/`|read-only, consent-gated linking of calls to agent transcripts (one adapter per client)|
|`internal/dashboard/`, `ui-dashboard/`|`leankg dashboard` server + SPA (`make go-ui-dashboard` builds and embeds it)|
|`internal/tstree/`|vendored generated grammars (objc/dart/perl) — build-tag gated|

## Multi-project setup

MCP HTTP `?project=` walks to the nearest `.leankg`; `LEANKG_DB_ENGINE=postgres` + `LEANKG_PG_URL` switch storage. Never paste personal host paths into commits.

## The self-host: ONE server, and how to restart it without being fooled

`leankg serve` is supervised by launchd (`com.freepeak.leankg-serve`, program
`~/.local/bin/leankg-serve-wrapper`, which scans `$LEANKG_SCAN_ROOT` and passes
`--http 127.0.0.1:9699 --rest :9700 --memory --embed-provider local
--hindsight-compat`). Restart it with:

```bash
launchctl kickstart -k gui/$(id -u)/com.freepeak.leankg-serve
```

**A restart can leave a duplicate behind, and the duplicate can be the one
answering you.** Measured 2026-10-12: a manually started server (`--http :9699`,
wildcard) was still alive 2h41m later on the SAME ports as launchd's
(`--http 127.0.0.1:9699`). Both bound successfully — different address families
— and `curl 127.0.0.1:9699` reached whichever held the IPv4 socket. So a
"restart then verify" loop can read a server it never restarted, and every
conclusion drawn from it is about the wrong binary.

Two habits that avoid it:

```bash
# 1. Find the LISTENERS, not the processes you meant to kill. A pkill pattern
#    copied from the wrapper misses any server started with a different argv.
lsof -nP -iTCP:9699 -sTCP:LISTEN    # expect exactly ONE pid
lsof -nP -iTCP:9700 -sTCP:LISTEN    # and the same pid here

# 2. Kill by the LISTENER's pid, then confirm the port is free before starting.
kill -TERM <pid>; sleep 3; lsof -nP -iTCP:9699 -sTCP:LISTEN   # empty
```

Both ports must be owned by the SAME pid after a restart. If `:9699` answers but
`:9700` returns `000`, a stale server holds the REST port and the fresh one lost
it — the surface you are testing is half of two different processes.

*Last updated: 2026-10-12 (self-host restart runbook: the duplicate-listener trap and the lsof check that catches it)*
