#!/usr/bin/env bash
# Deterministic perf workload collector for the perf gate (H8/CORE-6).
# Runs against a scratch fixture project and prints a JSON metrics object:
#   {"index":ms,"server_boot":ms,"search_code":ms,"get_impact_radius":ms}
# Usage: run_perf_workload.sh <fixture_root> <leankg_binary> <port>
set -euo pipefail
FIXTURE="${1:?fixture root}"
BIN="${2:?leankg binary}"
PORT="${3:-9797}"

command -v curl >/dev/null || { echo "curl required" >&2; exit 1; }

gen_fixture() {
  # generator chatter goes to STDERR — stdout must carry only the final JSON
  python3 "$(dirname "$0")/gen_perf_fixture.py" "$FIXTURE/src" "${PERF_FILES:-20}" >&2
}

median3() { # median-of-3 wall-clock ms for "$@" (a command)
  local vals=() s e i
  for i in 1 2 3; do
    s=$(date +%s%3N); "$@" >/dev/null 2>&1 || true; e=$(date +%s%3N)
    vals+=( $((e - s)) )
  done
  printf '%s\n' "${vals[@]}" | sort -n | sed -n 2p
}

wait_health() {
  local i
  for i in $(seq 240); do
    curl -sf "localhost:$PORT/health" >/dev/null 2>&1 && return 0
    sleep 0.5
  done
  echo "server failed to become healthy on :$PORT" >&2; return 1
}

call_tool() { # tool_name arguments_json
  curl -sf -X POST "localhost:$PORT/mcp" \
    -H 'content-type: application/json' \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"$1\",\"arguments\":$2}}"
}

main() {
  rm -rf "$FIXTURE"; mkdir -p "$FIXTURE"
  gen_fixture

  ( cd "$FIXTURE"
    "$BIN" init >/dev/null
    "$BIN" migrate >/dev/null
  )

  local idx_t boot_t sc_t imp_t
  idx_t=$(median3 bash -c "cd '$FIXTURE' && '$BIN' index ./src")

  # server_boot: time until /health answers (fresh process each try)
  local vals=() s e i
  for i in 1 2 3; do
    s=$(date +%s%3N)
    ( cd "$FIXTURE" && "$BIN" mcp-http --port "$PORT" >/dev/null 2>&1 & echo $! > /tmp/perf-gate-server.pid )
    wait_health
    e=$(date +%s%3N)
    kill "$(cat /tmp/perf-gate-server.pid)" 2>/dev/null || true
    sleep 0.5
    vals+=( $((e - s)) )
  done
  boot_t=$(printf '%s\n' "${vals[@]}" | sort -n | sed -n 2p)

  # restart once for query timings
  ( cd "$FIXTURE" && "$BIN" mcp-http --port "$PORT" >/dev/null 2>&1 & echo $! > /tmp/perf-gate-server.pid )
  wait_health
  sc_t=$(median3 call_tool search_code '{"query":"fn"}')

  local qn
  qn=$(call_tool get_code_tree '{"limit":5}' | grep -oE '"[a-z0-9_]+\.rs"' | head -1 | tr -d '"' || true)
  if [ -n "$qn" ]; then
    imp_t=$(median3 call_tool get_impact_radius "{\"file\":\"src/$qn\",\"depth\":1}")
  else
    imp_t="$sc_t"
  fi
  kill "$(cat /tmp/perf-gate-server.pid)" 2>/dev/null || true

  python3 -c "
import json,sys
sys.stdout.write(json.dumps({'index':int('$idx_t'),'server_boot':int('$boot_t'),'search_code':int('$sc_t'),'get_impact_radius':int('$imp_t')}))"
}

main
