#!/usr/bin/env bash
# phase-h.sh — Phase H: semantic search rebuild + re-benchmark ALL 3 question sets.
#
# Fix: binary was built WITHOUT --features embeddings → no semantic_search.
# Phase H: rebuild WITH embeddings, verify MCP tool discovery, run all 3 sets
# in parallel (3 repos × 3 arms = 9 parallel subprocesses), aggregate.
#
# Usage:
#   MCP_SMOKE_CHECK=1   abort if mcp_tool_count==0 for graph arms
#   SKIP_LEANKG_REBUILD=1   skip re-index+embed (if already warm)
#   Q_PARALLEL=8        concurrent questions within each arm (default: 8)
#   MODEL=haiku          model (default: haiku; machine routes to MiniMax)
#   N=1                  runs per question (default: 1)
#
# Question sets (from PLAN.md Phase H):
#   H-1: questions.yaml            Alamofire    10 core Swift
#   H-2: questions-ios-deep.yaml   Alamofire    15 deep-dive Swift
#   H-3: questions-typhoon-objc.yaml  Typhoon   10 ObjC

set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
RESULTS_DIR="${RESULTS_DIR:-${HERE}/results}"
LOG_DIR="${RESULTS_DIR}/scratch/phase-h-logs"
TIMESTAMP="$(date +%Y-%m-%d-%H%M)"
mkdir -p "${LOG_DIR}"

LEANKG_BIN="${LEANKG_BIN:-${HERE}/../../target/release/leankg}"
CODEGRAPH_BIN="${CODEGRAPH_BIN:-$(command -v codegraph)}"
CLAUDE_BIN="${CLAUDE_BIN:-$(command -v claude)}"
MODEL="${MODEL:-haiku}"
N="${N:-1}"
Q_PARALLEL="${Q_PARALLEL:-8}"
MCP_TIMEOUT="${MCP_TIMEOUT:-120}"
MCP_SMOKE_CHECK="${MCP_SMOKE_CHECK:-0}"
SKIP_LEANKG_REBUILD="${SKIP_LEANKG_REBUILD:-0}"

export LEANKG_BIN CODEGRAPH_BIN CLAUDE_BIN RESULTS_DIR BENCH_DIR="${HERE}"
export Q_PARALLEL MCP_SMOKE_CHECK SKIP_LEANKG_REBUILD N MODEL MCP_TIMEOUT

# ── Preflight ──────────────────────────────────────────────────────────
echo "=== Phase H — Semantic Search Rebuild + Re-benchmark ==="
echo "Timestamp: ${TIMESTAMP}"
echo "MCP_SMOKE_CHECK=${MCP_SMOKE_CHECK}"
echo "SKIP_LEANKG_REBUILD=${SKIP_LEANKG_REBUILD}"
echo "Q_PARALLEL=${Q_PARALLEL} N=${N} MODEL=${MODEL}"
echo ""
[[ -x "${LEANKG_BIN}" ]] || { echo "ERROR: missing leankg at ${LEANKG_BIN}"; exit 2; }
[[ -x "${CODEGRAPH_BIN}" ]] || { echo "ERROR: missing codegraph"; exit 2; }
[[ -x "${CLAUDE_BIN}" ]] || { echo "ERROR: missing claude"; exit 2; }

# Verify embeddings feature
if ! "${LEANKG_BIN}" embed --help >/dev/null 2>&1; then
  echo "ERROR: leankg binary lacks 'embed' subcommand. Rebuild: cargo build --release --features embeddings" >&2
  exit 2
fi
echo "LeanKG embed: OK"

# ── Define job matrix ──────────────────────────────────────────────────
# Each job: (repo_path, questions_yaml, lang, name)
declare -a JOBS
JOBS=(
  "${HERE}/repos/alamofire|${HERE}/questions.yaml|swift|alamofire-10q"
  "${HERE}/repos/alamofire|${HERE}/questions-ios-deep.yaml|swift|alamofire-ios-deep"
  "${HERE}/repos/typhoon|${HERE}/questions-typhoon-objc.yaml|objc|typhoon-objc"
)

# ── Pre-index all repos ───────────────────────────────────────────────
echo ""
echo "=== Pre-indexing all repos ==="
for job_spec in "${JOBS[@]}"; do
  IFS='|' read -r REPO_PATH QFILE LANG JOB_NAME <<< "${job_spec}"
  echo ""
  echo "--- ${JOB_NAME}: CodeGraph index ---"
  if [[ ! -d "${REPO_PATH}/.codegraph" ]]; then
    ( cd "${REPO_PATH}" && "${CODEGRAPH_BIN}" init ) || echo "WARN: codegraph init failed for ${JOB_NAME}"
  else
    ( cd "${REPO_PATH}" && "${CODEGRAPH_BIN}" sync ) || true
  fi
  ( cd "${REPO_PATH}" && "${CODEGRAPH_BIN}" status ) | head -5 || true
done

for job_spec in "${JOBS[@]}"; do
  IFS='|' read -r REPO_PATH QFILE LANG JOB_NAME <<< "${job_spec}"
  echo ""
  echo "--- ${JOB_NAME}: LeanKG index+embed (lang=${LANG}) ---"
  if [[ "${SKIP_LEANKG_REBUILD}" == "1" && -d "${REPO_PATH}/.leankg" ]]; then
    echo "Reusing existing .leankg"
    ( cd "${REPO_PATH}" && "${LEANKG_BIN}" status ) | tail -10 || true
  else
    rm -rf "${REPO_PATH}/.leankg"
    ( cd "${REPO_PATH}" && "${LEANKG_BIN}" init )
    python3 -c "
import yaml
path = '${REPO_PATH}/leankg.yaml'
lang = '${LANG}'
with open(path) as f:
    cfg = yaml.safe_load(f)
cfg['project']['languages'] = [lang]
ext_map = {'swift': ['*.swift'], 'objc': ['*.m','*.mm','*.h']}
cfg['indexer']['include'] = ext_map.get(lang, ['*.' + lang])
cfg['indexer']['exclude'] = [
    '**/node_modules/**', '**/vendor/**', '**/.build/**', '**/Carthage/**',
    '**/Example/**', '**/Tests/**', '**/watchOS Example/**', '**/Package@**',
]
with open(path, 'w') as f:
    yaml.safe_dump(cfg, f, default_flow_style=False)
print(f'{lang} config applied')
"
    ( cd "${REPO_PATH}" && "${LEANKG_BIN}" index . ) || { echo "ERROR: index failed for ${JOB_NAME}"; exit 2; }
    ( cd "${REPO_PATH}" && "${LEANKG_BIN}" embed --wait ) || { echo "ERROR: embed failed for ${JOB_NAME}"; exit 2; }
    ( cd "${REPO_PATH}" && "${LEANKG_BIN}" status ) | tail -15 || true
  fi
done

echo ""
echo "=== Pre-indexing complete ==="

# ── Launch all jobs in parallel ───────────────────────────────────────
#
# Each job → run_parallel.sh variant: 3 arms concurrently.
# 3 jobs × 3 arms = 9 subprocesses at once (each arm spawns Q_PARALLEL questions).
# Total: 35 questions × 3 arms = 105 agent calls.
#
echo ""
echo "=== Launching ${#JOBS[@]} jobs × 3 arms (9 total) ==="

PIDS=()
JOB_PIDS_FILE="${LOG_DIR}/job_pids.txt"
rm -f "${JOB_PIDS_FILE}"

for job_spec in "${JOBS[@]}"; do
  IFS='|' read -r REPO_PATH QFILE LANG JOB_NAME <<< "${job_spec}"
  JOB_LOG="${LOG_DIR}/${JOB_NAME}.log"
  echo "  launching job=${JOB_NAME} lang=${LANG} qfile=$(basename "${QFILE}")"
  (
    echo "=== Job: ${JOB_NAME} start $(date) ==="
    # Per-job result dir so runs don't collide
    JOB_RESULTS="${RESULTS_DIR}/phase-h/${TIMESTAMP}/${JOB_NAME}"
    mkdir -p "${JOB_RESULTS}"
    FAIL=0
    ARM_PIDS=()
    for arm in leankg codegraph none; do
      ARM_LOG="${LOG_DIR}/${JOB_NAME}-${arm}.log"
      echo "  [${JOB_NAME}] arm=${arm} starting..."
      (
        export REPO_PATH="${REPO_PATH}"
        export RESULTS_DIR="${JOB_RESULTS}"
        export QUESTIONS="${QFILE}"
        export LEANKG_LANG="${LANG}"
        export SKIP_INDEX_REBUILD=1
        bash "${HERE}/run_30q.sh" "${arm}" "${N}" "${MODEL}"
      ) > "${ARM_LOG}" 2>&1 &
      ARM_PIDS+=("$!")
      echo "    pid=${ARM_PIDS[${#ARM_PIDS[@]}-1]}"
    done
    for apid in "${ARM_PIDS[@]}"; do
      wait "${apid}" || FAIL=1
    done
    echo "=== Job: ${JOB_NAME} done (fail=${FAIL}) $(date) ==="
    exit "${FAIL}"
  ) > "${JOB_LOG}" 2>&1 &
  JPID="$!"
  PIDS+=("${JPID}")
  echo "    job pid=${JPID} log=${JOB_LOG}"
  echo "${JPID} ${JOB_NAME}" >> "${JOB_PIDS_FILE}"
done

# ── Wait for all jobs ─────────────────────────────────────────────────
echo ""
echo "=== Waiting for all ${#PIDS[@]} jobs ==="
FAIL=0
for pid in "${PIDS[@]}"; do
  wait "${pid}" || FAIL=1
done

echo ""
if [[ "${FAIL}" -ne 0 ]]; then
  echo "WARNING: one or more jobs failed. Check logs in ${LOG_DIR}/" >&2
fi

# ── Aggregate all results ─────────────────────────────────────────────
echo ""
echo "=== Aggregating Results ==="
for job_spec in "${JOBS[@]}"; do
  IFS='|' read -r REPO_PATH QFILE LANG JOB_NAME <<< "${job_spec}"
  JOB_RESULTS="${RESULTS_DIR}/phase-h/${TIMESTAMP}/${JOB_NAME}"
  QNAME="$(basename "${QFILE}" .yaml)"
  if [[ -d "${JOB_RESULTS}" ]]; then
    echo "  ${JOB_NAME} → ${QNAME}-${TIMESTAMP}.{md,json}"
    python3 "${HERE}/aggregate.py" \
      --results "${JOB_RESULTS}" \
      --questions "${QFILE}" \
      --name "${QNAME}-${TIMESTAMP}" || echo "WARN: aggregate failed for ${JOB_NAME}"
  else
    echo "  ${JOB_NAME}: no results dir (${JOB_RESULTS})"
  fi
done

# ── Summary ───────────────────────────────────────────────────────────
echo ""
echo "=== Phase H Complete ==="
echo "Timestamp: ${TIMESTAMP}"
echo "Logs: ${LOG_DIR}/"
echo "Reports:"
ls -la "${RESULTS_DIR}"/*-${TIMESTAMP}.md 2>/dev/null || echo "  (no reports found — may be in subdirs)"
echo ""
echo "Per-job tool call logs:"
find "${RESULTS_DIR}/runs/${TIMESTAMP}" -name "*.tools.log" -type f 2>/dev/null | head -20 || echo "  (none found)"
