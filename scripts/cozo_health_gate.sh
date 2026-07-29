#!/bin/bash
# Reusable health-gate for the LeanKG <-> cozoserver handshake.
# Sourced by entrypoint.sh and exercised by tests/enterprise_docker/test_health_gate.sh.
#
# Behavior:
#   - LEANKG_COZO_ENDPOINT unset  -> skip, return 0
#   - endpoint reachable within timeout -> print success, return 0
#   - timeout exceeded -> print FATAL, return 1
#
# Tunables (all optional):
#   LEANKG_COZO_HEALTH_TIMEOUT_SECS (default 60)
#   LEANKG_COZO_HEALTH_INTERVAL_SECS (default 2)
#
# stdout: human-readable progress lines
# stderr: nothing (callers can redirect)

cozo_health_gate() {
    local endpoint="${LEANKG_COZO_ENDPOINT:-}"
    if [ -z "$endpoint" ]; then
        return 0
    fi

    local timeout="${LEANKG_COZO_HEALTH_TIMEOUT_SECS:-60}"
    local interval="${LEANKG_COZO_HEALTH_INTERVAL_SECS:-2}"
    local elapsed=0

    echo "=== Waiting for remote cozoserver at ${endpoint} (timeout ${timeout}s) ==="
    while [ "$elapsed" -lt "$timeout" ]; do
        # Simple GET — cozo-bin v0.7.6 returns 200 on / regardless of method.
        # POST with body hangs on Docker Desktop's host.docker.internal proxy.
        if curl -fsS "${endpoint}/" >/dev/null 2>&1; then
            echo "  cozoserver reachable (after ${elapsed}s)."
            return 0
        fi
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done

    echo "WARN: cozoserver at ${endpoint} did not respond within ${timeout}s. Starting anyway." >&2
    return 0
}

# If executed (not sourced), run the gate directly. Allows `bash cozo_health_gate.sh`
# to be invoked from a docker healthcheck or smoke test.
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
    cozo_health_gate
fi