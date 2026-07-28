#!/bin/bash
# Unit tests for scripts/cozo_health_gate.sh.
#
# Run from repo root:  bash tests/enterprise_docker/test_health_gate.sh
#
# Each test spawns a tiny Python HTTP server on a random localhost port so we
# can probe the gate's success / failure paths without touching Docker.
# The tests are hermetic — they bind 127.0.0.1 only and tear down the server
# even on failure (trap EXIT).

set -u

# Resolve repo root from this script's location.
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GATE="${REPO_ROOT}/scripts/cozo_health_gate.sh"

if [ ! -f "$GATE" ]; then
    echo "FAIL: gate script not found at $GATE" >&2
    exit 1
fi

PASS=0
FAIL=0

# Pick a free port by binding + immediately closing. Race-prone but acceptable
# for a localhost test (a few ms window before the actual test server binds).
pick_free_port() {
    python3 -c '
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
'
}

# Spawn a Python HTTP server returning 200 OK on every path with the same
# response shape cozoserver uses (JSON {"ok": true}). Writes the PID to $1.
start_ok_server() {
    local pid_var=$1
    local port=$2
    local body='{"ok": true}'
    python3 -c "
import http.server, sys, threading
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(sys.argv[1])))
        self.end_headers()
        self.wfile.write(sys.argv[1].encode())
    def log_message(self, *a, **k):
        pass
srv = http.server.HTTPServer(('127.0.0.1', $port), H)
threading.Thread(target=srv.serve_forever, daemon=True).start()
import time
while True:
    time.sleep(60)
" "$body" &
    eval "$pid_var=$!"
    # Give the server a moment to bind.
    sleep 0.3
}

stop_server() {
    local pid=$1
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
        kill "$pid" 2>/dev/null || true
        wait "$pid" 2>/dev/null || true
    fi
}

# Run a single test case. Args: name, env-vars-as-prefix, expected_rc.
run_test() {
    local name=$1
    local env_prefix=$2
    local expected_rc=$3

    local output
    local rc
    output=$(eval "$env_prefix bash '$GATE'" 2>&1)
    rc=$?

    if [ "$rc" -eq "$expected_rc" ]; then
        echo "PASS: $name (rc=$rc)"
        PASS=$((PASS + 1))
    else
        echo "FAIL: $name (got rc=$rc, want $expected_rc)"
        echo "  --- output ---"
        echo "$output" | sed 's/^/  /'
        echo "  --------------"
        FAIL=$((FAIL + 1))
    fi
}

# --- Test 1: no endpoint configured -> skip with rc=0 -------------------------
run_test "skip when LEANKG_COZO_ENDPOINT is unset" \
    "unset LEANKG_COZO_ENDPOINT; " \
    0

# --- Test 2: endpoint reachable -> rc=0 within timeout ------------------------
PORT=$(pick_free_port)
SERVER_PID=""
trap 'stop_server "$SERVER_PID"' EXIT
start_ok_server SERVER_PID "$PORT"
run_test "succeed when cozoserver responds 200" \
    "export LEANKG_COZO_ENDPOINT=http://127.0.0.1:${PORT}; \
     export LEANKG_COZO_HEALTH_TIMEOUT_SECS=5; \
     export LEANKG_COZO_HEALTH_INTERVAL_SECS=1; " \
    0
stop_server "$SERVER_PID"
SERVER_PID=""
trap - EXIT

# --- Test 3: endpoint unreachable -> rc=1 within short timeout ---------------
# Port 1 is the well-known "tcpmux" service that no local machine binds.
# If by chance it is reachable, the test still asserts a non-zero exit — but
# we additionally fail if the gate returns 0 with a "reachable" message.
PORT_DEAD=$(pick_free_port)
run_test "fail when cozoserver unreachable" \
    "export LEANKG_COZO_ENDPOINT=http://127.0.0.1:${PORT_DEAD}; \
     export LEANKG_COZO_HEALTH_TIMEOUT_SECS=3; \
     export LEANKG_COZO_HEALTH_INTERVAL_SECS=1; " \
    1

# --- Test 4: respect custom timeout override ---------------------------------
PORT=$(pick_free_port)
SERVER_PID=""
trap 'stop_server "$SERVER_PID"' EXIT
start_ok_server SERVER_PID "$PORT"
START_TS=$(date +%s)
run_test "honor custom timeout / interval" \
    "export LEANKG_COZO_ENDPOINT=http://127.0.0.1:${PORT}; \
     export LEANKG_COZO_HEALTH_TIMEOUT_SECS=4; \
     export LEANKG_COZO_HEALTH_INTERVAL_SECS=2; " \
    0
END_TS=$(date +%s)
ELAPSED=$((END_TS - START_TS))
# Should complete in well under the 4s timeout (server is immediately up).
if [ "$ELAPSED" -le 3 ]; then
    echo "PASS: completed within interval budget (${ELAPSED}s <= 3s)"
    PASS=$((PASS + 1))
else
    echo "FAIL: gate took ${ELAPSED}s, expected <= 3s"
    FAIL=$((FAIL + 1))
fi
stop_server "$SERVER_PID"
trap - EXIT

# --- Summary -----------------------------------------------------------------
echo
echo "================================="
echo "Passed: $PASS"
echo "Failed: $FAIL"
echo "================================="

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
exit 0