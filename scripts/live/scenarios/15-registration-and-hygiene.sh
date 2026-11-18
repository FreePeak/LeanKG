# Scenario 15: registration (#586) and the #592 hygiene fixes, through the live surfaces.
#  1. Registration over HTTP: a duplicate email answers HTTP 400 with the generic
#     text "registration failed" and does not echo the email (any case). A
#     short-password refusal answers the same status and keeps its own text.
#  2. Docs hygiene: no git conflict marker at line start under docs/ (repo check),
#     and the same check fires on the origin default-branch copy of the p0 analysis
#     doc, which carries real markers (control: proves the detector works).
#  3. archtest guards (#592) run as a [unit-gate]. Static analysis has no runtime path.
#  4. Telemetry consent on a running serve: grant, withdraw, re-grant with SIGHUP,
#     then SIGTERM. The server exits 0 and logs no panic. The #592 Off-construction
#     writer bug has no CLI, HTTP or SIGHUP route (Open and ReloadTelemetry return
#     before NewRecorder for Off), so its regression test is a [unit-gate].
# Labels: [fixed] must FAIL on the baseline binary and PASS on the fix; [guard] must
# give the same result on both; [unit-gate] runs a Go test and asserts its PASS line.
scenario_main() {
  local dir="$WORK/s15/reg" rest_port rest body upper_body echoed rc
  local tel_dir="$WORK/s15/telemetry" tel_home="$WORK/s15/telemetry-home"
  local p0="docs/archive/analysis/p0-embed-and-agent-misuse-2026-07-31.md" origin_ref ctrl

  # --- Part 1: registration over HTTP on a store with no accounts or tokens.
  write_fixture "$dir"
  run_cmd reg-index "$LEANKG_BIN" index "$dir" --no-embed
  assert_eq "index for the registration store exits 0" 0 "$RC"
  rest_port=$(free_port)
  rest="http://127.0.0.1:$rest_port"
  start_server reg "$dir" --rest "127.0.0.1:$rest_port" || return 0

  body='{"email":"dup-reg@example.com","password":"password123","name":"Reg"}'
  http_request reg-first POST "$rest/api/v1/auth/register" \
    -H 'Content-Type: application/json' --data-raw "$body"
  assert_eq "[guard] first registration answers HTTP 200" 200 "$HTTP_STATUS"
  assert_json "[guard] first registration reports success" '.success' true "$HTTP_BODY"

  http_request reg-dup POST "$rest/api/v1/auth/register" \
    -H 'Content-Type: application/json' --data-raw "$body"
  assert_json "[fixed] duplicate registration error is the generic text" \
    '.error' "registration failed" "$HTTP_BODY"
  assert_eq "[guard] duplicate registration answers HTTP 400" 400 "$HTTP_STATUS"
  echoed=no
  case "$(printf '%s' "$HTTP_BODY" | tr 'A-Z' 'a-z')" in
    *dup-reg@example.com*) echoed=yes ;;
  esac
  assert_eq "[fixed] duplicate registration does not echo the email" no "$echoed"

  upper_body='{"email":"DUP-REG@example.com","password":"password123","name":"Reg2"}'
  http_request reg-dup-upper POST "$rest/api/v1/auth/register" \
    -H 'Content-Type: application/json' --data-raw "$upper_body"
  assert_json "[fixed] duplicate registration in upper case gets the same generic text" \
    '.error' "registration failed" "$HTTP_BODY"

  http_request reg-short POST "$rest/api/v1/auth/register" \
    -H 'Content-Type: application/json' \
    --data-raw '{"email":"short-reg@example.com","password":"short","name":"Short"}'
  assert_eq "[guard] other registration failure (short password) answers HTTP 400" 400 "$HTTP_STATUS"
  assert_json "[guard] short-password refusal keeps its specific text" \
    '.error' "password must be at least 8 characters" "$HTTP_BODY"
  stop_server

  # --- Part 2: docs hygiene. The check runs on the checkout; the same check on the
  # origin default-branch copy of the p0 doc must find the markers it is meant to catch.
  run_cmd docs-markers grep -rnE '^(<<<<<<<|>>>>>>>|=======$)' "$REPO_ROOT/docs"
  assert_eq "[guard] no conflict markers at line start under docs/" 1 "$RC"

  origin_ref=$(git -C "$REPO_ROOT" symbolic-ref --short refs/remotes/origin/HEAD 2>/dev/null)
  ctrl="$WORK/s15/origin-p0.md"
  mkdir -p "$WORK/s15"
  git -C "$REPO_ROOT" show "$origin_ref:$p0" >"$ctrl" 2>/dev/null
  run_cmd origin-p0-markers grep -cE '^(<<<<<<<|>>>>>>>|=======$)' "$ctrl"
  assert_eq "[guard] control: origin/default copy of the p0 doc carries markers the check catches" \
    yes "$([ "${OUT:-0}" -gt 0 ] && echo yes || echo no)"

  # --- Part 3: archtest guards. [unit-gate]: static analysis, no runtime path.
  cd "$REPO_ROOT" || return 0
  run_cmd archtest-unit go test ./internal/archtest/... -count=1 -v
  assert_eq "[unit-gate] go test ./internal/archtest/... -count=1 -v exits 0" 0 "$RC"
  assert_contains "[unit-gate] TestDocsHaveNoConflictMarkers PASS line" \
    "--- PASS: TestDocsHaveNoConflictMarkers" "$OUT"

  # --- Part 4: telemetry consent reload on a running serve (#592 path).
  # [unit-gate] for the Off-construction regression: see the header comment.
  run_cmd telemetry-unit go test -run '^TestRecorderRaisedFromOffCloses$' -count=1 -v ./internal/telemetry
  assert_eq "[unit-gate] TestRecorderRaisedFromOffCloses exits 0" 0 "$RC"
  assert_contains "[unit-gate] TestRecorderRaisedFromOffCloses PASS line" \
    "--- PASS: TestRecorderRaisedFromOffCloses" "$OUT"

  write_fixture "$tel_dir"
  run_cmd tel-index "$LEANKG_BIN" index "$tel_dir" --no-embed
  assert_eq "index for the telemetry store exits 0" 0 "$RC"
  mkdir -p "$tel_home"
  export LEANKG_HOME="$tel_home"
  start_server telemetry "$tel_dir" || { unset LEANKG_HOME; return 0; }
  local tel_log="$WORK/raw/server-$SCENARIO_ID-telemetry.log"

  run_cmd tel-enable "$LEANKG_BIN" telemetry enable --yes
  assert_eq "telemetry enable --yes exits 0" 0 "$RC"
  kill -HUP "$SERVER_PID"
  s15_wait_log "consent reloaded at metadata" "$tel_log" 1 15
  assert_eq "[guard] SIGHUP after grant reloads consent (raise from off)" 1 "$S15_FOUND"

  run_cmd tel-disable "$LEANKG_BIN" telemetry disable
  assert_eq "telemetry disable exits 0" 0 "$RC"
  kill -HUP "$SERVER_PID"
  sleep 1

  run_cmd tel-regrant "$LEANKG_BIN" telemetry enable --yes
  assert_eq "telemetry re-grant exits 0" 0 "$RC"
  kill -HUP "$SERVER_PID"
  s15_wait_log "consent reloaded at metadata" "$tel_log" 2 15
  assert_eq "[guard] SIGHUP after re-grant reloads consent again" 1 "$S15_FOUND"

  kill -TERM "$SERVER_PID"
  wait "$SERVER_PID" 2>/dev/null
  rc=$?
  SERVER_PID=""
  unset LEANKG_HOME
  assert_eq "[guard] serve exits 0 after raise, withdraw, re-raise and SIGTERM" 0 "$rc"
  assert_eq "[guard] serve log carries no panic" 0 "$(grep -ci 'panic' "$tel_log" 2>/dev/null || true)"
}

# s15_wait_log PATTERN FILE COUNT SECS: poll FILE until PATTERN appears COUNT times
# or SECS elapse. Sets S15_FOUND to 1 when reached, 0 otherwise.
s15_wait_log() {
  local pattern=$1 file=$2 want=$3 secs=$4 i n
  S15_FOUND=0
  for i in $(seq 1 $((secs * 5))); do
    n=$(grep -c "$pattern" "$file" 2>/dev/null || true)
    if [ "${n:-0}" -ge "$want" ]; then S15_FOUND=1; return 0; fi
    sleep 0.2
  done
  return 0
}
