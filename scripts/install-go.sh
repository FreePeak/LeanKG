#!/usr/bin/env bash
# Install the LeanKG Go engine from source (no release artifacts required).
#
# Usage: scripts/install-go.sh [PREFIX]
#   PREFIX defaults to ~/.local/bin (falls back to /usr/local/bin with sudo).
# Requires: Go >= 1.25 (https://go.dev/dl/), git.
set -euo pipefail

REPO_DEFAULT="https://github.com/FreePeak/LeanKG.git"
REPO_URL="${LEANKG_REPO_URL:-$REPO_DEFAULT}"
PREFIX="${1:-$HOME/.local/bin}"

if ! command -v go >/dev/null; then
  echo "error: Go >= 1.25 is required (https://go.dev/dl/)" >&2
  exit 1
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# The clone lives under $WORK/src, not $WORK/leankg: `go build -o $WORK`
# cannot write a binary named after an existing directory.
echo "==> cloning $REPO_URL"
git clone --depth 1 "$REPO_URL" "$WORK/src"
cd "$WORK/src"

echo "==> building leankg + leankg-embed (CGO_ENABLED=0)"
mkdir -p "$WORK/bin"
CGO_ENABLED=0 go build -o "$WORK/bin" ./cmd/leankg ./cmd/leankg-embed

echo "==> installing to $PREFIX"
mkdir -p "$PREFIX"
install -m 0755 "$WORK/bin/leankg" "$PREFIX/leankg"
install -m 0755 "$WORK/bin/leankg-embed" "$PREFIX/leankg-embed"

echo "==> verifying"
"$PREFIX/leankg" version

echo "Installed: $PREFIX/leankg, $PREFIX/leankg-embed"

# Quoted heredoc: the body is literal text, so the backticks around the argv
# below stay readable instead of being run as command substitution.
cat <<'MSG'

Make sure that directory is on your PATH, then from inside a repo:

  leankg index .                        # build the knowledge index (one-time)
  leankg install --target claude-code   # wire your agent (claude-code | cursor | codex | gemini | opencode | omp)

The second command wires `serve --stdio --memory`, so the agent gets both the
code graph and the markdown memory layer. Restart the agent afterwards to pick
the entry up.

Shared server instead of one spawn per client (optional; adds REST + UI ports):
  leankg serve --http :9699 --rest :8080 --ui :8081 --memory
  leankg install --target claude-code --http --url http://127.0.0.1:9699/mcp

Semantic (L3) search needs embeddings: set LEANKG_EMBED_PROVIDER (see the
leankg-embed usage), then run `leankg-embed run`. Without it, queries still
answer from exact + fuzzy matches.

MSG
