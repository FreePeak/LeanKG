#!/usr/bin/env bash
# ttfv_smoke.sh — issue #280: CI-timed published TTFV for the Go engine.
#
# Replaces the Rust-era quickstart_smoke.sh: measures the Go cold happy path
# end-to-end and fails over budget:
#   install (CGO_ENABLED=0 go build ./cmd/leankg ./cmd/leankg-embed)
#   -> fixture (pinned examples/go-api-service copy, no .leankg state)
#   -> leankg index
#   -> leankg serve --http --rest bind (both /health green)
#   -> first useful query over REST  (POST /api/v1/query, hits >= 1)
#   -> first useful query over MCP   (initialize/tools-list/tools-call, hits >= 1)
#
# Usage:
#   scripts/ttfv_smoke.sh [--json OUT.json]
#
# Env:
#   TTFV_BUDGET      wall-clock budget seconds (default 300)
#   TTFV_MCP_PORT    MCP streamable-HTTP port (default 9711)
#   TTFV_REST_PORT   REST port (default 8280)
#   TTFV_SKIP_BUILD  "1" = reuse leankg on PATH / prebuilt binaries (local reruns)
#
# Exit codes: 0 = pass (< budget), 1 = over budget, 2 = setup/step error.
# Hermetic by construction: sqlite engine pinned, no embedding provider, no
# traffic beyond 127.0.0.1 loopback (and the Go toolchain proxy during build).
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MCP_PORT="${TTFV_MCP_PORT:-9711}"
REST_PORT="${TTFV_REST_PORT:-8280}"
BUDGET="${TTFV_BUDGET:-300}"
SKIP_BUILD="${TTFV_SKIP_BUILD:-0}"
# The query term is pinned to the fixture: models.UserRepository is a type in
# examples/go-api-service/internal/models/user.go, so both queries must land
# on rung L1 (exact identifier match) or the gate is measuring the wrong thing.
QUERY_TERM="UserRepository"
JSON_OUT=""
while [ $# -gt 0 ]; do
    case "$1" in
        --json) JSON_OUT="${2:?--json needs a path}"; shift 2 ;;
        *) printf 'unknown arg: %s\n' "$1" >&2; exit 2 ;;
    esac
done

WORK="$(mktemp -d)"
SCRATCH="$WORK/repo"
BIN="$WORK/bin"; mkdir -p "$BIN"
MCP_BASE="http://127.0.0.1:$MCP_PORT"
REST_BASE="http://127.0.0.1:$REST_PORT"
SERVER_PID=""
TIMES=()   # "name|seconds" rows

log()  { printf '%s\n' "$*"; }
err()  { printf '%s\n' "$*" >&2; }
now_s() { python3 -c 'import time; print(f"{time.time():.3f}")'; }
fsub()  { python3 -c "print(f'{$2-$1:.3f}')"; }

fail_setup() { err "TTFV SETUP ERROR: $*"; exit 2; }

cleanup() {
    if [ -n "$SERVER_PID" ]; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

record() { TIMES+=("$1|$2"); }

# timed_step NAME cmd... — run, abort on failure, record wall seconds.
timed_step() {
    local name="$1"; shift
    local t0 t1 secs
    t0=$(now_s)
    "$@" || fail_setup "step '$name' failed (exit $?)"
    t1=$(now_s)
    secs=$(fsub "$t0" "$t1")
    record "$name" "$secs"
    log "step $name: ${secs}s"
}
command -v python3 >/dev/null 2>&1 || fail_setup "python3 is required for timing/json"
command -v curl    >/dev/null 2>&1 || fail_setup "curl is required"
[ "$SKIP_BUILD" = "1" ] || command -v go >/dev/null 2>&1 || fail_setup "go toolchain is required (or TTFV_SKIP_BUILD=1)"

# sqlite is the contract; an inherited LEANKG_PG_URL must not flip the engine
# mid-run, and no embedding provider may be picked up from the ambient env.
export LEANKG_DB_ENGINE=sqlite
unset LEANKG_PG_URL LEANKG_EMBED_PROVIDER LEANKG_PROJECT_DIRS 2>/dev/null || true

T_START=$(now_s)
log "work dir: $WORK"

# ---------------------------------------------------------------- step 1: install
if [ "$SKIP_BUILD" = "1" ]; then
    LEANKG="$(command -v leankg || true)"
    [ -n "$LEANKG" ] || fail_setup "TTFV_SKIP_BUILD=1 but no leankg on PATH"
    record "install" "0.000"
    log "step install: 0.000s (TTFV_SKIP_BUILD=1, reusing $LEANKG)"
else
    build_all() {
        ( cd "$REPO_ROOT" \
            && CGO_ENABLED=0 go build -o "$BIN/leankg" ./cmd/leankg \
            && CGO_ENABLED=0 go build -o "$BIN/leankg-embed" ./cmd/leankg-embed )
    }
    timed_step "install" build_all
    LEANKG="$BIN/leankg"
fi

# ---------------------------------------------------------------- step 2: fixture
# pwd -P so macOS /var→/private/var canonicalization cannot split the project
# key between index and serve.
copy_fixture() {
    mkdir -p "$SCRATCH"
    ( cd "$REPO_ROOT" \
        && git ls-files examples/go-api-service \
           | grep -v '\.leankg/\|__pycache__\|\.DS_Store\|\.pyc' \
        && true ) | tar -cf - -T - | tar -xf - -C "$SCRATCH" --strip-components=2
}
timed_step "fixture" copy_fixture
SCRATCH="$(cd "$SCRATCH" && pwd -P)"
[ -n "$SCRATCH" ] && [ -d "$SCRATCH" ] || fail_setup "fixture dir missing"
FIXTURE_FILES=$(find "$SCRATCH" -type f | wc -l | tr -d ' ')
log "fixture: $SCRATCH ($FIXTURE_FILES files)"

# ---------------------------------------------------------------- step 3: index
# stdin is closed so a non-TTY-safe prompt path can never block the runner.
index_run() { cd "$SCRATCH" && "$LEANKG" index "$SCRATCH" < /dev/null > "$WORK/index.out" 2>&1; }
timed_step "index" index_run
ELEMENTS=$(grep -o 'elements=[0-9]*' "$WORK/index.out" | tail -1 | cut -d= -f2)
[ -n "$ELEMENTS" ] && [ "$ELEMENTS" -gt 0 ] || fail_setup "index produced no elements"

# ---------------------------------------------------------------- step 4: serve bind
# One process, two surfaces — the shipped `serve --http :9699 --rest :8080`
# shape, loopback-only.
t0=$(now_s)
"$LEANKG" serve --http "127.0.0.1:$MCP_PORT" --rest "127.0.0.1:$REST_PORT" --project "$SCRATCH" < /dev/null > "$WORK/serve.log" 2>&1 &
SERVER_PID=$!
BOUND=0
for _ in $(seq 1 240); do
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
        cat "$WORK/serve.log" >&2
        fail_setup "serve exited during bind"
    fi
    if curl -sf -m 1 "$REST_BASE/health" >/dev/null && curl -sf -m 1 "$MCP_BASE/health" >/dev/null; then
        BOUND=1; break
    fi
    sleep 0.25
done
t1=$(now_s)
[ "$BOUND" = "1" ] || { cat "$WORK/serve.log" >&2; fail_setup "serve /health never went green within 60s"; }
record "serve_bind" "$(fsub "$t0" "$t1")"
log "step serve_bind: $(fsub "$t0" "$t1")s"

# ---------------------------------------------------------------- step 5: first query (REST)
rest_query() {
    curl -sf -m 30 -X POST "$REST_BASE/api/v1/query" \
        -H 'Content-Type: application/json' \
        -d "{\"query\":\"$QUERY_TERM\",\"limit\":5}" \
    | python3 -c '
import json, sys
d = json.load(sys.stdin)
hits = d.get("hits", [])
rung = (d.get("retrieval") or {}).get("rung", "?")
if len(hits) < 1:
    sys.exit(f"REST query returned 0 hits: {d}")
print(f"REST first query: rung={rung} hits={len(hits)}")
'
}
timed_step "rest_query" rest_query

# ---------------------------------------------------------------- step 6: first query (MCP)
mcp_query() {
    python3 - "$MCP_BASE/mcp" "$QUERY_TERM" <<'PY'
import json, sys, urllib.request

url, term = sys.argv[1], sys.argv[2]
session = None

def rpc(method, params=None, mid=None):
    """One JSON-RPC round trip; handles both SSE and bare-JSON responses."""
    global session
    body = {"jsonrpc": "2.0", "method": method}
    if params is not None:
        body["params"] = params
    if mid is not None:
        body["id"] = mid
    req = urllib.request.Request(url, data=json.dumps(body).encode(), headers={
        "Content-Type": "application/json",
        "Accept": "application/json, text/event-stream",
    })
    if session:
        req.add_header("Mcp-Session-Id", session)
    with urllib.request.urlopen(req, timeout=30) as resp:
        sid = resp.headers.get("Mcp-Session-Id")
        if sid:
            session = sid
        payload = resp.read().decode()
        ctype = resp.headers.get("Content-Type", "")
    if mid is None:
        return None  # notification: 202, no body contract
    if "text/event-stream" in ctype:
        for line in payload.splitlines():
            if line.startswith("data: "):
                return json.loads(line[6:])
        raise SystemExit(f"no SSE data line in response: {payload[:200]}")
    return json.loads(payload)

init = rpc("initialize", {
    "protocolVersion": "2025-06-18",
    "capabilities": {},
    "clientInfo": {"name": "ttfv-smoke", "version": "1.0"},
}, mid=1)
if (init["result"].get("serverInfo") or {}).get("name") != "leankg":
    raise SystemExit(f"unexpected initialize result: {init}")
rpc("notifications/initialized", {})
tools = rpc("tools/list", {}, mid=2)["result"]["tools"]
names = sorted(t["name"] for t in tools)
if names != ["import", "query", "status"]:
    raise SystemExit(f"unexpected tool surface: {names}")
call = rpc("tools/call", {"name": "query", "arguments": {"query": term, "limit": 5}}, mid=3)["result"]
if call.get("isError"):
    raise SystemExit(f"MCP query tool errored: {call}")
out = json.loads(call["content"][0]["text"])
hits = out.get("hits", [])
if len(hits) < 1:
    raise SystemExit(f"MCP query returned 0 hits: {out}")
print(f"MCP first query: rung={(out.get('retrieval') or {}).get('rung', '?')} hits={len(hits)}")
PY
}
timed_step "mcp_query" mcp_query

# ---------------------------------------------------------------- verdict
T_END=$(now_s)
TOTAL="$(fsub "$T_START" "$T_END")"

log ""
log "step        seconds"
for row in "${TIMES[@]}"; do
    printf '%-12s %s\n' "${row%|*}" "${row#*|}"
done
log "total       $TOTAL"

VERDICT="pass"
OVER=$(python3 -c "print(1 if $TOTAL > $BUDGET else 0)")
[ "$OVER" = "1" ] && VERDICT="fail"

if [ -n "$JSON_OUT" ]; then
    python3 - "$JSON_OUT" "$TOTAL" "$BUDGET" "$VERDICT" "$FIXTURE_FILES" "$ELEMENTS" "${TIMES[@]}" <<'PY'
import json, os, sys, time
out, total, budget, verdict, files, elements, *steps = sys.argv[1:]
doc = {
    "issue": "#280",
    "measured_at_epoch": int(time.time()),
    "os": " ".join(os.uname()[:2]),
    "budget_s": float(budget),
    "total_s": float(total),
    "verdict": verdict,
    "fixture": {"source": "examples/go-api-service", "files": int(files), "elements": int(elements)},
    "steps": [{"step": s.split("|")[0], "seconds": float(s.split("|")[1])} for s in steps],
}
with open(out, "w") as fh:
    json.dump(doc, fh, indent=2)
    fh.write("\n")
print(f"json artifact: {out}")
PY
fi

# GITHUB_STEP_SUMMARY is set only on runners; publishing the number there is
# what makes it visible per-run next to the artifact.
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
    printf 'LeanKG Go cold TTFV: **%ss** (budget %ss, verdict: %s) — full breakdown in the `ttfv-go-cold` artifact.\n' "$TOTAL" "$BUDGET" "$VERDICT" >> "$GITHUB_STEP_SUMMARY"
fi

# The number itself, greppable by the workflow.
log "TTFV_TOTAL=$TOTAL TTFV_BUDGET=$BUDGET"

if [ "$VERDICT" = "fail" ]; then
    err "::error::Go cold happy path took ${TOTAL}s, over the ${BUDGET}s TTFV budget (#280)"
    exit 1
fi
log "PASS: first useful query (REST + MCP) in ${TOTAL}s (< ${BUDGET}s)"
exit 0
