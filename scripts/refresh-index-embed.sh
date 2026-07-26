#!/usr/bin/env bash
set -euo pipefail

# refresh-index-embed.sh
# Full leankg refresh: index code → index docs → embed in one shot.
# Optional: pass --source and --auth to sync remote content first.
#
# Usage:
#   ./scripts/refresh-index-embed.sh                          # local project root
#   ./scripts/refresh-index-embed.sh --source git+https://...  # remote git
#   ./scripts/refresh-index-embed.sh --source gs://bucket/     # GCS bucket
#   ./scripts/refresh-index-embed.sh --source git+https://... --ref-name main
#   ./scripts/refresh-index-embed.sh --full                    # full re-embed

PROJECT="${PROJECT:-.}"
SOURCE=""
REF_NAME=""
AUTH=""
FULL=""
ARGS=()

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
echo "Source:  ${SOURCE:-local}"
echo ""

# 1. Index code (with optional remote source)
if [ -n "$SOURCE" ]; then
  echo "--- Index code from $SOURCE ---"
  cargo run -- refresh \
    --source "$SOURCE" \
    ${REF_NAME:+--ref-name "$REF_NAME"} \
    ${AUTH:+--auth "$AUTH"} \
    --project . --wait
else
  echo "--- Index code ---"
  cargo run -- index .
  echo "--- Index docs ---"
  if [ -d "docs" ]; then
    cargo run -- index-docs --path ./docs --project .
  else
    echo "(no docs/ directory found, skipping)"
  fi
  echo "--- Embed ---"
  cargo run -- embed --project . --wait ${FULL:+"--full"}
fi

echo ""
echo "=== Done ==="
