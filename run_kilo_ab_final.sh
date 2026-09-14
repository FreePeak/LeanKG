#!/bin/bash
# LeanKG A/B Testing Benchmark via Kilo CLI with MCP
# This is the CORRECT way to test - using kilo CLI to measure actual AI token consumption
#
# FR-ZCP-08 hardening (issue #276): same pins-or-refuse gate as
# run_kilo_ab_test.sh — every trial is recorded via
# scripts/kilo_ab_common.sh + benchmark/ab/abrun with pinned corpus /
# tool SHAs and prompt-template hashes; >=3 trials/arm; per-arm medians;
# scorer-emitted zg pitfalls checklist in the results JSON.

set -e

WORKTREE_DIR="/Users/linh.doan/work/harvey/freepeak/.worktree/leankg-ab-benchmark"
PROMPTS_FILE="${WORKTREE_DIR}/ab_benchmark/prompts/queries.yaml"
RESULTS_DIR="${WORKTREE_DIR}/ab_benchmark/results"
KILO_CONFIG="$HOME/.config/kilo/kilo.json"
KILO_WORKTREE_DIR="$HOME/.config/kilo/worktree"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

TRIALS_JSONL="${RESULTS_DIR}/ab_final_trials.jsonl"
REPORT_JSON="${RESULTS_DIR}/ab_final_report.json"

# shellcheck source=kilo_ab_common.sh
source "${REPO_ROOT}/scripts/kilo_ab_common.sh"

BASELINE_TEMPLATE="Answer about the LeanKG codebase {query}"
LEANKG_TEMPLATE="Use LeanKG MCP tools to answer about the LeanKG codebase {query}"

# Pin or refuse — before any measurement runs.
ab_resolve_pins "kilo-ab-final-v1" "$BASELINE_TEMPLATE" "$LEANKG_TEMPLATE" || exit 1

echo "=============================================="
echo "LeanKG A/B Benchmark (Kilo CLI)"
echo "=============================================="

cd "${WORKTREE_DIR}"

echo "[Setup] Verify LeanKG is indexed..."
cargo run --quiet -- status 2>/dev/null | grep "Elements:"
echo ""

switch_config() {
    local with_leankg="$1"
    if [ "$with_leankg" = "true" ]; then
        cp "${KILO_WORKTREE_DIR}/mcp_settings_with_leankg.json" "$KILO_CONFIG"
        pkill -f "leankg.*mcp-stdio" 2>/dev/null || true
        sleep 1
    else
        cp "${KILO_WORKTREE_DIR}/mcp_settings_without_leankg.json" "$KILO_CONFIG"
        pkill -f "leankg.*mcp-stdio" 2>/dev/null || true
        sleep 1
    fi
}

get_total_tokens() {
    local output="$1"
    echo "$output" | grep -o '"total":[0-9]*' | tail -1 | cut -d: -f2
}

# run_query <query> <arm> <prefix> <task_id> <trial-n> — one arm of one
# trial; records the pinned row (refusal aborts the whole run).
run_query() {
    local query="$1" arm="$2" prompt_prefix="$3" task_id="$4" n="$5"

    switch_config "$arm"

    local temp output tokens
    temp=$(mktemp /tmp/kilo_final_${arm}_XXXXXX.json)
    output=$(timeout 180 kilo run --auto --format json --dir "${WORKTREE_DIR}" "${prompt_prefix} ${query}" 2>&1)
    tokens=$(get_total_tokens "$output")
    printf '%s' "$output" > "$temp"
    printf '%s' "$output" | head -c 8000 > "${temp}.answer.txt"

    ab_record_trial "$TRIALS_JSONL" "$arm" "$task_id" "$n" "${tokens:-0}" "$query" "${temp}.answer.txt" "$output"
    echo "${tokens:-0}"
}

ab_init_trials "$TRIALS_JSONL"
echo "task_id,arm,trial,baseline_tokens,leankg_tokens,savings,savings_pct" > "${RESULTS_DIR}/kilo_ab_final.csv"

QUERY_TASKS=(
    "ab-query-handler:Find the MCP handler implementation"
    "ab-code-element:Where is the CodeElement struct defined"
    "ab-dependency-graph:How does LeanKG build the dependency graph"
    "ab-context-retrieval:How does LeanKG retrieve context for a file"
)

echo "Running $((${#QUERY_TASKS[@]} / 2)) queries x ${AB_TRIALS_PER_ARM} trials/arm..."
echo ""

for entry in "${QUERY_TASKS[@]}"; do
    task_id="${entry%%:*}"
    query="${entry##*:}"

    echo "Query: ${query}"

    BASE=()
    LK=()
    for n in $(seq 1 "$AB_TRIALS_PER_ARM"); do
        echo "  [Baseline/${n}]..."
        BASE+=("$(run_query "${query}" baseline "Answer about the LeanKG codebase" "$task_id" "$n")")
        echo "  [LeanKG/${n}]..."
        LK+=("$(run_query "${query}" leankg "Use LeanKG MCP tools to answer about the LeanKG codebase" "$task_id" "$n")")
    done

    BM=$(ab_median "${BASE[@]}")
    LM=$(ab_median "${LK[@]}")
    if [ -n "$BM" ] && [ "$BM" -gt 0 ] 2>/dev/null; then
        BMi=$(echo "$BM" | cut -d. -f1); LMi=$(echo "$LM" | cut -d. -f1)
        savings=$((BMi - LMi))
        savings_pct=$(( (savings * 100) / BMi ))
        echo "  Median: Baseline=${BM}, LeanKG=${LM}, Savings=${savings_pct}%"
        echo "${task_id},medians,${AB_TRIALS_PER_ARM},${BM},${LM},${savings},${savings_pct}" >> "${RESULTS_DIR}/kilo_ab_final.csv"
    else
        echo "  ERROR: tokens=${BASE[*]}/${LK[*]}"
    fi
    for n in $(seq 1 "$AB_TRIALS_PER_ARM"); do
        echo "${task_id},baseline,${n},${BASE[$((n-1))]:-0},,," >> "${RESULTS_DIR}/kilo_ab_final.csv"
        echo "${task_id},leankg,${n},,${LK[$((n-1))]:-0},," >> "${RESULTS_DIR}/kilo_ab_final.csv"
    done
    echo ""
done

echo "[Score] Aggregate + judge-blind + zg pitfalls checklist (>=3 trials/arm enforced; refused below floor)"
if ab_score "$TRIALS_JSONL" "$REPORT_JSON"; then
    echo "  Results JSON: ${REPORT_JSON}"
else
    echo "  SCORER REFUSED the run — per-arm medians + checklist withheld."
    exit 1
fi

echo "=============================================="
echo "Trials (pinned JSONL): ${TRIALS_JSONL}"
echo "Results JSON:         ${REPORT_JSON}"
echo "Results saved to:      ${RESULTS_DIR}/kilo_ab_final.csv"
