#!/bin/bash
# Validate that the Docker compose files declared in this branch still parse
# and expose the services / env vars the LeanKG enterprise stack relies on.
#
# Requires docker (uses `docker compose config`, NOT a full `up`).
#
# Run from repo root:  bash tests/enterprise_docker/test_compose_files.sh

set -u

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

PASS=0
FAIL=0

assert_contains() {
    local name=$1
    local needle=$2
    local haystack=$3
    if echo "$haystack" | grep -qF -- "$needle"; then
        echo "PASS: $name"
        PASS=$((PASS + 1))
    else
        echo "FAIL: $name (missing: $needle)"
        FAIL=$((FAIL + 1))
    fi
}

assert_not_contains() {
    local name=$1
    local needle=$2
    local haystack=$3
    if echo "$haystack" | grep -qF -- "$needle"; then
        echo "FAIL: $name (unexpected: $needle)"
        FAIL=$((FAIL + 1))
    else
        echo "PASS: $name"
        PASS=$((PASS + 1))
    fi
}

if ! command -v docker >/dev/null 2>&1; then
    echo "SKIP: docker not on PATH"
    exit 0
fi

# --- docker-compose.enterprise.yml ------------------------------------------
echo "[enterprise]"
ENTERPRISE=$(docker compose -f "${REPO_ROOT}/docker-compose.enterprise.yml" config 2>&1) || {
    echo "FAIL: docker-compose.enterprise.yml failed to parse"
    echo "$ENTERPRISE" | sed 's/^/  /'
    exit 1
}

assert_contains "cozoserver service present" "cozoserver:" "$ENTERPRISE"
assert_contains "leankg service present" "leankg:" "$ENTERPRISE"
assert_contains "cozoserver rocksdb engine" "COZO_ENGINE: rocksdb" "$ENTERPRISE"
# ponytail: cozo-bin v0.7.6 hardcodes :3000 — the compose healthcheck
# must probe 3000, not 9070, until upstream fixes the bind.
assert_contains "cozoserver healthcheck probes :3000" "127.0.0.1:3000" "$ENTERPRISE"
assert_contains "leankg gets cozo endpoint via host.docker.internal" \
    "LEANKG_COZO_ENDPOINT: http://host.docker.internal:3000" "$ENTERPRISE"
# leankg publishes its own ports (9699 MCP + 8080 REST).
assert_contains "leankg publishes mcp :9699" 'published: "9699"' "$ENTERPRISE"
assert_contains "leankg publishes serve :8080" 'published: "8080"' "$ENTERPRISE"
# cozoserver uses host networking (no published ports; binds 127.0.0.1:3000
# on the Docker VM's real loopback, reachable via host.docker.internal).
assert_contains "cozoserver network_mode host" \
    "network_mode: host" "$ENTERPRISE"
# Side mounts present with neutral defaults (/workspace/other, /workspace/other2).
# Local operators override these to real nicknames (e.g. /workspace/be) in
# the gitignored .dockerfile, keeping tracked files free of service nicknames.
assert_contains "side mount /workspace/other present" "/workspace/other" "$ENTERPRISE"
assert_contains "side mount /workspace/other2 present" "/workspace/other2" "$ENTERPRISE"
assert_contains "cozo-data named volume" "cozo-data:" "$ENTERPRISE"
assert_contains "leankg_models named volume" "leankg_models:" "$ENTERPRISE"

# --- docker-compose.rocksdb.yml (single-container baseline) ------------------
echo "[single-container]"
SINGLE=$(docker compose -f "${REPO_ROOT}/docker-compose.rocksdb.yml" config 2>&1) || {
    echo "FAIL: docker-compose.rocksdb.yml failed to parse"
    echo "$SINGLE" | sed 's/^/  /'
    exit 1
}

assert_contains "single-mode still publishes mcp" 'published: "9699"' "$SINGLE"
assert_contains "single-mode has leankg-rocksdb volume" "leankg-rocksdb:" "$SINGLE"
# Single mode must NOT define a cozoserver service (backward compat).
assert_not_contains "single-mode has no cozoserver service" "cozoserver:" "$SINGLE"

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