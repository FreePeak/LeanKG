#!/bin/bash
# LeanKG A/B Testing Benchmark via Kilo CLI with MCP
# This is the CORRECT way to test - using kilo CLI to measure actual AI token consumption

set -e

WORKTREE_DIR="/Users/linh.doan/work/harvey/freepeak/.worktree/leankg-ab-benchmark"
PROMPTS_FILE="${WORKTREE_DIR}/ab_benchmark/prompts/queries.yaml"
RESULTS_DIR="${WORKTREE_DIR}/ab_benchmark/results"
KILO_CONFIG="$HOME/.config/kilo/kilo.json"
KILO_WORKTREE_DIR="$HOME/.config/kilo/worktree"

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

run_query() {
    local query="$1"
    local with_leankg="$2"
    local prompt_prefix="$3"
    
    switch_config "$with_leankg"
    
    output=$(timeout 180 kilo run --auto --format json --dir "${WORKTREE_DIR}" "${prompt_prefix} ${query}" 2>&1)
    tokens=$(get_total_tokens "$output")
    echo "${tokens:-0}"
}

init_results() {
    echo "task_id,baseline_tokens,leankg_tokens,savings,savings_pct" > "${RESULTS_DIR}/kilo_ab_final.csv"
}

QUERY_TASKS=(
    "ab-query-handler:Find the MCP handler implementation"
    "ab-code-element:Where is the CodeElement struct defined"
    "ab-dependency-graph:How does LeanKG build the dependency graph"
    "ab-context-retrieval:How does LeanKG retrieve context for a file"
)

init_results

echo "Running $((${#QUERY_TASKS[@]} / 2)) queries..."
echo ""

for entry in "${QUERY_TASKS[@]}"; do
    task_id="${entry%%:*}"
    query="${entry##*:}"
    
    echo "Query: ${query}"
    
    echo "  [Baseline]..."
    baseline_tokens=$(run_query "${query}" false "Answer about the LeanKG codebase")
    
    echo "  [LeanKG]..."
    leankg_tokens=$(run_query "${query}" true "Use LeanKG MCP tools to answer about the LeanKG codebase")
    
    if [ -n "$baseline_tokens" ] && [ -n "$leankg_tokens" ] && [ "$baseline_tokens" -gt 0 ]; then
        savings=$((baseline_tokens - leankg_tokens))
        savings_pct=$(( (savings * 100) / baseline_tokens ))
        echo "  Result: Baseline=${baseline_tokens}, LeanKG=${leankg_tokens}, Savings=${savings_pct}%"
        echo "${task_id},${baseline_tokens},${leankg_tokens},${savings},${savings_pct}" >> "${RESULTS_DIR}/kilo_ab_final.csv"
    else
        echo "  ERROR: tokens=${baseline_tokens}/${leankg_tokens}"
    fi
    echo ""
done

echo "=============================================="
echo "Results saved to: ${RESULTS_DIR}/kilo_ab_final.csv"
