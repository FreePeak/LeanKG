# Scenario 12: FR-ZCP-01 fail-closed routing on a multi-project HTTP server. A
# projectless tools/call is refused with LEANKG_ERROR_MISSING_PARAM, a call that
# names a project succeeds, an unknown ?project= is HTTP 404, and a stdio
# single-project server answers without a project argument. Regression scenario:
# the behaviour already exists on the baseline binary.
scenario_main() {
  local dir_a="$WORK/s12/alpha-proj" dir_b="$WORK/s12/beta-proj"
  local init_params='{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"live-harness","version":"0"}}'
  local init_line='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"live-harness","version":"0"}}}'
  local list_line='{"jsonrpc":"2.0","method":"notifications/initialized"}'
  local call_line='{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"status","arguments":{}}}'

  write_fixture "$dir_a"
  write_fixture "$dir_b"
  run_cmd index-alpha "$LEANKG_BIN" index "$dir_a" --no-embed
  assert_eq "index alpha-proj exits 0" 0 "$RC"
  run_cmd index-beta "$LEANKG_BIN" index "$dir_b" --no-embed
  assert_eq "index beta-proj exits 0" 0 "$RC"

  export LEANKG_PROJECT_DIRS="$dir_a,$dir_b"
  start_server multi "$dir_a" || return 0
  unset LEANKG_PROJECT_DIRS

  # Initialize with the protocol header the client sends on every later request.
  s12_rpc initialize initialize "$init_params"
  assert_eq "HTTP initialize answers 200" 200 "$HTTP_STATUS"

  local tool
  for tool in status query import; do
    s12_call "noproj-$tool" "$(jq -cn --arg n "$tool" \
      '{name: $n, arguments: (if $n == "query" then {query: "Alpha"} elif $n == "import" then {action: "repo", path: "/nonexistent-s12"} else {} end)}')"
    assert_contains "projectless $tool is refused with LEANKG_ERROR_MISSING_PARAM" \
      "LEANKG_ERROR_MISSING_PARAM" "$S12_ERR"
    assert_contains "projectless $tool error names the project argument" \
      "project" "$S12_ERR"
  done

  s12_call proj-status "$(jq -cn --arg p "alpha-proj" '{name: "status", arguments: {project: $p}}')"
  assert_eq "status with project=alpha-proj answers 200" 200 "$HTTP_STATUS"
  assert_json "status with project=alpha-proj resolves project_dir to alpha-proj" \
    '.project_dir | endswith("/s12/alpha-proj")' true "$S12_TEXT"
  assert_eq "status with project=alpha-proj is not an error" "" "$S12_ERR"

  s12_post unknown-project "$SERVER_URL/mcp?project=nope" \
    "$(jq -cn '{jsonrpc: "2.0", id: 9, method: "tools/call", params: {name: "status", arguments: {}}}')"
  assert_eq "?project=nope answers HTTP 404" 404 "$HTTP_STATUS"
  stop_server

  # Clause 1: a single-project stdio server answers status with no project argument.
  run_cmd stdio-status-noproj bash -c \
    'cd "$1" && { printf "%s\n" "$2" "$3" "$4"; sleep 1; } | "$5" serve --stdio' _ \
    "$dir_a" "$init_line" "$list_line" "$call_line" "$LEANKG_BIN"
  assert_eq "stdio serve exits 0 at EOF" 0 "$RC"
  local status_reply
  status_reply=$(printf '%s\n' "$OUT" | jq -c 'select(.id == 2)' 2>/dev/null)
  assert_json "stdio status without project succeeds (not an error)" \
    '.error // empty | tojson' '' "$status_reply"
  assert_json "stdio status without project returns a result" \
    '.result != null' true "$status_reply"
}

# s12_call LABEL PARAMS_JSON: tools/call against $SERVER_URL/mcp.
# s12_rpc LABEL METHOD PARAMS_JSON: any JSON-RPC method (used for initialize).
s12_call() { s12_rpc "$1" tools/call "$2"; }
s12_rpc() {
  local body
  body=$(jq -cn --arg m "$2" --argjson p "$3" '{jsonrpc: "2.0", id: 2, method: $m, params: $p}')
  s12_post "$1" "$SERVER_URL/mcp" "$body"
}

# s12_post LABEL URL BODY_JSON: one JSON-RPC POST. Sets HTTP_STATUS, S12_TEXT
# (the tool's text payload) and S12_ERR (the JSON-RPC error message, or the
# tool's isError text, empty when the call succeeded).
s12_post() {
  local label=$1 url=$2 body=$3 json
  http_request "$label" POST "$url" \
    -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' \
    -H 'Mcp-Protocol-Version: 2025-06-18' \
    --data-raw "$body"
  json=$(sed -n 's/^data: //p' "$WORK/raw/$SCENARIO_ID-$label.body" | tail -1)
  [ -n "$json" ] || json=$HTTP_BODY
  S12_TEXT=$(printf '%s' "$json" | jq -r '.result.content[0].text // empty' 2>/dev/null)
  S12_ERR=$(printf '%s' "$json" | jq -r \
    'if .error then .error.message elif .result.isError then (.result.content[0].text // "") else empty end' \
    2>/dev/null)
}
