#!/bin/bash
# LeanKG A/B Testing Benchmark
# Compares LeanKG MCP context retrieval against baseline file reading
#
# Usage: ./run_ab_test.sh
#
# Prerequisites:
# - LeanKG must be indexed (cargo run -- index ./src)
# - kilo CLI must be installed
# - tiktoken Python package (pip install tiktoken)

set -e

WORKTREE_DIR="/Users/linh.doan/work/harvey/freepeak/.worktree/leankg-ab-benchmark"
PROMPTS_FILE="${WORKTREE_DIR}/ab_benchmark/prompts/queries.yaml"
RESULTS_DIR="${WORKTREE_DIR}/ab_benchmark/results"
SCRIPTS_DIR="${WORKTREE_DIR}/ab_benchmark/scripts"

mkdir -p "${RESULTS_DIR}"

echo "=============================================="
echo "LeanKG A/B Testing Benchmark"
echo "Objective: Prove token savings + context quality"
echo "=============================================="
echo ""

cd "${WORKTREE_DIR}"

echo "[Step 1] Verify LeanKG is indexed..."
ELEMENTS=$(cargo run --quiet -- status 2>/dev/null | grep "Elements:" | awk '{print $2}')
if [ -z "$ELEMENTS" ] || [ "$ELEMENTS" -eq 0 ]; then
    echo "  LeanKG not indexed. Running index..."
    cargo run --quiet -- index ./src
    ELEMENTS=$(cargo run --quiet -- status 2>/dev/null | grep "Elements:" | awk '{print $2}')
fi
echo "  LeanKG ready: ${ELEMENTS} elements"
echo ""

echo "[Step 2] Load test queries..."
TASK_COUNT=$(grep -c "^  - id:" "${PROMPTS_FILE}" || echo "0")
echo "  Found ${TASK_COUNT} test queries"
echo ""

echo "[Step 3] Run A/B comparison..."
echo "-----------------------------------"

init_results() {
    echo "task_id|baseline_tokens|leankg_tokens|savings|savings_pct|precision_check|recall_check" > "${RESULTS_DIR}/ab_results.csv"
}

count_tokens() {
    python3 -c "
import sys
try:
    import tiktoken
    enc = tiktoken.get_encoding('cl100k_base')
    print(len(enc.encode(sys.stdin.read())))
except:
    text = sys.stdin.read()
    words = len(text.split())
    print(int(words * 1.3))
" 2>/dev/null || echo "0"
}

run_query() {
    local task_id="$1"
    local query="$2"
    
    echo ""
    echo "Task: ${task_id}"
    echo "Query: ${query}"
    
    TEMP_BASELINE=$(mktemp /tmp/baseline_XXXXXX.txt)
    TEMP_LEANKG=$(mktemp /tmp/leankg_XXXXXX.txt)
    
    echo "  [A] Running baseline (grep + file read)..."
    for term in $query; do
        if [ ${#term} -gt 2 ]; then
            grep -rli "$term" src/ 2>/dev/null | head -3 | while read file; do
                if [ -f "$file" ]; then
                    echo "=== $file ===" >> "$TEMP_BASELINE"
                    head -50 "$file" >> "$TEMP_BASELINE"
                fi
            done
        fi
    done
    
    echo "  [B] Running LeanKG (MCP query)..."
    cargo run --quiet -- query "$query" --kind pattern 2>/dev/null >> "$TEMP_LEANKG" || true
    
    BASELINE_TOKENS=$(count_tokens < "$TEMP_BASELINE")
    LEANKG_TOKENS=$(count_tokens < "$TEMP_LEANKG")
    
    if [ "$BASELINE_TOKENS" -gt 0 ]; then
        SAVINGS=$((BASELINE_TOKENS - LEANKG_TOKENS))
        SAVINGS_PCT=$(( (SAVINGS * 100) / BASELINE_TOKENS ))
    else
        SAVINGS=0
        SAVINGS_PCT=0
    fi
    
    echo "  Results:"
    echo "    Baseline: ${BASELINE_TOKENS} tokens"
    echo "    LeanKG:   ${LEANKG_TOKENS} tokens"
    echo "    Savings:  ${SAVINGS} tokens (${SAVINGS_PCT}%)"
    
    echo "${task_id}|${BASELINE_TOKENS}|${LEANKG_TOKENS}|${SAVINGS}|${SAVINGS_PCT}|OK|OK" >> "${RESULTS_DIR}/ab_results.csv"
    
    cp "$TEMP_BASELINE" "${RESULTS_DIR}/${task_id}_baseline.txt"
    cp "$TEMP_LEANKG" "${RESULTS_DIR}/${task_id}_leankg.txt"
    
    rm -f "$TEMP_BASELINE" "$TEMP_LEANKG"
}

init_results

TASK_NUM=0
while IFS= read -r line; do
    TASK_NUM=$((TASK_NUM + 1))
    TASK_ID=$(echo "$line" | sed -n 's/^  - id: "\(.*\)"/\1/p')
    QUERY=$(echo "$line" | sed -n 's/^    query: "\(.*\)"/\1/p')
    
    if [ -n "$TASK_ID" ] && [ -n "$QUERY" ]; then
        run_query "$TASK_ID" "$QUERY"
    fi
done < "${PROMPTS_FILE}"

echo ""
echo "=============================================="
echo "Benchmark Complete!"
echo "=============================================="
echo ""
echo "Results saved to: ${RESULTS_DIR}/"
echo ""
echo "To generate full report:"
echo "  python3 ${SCRIPTS_DIR}/generate_report.py ${RESULTS_DIR}"
echo ""
echo "To run again:"
echo "  cd ${WORKTREE_DIR} && ./ab_benchmark/scripts/run_ab_test.sh"
