#!/usr/bin/env bash
# Scale + nested-repo live harness (Tier 2) — SQLITE ONLY.
#
# Proves LeanKG behaves correctly on a mega-workspace shape WITHOUT touching
# the developer's real index or any running server:
#   * throwaway fixture dir with its own .leankg sqlite db (deleted at exit)
#   * dedicated port (default 9798) with a stale-server guard
#   * deterministic nested-repo fixture (scripts/gen_scale_fixture.py)
#   * mega-graph mode exercised by LOWERING LEANKG_MAX_CACHE_ELEMENTS below
#     the fixture's element count — the same code path a 50k-element real
#     repo takes, in ~60 seconds instead of ~25 minutes
#   * verb-envelope MCP queries + the refusal payload asserted at scale
#
# No Postgres, no Docker: sqlite is the default storage engine and the only
# one this harness exercises.
#
# Usage:
#   scripts/scale_harness.sh [--repos N] [--files-per-repo M] [--port P]
#                            [--keep]              # keep fixture dir + DB
#                            [--mega-threshold N]  # default: ELEMENTS/2 (auto)
# Env:
#   LEANKG_BIN            binary path (default release build in cargo cache)
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
STAMP="$(date +%s)"
FIXTURE="$(mktemp -d "${TMPDIR:-/tmp}/leankg-scale-${STAMP}.XXXXXX")"
SERVER_PID=""
PASS=0; FAIL=0

# Count/query helper: sqlite queries against the fixture's .leankg db via
# the python3 stdlib (no extra deps). Replaces the old psql probes.
# Cozo relations live inside cozo's serialized sqlite storage — raw SQL
# cannot see them. Count through the binary's engine-aware status output.
status_json() {
  ( cd "$FIXTURE" && "$BIN" status --json 2>/dev/null ) \
    | python3 -c "import json,sys; d=json.load(sys.stdin); print(json.dumps(d.get('projects',[{}])[0]))" 2>/dev/null
}
count_field() { status_json | python3 -c "import json,sys; print(json.load(sys.stdin).get('$1', 0))" 2>/dev/null; }
elements_count() { count_field elements; }
relationships_count() { count_field relationships; }

# macOS date lacks %N; python timing is portable.
now_ms() { python3 -c 'import time;print(int(time.time()*1000))'; }
ok()   { echo "PASS  $1"; PASS=$((PASS+1)); }
bad()  { echo "FAIL  $1"; FAIL=$((FAIL+1)); }
say()  { echo "----  $1"; }

cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null
  if [ "$KEEP" -eq 0 ]; then
    rm -rf "$FIXTURE"
  else
    echo "kept: fixture=$FIXTURE (sqlite db inside .leankg/)"
  fi
}
trap cleanup EXIT

[ -x "$BIN" ] || { echo "binary not found: $BIN (cargo build --release first, or set LEANKG_BIN)" >&2; exit 1; }

say "1. generate deterministic nested-repo fixture"
python3 "$HERE/gen_scale_fixture.py" "$FIXTURE" --repos "$REPOS" --files-per-repo "$FILES" --noise \
  && ok "fixture generated ($REPOS repos x $FILES files, nested depth 1+3, noise dirs)" \
  || { bad "fixture generation failed"; exit 1; }
[ -f "$FIXTURE/.fixture-manifest" ] || { bad "generator emitted no .fixture-manifest"; exit 1; }

# Isolation: sqlite engine, no PG anywhere. LEANKG_DB_ENGINE=sqlite even
# overrides an inherited LEANKG_PG_URL from the environment.
export LEANKG_DB_ENGINE=sqlite
unset LEANKG_PG_URL
export LEANKG_AUTO_ATTACH=0

say "2. scratch storage = fixture-local sqlite (created by init)"

say "3. init + migrate + full index (timed)"
( cd "$FIXTURE" && "$BIN" init >/dev/null 2>&1 && "$BIN" migrate >/dev/null 2>&1 )
T0=$(now_ms)
( cd "$FIXTURE" && "$BIN" index . >"$FIXTURE/.index.log" 2>&1 )
RC=$?
T1=$(now_ms)
IDX_MS=$((T1-T0))
if [ $RC -eq 0 ]; then ok "full index completed in ${IDX_MS} ms"; else bad "index failed (see $FIXTURE/.index.log)"; tail -5 "$FIXTURE/.index.log"; exit 1; fi

ELEMENTS=$(elements_count)
# A non-numeric count means the status round-trip failed — abort rather than
# let an empty ELEMENTS derive threshold 0 (vacuously-mega refusals).
case "${ELEMENTS:-}" in (''|*[!0-9]*) bad "could not count elements via status"; exit 1;; esac
say "   elements indexed: $ELEMENTS"
[ "$ELEMENTS" -gt 500 ] && ok "element count plausible for fixture ($ELEMENTS)" || bad "element count too low ($ELEMENTS)"
# Auto-derive mega threshold so the assertion holds at any fixture size.
if [ "$MEGA_THRESHOLD" -eq 0 ]; then MEGA_THRESHOLD=$((ELEMENTS / 2)); fi
# ELEMENTS=0 is numeric (passes the guard above) but derives 0, which would
# make every graph mega and PASS the refusal vacuously — refuse to proceed.
test "$MEGA_THRESHOLD" -ge 1 || { bad "mega threshold derived as $MEGA_THRESHOLD (elements=$ELEMENTS) — refusal assertion would be vacuous"; exit 1; }
say "   mega threshold: $MEGA_THRESHOLD"

# Steps 4+5 run live: after the server boots, verb queries give engine-aware
# per-repo and noise canary counts (raw sqlite3 cannot see cozo relations).

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
( cd "$FIXTURE" && env LEANKG_MAX_CACHE_ELEMENTS="$MEGA_THRESHOLD" \
    "$BIN" mcp-http --port "$PORT" --project "$FIXTURE" --read-only >"$FIXTURE/.server.log" 2>&1 ) &
SERVER_PID=$!
for i in $(seq 60); do curl -sf "localhost:$PORT/health" >/dev/null 2>&1 && break; sleep 0.5; done
curl -sf "localhost:$PORT/health" >/dev/null 2>&1 && ok "server healthy (pid $SERVER_PID)" || bad "server never became healthy"

# The mcp-http boot runs an AutoAttach (auto-)index for the fixture; verb
# queries before it completes race stale/empty in-memory state (observed:
# canary counts of 0 mid-reindex). Wait for the index to finish: element
# count via status must stabilize above the fixture's plausible floor.
IDX_WAIT_START=$(now_ms)
for i in $(seq 120); do
  N=$( ( cd "$FIXTURE" && "$BIN" status --json 2>/dev/null ) | python3 -c "import json,sys; print(json.load(sys.stdin).get('projects',[{}])[0].get('elements',0))" 2>/dev/null)
  case "${N:-0}" in (''|*[!0-9]*) sleep 1 ;; *) [ "$N" -ge 3000 ] && break ;; esac
  sleep 1
done
say "   auto-index settled: ${N:-0} elements in $(( $(now_ms) - IDX_WAIT_START )) ms"

MCP="localhost:$PORT/mcp?project=$FIXTURE"
HDR=(-H 'content-type: application/json' -H 'accept: application/json, text/event-stream')
sse_json() { python3 -c "
import json,sys
raw=sys.stdin.read()
lines=[l[6:] for l in raw.splitlines() if l.startswith('data: ')]
print(lines[-1] if lines else '{}')"; }

say "4. nested-repo coverage: every repo from the manifest contributed elements"
# Deterministic per-repo probe: get_impact_radius on each repo's m01.rs —
# the verb resolves the file through the graph, so a HIT proves that repo's
# elements are queryable (ranked-search probes are flaky at this boundary).
MISSING=""
while IFS= read -r r; do
  [ -n "$r" ] || continue
  HIT=$(curl -s -m 15 -X POST "$MCP" "${HDR[@]}" \
    -d '{"jsonrpc":"2.0","id":40,"method":"tools/call","params":{"name":"leankg_context","arguments":{"verb":"get_impact_radius","file":"./'"$r"'/src/m01.rs","project":"'"$FIXTURE"'"}}}' | sse_json \
    | python3 -c "import json,sys,re; d=json.load(sys.stdin); t=d.get('result',{}).get('content',[{}])[0].get('text','') if 'error' not in d else 'ERR'; print('HIT' if re.search(r'\\./'+'$r'.replace('.','\\\\.')+r'/src/m01\\.rs', t) else 'MISS')" 2>/dev/null)
  [ "$HIT" != "HIT" ] && MISSING="$MISSING $r"
done < "$FIXTURE/.fixture-manifest"
[ -z "$MISSING" ] && ok "all repos from manifest indexed (nested depth 1+3 discovery works)" \
                  || bad "repos with zero elements:$MISSING"

say "5. noise skip: node_modules/target/dist/vendor must NOT be indexed"
# The fixture drops a canary fn NOISE() inside noise dirs — if the walker
# had indexed any of them, search_code would surface it.
NOISE_HITS=$(curl -s -m 15 -X POST "$MCP" "${HDR[@]}" \
  -d '{"jsonrpc":"2.0","id":50,"method":"tools/call","params":{"name":"get","arguments":{"query":"NOISE"}}}' | sse_json \
  | python3 -c "import json,sys,re; d=json.load(sys.stdin); t=d.get('result',{}).get('content',[{}])[0].get('text','') if 'error' not in d else 'ERR'; m=re.search(r'count: (\\d+)',t); print(m.group(1) if m else ('ERR' if t=='ERR' else t[:40]))" 2>/dev/null)
case "${NOISE_HITS:-ERR}" in
  0) ok "walker skipped all noise dirs (NOISE canary: 0 hits)" ;;
  ERR|"") bad "noise probe failed: '${NOISE_HITS:-none}'" ;;
  *) bad "$NOISE_HITS elements leaked from noise dirs" ;;
esac

TOOLS=$(curl -s -m 10 -X POST "$MCP" "${HDR[@]}" -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | sse_json \
  | python3 -c "import json,sys; print(','.join(sorted(t['name'] for t in json.load(sys.stdin).get('result',{}).get('tools',[]))))" 2>/dev/null)
# #283 3-tool surface: read-only mode hides the write set + \`set\`, exposing
# exactly {get, status}. leankg_context stays callable via tools/call.
[ "${TOOLS:-}" = "get,status" ] && ok "read-only tool registry live at scale (get,status)" || bad "tools/list returned '${TOOLS:-<none>}', expected 'get,status'"

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
SERVER_PROC=$(pgrep -f "mcp-http --port $PORT" | head -1)
RSS_KB=$(ps -o rss= -p "${SERVER_PROC:-$SERVER_PID}" 2>/dev/null | tr -d ' ')
RSS_MB=$(( ${RSS_KB:-0} / 1024 ))
say "   server RSS: ${RSS_MB} MB"
# 3072 MB: mega-mode + ONNX peak varies ~1–2 GB run-to-run and the machine
# may host other workloads; a real regression (the old 10 GB+ leak class)
# sits far above this line.
[ "$RSS_MB" -lt 3072 ] && ok "server memory bounded (${RSS_MB} MB)" || bad "server RSS ${RSS_MB} MB — memory regression"

echo
echo "======================================"
echo "scale harness: $PASS passed, $FAIL failed"
echo "======================================"
[ "$FAIL" -eq 0 ]
