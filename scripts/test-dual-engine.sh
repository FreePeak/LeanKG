#!/usr/bin/env bash
# Dual-engine acceptance gate for the Go engine: unit suite on sqlite, PG
# store tests + end-to-end index/embed/query against a live pgvector
# container. Exit nonzero on any engine failure, printing the captured output
# on every assertion (a bare `grep -q` under pipefail fails silently).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)/go"

PG_URL="${LEANKG_TEST_PG_URL:-postgres://postgres:postgres@localhost:5433/leankg?sslmode=disable}"

SMOKE=$(mktemp -d)
PG_SCHEMA=""
cleanup() {
  # Drop the per-run PG schema (the PG backend keys schemas off the project
  # path, so a fresh mktemp dir would otherwise leak a schema per run).
  if [[ -n "$PG_SCHEMA" ]]; then
    psql "$PG_URL" -q -c "DROP SCHEMA IF EXISTS \"$PG_SCHEMA\" CASCADE" >/dev/null 2>&1 || true
  fi
  rm -rf "$SMOKE"
}
trap cleanup EXIT

echo "== build + vet =="
go build ./...
go vet ./...

echo "== unit suite (sqlite) =="
go test ./... -count=1 -timeout 300s

echo "== PG store tests =="
LEANKG_TEST_PG_URL="$PG_URL" go test ./internal/store/ -run TestPG -count=1

echo "== e2e fixture =="
go build -o "$SMOKE" ./cmd/leankg ./cmd/leankg-embed
mkdir -p "$SMOKE/proj"
printf 'package demo\n\n// Hello greets.\nfunc Hello() string { return "hi" }\n' > "$SMOKE/proj/demo.go"

assert_contains() {
  local label="$1" needle="$2" hay="$3"
  if ! grep -qF "$needle" <<<"$hay"; then
    echo "FAIL: $label" >&2
    echo "  wanted: $needle" >&2
    echo "  got:" >&2
    sed 's/^/    /' <<<"$hay" >&2
    exit 1
  fi
}

echo "== end-to-end: sqlite =="
out=$(cd "$SMOKE/proj" && "$SMOKE/leankg" index . \
  && LEANKG_EMBED_PROVIDER=deterministic LEANKG_EMBED_DIMS=16 "$SMOKE/leankg-embed" run 2>&1)
assert_contains "sqlite embed reports 1 element" '"Embedded":1' "$out"

echo "== end-to-end: postgres =="
out=$(cd "$SMOKE/proj" && LEANKG_DB_ENGINE=postgres LEANKG_PG_URL="$PG_URL" "$SMOKE/leankg" index . 2>&1)
assert_contains "pg index ran" "elements=1" "$out"
out=$(cd "$SMOKE/proj" && LEANKG_DB_ENGINE=postgres LEANKG_PG_URL="$PG_URL" \
  LEANKG_EMBED_PROVIDER=deterministic LEANKG_EMBED_DIMS=16 "$SMOKE/leankg-embed" full 2>&1)
assert_contains "pg embed self-identifies" '"backend":"postgres"' "$out"
assert_contains "pg embed reports 1 element" '"Embedded":1' "$out"

# Capture the schema this run created so cleanup can drop it.
PG_SCHEMA=$(psql "$PG_URL" -At -c \
  "SELECT nspname FROM pg_namespace WHERE nspname LIKE 'leankg\_%' ORDER BY nspname" 2>/dev/null | tail -1 || true)

echo "DUAL-ENGINE OK"
