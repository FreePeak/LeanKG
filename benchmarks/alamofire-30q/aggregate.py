#!/usr/bin/env python3
"""Aggregate per-question JSONL output from run_30q.sh into a multi-table report.

Inputs:
    results/runs/YYYY-MM-DD/<arm>/<question_id>/runs.jsonl

Outputs:
    results/alamofire-30q-YYYY-MM-DD.md
    results/alamofire-30q-YYYY-MM-DD.json

Reports:
  1. Per-question table (3-arm comparison per question)
  2. Per-arm summary (median across all 30 questions)
  3. Efficiency gains (% reduction in tokens, calls, time, cost)
  4. IQR appendix for variance across runs
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

HERE = Path(__file__).resolve().parent


def load_yaml(path: Path) -> dict[str, Any]:
    try:
        import yaml
        with path.open("r", encoding="utf-8") as fh:
            return yaml.safe_load(fh)
    except ImportError:
        print("ERROR: PyYAML required (pip install pyyaml)", file=sys.stderr)
        sys.exit(2)


def load_runs(results_root: Path) -> list[dict[str, Any]]:
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
        print(f"info: dropped {dropped} invalid run(s)", file=sys.stderr)

    # Warn on model mixing
    by_cell: dict[tuple[str, str], set[str]] = defaultdict(set)
    for r in rows:
        if not r.get("valid"):
            continue
        model = r.get("actual_model") or r.get("model") or "unknown"
        by_cell[(r["question_id"], r["arm"])].add(model)
    for (qid, arm), models in sorted(by_cell.items()):
        if len(models) > 1:
            print(f"warn: {qid}/{arm} mixes models: {sorted(models)}", file=sys.stderr)
    return rows


def median(values: list[float]) -> float | None:
    cleaned = [v for v in values if v is not None]
    if not cleaned:
        return None
    return statistics.median(cleaned)


def iqr(values: list[float]) -> float:
    cleaned = sorted(values)
    if len(cleaned) < 4:
        return 0.0
    q1 = statistics.median(cleaned[:len(cleaned) // 2])
    q3 = statistics.median(cleaned[(len(cleaned) + 1) // 2:])
    return round(q3 - q1, 3)


def fmt_int(value: float | None) -> str:
    if value is None: return "N/A"
    return f"{int(round(value)):,}"


def fmt_cost(value: float | None) -> str:
    if value is None: return "N/A"
    if value < 0.01: return f"${value:.3f}"
    return f"${value:.2f}"


def fmt_dur(value: float | None) -> str:
    if value is None: return "N/A"
    if value >= 60:
        m = int(value // 60)
        s = int(round(value - m * 60))
        return f"{m}m{s}s"
    return f"{int(round(value))}s"


def fmt_pct(a: float | None, b: float | None) -> str:
    if b is None or b == 0: return "N/A"
    if a is None: return "N/A"
    pct = (a - b) / b * 100.0
    sign = "" if pct < 0 else "+"
    return f"{sign}{pct:.0f}%"


def build_report(questions: list[dict[str, Any]], runs: list[dict[str, Any]], yaml_data: dict[str, Any] | None = None) -> tuple[str, dict[str, Any]]:
    valid_runs = [r for r in runs if r.get("valid")]
    invalid_runs = [r for r in runs if not r.get("valid")]

    q_ids = [q["id"] for q in questions]
    q_meta = {q["id"]: q for q in questions}

    lines: list[str] = []
    today = dt.date.today().isoformat()
    lines.append("# Alamofire 30-Question 3-Way Benchmark Report")
    lines.append("")
    lines.append(f"**Date:** {today}")
    q_repo = (yaml_data or {}).get("repo", "Unknown")
    q_lang = (yaml_data or {}).get("language", "")
    lines.append(f"**Repo:** {q_repo}{f' ({q_lang})' if q_lang else ''}")
    lines.append(f"**Method:** `claude -p` headless; 3 arms: LeanKG MCP / CodeGraph MCP / No graph (built-in Read/Grep/Bash)")
    lines.append(f"**Total valid runs:** {len(valid_runs)} | Dropped: {len(invalid_runs)}")
    lines.append("")

    # ====== Per-Arm Summary ======
    arms = ["leankg", "codegraph", "none"]
    arm_labels = {"leankg": "LeanKG", "codegraph": "CodeGraph", "none": "No Graph"}

    arm_summary: dict[str, dict[str, Any]] = {}
    for arm in arms:
        arm_runs = [r for r in valid_runs if r["arm"] == arm]
        if not arm_runs:
            arm_summary[arm] = {}
            continue
        def med(m: str) -> float | None:
            vals = [r[m] for r in arm_runs if m in r]
            return median(vals)
        s = {
            "n_runs": len(arm_runs),
            "duration_s": med("duration_s"),
            "total_cost_usd": med("total_cost_usd"),
            "input_tokens": med("input_tokens"),
            "output_tokens": med("output_tokens"),
            "total_tokens": (med("input_tokens") or 0) + (med("output_tokens") or 0),
            "tool_calls": med("tool_calls"),
            "file_reads": med("file_reads"),
            "num_turns": med("num_turns"),
        }
        arm_summary[arm] = s

    lines.append("## Per-Arm Summary (median across 30 questions)")
    lines.append("")
    lines.append("| Arm | Runs | Tool calls | Time | File reads | Input tok | Output tok | Total tok | turns | Cost |")
    lines.append("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |")
    for arm in arms:
        s = arm_summary.get(arm, {})
        if not s:
            lines.append(f"| {arm_labels[arm]} | 0 | N/A | N/A | N/A | N/A | N/A | N/A | N/A | N/A |")
        else:
            lines.append(
                f"| **{arm_labels[arm]}** | {s['n_runs']} | "
                f"{fmt_int(s['tool_calls'])} | {fmt_dur(s['duration_s'])} | "
                f"{fmt_int(s['file_reads'])} | {fmt_int(s['input_tokens'])} | "
                f"{fmt_int(s['output_tokens'])} | {fmt_int(s['total_tokens'])} | "
                f"{fmt_int(s['num_turns'])} | {fmt_cost(s['total_cost_usd'])} |"
            )

    # ====== Efficiency Gains ======
    lines.append("")
    lines.append("## Efficiency Gains vs No Graph (baseline)")
    lines.append("")
    lines.append("| Metric | LeanKG vs None | CodeGraph vs None | LeanKG vs CodeGraph |")
    lines.append("| --- | --- | --- | --- |")
    no = arm_summary.get("none", {})
    lkg = arm_summary.get("leankg", {})
    cg = arm_summary.get("codegraph", {})
    for metric, label in [
        ("total_tokens", "Total tokens"),
        ("input_tokens", "Input tokens"),
        ("duration_s", "Wall-clock time"),
        ("tool_calls", "Tool calls"),
        ("file_reads", "File reads"),
        ("total_cost_usd", "Cost"),
        ("num_turns", "Agent turns"),
    ]:
        lkg_delta = fmt_pct(lkg.get(metric), no.get(metric)) if lkg and no else "N/A"
        cg_delta = fmt_pct(cg.get(metric), no.get(metric)) if cg and no else "N/A"
        lcg_delta = fmt_pct(lkg.get(metric), cg.get(metric)) if lkg and cg else "N/A"
        lines.append(f"| {label} | {lkg_delta} | {cg_delta} | {lcg_delta} |")

    # ====== Per-Question Table ======
    lines.append("")
    lines.append("## Per-Question Results (median per arm)")
    lines.append("")
    for q in questions:
        qid = q["id"]
        q_cat = q.get("category", "")
        q_prompt = q["prompt"][:100]
        lines.append(f"### {qid} ({q_cat})")
        lines.append("")
        lines.append(f"_{q_prompt}..._")
        lines.append("")
        lines.append("| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |")
        lines.append("| --- | --- | --- | --- | --- | --- | --- | --- |")
        for arm in arms:
            arm_q_runs = [r for r in valid_runs if r["arm"] == arm and r["question_id"] == qid]
            if not arm_q_runs:
                lines.append(f"| {arm_labels[arm]} | 0 | N/A | N/A | N/A | N/A | N/A | N/A |")
                continue
            def med(m): return median([r[m] for r in arm_q_runs])
            lines.append(
                f"| {arm_labels[arm]} | {len(arm_q_runs)} | "
                f"{fmt_dur(med('duration_s'))} | {fmt_int(med('input_tokens'))} / {fmt_int(med('output_tokens'))} | "
                f"{fmt_cost(med('total_cost_usd'))} | {fmt_int(med('tool_calls'))} | "
                f"{fmt_int(med('file_reads'))} | {fmt_int(med('num_turns'))} |"
            )
        lines.append("")

    # ====== IQR Appendix ======
    lines.append("## Variance Appendix (IQR across runs per arm)")
    lines.append("")
    lines.append("| Question | Arm | Cost IQR | Latency IQR | Token IQR |")
    lines.append("| --- | --- | --- | --- | --- |")
    for q in questions:
        for arm in arms:
            arm_q_runs = [r for r in valid_runs if r["arm"] == arm and r["question_id"] == q["id"]]
            if len(arm_q_runs) < 2:
                continue
            ci = iqr([r["total_cost_usd"] for r in arm_q_runs])
            ti = iqr([r["duration_s"] for r in arm_q_runs])
            toki = iqr([r["input_tokens"] + r["output_tokens"] for r in arm_q_runs])
            lines.append(f"| {q['id']} | {arm_labels[arm]} | {ci:.3f} | {ti:.2f} | {toki:.0f} |")

    # ====== Dropped Runs ======
    if invalid_runs:
        lines.append("")
        lines.append("## Dropped Runs")
        lines.append("")
        lines.append(f"{len(invalid_runs)} run(s) excluded.")
        lines.append("")
        lines.append("| Q | Arm | Run | Model | Reason |")
        lines.append("| --- | --- | --- | --- | --- |")
        for r in invalid_runs:
            lines.append(
                f"| {r.get('question_id','?')} | {r.get('arm','?')} | {r.get('run_idx','?')} | "
                f"{r.get('actual_model') or r.get('model') or '?'} | "
                f"{r.get('dropped_reason','invalid')} |"
            )

    lines.append("")
    lines.append("## Methodology")
    lines.append("")
    q_count = len(questions)
    lines.append(f"- {q_count} architecture questions covering {q_repo}{f' ({q_lang})' if q_lang else ''}.")
    lines.append("- Each arm = `claude -p` headless with `--strict-mcp-config`, `--output-format json`, `--dangerously-skip-permissions`.")
    lines.append("- LeanKG index rebuilt before its arm; CodeGraph index pre-built.")
    lines.append("- N=3 runs per arm per question; median reported.")
    lines.append("- Metrics parsed from claude CLI JSON envelope (v2.1.201+).")
    lines.append("")
    lines.append("## Caveats")
    lines.append("")
    lines.append("- Self-reported single-vendor benchmark. Treat as best-case.")
    lines.append("- LeanKG Swift extraction is regex-based (no tree-sitter); under-reports call graph edges.")
    lines.append("- Cost/token numbers depend on model version; pin with `--model` for reproducibility.")
    lines.append("- Small sample (N=3); high variance expected. IQR appendix shows spread.")

    md = "\n".join(lines) + "\n"

    # JSON payload
    json_payload = {
        "date": today,
        "repo": "alamofire",
        "language": "Swift",
        "n_questions": len(questions),
        "n_runs_valid": len(valid_runs),
        "n_runs_dropped": len(invalid_runs),
        "arm_summary": arm_summary,
        "raw_runs": valid_runs,
    }
    return md, json_payload


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--results", type=Path, default=HERE / "results",
                        help="Path to results directory (default: ./results)")
    parser.add_argument("--questions", type=Path, default=HERE / "questions.yaml",
                        help="Path to questions.yaml")
    parser.add_argument("--date", type=str, default=None,
                        help="Override date stamp in filename (default: today)")
    parser.add_argument("--name", type=str, default=None,
                        help="Override base filename (default: alamofire-30q-YYYY-MM-DD)")
    args = parser.parse_args()

    questions_data = load_yaml(args.questions)
    questions = questions_data.get("questions", [])
    if not questions:
        print("ERROR: no questions found in questions.yaml", file=sys.stderr)
        return 2

    runs = load_runs(args.results)
    if not runs:
        print(f"warn: no runs found under {args.results}", file=sys.stderr)

    md, payload = build_report(questions, runs, yaml_data=questions_data)

    date_stamp = args.date or dt.date.today().isoformat()
    base_name = args.name or f"alamofire-30q-{date_stamp}"
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
