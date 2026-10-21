#!/usr/bin/env bash
# scripts/leankg-serve-wrapper.sh - the self-host serve wrapper, versioned here.
#
# Install (drop-in for the live copy):
#   install -m 0755 scripts/leankg-serve-wrapper.sh ~/.local/bin/leankg-serve-wrapper
#   install -m 0755 scripts/leankg-scan-repos.sh  ~/.local/bin/leankg-scan-repos
#
# SCOPE: the fleet this wrapper builds is the whole point of the file. LeanKG
# runs only under ~/work/harvey/freepeak (the rule of 2026-10-13); ~/work/be is
# served by be-kg and must not be here. The live wrapper defaulted to
# $HOME/work and swept in every git repo under ~/work - 287 repos, 231 of them
# ~/work/be company code that was never in scope - which is why the served
# fleet answered for projects that were never meant to be served. The default
# below is the narrow root; LEANKG_SCAN_ROOT overrides it (the launchd plist
# may set it explicitly; when it does not, the default is the safe one).
#
# Environment (all optional):
#   LEANKG_SCAN_ROOT       scan root         (default $HOME/work/harvey/freepeak)
#   LEANKG_BIN             leankg binary     (default $HOME/.local/bin/leankg)
#   LEANKG_EMBED_BASE_URL  embedding server  (default http://127.0.0.1:9103/v1)
#   LEANKG_EMBED_MODEL     embedding model   (default embeddinggemma-2)
#   LEANKG_EMBED_DIMS      embedding dims    (default 512)
set -euo pipefail

SCAN_ROOT="${LEANKG_SCAN_ROOT:-$HOME/work/harvey/freepeak}"
LEANKG_BIN="${LEANKG_BIN:-$HOME/.local/bin/leankg}"

if [ ! -d "$SCAN_ROOT" ]; then
  echo "leankg-serve-wrapper: scan root does not exist: $SCAN_ROOT" >&2
  exit 1
fi

# The scan: every git repository at most 4 levels below the root. Kept inline
# (not delegated to leankg-scan-repos) so this file is drop-in usable on its
# own; scripts/test-scan-root.sh proves the two agree.
LEANKG_PROJECT_DIRS="$(find "$SCAN_ROOT" -maxdepth 4 -name .git -type d 2>/dev/null \
  | sed 's|/\.git$||' \
  | sort \
  | tr '\n' ',' \
  | sed 's/,$//')"
export LEANKG_PROJECT_DIRS

project_count="$(printf '%s' "$LEANKG_PROJECT_DIRS" | awk -F, 'NF && length($0) {n++} END {print n+0}')"
if [ "$project_count" -eq 0 ]; then
  echo "leankg-serve-wrapper: no git repo under $SCAN_ROOT - serving the default project only" >&2
fi

# The embedding endpoint (the home-Mac tunnel); every value is overridable.
export LEANKG_EMBED_BASE_URL="${LEANKG_EMBED_BASE_URL:-http://127.0.0.1:9103/v1}"
export LEANKG_EMBED_MODEL="${LEANKG_EMBED_MODEL:-embeddinggemma-2}"
export LEANKG_EMBED_DIMS="${LEANKG_EMBED_DIMS:-512}"

echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) leankg-serve-wrapper: $project_count projects registered under $SCAN_ROOT"

# Loopback binds only: a wildcard bind answers on every interface, and the
# duplicate-server incident (docs/architecture-flow.md, OD1) started with one.
exec "$LEANKG_BIN" serve \
  --http 127.0.0.1:9699 \
  --rest 127.0.0.1:9700 \
  --memory \
  --embed-provider local \
  --hindsight-compat
