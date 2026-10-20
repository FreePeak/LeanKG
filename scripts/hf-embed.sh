#!/usr/bin/env bash
# scripts/hf-embed.sh - EmbeddingGemma 2 bulk embed on a Hugging Face Job.
#
# Exports the project's elements (leankg-embed export), uploads the NDJSON to a
# private Storage Bucket, runs scripts/hf_embed_job.py as a detached HF Job with
# the bucket mounted at /data, downloads the vectors and imports them
# (leankg-embed import). Configuration is environment only (see .env.example).
#
# Cost guards: Jobs bill per minute and a personal Hugging Face account cannot
# set a spending cap. Every run prints an estimate and a worst case. The worst
# case is the job timeout times the hourly price, because the timeout bounds the
# billed time. A run whose worst case exceeds LEANKG_HF_MAX_USD, or whose
# estimate exceeds the timeout, is refused before anything is uploaded.
#
# Prices (USD per hour) come from https://huggingface.co/docs/hub/jobs-pricing
# and are kept in flavor_usd_per_hour below. Only the flavors listed there run.
#
# Usage: scripts/hf-embed.sh [--dry-run] [--yes] [--limit N] [--keep] <project-dir>
set -euo pipefail

usage() {
  cat <<'EOF'
usage: scripts/hf-embed.sh [--dry-run] [--yes] [--limit N] [--keep] <project-dir>
  --dry-run  print the estimate, the guards and the planned commands; start nothing
  --yes      upload the export and start the Hugging Face Job (without it: exit 1)
  --limit N  embed only the first N exported elements (start with 200 to measure)
  --keep     keep the run directory in the bucket after a successful import
Configuration: LEANKG_HF_* and LEANKG_EMBED_* environment variables (see .env.example).
EOF
}

die() {
  echo "hf-embed: $*" >&2
  exit 1
}

# Hourly USD price per flavor, from https://huggingface.co/docs/hub/jobs-pricing
flavor_usd_per_hour() {
  case "$1" in
    cpu-basic) echo 0.01 ;;
    cpu-upgrade) echo 0.03 ;;
    t4-small) echo 0.40 ;;
    t4-medium) echo 0.60 ;;
    l4x1) echo 0.80 ;;
    a10g-small) echo 1.00 ;;
    a10g-large) echo 1.50 ;;
    *) return 1 ;;
  esac
}

# Job timeout as seconds: a bare integer or N with s, m or h.
timeout_seconds() {
  local re='^([0-9]+)([smh]?)$'
  [[ $1 =~ $re ]] || return 1
  local n="${BASH_REMATCH[1]}"
  [ "$n" -gt 0 ] || return 1
  case "${BASH_REMATCH[2]}" in
    "" | s) echo "$n" ;;
    m) echo $((n * 60)) ;;
    h) echo $((n * 3600)) ;;
  esac
}

# Print a command as "+ argv", the same line for the plan and the real run.
show() {
  printf '+'
  printf ' %s' "$@"
  printf '\n'
}

# Running time in seconds from `hf jobs inspect --json` (started_at and
# finished_at, found at any depth). Prints nothing when they are not there.
job_running_seconds() {
  python3 -c '
import datetime, json, sys

def find(node, key):
    if isinstance(node, dict):
        if isinstance(node.get(key), str):
            return node[key]
        for value in node.values():
            found = find(value, key)
            if found:
                return found
    elif isinstance(node, list):
        for value in node:
            found = find(value, key)
            if found:
                return found
    return None

def parse(text):
    return datetime.datetime.fromisoformat(text.replace("Z", "+00:00"))

try:
    doc = json.load(sys.stdin)
    started, finished = find(doc, "started_at"), find(doc, "finished_at")
    if started and finished:
        print(int((parse(finished) - parse(started)).total_seconds()))
except Exception:
    pass
'
}

is_number() {
  local re='^[0-9]+(\.[0-9]+)?$'
  [[ $1 =~ $re ]]
}

# ---- configuration and validation (no network, no hf calls) ----------------

DRY_RUN=0
YES=0
KEEP=0
LIMIT=""
PROJECT_DIR=""
while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) DRY_RUN=1 ;;
    --yes) YES=1 ;;
    --keep) KEEP=1 ;;
    --limit)
      [ $# -ge 2 ] || die "--limit needs a number"
      LIMIT="$2"
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    -*) die "unknown flag $1 (see --help)" ;;
    *)
      [ -z "$PROJECT_DIR" ] || die "one project directory expected"
      PROJECT_DIR="$1"
      ;;
  esac
  shift
done

[ -n "$PROJECT_DIR" ] || { usage >&2; exit 1; }
[ -d "$PROJECT_DIR" ] || die "project directory not found: $PROJECT_DIR"

BUCKET="${LEANKG_HF_BUCKET:-}"
[ -n "$BUCKET" ] || die "LEANKG_HF_BUCKET is required (<owner>/<bucket>, see .env.example)"
case "$BUCKET" in
  hf:* | /* | */) die "LEANKG_HF_BUCKET must be <owner>/<bucket> without a scheme (got '$BUCKET')" ;;
  */*) ;;
  *) die "LEANKG_HF_BUCKET must be <owner>/<bucket> (got '$BUCKET')" ;;
esac

MODEL="${LEANKG_EMBED_MODEL:-embeddinggemma-2}"
MODEL_DIR="${LEANKG_HF_MODEL_DIR:-}"
MODEL_DIR="${MODEL_DIR#/}"
MODEL_DIR="${MODEL_DIR%/}"
WORK="${LEANKG_HF_WORK_DIR:-leankg}"
WORK="${WORK#/}"
WORK="${WORK%/}"
[ -n "$WORK" ] || die "LEANKG_HF_WORK_DIR must not be empty"
FLAVOR="${LEANKG_HF_JOB_FLAVOR:-l4x1}"
TIMEOUT="${LEANKG_HF_JOB_TIMEOUT:-30m}"
MAX_USD="${LEANKG_HF_MAX_USD:-0.50}"
RATE="${LEANKG_HF_EST_RATE:-50}"
OVERHEAD="${LEANKG_HF_EST_OVERHEAD_MIN:-6}"
EMBED_BIN="${LEANKG_EMBED_BIN:-leankg-embed}"
HF="${HF_BIN:-hf}"

PRICE="$(flavor_usd_per_hour "$FLAVOR")" \
  || die "unknown LEANKG_HF_JOB_FLAVOR '$FLAVOR' (priced: cpu-basic cpu-upgrade t4-small t4-medium l4x1 a10g-small a10g-large)"
TIMEOUT_S="$(timeout_seconds "$TIMEOUT")" \
  || die "LEANKG_HF_JOB_TIMEOUT '$TIMEOUT' must be N, Ns, Nm or Nh with N > 0"
is_number "$MAX_USD" || die "LEANKG_HF_MAX_USD must be a number (got '$MAX_USD')"
if ! is_number "$RATE" || ! awk -v v="$RATE" 'BEGIN { exit !(v > 0) }'; then
  die "LEANKG_HF_EST_RATE must be a positive number (got '$RATE')"
fi
is_number "$OVERHEAD" || die "LEANKG_HF_EST_OVERHEAD_MIN must be a number (got '$OVERHEAD')"
if [ -n "$LIMIT" ]; then
  limit_re='^[1-9][0-9]*$'
  [[ $LIMIT =~ $limit_re ]] || die "--limit needs a positive integer (got '$LIMIT')"
fi

command -v "$HF" >/dev/null 2>&1 || die "hf CLI not found: $HF (set HF_BIN or install it)"
command -v "$EMBED_BIN" >/dev/null 2>&1 || die "leankg-embed not found: $EMBED_BIN (set LEANKG_EMBED_BIN)"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
RUN_REL="$WORK/$RUN_ID"
BUCKET_URI="hf://buckets/$BUCKET"
UP_URI="$BUCKET_URI/$RUN_REL/export.ndjson"
DOWN_URI="$BUCKET_URI/$RUN_REL/vectors.ndjson"
JOB_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/hf_embed_job.py"
JOB_MODEL_PATH="/data${MODEL_DIR:+/$MODEL_DIR}"

# ---- export and estimate ----------------------------------------------------

# The export is local. It opens the project store the way every leankg-embed
# command does (migrations run) and uploads or starts nothing.
"$EMBED_BIN" export --project "$PROJECT_DIR" --model "$MODEL" --out "$TMP/export-all.ndjson"
if [ -n "$LIMIT" ]; then
  head -n "$LIMIT" "$TMP/export-all.ndjson" > "$TMP/export.ndjson"
else
  mv "$TMP/export-all.ndjson" "$TMP/export.ndjson"
fi
LINES="$(wc -l < "$TMP/export.ndjson" | tr -d ' ')"

# Estimate: minutes = overhead + lines / rate / 60; cost = minutes / 60 * price;
# worst case = timeout / 3600 * price. Flags are computed on the unrounded values.
ESTIMATE="$(awk -v n="$LINES" -v o="$OVERHEAD" -v r="$RATE" -v p="$PRICE" \
  -v t="$TIMEOUT_S" -v max="$MAX_USD" 'BEGIN {
    m = o + n / r / 60
    est = m / 60 * p
    worst = t / 3600 * p
    printf "%.2f %.2f %.2f %d %d", m, est, worst, (m * 60 > t), (worst > max)
  }')"
read -r EST_MIN EST_USD WORST_USD TOO_LONG OVER_BUDGET <<<"$ESTIMATE"

cat <<EOF
hf-embed plan (run $RUN_ID)
  project:          $PROJECT_DIR
  model:            $MODEL
  lines to embed:   $LINES
  job flavor:       $FLAVOR (\$$PRICE per hour)
  job timeout:      $TIMEOUT (${TIMEOUT_S}s, one attempt)
  estimate:         $EST_MIN min, \$$EST_USD (overhead ${OVERHEAD} min + lines / ${RATE} per s)
  worst case:       \$$WORST_USD (timeout x hourly price; billing stops at the timeout)
  budget:           LEANKG_HF_MAX_USD=$MAX_USD
EOF

if [ "$LINES" -eq 0 ]; then
  echo "nothing to embed: no upload, no job"
  exit 0
fi
if [ "$OVER_BUDGET" -eq 1 ]; then
  die "worst case \$$WORST_USD exceeds LEANKG_HF_MAX_USD=$MAX_USD: raise LEANKG_HF_MAX_USD, or lower LEANKG_HF_JOB_TIMEOUT or LEANKG_HF_JOB_FLAVOR. Nothing uploaded."
fi
if [ "$TOO_LONG" -eq 1 ]; then
  die "estimated job time $EST_MIN min exceeds LEANKG_HF_JOB_TIMEOUT=$TIMEOUT: raise LEANKG_HF_JOB_TIMEOUT (and check LEANKG_HF_MAX_USD), or measure the rate with --limit 200 and set LEANKG_HF_EST_RATE. Nothing uploaded."
fi

# ---- the commands this run would execute ------------------------------------

UP_CMD=("$HF" buckets cp "$TMP/export.ndjson" "$UP_URI")
JOB_CMD=("$HF" jobs uv run --detach --quiet --flavor "$FLAVOR" --timeout "$TIMEOUT" --attempts 1
  -v "$BUCKET_URI:/data" "$JOB_SCRIPT"
  --model-dir "$JOB_MODEL_PATH" --in "/data/$RUN_REL/export.ndjson" --out "/data/$RUN_REL/vectors.ndjson")
DOWN_CMD=("$HF" buckets cp "$DOWN_URI" "$TMP/vectors.ndjson")
IMPORT_CMD=("$EMBED_BIN" import --project "$PROJECT_DIR" --in "$TMP/vectors.ndjson")
RM_CMD=("$HF" buckets rm --recursive --yes "$BUCKET/$RUN_REL/")

echo "commands:"
show "${UP_CMD[@]}"
show "${JOB_CMD[@]}"
echo "+ $HF jobs wait <job-id>"
echo "+ $HF jobs inspect --json <job-id>"
show "${DOWN_CMD[@]}"
show "${IMPORT_CMD[@]}"
if [ "$KEEP" -eq 1 ]; then
  echo "+ (run directory kept: --keep)"
else
  show "${RM_CMD[@]}"
fi

if [ "$DRY_RUN" -eq 1 ]; then
  echo "dry run: nothing uploaded, no job started"
  exit 0
fi
if [ "$YES" -eq 0 ]; then
  die "re-run with --yes to upload and start the job (nothing uploaded)"
fi

# ---- run --------------------------------------------------------------------

show "${UP_CMD[@]}"
"${UP_CMD[@]}"

START="$(date +%s)"
show "${JOB_CMD[@]}"
JOB_RAW="$("${JOB_CMD[@]}")" \
  || die "job launch failed; nothing imported; run directory hf://buckets/$BUCKET/$RUN_REL kept"
JOB_ID="$(printf '%s\n' "$JOB_RAW" | tail -n 1 | tr -d '[:space:]')"
[ -n "$JOB_ID" ] || die "hf jobs uv run printed no job id; run directory hf://buckets/$BUCKET/$RUN_REL kept"
echo "job started: $JOB_ID"

echo "+ $HF jobs wait $JOB_ID"
if ! "$HF" jobs wait "$JOB_ID"; then
  echo "--- last 50 log lines of $JOB_ID ---"
  "$HF" jobs logs --tail 50 "$JOB_ID" || true
  die "job $JOB_ID did not complete; nothing imported; run directory hf://buckets/$BUCKET/$RUN_REL kept"
fi
END="$(date +%s)"

SECS=$((END - START))
SOURCE="wall time from launch to finish, includes queueing, startup and dependency install"
INSPECT="$("$HF" jobs inspect --json "$JOB_ID" 2>/dev/null || true)"
RUN_SECS="$(printf '%s' "$INSPECT" | job_running_seconds || true)"
if [ -n "$RUN_SECS" ] && [ "$RUN_SECS" -gt 0 ]; then
  SECS="$RUN_SECS"
  SOURCE="job running time from hf jobs inspect"
fi
[ "$SECS" -gt 0 ] || SECS=1
THROUGHPUT="$(awk -v n="$LINES" -v s="$SECS" 'BEGIN { printf "%.1f", n / s }')"
echo "measured: $LINES texts in $SECS s = $THROUGHPUT texts/s ($SOURCE)."
echo "set LEANKG_HF_EST_RATE to the measured rate; a wall-time figure is conservative."

show "${DOWN_CMD[@]}"
"${DOWN_CMD[@]}"
show "${IMPORT_CMD[@]}"
"${IMPORT_CMD[@]}"

if [ "$KEEP" -eq 1 ]; then
  echo "run directory kept: hf://buckets/$BUCKET/$RUN_REL (--keep)"
else
  show "${RM_CMD[@]}"
  "${RM_CMD[@]}"
fi
echo "done: $LINES elements embedded and imported (job $JOB_ID)"
