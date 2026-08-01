#!/usr/bin/env bash
# Build graph-ui and copy its dist into src/embed/3d/ so `leankg serve`
# serves the Track E 3D explorer at /3d/ (FR-E41/E43). Mirrors the ui-v2
# embed step in .github/workflows/release.yml.
#
# Usage: bash scripts/embed-graph-ui.sh
set -euo pipefail
cd "$(dirname "$0")/.."

cd graph-ui
npm ci
npm run build
cd ..

rm -rf src/embed/3d
mkdir -p src/embed/3d
cp -r graph-ui/dist/* src/embed/3d/
test -f src/embed/3d/index.html
echo "graph-ui embedded into src/embed/3d/ ($(du -sh src/embed/3d | cut -f1))"
