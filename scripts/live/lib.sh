#!/usr/bin/env bash
# Shared helpers for the live-test harness. Sourced by run.sh; scenarios call
# these helpers and never redefine them. Not runnable on its own.
#
# Globals set by run.sh before any scenario runs:
#   REPO_ROOT, LEANKG_BIN, WORK (temp root), RUN_DIR (evidence of this run),
#   FIXTURE (shared fixture dir), RESULTS (results TSV), SCENARIO_ID, SCN_DIR,
#   CAP_DIR (evidence captures of the current scenario).
#
# Evidence rule: everything written under RUN_DIR goes through redact(). Raw
# output lives in WORK/raw and is deleted at teardown, so a step can read a
# token it just minted without that token reaching evidence.
#
# Bounds: every curl gets --max-time CURL_MAX_TIME. Every process the harness
# starts is listed in WORK/servers.pids and stopped by stop_pid (TERM, a 5 s
# grace, then KILL). free_port never returns a port twice in one run (WORK/ports).

# Default --max-time for every curl. http_request passes it before the caller's
# own arguments, so a scenario that needs a longer wait passes its own value.
CURL_MAX_TIME=30

# Millisecond wall clock (macOS date has no %N).
now_ms() { perl -MTime::HiRes=time -e 'printf "%d\n", time() * 1000'; }

# Strip secrets and machine-specific roots from anything written as evidence.
redact() {
  sed -E \
    -e 's/lkg_[A-Za-z0-9_-]+/lkg_REDACTED/g' \
    -e 's/(Authorization: Bearer )[^[:space:]"]+/\1REDACTED/Ig' \
    -e 's/(X-CSRF-Token: )[^[:space:]"]+/\1REDACTED/Ig' \
    -e 's/("password" *: *")[^"]*/\1REDACTED/g' \
    -e 's/(LEANKG_ADMIN_PASSWORD=).*/\1REDACTED/g' \
    -e "s|$WORK|<workdir>|g" \
    -e "s|$REPO_ROOT|<repo>|g" \
    -e "s|$LEANKG_BIN|<leankg-bin>|g"
}

# log LINE...: append a redacted line to the scenario log and echo it.
log() {
  local line
  line=$(printf '%s' "$*" | redact)
  printf '%s\n' "$line" >>"$SCN_DIR/scenario.log"
  printf '%s\n' "$line"
}

step() { log ""; log "== $*"; }

# record LABEL EXPECTED ACTUAL STATUS NOTE: one row of results.tsv.
record() {
  local label=$1 expected actual note
  expected=$(printf '%s' "$2" | redact | tr '\t\r\n' '   ' | cut -c1-300)
  actual=$(printf '%s' "$3" | redact | tr '\t\r\n' '   ' | cut -c1-300)
  note=$(printf '%s' "$5" | redact | tr '\t\r\n' '   ' | cut -c1-300)
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$SCENARIO_ID" \
    "$(printf '%s' "$label" | tr '\t\r\n' '   ')" \
    "$expected" "$actual" "$4" "${LAST_MS:-0}" "$note" >>"$RESULTS"
}

# _assert LABEL EXPECTED ACTUAL PASSED(1|0)
_assert() {
  local status=FAIL note=""
  if [ "$4" = 1 ]; then status=PASS; else note=$LAST_CAPTURE; fi
  record "$1" "$2" "$3" "$status" "$note"
  log "$status  $1  (expected: $(printf '%s' "$2" | cut -c1-120) | actual: $(printf '%s' "$3" | cut -c1-120))"
}

# assert_eq LABEL EXPECTED ACTUAL
assert_eq() {
  if [ "$2" = "$3" ]; then _assert "$1" "$2" "$3" 1; else _assert "$1" "$2" "$3" 0; fi
}

# assert_contains LABEL NEEDLE HAYSTACK
assert_contains() {
  case "$3" in
    *"$2"*) _assert "$1" "contains: $2" "$3" 1 ;;
    *) _assert "$1" "contains: $2" "$3" 0 ;;
  esac
}

# assert_json LABEL JQ_FILTER EXPECTED JSON
# The filter's result is compared as text: strings raw, everything else compact JSON.
assert_json() {
  local label=$1 filter=$2 expected=$3 json=$4 actual
  actual=$(printf '%s' "$json" | jq -r "($filter) | if type == \"string\" then . else tojson end" 2>/dev/null) \
    || actual="<jq error>"
  assert_eq "$label" "$expected" "$actual"
}

# skip REASON: the scenario cannot run here; recorded as SKIP, not FAIL.
skip() {
  record "prerequisite" "present" "missing" SKIP "$1"
  log "SKIP  $1"
}

# run_cmd LABEL CMD...: run a command, capture it. Sets RC, OUT, ERR, LAST_MS.
run_cmd() {
  local label=$1 raw t0 t1
  shift
  raw="$WORK/raw/$SCENARIO_ID-$label"
  t0=$(now_ms)
  "$@" >"$raw.stdout" 2>"$raw.stderr"
  RC=$?
  t1=$(now_ms)
  LAST_MS=$((t1 - t0))
  OUT=$(cat "$raw.stdout")
  ERR=$(cat "$raw.stderr")
  redact <"$raw.stdout" >"$CAP_DIR/$label.stdout"
  redact <"$raw.stderr" >"$CAP_DIR/$label.stderr"
  LAST_CAPTURE="scenarios/$SCENARIO_ID/captures/$label.stdout"
  log "\$ $(printf '%q ' "$@")=> exit $RC (${LAST_MS} ms)"
}

# http_request LABEL METHOD URL [curl args...]: sets HTTP_STATUS, HTTP_BODY, RC, LAST_MS.
http_request() {
  local label=$1 method=$2 url=$3 raw t0 t1
  shift 3
  raw="$WORK/raw/$SCENARIO_ID-$label"
  t0=$(now_ms)
  HTTP_STATUS=$(curl -sS --max-time "$CURL_MAX_TIME" -o "$raw.body" -D "$raw.headers" \
    -w '%{http_code}' -X "$method" "$@" "$url" 2>"$raw.curl")
  RC=$?
  t1=$(now_ms)
  LAST_MS=$((t1 - t0))
  HTTP_BODY=$(cat "$raw.body" 2>/dev/null)
  {
    printf '%s %s\n' "$method" "$url"
    printf '%s\n' "$@"
  } | redact >"$CAP_DIR/$label.request"
  {
    redact <"$raw.headers"
    printf '\n'
    redact <"$raw.body"
  } >"$CAP_DIR/$label.response"
  redact <"$raw.curl" >"$CAP_DIR/$label.curl"
  LAST_CAPTURE="scenarios/$SCENARIO_ID/captures/$label.response"
  log "HTTP $method $url -> $HTTP_STATUS (curl rc $RC, ${LAST_MS} ms)"
}

MCP_ID=0

# mcp_request LABEL URL METHOD PARAMS_JSON [TOKEN]: JSON-RPC over streamable HTTP.
# Sets MCP_JSON (the JSON-RPC reply), MCP_ERROR (its error message, if any).
mcp_request() {
  local label=$1 url=$2 method=$3 params=$4 token=${5:-} body
  local auth=()
  MCP_ID=$((MCP_ID + 1))
  body=$(jq -cn --argjson id "$MCP_ID" --arg m "$method" --argjson p "$params" \
    '{jsonrpc: "2.0", id: $id, method: $m, params: $p}')
  if [ -n "$token" ]; then auth=(-H "Authorization: Bearer $token"); fi
  http_request "$label" POST "$url/mcp" \
    -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    "${auth[@]}" --data-raw "$body"
  MCP_JSON=$(sed -n 's/^data: //p' "$WORK/raw/$SCENARIO_ID-$label.body" | tail -1)
  [ -n "$MCP_JSON" ] || MCP_JSON=$HTTP_BODY
  MCP_ERROR=$(printf '%s' "$MCP_JSON" | jq -r '.error.message // empty' 2>/dev/null)
}

# mcp_tool LABEL URL TOOL ARGS_JSON [TOKEN]: tools/call. Sets MCP_RESULT (the tool's text payload).
mcp_tool() {
  local label=$1 url=$2 tool=$3 args=$4 token=${5:-} params
  params=$(jq -cn --arg n "$tool" --argjson a "$args" '{name: $n, arguments: $a}')
  mcp_request "$label" "$url" tools/call "$params" "$token"
  MCP_RESULT=$(printf '%s' "$MCP_JSON" | jq -r '.result.content[0].text // empty' 2>/dev/null)
}

# mcp_initialize LABEL URL PARAMS: one initialize. Succeeds on HTTP 200 with no JSON-RPC error.
mcp_initialize() {
  mcp_request "$1" "$2" initialize "$3"
  [ "$HTTP_STATUS" = 200 ] && [ -z "$MCP_ERROR" ]
}

# retry_until CMD...: run CMD once a second, up to 30 times, until it succeeds. A
# freshly bound MCP listener can refuse the first handshake (see quickstart_smoke.sh);
# the captures keep the last attempt.
retry_until() {
  local i
  for i in $(seq 1 30); do
    "$@" && return 0
    sleep 1
  done
  return 1
}

# free_port: a loopback port nothing is listening on and no earlier call in this
# run has handed out (WORK/ports). Runs in a command substitution, so the record
# goes to a file.
free_port() {
  local p
  while :; do
    p=$((20000 + RANDOM % 40000))
    grep -qx "$p" "$WORK/ports" 2>/dev/null && continue
    nc -z 127.0.0.1 "$p" >/dev/null 2>&1 && continue
    echo "$p" >>"$WORK/ports"
    echo "$p"
    return 0
  done
}

# pid_alive PID: 0 while PID runs. A zombie (exited, not yet reaped) does not count.
pid_alive() {
  local st
  st=$(ps -o stat= -p "$1" 2>/dev/null)
  [ -n "$st" ] && [ "${st#Z}" = "$st" ]
}

# finish_pid PID SECS: wait up to SECS for PID to exit, KILL it if it is still
# running, then reap it. Sets FINISH_TICK (the 0.1 s tick of the exit, or none) and FINISH_RC.
finish_pid() {
  local pid=$1 limit=$2 i
  FINISH_TICK=none
  for i in $(seq 1 $((limit * 10))); do
    if ! pid_alive "$pid"; then FINISH_TICK=$i; break; fi
    sleep 0.1
  done
  if pid_alive "$pid"; then kill -KILL "$pid" 2>/dev/null; fi
  wait "$pid" 2>/dev/null
  FINISH_RC=$?
}

# stop_pid PID: TERM, then up to 5 s for a clean exit, then KILL. Reaps the process.
stop_pid() {
  kill -TERM "$1" 2>/dev/null
  finish_pid "$1" 5
}

# forget_pid PID: drop PID from WORK/servers.pids once it has been stopped.
forget_pid() {
  [ -f "$WORK/servers.pids" ] || return 0
  grep -vx "$1" "$WORK/servers.pids" >"$WORK/servers.pids.new"
  mv "$WORK/servers.pids.new" "$WORK/servers.pids"
}

# start_server LABEL DIR [serve flags...]: starts `leankg serve --http` on a free
# port and waits for /health. Sets SERVER_URL, SERVER_PID. Stopped by stop_server
# or, failing that, by run.sh teardown (WORK/servers.pids).
start_server() {
  local label=$1 dir=$2 i logfile
  shift 2
  local port
  port=$(free_port)
  SERVER_URL="http://127.0.0.1:$port"
  logfile="$WORK/raw/server-$SCENARIO_ID-$label.log"
  "$LEANKG_BIN" serve --http "127.0.0.1:$port" --project "$dir" "$@" >"$logfile" 2>&1 &
  SERVER_PID=$!
  echo "$SERVER_PID" >>"$WORK/servers.pids"
  for i in $(seq 1 100); do
    if curl -fsS --max-time "$CURL_MAX_TIME" -o /dev/null "$SERVER_URL/health" 2>/dev/null; then
      log "server $label ready (log: logs/server-$SCENARIO_ID-$label.log)"
      return 0
    fi
    pid_alive "$SERVER_PID" || break
    sleep 0.2
  done
  LAST_CAPTURE="logs/server-$SCENARIO_ID-$label.log"
  record "server $label starts and answers /health" "ready" "not ready" FAIL "$LAST_CAPTURE"
  log "FAIL  server $label did not become ready (see $LAST_CAPTURE)"
  return 1
}

# wait_listener_health LABEL URL: poll URL/health for up to 20 s. Sets HTTP_STATUS.
# Gives up early when SERVER_PID, if set, has exited.
wait_listener_health() {
  local i
  for i in $(seq 1 100); do
    http_request "$1" GET "$2/health"
    [ "$HTTP_STATUS" = 200 ] && return 0
    if [ -n "${SERVER_PID:-}" ] && ! pid_alive "$SERVER_PID"; then break; fi
    sleep 0.2
  done
  return 1
}

# stop_server: stop the server started by start_server and drop it from servers.pids.
stop_server() {
  [ -n "${SERVER_PID:-}" ] || return 0
  stop_pid "$SERVER_PID"
  forget_pid "$SERVER_PID"
  SERVER_PID=""
}

# unit_gate PKG TEST NOTE [pg]: run one named test in the checkout with go test -v;
# assert exit 0 and its PASS line. "pg" runs it with LEANKG_TEST_PG_URL=LIVE_PG_URL
# (scenarios call the pg form only when LIVE_PG_URL is set).
unit_gate() {
  local pkg=$1 test=$2 note=$3 pg=${4:-}
  if [ "$pg" = pg ]; then
    run_cmd "unit-$test" env LEANKG_TEST_PG_URL="$LIVE_PG_URL" \
      go -C "$REPO_ROOT" test -run "^${test}\$" -count=1 -v "$pkg"
  else
    run_cmd "unit-$test" go -C "$REPO_ROOT" test -run "^${test}\$" -count=1 -v "$pkg"
  fi
  assert_eq "[unit-gate] $note: $test exits 0" 0 "$RC"
  assert_contains "[unit-gate] $note: $test reports PASS" "--- PASS: $test (" "$OUT"
}

# fixture_file NAME: the content of one file of the shared Go fixture. Two
# package-level files call each other with bare names, so the regex tier
# produces cross-file "calls" edges (same shape as the #598 regression test).
fixture_file() {
  case "$1" in
    go.mod) printf 'module example.com/fixture\n\ngo 1.25\n' ;;
    a.go) printf 'package pkg\n\n// Alpha drives the cross-file calls.\nfunc Alpha() {\n\tbetaHelper()\n\tgammaHelper()\n}\n\n// AlphaTwo calls into c.go.\nfunc AlphaTwo() {\n\tgammaHelper()\n}\n' ;;
    b.go) printf 'package pkg\n\n// betaHelper calls back into a.go.\nfunc betaHelper() {\n\talphaTwo()\n}\n\nfunc alphaTwo() {}\n' ;;
    c.go) printf 'package pkg\n\nfunc gammaHelper() {}\n' ;;
    *) echo "fixture_file: no fixture file named $1" >&2; return 1 ;;
  esac
}

# write_fixture DIR: materialise the fixture Go module in DIR.
write_fixture() {
  local f
  mkdir -p "$1"
  for f in go.mod a.go b.go c.go; do
    fixture_file "$f" >"$1/$f"
  done
}

# ensure_fixture: the shared fixture under WORK, indexed once per run (SQLite,
# no embeddings). Idempotent across scenarios because it checks a marker.
ensure_fixture() {
  if [ -f "$WORK/fixture.ready" ]; then return 0; fi
  write_fixture "$FIXTURE"
  run_cmd fixture-index "$LEANKG_BIN" index "$FIXTURE" --no-embed
  if [ "$RC" -eq 0 ]; then touch "$WORK/fixture.ready"; fi
  assert_eq "shared fixture indexes with exit 0" 0 "$RC"
}

# assert_cross_file_callees TAG DIR: Alpha (a.go) must call into b.go and c.go.
assert_cross_file_callees() {
  local tag=$1 dir=$2
  run_cmd "callees-$tag" "$LEANKG_BIN" query --action callees Alpha --project "$dir"
  assert_json "[$tag] Alpha has a cross-file call to b.go::betaHelper" \
    '.callees | any(. == "b.go::betaHelper")' true "$OUT"
  assert_json "[$tag] Alpha has a cross-file call to c.go::gammaHelper" \
    '.callees | any(. == "c.go::gammaHelper")' true "$OUT"
}
