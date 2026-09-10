#!/usr/bin/env bash
# Install the LeanKG Go engine from source (no release artifacts required).
#
# Usage: scripts/install-go.sh [PREFIX]
#   PREFIX defaults to ~/.local/bin (falls back to /usr/local/bin with sudo).
# Requires: Go >= 1.25 (https://go.dev/dl/), git.
set -euo pipefail

REPO_DEFAULT="git@github.com:FreePeak/LeanKG.git"
REPO_URL="${LEANKG_REPO_URL:-$REPO_DEFAULT}"
PREFIX="${1:-$HOME/.local/bin}"

if ! command -v go >/dev/null; then
  echo "error: Go >= 1.25 is required (https://go.dev/dl/)" >&2
  exit 1
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo "==> cloning $REPO_URL"
git clone --depth 1 "$REPO_URL" "$WORK/leankg"
cd "$WORK/leankg/go"

echo "==> building leankg + leankg-embed (CGO_ENABLED=0)"
CGO_ENABLED=0 go build -o "$WORK" ./cmd/leankg ./cmd/leankg-embed

echo "==> installing to $PREFIX"
mkdir -p "$PREFIX"
install -m 0755 "$WORK/leankg" "$PREFIX/leankg"
install -m 0755 "$WORK/leankg-embed" "$PREFIX/leankg-embed"

echo "==> verifying"
"$PREFIX/leankg" doctor --project "$(mktemp -d)" || true

cat <<MSG

Installed:
  $PREFIX/leankg        server (serve/index/writer/doctor/connect/install)
  $PREFIX/leankg-embed  embedding pipeline (run/full/export/import/status)

Next steps:
  leankg install --target claude    # wire your coding tool (6 targets)
  leankg index /path/to/repo        # build a knowledge index
  leankg serve --http :9699 --rest :8080 --memory

MSG
