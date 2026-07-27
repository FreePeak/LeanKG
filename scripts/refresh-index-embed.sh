#!/usr/bin/env bash
set -euo pipefail

# refresh-index-embed.sh
# Full leankg refresh: index code → index docs → embed in one shot.
#
# Works in two modes:
#   1. Local stdio: uses `cargo run --` automatically (Rust + Cargo required)
#   2. Docker/installed: set LEANKG_BIN=leankg (or put leankg in PATH)
#
# In Docker, also set PROJECT=/workspace (the container mount path).
#
# Usage:
#   ./scripts/refresh-index-embed.sh                                # local
#   LEANKG_BIN=leankg PROJECT=/workspace ./scripts/refresh-index-embed.sh  # Docker
#
#   ./scripts/refresh-index-embed.sh --source git+https://...  # remote git
#   ./scripts/refresh-index-embed.sh --source gs://bucket/     # GCS
#   ./scripts/refresh-index-embed.sh --full                    # full re-embed

PROJECT="${PROJECT:-.}"
SOURCE=""
REF_NAME=""
AUTH=""
FULL=""

# Auto-detect binary: use `leankg` if in PATH, else fallback to cargo run
if [ -n "${LEANKG_BIN:-}" ]; then
  LEANKG_CMD="$LEANKG_BIN"
elif command -v leankg &>/dev/null; then
  LEANKG_CMD="leankg"
else
  LEANKG_CMD="cargo run --"
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    --source) SOURCE="$2"; shift 2 ;;
    --ref-name) REF_NAME="$2"; shift 2 ;;
    --auth) AUTH="$2"; shift 2 ;;
    --full) FULL="--full"; shift ;;
    --project) PROJECT="$2"; shift 2 ;;
    *) echo "Unknown: $1"; exit 1 ;;
  esac
done

cd "$PROJECT"

echo "=== LeanKG Refresh ==="
echo "Project: $(pwd)"
echo "Binary:  $LEANKG_CMD"
echo "Source:  ${SOURCE:-local}"
echo ""

run_leankg() {
  # If using cargo run, pass -- before subcommand to avoid ambiguity
  if [ "$LEANKG_CMD" = "cargo run --" ]; then
    cargo run -- "$@"
  else
    "$LEANKG_CMD" "$@"
  fi
}

if [ -n "$SOURCE" ]; then
  echo "--- Index code from $SOURCE ---"
  run_leankg refresh \
    --source "$SOURCE" \
    ${REF_NAME:+--ref-name "$REF_NAME"} \
    ${AUTH:+--auth "$AUTH"} \
    --project .
else
  echo "--- Index code ---"
  run_leankg index .
  echo "--- Index docs ---"
  if [ -d "docs" ]; then
    run_leankg index-docs --path ./docs --project .
  else
    echo "(no docs/ directory found, skipping)"
  fi
  echo "--- Embed ---"
  run_leankg embed --project . --wait ${FULL:+"--full"}
fi

echo ""
echo "=== Done ==="
