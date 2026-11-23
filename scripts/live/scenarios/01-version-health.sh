# Scenario 01: the binary reports the VERSION file, status is healthy, and the
# HTTP listener answers /health and the MCP initialize handshake.
scenario_main() {
  local version
  version=$(tr -d '[:space:]' <"$REPO_ROOT/cmd/leankg/VERSION")

  run_cmd version "$LEANKG_BIN" version
  assert_eq "leankg version prints the VERSION file" "leankg $version" "$OUT"
  assert_eq "leankg version exits 0" 0 "$RC"

  ensure_fixture
  run_cmd status "$LEANKG_BIN" status --project "$FIXTURE"
  assert_eq "status exits 0" 0 "$RC"
  assert_json "status reports healthy" '.healthy' true "$OUT"
  assert_json "status freshness is fresh" '.freshness' fresh "$OUT"
  assert_json "status backend is sqlite" '.backend' sqlite "$OUT"

  start_server health "$FIXTURE" || return 0
  http_request health GET "$SERVER_URL/health"
  assert_eq "GET /health returns 200" 200 "$HTTP_STATUS"
  assert_json "GET /health body is ok" '.ok' true "$HTTP_BODY"

  retry_until mcp_initialize initialize "$SERVER_URL" \
    '{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"live-harness","version":"0"}}'
  assert_eq "MCP initialize answers HTTP 200" 200 "$HTTP_STATUS"
  assert_json "MCP serverInfo.name is leankg" '.result.serverInfo.name' leankg "$MCP_JSON"
  assert_json "MCP serverInfo.version matches VERSION" '.result.serverInfo.version' "$version" "$MCP_JSON"
  stop_server
}
