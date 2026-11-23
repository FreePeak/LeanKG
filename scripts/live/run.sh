#!/usr/bin/env bash
# Local live-test harness. Builds the real leankg binary into the run's temp dir,
# runs every scenario in scripts/live/scenarios/ against temp fixtures and a real
# server, and writes evidence to .live-evidence/<UTC timestamp>/ (latest -> newest run).
# See scripts/live/README.md.
#
# Usage: scripts/live/run.sh [--only NN] [--no-build] [--bin PATH] [--label NAME]
#                            [--pg-url URL] [--baseline-bin PATH] [--seed-bin PATH]
#   --only NN          run one scenario; NN is digits only (e.g. 03)
#   --bin PATH         run a prebuilt binary instead of building (implies --no-build);
#                      LEANKG_BIN=PATH does the same when --bin is not given
#   --label NAME       recorded in summary.md and appended to the evidence dir name
#   --pg-url URL       LIVE_PG_URL for the PostgreSQL gates in scenario 16 (private pgvector only)
#   --baseline-bin P   LIVE_BASELINE_BIN for scenario 16 (schema-19 migration check)
#   --seed-bin PATH    LIVE_SEED_BIN for scenario 21 (pre-fix binary)
# The LIVE_* inputs come from these flags (or the make line, see the Makefile),
# never from the ambient shell environment.

ENV_BIN=${LEANKG_BIN:-}
LIVE_DIR=$(cd "$(dirname "$0")" && pwd -P)
REPO_ROOT=$(cd "$LIVE_DIR/../.." && pwd -P)

usage() { sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'; }

# resolve_exe FLAG PATH: sets ABS_EXE to the absolute path of an executable file, or exits 2.
resolve_exe() {
  [ -f "$2" ] && [ -x "$2" ] || { echo "run.sh: $1 is not an executable file: $2" >&2; exit 2; }
  ABS_EXE="$(cd "$(dirname "$2")" && pwd -P)/$(basename "$2")"
}

ONLY=""
BUILD=1
BIN_ARG=""
LABEL=""
PG_URL_ARG=""
BASELINE_ARG=""
SEED_ARG=""
while [ $# -gt 0 ]; do
  case "$1" in
    --only)
      [ $# -ge 2 ] || { echo "run.sh: --only needs a scenario number" >&2; exit 2; }
      [[ $2 =~ ^[0-9]+$ ]] || { echo "run.sh: --only takes digits only (e.g. 03), got: $2" >&2; exit 2; }
      ONLY=$(printf '%02d' "$((10#$2))")
      shift 2 ;;
    --no-build) BUILD=0; shift ;;
    --bin)
      [ $# -ge 2 ] || { echo "run.sh: --bin needs a binary path" >&2; exit 2; }
      BIN_ARG=$2
      shift 2 ;;
    --label)
      [ $# -ge 2 ] || { echo "run.sh: --label needs a name" >&2; exit 2; }
      LABEL=$2
      shift 2 ;;
    --pg-url)
      [ $# -ge 2 ] || { echo "run.sh: --pg-url needs a URL" >&2; exit 2; }
      PG_URL_ARG=$2
      shift 2 ;;
    --baseline-bin)
      [ $# -ge 2 ] || { echo "run.sh: --baseline-bin needs a binary path" >&2; exit 2; }
      BASELINE_ARG=$2
      shift 2 ;;
    --seed-bin)
      [ $# -ge 2 ] || { echo "run.sh: --seed-bin needs a binary path" >&2; exit 2; }
      SEED_ARG=$2
      shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "run.sh: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [ -n "$ONLY" ] && ! ls "$LIVE_DIR/scenarios/$ONLY"-*.sh >/dev/null 2>&1; then
  echo "run.sh: no scenario matches --only $ONLY" >&2
  exit 2
fi

if [ -n "$LABEL" ] && ! printf '%s' "$LABEL" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$'; then
  echo "run.sh: --label must be 1-40 characters of [A-Za-z0-9._-], starting with a letter or digit" >&2
  exit 2
fi

BIN_ARG=${BIN_ARG:-$ENV_BIN}
if [ -n "$BIN_ARG" ]; then
  resolve_exe --bin "$BIN_ARG"
  BIN_ARG=$ABS_EXE
  BUILD=0
fi
if [ -n "$BASELINE_ARG" ]; then
  resolve_exe --baseline-bin "$BASELINE_ARG"
  BASELINE_ARG=$ABS_EXE
fi
if [ -n "$SEED_ARG" ]; then
  resolve_exe --seed-bin "$SEED_ARG"
  SEED_ARG=$ABS_EXE
fi

for tool in make go curl jq nc perl; do
  command -v "$tool" >/dev/null 2>&1 || { echo "run.sh: required tool missing: $tool" >&2; exit 2; }
done

# The shared Go build cache only (never a private GOCACHE), and a clean LEANKG_*
# environment so a developer's own tokens, DB engine or project list cannot
# change what the harness exercises. The LIVE_* inputs are cleared the same way.
unset GOCACHE
for v in $(env | sed -n 's/^\(LEANKG_[A-Za-z0-9_]*\)=.*/\1/p'); do unset "$v"; done
unset LIVE_PG_URL LIVE_BASELINE_BIN LIVE_SEED_BIN

STARTED=$(date -u +%Y-%m-%dT%H:%M:%SZ)
RUN_STAMP=$(date -u +%Y%m%dT%H%M%SZ)
EVID_ROOT="$REPO_ROOT/.live-evidence"
RUN_DIR="$EVID_ROOT/$RUN_STAMP${LABEL:+-$LABEL}"
[ -e "$RUN_DIR" ] && RUN_DIR="$RUN_DIR-$$"
mkdir -p "$RUN_DIR/logs" "$RUN_DIR/scenarios"
ln -sfn "$(basename "$RUN_DIR")" "$EVID_ROOT/latest"
RESULTS="$RUN_DIR/results.tsv"
SCEN="$RUN_DIR/scenarios.tsv"
: >"$RESULTS"
: >"$SCEN"

# The work dir is everything the run writes outside the evidence dir. WORK must be
# set before any cleanup or redact runs, so a failed mktemp stops the run here.
WORK=$(mktemp -d "${TMPDIR:-/tmp}/leankg-live.XXXXXX") && [ -n "$WORK" ] \
  || { echo "run.sh: mktemp failed; no work dir" >&2; exit 2; }
WORK=$(cd "$WORK" && pwd -P) || exit 2
mkdir -p "$WORK/raw" "$WORK/home" "$WORK/bin"
FIXTURE="$WORK/fixture"
export LEANKG_HOME="$WORK/home"
# LEANKG_HOME does not redirect the fleet registry, so pin it to the work dir.
# Without this every `leankg index` in the run would register into the real one.
export LEANKG_PORTFOLIO_DB="$WORK/home/portfolio.db"
LEANKG_BIN=${BIN_ARG:-$WORK/bin/leankg}
if [ -n "$PG_URL_ARG" ]; then export LIVE_PG_URL="$PG_URL_ARG"; fi
if [ -n "$BASELINE_ARG" ]; then export LIVE_BASELINE_BIN="$BASELINE_ARG"; fi
if [ -n "$SEED_ARG" ]; then export LIVE_SEED_BIN="$SEED_ARG"; fi

# shellcheck source=lib.sh
. "$LIVE_DIR/lib.sh"

cleanup() {
  local pid f
  if [ -n "$WORK" ] && [ -f "$WORK/servers.pids" ]; then
    while read -r pid; do
      [ -n "$pid" ] && stop_pid "$pid"
    done <"$WORK/servers.pids"
  fi
  if [ -n "$WORK" ] && [ -d "$WORK" ]; then
    for f in "$WORK"/raw/server-*.log; do
      [ -f "$f" ] && redact <"$f" >"$RUN_DIR/logs/$(basename "$f")"
    done
  fi
  if [ -n "$WORK" ] && [ -d "$WORK" ] && [ "${LIVE_KEEP_WORK:-0}" != 1 ]; then
    rm -rf "$WORK"
  fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# The binary is built into the work dir, never into the checkout's bin/, which a
# launchd job runs from the main checkout.
if [ "$BUILD" = 1 ]; then
  echo "build: go build into the work dir (log: $RUN_DIR/logs/build.log)"
  build_rc=0
  CGO_ENABLED=0 go -C "$REPO_ROOT" build -o "$WORK/bin/" ./cmd/leankg ./cmd/leankg-embed \
    >"$WORK/raw/build.log" 2>&1 || build_rc=$?
  redact <"$WORK/raw/build.log" >"$RUN_DIR/logs/build.log"
  if [ "$build_rc" -ne 0 ]; then
    echo "build failed, see $RUN_DIR/logs/build.log" >&2
    exit 1
  fi
fi
[ -x "$LEANKG_BIN" ] || { echo "run.sh: $LEANKG_BIN missing; drop --no-build to build it" >&2; exit 2; }

GIT_HEAD=$(git -C "$REPO_ROOT" rev-parse HEAD)
GIT_BRANCH=$(git -C "$REPO_ROOT" rev-parse --abbrev-ref HEAD)
VERSION=$(tr -d '[:space:]' <"$REPO_ROOT/cmd/leankg/VERSION")
GO_VERSION=$(go version)
LEANKG_VERSION=$("$LEANKG_BIN" version 2>&1)
BIN_NAME=$(basename "$LEANKG_BIN")
BIN_SHA=$(shasum -a 256 "$LEANKG_BIN" | cut -d' ' -f1)
LABEL_DISPLAY=${LABEL:-none}
{
  echo "started_utc=$STARTED"
  echo "label=$LABEL_DISPLAY"
  echo "git_head=$GIT_HEAD"
  echo "git_branch=$GIT_BRANCH"
  echo "VERSION=$VERSION"
  echo "binary=$BIN_NAME"
  echo "binary_sha256=$BIN_SHA"
  echo "leankg_version=$LEANKG_VERSION"
  echo "go_version=$GO_VERSION"
  echo "host=$(uname -s) $(uname -r) $(uname -m)"
} >"$RUN_DIR/env.txt"

# run_scenario FILE: run one scenario file in a subshell, then score it.
run_scenario() {
  local file=$1 base t0 t1 rc=0
  base=$(basename "$file" .sh)
  SCENARIO_ID=${base%%-*}
  SCENARIO_NAME=${base#*-}
  SCN_DIR="$RUN_DIR/scenarios/$SCENARIO_ID"
  CAP_DIR="$SCN_DIR/captures"
  mkdir -p "$CAP_DIR"
  : >"$SCN_DIR/scenario.log"
  t0=$(now_ms)
  step "scenario $SCENARIO_ID: $SCENARIO_NAME"
  ( . "$file"; scenario_main ) || rc=$?
  t1=$(now_ms)
  finish_scenario "$rc" $((t1 - t0))
}

# finish_scenario RC MS: derive the scenario status from its rows in results.tsv.
finish_scenario() {
  local rc=$1 ms=$2 counts pass fail skip status note
  LAST_MS=0
  if [ "$rc" -ne 0 ]; then
    LAST_CAPTURE=""
    record "scenario runs to completion" "exit 0" "exit $rc" FAIL ""
  fi
  counts=$(awk -F'\t' -v id="$SCENARIO_ID" \
    '$1 == id { n[$5]++ } END { printf "%d %d %d", n["PASS"], n["FAIL"], n["SKIP"] }' "$RESULTS")
  read -r pass fail skip <<<"$counts"
  if [ "$skip" -gt 0 ] && [ "$pass" -eq 0 ] && [ "$fail" -eq 0 ]; then
    status=SKIP
    note=$(awk -F'\t' -v id="$SCENARIO_ID" '$1 == id && $5 == "SKIP" { print $7; exit }' "$RESULTS")
  elif [ "$fail" -gt 0 ]; then
    status=FAIL
    note="$fail assertion(s) failed"
  elif [ "$pass" -gt 0 ]; then
    status=PASS
    note="$pass assertion(s) passed"
  else
    status=FAIL
    note="no assertions recorded"
    record "scenario records assertions" ">=1" "0" FAIL ""
  fi
  printf '%s\t%s\t%s\t%s\t%s\n' "$SCENARIO_ID" "$SCENARIO_NAME" "$status" "$ms" "$note" >>"$SCEN"
  echo "SCENARIO $SCENARIO_ID $SCENARIO_NAME: $status ($note)"
}

for f in "$LIVE_DIR"/scenarios/*.sh; do
  id=$(basename "$f" | cut -c1-2)
  if [ -n "$ONLY" ] && [ "$id" != "$ONLY" ]; then continue; fi
  run_scenario "$f"
done

# write_summary: summary.json and summary.md from results.tsv and scenarios.tsv.
write_summary() {
  local a s
  a=$(jq -Rn '[inputs | select(length > 0) | split("\t")
      | {scenario: .[0], assertion: .[1], expected: .[2], actual: .[3],
         status: .[4], duration_ms: (.[5] | tonumber), note: .[6]}]' "$RESULTS")
  s=$(jq -Rn '[inputs | select(length > 0) | split("\t")
      | {id: .[0], name: .[1], status: .[2], duration_ms: (.[3] | tonumber), note: .[4]}]' "$SCEN")
  jq -n --argjson a "$a" --argjson s "$s" \
    --arg started "$STARTED" --arg head "$GIT_HEAD" --arg branch "$GIT_BRANCH" \
    --arg version "$VERSION" --arg leankg "$LEANKG_VERSION" --arg go "$GO_VERSION" \
    --arg label "$LABEL_DISPLAY" --arg bin "$BIN_NAME" --arg sha "$BIN_SHA" '
    ($a | map(select(.status == "FAIL"))) as $fails
    | {
        result: (if ($s | any(.status == "FAIL")) then "FAIL" else "PASS" end),
        meta: {started_utc: $started, label: $label, git_head: $head, git_branch: $branch,
               VERSION: $version, binary: $bin, binary_sha256: $sha,
               leankg_version: $leankg, go_version: $go},
        totals: {scenarios: ($s | length),
                 scenarios_pass: ($s | map(select(.status == "PASS")) | length),
                 scenarios_fail: ($s | map(select(.status == "FAIL")) | length),
                 scenarios_skip: ($s | map(select(.status == "SKIP")) | length),
                 assertions_pass: ($a | map(select(.status == "PASS")) | length),
                 assertions_fail: ($fails | length),
                 assertions_skip: ($a | map(select(.status == "SKIP")) | length)},
        scenarios: $s,
        assertions: $a,
        product_failures: ($fails | map(select(.assertion | startswith("[product]"))))
      }' >"$RUN_DIR/summary.json"

  jq -r '
    def cell: tostring | gsub("[|]"; "\\|");
    "# LeanKG live test run",
    "",
    "Result: **\(.result)**",
    "",
    "| Field | Value |",
    "|---|---|",
    "| Label | \(.meta.label) |",
    "| Started (UTC) | \(.meta.started_utc) |",
    "| Git HEAD | `\(.meta.git_head)` (harness checkout; the binary may come from elsewhere) |",
    "| Branch | \(.meta.git_branch) |",
    "| VERSION file | \(.meta.VERSION) |",
    "| Binary file | \(.meta.binary), sha256 `\(.meta.binary_sha256)` |",
    "| leankg binary | \(.meta.leankg_version) |",
    "| Go | \(.meta.go_version) |",
    "| Scenarios | \(.totals.scenarios_pass) PASS, \(.totals.scenarios_fail) FAIL, \(.totals.scenarios_skip) SKIP |",
    "| Assertions | \(.totals.assertions_pass) PASS, \(.totals.assertions_fail) FAIL, \(.totals.assertions_skip) SKIP |",
    "",
    "## Scenarios",
    "",
    "| ID | Scenario | Status | Duration (ms) | Note |",
    "|---|---|---|---|---|",
    (.scenarios[] | "| \(.id) | \(.name) | \(.status) | \(.duration_ms) | \(.note | cell) |"),
    "",
    "## Assertions",
    "",
    "| Scenario | Assertion | Expected | Actual | Status | Duration (ms) |",
    "|---|---|---|---|---|---|",
    (.assertions[] | "| \(.scenario) | \(.assertion | cell) | \(.expected | cell) | \(.actual | cell) | \(.status) | \(.duration_ms) |"),
    "",
    "## Product failures",
    "",
    (if (.product_failures | length) == 0
     then "None."
     else (.product_failures[] | "- scenario \(.scenario): \(.assertion). Expected `\(.expected)`, actual `\(.actual)`. Evidence: `\(.note)`")
     end),
    "",
    "Evidence: `logs/` (build and server logs), `scenarios/NN/scenario.log`, `scenarios/NN/captures/`, `results.tsv`, `summary.json`."
  ' "$RUN_DIR/summary.json" >"$RUN_DIR/summary.md"
}

write_summary
echo ""
echo "evidence: $RUN_DIR"
echo "summary:  $RUN_DIR/summary.md"
if jq -e '.result == "PASS"' "$RUN_DIR/summary.json" >/dev/null; then
  echo "result: PASS"
  exit 0
fi
echo "result: FAIL"
exit 1
