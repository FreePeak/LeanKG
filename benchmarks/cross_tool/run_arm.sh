#!/usr/bin/env bash
# Run one arm (with or without) for one repo, N times, appending to a per-day
# JSONL file. Called by the Makefile; safe to call directly too.
#
# Usage: run_arm.sh <repo_slug> <arm> <N> <model>
#   arm in {with, without}
#   model = claude model id (sonnet, opus, ...)
#
# Environment overrides (optional):
#   LEANKG_BIN     absolute path to leankg binary
#   CLAUDE_BIN     absolute path to claude CLI
#   RESULTS_DIR    absolute path to the results root
#   REPOS_DIR      absolute path to the cloned repos root
#   DRY_RUN=1      print what would run instead of invoking claude (smoke test)

set -euo pipefail

SLUG="${1:?repo slug required}"
ARM="${2:?arm required (with|without)}"
N="${3:?N required}"
MODEL="${4:-}"              # optional: empty = use claude -p default

if [[ "${ARM}" != "with" && "${ARM}" != "without" ]]; then
  echo "ERROR: arm must be 'with' or 'without' (got '${ARM}')" >&2
  exit 2
fi

HERE="$(cd "$(dirname "$0")" && pwd)"
LEANKG_BIN="${LEANKG_BIN:-${HERE}/../../target/release/leankg}"
CLAUDE_BIN="${CLAUDE_BIN:-$(command -v claude || true)}"
RESULTS_DIR="${RESULTS_DIR:-${HERE}/results}"
REPOS_DIR="${REPOS_DIR:-${HERE}/repos}"
DRY_RUN="${DRY_RUN:-0}"

# Export so child scripts (install_leankg_mcp.sh, run_one.sh) see them
export LEANKG_BIN CLAUDE_BIN RESULTS_DIR REPOS_DIR

if [[ -z "${CLAUDE_BIN}" ]]; then
  echo "ERROR: claude CLI not found on PATH and CLAUDE_BIN not set" >&2
  exit 2
fi

if [[ "${ARM}" == "with" && ! -x "${LEANKG_BIN}" ]]; then
  echo "ERROR: leankg binary not executable at ${LEANKG_BIN}" >&2
  exit 2
fi

REPO_PATH="${REPOS_DIR}/${SLUG}"
if [[ ! -d "${REPO_PATH}" ]]; then
  echo "ERROR: repo not cloned: ${REPO_PATH}  (run: make setup)" >&2
  exit 2
fi

DATE="$(date +%Y-%m-%d)"
OUTPUT_PATH="${RESULTS_DIR}/runs/${DATE}/${SLUG}/${ARM}/runs.jsonl"
mkdir -p "$(dirname "${OUTPUT_PATH}")"

CONFIG_PATH="$(mktemp -t leankg-mcp-XXXXXX.json)"
trap 'rm -f "${CONFIG_PATH}"' EXIT

"${HERE}/install_leankg_mcp.sh" "${CONFIG_PATH}" "${ARM}" >/dev/null
PROMPT="$("${PYTHON:-python3}" "${HERE}/get_prompt.py" --repos "${HERE}/repos.yaml" --slug "${SLUG}")"

echo "=== arm=${ARM} repo=${SLUG} N=${N} model=${MODEL:-default} ==="

for i in $(seq 1 "${N}"); do
  if [[ "${ARM}" == "with" ]]; then
    rm -rf "${REPO_PATH}/.leankg"
    if [[ "${DRY_RUN}" == "1" ]]; then
      echo "  [dry] would run: (cd ${REPO_PATH} && ${LEANKG_BIN} init && ${LEANKG_BIN} index .)" >&2
    else
      ( cd "${REPO_PATH}" && "${LEANKG_BIN}" init ) >/dev/null 2>&1
      ( cd "${REPO_PATH}" && "${LEANKG_BIN}" index . ) >/dev/null 2>&1
    fi
  fi

  if [[ "${DRY_RUN}" == "1" ]]; then
    echo "  [dry] run ${i}: would invoke claude -p with prompt='${PROMPT:0:50}...'" >&2
    continue
  fi

  "${HERE}/run_one.sh" \
    "${REPO_PATH}" \
    "${PROMPT}" \
    "${ARM}" \
    "${i}" \
    "${MODEL}" \
    "${CONFIG_PATH}" \
    "${OUTPUT_PATH}"
done