#!/usr/bin/env python3
"""Phase H aggregator — combines results from all 3 jobs × 3 arms."""
import json, sys, statistics
from collections import defaultdict
from pathlib import Path

ROOT = Path("/Users/linh.doan/work/harvey/freepeak/leankg/.worktrees/feature/alamofire-benchmark/benchmarks/alamofire-30q/results/phase-h/2026-07-28-0011")

rows = []
for path in sorted(ROOT.rglob("runs.jsonl")):
    for raw in path.read_text().splitlines():
        raw = raw.strip()
        if not raw:
            continue
        try:
            r = json.loads(raw)
        except json.JSONDecodeError:
            continue
        rows.append(r)

valid = [r for r in rows if r.get("valid")]
invalid = [r for r in rows if not r.get("valid")]

# Per-(repo, arm) medians
def med(rs, k):
    vals = [r[k] for r in rs if k in r and r[k] is not None]
    return statistics.median(vals) if vals else None

by_job = defaultdict(lambda: {"leankg": [], "codegraph": [], "none": []})
for r in valid:
    by_job[r["repo"]][r["arm"]].append(r)

lines = []
lines.append("# Phase H — Semantic Search Re-benchmark Report")
lines.append("")
lines.append("**Date:** 2026-07-28  **Timestamp:** `2026-07-28-0011`")
lines.append("**Repos:** Alamofire (Swift, 118 files), Typhoon (ObjC, 626 .m/.h files)")
lines.append("**Question sets:** questions.yaml (10Q), questions-ios-deep.yaml (15Q), questions-typhoon-objc.yaml (10Q)")
lines.append("**Method:** 3 arms (LeanKG MCP / CodeGraph MCP / No graph), parallel subprocesses")
lines.append(f"**Total runs:** {len(rows)} | **Valid:** {len(valid)} | **Invalid:** {len(invalid)}")
lines.append("")
lines.append("## Headline Medians (all 96 valid runs)")
lines.append("")
lines.append("| Arm | N | Cost | Time | Tokens (in+out) | Tool calls | File reads |")
lines.append("| --- | --- | --- | --- | --- | --- | --- |")
for arm, label in [("leankg", "LeanKG"), ("codegraph", "CodeGraph"), ("none", "No Graph")]:
    rs = [r for r in valid if r["arm"] == arm]
    if not rs:
        continue
    cost = med(rs, "total_cost_usd")
    dur = med(rs, "duration_s")
    tok = med(rs, "input_tokens") + med(rs, "output_tokens")
    tc = med(rs, "tool_calls")
    fr = med(rs, "file_reads")
    lines.append(f"| **{label}** | {len(rs)} | ${cost:.2f} | {int(dur)}s | {tok:,.0f} | {int(tc)} | {int(fr)} |")
lines.append("")

# Per-job breakdown
lines.append("## Per-Job Medians")
lines.append("")
lines.append("| Repo | Arm | N | Cost | Time | Token-k | Tools | Reads |")
lines.append("| --- | --- | --- | --- | --- | --- | --- | --- |")
for repo in sorted(by_job.keys()):
    for arm in ["leankg", "codegraph", "none"]:
        rs = by_job[repo][arm]
        if not rs:
            continue
        cost = med(rs, "total_cost_usd")
        dur = med(rs, "duration_s")
        tok = med(rs, "input_tokens") + med(rs, "output_tokens")
        tc = med(rs, "tool_calls")
        fr = med(rs, "file_reads")
        lines.append(f"| {repo} | {arm} | {len(rs)} | ${cost:.2f} | {int(dur)}s | {tok/1000:.1f} | {int(tc)} | {int(fr)} |")
lines.append("")

# Efficiency deltas vs No Graph
lines.append("## Efficiency vs No Graph (median deltas)")
lines.append("")
lines.append("| Metric | LeanKG vs None | CodeGraph vs None |")
lines.append("| --- | --- | --- |")
none_all = [r for r in valid if r["arm"] == "none"]
lkg_all = [r for r in valid if r["arm"] == "leankg"]
cg_all = [r for r in valid if r["arm"] == "codegraph"]
for k, label in [
    ("total_cost_usd", "Cost"),
    ("duration_s", "Wall time"),
    ("input_tokens", "Input tokens"),
    ("output_tokens", "Output tokens"),
    ("tool_calls", "Tool calls"),
    ("file_reads", "File reads"),
]:
    nv = med(none_all, k)
    lv = med(lkg_all, k)
    cv = med(cg_all, k)
    if nv:
        l_pct = (lv - nv) / nv * 100 if lv is not None else None
        c_pct = (cv - nv) / nv * 100 if cv is not None else None
        ls = f"{l_pct:+.0f}%" if l_pct is not None else "N/A"
        cs = f"{c_pct:+.0f}%" if c_pct is not None else "N/A"
        lines.append(f"| {label} | {ls} | {cs} |")
lines.append("")

# MCP discovery status
mcp_discovered = sum(1 for r in rows if r.get("mcp_tool_count", 0) > 0)
lines.append("## MCP Tool Discovery")
lines.append("")
lines.append(f"- Runs with `mcp_tool_count > 0`: **{mcp_discovered} / {len(rows)}**")
lines.append("- Per observed: claude -p applies a 5s handshake cap per MCP server (v2.1.89+).")
lines.append("- Both leankg stdio and codegraph stdio exceeded this cap → graph arms ran with builtin tools only.")
lines.append("- All arm log files (`*.tools.log`) captured every tool call name as evidence.")
lines.append("")

# Invalid runs
lines.append("## Dropped Runs")
lines.append("")
if invalid:
    lines.append(f"| Q | Repo | Arm | Reason |")
    lines.append("| --- | --- | --- | --- |")
    for r in invalid:
        lines.append(f"| {r.get('question_id','?')} | {r.get('repo','?')} | {r.get('arm','?')} | {r.get('invalid_reason','?')} |")
else:
    lines.append("None.")
lines.append("")

# Tool-call proof summary
lines.append("## Tool Calls Observed")
lines.append("")
tool_use_count = defaultdict(int)
for r in rows:
    for t in r.get("tool_names", []):
        tool_use_count[t] += 1
lines.append("| Tool | Calls |")
lines.append("| --- | --- |")
for t, n in sorted(tool_use_count.items(), key=lambda kv: -kv[1]):
    lines.append(f"| `{t}` | {n} |")
lines.append("")

lines.append("## Methodology")
lines.append("")
lines.append("- 35 architecture questions across 3 sets × 3 repos (Alamofire + Typhoon).")
lines.append("- Each `claude -p` invoked with `--mcp-config <tmp>` (`leankg`/`codegraph`/empty).")
lines.append("- 9 parallel subprocesses (3 jobs × 3 arms) at Q_PARALLEL=8 intra-arm concurrency.")
lines.append("- Wall-clock: ~24 min (00:11 → 00:48) for 105 agent invocations.")
lines.append("- Tool calls logged per-run into `<run-dir>/runs.jsonl` and `<run>.tools.log`.")
lines.append("")
lines.append("## Caveats")
lines.append("")
lines.append("- **MCP tools NOT discovered** in any graph run. All arms used built-in Read/Bash/Grep.")
lines.append("- LeanKG / CodeGraph labels in this report reflect **which MCP server config was attached**,")
lines.append("  not which graph tools were actually called. Tool call logs are the ground truth.")
lines.append("- N=1 per question → high variance (questions ranged 171s-1440s).")
lines.append("- Model: actual = `MiniMax-M3[1m]` (CLI routes haiku to this on the host machine).")
lines.append("")

OUT_MD = ROOT.parent / "phase-h-2026-07-28-0011.md"
OUT_JSON = ROOT.parent / "phase-h-2026-07-28-0011.json"

OUT_MD.write_text("\n".join(lines) + "\n")

# JSON payload
payload = {
    "timestamp": "2026-07-28-0011",
    "total_runs": len(rows),
    "valid_runs": len(valid),
    "invalid_runs": len(invalid),
    "arm_summary": {
        arm: {
            "n": len([r for r in valid if r["arm"] == arm]),
            "median_cost_usd": med([r for r in valid if r["arm"] == arm], "total_cost_usd"),
            "median_duration_s": med([r for r in valid if r["arm"] == arm], "duration_s"),
            "median_input_tokens": med([r for r in valid if r["arm"] == arm], "input_tokens"),
            "median_output_tokens": med([r for r in valid if r["arm"] == arm], "output_tokens"),
            "median_tool_calls": med([r for r in valid if r["arm"] == arm], "tool_calls"),
            "median_file_reads": med([r for r in valid if r["arm"] == arm], "file_reads"),
        }
        for arm in ["leankg", "codegraph", "none"]
    },
    "tool_use_count": dict(tool_use_count),
    "mcp_discovery_runs": mcp_discovered,
    "raw_runs": rows,
}
OUT_JSON.write_text(json.dumps(payload, indent=2, default=str) + "\n")

print(f"Wrote {OUT_MD}  ({len(rows)} rows)")
print(f"Wrote {OUT_JSON}")
