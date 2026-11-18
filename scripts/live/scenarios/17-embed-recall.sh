# Scenario 17 (issue #587): the embed and dense-recall fixes, against a loopback
# embedding stub written in python3 (no keys, no network). The stub answers
# GET /props (a llama.cpp sidecar) and POST /v1/embeddings, records each request in
# a JSON-lines log, and can hold a request forever when its input carries HANGME.
#  1. [fixed] A dense recall whose row embedding hangs answers within 25 s with the
#     keyword row. Baseline: no deadline on the dense arm, so the recall waits for
#     the stub (the harness cuts its wait at 60 s). [guard] the dense arm did reach
#     the hung embedding, so the bound is not tested on a recall that never embeds.
#  2. [fixed] Four concurrent recalls on a cold provider all embed their query: a
#     caller that arrives while the first /props read is in flight waits for the
#     answer instead of reading the legacy identity and skipping the dense arm.
#     [guard] /props never has more than one request in flight on either build.
#  3. [guard] An element over the 8000-rune cap reaches the stub capped at 8000 and
#     the run reports one truncation (embed.MaxContentChars, the single constant).
#  4. [unit-gate] A stop coinciding with a store error records cancelled. No CLI or
#     HTTP path can make a store error land inside a stop deterministically.
#  5. [unit-gate] Auto-embed starts the provider once. The sidecar adopts a running
#     endpoint, so a second start is not visible as a second process.
scenario_main() {
  local emb_bin base="$WORK/s17" port port2 rest_port rest dir1 dir2 dir3 ctype='Content-Type: application/json'
  local hang within n200 embedded maxflight truncations max_chars p i
  local -a embed_env=(LEANKG_EMBED_PROVIDER=local LEANKG_EMBED_MODEL=fake-embed LEANKG_EMBED_DIMS=8)

  emb_bin="$(dirname "$LEANKG_BIN")/leankg-embed"
  if [ ! -x "$emb_bin" ]; then
    skip "leankg-embed is not beside $LEANKG_BIN; pass --bin with a path whose directory holds leankg-embed"
    return 0
  fi
  if ! command -v python3 >/dev/null 2>&1; then
    skip "python3 is not installed; the stub embeddings endpoint needs it"
    return 0
  fi
  mkdir -p "$base/stub"
  s17_write_stub

  # 1. Hung row embedding: the dense arm is bounded, the keyword row still answers.
  dir1="$base/recall"
  write_fixture "$dir1"
  run_cmd s17-index-recall "$LEANKG_BIN" index "$dir1" --no-embed
  assert_eq "index for the recall fixture exits 0" 0 "$RC"
  port=$(free_port)
  s17_start_stub "$port" "$base/req-recall.log" 0 || { assert_eq "embedding stub listens on loopback" listening "not listening"; return 0; }
  run_cmd s17-calibrate-recall env "${embed_env[@]}" "LEANKG_EMBED_BASE_URL=http://127.0.0.1:$port/v1" \
    "$emb_bin" run --project "$dir1"
  assert_eq "calibration run for the recall fixture exits 0" 0 "$RC"
  s17_stop_stub

  port=$(free_port)
  s17_start_stub "$port" "$base/req-recall.log" 0 || { assert_eq "embedding stub listens on loopback" listening "not listening"; return 0; }
  rest_port=$(free_port)
  export "${embed_env[@]}" "LEANKG_EMBED_BASE_URL=http://127.0.0.1:$port/v1"
  start_server recall "$dir1" --memory --hindsight-compat --embed-provider local --rest "127.0.0.1:$rest_port" || return 0
  unset LEANKG_EMBED_PROVIDER LEANKG_EMBED_MODEL LEANKG_EMBED_DIMS LEANKG_EMBED_BASE_URL
  rest="http://127.0.0.1:$rest_port"
  s17_wait_rest rest-health "$rest"

  http_request s17-retain-hang POST "$rest/v1/default/banks/probe/memories" -H "$ctype" \
    --data-raw '{"items":[{"content":"We store embeddings in pgvector on Postgres. HANGME"}]}'
  assert_eq "retain of the row whose embedding hangs returns 200" 200 "$HTTP_STATUS"
  http_request s17-recall-hang POST "$rest/api/v1/memory/banks/probe/recall" -H "$ctype" --max-time 60 \
    --data-raw '{"query":"which pgvector holds the vectors","limit":3}'
  within=no
  [ "$LAST_MS" -le 25000 ] && within=yes
  assert_eq "[fixed] dense recall with a hung embedding answers within 25 s (took ${LAST_MS} ms)" yes "$within"
  assert_eq "[fixed] the recall answers 200 when its dense embedding hangs" 200 "$HTTP_STATUS"
  assert_contains "[fixed] the recall answer carries the keyword row" "pgvector" "$HTTP_BODY"
  hang=no
  grep -q '"hang": true' "$base/req-recall.log" 2>/dev/null && hang=yes
  assert_eq "[guard] the dense arm reached the hung embedding (the bound is tested)" yes "$hang"
  stop_server
  s17_stop_stub

  # 2. Concurrent callers on a cold provider. The first /props read is slowed to 2 s,
  # so three more recalls arrive while it is in flight.
  dir2="$base/concurrent"
  write_fixture "$dir2"
  run_cmd s17-index-concurrent "$LEANKG_BIN" index "$dir2" --no-embed
  assert_eq "index for the concurrent fixture exits 0" 0 "$RC"
  port=$(free_port)
  s17_start_stub "$port" "$base/req-calibrate-2.log" 0 || { assert_eq "embedding stub listens on loopback" listening "not listening"; return 0; }
  run_cmd s17-calibrate-concurrent env "${embed_env[@]}" "LEANKG_EMBED_BASE_URL=http://127.0.0.1:$port/v1" \
    "$emb_bin" run --project "$dir2"
  assert_eq "calibration run for the concurrent fixture exits 0" 0 "$RC"
  s17_stop_stub

  port2=$(free_port)
  s17_start_stub "$port2" "$base/req-concurrent.log" 2 || { assert_eq "embedding stub listens on loopback" listening "not listening"; return 0; }
  rest_port=$(free_port)
  export "${embed_env[@]}" "LEANKG_EMBED_BASE_URL=http://127.0.0.1:$port2/v1"
  start_server concurrent "$dir2" --memory --hindsight-compat --embed-provider local --rest "127.0.0.1:$rest_port" || return 0
  unset LEANKG_EMBED_PROVIDER LEANKG_EMBED_MODEL LEANKG_EMBED_DIMS LEANKG_EMBED_BASE_URL
  rest="http://127.0.0.1:$rest_port"
  s17_wait_rest rest-health-2 "$rest"
  http_request s17-retain-plain POST "$rest/v1/default/banks/probe/memories" -H "$ctype" \
    --data-raw '{"items":[{"content":"We store embeddings in pgvector on Postgres for search"}]}'
  assert_eq "retain for the concurrent recall returns 200" 200 "$HTTP_STATUS"

  p=""
  for i in alpha beta gamma delta; do
    curl -sS --max-time 60 -o "$WORK/raw/$SCENARIO_ID-recall-$i.body" -w '%{http_code}' -X POST \
      "$rest/api/v1/memory/banks/probe/recall" -H "$ctype" \
      --data-raw "{\"query\":\"which $i database holds the vectors today\",\"limit\":3}" \
      >"$WORK/raw/$SCENARIO_ID-recall-$i.code" 2>/dev/null &
    p="$p $!"
    sleep 0.3
  done
  for i in $p; do wait "$i"; done
  n200=0
  for i in "$WORK/raw/$SCENARIO_ID"-recall-*.code; do
    [ "$(cat "$i")" = 200 ] && n200=$((n200 + 1))
  done
  assert_eq "[guard] all four concurrent recalls answer 200" 4 "$n200"
  embedded=$(jq -r 'select(.route == "embeddings") | .head // empty' "$base/req-concurrent.log" 2>/dev/null | grep -c '^which [a-z]* database')
  assert_eq "[fixed] all four concurrent recalls embed their query (no caller skips the dense arm)" 4 "$embedded"
  maxflight=$(jq -r 'select(.route == "props") | .max_in_flight' "$base/req-concurrent.log" 2>/dev/null | sort -n | tail -1)
  assert_eq "[guard] /props never has more than one request in flight" 1 "$maxflight"
  stop_server
  s17_stop_stub

  # 3. Cap: an element over the 8000-rune constant is sent truncated to it.
  dir3="$base/cap"
  write_fixture "$dir3"
  {
    printf 'package pkg\n\n// Big carries an oversized comment.\nfunc Big() {\n\t// '
    head -c 9000 /dev/zero | tr '\0' 'x'
    printf '\n}\n'
  } >"$dir3/big.go"
  run_cmd s17-index-cap "$LEANKG_BIN" index "$dir3" --no-embed
  assert_eq "index for the cap fixture exits 0" 0 "$RC"
  port=$(free_port)
  s17_start_stub "$port" "$base/req-cap.log" 0 || { assert_eq "embedding stub listens on loopback" listening "not listening"; return 0; }
  run_cmd s17-run-cap env "${embed_env[@]}" "LEANKG_EMBED_BASE_URL=http://127.0.0.1:$port/v1" \
    "$emb_bin" run --project "$dir3"
  assert_eq "embed run for the cap fixture exits 0" 0 "$RC"
  truncations=$(printf '%s' "$OUT" | jq -r '.Truncations' 2>/dev/null)
  assert_eq "[guard] the run reports one truncation for the oversized element" 1 "$truncations"
  max_chars=$(jq -r 'select(.route == "embeddings") | .chars // empty' "$base/req-cap.log" 2>/dev/null | sort -n | tail -1)
  assert_eq "[guard] the stub receives no text longer than the 8000-rune cap" 8000 "$max_chars"
  s17_stop_stub

  # 4 and 5. No CLI or HTTP path reaches these; the unit test runs on this checkout.
  unit_gate_17 "[unit-gate] stop coinciding with a store error records cancelled" ./internal/embed \
    TestRunStopCoincidingWithStoreErrorRecordsCancelled
  unit_gate_17 "[unit-gate] auto-embed starts the provider once" ./internal/rest/auto \
    TestAutoEmbedStartsProviderOnce
}

# unit_gate_17 LABEL PKG TEST: run one unit test in the checkout; assert exit 0 and its PASS line.
unit_gate_17() {
  local label=$1 pkg=$2 test=$3
  cd "$REPO_ROOT" || return 1
  run_cmd "unit-$test" go test -run "^$test\$" -count=1 -v "$pkg"
  assert_eq "$label: go test exits 0" 0 "$RC"
  assert_contains "$label: PASS line for $test" "--- PASS: $test (" "$OUT"
}

# s17_write_stub: the embedding stub. Args PORT LOG PROPS_DELAY_SECS. /props answers a
# llama.cpp GGUF path with n_ctx 8192 after PROPS_DELAY_SECS and counts its requests in
# flight (max_in_flight). /v1/embeddings records each input's length and head; an input
# containing HANGME is recorded as a hang and never answered.
s17_write_stub() {
  cat >"$WORK/s17/stub/stub.py" <<'PY'
import hashlib
import http.server
import json
import sys
import threading
import time

port, log, props_delay = int(sys.argv[1]), sys.argv[2], float(sys.argv[3])
SHA = "0123456789abcdef0123456789abcdef01234567"
lock = threading.Lock()
state = {"inflight": 0, "max": 0}


def record(obj):
    with lock:
        with open(log, "a") as f:
            f.write(json.dumps(obj) + "\n")


class Handler(http.server.BaseHTTPRequestHandler):
    def send_json(self, code, obj):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path.split("?")[0] != "/props":
            return self.send_json(404, {"error": "no route"})
        with lock:
            state["inflight"] += 1
            state["max"] = max(state["max"], state["inflight"])
        time.sleep(props_delay)
        with lock:
            state["inflight"] -= 1
            mx = state["max"]
        record({"route": "props", "max_in_flight": mx})
        self.send_json(200, {"model_path": "/models/snapshots/" + SHA + "/fake-embed.gguf",
                             "default_generation_settings": {"n_ctx": 8192}})

    def do_POST(self):
        n = int(self.headers.get("Content-Length", "0"))
        req = json.loads(self.rfile.read(n) or b"{}")
        inputs = req.get("input", [])
        if isinstance(inputs, str):
            inputs = [inputs]
        for t in inputs:
            record({"route": "embeddings", "chars": len(t), "head": t[:40]})
        if any("HANGME" in t for t in inputs):
            record({"route": "embeddings", "hang": True})
            time.sleep(3600)
            return
        data = []
        for i, t in enumerate(inputs):
            d = hashlib.sha256(t.encode()).digest()
            data.append({"index": i, "embedding": [(b - 127.5) / 127.5 for b in d[:8]]})
        self.send_json(200, {"object": "list", "data": data, "model": "fake-embed"})

    def log_message(self, *a):
        pass


http.server.ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
PY
}

# s17_start_stub PORT LOG PROPS_DELAY: start the stub; sets S17_PID and records it for teardown.
s17_start_stub() {
  local port=$1 log=$2 delay=$3 i
  rm -f "$log"
  python3 -I "$WORK/s17/stub/stub.py" "$port" "$log" "$delay" \
    >"$WORK/raw/stub-$SCENARIO_ID-$port.log" 2>&1 &
  S17_PID=$!
  echo "$S17_PID" >>"$WORK/servers.pids"
  for i in $(seq 1 50); do
    nc -z 127.0.0.1 "$port" >/dev/null 2>&1 && return 0
    sleep 0.1
  done
  return 1
}

# s17_stop_stub: stop the stub started by s17_start_stub.
s17_stop_stub() {
  [ -n "${S17_PID:-}" ] || return 0
  kill "$S17_PID" 2>/dev/null
  wait "$S17_PID" 2>/dev/null
  S17_PID=""
}

# s17_wait_rest LABEL URL: poll the REST listener's /health; sets HTTP_STATUS.
s17_wait_rest() {
  local i
  for i in $(seq 1 100); do
    http_request "$1" GET "$2/health"
    [ "$HTTP_STATUS" = 200 ] && break
    sleep 0.2
  done
}
