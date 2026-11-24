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
from typing import Any, Callable, Iterable

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
        # score.py output lives under results/scores/ — different schema,
        # must never enter the run stream.
        if "scores" in path.parts:
            continue
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


def fmt_bytes(value: float | None) -> str:
    if value is None:
        return "N/A"
    return f"{int(round(value)):,} B"


def fmt_reduction(arm_bytes: float | None, baseline_bytes: float | None) -> str:
    """Reduction in tool-result bytes returned to the model, relative to the baseline arm.

    Not directly comparable to context-mode's bytes-in-context figure: raw tool-output
    bytes before any compaction are not recorded for the context-mode arm."""
    if arm_bytes is None or not baseline_bytes:
        return "N/A"
    return f"{(1 - arm_bytes / baseline_bytes) * 100:.0f}%"


def fmt_score(value: float | None) -> str:
    return f"{value:.1f}" if value is not None else "N/A"


def fmt_delta(value: float | None, reference: float | None) -> str:
    if value is None or reference is None:
        return "N/A"
    return f"{value - reference:+.1f}"


def table_rule(columns: int) -> str:
    return "| " + " | ".join(["---"] * columns) + " |"


ARM_ORDER = ("baseline", "context-mode", "leankg")
# The legacy pair from the first harness: `without` plays the baseline role.
BASELINE_ARMS = ("baseline", "without")
# Arms whose runs must not attach an MCP server.
NO_MCP_ARMS = BASELINE_ARMS
# Per-run metrics compared in the losing-rows section. Lower is better.
COST_METRICS = (
    ("total_tokens", "tokens", fmt_int),
    ("tool_calls", "tool calls", fmt_int),
    ("duration_s", "wall time", fmt_dur),
    ("tool_result_bytes", "tool_result bytes", fmt_bytes),
)
# Averages across repos, percent change vs baseline.
AVG_METRICS = (
    ("tool_calls", "Tool calls"),
    ("duration_s", "Wall-clock time"),
    ("file_reads", "File reads"),
    ("total_tokens", "Total tokens"),
    ("total_cost_usd", "Cost"),
)


def discover_arms(arms: Iterable[str]) -> list[str]:
    """Arms seen in the input: ARM_ORDER first, then any other arm by name."""
    rank = {arm: i for i, arm in enumerate(ARM_ORDER)}
    return sorted(set(arms), key=lambda a: (rank.get(a, len(rank)), a))


def baseline_of(arms: list[str]) -> str | None:
    return next((a for a in BASELINE_ARMS if a in arms), None)


def arm_stats(arm_runs: list[dict[str, Any]]) -> dict[str, Any]:
    def med(metric: str) -> float | None:
        return median_or_none([r.get(metric) for r in arm_runs])

    input_tokens = med("input_tokens")
    output_tokens = med("output_tokens")
    return {
        "n": len(arm_runs),
        "tool_calls": med("tool_calls"),
        "duration_s": med("duration_s"),
        "file_reads": med("file_reads"),
        "total_tokens": (input_tokens or 0) + (output_tokens or 0),
        "input_tokens": input_tokens,
        "output_tokens": output_tokens,
        "cache_read_tokens": med("cache_read_tokens"),
        "tool_result_bytes": med("tool_result_bytes"),
        "total_cost_usd": med("total_cost_usd"),
    }


def iqr_cell(by_arm: dict[str, list[dict[str, Any]]], arms: list[str], key: str,
             fmt: Callable[[float], str] = str) -> str:
    return " / ".join(fmt(iqr([r[key] for r in by_arm[a]])) for a in arms)


def losing_rows(
    rows: list[dict[str, Any]],
    quality: list[tuple[str, str, dict[str, float | None]]],
) -> list[tuple[str, str, str, str, str]]:
    """Every repo/metric where leankg is worse than baseline or context-mode:
    higher cost metric, or lower judge-blind quality."""
    refs = ("baseline", "context-mode")
    out: list[tuple[str, str, str, str, str]] = []
    for row in rows:
        stats = row["arms"]
        if "leankg" not in stats:
            continue
        for ref in refs:
            if ref not in stats:
                continue
            for key, label, fmt in COST_METRICS:
                lk, rv = stats["leankg"][key], stats[ref][key]
                if lk is not None and rv is not None and lk > rv:
                    out.append((row["codebase"], label, fmt(lk), ref, fmt(rv)))
    for slug, judge, med in quality:
        lk = med.get("leankg")
        for ref in refs:
            rv = med.get(ref)
            if lk is not None and rv is not None and lk < rv:
                out.append((slug, f"quality ({judge})", fmt_score(lk), ref, fmt_score(rv)))
    return out


def build_report(
    repos_meta: list[dict[str, Any]],
    runs: list[dict[str, Any]],
    results_root: Path | None = None,
) -> tuple[str, dict[str, Any]]:
    by_repo: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for r in runs:
        by_repo[r["repo"]].append(r)

    # Only valid runs count toward medians / IQR / averages.
    valid_runs = [r for r in runs if r.get("valid")]
    invalid_runs = [r for r in runs if not r.get("valid")]

    # FR-ZCP-08: judge-blind quality scores (from score.py), if present.
    score_rows: list[dict[str, Any]] = []
    if results_root is not None:
        sp = results_root / "scores" / "scores.jsonl"
        if sp.exists():
            for line in sp.read_text(encoding="utf-8").splitlines():
                try:
                    score_rows.append(json.loads(line))
                except json.JSONDecodeError:
                    continue
    # Key medians by (repo, judge_model): re-scoring the same runs with a
    # different judge must surface as separate rows, never blend.
    scores_by_repo: dict[tuple[str, str], dict[str, list[float]]] = defaultdict(
        lambda: defaultdict(list)
    )
    for sr in score_rows:
        scores_by_repo[(sr["repo"], str(sr.get("judge_model") or "default"))][
            sr["arm"]
        ].append(float(sr["score"]))

    arms = discover_arms({r["arm"] for r in valid_runs} | {sr["arm"] for sr in score_rows})
    base = baseline_of(arms)
    non_base = [a for a in arms if base is not None and a != base]
    quality = [
        (slug, judge, {a: (statistics.median(v[a]) if v.get(a) else None) for a in arms})
        for (slug, judge), v in sorted(scores_by_repo.items())
    ]

    meta_by_repo = {m["slug"]: m for m in repos_meta}
    rows: list[dict[str, Any]] = []
    avg_acc: dict[tuple[str, str], list[float]] = defaultdict(list)
    checks: dict[str, dict[str, Any]] = {}  # FR-ZCP-08 rigor gates
    runs_by_repo_arm: dict[str, dict[str, list[dict[str, Any]]]] = {}

    for meta in repos_meta:
        slug = meta["slug"]
        repo_name = meta.get("repo_name", slug)
        # Allow either key: repos.yaml uses 'slug' but the actual cloned dir
        # name may differ (e.g. vscode vs VSCode). Fall back to slug.
        repo_runs_all = by_repo.get(slug) or by_repo.get(repo_name) or by_repo.get(meta.get("clone_dir", slug)) or []
        repo_runs = [r for r in repo_runs_all if r.get("valid")]
        by_arm = {arm: [r for r in repo_runs if r["arm"] == arm] for arm in arms}
        runs_by_repo_arm[slug] = by_arm
        stats = {arm: arm_stats(by_arm[arm]) for arm in arms}

        # Track averages where both the arm and the baseline have data.
        for arm in non_base:
            for key, _ in AVG_METRICS:
                w, wo = stats[arm][key], stats[base][key]
                if w is not None and wo is not None and wo != 0:
                    avg_acc[(arm, key)].append((w - wo) / wo * 100.0)

        # FR-ZCP-08 rigor gates per repo (like-for-like, trials, pinning).
        prompt_shas = {r.get("prompt_sha256") for r in repo_runs if r.get("prompt_sha256")}
        models = {r.get("actual_model") for r in repo_runs if r.get("actual_model")}
        shas = {r.get("repo_sha") for r in repo_runs if r.get("repo_sha")}
        mcp_runs = [r for arm in arms if arm not in NO_MCP_ARMS for r in by_arm[arm]]
        checks[slug] = {
            "trials_min3": bool(arms) and all(len(by_arm[a]) >= 3 for a in arms),
            "n_by_arm": {a: len(by_arm[a]) for a in arms},
            "prompt_sha_uniform": len(prompt_shas) <= 1,
            "model_uniform": len(models) <= 1,
            "repo_sha_uniform": len(shas) <= 1,
            "repo_sha": sorted(shas)[0][:10] if len(shas) == 1 and shas else None,
            "prompt_version": next((r.get("prompt_version") for r in repo_runs if r.get("prompt_version")), None),
            "tool_smoke": all(r.get("mcp_tool_count", 0) > 0 for r in mcp_runs) if mcp_runs else None,
        }
        rows.append({"codebase": slug, "language": meta.get("language", ""), "arms": stats})

    # Markdown
    lines: list[str] = []
    today = dt.date.today().isoformat()
    lines.append(f"# Cross-Tool Agent A/B Benchmark Report")
    lines.append("")
    lines.append(f"**Date:** {today}  ")
    lines.append(f"**Method:** `claude -p` headless; one column group per arm (baseline = no MCP; context-mode and leankg = their MCP server). Built-in Read/Grep/Bash available to every arm.  ")
    lines.append(f"**Runs per arm per repo:** median reported (matches codegraph methodology).  ")
    lines.append(
        f"**Total runs loaded:** {len(runs)} (valid: {len(valid_runs)}, "
        f"dropped: {len(invalid_runs)}).  "
    )
    lines.append("")
    lines.append("## Per-Repo Results")
    lines.append("")
    head = ["Codebase", "Language"]
    for arm in arms:
        head += [f"{arm}: N", f"{arm}: tool calls", f"{arm}: time", f"{arm}: tokens", f"{arm}: tool_result bytes"]
        if arm in non_base:
            head.append(f"{arm}: tool-result bytes reduction")
    lines.append("| " + " | ".join(head) + " |")
    lines.append(table_rule(len(head)))
    for row in rows:
        cells = [f"**{row['codebase']}**", row["language"]]
        for arm in arms:
            s = row["arms"][arm]
            cells += [
                str(s["n"]), fmt_int(s["tool_calls"]), fmt_dur(s["duration_s"]),
                fmt_int(s["total_tokens"]), fmt_bytes(s["tool_result_bytes"]),
            ]
            if arm in non_base:
                cells.append(fmt_reduction(s["tool_result_bytes"], row["arms"][base]["tool_result_bytes"]))
        lines.append("| " + " | ".join(cells) + " |")

    # Averages
    lines.append("")
    lines.append("## Average Savings (median across repos)")
    lines.append("")
    lines.append("| " + " | ".join(["Metric"] + [f"{a} vs {base}" for a in non_base]) + " |")
    lines.append(table_rule(1 + len(non_base)))
    for key, label in AVG_METRICS:
        cells = []
        for arm in non_base:
            vals = avg_acc[(arm, key)]
            if vals:
                avg = sum(vals) / len(vals)
                cells.append(f"{'' if avg < 0 else '+'}{avg:.0f}%")
            else:
                cells.append("N/A")
        lines.append("| " + " | ".join([label] + cells) + " |")

    # IQR appendix for transparency on the median-vs-mean story
    arm_list = " / ".join(arms)
    lines.append("")
    lines.append("## Variance Appendix (IQR across runs)")
    lines.append("")
    lines.append("Per-arm IQR across the N runs per repo. High IQR on the baseline")
    lines.append("arm is expected; the MCP arms should be tighter.")
    lines.append("")
    lines.append(f"| Codebase | Tool calls IQR ({arm_list}) | Cost IQR ({arm_list}) | Time IQR ({arm_list}) |")
    lines.append(table_rule(4))
    for meta in repos_meta:
        slug = meta["slug"]
        by_arm = runs_by_repo_arm[slug]
        if not any(by_arm.values()):
            continue
        lines.append(
            f"| {slug} | "
            f"{iqr_cell(by_arm, arms, 'tool_calls')} | "
            f"{iqr_cell(by_arm, arms, 'total_cost_usd', lambda v: f'{v:.2f}')} | "
            f"{iqr_cell(by_arm, arms, 'duration_s')} |"
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

    if quality:
        lines.append("")
        lines.append("## Answer Quality (judge-blind, rubric 0-6)")
        lines.append("")
        lines.append("Median blind-rubric score per arm (judge never saw arm labels;")
        lines.append("answers shuffled before grading). Higher is better. One row per")
        lines.append("judge model — scores from different judges never blend.")
        lines.append("")
        lines.append("| " + " | ".join(["Codebase", "Judge"] + arms + [f"{a} vs {base}" for a in non_base]) + " |")
        lines.append(table_rule(2 + len(arms) + len(non_base)))
        for slug, judge, med in quality:
            cells = [fmt_score(med[a]) for a in arms] + [fmt_delta(med[a], med[base]) for a in non_base]
            lines.append("| " + " | ".join([slug, judge] + cells) + " |")

    # Losing rows: shown, never dropped (RUNNING.md section 6).
    lines.append("")
    lines.append("## Losing rows")
    lines.append("")
    losing = losing_rows(rows, quality)
    if "leankg" not in arms:
        lines.append("No leankg arm in these results; nothing to compare.")
    elif not losing:
        lines.append("None: leankg is not worse than baseline or context-mode on any repo or metric.")
    else:
        lines.append("Every repo and metric where leankg is worse than baseline or context-mode")
        lines.append("(higher cost, lower quality). Shown, not dropped.")
        lines.append("")
        lines.append("| Codebase | Metric | leankg | Compared with | Value |")
        lines.append(table_rule(5))
        for slug, metric, lk, ref, rv in losing:
            lines.append(f"| {slug} | {metric} | {lk} | {ref} | {rv} |")

    # FR-ZCP-08: the zg pitfalls checklist, computed from run metadata.
    lines.append("")
    lines.append("## Rigor Checklist (zg pitfalls, computed from run metadata)")
    lines.append("")
    lines.append("| Codebase | >=3 trials/arm | Prompt identical | Model uniform | Corpus pinned | MCP tools reachable |")
    lines.append("| --- | --- | --- | --- | --- | --- |")
    for meta_r in repos_meta:
        slug = meta_r["slug"]
        c = checks.get(slug)
        # Only repos with at least one valid run appear (0/0 noise hides
        # nothing but buries real rows).
        if not c or not any(c["n_by_arm"].values()):
            continue
        mark = lambda ok: "PASS" if ok else ("FAIL" if ok is False else "n/a")
        counts = "/".join(str(c["n_by_arm"][a]) for a in arms)
        lines.append(
            f"| {slug} | {mark(c['trials_min3'])} ({counts}) | "
            f"{mark(c['prompt_sha_uniform'])} (v{c['prompt_version']}) | "
            f"{mark(c['model_uniform'])} | "
            f"{mark(c['repo_sha_uniform'])} ({c['repo_sha'] or 'unpinned'}) | "
            f"{mark(c['tool_smoke'])} |"
        )
    lines.append("")
    lines.append("Leakage guard: baseline-arm runs with any attached MCP server are")
    lines.append("dropped at parse time (`mcp_leaked_into_baseline_arm`), so a")
    lines.append("contaminated baseline can never reach the medians.")

    lines.append("")
    lines.append("## Methodology")
    lines.append("")
    lines.append("- Same harness as `colbymchenry/codegraph` 7-repo suite (re-validated 2026-07-21, Opus 4.8).")
    lines.append("- Each arm = `claude -p <prompt>` headless, same question per repo, median of N runs.")
    lines.append("- `--mcp-config <file> --strict-mcp-config` loads only the file's MCP servers; nothing else.")
    lines.append("- `--bare` disables CLAUDE.md auto-discovery, hooks, LSP, plugins, and attribution so the run is hermetic.")
    lines.append("- Repos are fetched at depth 1 at the commit SHA locked in `repos.lock.yaml` (`make lock`); `run_arm.sh` refuses to run unless the clone HEAD matches the lock.")
    lines.append("- LeanKG index is rebuilt (`rm -rf .leankg`, then `leankg index .`, in `run_arm.sh`) before every leankg-arm trial to keep runs deterministic.")
    lines.append("- `tool-result bytes reduction` = 1 - arm / baseline on median `tool_result_bytes` (bytes returned to the model only). Raw tool-output bytes before any compaction are not recorded for the context-mode arm, so this is not directly comparable to context-mode's own bytes-in-context figure.")
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
        "arms": arms,
        "rows": rows,
        "averages_pct": {
            arm: {
                key: (sum(avg_acc[(arm, key)]) / len(avg_acc[(arm, key)]) if avg_acc[(arm, key)] else None)
                for key, _ in AVG_METRICS
            }
            for arm in non_base
        },
        "dropped_runs": [
            {k: v for k, v in r.items() if k != "_source_path"}
            for r in invalid_runs
        ],
        "rigor_checks": checks,
        "raw_runs": runs,
    }
    return md, json_payload


def synthetic_run(repo: str, arm: str, run_idx: int, mcp: list[str], **fields: Any) -> dict[str, Any]:
    """One valid run row in the run-JSONL schema; `fields` override the defaults."""
    row: dict[str, Any] = {
        "repo": repo, "arm": arm, "run_idx": run_idx,
        "model": "sonnet", "actual_model": "claude-sonnet",
        "mcp_servers": mcp, "mcp_tool_count": 4 if mcp else 0,
        "valid": True, "invalid_reason": None,
        "prompt_chars": 100, "exit_code": 0,
        "duration_s": 40.0 + run_idx, "total_cost_usd": 0.30 + 0.01 * run_idx,
        "input_tokens": 260000, "output_tokens": 4500,
        "cache_read_tokens": 200000,
        "tool_calls": 2 + run_idx, "file_reads": 1, "num_turns": 1,
        "stop_reason": "end_turn", "result_chars": 1200,
        "repo_sha": "a" * 40, "prompt_sha256": "b" * 64,
        "prompt_version": 1,
    }
    row.update(fields)
    return row


def build_selftest_tree(root: Path) -> None:
    """Fabricate a minimal results tree exercising every rigor gate.

    gin: 4 valid runs per arm (trials PASS), one WITHOUT run with an MCP
    server attached (leakage guard must drop it), scores from two judge
    models (must stay segmented), a foreign scores.jsonl row that must
    never enter the run stream.
    """
    base = root / "runs" / "2026-01-01" / "gin"
    for arm, mcp in (("with", ["leankg"]), ("without", [])):
        d = base / arm
        d.mkdir(parents=True, exist_ok=True)
        rows = [synthetic_run("gin", arm, i, mcp) for i in range(1, 5)]
        if arm == "without":
            leaked = dict(rows[0])
            leaked.update(
                {
                    "run_idx": 5, "mcp_servers": ["leankg"],
                    "valid": False,
                    "invalid_reason": "mcp_leaked_into_baseline_arm",
                }
            )
            rows.append(leaked)
        (d / "runs.jsonl").write_text(
            "".join(json.dumps(r) + "\n" for r in rows), encoding="utf-8"
        )
    scores = root / "scores"
    scores.mkdir(parents=True, exist_ok=True)
    lines = []
    for judge in ("j1", "j2"):
        for arm in ("with", "without"):
            for i in range(1, 5):
                lines.append(
                    json.dumps(
                        {
                            "repo": "gin", "arm": arm, "run_idx": i,
                            "judge_model": judge, "score": 5 if arm == "with" else 3,
                        }
                    )
                )
    # A row with run-schema keys must be ignored by load_runs (scores skip).
    (scores / "scores.jsonl").write_text(
        "\n".join(lines) + "\n", encoding="utf-8"
    )


# Three-arm fixture for repo gin: (tool_calls, duration_s, input_tokens,
# tool_result_bytes, judge-j1 quality). Output tokens are fixed at 100.
ARMS_FIXTURE = {
    "baseline": (10, 50.0, 1000, 40000, 3),
    "context-mode": (6, 40.0, 800, 20000, 4),
    "leankg": (4, 55.0, 700, 30000, 2),
}


def build_arms_tree(root: Path) -> None:
    """Fabricate baseline / context-mode / leankg results for gin.

    leankg is slower than both other arms and sends more bytes than
    context-mode, and scores below both on judge j1, so every losing-row
    case is exercised.
    """
    for arm, (calls, dur, inp, nbytes, _) in ARMS_FIXTURE.items():
        d = root / "runs" / "2026-01-02" / "gin" / arm
        d.mkdir(parents=True, exist_ok=True)
        mcp = [] if arm == "baseline" else [arm]
        rows = [
            synthetic_run(
                "gin", arm, i, mcp,
                tool_calls=calls, duration_s=dur, input_tokens=inp,
                output_tokens=100, cache_read_tokens=0,
                tool_result_bytes=nbytes,
            )
            for i in range(1, 5)
        ]
        (d / "runs.jsonl").write_text(
            "".join(json.dumps(r) + "\n" for r in rows), encoding="utf-8"
        )
    scores = root / "scores"
    scores.mkdir(parents=True, exist_ok=True)
    lines = [
        json.dumps({"repo": "gin", "arm": arm, "run_idx": i, "judge_model": "j1", "score": quality})
        for arm, (*_, quality) in ARMS_FIXTURE.items()
        for i in range(1, 4)
    ]
    (scores / "scores.jsonl").write_text("\n".join(lines) + "\n", encoding="utf-8")


def selftest() -> int:
    """Offline gate check: builds a fixture tree and asserts every rigor
    gate computes the expected verdict. Exits 0 only if all gates hold."""
    import tempfile

    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        build_selftest_tree(root)
        rows = load_runs(root)
        leaked = [r for r in rows if not r.get("valid") and "leaked" in (r.get("dropped_reason") or "")]
        assert len(leaked) == 1, f"leaked run not dropped: {len(leaked)}"
        valid = [r for r in rows if r.get("valid")]
        assert len(valid) == 8, f"valid count wrong: {len(valid)} (scores.jsonl leaked into runs?)"
        by_arm = {arm: [r for r in valid if r["arm"] == arm] for arm in ("with", "without")}
        assert all(len(v) == 4 for v in by_arm.values()), "trial counts wrong"
        # Score rows never enter the run stream: every valid row has metrics.
        assert all("tool_calls" in r for r in valid), "score row polluted runs"
        md, payload = build_report([{"slug": "gin", "language": "Go"}], rows, results_root=root)
        checks = payload["rigor_checks"]["gin"]
        assert checks["trials_min3"] is True, checks
        assert checks["tool_smoke"] is True, checks
        assert checks["repo_sha"] == "a" * 10, checks
        assert checks["prompt_sha_uniform"] is True, checks
        assert "| gin | j1 | 5.0 | 3.0 | +2.0 |" in md, "judge j1 row missing"
        assert "| gin | j2 | 5.0 | 3.0 | +2.0 |" in md, "judge j2 row missing (blended?)"
        assert "mcp_leaked_into_baseline_arm" in md, "leak drop not surfaced"
        assert "dropped at parse time (`mcp_leaked_into_baseline_arm`)" in md, "leakage guard text names the wrong reason"
        print("aggregate selftest: OK (leak drop, trials gate, tool smoke, pinning, judge segmentation)")

        # Three arms, no legacy with/without: arms come from the rows.
        with tempfile.TemporaryDirectory() as td3:
            arms_root = Path(td3)
            build_arms_tree(arms_root)
            md3, payload3 = build_report(
                [{"slug": "gin", "language": "Go"}], load_runs(arms_root), results_root=arms_root
            )
            header = next((l for l in md3.splitlines() if l.startswith("| Codebase | Language |")), "")
            pos = [header.find(f"{arm}:") for arm in ("baseline", "context-mode", "leankg")]
            assert all(p >= 0 for p in pos) and pos == sorted(pos), header
            assert "context-mode: tool-result bytes reduction" in header, header
            assert "bytes in context" not in md3, "old column name still in report"
            assert "| **gin** | Go | 4 | 10 | 50s | 1,100 | 40,000 B | 4 | 6 | 40s | 900 | 20,000 B | 50% | 4 | 4 | 55s | 800 | 30,000 B | 25% |" in md3, md3
            assert "| gin | j1 | 3.0 | 4.0 | 2.0 | +1.0 | -1.0 |" in md3, "quality row wrong"
            assert "| Metric | context-mode vs baseline | leankg vs baseline |" in md3, "averages header wrong"
            assert "| Tool calls | -40% | -60% |" in md3, "tool-call average wrong"
            assert "| Wall-clock time | -20% | +10% |" in md3, "wall-time average wrong"
            assert "## Losing rows" in md3, "losing-rows section missing"
            for line in (
                "| gin | wall time | 55s | baseline | 50s |",
                "| gin | wall time | 55s | context-mode | 40s |",
                "| gin | tool_result bytes | 30,000 B | context-mode | 20,000 B |",
                "| gin | quality (j1) | 2.0 | baseline | 3.0 |",
                "| gin | quality (j1) | 2.0 | context-mode | 4.0 |",
            ):
                assert line in md3, f"losing row missing: {line}"
            assert payload3["rigor_checks"]["gin"]["trials_min3"] is True, payload3["rigor_checks"]
            assert payload3["rigor_checks"]["gin"]["tool_smoke"] is True, payload3["rigor_checks"]
            assert discover_arms(["leankg", "zeta", "baseline", "context-mode", "without", "with"]) == [
                "baseline", "context-mode", "leankg", "with", "without", "zeta",
            ]
            print("aggregate selftest: OK (three arms: column groups, medians, byte reduction, losing rows)")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--selftest",
        action="store_true",
        help="offline: build a fixture tree in a tempdir and assert every rigor gate",
    )
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
    if args.selftest:
        return selftest()

    repos_data = load_yaml_fallback(args.repos)
    repos_meta = repos_data.get("repos", [])

    runs = load_runs(args.results)
    if not runs:
        print(f"warn: no runs found under {args.results}", file=sys.stderr)
    md, payload = build_report(repos_meta, runs, results_root=args.results)

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