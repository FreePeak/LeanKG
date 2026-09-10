#!/usr/bin/env bash
# Dual-engine acceptance gate for the Go engine: unit suite on sqlite, PG
# store tests + end-to-end index/embed/query against the live pgvector
# container. Exit nonzero on any engine failure.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)/go"

PG_URL="${LEANKG_TEST_PG_URL:-postgres://postgres:postgres@localhost:5433/leankg?sslmode=disable}"

echo "== build + vet =="
go build ./...
go vet ./...

echo "== unit suite (sqlite) =="
go test ./... -count=1 -timeout 300s

echo "== PG store tests =="
LEANKG_TEST_PG_URL="$PG_URL" go test ./internal/store/ -run TestPG -count=1

echo "== end-to-end: sqlite =="
SMOKE=$(mktemp -d)
trap 'rm -rf "$SMOKE"' EXIT
go build -o "$SMOKE" ./cmd/leankg ./cmd/leankg-embed
mkdir -p "$SMOKE/proj"
printf 'package demo\n\n// Hello greets.\nfunc Hello() string { return "hi" }\n' > "$SMOKE/proj/demo.go"
(cd "$SMOKE/proj" && "$SMOKE/leankg" index . \
  && LEANKG_EMBED_PROVIDER=deterministic LEANKG_EMBED_DIMS=16 "$SMOKE/leankg-embed" run \
  | grep -q '"Embedded":1')

echo "== end-to-end: postgres =="
export LEANKG_DB_ENGINE=postgres LEANKG_PG_URL="$PG_URL"
(cd "$SMOKE/proj" && "$SMOKE/leankg" index . \
  && LEANKG_EMBED_PROVIDER=deterministic LEANKG_EMBED_DIMS=16 "$SMOKE/leankg-embed" run \
  | grep -q '"backend":"postgres"' \
  && LEANKG_EMBED_PROVIDER=deterministic LEANKG_EMBED_DIMS=16 "$SMOKE/leankg-embed" full \
  | grep -q '"Embedded":1')

echo "DUAL-ENGINE OK"
