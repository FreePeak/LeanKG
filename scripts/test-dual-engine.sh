#!/usr/bin/env bash
# Dual-engine acceptance gate for the Go engine: unit suite on sqlite, PG
# store tests + end-to-end index/embed/query against a live pgvector
# container. Exit nonzero on any engine failure, printing the captured output
# on every assertion (a bare `grep -q` under pipefail fails silently).
set -euo pipefail
# The Go module is the repository root since #403 (the old `go/` subdirectory
# is gone, which left this gate unrunnable).
cd "$(git rev-parse --show-toplevel)"

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
# sqlite only: with LEANKG_TEST_PG_URL exported, every package's PG tests
# would run here in parallel against one database, and on a fresh database the
# vector extension lands in whichever project schema migrates first.
env -u LEANKG_TEST_PG_URL go test ./... -count=1 -timeout 300s

# Fast-fail if the PG endpoint is unreachable (a wedged Docker daemon would
# otherwise hang every probe for 120s+ and the gate would report a nameless FAIL).
if ! timeout 5 psql "$PG_URL" -At -c "select 1" >/dev/null 2>&1; then
  echo "FAIL: PostgreSQL unreachable at $PG_URL (start the pgvector container: docker compose -f scripts/pgvector.compose.yml up -d)" >&2
  exit 1
fi
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

# Capture the schema THIS RUN created, immediately after the index that made
# it. The previous code picked `tail -1` of every leankg_% schema AT THE END of
# the run: on a database that already holds the operator's project schemas that
# drops the WRONG one — demonstrated live on the dev pgvector, where a decoy
# schema named to sort later was dropped while the run's own leaked. Dropping a
# schema is data loss, so the run now DROPS ONLY WHAT IT CREATED, by deriving
# the name the same way the backend does: leankg_ + sha256(abs project dir)[:16]
# (internal/store/store_pg.go pgSchemaForDir).
PG_SCHEMA="leankg_$(python3 -c '
import hashlib, os, sys
p = os.path.realpath(sys.argv[1])
print(hashlib.sha256(p.encode()).hexdigest()[:16])
' "$SMOKE/proj")"
EXISTS=$(psql "$PG_URL" -At -c "SELECT 1 FROM pg_namespace WHERE nspname='${PG_SCHEMA}'" 2>/dev/null || true)
[ "$EXISTS" = "1" ] || { echo "FAIL: expected schema ${PG_SCHEMA} to exist after the pg index" >&2; exit 1; }
# Prove the derivation matches what the run created: the query above found it.
# The e2e continues; cleanup drops PG_SCHEMA at exit.

out=$(cd "$SMOKE/proj" && LEANKG_DB_ENGINE=postgres LEANKG_PG_URL="$PG_URL" \
  LEANKG_EMBED_PROVIDER=deterministic LEANKG_EMBED_DIMS=16 "$SMOKE/leankg-embed" full 2>&1)
assert_contains "pg embed self-identifies" '"backend":"postgres"' "$out"
assert_contains "pg embed reports 1 element" '"Embedded":1' "$out"

echo "DUAL-ENGINE OK"
