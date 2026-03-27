#!/bin/bash
set -e

WORKTREE_DIR="/Users/linh.doan/work/harvey/freepeak/.worktree/leankg-ab-benchmark"
PROMPTS_DIR="${WORKTREE_DIR}/ab_benchmark/prompts"
RESULTS_DIR="${WORKTREE_DIR}/ab_benchmark/results"
SCRIPTS_DIR="${WORKTREE_DIR}/ab_benchmark/scripts"

mkdir -p "${RESULTS_DIR}"

echo "=== LeanKG A/B Testing Benchmark ==="
echo "======================================"
echo ""

echo "Step 1: Indexing codebase with LeanKG (if needed)..."
cd "${WORKTREE_DIR}"
if [ ! -f ".leankg/leankg.db" ]; then
    echo "LeanKG database not found, initializing..."
    cargo run --quiet -- init 2>/dev/null || true
fi

cargo run --quiet -- index ./src 2>/dev/null || true
echo "Indexing complete."
echo ""

echo "Step 2: Loading test queries..."
QUERIES=$(cat "${PROMPTS_DIR}/queries.yaml")
QUERY_COUNT=$(echo "${QUERIES}" | grep -c "id:" || true)
echo "Found ${QUERY_COUNT} test queries"
echo ""

echo "Step 3: Running A/B comparison..."
echo "-----------------------------------"

TOTAL_METHOD_A_TOKENS=0
TOTAL_METHOD_B_TOKENS=0
TOKEN_SAVINGS=0

for i in $(seq 1 ${QUERY_COUNT}); do
    TASK_LINE=$(echo "${QUERIES}" | grep -n "id:" | sed -n "${i}p")
    TASK_NUM=$(echo "${TASK_LINE}" | cut -d: -f1)
    TASK_ID=$(echo "${TASK_LINE}" | grep -oP '(?<=id: ").*(?=")')"
    
    QUERY=$(echo "${QUERIES}" | awk -v task_start="${TASK_LINE}" 'NR>=task_start {if (match($0, /^  - id:/)) exit; if (match($0, /^  - query:/)) {gsub(/^  - query: /, ""); gsub(/"/, ""); print}}')
    
    echo ""
    echo "[${i}/${QUERY_COUNT}] Task: ${TASK_ID}"
    echo "Query: ${QUERY}"
    
    echo "  Method A (Baseline - Full File Read)..."
    METHOD_A_START=$(date +%s%3N)
    METHOD_A_CONTEXT=$(bash "${SCRIPTS_DIR}/method_a_baseline.sh" "${QUERY}" 2>/dev/null || echo "")
    METHOD_A_END=$(date +%s%3N)
    METHOD_A_TIME=$((METHOD_A_END - METHOD_A_START))
    METHOD_A_TOKENS=$(python3 "${SCRIPTS_DIR}/count_tokens.py" "${METHOD_A_CONTEXT}" 2>/dev/null || echo "0")
    
    echo "  Method B (LeanKG - Targeted Subgraph)..."
    METHOD_B_START=$(date +%s%3N)
    METHOD_B_CONTEXT=$(bash "${SCRIPTS_DIR}/method_b_leankg.sh" "${QUERY}" 2>/dev/null || echo "")
    METHOD_B_END=$(date +%s%3N)
    METHOD_B_TIME=$((METHOD_B_END - METHOD_B_START))
    METHOD_B_TOKENS=$(python3 "${SCRIPTS_DIR}/count_tokens.py" "${METHOD_B_CONTEXT}" 2>/dev/null || echo "0")
    
    SAVINGS=$((METHOD_A_TOKENS - METHOD_B_TOKENS))
    SAVINGS_PCT=0
    if [ ${METHOD_A_TOKENS} -gt 0 ]; then
        SAVINGS_PCT=$(( (SAVINGS * 100) / METHOD_A_TOKENS ))
    fi
    
    echo "  Results:"
    echo "    Method A: ${METHOD_A_TOKENS} tokens (${METHOD_A_TIME}ms)"
    echo "    Method B: ${METHOD_B_TOKENS} tokens (${METHOD_B_TIME}ms)"
    echo "    Savings: ${SAVINGS} tokens (${SAVINGS_PCT}%)"
    
    echo "${TASK_ID}|${QUERY}|${METHOD_A_TOKENS}|${METHOD_B_TOKENS}|${SAVINGS}|${SAVINGS_PCT}|${METHOD_A_TIME}|${METHOD_B_TIME}" >> "${RESULTS_DIR}/benchmark_raw.tsv"
    
    TOTAL_METHOD_A_TOKENS=$((TOTAL_METHOD_A_TOKENS + METHOD_A_TOKENS))
    TOTAL_METHOD_B_TOKENS=$((TOTAL_METHOD_B_TOKENS + METHOD_B_TOKENS))
    TOKEN_SAVINGS=$((TOKEN_SAVINGS + SAVINGS))
    
    echo "${METHOD_A_CONTEXT}" > "${RESULTS_DIR}/${TASK_ID}_method_a.txt"
    echo "${METHOD_B_CONTEXT}" > "${RESULTS_DIR}/${TASK_ID}_method_b.txt"
done

echo ""
echo "======================================"
echo "Benchmark Complete!"
echo "-----------------------------------"
echo "Total Method A Tokens: ${TOTAL_METHOD_A_TOKENS}"
echo "Total Method B Tokens: ${TOTAL_METHOD_B_TOKENS}"
OVERALL_SAVINGS=$(( (TOKEN_SAVINGS * 100) / TOTAL_METHOD_A_TOKENS ))
echo "Overall Token Savings: ${TOKEN_SAVINGS} tokens (${OVERALL_SAVINGS}%)"
echo "======================================"

python3 "${SCRIPTS_DIR}/generate_report.py" "${RESULTS_DIR}"
