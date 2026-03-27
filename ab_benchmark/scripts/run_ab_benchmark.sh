#!/bin/bash
# LeanKG A/B Testing Benchmark using Kilo CLI
# This script compares LeanKG MCP context retrieval against baseline file reading
#
# Usage: ./run_ab_benchmark.sh
#
# Method A (Baseline): Standard grep + file reading (simulates non-LeanKG AI tools)
# Method B (LeanKG):    MCP tool queries via kilo CLI

set -e

WORKTREE_DIR="/Users/linh.doan/work/harvey/freepeak/.worktree/leankg-ab-benchmark"
PROMPTS_DIR="${WORKTREE_DIR}/ab_benchmark/prompts"
RESULTS_DIR="${WORKTREE_DIR}/ab_benchmark/results"
SCRIPTS_DIR="${WORKTREE_DIR}/ab_benchmark/scripts"

KILO_CONFIG_DIR="$HOME/.config/kilo"

mkdir -p "${RESULTS_DIR}"

echo "=============================================="
echo "LeanKG A/B Testing Benchmark"
echo "Comparing: LeanKG MCP vs Baseline File Read"
echo "=============================================="
echo ""

cd "${WORKTREE_DIR}"

echo "[Setup 1/3] Ensuring LeanKG is indexed..."
ELEMENTS=$(cargo run --quiet -- status 2>/dev/null | grep "Elements:" | awk '{print $2}')
if [ -z "$ELEMENTS" ] || [ "$ELEMENTS" -eq 0 ]; then
    echo "  Indexing codebase..."
    cargo run --quiet -- index ./src 2>/dev/null
fi
echo "  LeanKG ready with ${ELEMENTS:-0} elements"
echo ""

echo "[Setup 2/3] Loading test queries from ${PROMPTS_DIR}/queries.yaml..."
if [ ! -f "${PROMPTS_DIR}/queries.yaml" ]; then
    echo "ERROR: Prompts file not found at ${PROMPTS_DIR}/queries.yaml"
    exit 1
fi

TASK_COUNT=$(grep -c "^  - id:" "${PROMPTS_DIR}/queries.yaml" || echo "0")
echo "  Found ${TASK_COUNT} test queries"
echo ""

echo "[Setup 3/3] Preparing kilo MCP configuration..."
if [ ! -f "${KILO_CONFIG_DIR}/mcp_settings_with_leankg.json" ]; then
    echo "  WARNING: mcp_settings_with_leankg.json not found"
fi
if [ ! -f "${KILO_CONFIG_DIR}/mcp_settings_without_leankg.json" ]; then
    echo "  WARNING: mcp_settings_without_leankg.json not found"
fi
echo ""

echo "=============================================="
echo "Running A/B Comparison"
echo "=============================================="
echo ""

initialize_results() {
    echo "task_id|query_tokens|leankg_tokens|baseline_tokens|savings|savings_pct|time_ms" > "${RESULTS_DIR}/ab_benchmark_results.csv"
    echo "# LeanKG A/B Testing Results" > "${RESULTS_DIR}/ab_benchmark_summary.md"
    echo "" >> "${RESULTS_DIR}/ab_benchmark_summary.md"
    echo "Generated: $(date)" >> "${RESULTS_DIR}/ab_benchmark_summary.md"
    echo "" >> "${RESULTS_DIR}/ab_benchmark_summary.md"
}

run_baseline_method() {
    local query="$1"
    local context=""
    
    echo "=== BASELINE: grep-based file search ==="
    for term in $query; do
        if [ ${#term} -gt 2 ]; then
            results=$(grep -rli "$term" src/ 2>/dev/null | head -5 || true)
            for file in $results; do
                if [ -f "$file" ]; then
                    echo "--- File: $file ---"
                    head -100 "$file"
                fi
            done
        fi
    done
}

run_leankg_method() {
    local query="$1"
    local context=""
    
    echo "=== LEANKG: MCP tool query ==="
    cargo run --quiet -- query "$query" --kind pattern 2>/dev/null || true
}

count_tokens_py() {
    python3 -c "
import sys
import tiktoken
try:
    enc = tiktoken.get_encoding('cl100k_base')
    text = sys.stdin.read()
    print(len(enc.encode(text)))
except:
    words = len(text.split())
    print(int(words * 1.3))
"
}

run_single_query() {
    local task_id="$1"
    local query="$2"
    local task_num="$3"
    local total_tasks="$4"
    
    echo ""
    echo "--- [${task_num}/${total_tasks}] Task: ${task_id} ---"
    echo "Query: ${query}"
    
    TEMP_BASELINE=$(mktemp)
    TEMP_LEANKG=$(mktemp)
    
    echo "Running baseline method..."
    run_baseline_method "$query" > "$TEMP_BASELINE" 2>&1
    
    echo "Running LeanKG method..."
    run_leankg_method "$query" > "$TEMP_LEANKG" 2>&1
    
    BASELINE_TOKENS=$(count_tokens_py < "$TEMP_BASELINE")
    LEANKG_TOKENS=$(count_tokens_py < "$TEMP_LEANKG")
    
    if [ "$BASELINE_TOKENS" -gt 0 ]; then
        SAVINGS=$((BASELINE_TOKENS - LEANKG_TOKENS))
        SAVINGS_PCT=$(( (SAVINGS * 100) / BASELINE_TOKENS ))
    else
        SAVINGS=0
        SAVINGS_PCT=0
    fi
    
    echo ""
    echo "Results:"
    echo "  Baseline tokens: ${BASELINE_TOKENS}"
    echo "  LeanKG tokens:   ${LEANKG_TOKENS}"
    echo "  Savings:          ${SAVINGS} tokens (${SAVINGS_PCT}%)"
    
    echo "${task_id}|${query}|${BASELINE_TOKENS}|${LEANKG_TOKENS}|${SAVINGS}|${SAVINGS_PCT}" >> "${RESULTS_DIR}/ab_benchmark_results.csv"
    
    cp "$TEMP_BASELINE" "${RESULTS_DIR}/${task_id}_baseline.txt"
    cp "$TEMP_LEANKG" "${RESULTS_DIR}/${task_id}_leankg.txt"
    
    rm -f "$TEMP_BASELINE" "$TEMP_LEANKG"
}

initialize_results

TOTAL_BASELINE=0
TOTAL_LEANKG=0
TASK_NUM=0

while IFS= read -r line; do
    TASK_NUM=$((TASK_NUM + 1))
    TASK_ID=$(echo "$line" | sed -n 's/^  - id: "\(.*\)"/\1/p')
    QUERY=$(echo "$line" | sed -n 's/^    query: "\(.*\)"/\1/p')
    
    if [ -n "$TASK_ID" ] && [ -n "$QUERY" ]; then
        run_single_query "$TASK_ID" "$QUERY" "$TASK_NUM" "$TASK_COUNT"
    fi
done < "${PROMPTS_DIR}/queries.yaml"

echo ""
echo "=============================================="
echo "Benchmark Complete!"
echo "=============================================="
echo ""
echo "Results saved to: ${RESULTS_DIR}/"
echo ""

python3 "${SCRIPTS_DIR}/generate_report.py" "${RESULTS_DIR}"
