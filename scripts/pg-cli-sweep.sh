#!/usr/bin/env bash
# Phase 5.5 T5.5.2 — CLI feature sweep: every user-facing subcommand that
# touches the graph, run against the cozo shim (path-based .leankg) and
# PostgreSQL (migrate against LEANKG_PG_URL) on a tiny fixture repo.
#
# Usage:
#   scripts/pg-cli-sweep.sh [path-to-leankg-binary] [fixture-dir]
#
# Exit code = number of failed checks (0 = all pass).

set -u
LEANKG_BIN="${1:-$(pwd)/target/release/leankg}"
FIXTURE="${2:-$(mktemp -d /tmp/leankg-cli-sweep.XXXXXX)}"
PG_URL="${LEANKG_PG_URL:-postgresql://postgres:postgres@localhost:5433/leankg}"

PASS=0
FAIL=0
failed=()

check() { # name, expected_exit, actual_exit
  local name="$1" exp="$2" act="$3"
  if [ "$exp" = "$act" ]; then
    PASS=$((PASS + 1)); echo "PASS  $name (exit $act)"
  else
    FAIL=$((FAIL + 1)); failed+=("$name"); echo "FAIL  $name (expected exit $exp, got $act)"
  fi
}

echo "== fixture at $FIXTURE =="
mkdir -p "$FIXTURE/src" "$FIXTURE/docs"
cat > "$FIXTURE/src/main.rs" <<'EOF'
fn main() { let x = helper(1); println!("{}", x); }
fn helper(n: i32) -> i32 { n * 2 }
EOF
printf '# API\n\nThis is the API doc.\n' > "$FIXTURE/docs/api.md"

cd "$FIXTURE"

# ---- cozo (default, path-based) ----
"$LEANKG_BIN" init                                   >/dev/null 2>&1; check "cozo init" 0 $?
"$LEANKG_BIN" index ./src                            >/dev/null 2>&1; check "cozo index" 0 $?
"$LEANKG_BIN" query -- main                          >/dev/null 2>&1; check "cozo query" 0 $?
"$LEANKG_BIN" impact src/main.rs --depth 2           >/dev/null 2>&1; check "cozo impact" 0 $?
"$LEANKG_BIN" gods                                   >/dev/null 2>&1; check "cozo gods" 0 $?
"$LEANKG_BIN" status                                 >/dev/null 2>&1; check "cozo status" 0 $?
"$LEANKG_BIN" check-consistency                      >/dev/null 2>&1; check "cozo check-consistency" 0 $?
"$LEANKG_BIN" index-docs --project .                 >/dev/null 2>&1; check "cozo index-docs" 0 $?
"$LEANKG_BIN" generate                                >/dev/null 2>&1; check "cozo generate" 0 $?
"$LEANKG_BIN" path src/main.rs::main src/main.rs::helper >/dev/null 2>&1; check "cozo path" 0 $?
"$LEANKG_BIN" report                                   >/dev/null 2>&1; check "cozo report" 0 $?

# Explicitly cozo via LEANKG_DB_ENGINE=cozo (matches pre-migration behavior)
LEANKG_DB_ENGINE=cozo "$LEANKG_BIN" status          >/dev/null 2>&1; check "cozo status (engine=cozo)" 0 $?

# ---- PostgreSQL (schema migrations only — CLI graph commands are
#      path-based cozo by design; see src/db/backend.rs resolve_engine) ----
LEANKG_PG_URL="$PG_URL" "$LEANKG_BIN" migrate        >/dev/null 2>&1; check "pg migrate" 0 $?

# mcp-http smoke on an ISOLATED port (never touches :9699)
LEANKG_DB_ENGINE=cozo "$LEANKG_BIN" mcp-http --port 19699 --project . >/dev/null 2>&1 &
MCP_PID=$!
ok=1
for _ in 1 2 3 4 5; do
  sleep 2
  if curl -sf -m 2 http://localhost:19699/health >/dev/null 2>&1; then ok=0; break; fi
done
kill "$MCP_PID" 2>/dev/null; wait "$MCP_PID" 2>/dev/null
check "mcp-http health (port 19699)" 0 "$ok"

echo
echo "== PASS=$PASS FAIL=$FAIL =="
[ "$FAIL" -gt 0 ] && printf 'failed: %s\n' "${failed[@]}"
exit "$FAIL"
