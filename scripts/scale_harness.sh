#!/usr/bin/env bash
# Scale + nested-repo live harness (Tier 2).
#
# Proves LeanKG behaves correctly on a mega-workspace shape WITHOUT touching
# the developer's real index or the 9699 server:
#   * throwaway scratch database (created + dropped here)
#   * dedicated port (default 9798) with a stale-server guard
#   * deterministic nested-repo fixture (scripts/gen_scale_fixture.py)
#   * mega-graph mode exercised by LOWERING LEANKG_MAX_CACHE_ELEMENTS below
#     the fixture's element count — the same code path a 50k-element real
#     repo takes, in ~60 seconds instead of ~25 minutes
#   * verb-envelope MCP queries + the refusal payload asserted at scale
#
# Usage:
#   scripts/scale_harness.sh [--repos N] [--files-per-repo M] [--port P]
#                            [--keep]              # keep fixture dir + DB
#                            [--mega-threshold N]  # default: ELEMENTS/2 (auto)
# Env:
#   LEANKG_SCALE_PG_URL   admin PG URL (default local docker 5433)
#   LEANKG_SCALE_PG_MODE  docker (default, uses docker exec) | direct (psql)
#   LEANKG_BIN            binary path (default release build in cargo cache)
#   DOCKER_PG_CONTAINER   container hosting PG in docker mode (default leankg-pg-500mb)
set -uo pipefail

REPOS=4
FILES=60
PORT=9798
KEEP=0
MEGA_THRESHOLD=0   # 0 = auto-derive as ELEMENTS/2 so mega mode always engages
while [ $# -gt 0 ]; do
  case "$1" in
    --repos) REPOS="$2"; shift 2 ;;
    --files-per-repo) FILES="$2"; shift 2 ;;
    --port) PORT="$2"; shift 2 ;;
    --mega-threshold) MEGA_THRESHOLD="$2"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

HERE="$(cd "$(dirname "$0")" && pwd)"
BIN="${LEANKG_BIN:-$HOME/.cache/cargo-target/leankg-target/release/leankg}"
PG_MODE="${LEANKG_SCALE_PG_MODE:-docker}"
STAMP="$(date +%s)"
SCRATCH_DB="leankg_scale_${STAMP}"
FIXTURE="$(mktemp -d "${TMPDIR:-/tmp}/leankg-scale-${STAMP}.XXXXXX")"
SERVER_PID=""
PASS=0; FAIL=0

# PG access. Local dev talks to the 500MB docker container via docker exec;
# CI (service container) uses psql directly. NOTE: `psql <URI> -d <db>` is a
# conflict (URI positional vs -d option) and silently ignores the URI — so
# direct mode selects the database by rewriting the URI path instead of -d.
if [ "$PG_MODE" = "docker" ]; then
  PGCONTAINER="${DOCKER_PG_CONTAINER:-leankg-pg-500mb}"
  ADMIN_URL="${LEANKG_SCALE_PG_URL:-postgres://postgres:postgres@localhost:5433/leankg}"
  pg_admin()   { docker exec "$PGCONTAINER" psql -U postgres "$@"; }
  pg_scratch() { docker exec "$PGCONTAINER" psql -U postgres -d "$SCRATCH_DB" "$@"; }
  pg_ready()   { docker exec "$PGCONTAINER" pg_isready -U postgres >/dev/null 2>&1; }
else
  ADMIN_URL="${LEANKG_SCALE_PG_URL:?LEANKG_SCALE_PG_URL required in direct mode}"
  pg_admin()   { psql "$ADMIN_URL" "$@"; }
  pg_scratch() { psql "${ADMIN_URL%/*}/$SCRATCH_DB" "$@"; }
  pg_ready()   { psql "$ADMIN_URL" -c 'SELECT 1' >/dev/null 2>&1; }
fi
# App connection points at the scratch DB (strip trailing /db from admin URL;
# admin URLs here carry no query string, so %%\?* is belt-and-braces).
SCRATCH_URL="${ADMIN_URL%%\?*}"; SCRATCH_URL="${SCRATCH_URL%/*}/${SCRATCH_DB}"

# macOS date lacks %N; python timing is portable.
now_ms() { python3 -c 'import time;print(int(time.time()*1000))'; }
ok()   { echo "PASS  $1"; PASS=$((PASS+1)); }
bad()  { echo "FAIL  $1"; FAIL=$((FAIL+1)); }
say()  { echo "----  $1"; }

cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null
  if [ "$KEEP" -eq 0 ]; then
    pg_admin -q -c "DROP DATABASE IF EXISTS $SCRATCH_DB" >/dev/null 2>&1
    rm -rf "$FIXTURE"
  else
    echo "kept: fixture=$FIXTURE db=$SCRATCH_DB (drop with: psql '$ADMIN_URL' -c 'DROP DATABASE $SCRATCH_DB')"
  fi
}
trap cleanup EXIT

[ -x "$BIN" ] || { echo "binary not found: $BIN (cargo build --release first, or set LEANKG_BIN)" >&2; exit 1; }
pg_ready || { echo "Postgres not reachable (mode=$PG_MODE url=$ADMIN_URL)" >&2; exit 1; }

say "1. generate deterministic nested-repo fixture"
python3 "$HERE/gen_scale_fixture.py" "$FIXTURE" --repos "$REPOS" --files-per-repo "$FILES" --noise \
  && ok "fixture generated ($REPOS repos x $FILES files, nested depth 1+3, noise dirs)" \
  || { bad "fixture generation failed"; exit 1; }
[ -f "$FIXTURE/.fixture-manifest" ] || { bad "generator emitted no .fixture-manifest"; exit 1; }

# Every tool call below runs against the scratch DB, never the shared one.
export LEANKG_PG_URL="$SCRATCH_URL"
export LEANKG_AUTO_ATTACH=0

say "2. create scratch database"
pg_admin -q -c "CREATE DATABASE $SCRATCH_DB" >/dev/null 2>&1 \
  && ok "scratch db $SCRATCH_DB created" \
  || { bad "could not create scratch db"; exit 1; }

say "3. init + migrate + full index (timed)"
( cd "$FIXTURE" && "$BIN" init >/dev/null 2>&1 && "$BIN" migrate >/dev/null 2>&1 )
T0=$(now_ms)
( cd "$FIXTURE" && "$BIN" index . >"$FIXTURE/.index.log" 2>&1 )
RC=$?
T1=$(now_ms)
IDX_MS=$((T1-T0))
if [ $RC -eq 0 ]; then ok "full index completed in ${IDX_MS} ms"; else bad "index failed (see $FIXTURE/.index.log)"; tail -5 "$FIXTURE/.index.log"; exit 1; fi

schema=$(pg_scratch -tAc "SELECT nspname FROM pg_namespace WHERE nspname LIKE 'leankg_p_%' LIMIT 1")
ELEMENTS=$(pg_scratch -tAc "SELECT count(*) FROM \"$schema\".code_elements")
# A non-numeric count means the DB round-trip failed — abort rather than let
# an empty ELEMENTS derive threshold 0 (which makes everything vacuously mega).
case "${ELEMENTS:-}" in (''|*[!0-9]*) bad "could not count elements (schema='$schema')"; exit 1;; esac
say "   elements indexed: $ELEMENTS"
[ "$ELEMENTS" -gt 500 ] && ok "element count plausible for fixture ($ELEMENTS)" || bad "element count too low ($ELEMENTS)"
# Auto-derive mega threshold so the assertion holds at any fixture size.
if [ "$MEGA_THRESHOLD" -eq 0 ]; then MEGA_THRESHOLD=$((ELEMENTS / 2)); fi
say "   mega threshold: $MEGA_THRESHOLD"

say "4. nested-repo coverage: every repo from the manifest contributed elements"
MISSING=""
while IFS= read -r r; do
  [ -n "$r" ] || continue
  n=$(pg_scratch -tAc \
    "SELECT count(*) FROM \"$schema\".code_elements WHERE file_path LIKE './$r/%' OR file_path LIKE '%/$r/%'")
  [ "${n:-0}" -eq 0 ] && MISSING="$MISSING $r"
done < "$FIXTURE/.fixture-manifest"
[ -z "$MISSING" ] && ok "all repos from manifest indexed (nested depth 1+3 discovery works)" \
                  || bad "repos with zero elements:$MISSING"

say "5. noise skip: node_modules/target/dist/vendor must NOT be indexed"
NOISE=$(pg_scratch -tAc \
  "SELECT count(*) FROM \"$schema\".code_elements WHERE file_path LIKE '%node_modules%' OR file_path LIKE '%/target/%' OR file_path LIKE '%/dist/%' OR file_path LIKE '%/vendor/%'")
[ "${NOISE:-0}" -eq 0 ] && ok "walker skipped all noise dirs" || bad "$NOISE elements leaked from noise dirs"

say "6. incremental re-index at scale (touch + delete)"
# Derive the delete target from the actual fixture size: a hardcoded m59 is a
# silent no-op at --files-per-repo 30, so the delete half never ran.
VICTIM="$FIXTURE/app-a/src/m$((FILES - 1)).rs"
if [ ! -f "$VICTIM" ]; then
  bad "incremental delete target missing: $VICTIM"
else
  echo "// scale harness touch $STAMP" >> "$FIXTURE/app-a/src/m00.rs"
  rm -f "$VICTIM"
  T0=$(now_ms)
  ( cd "$FIXTURE" && "$BIN" index . --incremental >"$FIXTURE/.incr.log" 2>&1 )
  RC=$?
  T1=$(now_ms)
  if [ $RC -ne 0 ]; then bad "incremental index failed"; tail -5 "$FIXTURE/.incr.log"
  else
    ok "incremental index completed in $((T1-T0)) ms (full was ${IDX_MS} ms)"
  fi
fi

say "7. live server on :$PORT under mega threshold + verb-envelope queries"
# Port guard: a stale server from an earlier run answers health checks and
# then fails every query against its dropped DB — refuse to start instead.
if lsof -nP -iTCP:"$PORT" -sTCP:LISTEN >/dev/null 2>&1; then
  bad "port $PORT already in use — kill the stale server or pass --port"; exit 1
fi
# Server runs UNDER the mega threshold so step 7 also proves mega-mode.
( cd "$FIXTURE" && exec env LEANKG_MAX_CACHE_ELEMENTS="$MEGA_THRESHOLD" \
    "$BIN" mcp-http --port "$PORT" --project "$FIXTURE" --read-only >"$FIXTURE/.server.log" 2>&1 ) &
SERVER_PID=$!
for i in $(seq 60); do curl -sf "localhost:$PORT/health" >/dev/null 2>&1 && break; sleep 0.5; done
curl -sf "localhost:$PORT/health" >/dev/null 2>&1 && ok "server healthy (pid $SERVER_PID)" || bad "server never became healthy"

MCP="localhost:$PORT/mcp?project=$FIXTURE"
HDR=(-H 'content-type: application/json' -H 'accept: application/json, text/event-stream')
sse_json() { python3 -c "
import json,sys
raw=sys.stdin.read()
lines=[l[6:] for l in raw.splitlines() if l.startswith('data: ')]
print(lines[-1] if lines else '{}')"; }

TOOLS=$(curl -s -m 10 -X POST "$MCP" "${HDR[@]}" -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | sse_json \
  | python3 -c "import json,sys; print(len(json.load(sys.stdin).get('result',{}).get('tools',[])))" 2>/dev/null)
[ "${TOOLS:-0}" -eq 1 ] && ok "one-tool registry live at scale" || bad "tools/list returned '$TOOLS'"

SEARCH=$(curl -s -m 10 -X POST "$MCP" "${HDR[@]}" -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"leankg_context","arguments":{"verb":"search_code","query":"op_0","project":"'"$FIXTURE"'"}}}' | sse_json \
  | python3 -c "import json,sys,re; d=json.load(sys.stdin); t=d.get('result',{}).get('content',[{}])[0].get('text','') if 'error' not in d else 'ERR'; m=re.search(r'count: (\d+)',t); print(m.group(1) if m else t[:60])" 2>/dev/null)
[ "${SEARCH:-0}" -gt 0 ] 2>/dev/null && ok "search_code at scale returned $SEARCH hits" || bad "search_code verb call failed: $SEARCH"

IMPACT=$(curl -s -m 10 -X POST "$MCP" "${HDR[@]}" -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"leankg_context","arguments":{"verb":"get_impact_radius","file":"./app-a/src/m01.rs","project":"'"$FIXTURE"'"}}}' | sse_json \
  | python3 -c "import json,sys,re; d=json.load(sys.stdin); t=d.get('result',{}).get('content',[{}])[0].get('text','') if 'error' not in d else 'ERR'; print('HIT' if re.search(r'\./app-a/src/m01\.rs', t) else 'MISS:'+t[:80])" 2>/dev/null)
case "$IMPACT" in HIT) ok "get_impact_radius across nested repos works";; *) bad "impact query failed: $IMPACT";; esac

# Deterministic mega-mode proof: check_consistency ignores its args and runs
# refuse_full_scan_if_mega as its FIRST statement (handler.rs:1401), so under
# the lowered threshold it MUST return the refusal payload — no arg validation
# race, no log grep. Refusals return Ok(refusal): the envelope has no top-level
# "error" key, so assert on the refusal text inside content[0].text.
REFUSE=$(curl -s -m 10 -X POST "$MCP" "${HDR[@]}" -d '{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"leankg_context","arguments":{"verb":"check_consistency","project":"'"$FIXTURE"'"}}}' | sse_json \
  | python3 -c "import json,sys; d=json.load(sys.stdin); t=d.get('result',{}).get('content',[{}])[0].get('text','') if 'error' not in d else 'ERR'; print('REFUSED' if 'refused: graph has' in t else 'NOT_REFUSED:'+t[:80])" 2>/dev/null)
[ "$REFUSE" = "REFUSED" ] && ok "mega-graph mode engaged: full-scan verb refused as designed ($ELEMENTS > $MEGA_THRESHOLD)" \
                          || bad "mega refusal missing: $REFUSE"

say "8. memory sanity: server RSS after scale queries"
RSS_KB=$(ps -o rss= -p "$SERVER_PID" 2>/dev/null | tr -d ' ')
RSS_MB=$(( ${RSS_KB:-0} / 1024 ))
say "   server RSS: ${RSS_MB} MB"
[ "$RSS_MB" -lt 1500 ] && ok "server memory bounded (${RSS_MB} MB)" || bad "server RSS ${RSS_MB} MB — memory regression"

echo
echo "======================================"
echo "scale harness: $PASS passed, $FAIL failed"
echo "======================================"
[ "$FAIL" -eq 0 ]
