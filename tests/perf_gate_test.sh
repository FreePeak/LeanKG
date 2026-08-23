#!/usr/bin/env bash
# TDD suite for scripts/perf_gate.sh — run: bash tests/perf_gate_test.sh
set -u
SCRIPT="$(cd "$(dirname "$0")/.." && pwd)/scripts/perf_gate.sh"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
pass=0; fail=0
ok()   { pass=$((pass+1)); echo "PASS: $1"; }
bad()  { fail=$((fail+1)); echo "FAIL: $1"; }

# --- valid baseline fixture -------------------------------------------------
cat > "$TMP/base.json" <<'EOF'
{
  "_comment": "fixture",
  "index": 1000,
  "server_boot": 500,
  "search_code": 200,
  "get_impact_radius": 300
}
EOF

# 1. --update writes valid JSON with all four ops
OUT="$TMP/new.json"
if bash "$SCRIPT" --update --output "$OUT" --dry-run-metrics '{"index":900,"server_boot":480,"search_code":190,"get_impact_radius":310}' >/dev/null 2>&1 \
   && python3 -c "
import json,sys
d=json.load(open('$OUT'))
missing=[k for k in ('index','server_boot','search_code','get_impact_radius') if k not in d]
sys.exit(1 if missing else 0)" ; then ok "--update writes valid JSON with all ops"; else bad "--update writes valid JSON"; fi

# 2. comparison passes at +5%
if bash "$SCRIPT" --baseline "$TMP/base.json" --metrics '{"index":1050,"server_boot":525,"search_code":210,"get_impact_radius":315}' >/dev/null 2>&1; then ok "+5% regression passes"; else bad "+5% should pass"; fi

# 3. comparison fails at +25%
if bash "$SCRIPT" --baseline "$TMP/base.json" --metrics '{"index":1250,"server_boot":525,"search_code":210,"get_impact_radius":315}' >/dev/null 2>&1; then bad "+25% should fail"; else ok "+25% regression fails (exit!=0)"; fi

# 4. PERF_GATE_PCT=1 makes small (+3%) regressions fail
if PERF_GATE_PCT=1 bash "$SCRIPT" --baseline "$TMP/base.json" --metrics '{"index":1030,"server_boot":505,"search_code":202,"get_impact_radius":303}' >/dev/null 2>&1; then bad "PCT=1 should flag +3%"; else ok "PERF_GATE_PCT=1 flags small regression"; fi

# 5. improvement passes
if bash "$SCRIPT" --baseline "$TMP/base.json" --metrics '{"index":800,"server_boot":400,"search_code":160,"get_impact_radius":240}' >/dev/null 2>&1; then ok "improvement passes"; else bad "improvement should pass"; fi

# 6. malformed baseline → exit 1
echo "{ broken" > "$TMP/bad.json"
CODE=0; bash "$SCRIPT" --baseline "$TMP/bad.json" --metrics '{"index":1000,"server_boot":500,"search_code":200,"get_impact_radius":300}' >/dev/null 2>&1 || CODE=$?
if [ "$CODE" = "1" ]; then ok "malformed baseline exits 1"; else bad "malformed baseline expected exit 1 got $CODE"; fi

# 7. missing baseline without --update → warn pass (exit 0)
CODE=0; bash "$SCRIPT" --baseline "$TMP/absent.json" --metrics '{"index":1000,"server_boot":500,"search_code":200,"get_impact_radius":300}' >/dev/null 2>&1 || CODE=$?
if [ "$CODE" = "0" ]; then ok "missing baseline warns and passes (exit 0)"; else bad "missing baseline expected exit 0 got $CODE"; fi

# 8. missing metrics keys handled (op absent from current run → skip, not crash)
if bash "$SCRIPT" --baseline "$TMP/base.json" --metrics '{"index":1050}' >/dev/null 2>&1; then ok "partial metrics tolerated"; else bad "partial metrics should not crash"; fi

echo
echo "perf_gate_test: $pass passed, $fail failed"
[ "$fail" = "0" ]
