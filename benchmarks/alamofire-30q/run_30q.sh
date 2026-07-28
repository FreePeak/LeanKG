#!/usr/bin/env bash
# run_30q.sh — Run a single arm across 30 questions, N times each.
#
# Usage: run_30q.sh <arm> <N> <model>
#   arm   = leankg | codegraph | none
#   N     = number of runs per question (e.g. 3)
#   model = claude model id (sonnet, opus, etc.) — optional; empty = default
#
# Env:
#   LEANKG_BIN           absolute path to leankg binary
#   CODEGRAPH_BIN        absolute path to codegraph binary
#   REPO_PATH            absolute path to Alamofire clone
#   BENCH_DIR            absolute path to this benchmark directory
#   RESULTS_DIR          absolute path to results root
#   DRY_RUN=1            print what would run instead of invoking claude

set -euo pipefail

ARM="${1:?arm required (leankg|codegraph|none)}"
N="${2:?N required}"
MODEL="${3:-}"

HERE="$(cd "$(dirname "$0")" && pwd)"
BENCH_DIR="${BENCH_DIR:-${HERE}}"
REPO_PATH="${REPO_PATH:-${HERE}/repos/alamofire}"
RESULTS_DIR="${RESULTS_DIR:-${BENCH_DIR}/results}"
LEANKG_BIN="${LEANKG_BIN:-${HERE}/../../target/release/leankg}"
CODEGRAPH_BIN="${CODEGRAPH_BIN:-$(command -v codegraph || true)}"
CLAUDE_BIN="${CLAUDE_BIN:-$(command -v claude || true)}"
DRY_RUN="${DRY_RUN:-0}"

# Python3 with PyYAML required for question parsing
PYTHON="${PYTHON:-python3}"

export LEANKG_BIN CODEGRAPH_BIN RESULTS_DIR REPO_PATH BENCH_DIR
export REPO_NAME="${REPO_NAME:-${REPO_PATH##*/}}"
export MCP_SMOKE_CHECK="${MCP_SMOKE_CHECK:-0}"

if [[ -z "${CLAUDE_BIN}" ]]; then
  echo "ERROR: claude CLI not found on PATH" >&2
  exit 2
fi

DATE="$(date +%Y-%m-%d)"
if [[ "${QUESTIONS:-}" == /* ]]; then
  # Already an absolute path (e.g. passed by phase-h.sh)
  QUESTIONS_YAML="${QUESTIONS}"
else
  QUESTIONS_YAML="${BENCH_DIR}/${QUESTIONS:-questions.yaml}"
fi
ARM_OUTPUT_DIR="${RESULTS_DIR}/runs/${DATE}/${ARM}"
mkdir -p "${ARM_OUTPUT_DIR}"

# Parse questions from YAML using Python
QUESTION_IDS=($("${PYTHON}" -c "
import sys, yaml
with open('${QUESTIONS_YAML}', 'r') as f:
    data = yaml.safe_load(f)
for q in data['questions']:
    print(q['id'])
" 2>/dev/null))

TOTAL_Q="${#QUESTION_IDS[@]}"
echo "=== ARM=${ARM} N=${N} model=${MODEL:-default} questions=${TOTAL_Q} ==="
echo "Output: ${ARM_OUTPUT_DIR}"

# Per-arm pre-work. When launched from run_parallel.sh, SKIP_INDEX_REBUILD=1
# so all arms share a pre-built index/embed and do not race on .leankg.
SKIP_INDEX_REBUILD="${SKIP_INDEX_REBUILD:-0}"
if [[ "${ARM}" == "leankg" ]]; then
  if [[ ! -x "${LEANKG_BIN}" ]]; then
    echo "ERROR: leankg binary not found at ${LEANKG_BIN}" >&2
    exit 2
  fi
  if [[ "${SKIP_INDEX_REBUILD}" == "1" ]]; then
    if [[ ! -d "${REPO_PATH}/.leankg" ]]; then
      echo "ERROR: SKIP_INDEX_REBUILD=1 but ${REPO_PATH}/.leankg missing" >&2
      exit 2
    fi
    echo "Pre-work: reusing existing LeanKG index+embed (SKIP_INDEX_REBUILD=1)."
  else
    echo "Pre-work: rebuilding LeanKG index + embed..."
    rm -rf "${REPO_PATH}/.leankg"
    ( cd "${REPO_PATH}" && "${LEANKG_BIN}" init ) > /dev/null 2>&1
    python3 -c "
import yaml
path = '${REPO_PATH}/leankg.yaml'
with open(path) as f:
    cfg = yaml.safe_load(f)
cfg['project']['languages'] = ['swift']
cfg['indexer']['include'] = ['*.swift']
cfg['indexer']['exclude'] = [
    '**/node_modules/**', '**/vendor/**', '**/.build/**', '**/Carthage/**',
    '**/Example/**', '**/Tests/**', '**/watchOS Example/**', '**/Package@**',
]
with open(path, 'w') as f:
    yaml.safe_dump(cfg, f, default_flow_style=False)
" 2>/dev/null || true
    ( cd "${REPO_PATH}" && "${LEANKG_BIN}" index . ) > /dev/null 2>&1
    if "${LEANKG_BIN}" embed --help >/dev/null 2>&1; then
      ( cd "${REPO_PATH}" && "${LEANKG_BIN}" embed --wait ) > /dev/null 2>&1
      echo "LeanKG index+embed ready."
    else
      echo "WARN: leankg lacks embed feature; continuing without vectors." >&2
      echo "LeanKG index ready (no embed)."
    fi
  fi
elif [[ "${ARM}" == "codegraph" ]]; then
  if [[ ! -x "${CODEGRAPH_BIN}" ]]; then
    echo "ERROR: codegraph binary not found at ${CODEGRAPH_BIN}" >&2
    exit 2
  fi
  echo "Pre-work: ensuring CodeGraph index exists..."
  if [[ ! -d "${REPO_PATH}/.codegraph" ]]; then
    ( cd "${REPO_PATH}" && "${CODEGRAPH_BIN}" init ) > /dev/null 2>&1
  fi
  echo "CodeGraph index ready."
fi

# For each question: run N times, with up to Q_PARALLEL concurrent questions.
# Default Q_PARALLEL=5 so wall-clock ≈ ceil(10/5) × one-question latency.
# Compatible with macOS bash 3.2 (no associative arrays).
Q_PARALLEL="${Q_PARALLEL:-5}"
MCP_CONFIG_PATH="$(mktemp -t leankg-mcp-XXXXXX.json)"
trap 'rm -f "${MCP_CONFIG_PATH}"' EXIT

"${BENCH_DIR}/install_mcp.sh" "${MCP_CONFIG_PATH}" "${ARM}" >/dev/null

export DATE CLAUDE_BIN REPO_PATH RESULTS_DIR DRY_RUN

echo "Q_PARALLEL=${Q_PARALLEL} (questions within this arm)"

FAIL=0
pids=""

count_live() {
  local live=0 p
  for p in ${pids}; do
    if kill -0 "${p}" 2>/dev/null; then live=$((live + 1)); fi
  done
  echo "${live}"
}

prune_pids() {
  local new="" p
  for p in ${pids}; do
    if kill -0 "${p}" 2>/dev/null; then
      new="${new} ${p}"
    else
      set +e; wait "${p}" 2>/dev/null; [[ $? -ne 0 ]] && FAIL=1; set -e
    fi
  done
  pids="${new# }"
}

for q_id in "${QUESTION_IDS[@]}"; do
  PROMPT="$("${PYTHON}" -c "
import yaml
with open('${QUESTIONS_YAML}', 'r') as f:
    data = yaml.safe_load(f)
for q in data['questions']:
    if q['id'] == '${q_id}':
        print(q['prompt'])
        break
" 2>/dev/null)"
  for (( run_idx=1; run_idx<=N; run_idx++ )); do
    while [[ "$(count_live)" -ge "${Q_PARALLEL}" ]]; do
      sleep 2
      prune_pids
    done
    prune_pids
    echo "--- launch ${q_id} run=${run_idx} (${ARM}) ---"
    bash "${BENCH_DIR}/run_one_q.sh" \
      "${ARM}" "${q_id}" "${run_idx}" "${MODEL}" "${MCP_CONFIG_PATH}" "${PROMPT}" &
    pids="${pids} $!"
    pids="${pids# }"
  done
done

for p in ${pids}; do
  set +e; wait "${p}"; [[ $? -ne 0 ]] && FAIL=1; set -e
done

if [[ "${FAIL}" -ne 0 ]]; then
  echo "=== ${ARM} finished with failures ===" >&2
  exit 1
fi

echo "=== ${ARM} done ==="
