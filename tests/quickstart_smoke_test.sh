#!/usr/bin/env bash
# H5 (FR-PLG-7) — unit-ish tests for scripts/quickstart_smoke.sh
# Fast, hermetic checks: syntax + dry-run plan completeness + guard behavior.
# The real timed smoke (network + PG) is NOT run here; see scripts/quickstart_smoke.sh.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SMOKE="$SCRIPT_DIR/../scripts/quickstart_smoke.sh"
PASS=0; FAIL=0

check() {
    local name="$1" cond="$2"
    if eval "$cond"; then
        PASS=$((PASS+1)); echo "PASS: $name"
    else
        FAIL=$((FAIL+1)); echo "FAIL: $name"
    fi
}

# T1: script exists and passes bash -n syntax check
check "syntax (bash -n)" "[ -x '$SMOKE' ] && bash -n '$SMOKE'"

# T2: dry-run prints all 7 steps without executing anything (no binary, no PG needed)
DRY_OUT="$(QUICKSTART_DRY_RUN=1 bash "$SMOKE" 2>&1)"; DRY_RC=$?
check "dry-run exits 0" "[ $DRY_RC -eq 0 ]"
for step in init migrate index server_up first_query doctor cleanup; do
    check "dry-run lists step: $step" "printf '%s\n' \"\$DRY_OUT\" | grep -qE '^${step}([^a-zA-Z0-9_]|$)'"
done

# T3: dry-run output has a TOTAL line and the 300s budget
check "dry-run has TOTAL line" "printf '%s' \"\$DRY_OUT\" | grep -q '^TOTAL'"
check "300s budget declared"  "grep -q 'TIME_BUDGET=300' '$SMOKE'"

# T4: non-dry run without LEANKG_PG_URL fails fast with setup-error exit code 2
OUT_NOENV="$(env -u LEANKG_PG_URL bash "$SMOKE" 2>&1)"; RC_NOENV=$?
check "missing LEANKG_PG_URL -> exit 2 (setup error)" "[ $RC_NOENV -eq 2 ]"
check "missing-URL message mentions LEANKG_PG_URL" "printf '%s' \"\$OUT_NOENV\" | grep -q 'LEANKG_PG_URL'"

echo
echo "quickstart_smoke_test: $PASS passed, $FAIL failed"
[ $FAIL -eq 0 ]
