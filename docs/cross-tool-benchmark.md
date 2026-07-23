# Cross-Tool Agent A/B Benchmark (US-CT-BMK)

A codegraph/graphify-style headless-agent benchmark for LeanKG's MCP
integration. Compares a fixed `claude -p` agent answering one architecture
question on real-world codebases — **WITH** LeanKG MCP enabled vs **WITHOUT**
(empty MCP config, built-in Read/Grep/Bash available to both).

The methodology mirrors `colbymchenry/codegraph`'s published 7-repo suite
(re-validated 2026-07-21, Opus 4.8), so our numbers are directly comparable
to theirs. We are not copying codegraph — we are running the same harness
against our own product to prove LeanKG saves the agent time, money, and
tool calls.

## Latest results

Latest run: [results/cross_tool-2026-07-23.md](../benchmarks/cross_tool/results/cross_tool-2026-07-23.md)

```text
| Codebase | Language | Tool calls | Time | File reads | Tokens | Cost |
```

(See the generated Markdown report for the full per-repo table + IQR appendix.)

## What we measure

| Metric | Source |
|---|---|
| Tool calls | walk of the `claude -p --output-format json` event array, counting `tool_use` blocks |
| Wall-clock time | `time` around the `claude -p` call |
| File reads | subset of tool_use blocks where `name == "Read"` |
| Input / output / cache-read tokens | `usage.input_tokens`, `usage.output_tokens`, `usage.cache_read_input_tokens` |
| Cost | `total_cost_usd` from the `{"type":"result"}` envelope element |
| Exit reason | `stop_reason`, `exit_code` |

Per arm per repo we report the **median** of N runs (codegraph uses N=4). IQR
is shown in the appendix so reviewers can see variance — the WITH arm should
be tighter than the WITHOUT arm.

## Harness

```
benchmarks/cross_tool/
├── README.md
├── Makefile                  # setup / index / with / without / report / full / clean
├── repos.yaml                # 7 repos + pinned refs + one architecture question each
├── clone_repos.py            # shallow-clones each repo at its pinned ref (skip-if-exists)
├── install_leankg_mcp.sh     # emit strict --strict-mcp-config JSON for an arm
├── run_one.sh                # run one (repo, arm, run_idx) and append JSONL
├── run_arm.sh                # run one (repo, arm) for N iterations
├── get_prompt.py             # print the prompt for a given repo slug
├── aggregate.py              # JSONL -> Markdown + JSON report
├── .gitignore                # ignores cloned repos/ + scratch/
└── results/
    ├── runs/YYYY-MM-DD/<repo>/<arm>/runs.jsonl
    └── cross_tool-YYYY-MM-DD.{md,json}
```

## Running

```bash
cd benchmarks/cross_tool

# 1. Clone the 7 benchmark repos at pinned refs (depth 1)
make setup

# 2. Run one repo end-to-end (use the default claude model)
make all REPO=gin N=4

# 3. Run the full 7-repo suite
make full N=4

# 4. Just the report from existing JSONL
make report
```

Override defaults:

```bash
make full LEANKG_BIN=/abs/path/to/leankg N=4 MODEL=sonnet
make with REPO=django N=8
```

## Per-repo question set

Prompts are taken verbatim from `colbymchenry/codegraph` so numbers are
directly comparable:

| Codebase | Language | Question |
|---|---|---|
| VS Code | TypeScript | "How does the extension host communicate with the main process?" |
| Excalidraw | TypeScript | "How does Excalidraw render and update canvas elements?" |
| Django | Python | "How does Django's ORM build and execute a query from a QuerySet?" |
| Tokio | Rust | "How does tokio schedule and run async tasks on its runtime?" |
| OkHttp | Java | "How does OkHttp process a request through its interceptor chain?" |
| Gin | Go | "How does gin route requests through its middleware chain?" |
| Alamofire | Swift | "How does Alamofire build, send, and validate a request?" |

## Methodology

- Each arm = `claude -p <prompt>` headless, same question per repo, median of N runs.
- `--strict-mcp-config` ensures neither arm inherits the user's global MCP setup.
  - **WITH** arm config: `{"mcpServers": {"leankg": {"type":"stdio", "command":"...", "args":["mcp-stdio","--watch"]}}}`
  - **WITHOUT** arm config: `{"mcpServers": {}}`
- For every WITH-arm run the LeanKG index is rebuilt (`leankg init` + `leankg index .`)
  so runs are deterministic. `--watch` is intentionally **not** enabled.
- Built-in `Read` / `Grep` / `Bash` stay available to both arms.
- Repos are cloned with `git clone --depth 1` and pinned to a specific tag or
  branch (see `repos.yaml`). Updates to upstream can be re-pinned by editing
  `repos.yaml` and re-running `make clean && make setup`.
- Cost and token numbers depend on the Claude model. The harness defaults to
  `claude -p`'s configured model (whatever `claude` resolves when no `--model`
  is passed); pass `MODEL=sonnet` (or any other model id) to override.
- We **re-validate the parser** against `claude` CLI 2.1.201 (verified
  2026-07-23). Older shapes are also handled defensively.

## Caveats

- Self-reported single-vendor benchmark. Treat as best-case.
- Larger repos dominate the mean; we report medians for transparency.
- Variance on the WITHOUT arm can be high (the agent has no structured index
  to lean on); IQR in the appendix makes that visible.
- The default `claude` model on the developer's machine determines cost and
  speed. Pin explicitly with `MODEL=...` if you need exact reproducibility.
- LeanKG's index is rebuilt before every WITH-arm run; this captures
  indexing-once cost as well as agent cost. If you want incremental-only,
  remove the `rm -rf .leankg && leankg init && leankg index .` block in
  `run_arm.sh`.

## Why this matters

The grep-vs-LeanKG A/B harness at `benchmark/` already proves the **index**
saves tokens (avg 89% on a self-indexed leankg, see
`benchmark/results/ab-benchmark-2026-07-02.md`). This benchmark complements
it by proving the **MCP integration** saves the agent real money on real
codebases with a fixed prompt and a fixed model — the canonical "codegraph
proves it works" claim.

If both A/B harnesses show consistent wins, the story is:
- The index is small and fast to query (grep A/B).
- The agent uses the index effectively on real workloads (this harness).

If the grep A/B wins but this one doesn't, the story is: the index is good
but the agent doesn't reach for it. That tells us where to invest in better
MCP tool descriptions or instructions.

## References

- `colbymchenry/codegraph` 7-repo suite, re-validated 2026-07-21 (Opus 4.8):
  the methodology we replicate.
- `Graphify-Labs/graphify` BENCHMARKS.md: complementary code-intelligence
  harness using a fixed agent on ERPNext with judge-graded key-fact coverage.
- `benchmark/README.md` — the grep A/B harness that this benchmark
  complements.

## License

Apache-2.0 (matching the leankg project). Prompts come from
`colbymchenry/codegraph` (MIT).