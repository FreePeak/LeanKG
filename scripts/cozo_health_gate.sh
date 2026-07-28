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
    local probe_body='{"script":"?[a] := a = 1","params":{}}'

    echo "=== Waiting for remote cozoserver at ${endpoint} (timeout ${timeout}s) ==="
    while [ "$elapsed" -lt "$timeout" ]; do
        if curl -fsS "${endpoint}/" \
                -H 'Content-Type: application/json' \
                -d "$probe_body" \
                >/dev/null 2>&1; then
            echo "  cozoserver reachable (after ${elapsed}s)."
            return 0
        fi
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done

    echo "FATAL: cozoserver at ${endpoint} did not respond within ${timeout}s." >&2
    return 1
}

# If executed (not sourced), run the gate directly. Allows `bash cozo_health_gate.sh`
# to be invoked from a docker healthcheck or smoke test.
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
    cozo_health_gate
fi