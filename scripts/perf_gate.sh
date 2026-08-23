#!/usr/bin/env bash
# Deterministic perf regression gate (CORE-6 / backlog H8).
#
# Usage:
#   scripts/perf_gate.sh --baseline FILE --metrics JSON   compare mode
#   scripts/perf_gate.sh --update --output FILE --metrics JSON   write baseline
#   PERF_GATE_PCT=20   max tolerated regression percent (default 20)
#
# Compare mode exits: 0 pass/warn · 1 input error · 2 regression detected.
set -u
PCT="${PERF_GATE_PCT:-20}"
BASELINE="" ; METRICS="" ; UPDATE=0 ; OUT=""
while [ $# -gt 0 ]; do
  case "$1" in
    --baseline) BASELINE="$2"; shift 2 ;;
    --metrics)  METRICS="$2";  shift 2 ;;
    --dry-run-metrics) METRICS="$2"; shift 2 ;;
    --update)   UPDATE=1;      shift ;;
    --output)   OUT="$2";      shift 2 ;;
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

OPS='index server_boot search_code get_impact_radius'

if [ -z "$METRICS" ]; then
  echo "no --metrics provided (live workload collection lives in CI workflow)" >&2
  exit 1
fi

python3 - "$BASELINE" "$METRICS" "$UPDATE" "$OUT" "$PCT" <<'PYEOF'
import json, sys
baseline_path, metrics_json, update, out_path, pct = (
    sys.argv[1], sys.argv[2], sys.argv[3] == "1", sys.argv[4], float(sys.argv[5]))
try:
    current = json.loads(metrics_json)
except Exception as e:
    print(f"invalid --metrics JSON: {e}"); sys.exit(1)
ops = ["index", "server_boot", "search_code", "get_impact_radius"]
current = {k: v for k, v in current.items() if k in ops and isinstance(v, (int, float))}

if update:
    doc = {"_comment": "perf gate baseline — regenerate via scripts/perf_gate.sh --update",
           **current}
    if not out_path:
        print("--update requires --output"); sys.exit(1)
    with open(out_path, "w") as f:
        json.dump(doc, f, indent=2, sort_keys=True); f.write("\n")
    print(f"WROTE baseline {out_path}: { {k: v for k, v in doc.items() if k != '_comment'} }")
    sys.exit(0)

if not baseline_path:
    print("WARN: no baseline configured — nothing to compare against")
    sys.exit(0)
try:
    with open(baseline_path) as f:
        base = json.load(f)
except FileNotFoundError:
    # Bootstrap: no baseline yet (first run / new machine) — warn, don't gate.
    print(f"WARN: baseline {baseline_path} not found — run with --update to record one")
    sys.exit(0)
except Exception as e:
    print(f"ERROR: malformed baseline {baseline_path}: {e}"); sys.exit(1)

regressions = []
rows = []
for op in ops:
    if op not in base or op not in current:
        continue
    b, c = float(base[op]), float(current[op])
    delta_pct = (c - b) / b * 100 if b else 0.0
    rows.append((op, b, c, delta_pct))
print(f"{'op':<20} {'baseline_ms':>12} {'current_ms':>12} {'delta':>9}")
worst = 0.0
for op, b, c, d in rows:
    flag = ""
    if d > pct:
        flag = "  REGRESSION"
        regressions.append(op)
    worst = max(worst, d)
    print(f"{op:<20} {b:>12.0f} {c:>12.0f} {d:>+8.1f}%{flag}")
if len(rows) < len(ops):
    print(f"note: {len(ops)-len(rows)} op(s) skipped (absent from one side)")
if regressions:
    print(f"FAIL: regression over {pct:.0f}% on: {', '.join(regressions)}")
    sys.exit(2)
print(f"PASS: all measured ops within +{pct:.0f}% of baseline (worst {worst:+.1f}%)")
PYEOF
