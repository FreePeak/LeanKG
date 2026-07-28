#!/usr/bin/env bash
# run_parallel.sh — Launch all 3 arms concurrently (10Q, N=1 by default).
#
# Usage: run_parallel.sh [N] [model]
#   N     = runs per question (default: 1)
#   model = claude model id (default: sonnet)
#
# Each arm writes to results/runs/<DATE>/<arm>/... so paths do not conflict.

set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
N="${1:-1}"
MODEL="${2:-haiku}"
Q_PARALLEL="${Q_PARALLEL:-5}"
export Q_PARALLEL

LEANKG_BIN="${LEANKG_BIN:-${HERE}/../../target/release/leankg}"
CODEGRAPH_BIN="${CODEGRAPH_BIN:-$(command -v codegraph)}"
CLAUDE_BIN="${CLAUDE_BIN:-$(command -v claude)}"
REPO_PATH="${REPO_PATH:-${HERE}/repos/alamofire}"
RESULTS_DIR="${RESULTS_DIR:-${HERE}/results}"
LOG_DIR="${RESULTS_DIR}/scratch/parallel-logs"
mkdir -p "${LOG_DIR}"

export LEANKG_BIN CODEGRAPH_BIN CLAUDE_BIN REPO_PATH RESULTS_DIR BENCH_DIR="${HERE}"

echo "=== Alamofire 10Q Parallel Benchmark ==="
echo "N=${N} MODEL=${MODEL} Q_PARALLEL=${Q_PARALLEL}"
echo "leankg=${LEANKG_BIN}"
echo "codegraph=${CODEGRAPH_BIN}"
echo "repo=${REPO_PATH}"
echo ""

# Preflight
[[ -x "${LEANKG_BIN}" ]] || { echo "ERROR: missing leankg at ${LEANKG_BIN}"; exit 2; }
[[ -x "${CODEGRAPH_BIN}" ]] || { echo "ERROR: missing codegraph"; exit 2; }
[[ -x "${CLAUDE_BIN}" ]] || { echo "ERROR: missing claude"; exit 2; }
[[ -d "${REPO_PATH}" ]] || { echo "ERROR: missing Alamofire at ${REPO_PATH}"; exit 2; }

# Verify embeddings feature is present (embed subcommand must work)
# if ! "${LEANKG_BIN}" embed --help >/dev/null 2>&1; then
#   echo "ERROR: leankg binary lacks 'embed' (rebuild with: cargo build --release --features embeddings)" >&2
#   exit 2
# fi

# --- Pre-index both graphs BEFORE parallel arms ---
# Skip LeanKG rebuild if a fresh index+embed already exists (SKIP_LEANKG_REBUILD=1).
echo "--- CodeGraph index ---"
if [[ ! -d "${REPO_PATH}/.codegraph" ]]; then
  ( cd "${REPO_PATH}" && "${CODEGRAPH_BIN}" init )
else
  ( cd "${REPO_PATH}" && "${CODEGRAPH_BIN}" sync ) || true
fi
( cd "${REPO_PATH}" && "${CODEGRAPH_BIN}" status ) | head -20

echo ""
# Language override for leankg.yaml: Swift by default; set LEANKG_LANG=objc for ObjC repos.
LEANKG_LANG="${LEANKG_LANG:-swift}"
if [[ "${SKIP_LEANKG_REBUILD:-0}" == "1" && -d "${REPO_PATH}/.leankg" ]]; then
  echo "--- LeanKG index+embed: reusing existing (.leankg) ---"
  ( cd "${REPO_PATH}" && "${LEANKG_BIN}" status ) | tail -20
else
  echo "--- LeanKG index + embed (lang=${LEANKG_LANG}) ---"
  rm -rf "${REPO_PATH}/.leankg"
  ( cd "${REPO_PATH}" && "${LEANKG_BIN}" init )
  python3 -c "
import yaml
path = '${REPO_PATH}/leankg.yaml'
lang = '${LEANKG_LANG}'
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
  ( cd "${REPO_PATH}" && "${LEANKG_BIN}" index . )
  echo "Running embed --wait ..."
  ( cd "${REPO_PATH}" && "${LEANKG_BIN}" embed --wait )
  ( cd "${REPO_PATH}" && "${LEANKG_BIN}" status ) | tail -20
fi

echo ""
echo "--- Launching 3 arms in parallel ---"
PIDS=()
for arm in leankg codegraph none; do
  LOG="${LOG_DIR}/${arm}.log"
  echo "  starting arm=${arm} -> ${LOG}"
  (
    # leankg arm: do NOT re-index (already embedded); skip rebuild in run_30q
    SKIP_INDEX_REBUILD=1 bash "${HERE}/run_30q.sh" "${arm}" "${N}" "${MODEL}"
  ) > "${LOG}" 2>&1 &
  PIDS+=($!)
done

FAIL=0
for i in "${!PIDS[@]}"; do
  arm=("leankg" "codegraph" "none")
  pid="${PIDS[$i]}"
  name="${arm[$i]}"
  if wait "${pid}"; then
    echo "  arm ${name} OK (pid ${pid})"
  else
    echo "  arm ${name} FAILED (pid ${pid}) — see ${LOG_DIR}/${name}.log" >&2
    FAIL=1
  fi
done

echo ""
echo "--- Aggregate ---"
QNAME="$(basename "${QUESTIONS:-questions.yaml}" .yaml)"
python3 "${HERE}/aggregate.py" --results "${RESULTS_DIR}" --questions "${HERE}/${QUESTIONS:-questions.yaml}" \
  --name "${QNAME}-$(date +%Y-%m-%d)"

echo ""
if [[ "${FAIL}" -ne 0 ]]; then
  echo "WARNING: one or more arms failed; report may be incomplete."
  exit 1
fi
echo "=== Parallel run complete ==="
ls -la "${RESULTS_DIR}"/alamofire-10q-*.md 2>/dev/null || true
