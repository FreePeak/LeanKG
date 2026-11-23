# Scenario 05: the query ladder. L1 exact identifier, then the L2 keyword
# fallback, then a clean miss, over MCP and over the CLI.
scenario_main() {
  ensure_fixture
  start_server query "$FIXTURE" || return 0

  mcp_tool mcp-exact "$SERVER_URL" query '{"query":"Alpha"}'
  assert_json "MCP L1 exact: first hit is a.go::Alpha" '.hits[0].qualified_name' 'a.go::Alpha' "$MCP_RESULT"
  assert_json "MCP L1 exact: rung is L1" '.retrieval.rung' L1 "$MCP_RESULT"

  mcp_tool mcp-fuzzy "$SERVER_URL" query '{"query":"gamma helper"}'
  assert_json "MCP L2 keyword fallback finds c.go::gammaHelper" \
    '.hits | any(.qualified_name == "c.go::gammaHelper")' true "$MCP_RESULT"
  assert_json "MCP L2 keyword fallback: rung is L2" '.retrieval.rung' L2 "$MCP_RESULT"

  mcp_tool mcp-miss "$SERVER_URL" query '{"query":"zzzNoSuchSymbol"}'
  assert_json "MCP miss returns zero hits" '.hits | length' 0 "$MCP_RESULT"
  stop_server

  run_cmd cli-exact "$LEANKG_BIN" query Alpha --project "$FIXTURE"
  assert_json "CLI exact: first hit is a.go::Alpha" '.[0].qualified_name' 'a.go::Alpha' "$OUT"

  run_cmd cli-fuzzy "$LEANKG_BIN" query "gamma helper" --project "$FIXTURE"
  assert_json "CLI fuzzy fallback finds c.go::gammaHelper" \
    '[.[].qualified_name] | any(. == "c.go::gammaHelper")' true "$OUT"
}
