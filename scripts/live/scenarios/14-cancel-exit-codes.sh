# Scenario 14 (issue #589): cancellation and exit codes. A SIGINT or SIGTERM to
# `leankg run` kills the child it started, and the run exits 130 with
# "context canceled" on stderr. The same signal to `leankg update --check` exits
# 130 too.
#
# `leankg run`: the child is `sleep 30.<token>`, where the token is unique to this
# case, so pgrep can match only this scenario's child. The signal is sent once that
# child exists.
#
# `leankg update --check`: the update client honours HTTPS_PROXY. The proxy here is a
# loopback CONNECT stub that reads the request and never answers, so the signal lands
# on a request that is already in flight. No network and no timing race.
#
# Not covered: `leankg index` cancelled mid-walk. The point where a walk stops is a
# timing race, so there is no deterministic stop without a product hook.
#
# Helpers are prefixed s14_ and defined here, not in lib.sh.

S14_TOKENS=""

# s14_child_running TOKEN: 0 while the sleep child with this unique duration exists.
s14_child_running() { [ -n "$(pgrep -f "^sleep 30\.$1\$")" ]; }

# s14_kill_token TOKEN: kill any leftover sleep child with this token.
s14_kill_token() {
  local p
  for p in $(pgrep -f "^sleep 30\.$1\$"); do kill -KILL "$p" 2>/dev/null; done
}

# s14_alive PID: 0 while PID runs. A zombie (not yet reaped by this shell) is not alive.
s14_alive() {
  local st
  st=$(ps -o stat= -p "$1" 2>/dev/null)
  [ -n "$st" ] && [ "${st#Z}" = "$st" ]
}

# s14_ready_sleep TOKEN and s14_ready_file PATH: readiness predicates for s14_wait_for.
s14_ready_sleep() { s14_child_running "$1"; }
s14_ready_file() { [ -f "$1" ]; }

# s14_wait_for FN ARG: poll FN ARG for up to 5 s. Sets S14_READY (yes|no).
s14_wait_for() {
  local i
  S14_READY=no
  for i in $(seq 1 50); do
    if "$1" "$2"; then S14_READY=yes; return 0; fi
    sleep 0.1
  done
  return 1
}

# s14_signal SIG PID CHILD_TOKEN: send SIG to PID only, then poll up to 10 s until
# PID has exited and, when CHILD_TOKEN is set, its sleep child is gone. Sets
# S14_EXIT_TICKS and S14_CHILD_TICKS (0.1 s ticks, or none), and S14_RC. A process
# still alive after 10 s is killed so the run ends.
s14_signal() {
  local sig=$1 pid=$2 token=$3 i
  S14_EXIT_TICKS=none
  S14_CHILD_TICKS=none
  kill "-$sig" "$pid" 2>/dev/null
  for i in $(seq 1 100); do
    if [ "$S14_EXIT_TICKS" = none ] && ! s14_alive "$pid"; then S14_EXIT_TICKS=$i; fi
    if [ -n "$token" ] && [ "$S14_CHILD_TICKS" = none ] && ! s14_child_running "$token"; then
      S14_CHILD_TICKS=$i
    fi
    if [ "$S14_EXIT_TICKS" != none ] && { [ -z "$token" ] || [ "$S14_CHILD_TICKS" != none ]; }; then
      break
    fi
    sleep 0.1
  done
  if s14_alive "$pid"; then kill -KILL "$pid" 2>/dev/null; fi
  wait "$pid" 2>/dev/null
  S14_RC=$?
}

# s14_capture LABEL: redacted stdout and stderr of a background run into the captures.
# Sets S14_OUT, S14_ERR and LAST_CAPTURE.
s14_capture() {
  local raw="$WORK/raw/$SCENARIO_ID-$1"
  redact <"$raw.stdout" >"$CAP_DIR/$1.stdout"
  redact <"$raw.stderr" >"$CAP_DIR/$1.stderr"
  S14_OUT=$(cat "$raw.stdout")
  S14_ERR=$(cat "$raw.stderr")
  LAST_CAPTURE="scenarios/$SCENARIO_ID/captures/$1.stderr"
}

# s14_secs TICKS: "N.N s" for a tick count, or "none".
s14_secs() {
  if [ "$1" = none ]; then echo none; else printf '%d.%d s' $(($1 / 10)) $(($1 % 10)); fi
}

# s14_run_case SIG: start `leankg run -- sleep 30.<token>`, signal leankg once the
# child is running, and check the exit status, the message and the child's fate.
s14_run_case() {
  local sig=$1 token pid label="run-$1"
  token="$RANDOM$RANDOM$$"
  S14_TOKENS="$S14_TOKENS $token"
  "$LEANKG_BIN" run -- sleep "30.$token" \
    >"$WORK/raw/$SCENARIO_ID-$label.stdout" 2>"$WORK/raw/$SCENARIO_ID-$label.stderr" &
  pid=$!
  echo "$pid" >>"$WORK/servers.pids"
  s14_wait_for s14_ready_sleep "$token"
  log "\$ leankg run -- sleep 30.<token> (pid $pid), $sig once the child runs"
  assert_eq "[cancel $sig] sleep child started before the signal" yes "$S14_READY"
  s14_signal "$sig" "$pid" "$token"
  s14_capture "$label"
  log "$sig to leankg run => exit $S14_RC (exited: $(s14_secs "$S14_EXIT_TICKS"), child gone: $(s14_secs "$S14_CHILD_TICKS"))"
  assert_eq "[cancel $sig] leankg run exits 130 on $sig (cancelled, not a plain failure)" 130 "$S14_RC"
  assert_contains "[cancel $sig] leankg run stderr names the cancellation" "context canceled" "$S14_ERR"
  assert_eq "[cancel $sig] leankg run exits within 10 s of $sig" yes \
    "$([ "$S14_EXIT_TICKS" != none ] && echo yes || echo no)"
  assert_eq "[cancel $sig] sleep child is gone within 5 s of $sig" yes \
    "$([ "$S14_CHILD_TICKS" != none ] && [ "$S14_CHILD_TICKS" -le 50 ] && echo yes || echo no)"
  s14_kill_token "$token"
}

# s14_update_case SIG: start `leankg update --check` through a loopback CONNECT proxy
# that holds the request open, signal leankg once the request has reached it, and
# check the exit status and the message.
s14_update_case() {
  local sig=$1 port pid stub i marker label="update-$1" stub_log
  marker="$WORK/s14/connect-seen-$sig"
  stub_log="$WORK/raw/stub-$SCENARIO_ID-$sig.log"
  rm -f "$marker"
  port=$(free_port)
  python3 -I "$WORK/s14/connect_hold.py" "$port" "$marker" >"$stub_log" 2>&1 &
  stub=$!
  echo "$stub" >>"$WORK/servers.pids"
  for i in $(seq 1 50); do
    nc -z 127.0.0.1 "$port" >/dev/null 2>&1 && break
    sleep 0.1
  done
  env -u NO_PROXY -u no_proxy -u GITHUB_TOKEN \
    HTTPS_PROXY="http://127.0.0.1:$port" https_proxy="http://127.0.0.1:$port" \
    "$LEANKG_BIN" update --check >"$WORK/raw/$SCENARIO_ID-$label.stdout" 2>"$WORK/raw/$SCENARIO_ID-$label.stderr" &
  pid=$!
  echo "$pid" >>"$WORK/servers.pids"
  s14_wait_for s14_ready_file "$marker"
  log "\$ leankg update --check via a holding CONNECT proxy (pid $pid), $sig once the request is in flight"
  assert_eq "[cancel $sig] update --check reached the proxy before the signal" yes "$S14_READY"
  s14_signal "$sig" "$pid" ""
  s14_capture "$label"
  log "$sig to leankg update => exit $S14_RC (exited: $(s14_secs "$S14_EXIT_TICKS"))"
  assert_eq "[cancel $sig] leankg update --check exits 130 on $sig" 130 "$S14_RC"
  # update wraps the cancel cause, which is "interrupt/terminated signal received",
  # not "context canceled"; identical on baseline and wave1.
  assert_contains "(regression guard) [cancel $sig] leankg update stderr names the cancellation" \
    "signal received" "$S14_ERR"
  assert_eq "[cancel $sig] leankg update exits within 10 s of $sig" yes \
    "$([ "$S14_EXIT_TICKS" != none ] && echo yes || echo no)"
  kill -KILL "$stub" 2>/dev/null
  wait "$stub" 2>/dev/null
  return 0
}

scenario_main() {
  local sig
  trap 's14_teardown' EXIT

  if ! command -v python3 >/dev/null 2>&1; then
    skip "python3 is not installed; the update case needs it for the holding proxy"
    return 0
  fi
  mkdir -p "$WORK/s14"
  cat >"$WORK/s14/connect_hold.py" <<'PY'
import socket
import sys

port, marker = int(sys.argv[1]), sys.argv[2]
srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
srv.bind(("127.0.0.1", port))
srv.listen(8)
while True:
    conn, _ = srv.accept()
    conn.recv(65536)
    open(marker, "w").close()
PY

  # Regression guards: exit codes that must not change, identical on both binaries.
  run_cmd guard-run-ok "$LEANKG_BIN" run -- echo guard-ok
  assert_eq "(regression guard) leankg run -- echo exits 0" 0 "$RC"
  assert_eq "(regression guard) leankg run -- echo prints the child stdout" guard-ok "$OUT"
  run_cmd guard-run-exit "$LEANKG_BIN" run -- sh -c 'exit 3'
  assert_eq "(regression guard) leankg run propagates the child exit status" 3 "$RC"
  assert_contains "(regression guard) leankg run reports the child status on stderr" "exit code 3" "$ERR"
  run_cmd guard-run-usage "$LEANKG_BIN" run
  assert_eq "(regression guard) leankg run with no command exits 2" 2 "$RC"

  for sig in INT TERM; do
    s14_run_case "$sig"
  done
  for sig in INT TERM; do
    s14_update_case "$sig"
  done
  return 0
}

# s14_teardown: kill any sleep child this scenario started, even one that outlived a
# failed case.
s14_teardown() {
  local t
  for t in $S14_TOKENS; do s14_kill_token "$t"; done
}
