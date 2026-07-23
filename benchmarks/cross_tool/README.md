# Cross-Tool Agent A/B Benchmark

A codegraph/graphify-style headless-agent benchmark that compares a fixed
`claude -p` (Claude Code) agent answering one architecture question on seven
real-world codebases — **WITH** LeanKG MCP enabled vs **WITHOUT** (empty MCP
config, built-in Read/Grep/Bash available to both). Numbers are directly
comparable to `colbymchenry/codegraph`'s published 7-repo suite (re-validated
2026-07-21, Opus 4.8).

## What we measure (per run, per arm, per repo)

| Metric | Source |
|---|---|
| Tool calls | `tool_use_count` from `claude -p --output-format json` envelope |
| Wall-clock time | `time` around the `claude -p` call |
| File reads | `file_read_count` from envelope (or transcript if exposed) |
| Input / output / cache-read tokens | `usage.input_tokens`, `usage.output_tokens`, `usage.cache_read_input_tokens` |
| Cost | `total_cost_usd` |
| Exit reason | `stop_reason`, `exit_code` |

The aggregator (`aggregate.py`) reports the **median** of N runs per arm per
repo, matching codegraph's methodology. IQR is shown in an appendix so
reviewers can see variance (the WITH arm should be tighter).

## Layout

```
benchmarks/cross_tool/
├── README.md                 # this file
├── Makefile                  # make setup / with / without / report / full
├── repos.yaml                # the 7 repos + prompts
├── clone_repos.py            # shallow-clones each repo at its pinned ref
├── install_leankg_mcp.sh     # emit strict --strict-mcp-config JSON for an arm
├── run_one.sh                # run one (repo, arm, run_idx) and append JSONL
├── get_prompt.py             # print the prompt for a given repo slug
├── aggregate.py              # read all JSONL, emit cross_tool-YYYY-MM-DD.{md,json}
├── repos/                    # shallow clones (gitignored; created by `make setup`)
└── results/
    ├── runs/YYYY-MM-DD/<repo>/<arm>/runs.jsonl
    ├── scratch/...
    └── cross_tool-YYYY-MM-DD.md
    └── cross_tool-YYYY-MM-DD.json
```

## Running

Prereqs:

- `claude` CLI >= 2.1.0 on PATH (verified 2.1.201)
- A working `leankg` binary on PATH or in `../../target/release/leankg`
  (`cargo build --release` builds it)
- `python3` with `pyyaml` (`pip install pyyaml`)

```bash
# 1. Clone the 7 benchmark repos at pinned refs (depth 1)
make setup

# 2a. Smoke-test on Gin (~110 files, fast) — 4 runs each arm
make with    REPO=gin N=4 MODEL=sonnet
make without REPO=gin N=4 MODEL=sonnet
make report

# 2b. Full suite — 7 repos × 2 arms × N runs
#     Recommended: dispatch one subagent per repo in parallel
#     via the Task tool. Each subagent runs:
#       LEANKG_BIN=/abs/path/to/leankg bash run_repo.sh <slug> 4 sonnet
#     Total wall-clock bounded by the slowest repo (typically vscode).

# 2c. Or run serially (slower, ~90 min)
make full MODEL=sonnet N=4

# 3. Just the report
make report
```

Override defaults:

```bash
make full MODEL=opus N=4 LEANKG_BIN=/abs/path/to/leankg
make with REPO=django N=8 MODEL=sonnet
LEANKG_BIN=/abs/path/to/leankg bash run_repo.sh django 4 sonnet
```

## Methodology notes

- Same prompt per repo for both arms. Prompts are taken verbatim from the
  codegraph README where applicable so numbers are comparable. See `repos.yaml`.
- Each `claude -p` invocation uses `--strict-mcp-config` so neither arm
  inherits the user's global MCP setup. The WITH-arm config registers LeanKG
  stdio MCP pointing at the local `leankg` binary; the WITHOUT-arm config is
  `{"mcpServers": {}}`.
- For every WITH-arm run the LeanKG index is rebuilt (`leankg init`) so runs
  are deterministic; `--watch` is intentionally **not** enabled.
- Built-in `Read`/`Grep`/`Bash` stay available to both arms.
- `claude -p --output-format json` returns a single JSON envelope with all
  metrics; `run_one.sh` parses it in Python for robustness against CLI
  version drift.
- Median of N=4 runs is reported per arm per repo (matches codegraph).
- Cost and token numbers depend on the Claude model; the Makefile defaults to
  `sonnet` to keep total cost low. Switch to `opus` for direct comparability
  with codegraph's 2026-07-21 re-validation.

## Caveats

- Self-reported single-vendor benchmark. Treat as best-case.
- Larger repos dominate the mean; we report medians for transparency.
- Variance on the WITHOUT arm can be high because the agent has no structured
  index to lean on; IQR in the appendix makes that visible.
- Index-only-leankg is not run during the agent invocation; we rebuild the
  index once per run for fairness. If you want to measure incremental
  indexing cost separately, see `leankg init --watch`.

## License

This benchmark harness is Apache-2.0 (matching the leankg project). The
underlying benchmark questions come from `colbymchenry/codegraph` (MIT).