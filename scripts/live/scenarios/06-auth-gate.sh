# Scenario 06: the HTTP auth gate after `leankg auth admin init`. Anonymous MCP
# calls are refused except the handshake, tools/list and authenticate; a bad bearer
# is 401; the bootstrap credentials mint a token that opens MCP and REST.
# Uses its own store so the gate does not leak into other scenarios.
scenario_main() {
  local dir="$WORK/s06/auth" envfile="$WORK/s06/auth.env" user pass token rest_port
  write_fixture "$dir"
  run_cmd index "$LEANKG_BIN" index "$dir" --no-embed
  assert_eq "index for the auth store exits 0" 0 "$RC"

  run_cmd admin-init "$LEANKG_BIN" auth admin init --project "$dir" --env-file "$envfile"
  assert_eq "auth admin init exits 0" 0 "$RC"
  assert_eq "env file holds both admin keys" 2 \
    "$(grep -c '^LEANKG_ADMIN_\(USERNAME\|PASSWORD\)=' "$envfile")"
  user=$(sed -n 's/^LEANKG_ADMIN_USERNAME=//p' "$envfile")
  pass=$(sed -n 's/^LEANKG_ADMIN_PASSWORD=//p' "$envfile")

  rest_port=$(free_port)
  start_server gate "$dir" --rest "127.0.0.1:$rest_port" || return 0

  mcp_tool anon-query "$SERVER_URL" query '{"query":"Alpha"}'
  assert_contains "anonymous query is refused with LEANKG_ERROR_UNAUTHORIZED" \
    "LEANKG_ERROR_UNAUTHORIZED" "$MCP_ERROR"

  mcp_request anon-tools-list "$SERVER_URL" tools/list '{}'
  assert_eq "anonymous tools/list is allowed (HTTP 200)" 200 "$HTTP_STATUS"

  mcp_tool bad-bearer "$SERVER_URL" status '{}' "lkg_not_a_real_token"
  assert_eq "invalid bearer token gets HTTP 401" 401 "$HTTP_STATUS"

  mcp_tool login-wrong "$SERVER_URL" authenticate "{\"username\":\"$user\",\"password\":\"not-the-password\"}"
  assert_contains "wrong password gets the generic invalid credentials error" \
    "invalid credentials" "$MCP_ERROR"

  mcp_tool login "$SERVER_URL" authenticate "{\"username\":\"$user\",\"password\":\"$pass\"}"
  token=$(printf '%s' "$MCP_RESULT" | jq -r '.token // empty' 2>/dev/null)
  assert_json "login with bootstrap credentials returns the admin role" '.role' admin "$MCP_RESULT"
  assert_eq "login token has the lkg_ prefix" true "$([ "${token#lkg_}" != "$token" ] && echo true || echo false)"

  mcp_tool authed-status "$SERVER_URL" status '{}' "$token"
  assert_json "authenticated status call is healthy" '.healthy' true "$MCP_RESULT"

  http_request rest-anon GET "http://127.0.0.1:$rest_port/api/v1/status"
  assert_eq "REST status without a token is 401" 401 "$HTTP_STATUS"
  http_request rest-authed GET "http://127.0.0.1:$rest_port/api/v1/status" \
    -H "Authorization: Bearer $token"
  assert_eq "REST status with the minted token is 200" 200 "$HTTP_STATUS"
  stop_server
}
