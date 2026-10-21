#!/usr/bin/env bash
# scripts/leankg-scan-repos.sh - list the git repositories in the served scope.
#
# SCOPE (the rule of 2026-10-13): LeanKG runs only under ~/work/harvey/freepeak.
# ~/work/be is served by be-kg, its own graph server, and must never enter the
# LeanKG fleet. The live copy of this script scanned $HOME/work, so every git
# repo under ~/work joined the fleet: 287 repos, 231 of them the ~/work/be
# company code that was never in scope.
#
# The default below is the narrow root. LEANKG_SCAN_ROOT (or argv[1]) overrides
# it, so a wider scan stays possible when someone asks for one on purpose.
#
# Output: the repository directories, comma-separated, no trailing comma.
set -euo pipefail

SCAN_ROOT="${1:-${LEANKG_SCAN_ROOT:-$HOME/work/harvey/freepeak}}"

if [ ! -d "$SCAN_ROOT" ]; then
  echo "leankg-scan-repos: scan root does not exist: $SCAN_ROOT" >&2
  exit 1
fi

find "$SCAN_ROOT" -maxdepth 4 -name .git -type d 2>/dev/null \
  | sed 's|/\.git$||' \
  | sort \
  | tr '\n' ',' \
  | sed 's/,$//'
