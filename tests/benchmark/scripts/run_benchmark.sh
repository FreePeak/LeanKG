#!/bin/bash
set -e

BENCHMARK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RESULTS_DIR="$BENCHMARK_DIR/results/$(date +%Y-%m-%d)"
QUERIES_FILE="$BENCHMARK_DIR/prompts/queries.yaml"

mkdir -p "$RESULTS_DIR"

echo "LeanKG Benchmark Runner"
echo "======================="
echo "Results directory: $RESULTS_DIR"
echo ""
echo "To run E2E tests with Kilo:"
echo "1. Ensure LeanKG is indexed: cargo run -- index ./src"
echo "2. Start Kilo: kilo"
echo "3. Run queries and export sessions: kilo export <session_id> > $RESULTS_DIR/<query_id>.json"
echo ""
echo "Then compare results: python3 $BENCHMARK_DIR/scripts/compare_results.py $RESULTS_DIR"
