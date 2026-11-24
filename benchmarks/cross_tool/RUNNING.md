# Running the cross-tool benchmark

Status: setup and dry run are done. No agent trial has run, and no API call has
been made. A real run needs the approvals in section 5.

## 1. Prerequisites

| Need | How | Checked by |
|---|---|---|
| LeanKG binary | `make go-build` at the repo root (writes `bin/leankg`) | `plan.py` (leankg arm) |
| `claude` CLI | on PATH; version pinned in `tools.lock.yaml` `claude_code.version` | `plan.py` |
| Python with PyYAML | `python3 -m venv .venv && .venv/bin/pip install pyyaml`, then pass `PYTHON=.venv/bin/python` to `make`. The default `python3` (Makefile `PYTHON ?= python3`) may lack PyYAML; then `make selftest` stops with `ERROR: PyYAML required`. The default is unchanged. | every target |
| Node >= 24.15.0 | for the context-mode arm only (`engines.node` at the pinned tag) | `plan.py` (context-mode arm) |
| git | clones and `ls-remote` | `clone_repos.py` |

## 2. Pins

- `repos.lock.yaml` (generated, `make lock`): every `repos.yaml` ref resolved to a
  40-hex commit SHA. Annotated tags use the peeled commit, the one a clone's
  `HEAD` reports. All seven resolved on 2026-11-24. `make setup` checks out the
  locked SHA (not the branch tip) and fails on a mismatch; the lock changes only with `make lock` (`--lock-only`).
- `tools.lock.yaml` (hand-maintained):
  - `context_mode`: tag `v2.2.2` (newest stable tag), commit `65d228b6...`.
  - `claude_code.version`: **PLACEHOLDER**. Last verified 2.1.201 (Makefile comment, 2026-07-23), not re-verified.
  - `model.id`: **PLACEHOLDER**. A real run needs `MODEL=` to equal this value.

## 3. Setup and dry run (safe, no agents)

```bash
# from the repo root
make go-build
PY=.venv/bin/python   # any python3 with PyYAML
make -C benchmarks/cross_tool lock PYTHON=$PY
make -C benchmarks/cross_tool setup PYTHON=$PY          # clones 7 repos (vscode is large)
make -C benchmarks/cross_tool dry-run PYTHON=$PY MODEL=<pinned id>
# or, from the root:
make bench-cross-tool PYTHON=$PY CROSS_ARGS="MODEL=<pinned id>"
```

`dry-run` prints the plan (arms x repos x prompts x trials), the invocation count
and the blockers. Each real-run target runs the same check through
`plan.py --require-ready --arm <arm>` and stops with exit 3 while a blocker applies
to its arm. Confirmed on 2026-11-24: `make leankg REPO=gin N=4` stops at the gate
(`plan.py` exits 3; Make reports the target as failed with exit 2, and no agent runs).
The gate prints every blocker, including ones for other arms; only those for the
requested arm stop it.

### context-mode arm (documented; not executed here)

Pinned to `v2.2.2`. Do **not** use the Claude Code plugin install
(`claude plugin install context-mode@context-mode`): the plugin brings its hooks
into every session. The bench uses the npm package and an MCP-only config.

```bash
npm i -g context-mode@2.2.2        # NOT VERIFIED on 2026-11-24: the registry query timed out
export CONTEXT_MODE_SERVER="$(npm root -g)/context-mode/server.mjs"   # the bundled MCP server
# confirm the server starts (manual MCP handshake) before a real run
```

`install_leankg_mcp.sh context-mode` refuses to run unless `CONTEXT_MODE_SERVER`
names an existing file.

## 4. Real run (after approval, section 5)

```bash
# one repo, smoke test: 3 arms x N trials
make -C benchmarks/cross_tool all REPO=gin N=4 MODEL=<pinned id> PYTHON=$PY

# or one arm at a time
make -C benchmarks/cross_tool baseline     REPO=gin N=4 MODEL=<pinned id> PYTHON=$PY
make -C benchmarks/cross_tool context-mode REPO=gin N=4 MODEL=<pinned id> PYTHON=$PY
make -C benchmarks/cross_tool leankg       REPO=gin N=4 MODEL=<pinned id> PYTHON=$PY

# parallel across repos (one run_repo.sh per repo; each runs all three arms)
LEANKG_BIN=<abs>/bin/leankg CONTEXT_MODE_SERVER=<abs>/server.mjs \
  bash benchmarks/cross_tool/run_repo.sh <slug> 4 <pinned id>

# full suite, serial (7 repos x 3 arms x N)
make -C benchmarks/cross_tool full MODEL=<pinned id> N=4 PYTHON=$PY

# judge-blind quality, after the arms (7 judge calls)
make -C benchmarks/cross_tool score PYTHON=$PY
```

Minimum: N >= 3 valid trials per arm (`MinTrialsPerArm`). `N=4` is the default.

`make report` reads the arms found in the runs (`baseline`, `context-mode`,
`leankg`; legacy `with`/`without` also load) and writes one column group per arm
plus a losing-rows section. `make all` ends in that report step.

## 5. Cost drivers and what to approve

Invocations per full run = arms (3) x repos (7) x N (4) = **84 `claude -p` runs**,
plus 7 judge calls from `score.py`. For one repo, `all` = 3 x N = 12 runs.

Cost per run is not measured here. Cost scales with repo size (vscode is the
largest), model, and the number of tool calls. Measure it with a pilot before the
full run: one repo, N=3, all arms. Then estimate:
`total = invocations x median cost per run from the pilot`.

Approve each of these before a real run:

1. **Model id** and its pin in `tools.lock.yaml` (drives all cost).
2. **Claude Code version** pin (`claude_code.version`).
3. **Scope**: which repos, N (>= 3), and whether to run the full 84-run suite or the pilot first.
4. **Global install on the host**: `npm i -g context-mode@2.2.2`, and node >= 24.15.0.
5. **`leankg index` embed step**: `leankg index .` also embeds unless `--no-embed`. If a remote embedding provider is configured, that step costs money on every `leankg` trial. Choose `--no-embed` or confirm the provider.
6. **Confound acceptance**: the `leankg` and `context-mode` arms run without `--bare`, so the user's global CLAUDE.md, hooks and plugins load into those arms and not into `baseline` (`run_one.sh`, `BARE_FLAG`). Accept this, or run with a clean profile first.
7. **Raw-output bytes** for the context-mode byte metric. Only `tool_result_bytes` (delivered) is recorded. The raw side (what the raw tool output would have put in context) needs per-arm instrumentation. Decide whether to build it or report delivered bytes only.
8. **Judge model** for `score.py` (`--model`, empty = CLI default).
9. **Disk and network** for `make setup` (7 clones; vscode is large).

## 6. Results and reporting

Outputs (all under `results/`, gitignored):

| Path | Content |
|---|---|
| `results/runs/<YYYY-MM-DD>/<repo>/<arm>/runs.jsonl` | one JSON row per trial: cost, tokens, tool calls, wall time, `tool_result_bytes`, validity, pins |
| `results/runs/.../scratch/...` | raw `claude.json` envelope, `*.answer.txt`, stderr per trial |
| `results/scores/scores.jsonl` | judge-blind 0-6 scores, de-anonymized after scoring |
| `results/cross_tool-<date>.md` and `.json` | medians, IQR, quality, rigor checklist |

The REPORT must keep **losing rows**: any repo where `leankg` or `context-mode`
is worse than `baseline` on tokens, bytes, time or quality is shown, not dropped.

Pending before the first real report:

- The raw-output byte side (approval 7).

## 7. Known issues (pre-existing, reported, not fixed here)

- `score.py:45-53` duplicates `benchmark/ab/harness.go:353-360` (`Rubric`), and
  `score.py` duplicates the judge-blind shuffle and prompt build
  (`score.py:86-105`, `harness.go` `BlindGroups`/`JudgePrompt`).
- `aggregate.py` duplicates the median and trial gates of `harness.go`
  `Aggregate`/`Checklist`.
- `run_arm.sh` discards the index output (`>/dev/null 2>&1`), so a failed index
  would not stop the run.
- `benchmarks/baseline.json` references `scripts/perf_gate.sh`, which is missing
  (report only).
