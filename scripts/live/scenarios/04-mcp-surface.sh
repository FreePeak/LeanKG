# Scenario 04: the MCP surface is exactly the four tools, over both HTTP and stdio.
scenario_main() {
  local init_line list_line tools_line
  init_line='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"live-harness","version":"0"}}}'
  list_line='{"jsonrpc":"2.0","method":"notifications/initialized"}'
  local tools_req='{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  ensure_fixture

  start_server mcp "$FIXTURE" || return 0
  mcp_request http-initialize "$SERVER_URL" initialize \
    '{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"live-harness","version":"0"}}'
  assert_eq "HTTP initialize answers 200" 200 "$HTTP_STATUS"
  mcp_request http-tools-list "$SERVER_URL" tools/list '{}'
  assert_eq "HTTP tools/list answers 200" 200 "$HTTP_STATUS"
  assert_json "HTTP tools/list exposes exactly import,query,status,authenticate" \
    '[.result.tools[].name] | sort | join(",")' 'authenticate,import,query,status' "$MCP_JSON"
  stop_server

  # Stdio: stdin is held open for a second so the server does not see EOF before
  # it answers (an EOF with requests still in flight drops their replies).
  run_cmd stdio-tools-list bash -c \
    '{ printf "%s\n" "$1" "$2" "$3"; sleep 1; } | "$4" serve --stdio --project "$5"' _ \
    "$init_line" "$list_line" "$tools_req" "$LEANKG_BIN" "$FIXTURE"
  assert_eq "stdio serve exits 0 at EOF" 0 "$RC"
  tools_line=$(printf '%s\n' "$OUT" | jq -c 'select(.id == 2)' 2>/dev/null)
  assert_json "stdio tools/list exposes exactly import,query,status,authenticate" \
    '.result.tools | map(.name) | sort | join(",")' 'authenticate,import,query,status' "$tools_line"
}
