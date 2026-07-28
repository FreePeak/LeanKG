#!/usr/bin/env bash
# run.sh — One-shot benchmark: ensure indexes exist, then run all 3 arms and aggregate.
#
# Usage: run.sh [N] [model]
#   N     = runs per question (default: 3)
#   model = claude model id (default: sonnet)
#
# This is the top-level convenience script. It:
#   1. Builds/verifies LeanKG release binary
#   2. Verifies CodeGraph CLI is installed
#   3. Initializes both indexes on Alamofire
#   4. Runs all 3 arms (leankg, codegraph, none) sequentially
#   5. Aggregates results into Markdown + JSON report

set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
N="${1:-3}"
MODEL="${2:-sonnet}"

LEANKG_BIN="${HERE}/../../target/release/leankg"
CODEGRAPH_BIN="$(command -v codegraph)"
CLAUDE_BIN="$(command -v claude)"
REPO_PATH="${REPO_PATH:-${HERE}/repos/alamofire}"

export LEANKG_BIN CODEGRAPH_BIN CLAUDE_BIN REPO_PATH

echo "=== Alamofire 30Q 3-Way Benchmark ==="
echo "N per question per arm: ${N}"
echo "Model: ${MODEL}"
echo ""

# Pre-flight checks
if [[ ! -x "${LEANKG_BIN}" ]]; then
  echo "ERROR: leankg binary not found at ${LEANKG_BIN}. Build: cargo build --release" >&2
  exit 2
fi
if [[ ! -x "${CODEGRAPH_BIN}" ]]; then
  echo "ERROR: codegraph not found. Install: npm i -g @colbymchenry/codegraph" >&2
  exit 2
fi
if [[ ! -x "${CLAUDE_BIN}" ]]; then
  echo "ERROR: claude CLI not found on PATH" >&2
  exit 2
fi
if [[ ! -d "${REPO_PATH}" ]]; then
  echo "ERROR: Alamofire repo not found at ${REPO_PATH}" >&2
  exit 2
fi

# Step 1: Ensure CodeGraph index
if [[ ! -d "${REPO_PATH}/.codegraph" ]]; then
  echo "--- Building CodeGraph index ---"
  ( cd "${REPO_PATH}" && "${CODEGRAPH_BIN}" init )
fi

# Step 2: Ensure LeanKG index (Swift needs config fix after init: auto-detect misses .swift)
echo "--- Building LeanKG index ---"
rm -rf "${REPO_PATH}/.leankg"
( cd "${REPO_PATH}" && "${LEANKG_BIN}" init )
python3 -c "
import yaml
with open('${REPO_PATH}/leankg.yaml', 'r') as f:
    cfg = yaml.safe_load(f)
cfg['project']['languages'] = ['swift']
cfg['indexer']['include'] = ['*.swift']
cfg['indexer']['exclude'] = ['**/node_modules/**', '**/vendor/**', '**/.build/**', '**/Carthage/**', '**/Example/**', '**/Tests/**', '**/watchOS Example/**', '**/Package@**']
with open('${REPO_PATH}/leankg.yaml', 'w') as f:
    yaml.safe_dump(cfg, f, default_flow_style=False)
print('  Swift config applied')
"
( cd "${REPO_PATH}" && "${LEANKG_BIN}" index . )

# Step 3: Run arms
for arm in leankg codegraph none; do
  echo ""
  echo "===== ARM: ${arm} ====="
  bash "${HERE}/run_30q.sh" "${arm}" "${N}" "${MODEL}"
done

# Step 4: Aggregate
echo ""
echo "===== Aggregating Results ====="
python3 "${HERE}/aggregate.py" --results "${HERE}/results" --questions "${HERE}/questions.yaml"

echo ""
echo "=== Done ==="
echo "Report: $(ls "${HERE}/results"/alamofire-30q-*.md 2>/dev/null || echo 'no report found')"
