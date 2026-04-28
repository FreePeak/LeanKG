#!/bin/bash
# LeanKG A/B Testing Benchmark using Kilo CLI with MCP
# This properly tests LeanKG MCP context retrieval vs baseline AI context
#
# Usage: ./run_kilo_ab_test.sh

set -e

WORKTREE_DIR="/Users/linh.doan/work/harvey/freepeak/.worktree/leankg-ab-benchmark"
PROMPTS_FILE="${WORKTREE_DIR}/ab_benchmark/prompts/queries.yaml"
RESULTS_DIR="${WORKTREE_DIR}/ab_benchmark/results"
KILO_CONFIG_DIR="$HOME/.config/kilo"
KILO_WORKTREE_DIR="$HOME/.config/kilo/worktree"

KILO_MCP_SETTINGS="kilo.json"

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
    echo "task_id,baseline_tokens,leankg_tokens,savings,savings_pct,baseline_success,leankg_success" > "${RESULTS_DIR}/kilo_ab_results.csv"
}

run_single_query() {
    local task_id="$1"
    local query="$2"
    local task_num="$3"
    local total="$4"
    
    echo ""
    echo "--- [${task_num}/${total}] ${task_id} ---"
    echo "Query: ${query}"
    
    TEMP_BASELINE=$(mktemp /tmp/kilo_baseline_XXXXXX.json)
    TEMP_LEANKG=$(mktemp /tmp/kilo_leankg_XXXXXX.json)
    
    echo "  [A] Running BASELINE (no LeanKG MCP)..."
    switch_mcp_config false
    kill_leankg_mcp
    
    BASELINE_OUTPUT=$(kilo run --auto --format json --dir "${WORKTREE_DIR}" "Answer this query about the LeanKG codebase: ${query}. Provide file paths and relevant code snippets." 2>&1)
    BASELINE_TOKENS=$(parse_kilo_tokens "$BASELINE_OUTPUT")
    echo "$BASELINE_OUTPUT" > "$TEMP_BASELINE"
    echo "  Baseline tokens: ${BASELINE_TOKENS}"
    
    echo "  [B] Running LEANKG (with LeanKG MCP)..."
    switch_mcp_config true
    kill_leankg_mcp
    
    LEANKG_OUTPUT=$(kilo run --auto --format json --dir "${WORKTREE_DIR}" "Answer this query about the LeanKG codebase: ${query}. Use LeanKG MCP tools first to find relevant code." 2>&1)
    LEANKG_TOKENS=$(parse_kilo_tokens "$LEANKG_OUTPUT")
    echo "$LEANKG_OUTPUT" > "$TEMP_LEANKG"
    echo "  LeanKG tokens: ${LEANKG_TOKENS}"
    
    if [ -n "$BASELINE_TOKENS" ] && [ -n "$LEANKG_TOKENS" ] && [ "$BASELINE_TOKENS" -gt 0 ]; then
        SAVINGS=$((BASELINE_TOKENS - LEANKG_TOKENS))
        SAVINGS_PCT=$(( (SAVINGS * 100) / BASELINE_TOKENS ))
    else
        SAVINGS=0
        SAVINGS_PCT=0
    fi
    
    echo "  Savings: ${SAVINGS} tokens (${SAVINGS_PCT}%)"
    
    echo "${task_id},${BASELINE_TOKENS:-0},${LEANKG_TOKENS:-0},${SAVINGS},${SAVINGS_PCT},true,true" >> "${RESULTS_DIR}/kilo_ab_results.csv"
    
    cp "$TEMP_BASELINE" "${RESULTS_DIR}/${task_id}_baseline.json"
    cp "$TEMP_LEANKG" "${RESULTS_DIR}/${task_id}_leankg.json"
    
    rm -f "$TEMP_BASELINE" "$TEMP_LEANKG"
}

init_results

TASK_NUM=0
while IFS= read -r line; do
    TASK_NUM=$((TASK_NUM + 1))
    TASK_ID=$(echo "$line" | sed -n 's/^  - id: "\(.*\)"/\1/p')
    QUERY=$(echo "$line" | sed -n 's/^    query: "\(.*\)"/\1/p')
    
    if [ -n "$TASK_ID" ] && [ -n "$QUERY" ]; then
        run_single_query "$TASK_ID" "$QUERY" "$TASK_NUM" "$TASK_COUNT"
    fi
done < "${PROMPTS_FILE}"

echo ""
echo "=============================================="
echo "Benchmark Complete!"
echo "=============================================="
echo ""
echo "Results: ${RESULTS_DIR}/kilo_ab_results.csv"
