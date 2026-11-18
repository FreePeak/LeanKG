# Scenario 10 (issue #587): the embed run ledger. A run stopped by SIGINT exits 130
# and its newest embed_runs row is "cancelled", not "failed", both when the stop
# lands mid-embed and when it lands before the run starts its row (the provider
# probe). A run whose embeddings endpoint is unreachable exits 1 and its newest row
# is "failed". Rows are read back through `leankg-embed status` (last_run).
#
# Binary: leankg-embed is looked up beside the leankg binary given by --bin (or the
# default bin/leankg), so the two must share a directory. No env override: run.sh
# clears every LEANKG_* variable before the scenarios run.
#
# Endpoint: a stub server written in python3, bound to loopback only, with no keys.
# It answers every request only after DELAY seconds, so SIGINT lands while the run
# is waiting on it. The unreachable case points at a loopback port nothing listens on.
scenario_main() {
  local emb_bin dir="$WORK/s10/fixture" port marker
  local -a embed_env=(LEANKG_EMBED_PROVIDER=openai LEANKG_EMBED_MODEL=fake-embed
    LEANKG_EMBED_DIMS=8 LEANKG_EMBED_REQUEST_TIMEOUT_SECS=30)

  emb_bin="$(dirname "$LEANKG_BIN")/leankg-embed"
  if [ ! -x "$emb_bin" ]; then
    skip "leankg-embed is not beside $LEANKG_BIN; pass --bin with a path whose directory holds leankg-embed"
    return 0
  fi
  if ! command -v python3 >/dev/null 2>&1; then
    skip "python3 is not installed; the stub embeddings endpoint needs it"
    return 0
  fi
  mkdir -p "$WORK/s10/stub"
  write_fixture "$dir"
  run_cmd index "$LEANKG_BIN" index "$dir" --no-embed
  assert_eq "index for the ledger fixture exits 0" 0 "$RC"

  # (a) Unreachable endpoint: nothing listens on the port.
  port=$(free_port)
  run_cmd run-unreachable env "${embed_env[@]}" "LEANKG_EMBED_BASE_URL=http://127.0.0.1:$port/v1" \
    "$emb_bin" run --project "$dir"
  assert_eq "run against an unreachable endpoint exits 1" 1 "$RC"
  ledger_status status-after-unreachable "$emb_bin" "$dir"
  assert_eq "newest embed_runs row after an unreachable endpoint is failed" failed "$LAST_RUN_STATUS"

  # (b) SIGINT while the first /embeddings request is in flight (mid-embed).
  marker="$WORK/s10/stub/embed-seen"
  rm -f "$marker"
  port=$(free_port)
  if ! start_stub_embed "$port" "$marker" 60; then
    assert_eq "stub endpoint listens on loopback" listening "not listening"
    return 0
  fi
  interrupt_run run-interrupted "$marker" \
    env "${embed_env[@]}" "LEANKG_EMBED_BASE_URL=http://127.0.0.1:$port/v1" "$emb_bin" run --project "$dir"
  assert_eq "mid-embed run reached the stub before SIGINT" yes "$IR_MARKED"
  assert_eq "mid-embed run exits within 10 s of SIGINT" yes "$IR_STOPPED"
  assert_eq "SIGINT during an embed run exits 130" 130 "$IR_RC"
  ledger_status status-after-interrupt "$emb_bin" "$dir"
  assert_eq "newest embed_runs row after a mid-embed SIGINT is cancelled" cancelled "$LAST_RUN_STATUS"
  stop_stub

  # (c) SIGINT during the provider probe, before the run starts its ledger row. The
  # local provider attached to the stub hangs on GET /props. A fresh project has no
  # earlier rows, so a missing row cannot be mistaken for an old one.
  mkdir -p "$WORK/s10/probe"
  marker="$WORK/s10/stub/probe-seen"
  rm -f "$marker"
  port=$(free_port)
  if ! start_stub_embed "$port" "$marker" 60; then
    assert_eq "stub endpoint listens on loopback" listening "not listening"
    return 0
  fi
  interrupt_run run-probe-interrupted "$marker" \
    env LEANKG_EMBED_PROVIDER=local LEANKG_EMBED_MODEL=fake-embed LEANKG_EMBED_DIMS=8 \
    LEANKG_EMBED_REQUEST_TIMEOUT_SECS=30 "LEANKG_EMBED_BASE_URL=http://127.0.0.1:$port/v1" \
    "$emb_bin" run --project "$WORK/s10/probe"
  assert_eq "probe-stage run reached the stub before SIGINT" yes "$IR_MARKED"
  assert_eq "probe-stage run exits within 10 s of SIGINT" yes "$IR_STOPPED"
  assert_eq "SIGINT during the provider probe exits 130" 130 "$IR_RC"
  ledger_status status-after-probe-interrupt "$emb_bin" "$WORK/s10/probe"
  assert_eq "a run stopped before its ledger row still leaves a cancelled row" cancelled "$LAST_RUN_STATUS"
  stop_stub
}

# ledger_status LABEL EMBED_BIN DIR: sets LAST_RUN_STATUS to the newest embed_runs
# status, read through `leankg-embed status`.
ledger_status() {
  run_cmd "$1" "$2" status --project "$3"
  LAST_RUN_STATUS=$(printf '%s' "$OUT" | jq -r '.last_run.status // "none"' 2>/dev/null || echo "<jq error>")
}

# interrupt_run LABEL MARKER CMD...: run CMD in the background, send SIGINT once
# MARKER exists, and wait up to 10 s for it to exit. Sets IR_RC, IR_MARKED and
# IR_STOPPED (yes|no), and captures stdout and stderr as LABEL.stdout and LABEL.stderr.
interrupt_run() {
  local label=$1 marker=$2 raw pid i
  shift 2
  raw="$WORK/raw/$SCENARIO_ID-$label"
  "$@" >"$raw.stdout" 2>"$raw.stderr" &
  pid=$!
  echo "$pid" >>"$WORK/servers.pids"
  IR_MARKED=no
  for i in $(seq 1 100); do
    if [ -f "$marker" ]; then IR_MARKED=yes; break; fi
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.1
  done
  [ "$IR_MARKED" = yes ] && kill -INT "$pid" 2>/dev/null
  IR_STOPPED=no
  for i in $(seq 1 100); do
    kill -0 "$pid" 2>/dev/null || { IR_STOPPED=yes; break; }
    sleep 0.1
  done
  [ "$IR_STOPPED" = yes ] || kill -KILL "$pid" 2>/dev/null
  wait "$pid"
  IR_RC=$?
  redact <"$raw.stdout" >"$CAP_DIR/$label.stdout"
  redact <"$raw.stderr" >"$CAP_DIR/$label.stderr"
  LAST_CAPTURE="scenarios/$SCENARIO_ID/captures/$label.stderr"
  log "\$ $label: SIGINT once the stub is hit => exit $IR_RC (stopped in time: $IR_STOPPED)"
}

# start_stub_embed PORT MARKER DELAY_SECS: loopback OpenAI-compatible stub. Every
# request writes MARKER and is answered after DELAY_SECS. Sets FAKE_PID; the process
# is also listed in servers.pids for teardown.
start_stub_embed() {
  local port=$1 marker=$2 delay=$3 i
  cat >"$WORK/s10/stub/embed_stub.py" <<'PY'
import http.server
import sys
import time

port, marker, delay = int(sys.argv[1]), sys.argv[2], float(sys.argv[3])


class Handler(http.server.BaseHTTPRequestHandler):
    def handle_any(self):
        if self.command == "POST":
            self.rfile.read(int(self.headers.get("Content-Length", "0")))
        open(marker, "w").close()
        time.sleep(delay)
        body = b'{"data": []}'
        try:
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        except OSError:
            pass

    do_GET = handle_any
    do_POST = handle_any

    def log_message(self, *args):
        pass


http.server.ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
PY
  python3 -I "$WORK/s10/stub/embed_stub.py" "$port" "$marker" "$delay" \
    >"$WORK/raw/stub-embed-$SCENARIO_ID-$port.log" 2>&1 &
  FAKE_PID=$!
  echo "$FAKE_PID" >>"$WORK/servers.pids"
  for i in $(seq 1 50); do
    nc -z 127.0.0.1 "$port" >/dev/null 2>&1 && return 0
    sleep 0.1
  done
  return 1
}

# stop_stub: stop the stub started by start_stub_embed.
stop_stub() {
  [ -n "${FAKE_PID:-}" ] || return 0
  kill "$FAKE_PID" 2>/dev/null
  wait "$FAKE_PID" 2>/dev/null
  FAKE_PID=""
}
