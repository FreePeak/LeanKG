#!/usr/bin/env bash
# Run BOTH arms (with + without) for one repo, N runs each. Designed to be
# invoked by one subagent per repo so the 7 benchmark repos can run in
# parallel. Each invocation writes to a per-day JSONL under
# results/runs/<DATE>/<slug>/<arm>/runs.jsonl.
#
# Usage: run_repo.sh <slug> <N> <model>
#   slug   = one of the entries in repos.yaml
#   N      = number of runs per arm (default 4)
#   model  = claude model id, empty = use claude -p default
#
# Environment overrides:
#   LEANKG_BIN    absolute path to leankg binary (default: ../../target/release/leankg)
#   CLAUDE_BIN    absolute path to claude CLI (default: command -v claude)
#   RESULTS_DIR   absolute path to the results root
#   REPOS_DIR     absolute path to the cloned repos root
#   BENCH_DIR     absolute path to this bench dir (auto-detected if unset)
#   DRY_RUN=1     print what would run instead of invoking claude

set -uo pipefail

SLUG="${1:?repo slug required}"
N="${2:-4}"
MODEL="${3:-}"

if [[ ! -d "${BENCH_DIR:-}" ]]; then
  BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
fi

# Default to the worktree's release build if the caller didn't specify one.
if [[ -z "${LEANKG_BIN:-}" ]]; then
  # 1) Honor explicit override; 2) prefer the worktree's release binary; 3) PATH
  WORKTREE_LEANKG="${BENCH_DIR}/../../target/release/leankg"
  if [[ -x "${WORKTREE_LEANKG}" ]]; then
    LEANKG_BIN="${WORKTREE_LEANKG}"
  else
    LEANKG_BIN="$(command -v leankg || true)"
  fi
fi

RESULTS_DIR="${RESULTS_DIR:-${BENCH_DIR}/results}"
REPOS_DIR="${REPOS_DIR:-${BENCH_DIR}/repos}"
CLAUDE_BIN="${CLAUDE_BIN:-$(command -v claude || true)}"

export LEANKG_BIN CLAUDE_BIN RESULTS_DIR REPOS_DIR BENCH_DIR

if [[ -z "${CLAUDE_BIN}" ]]; then
  echo "ERROR: claude CLI not found on PATH and CLAUDE_BIN not set" >&2
  exit 2
fi
if [[ ! -x "${LEANKG_BIN}" ]]; then
  echo "ERROR: leankg binary not executable at ${LEANKG_BIN}" >&2
  exit 2
fi

REPO_PATH="${REPOS_DIR}/${SLUG}"
if [[ ! -d "${REPO_PATH}" ]]; then
  echo "ERROR: repo not cloned: ${REPO_PATH}  (run: make setup)" >&2
  exit 2
fi

echo "=== repo=${SLUG} N=${N} model=${MODEL:-default} leankg=${LEANKG_BIN} ==="

# Run both arms sequentially inside this single repo (each subagent owns its repo).
"${BENCH_DIR}/run_arm.sh" "${SLUG}" with    "${N}" "${MODEL}"
"${BENCH_DIR}/run_arm.sh" "${SLUG}" without "${N}" "${MODEL}"

echo "=== repo=${SLUG} done ==="