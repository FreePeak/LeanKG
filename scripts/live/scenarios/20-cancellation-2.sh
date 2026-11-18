# Scenario 20 (issue #589, remaining cancellation work): bounded shutdown and the
# cases that scenario 14 does not cover.
#
# [fixed] assertions FAIL on the baseline binary and PASS on the wave-2 binary:
#  - `leankg run` whose child spawns a grandchild that inherits stdout. The grandchild
#    is `sleep 40.<token>`. A SIGINT must exit 130 within 5 s. Baseline waits for the
#    grandchild to end, so it is killed at the 8 s cap.
#  - the same run where the child exits 0 and the grandchild keeps stdout open. The run
#    must return within 5 s, with the child's stdout and exit 0.
#  - `leankg serve` with a locked telemetry ledger. The ledger is locked by a python3
#    process holding an EXCLUSIVE sqlite transaction, so every ledger write waits on the
#    store's busy timeout. A SIGINT must exit within 5 s and name the abandoned flush on
#    stderr. Baseline waits for the flush (about 30 s), so it is killed at the 15 s cap.
#
# [guard] assertions behave the same on both binaries: the grandchild and server
# preconditions, and a grandchild that ends inside the bound keeps its whole stdout.
#
# [unit-gate] assertions run one unit test and check its PASS line. Each covers a fix
# with no deterministic CLI path:
#  - summarize: a cancelled run keeps the cached entry of the batch it never reached.
#    The cancel lands between two batches only through a fake chat call, not a signal.
#  - maintain.Pass: the checkpoint cancel. Earlier ctx checks in Pass return the cancel
#    first, so a signal cannot isolate the checkpoint step without timing.
#  - gitobj: the cat-file read cancel. The cancel must land inside one pipe read.
#  - index walk: a cancelled ctx stops SupportedFiles. The walk skips FIFOs without
#    opening them, so there is no slow entry that blocks it on a CLI path.
#  - doctor fleet: the deadline message. `leankg doctor` has no deadline flag, and the
#    fixed 10 s probe timeout races the sqlite busy timeout.
#  - leankg-embed: the %w wrapping on the verbs. Export, import and status cannot be
#    cancelled at a known point from a CLI.
#
# Helpers are prefixed g20_ and defined here, not in lib.sh.

G20_TOKENS=""

# g20_sleep_running TOKEN: 0 while the grandchild `sleep 40.<token>` exists.
g20_sleep_running() { [ -n "$(pgrep -f "^sleep 40\.$1\$")" ]; }

# g20_file PATH: 0 while PATH exists (readiness predicate).
g20_file() { [ -f "$1" ]; }

# g20_alive PID: 0 while PID runs. A zombie not yet reaped by this shell is not alive.
g20_alive() {
  local st
  st=$(ps -o stat= -p "$1" 2>/dev/null)
  [ -n "$st" ] && [ "${st#Z}" = "$st" ]
}

# g20_wait_until FN ARG SECS: poll FN ARG every 0.1 s for up to SECS. Sets G20_READY.
g20_wait_until() {
  local i
  G20_READY=no
  for i in $(seq 1 $(($3 * 10))); do
    if "$1" "$2"; then G20_READY=yes; return 0; fi
    sleep 0.1
  done
  return 1
}

# g20_finish PID SECS: poll up to SECS for PID to exit, then kill it if it is still
# alive so the run ends. Sets G20_EXIT (tenth-second tick of the exit, or none) and G20_RC.
g20_finish() {
  local pid=$1 limit=$2 i
  G20_EXIT=none
  for i in $(seq 1 $((limit * 10))); do
    if ! g20_alive "$pid"; then G20_EXIT=$i; break; fi
    sleep 0.1
  done
  if g20_alive "$pid"; then kill -KILL "$pid" 2>/dev/null; fi
  wait "$pid" 2>/dev/null
  G20_RC=$?
}

# g20_within_5s: yes when the last g20_finish saw the exit within 5 s.
g20_within_5s() {
  if [ "$G20_EXIT" != none ] && [ "$G20_EXIT" -le 50 ]; then echo yes; else echo no; fi
}

# g20_capture LABEL: redacted stdout and stderr of a background run into the captures.
# Sets G20_OUT, G20_ERR and LAST_CAPTURE.
g20_capture() {
  local raw="$WORK/raw/$SCENARIO_ID-$1"
  redact <"$raw.stdout" >"$CAP_DIR/$1.stdout"
  redact <"$raw.stderr" >"$CAP_DIR/$1.stderr"
  G20_OUT=$(cat "$raw.stdout")
  G20_ERR=$(cat "$raw.stderr")
  LAST_CAPTURE="scenarios/$SCENARIO_ID/captures/$1.stderr"
}

# g20_grandchild_run_case LABEL SIG CHILD_SCRIPT: start `leankg run -- sh -c CHILD_SCRIPT`
# where CHILD_SCRIPT starts `sleep 40.<token>` in the background. Waits until the
# grandchild runs, sends SIG to leankg if SIG is not empty, and waits up to 8 s for
# leankg to exit.
g20_grandchild_run() {
  local label=$1 sig=$2 script=$3 token pid
  token="$RANDOM$RANDOM$$"
  G20_TOKENS="$G20_TOKENS $token"
  "$LEANKG_BIN" run -- sh -c "${script//TOKEN/$token}" \
    >"$WORK/raw/$SCENARIO_ID-$label.stdout" 2>"$WORK/raw/$SCENARIO_ID-$label.stderr" &
  pid=$!
  echo "$pid" >>"$WORK/servers.pids"
  g20_wait_until g20_sleep_running "$token" 5
  assert_eq "(regression guard) [$label] grandchild sleep runs before leankg exits" yes "$G20_READY"
  if [ -n "$sig" ]; then
    log "\$ leankg run -- sh -c '$script' (pid $pid), $sig once the grandchild runs"
    kill "-$sig" "$pid" 2>/dev/null
  fi
  g20_finish "$pid" 8
  g20_capture "$label"
  log "leankg run [$label] => exit $G20_RC (exited within 5 s: $(g20_within_5s))"
  [ -n "$token" ] && G20_LAST_TOKEN=$token
}

# g20_grandchild_cases: the two `leankg run` cases with a grandchild holding stdout.
g20_grandchild_cases() {
  g20_grandchild_run run-grandchild-int INT 'sleep 40.TOKEN & wait'
  assert_eq "[fixed] leankg run exits 130 on SIGINT while a grandchild holds stdout" 130 "$G20_RC"
  assert_contains "[fixed] leankg run stderr names the cancellation with a grandchild holding stdout" \
    "context canceled" "$G20_ERR"
  assert_eq "[fixed] leankg run exits within 5 s of SIGINT while a grandchild holds stdout" yes "$(g20_within_5s)"

  g20_grandchild_run run-grandchild-exit0 "" 'sleep 40.TOKEN & echo grand-ok'
  assert_eq "[fixed] leankg run exits 0 when the child exits 0 and a grandchild holds stdout" 0 "$G20_RC"
  assert_eq "[fixed] leankg run returns the child stdout when a grandchild holds stdout" grand-ok "$G20_OUT"
  assert_eq "[fixed] leankg run returns within 5 s when the child exited and a grandchild holds stdout" yes "$(g20_within_5s)"
}

# g20_telemetry_case: `leankg serve` with consent granted and a telemetry ledger locked
# by another process. Calls queue events the writer cannot flush. SIGINT must end the
# server within 5 s, and stderr must name the abandoned flush.
g20_telemetry_case() {
  local home="$WORK/s20/home" port pid holder marker log="$WORK/raw/server-$SCENARIO_ID-telemetry.log" i
  if ! command -v python3 >/dev/null 2>&1; then
    skip "python3 is not installed; the ledger holder needs it"
    return 0
  fi
  mkdir -p "$home"
  run_cmd telemetry-enable env LEANKG_HOME="$home" "$LEANKG_BIN" telemetry enable --yes
  assert_eq "telemetry consent is recorded in the case home" 0 "$RC"

  port=$(free_port)
  env LEANKG_HOME="$home" "$LEANKG_BIN" serve --http "127.0.0.1:$port" --project "$FIXTURE" \
    >"$log" 2>&1 &
  pid=$!
  echo "$pid" >>"$WORK/servers.pids"
  for i in $(seq 1 100); do
    curl -fsS -o /dev/null "http://127.0.0.1:$port/health" 2>/dev/null && break
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.2
  done
  assert_eq "[guard] telemetry server answers /health" 0 "$(curl -fsS -o /dev/null "http://127.0.0.1:$port/health" 2>/dev/null; echo $?)"

  marker="$WORK/s20/ledger-locked"
  holder=$(g20_start_ledger_holder "$home/telemetry.db" "$marker")
  g20_wait_until g20_file "$marker" 5
  assert_eq "[guard] ledger lock is held before the calls" yes "$G20_READY"
  for i in 1 2 3; do
    mcp_tool "telemetry-call-$i" "http://127.0.0.1:$port" status '{}'
  done

  kill -INT "$pid" 2>/dev/null
  g20_finish "$pid" 15
  kill -KILL "$holder" 2>/dev/null
  wait "$holder" 2>/dev/null
  redact <"$log" >"$CAP_DIR/telemetry-server.stderr"
  LAST_CAPTURE="scenarios/$SCENARIO_ID/captures/telemetry-server.stderr"
  log "serve with a locked ledger => exit $G20_RC (exited within 5 s: $(g20_within_5s))"
  assert_eq "[fixed] leankg serve exits within 5 s of SIGINT with a locked telemetry ledger" yes "$(g20_within_5s)"
  assert_contains "[fixed] leankg serve names the abandoned ledger flush on stderr" \
    "abandoned" "$(cat "$log")"
}

# g20_start_ledger_holder DB MARKER: start a python3 process that holds an EXCLUSIVE
# sqlite transaction on DB, writes MARKER once the lock is held, and sleeps until
# killed. Prints its pid.
g20_start_ledger_holder() {
  local script="$WORK/s20/ledger_hold.py"
  mkdir -p "$WORK/s20"
  cat >"$script" <<'PY'
import sqlite3
import sys
import time

db, marker = sys.argv[1], sys.argv[2]
conn = sqlite3.connect(db, isolation_level=None, timeout=1)
conn.execute("BEGIN EXCLUSIVE")
open(marker, "w").close()
while True:
    time.sleep(1)
PY
  python3 -I "$script" "$1" "$2" >/dev/null 2>&1 &
  echo "$!" >>"$WORK/servers.pids"
  echo "$!"
}

# g20_unit_gate LABEL PKG TEST REASON: run one unit test with -v and assert its PASS
# line. REASON is the one-line justification for having no CLI assertion.
g20_unit_gate() {
  local label=$1 pkg=$2 test=$3 reason=$4 prev=$PWD
  log "unit-gate: $reason"
  cd "$REPO_ROOT" || return 1
  run_cmd "unit-$test" go test -run "^$test\$" -count=1 -v "$pkg"
  cd "$prev" || return 1
  assert_contains "[unit-gate] $label" "--- PASS: $test " "$OUT"
}

g20_unit_gates() {
  g20_unit_gate "summarize keeps the cached entry of an unreached batch after a cancel" \
    ./internal/summarize TestSynthCacheSurvivesCancelledRun \
    "no CLI path: the cancel must land between two summary batches (fake chat call)"
  g20_unit_gate "maintain.Pass returns the cancel when the checkpoint is cut" \
    ./internal/maintain TestPassReturnsCancelWhenCheckpointIsCut \
    "no CLI path: earlier ctx checks in Pass return the cancel first, so the checkpoint step cannot be isolated"
  g20_unit_gate "gitobj cat-file cancel reports context.Canceled" \
    ./internal/gitobj TestReaderCancelReportsContextCanceled \
    "no CLI path: the cancel must land inside one cat-file pipe read"
  g20_unit_gate "index SupportedFiles stops on a cancelled ctx" \
    ./internal/index TestSupportedFilesStopsOnCancelledCtx \
    "no CLI path: the walk skips FIFOs without opening them, so no entry blocks it"
  g20_unit_gate "doctor fleet deadline is not reported as an unreadable registry" \
    ./internal/doctor TestFleetDeadlineIsNotAnUnreadableRegistry \
    "no CLI path: doctor has no deadline flag and the 10 s probe timeout races the sqlite busy timeout"
  g20_unit_gate "leankg-embed verbs keep context.Canceled through the %w wrapping" \
    ./cmd/leankg-embed TestVerbErrorsKeepCancellation \
    "no CLI path: export, import and status cannot be cancelled at a known point from a shell"
}

scenario_main() {
  trap 'g20_teardown' EXIT
  G20_LAST_TOKEN=""

  if ! command -v python3 >/dev/null 2>&1; then
    skip "python3 is not installed; the grandchild and ledger cases need it"
    return 0
  fi
  ensure_fixture

  g20_grandchild_cases
  run_cmd guard-grandchild-quick "$LEANKG_BIN" run -- sh -c 'sleep 1 & echo quick'
  assert_eq "(regression guard) leankg run exits 0 when a grandchild ends inside the bound" 0 "$RC"
  assert_eq "(regression guard) leankg run keeps the stdout of a grandchild that ends inside the bound" quick "$OUT"

  g20_telemetry_case
  g20_unit_gates
  return 0
}

# g20_teardown: kill any grandchild sleep this scenario started, even one that outlived
# a failed case.
g20_teardown() {
  local t p
  for t in $G20_TOKENS; do
    for p in $(pgrep -f "^sleep 40\.$t\$"); do kill -KILL "$p" 2>/dev/null; done
  done
}
