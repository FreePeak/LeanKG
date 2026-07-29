#!/bin/bash
# Live integration test for the enterprise Docker separation.
# Builds the cozoserver image, runs CRUD against it via the HTTP API,
# restarts the container, and confirms RocksDB data survives.
#
# Requires:
#   - docker (Linux container engine; macOS Docker Desktop works)
#   - bash, curl, jq (optional)
#
# Run from repo root:  bash tests/enterprise_docker/test_live.sh
#
# Skip automatically if docker isn't reachable.

set -u

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

if ! command -v docker >/dev/null 2>&1; then
    echo "SKIP: docker not on PATH"
    exit 0
fi

PASS=0
FAIL=0
COZO_NAME="leankg-cozo-live-test"

# Clean any stale containers / volumes from a previous run.
docker rm -f "$COZO_NAME" >/dev/null 2>&1 || true
docker volume rm leankg-live-cozo-data >/dev/null 2>&1 || true

cleanup() {
    docker rm -f "$COZO_NAME" >/dev/null 2>&1 || true
    docker volume rm leankg-live-cozo-data >/dev/null 2>&1 || true
}
trap cleanup EXIT

step() { echo; echo "=== $* ==="; }

step "1. Build cozoserver image (5-8 min on cold cache)"
if docker build -f "${REPO_ROOT}/Dockerfile.cozoserver" -t freepeak/cozoserver:latest "${REPO_ROOT}" >/dev/null 2>&1; then
    echo "PASS: build succeeded"
    PASS=$((PASS + 1))
else
    echo "FAIL: docker build failed"
    FAIL=$((FAIL + 1))
    exit 1
fi

step "2. Run cozoserver with persistent volume + host networking"
docker run -d --name "$COZO_NAME" \
    --network host \
    -v leankg-live-cozo-data:/data/cozo \
    freepeak/cozoserver:latest >/dev/null 2>&1
sleep 5

if docker inspect --format '{{.State.Running}}' "$COZO_NAME" 2>/dev/null | grep -q true; then
    echo "PASS: container is running"
    PASS=$((PASS + 1))
else
    echo "FAIL: container not running"
    docker logs "$COZO_NAME" 2>&1 | tail -10
    exit 1
fi

# cozoserver v0.7.6 hardcodes the listener at 127.0.0.1:3000; with --network
# host on the host machine, that becomes host's 127.0.0.1:3000.
step "3. GET / on host port 3000"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:3000/ || echo 000)
if [ "$HTTP_CODE" = "200" ]; then
    echo "PASS: HTTP 200 from /"
    PASS=$((PASS + 1))
else
    echo "FAIL: HTTP $HTTP_CODE from / (expected 200)"
    FAIL=$((FAIL + 1))
fi

step "4. CRUD round-trip: :create + :put + read"
SCRIPT_CREATE=':create persist_test { name: String => value: Int }'
SCRIPT_PUT='?[name, value] <- [["alpha", 1], ["beta", 2]] :put persist_test { name => value }'
SCRIPT_READ='?[name, value] := *persist_test[name, value]'

POST_QUERY() {
    curl -s -X POST http://127.0.0.1:3000/text-query \
        -H 'Content-Type: application/json' \
        -d "$1"
}

RESP=$(POST_QUERY "{\"script\":\"${SCRIPT_CREATE}\",\"params\":{}}")
if echo "$RESP" | grep -q '"ok":true'; then
    echo "PASS: create relation"
    PASS=$((PASS + 1))
else
    echo "FAIL: create — $RESP"
    FAIL=$((FAIL + 1))
fi

RESP=$(POST_QUERY "{\"script\":\"${SCRIPT_PUT//\"/\\\"}\",\"params\":{}}")
# Above escaping for double quotes inside double-quoted shell is fragile;
# write the body to a temp file and post with --data-binary.
PUT_BODY=$(mktemp)
cat > "$PUT_BODY" <<JSON
{"script":"?[name, value] <- [[\"alpha\", 1], [\"beta\", 2]] :put persist_test { name => value }","params":{}}
JSON
RESP=$(curl -s -X POST http://127.0.0.1:3000/text-query \
    -H 'Content-Type: application/json' \
    --data-binary "@${PUT_BODY}")
rm -f "$PUT_BODY"
if echo "$RESP" | grep -q '"ok":true'; then
    echo "PASS: insert rows"
    PASS=$((PASS + 1))
else
    echo "FAIL: insert — $RESP"
    FAIL=$((FAIL + 1))
fi

RESP=$(POST_QUERY "{\"script\":\"${SCRIPT_READ}\",\"params\":{}}")
if echo "$RESP" | grep -q 'alpha' && echo "$RESP" | grep -q 'beta'; then
    echo "PASS: read returns alpha + beta"
    PASS=$((PASS + 1))
else
    echo "FAIL: read — $RESP"
    FAIL=$((FAIL + 1))
fi

step "5. Restart container, verify RocksDB persistence"
docker rm -f "$COZO_NAME" >/dev/null 2>&1
docker run -d --name "$COZO_NAME" \
    --network host \
    -v leankg-live-cozo-data:/data/cozo \
    freepeak/cozoserver:latest >/dev/null 2>&1
sleep 5

RESP=$(POST_QUERY "{\"script\":\"${SCRIPT_READ}\",\"params\":{}}")
if echo "$RESP" | grep -q 'alpha' && echo "$RESP" | grep -q 'beta'; then
    echo "PASS: data persisted across restart"
    PASS=$((PASS + 1))
else
    echo "FAIL: data lost after restart — $RESP"
    FAIL=$((FAIL + 1))
fi

step "6. Join cozoserver's netns from another container (sidecar pattern)"
# Use alpine with curl. Drop privileges issue: docker exec as root works.
ALPINE_RESP=$(docker run --rm --network container:"$COZO_NAME" alpine sh -c "
apk add --no-cache curl >/dev/null 2>&1
curl -s -X POST http://127.0.0.1:3000/text-query \
  -H 'Content-Type: application/json' \
  -d '{\"script\":\"?[name, value] := *persist_test[name, value]\",\"params\":{}}'
" 2>&1 | tail -1)
if echo "$ALPINE_RESP" | grep -q 'alpha' && echo "$ALPINE_RESP" | grep -q 'beta'; then
    echo "PASS: sidecar pattern works (joiner hits 127.0.0.1:3000)"
    PASS=$((PASS + 1))
else
    echo "FAIL: joiner cannot reach cozoserver — $ALPINE_RESP"
    FAIL=$((FAIL + 1))
fi

step "Summary"
echo "Passed: $PASS"
echo "Failed: $FAIL"

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
exit 0