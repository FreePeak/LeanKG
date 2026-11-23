# Live-test harness

Re-runnable, local, end-to-end checks against the real `leankg` binary. It builds
the binary into the run's temp dir (never into the checkout's `bin/`), creates a
temp Go fixture and temp store, starts `leankg serve` on a free loopback port, runs
every scenario, and writes evidence. SQLite by default, no network, no containers.
The PostgreSQL gates run only when `LIVE_PG_URL` is given.

## Run

```bash
make live-test                          # build, then run all scenarios
make live-test LIVE_ARGS="--only 03"    # one scenario
make live-test LIVE_PG_URL=postgres://... LIVE_BASELINE_BIN=/abs/baseline/leankg
```

Run a prebuilt binary (no build) and label the run so evidence directories stay
distinguishable:

```bash
bash scripts/live/run.sh --bin /path/to/leankg --label baseline
LEANKG_BIN=/path/to/leankg bash scripts/live/run.sh --label wave1   # same, via env
make live-test LIVE_ARGS="--bin /path/to/leankg --label baseline"
```

`--bin PATH` must be an executable file. `LEANKG_BIN` is read before the harness
clears its `LEANKG_*` environment, and `--bin` wins when both are set. `--label NAME`
takes 1-40 characters of `[A-Za-z0-9._-]` and is appended to the evidence dir name
(`.live-evidence/<UTC timestamp>-<NAME>/`).

Requires `make`, `go`, `curl`, `jq`, `nc`, `perl` and `shasum`. `shellcheck` is optional.

Exit status is 0 when no assertion fails and 1 otherwise. A `[product]` assertion
fails while its product bug exists, so the run stays red until the bug is fixed.
`summary.md` lists those failures separately from harness failures. Against the
baseline binary, the `[fixed]` assertions fail by design, so exit 1 is expected there.
The harness is for local runs, not for CI.

## Layout

| Path | Role |
|---|---|
| `run.sh` | Entry point: build, temp dirs, scenario loop, teardown, summary |
| `lib.sh` | Helpers: `run_cmd`, `http_request`, `mcp_request`, `mcp_tool`, `mcp_initialize`, `retry_until`, `start_server`, `wait_listener_health`, `stop_pid`, `free_port`, `unit_gate`, `assert_eq`, `assert_contains`, `assert_json`, `skip`, fixture |
| `scenarios/NN-name.sh` | One feature area; defines `scenario_main` |

## Scenarios

| ID | Area |
|---|---|
| 01 | version, status, `/health`, MCP `initialize` |
| 02 | index a fixture; elements and cross-file edges |
| 03 | incremental re-index keeps cross-file edges (#598), matches a fresh index |
| 04 | MCP surface is exactly `import`, `query`, `status`, `authenticate` (HTTP and stdio) |
| 05 | query ladder: L1 exact, L2 keyword fallback, miss (MCP and CLI) |
| 06 | auth gate after `auth admin init`: anonymous, bad bearer, login, REST |
| 07 | `[product]` precision checks: false call edge, element span, unstable name resolution |
| 08 | auth hardening (#586): token list visibility, HTTP surface |
| 09 | memory redaction (#591): secret-looking document ids and bare tokens stored redacted |
| 10 | embed run ledger (#587): SIGINT ends a run as `cancelled`, exit 130 |
| 11 | CLI hygiene (#592): single usage print, `leankg-embed` reads `db.url` from `leankg.yaml` |
| 12 | FR-ZCP-01 fail-closed routing on a multi-project server, stdio single-project |
| 13 | HTTP contract (#590): malformed JSON answers 400 `INVALID_PARAM`; `token_budget` 0 is 400 |
| 14 | cancellation exit codes (#589): `leankg run` and `update --check` exit 130 on SIGINT |
| 15 | registration (#592): duplicate email and short password answer generic 400s; no docs conflict markers |
| 16 | store parity (#588, #592): FTS rowid alignment, baseline schema-19 migration, absent `emb_stamp`; PG unit gates with `LIVE_PG_URL` |
| 17 | embed recall (#587): a hanging dense arm answers within 25 s with the keyword row |
| 18 | memory and docs (#591): pre-redaction rows compacted, markdown ownership independent of run order |
| 19 | HTTP contract 2 (#590): body over 1 MiB answers 413; store failure answers 500 (REST) and 503 (UI) |
| 20 | cancellation 2 (#589): SIGINT with a grandchild holding stdout exits 130 within 5 s |
| 21 | index re-extract (extractor-version bump): a pre-fix store is re-extracted once on the first incremental index; needs `LIVE_SEED_BIN` for the pre-fix binary |

## Options and environment

`run.sh` options:

| Option | Effect |
|---|---|
| `--only NN` | run one scenario; `NN` is digits only, otherwise exit 2 |
| `--no-build` | skip the build; needs `--bin` (or `LEANKG_BIN`), since the build output is per run |
| `--bin PATH` | run a prebuilt binary (implies `--no-build`); must be an executable file |
| `--label NAME` | 1-40 characters of `[A-Za-z0-9._-]`; recorded in `summary.md` and appended to the evidence dir name |
| `--pg-url URL` | sets `LIVE_PG_URL` for scenario 16's PostgreSQL gates; use a private pgvector database |
| `--baseline-bin PATH` | sets `LIVE_BASELINE_BIN` for scenario 16 (schema-19 migration check) |
| `--seed-bin PATH` | sets `LIVE_SEED_BIN` for scenario 21 (pre-fix binary) |

`run.sh` unsets every `LEANKG_*` variable and every `LIVE_*` variable before the
scenarios start, so a developer's own tokens, DB engine, project list or exported
`LIVE_*` values cannot change the run. The `LIVE_*` inputs reach the scenarios only
through the flags above. `make live-test` passes the make-line variables
`LIVE_PG_URL`, `LIVE_BASELINE_BIN` and `LIVE_SEED_BIN` as those flags. Pass absolute
paths; the flags resolve them to absolute paths.

| Input (flag or make variable) | Used by | Meaning |
|---|---|---|
| `LIVE_SEED_BIN` | 21 | the pre-fix binary that writes the store the scenario re-indexes; defaults to the binary under test, so a baseline run shows the `[fixed]` assertions failing |
| `LIVE_BASELINE_BIN` | 16 | the baseline `leankg` for the schema-19 migration check (b); SKIP without it |
| `LIVE_PG_URL` | 16 | PostgreSQL URL for the PG unit gates; they run against that database only and SKIP without it. Use a private pgvector container, never the CI one |

Example:

```bash
bash scripts/live/run.sh --bin /abs/candidate/leankg --label candidate \
  --seed-bin /abs/baseline/leankg --baseline-bin /abs/baseline/leankg
```

## Add a scenario

1. Create `scenarios/NN-short-name.sh`. `NN` is the next free two-digit number.
2. Define `scenario_main`. Use the helpers in `lib.sh`; do not redefine them.
3. Assert with `assert_eq`, `assert_contains` or `assert_json`. A label starting
   with `[product]` marks a known product bug.
4. Call `skip "reason"` and `return` when a prerequisite is missing.
5. Run `bash scripts/live/run.sh --only NN` until it is green or shows the
   intended product failure.

Keep scenarios independent. Use `ensure_fixture` for the shared index, or write
your own fixture under `$WORK` when the scenario changes the store (for example,
it turns on the auth gate). Every curl gets `--max-time "$CURL_MAX_TIME"` by
default, and every process a scenario starts goes through `stop_pid`, so a hung
server or stub cannot stall the run.

## Evidence

Each run writes `.live-evidence/<UTC timestamp>[-<label>]/`. `.live-evidence/latest`
links to the newest run. The directory is git-ignored.

`summary.md` records the label, the binary's file name and SHA-256, and its
`version` output. `Git HEAD` is the checkout that hosts the harness, not necessarily
the source of a `--bin` binary, so use the SHA-256 to tell binaries apart.

| File | Content |
|---|---|
| `summary.md` | Result, scenario table, every assertion with expected and actual, product failures |
| `summary.json` | The same data, machine-readable |
| `env.txt` | Git HEAD, branch, `VERSION`, leankg version, Go version, host |
| `scenarios/NN/scenario.log` | Commands with exit codes, HTTP status, assertion lines |
| `scenarios/NN/captures/` | Redacted command stdout and stderr, HTTP request and response |
| `logs/` | `build.log` (the `go build` of this run) and the `leankg serve` log of each server |
| `results.tsv` | Raw assertion rows that `summary.*` is built from |

Tokens, passwords, the `Authorization` and `X-CSRF-Token` headers are redacted, and
the temp workdir and repo path appear as `<workdir>` and `<repo>`. Raw output lives
only under the temp workdir and is removed at exit. Set `LIVE_KEEP_WORK=1` to keep
it for debugging.

## Read summary.md

1. Check the `Result` line and the `Scenarios` count.
2. Failed assertions appear in the Assertions table with expected and actual values.
3. Under "Product failures", each entry names the capture file that holds the evidence.
