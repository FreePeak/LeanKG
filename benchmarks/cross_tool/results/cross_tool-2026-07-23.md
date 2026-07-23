# Cross-Tool Agent A/B Benchmark Report

**Date:** 2026-07-23  
**Method:** `claude -p` headless; WITH = LeanKG MCP stdio; WITHOUT = empty MCP config. Built-in Read/Grep/Bash available to both.  
**Runs per arm per repo:** median reported (matches codegraph methodology).  
**Total runs in this report:** 8.

## Per-Repo Results

| Codebase | Language | Tool calls (WITH / WITHOUT) | Time (WITH / WITHOUT) | File reads (WITH / WITHOUT) | Tokens (WITH / WITHOUT) | Cost (WITH / WITHOUT) |
| --- | --- | --- | --- | --- | --- | --- |
| **vscode** | TypeScript | N/A / N/A | N/A / N/A | N/A / N/A | 0 / 0 | N/A / N/A |
| **excalidraw** | TypeScript | N/A / N/A | N/A / N/A | N/A / N/A | 0 / 0 | N/A / N/A |
| **django** | Python | N/A / N/A | N/A / N/A | N/A / N/A | 0 / 0 | N/A / N/A |
| **tokio** | Rust | N/A / N/A | N/A / N/A | N/A / N/A | 0 / 0 | N/A / N/A |
| **okhttp** | Java | N/A / N/A | N/A / N/A | N/A / N/A | 0 / 0 | N/A / N/A |
| **gin** | Go | 7 / 9 | 1m 7s / 1m 59s | 4 / 4 | 39,585 / 37,237 | $0.37 / $0.53 |
| **alamofire** | Swift | N/A / N/A | N/A / N/A | N/A / N/A | 0 / 0 | N/A / N/A |

## Average Savings (median across repos)

| Metric | Avg % change (WITH vs WITHOUT) |
| --- | --- |
| Tool calls | -22% |
| Wall-clock time | -44% |
| File reads | +0% |
| Total tokens | +6% |
| Cost | -31% |

## Variance Appendix (IQR across runs)

Per-arm IQR across the N runs per repo. High IQR on the WITHOUT
arm is expected; the WITH arm should be tighter.

| Codebase | Tool calls IQR (WITH / WITHOUT) | Cost IQR (WITH / WITHOUT) | Time IQR (WITH / WITHOUT) |
| --- | --- | --- | --- |
| gin | 6.0 / 2.0 | 0.07 / 0.34 | 26.815 / 91.59 |

## Methodology

- Same harness as `colbymchenry/codegraph` 7-repo suite (re-validated 2026-07-21, Opus 4.8).
- Each arm = `claude -p <prompt>` headless, same question per repo, median of N runs.
- `--strict-mcp-config` ensures no fallback MCP servers pollute either arm.
- Repos cloned with `git clone --depth 1` and pinned to the tag in `repos.yaml`.
- LeanKG index is rebuilt (`leankg init`) before every WITH-arm run to keep runs deterministic.

## Caveats

- Self-reported single-vendor benchmarks; treat as best-case.
- Cost and token numbers depend on the Claude model version; pin via `--model`.
- Larger repos like VS Code dominate the average; report median-of-medians when sample sizes grow.
