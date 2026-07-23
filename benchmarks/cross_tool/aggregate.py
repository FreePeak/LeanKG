#!/usr/bin/env python3
"""Aggregate per-run JSONL output from `run_one.sh` into a codegraph-style report.

Inputs (under `benchmarks/cross_tool/results/`):
    runs-YYYY-MM-DD/<repo>/<arm>/run_<idx>.jsonl   (each line = one run)
    repos.yaml                                       (canonical repo + prompt list)

Outputs:
    results/cross_tool-YYYY-MM-DD.md
    results/cross_tool-YYYY-MM-DD.json

The Markdown table mirrors `colbymchenry/codegraph`'s published layout:

| Codebase | Language | Tool calls | Time | File reads | Tokens | Cost |
| -------- | -------- | ---------- | ---- | ---------- | ------ | ---- |
| VS Code  | TS       | 2 vs 40    | ...  | ...        | ...    | ...  |
| **Avg**  |          | ...        | ...  | ...        | ...    | ...  |

Per-arm metric reported per repo is the **median** across runs (matches
codegraph's published methodology: 4 runs per arm, median reported). We also
print IQR as an appendix row so reviewers can see variance.
"""
from __future__ import annotations

import argparse
import datetime as dt
import json
import statistics
import sys
from collections import defaultdict
from pathlib import Path
from typing import Any

try:
    import yaml  # PyYAML; falls back to a minimal parser below
except ImportError:
    yaml = None

HERE = Path(__file__).resolve().parent


def load_yaml_fallback(path: Path) -> dict[str, Any]:
    """Parse a *tiny* subset of YAML sufficient for repos.yaml without PyYAML.

    Supports the schema used by repos.yaml: top-level mapping, list items that
    are simple `key: value` mappings. Indentation is two spaces. If PyYAML is
    installed, use it instead.
    """
    if yaml is not None:
        with path.open("r", encoding="utf-8") as fh:
            return yaml.safe_load(fh)

    text = path.read_text(encoding="utf-8")
    root: dict[str, Any] = {}
    current_list_key: str | None = None
    current_item: dict[str, Any] | None = None
    for raw in text.splitlines():
        if not raw.strip() or raw.lstrip().startswith("#"):
            continue
        indent = len(raw) - len(raw.lstrip())
        stripped = raw.strip()
        if indent == 0 and ":" in stripped:
            key, _, value = stripped.partition(":")
            value = value.strip()
            if value == "":
                root[key] = []
                current_list_key = key
                current_item = None
            else:
                root[key] = value.strip('"').strip("'")
                current_list_key = None
                current_item = None
        elif indent == 2 and current_list_key is not None and stripped.startswith("- "):
            if current_item is not None:
                root[current_list_key].append(current_item)
            current_item = {}
            kv = stripped[2:]
            if ":" in kv:
                k, _, v = kv.partition(":")
                current_item[k.strip()] = v.strip().strip('"').strip("'")
        elif indent == 4 and current_item is not None and ":" in stripped:
            k, _, v = stripped.partition(":")
            current_item[k.strip()] = v.strip().strip('"').strip("'")
    if current_item is not None and current_list_key is not None:
        root[current_list_key].append(current_item)
    return root


def load_runs(results_root: Path) -> list[dict[str, Any]]:
    """Load per-run JSONL rows, dropping invalid runs and warning on model mixing.

    A run is dropped if any of the following holds:
      * `exit_code != 0` (process died / was killed)
      * `total_cost_usd == 0` (no API call actually completed)
      * `valid == false` with a non-empty `invalid_reason` (e.g. WITH-arm run
        that did not actually attach the MCP server)

    All rows are still returned so the caller can decide what to do; invalid
    rows are returned with `valid=False` and a `dropped_reason` populated.
    """
    rows: list[dict[str, Any]] = []
    dropped = 0
    for path in sorted(results_root.rglob("*.jsonl")):
        for lineno, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
            line = raw.strip()
            if not line:
                continue
            try:
                row = json.loads(line)
            except json.JSONDecodeError as exc:
                print(f"warn: malformed JSONL in {path}:{lineno}: {exc}", file=sys.stderr)
                continue
            row["_source_path"] = str(path)
            reasons = []
            if row.get("exit_code", 0) != 0:
                reasons.append(f"exit_code={row.get('exit_code')}")
            if float(row.get("total_cost_usd", 0) or 0) <= 0:
                reasons.append("zero_cost")
            existing = row.get("invalid_reason")
            if existing and str(existing).strip():
                reasons.append(str(existing))
            if reasons:
                row["valid"] = False
                row["dropped_reason"] = "|".join(reasons)
                dropped += 1
            else:
                row.setdefault("valid", True)
            rows.append(row)
    if dropped:
        print(
            f"info: dropped {dropped} invalid run(s); see report footer.",
            file=sys.stderr,
        )

    # Per (repo, arm), all valid runs should report the same actual_model.
    # Different actual_models inside one cell = apples vs oranges.
    by_cell: dict[tuple[str, str], set[str]] = defaultdict(set)
    for r in rows:
        if not r.get("valid"):
            continue
        model = r.get("actual_model") or r.get("model") or "unknown"
        by_cell[(r["repo"], r["arm"])].add(model)
    for (repo, arm), models in sorted(by_cell.items()):
        if len(models) > 1:
            print(
                f"warn: {repo}/{arm} mixes models: {sorted(models)}",
                file=sys.stderr,
            )
    return rows


def median_or_none(values: list[float]) -> float | None:
    cleaned = [v for v in values if v is not None]
    if not cleaned:
        return None
    return statistics.median(cleaned)


def iqr(values: list[float]) -> float:
    cleaned = sorted(values)
    if len(cleaned) < 4:
        return 0.0
    q1 = statistics.median(cleaned[: len(cleaned) // 2])
    q3 = statistics.median(cleaned[(len(cleaned) + 1) // 2 :])
    return round(q3 - q1, 3)


def fmt_int(value: float | None) -> str:
    if value is None:
        return "N/A"
    return f"{int(round(value)):,}"


def fmt_cost(value: float | None) -> str:
    if value is None:
        return "N/A"
    if value < 0.01:
        return f"${value:.3f}"
    return f"${value:.2f}"


def fmt_dur(value: float | None) -> str:
    if value is None:
        return "N/A"
    if value >= 60:
        m = int(value // 60)
        s = int(round(value - m * 60))
        return f"{m}m {s}s"
    return f"{int(round(value))}s"


def fmt_pct(delta_with: float, delta_without: float) -> str:
    if delta_without == 0:
        return "N/A"
    pct = (delta_with - delta_without) / delta_without * 100.0
    sign = "" if pct < 0 else "+"
    return f"{sign}{pct:.0f}%"


def build_report(
    repos_meta: list[dict[str, Any]],
    runs: list[dict[str, Any]],
) -> tuple[str, dict[str, Any]]:
    by_repo: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for r in runs:
        by_repo[r["repo"]].append(r)

    # Only valid runs count toward medians / IQR / averages.
    valid_runs = [r for r in runs if r.get("valid")]
    invalid_runs = [r for r in runs if not r.get("valid")]

    meta_by_repo = {m["slug"]: m for m in repos_meta}

    rows: list[dict[str, Any]] = []
    avg_acc: dict[str, list[float]] = defaultdict(list)

    for meta in repos_meta:
        slug = meta["slug"]
        repo_name = meta.get("repo_name", slug)
        # Allow either key: repos.yaml uses 'slug' but the actual cloned dir
        # name may differ (e.g. vscode vs VSCode). Fall back to slug.
        repo_runs_all = by_repo.get(slug) or by_repo.get(repo_name) or by_repo.get(meta.get("clone_dir", slug)) or []
        repo_runs = [r for r in repo_runs_all if r.get("valid")]
        repo_dropped = [r for r in repo_runs_all if not r.get("valid")]

        with_runs = [r for r in repo_runs if r["arm"] == "with"]
        without_runs = [r for r in repo_runs if r["arm"] == "without"]

        def med(metric: str, arm_runs: list[dict[str, Any]]) -> float | None:
            return median_or_none([r[metric] for r in arm_runs])

        row = {
            "codebase": slug,
            "language": meta.get("language", ""),
            "with": {
                "tool_calls": med("tool_calls", with_runs),
                "duration_s": med("duration_s", with_runs),
                "file_reads": med("file_reads", with_runs),
                "total_tokens": med("input_tokens", with_runs) is None
                and 0
                or (
                    med("input_tokens", with_runs)
                    or 0
                )
                + (med("output_tokens", with_runs) or 0),
                "input_tokens": med("input_tokens", with_runs),
                "output_tokens": med("output_tokens", with_runs),
                "cache_read_tokens": med("cache_read_tokens", with_runs),
                "total_cost_usd": med("total_cost_usd", with_runs),
                "n": len(with_runs),
            },
            "without": {
                "tool_calls": med("tool_calls", without_runs),
                "duration_s": med("duration_s", without_runs),
                "file_reads": med("file_reads", without_runs),
                "total_tokens": (med("input_tokens", without_runs) or 0)
                + (med("output_tokens", without_runs) or 0),
                "input_tokens": med("input_tokens", without_runs),
                "output_tokens": med("output_tokens", without_runs),
                "cache_read_tokens": med("cache_read_tokens", without_runs),
                "total_cost_usd": med("total_cost_usd", without_runs),
                "n": len(without_runs),
            },
        }
        # Track averages where both arms have data
        for metric in ("tool_calls", "duration_s", "file_reads", "total_tokens", "total_cost_usd"):
            w = row["with"][metric]
            wo = row["without"][metric]
            if w is not None and wo is not None and wo != 0:
                avg_acc[metric].append((w - wo) / wo * 100.0)
        rows.append(row)

    # Markdown
    lines: list[str] = []
    today = dt.date.today().isoformat()
    lines.append(f"# Cross-Tool Agent A/B Benchmark Report")
    lines.append("")
    lines.append(f"**Date:** {today}  ")
    lines.append(f"**Method:** `claude -p` headless; WITH = LeanKG MCP stdio; WITHOUT = empty MCP config. Built-in Read/Grep/Bash available to both.  ")
    lines.append(f"**Runs per arm per repo:** median reported (matches codegraph methodology).  ")
    lines.append(
        f"**Total runs loaded:** {len(runs)} (valid: {len(valid_runs)}, "
        f"dropped: {len(invalid_runs)}).  "
    )
    lines.append("")
    lines.append("## Per-Repo Results")
    lines.append("")
    lines.append(
        "| Codebase | Language | N (WITH / WITHOUT) | "
        "Tool calls (WITH / WITHOUT) | Time (WITH / WITHOUT) | "
        "File reads (WITH / WITHOUT) | Tokens (WITH / WITHOUT) | "
        "Cost (WITH / WITHOUT) |"
    )
    lines.append("| --- | --- | --- | --- | --- | --- | --- | --- |")
    for row in rows:
        w, wo = row["with"], row["without"]
        lines.append(
            f"| **{row['codebase']}** | {row['language']} | "
            f"{w['n']} / {wo['n']} | "
            f"{fmt_int(w['tool_calls'])} / {fmt_int(wo['tool_calls'])} | "
            f"{fmt_dur(w['duration_s'])} / {fmt_dur(wo['duration_s'])} | "
            f"{fmt_int(w['file_reads'])} / {fmt_int(wo['file_reads'])} | "
            f"{fmt_int(w['total_tokens'])} / {fmt_int(wo['total_tokens'])} | "
            f"{fmt_cost(w['total_cost_usd'])} / {fmt_cost(wo['total_cost_usd'])} |"
        )

    # Averages
    lines.append("")
    lines.append("## Average Savings (median across repos)")
    lines.append("")
    lines.append("| Metric | Avg % change (WITH vs WITHOUT) |")
    lines.append("| --- | --- |")
    for metric, label in [
        ("tool_calls", "Tool calls"),
        ("duration_s", "Wall-clock time"),
        ("file_reads", "File reads"),
        ("total_tokens", "Total tokens"),
        ("total_cost_usd", "Cost"),
    ]:
        if avg_acc[metric]:
            avg = sum(avg_acc[metric]) / len(avg_acc[metric])
            sign = "" if avg < 0 else "+"
            lines.append(f"| {label} | {sign}{avg:.0f}% |")
        else:
            lines.append(f"| {label} | N/A |")

    # IQR appendix for transparency on the median-vs-mean story
    lines.append("")
    lines.append("## Variance Appendix (IQR across runs)")
    lines.append("")
    lines.append("Per-arm IQR across the N runs per repo. High IQR on the WITHOUT")
    lines.append("arm is expected; the WITH arm should be tighter.")
    lines.append("")
    lines.append("| Codebase | Tool calls IQR (WITH / WITHOUT) | Cost IQR (WITH / WITHOUT) | Time IQR (WITH / WITHOUT) |")
    lines.append("| --- | --- | --- | --- |")
    for meta in repos_meta:
        slug = meta["slug"]
        repo_runs_all = by_repo.get(slug, [])
        repo_runs = [r for r in repo_runs_all if r.get("valid")]
        with_runs = [r for r in repo_runs if r["arm"] == "with"]
        without_runs = [r for r in repo_runs if r["arm"] == "without"]
        if not with_runs and not without_runs:
            continue
        lines.append(
            f"| {slug} | "
            f"{iqr([r['tool_calls'] for r in with_runs])} / {iqr([r['tool_calls'] for r in without_runs])} | "
            f"{iqr([r['total_cost_usd'] for r in with_runs]):.2f} / {iqr([r['total_cost_usd'] for r in without_runs]):.2f} | "
            f"{iqr([r['duration_s'] for r in with_runs])} / {iqr([r['duration_s'] for r in without_runs])} |"
        )

    # Dropped-run footer — surface every rejected run so silent harness bugs
    # cannot quietly disappear.
    if invalid_runs:
        lines.append("")
        lines.append("## Dropped Runs")
        lines.append("")
        lines.append(
            f"{len(invalid_runs)} run(s) excluded from medians/averages above."
        )
        lines.append("")
        lines.append("| Repo | Arm | Run | Model | Reason |")
        lines.append("| --- | --- | --- | --- | --- |")
        for r in invalid_runs:
            lines.append(
                f"| {r['repo']} | {r['arm']} | {r['run_idx']} | "
                f"{r.get('actual_model') or r.get('model') or '?'} | "
                f"{r.get('dropped_reason') or 'invalid'} |"
            )

    lines.append("")
    lines.append("## Methodology")
    lines.append("")
    lines.append("- Same harness as `colbymchenry/codegraph` 7-repo suite (re-validated 2026-07-21, Opus 4.8).")
    lines.append("- Each arm = `claude -p <prompt>` headless, same question per repo, median of N runs.")
    lines.append("- `--mcp-config <file> --strict-mcp-config` loads only the file's MCP servers; nothing else.")
    lines.append("- `--bare` disables CLAUDE.md auto-discovery, hooks, LSP, plugins, and attribution so the run is hermetic.")
    lines.append("- Repos cloned with `git clone --depth 1` and pinned to the tag in `repos.yaml`.")
    lines.append("- LeanKG index is rebuilt (`leankg init && leankg index`) before every WITH-arm run to keep runs deterministic.")
    lines.append("- Each run records `actual_model`, `mcp_servers`, and `mcp_tool_count` from the session init event so model routing / MCP attachment can be audited after the fact.")
    lines.append("")
    lines.append("## Caveats")
    lines.append("")
    lines.append("- Self-reported single-vendor benchmarks; treat as best-case.")
    lines.append("- Cost and token numbers depend on the Claude model version; pin via `--model`. The harness records the actual model used in each run for auditing.")
    lines.append("- Larger repos like VS Code dominate the average; report median-of-medians when sample sizes grow.")

    md = "\n".join(lines) + "\n"

    json_payload = {
        "date": today,
        "n_runs_total": len(runs),
        "n_runs_valid": len(valid_runs),
        "n_runs_dropped": len(invalid_runs),
        "rows": rows,
        "averages_pct": {k: (sum(v) / len(v) if v else None) for k, v in avg_acc.items()},
        "dropped_runs": [
            {k: v for k, v in r.items() if k != "_source_path"}
            for r in invalid_runs
        ],
        "raw_runs": runs,
    }
    return md, json_payload


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--results",
        type=Path,
        default=HERE / "results",
        help="Path to the results directory (defaults to ./results)",
    )
    parser.add_argument(
        "--repos",
        type=Path,
        default=HERE / "repos.yaml",
        help="Path to repos.yaml",
    )
    parser.add_argument(
        "--date",
        type=str,
        default=None,
        help="Override the date stamp in the output filename (default: today)",
    )
    parser.add_argument(
        "--name",
        type=str,
        default=None,
        help="Override the base filename for outputs (default: cross_tool-YYYY-MM-DD)",
    )
    args = parser.parse_args()

    repos_data = load_yaml_fallback(args.repos)
    repos_meta = repos_data.get("repos", [])

    runs = load_runs(args.results)
    if not runs:
        print(f"warn: no runs found under {args.results}", file=sys.stderr)

    md, payload = build_report(repos_meta, runs)

    date_stamp = args.date or dt.date.today().isoformat()
    base_name = args.name or f"cross_tool-{date_stamp}"
    md_path = args.results / f"{base_name}.md"
    json_path = args.results / f"{base_name}.json"

    args.results.mkdir(parents=True, exist_ok=True)
    md_path.write_text(md, encoding="utf-8")
    json_path.write_text(json.dumps(payload, indent=2, ensure_ascii=False), encoding="utf-8")
    print(f"wrote {md_path}")
    print(f"wrote {json_path}")
    return 0


if __name__ == "__main__":
    sys.exit(main())