#!/bin/bash
# LeanKG A/B Testing Benchmark via Kilo CLI with MCP
# This properly tests LeanKG MCP context retrieval vs baseline AI context
#
# Usage: ./run_kilo_ab_test.sh   (AB_TRIALS_PER_ARM defaults to 3, the
# FR-ZCP-08 floor; runs below the floor are refused by the scorer)
#
# FR-ZCP-08 hardening (issue #276): every trial is recorded via
# scripts/kilo_ab_common.sh + go/benchmark/ab/abrun with pinned corpus /
# tool SHAs and per-arm prompt-template hashes; a run whose pins cannot
# be resolved is refused, never recorded. The scorer enforces >=3
# trials/arm, aggregates per-arm medians, and emits the results JSON
# (provenance stamp + zg pitfalls checklist).

set -e

WORKTREE_DIR="/Users/linh.doan/work/harvey/freepeak/.worktree/leankg-ab-benchmark"
PROMPTS_FILE="${WORKTREE_DIR}/ab_benchmark/prompts/queries.yaml"
RESULTS_DIR="${WORKTREE_DIR}/ab_benchmark/results"
KILO_CONFIG_DIR="$HOME/.config/kilo"
KILO_WORKTREE_DIR="$HOME/.config/kilo/worktree"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

KILO_MCP_SETTINGS="kilo.json"

TRIALS_JSONL="${RESULTS_DIR}/ab_trials.jsonl"
REPORT_JSON="${RESULTS_DIR}/ab_report.json"

# shellcheck source=kilo_ab_common.sh
source "${REPO_ROOT}/scripts/kilo_ab_common.sh"

BASELINE_TEMPLATE="Answer this query about the LeanKG codebase: {query}. Provide file paths and relevant code snippets."
LEANKG_TEMPLATE="Answer this query about the LeanKG codebase: {query}. Use LeanKG MCP tools first to find relevant code."

# Pin or refuse — before any measurement runs.
ab_resolve_pins "kilo-ab-test-v1" "$BASELINE_TEMPLATE" "$LEANKG_TEMPLATE" || exit 1

echo "=============================================="
echo "LeanKG A/B Testing Benchmark (via Kilo CLI)"
echo "=============================================="
echo ""

cd "${WORKTREE_DIR}"

echo "[Step 1] Verify LeanKG is indexed..."
ELEMENTS=$(cargo run --quiet -- status 2>/dev/null | grep "Elements:" | awk '{print $2}')
if [ -z "$ELEMENTS" ] || [ "$ELEMENTS" -eq 0 ]; then
    echo "  Indexing codebase..."
    cargo run --quiet -- index ./src
    ELEMENTS=$(cargo run --quiet -- status 2>/dev/null | grep "Elements:" | awk '{print $2}')
fi
echo "  LeanKG ready: ${ELEMENTS} elements"
echo ""

echo "[Step 2] Load test queries..."
TASK_COUNT=$(grep -c "^  - id:" "${PROMPTS_FILE}" || echo "0")
echo "  Found ${TASK_COUNT} test queries"
echo ""

echo "[Step 3] Set up Kilo MCP configuration..."
if [ ! -f "${KILO_WORKTREE_DIR}/mcp_settings_with_leankg.json" ]; then
    echo "  ERROR: MCP config not found"
    exit 1
fi
echo "  Using worktree MCP config: ${KILO_WORKTREE_DIR}"
echo ""

switch_mcp_config() {
    local with_leankg="$1"
    if [ "$with_leankg" = "true" ]; then
        cp "${KILO_WORKTREE_DIR}/mcp_settings_with_leankg.json" "${KILO_CONFIG_DIR}/${KILO_MCP_SETTINGS}"
        echo "  Switched TO LeanKG MCP"
    else
        cp "${KILO_WORKTREE_DIR}/mcp_settings_without_leankg.json" "${KILO_CONFIG_DIR}/${KILO_MCP_SETTINGS}"
        echo "  Switched TO Baseline (no LeanKG)"
    fi
}

kill_leankg_mcp() {
    pkill -f "leankg.*mcp-stdio" 2>/dev/null || true
    sleep 1
}

parse_kilo_tokens() {
    local output="$1"
    echo "$output" | grep -o '"total":[0-9]*' | head -1 | cut -d: -f2 || echo "0"
}

echo "=============================================="
echo "Running Kilo A/B Comparison"
echo "=============================================="
echo ""

init_results() {
    ab_init_trials "$TRIALS_JSONL"
    echo "task_id,arm,trial,baseline_tokens,leankg_tokens,savings,savings_pct,baseline_success,leankg_success" > "${RESULTS_DIR}/kilo_ab_results.csv"
}

# run_one_trial <task_id> <query> <arm> <trial-n>
# Runs one arm of one trial: switches config, runs kilo, records the
# pinned JSONL row via abrun (which refuses unpinned rows — aborting the
# run per FR-ZCP-08), and returns the parsed token count.
run_one_trial() {
    local task_id="$1" query="$2" arm="$3" n="$4"
    local prefix
    if [ "$arm" = "leankg" ]; then
        prefix="Use LeanKG MCP tools to answer about the LeanKG codebase"
        switch_mcp_config true
    else
        prefix="Answer about the LeanKG codebase"
        switch_mcp_config false
    fi
    kill_leankg_mcp

    local temp answer_file
    temp=$(mktemp /tmp/kilo_${arm}_XXXXXX.json)
    answer_file="${temp}.answer.txt"

    local output tokens
    output=$(timeout 180 kilo run --auto --format json --dir "${WORKTREE_DIR}" "${prefix} ${query}" 2>&1)
    tokens=$(parse_kilo_tokens "$output")
    printf '%s' "$output" > "$temp"
    # Persist the answer text for judge-blind scoring (parity with the
    # cross_tool harness claude.json.answer.txt): prefer the envelope's
    # text/result/answer field, fall back to the raw head.
    printf '%s' "$output" | AB_ANSWER_OUT="$answer_file" python3 -c 'import json,os,sys
raw = sys.stdin.read()
text = raw[:8000]
try:
    doc = json.loads(raw)
    for key in ("result", "text", "answer"):
        v = doc.get(key) if isinstance(doc, dict) else None
        if isinstance(v, str) and v.strip():
            text = v[:8000]
            break
except Exception:
    pass
open(os.environ["AB_ANSWER_OUT"], "w", encoding="utf-8").write(text)
' || printf '%s' "$output" | head -c 8000 > "$answer_file"

    cp "$temp" "${RESULTS_DIR}/${task_id}_${arm}_run${n}.json"
    ab_record_trial "$TRIALS_JSONL" "$arm" "$task_id" "$n" "${tokens:-0}" "$query" "$answer_file" "$output"
    cp "$answer_file" "${RESULTS_DIR}/${task_id}_${arm}_run${n}.answer.txt"
    rm -f "$temp" "$answer_file"
    echo "${tokens:-0}"
}

init_results

TASK_NUM=0
while IFS= read -r line; do
    TASK_NUM=$((TASK_NUM + 1))
    TASK_ID=$(echo "$line" | sed -n 's/^  - id: "\(.*\)"/\1/p')
    QUERY=$(echo "$line" | sed -n 's/^    query: "\(.*\)"/\1/p')

    if [ -n "$TASK_ID" ] && [ -n "$QUERY" ]; then
        echo ""
        echo "--- [${TASK_NUM}/${TASK_COUNT}] ${TASK_ID} (${AB_TRIALS_PER_ARM} trials/arm) ---"
        echo "Query: ${QUERY}"

        BASE_TOKENS=()
        LK_TOKENS=()
        for n in $(seq 1 "$AB_TRIALS_PER_ARM"); do
            echo "  [A/${n}] BASELINE..."
            b=$(run_one_trial "$TASK_ID" "$QUERY" baseline "$n")
            BASE_TOKENS+=("$b")
            echo "  [B/${n}] LEANKG..."
            l=$(run_one_trial "$TASK_ID" "$QUERY" leankg "$n")
            LK_TOKENS+=("$l")
        done

        BM=$(ab_median "${BASE_TOKENS[@]}")
        LM=$(ab_median "${LK_TOKENS[@]}")
        if [ -n "$BM" ] && [ "$BM" -gt 0 ] 2>/dev/null; then
            SAVINGS=$(( $(echo "$BM" | cut -d. -f1) - $(echo "$LM" | cut -d. -f1) ))
            SAVINGS_PCT=$(( (SAVINGS * 100) / $(echo "$BM" | cut -d. -f1) ))
            echo "  Median result: Baseline=${BM}, LeanKG=${LM}, Savings=${SAVINGS_PCT}%"
        else
            echo "  ERROR: no measurable baseline tokens"
        fi
        for n in $(seq 1 "$AB_TRIALS_PER_ARM"); do
            echo "${TASK_ID},baseline,${n},${BASE_TOKENS[$((n-1))]:-0},,,," >> "${RESULTS_DIR}/kilo_ab_results.csv"
            echo "${TASK_ID},leankg,${n},,${LK_TOKENS[$((n-1))]:-0},,," >> "${RESULTS_DIR}/kilo_ab_results.csv"
        done
        echo "${TASK_ID},medians,${AB_TRIALS_PER_ARM},${BM},${LM},${SAVINGS:-0},${SAVINGS_PCT:-0}" >> "${RESULTS_DIR}/kilo_ab_results.csv"
    fi
done < "${PROMPTS_FILE}"

echo ""
echo "[Score] Aggregate + judge-blind + zg pitfalls checklist (>=3 trials/arm enforced; refused below floor)"
if ab_score "$TRIALS_JSONL" "$REPORT_JSON"; then
    echo "  Results JSON: ${REPORT_JSON}"
else
    echo "  SCORER REFUSED the run (see gates above) — per-arm medians + checklist withheld."
    exit 1
fi

echo ""
echo "=============================================="
echo "Benchmark Complete!"
echo "=============================================="
echo ""
echo "Trials (pinned JSONL): ${TRIALS_JSONL}"
echo "Results JSON:          ${REPORT_JSON}"
echo "Results CSV:           ${RESULTS_DIR}/kilo_ab_results.csv"
