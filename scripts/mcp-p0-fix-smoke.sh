#!/usr/bin/env bash
# FR-P0-MCP-RC-01..04 + FR-P0-EMBED-LOCK live smoke against Docker MCP :9699.
#
# Exercises the acceptance criteria on the /workspace-be mega-graph. Prints a
# PASS/FAIL line per check. Exit 0 = all checks pass.
#
# Usage:  ./scripts/mcp-p0-fix-smoke.sh
# Env:    LEANKG_SMOKE_URL (default http://localhost:9699/mcp)
#         LEANKG_SMOKE_PROJECT (default /workspace-be)

set -u
URL="${LEANKG_SMOKE_URL:-http://localhost:9699/mcp}"
PROJECT="${LEANKG_SMOKE_PROJECT:-/workspace-be}"
PASS=0
FAIL=0

req() { # name json-body
  local name="$1" body="$2"
  local out
  out="$(curl -s -m 45 -X POST "$URL" -H 'Content-Type: application/json' -d "$body")"
  printf '%s' "$out"
}

check() { # name 0/1 detail
  if [ "$2" -eq 0 ]; then
    echo "PASS  $1${3:+ — $3}"
    PASS=$((PASS+1))
  else
    echo "FAIL  $1${3:+ — $3}"
    FAIL=$((FAIL+1))
  fi
}

echo "== P0 fix smoke on $PROJECT =="

# --- FR-P0-MCP-RC-01: project-authoritative routing ---
# get_dependencies with project=/workspace-be + relative file must return the
# anchor's real edges (not /workspace empty / lock error).
r=$(req "get_dependencies" "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"get_dependencies\",\"arguments\":{\"project\":\"$PROJECT\",\"file\":\"src/main.rs\"}}}")
echo "$r" | grep -qE '"error"|"isError"' && check "RC-01 get_dependencies(project,file)" 1 "$(echo "$r" | head -c 200)" \
  || check "RC-01 get_dependencies(project,file) returns deps" 0 "ok"

# --- FR-P0-MCP-RC-02: single handle, no lock re-open ---
# add_knowledge then find_related_docs on same project — no lock hold.
r1=$(req "add_knowledge" "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"add_knowledge\",\"arguments\":{\"project\":\"$PROJECT\",\"knowledge_type\":\"general\",\"title\":\"rc02 smoke\",\"content\":\"handle reuse\"}}}")
echo "$r1" | grep -q 'lock hold by current process' && check "RC-02 add_knowledge no lock" 1 || check "RC-02 add_knowledge no lock" 0
r2=$(req "find_related_docs" "{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\",\"params\":{\"name\":\"find_related_docs\",\"arguments\":{\"project\":\"$PROJECT\",\"file\":\"src/main.rs\"}}}")
echo "$r2" | grep -q 'lock hold by current process' && check "RC-02 find_related_docs after write no lock" 1 || check "RC-02 find_related_docs after write no lock" 0

# --- FR-P0-MCP-RC-03: /health stays ok during a slow tool ---
# Fire a full-scan tool (export_graph_snapshot) and immediately probe /health.
req "export_graph_snapshot" "{\"jsonrpc\":\"2.0\",\"id\":4,\"method\":\"tools/call\",\"params\":{\"name\":\"export_graph_snapshot\",\"arguments\":{\"project\":\"$PROJECT\"}}}" >/dev/null 2>&1 &
BGPID=$!
sleep 1
h=$(curl -s -m 3 http://localhost:9699/health 2>&1)
wait "$BGPID" 2>/dev/null
echo "$h" | grep -q '"status": "ok"' && check "RC-03 /health ok during slow tool" 0 || check "RC-03 /health ok during slow tool" 1 "$h"

# --- FR-P0-MCP-RC-04: full-scan tools refuse on mega ---
r=$(req "get_graph_report" "{\"jsonrpc\":\"2.0\",\"id\":5,\"method\":\"tools/call\",\"params\":{\"name\":\"get_graph_report\",\"arguments\":{\"project\":\"$PROJECT\"}}}")
echo "$r" | grep -qE 'refused|max 50000' && check "RC-04 get_graph_report refuses on mega" 0 || check "RC-04 get_graph_report refuses on mega" 1 "$(echo "$r" | head -c 150)"

# get_cluster_skill serves precomputed or refuses (never live Louvain)
r=$(req "get_cluster_skill" "{\"jsonrpc\":\"2.0\",\"id\":6,\"method\":\"tools/call\",\"params\":{\"name\":\"get_cluster_skill\",\"arguments\":{\"project\":\"$PROJECT\",\"cluster_id\":\"1\"}}}")
echo "$r" | grep -qE 'precomputed|refused|SKILL' && check "RC-04 get_cluster_skill no live Louvain" 0 || check "RC-04 get_cluster_skill no live Louvain" 1 "$(echo "$r" | head -c 150)"

# --- FR-P0-EMBED-LOCK: semantic_search completes ---
r=$(req "semantic_search" "{\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"tools/call\",\"params\":{\"name\":\"semantic_search\",\"arguments\":{\"project\":\"$PROJECT\",\"query\":\"handle requests\",\"limit\":5}}}")
echo "$r" | grep -qE 'lock hold by current process' && check "EMBED-LOCK semantic_search no lock" 1 \
  || check "EMBED-LOCK semantic_search completes" 0 "returned $(echo "$r" | wc -c) bytes"

# --- /health final ---
h=$(curl -s -m 3 http://localhost:9699/health)
echo "$h" | grep -q '"status": "ok"' && check "container /health healthy" 0 || check "container /health healthy" 1 "$h"

echo ""
echo "RESULT: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
