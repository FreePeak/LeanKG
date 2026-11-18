# Scenario 13: HTTP contract fixes (#590) on the REST and UI listeners.
#  1. [fixed] A malformed JSON body on POST /api/v1/query answers 400 with code
#     LEANKG_ERROR_INVALID_PARAM (baseline: no code). [guard] the 400 itself.
#  2. [fixed] The same on POST /api/query-graph (UI listener) and on
#     POST /api/v1/mcp/auto-config (REST listener, body {"cwd":).
#  3. [fixed] query-graph token_budget 0 is 400 INVALID_PARAM (baseline: 200).
#     [guard] token_budget 60 is 200 and data.token_budget is the 200 floor.
#  4. Per-action _token_budget.max (history 2000, prd 4000) on REST /api/v1/query.
#     The marker is attached only when a response exceeds its cap, so each action
#     is asserted only when its probe answer carries the marker; otherwise SKIP.
#  5. [fixed] import repo of a subdirectory of the indexed project answers 409
#     (baseline: 400). [guard] the body carries LEANKG_ERROR_INDEX_ROOT_MISMATCH.
# No tokens are minted, so nothing here needs redaction beyond the harness default.
scenario_main() {
  local dir="$WORK/s13/proj" rest_port ui_port rest ui ctype='Content-Type: application/json'
  local pair name want has
  write_fixture "$dir"
  mkdir -p "$dir/sub"
  printf 'package sub\n\nfunc Inner() {}\n' >"$dir/sub/s.go"
  run_cmd index "$LEANKG_BIN" index "$dir" --no-embed
  assert_eq "index for the HTTP contract store exits 0" 0 "$RC"

  rest_port=$(free_port)
  ui_port=$(free_port)
  rest="http://127.0.0.1:$rest_port"
  ui="http://127.0.0.1:$ui_port"
  start_server http "$dir" --rest "127.0.0.1:$rest_port" --ui "127.0.0.1:$ui_port" || return 0
  wait_listener_health rest-health "$rest" || { record "REST listener answers /health" 200 "$HTTP_STATUS" FAIL ""; return 0; }
  wait_listener_health ui-health "$ui" || { record "UI listener answers /health" 200 "$HTTP_STATUS" FAIL ""; return 0; }

  step "malformed JSON bodies answer 400 with a stable code"
  http_request rest-query-badjson POST "$rest/api/v1/query" -H "$ctype" --data-raw '{"query":'
  assert_eq "[guard] REST malformed /api/v1/query body answers 400" 400 "$HTTP_STATUS"
  assert_json "[fixed] REST malformed /api/v1/query body carries code LEANKG_ERROR_INVALID_PARAM" \
    '.code' LEANKG_ERROR_INVALID_PARAM "$HTTP_BODY"

  http_request ui-qg-badjson POST "$ui/api/query-graph" -H "$ctype" --data-raw '{"question":'
  assert_eq "[guard] UI malformed /api/query-graph body answers 400" 400 "$HTTP_STATUS"
  assert_json "[fixed] UI malformed /api/query-graph body carries code LEANKG_ERROR_INVALID_PARAM" \
    '.code' LEANKG_ERROR_INVALID_PARAM "$HTTP_BODY"

  http_request rest-autoconfig-badjson POST "$rest/api/v1/mcp/auto-config" -H "$ctype" --data-raw '{"cwd":'
  assert_eq "[guard] REST malformed auto-config body answers 400" 400 "$HTTP_STATUS"
  assert_json "[fixed] REST malformed auto-config body carries code LEANKG_ERROR_INVALID_PARAM" \
    '.code' LEANKG_ERROR_INVALID_PARAM "$HTTP_BODY"

  step "query-graph token_budget 0 and 60"
  http_request ui-qg-budget0 POST "$ui/api/query-graph" -H "$ctype" \
    --data-raw '{"question":"Alpha","token_budget":0}'
  assert_eq "[fixed] query-graph token_budget 0 answers 400" 400 "$HTTP_STATUS"
  assert_json "[fixed] query-graph token_budget 0 carries code LEANKG_ERROR_INVALID_PARAM" \
    '.code' LEANKG_ERROR_INVALID_PARAM "$HTTP_BODY"

  http_request ui-qg-budget60 POST "$ui/api/query-graph" -H "$ctype" \
    --data-raw '{"question":"Alpha","token_budget":60}'
  assert_eq "[guard] query-graph token_budget 60 answers 200" 200 "$HTTP_STATUS"
  assert_json "[guard] query-graph token_budget 60 reports data.token_budget 200 (floor)" \
    '.data.token_budget' 200 "$HTTP_BODY"

  step "per-action _token_budget.max on REST /api/v1/query"
  for pair in history:2000 prd:4000; do
    name=${pair%%:*}
    want=${pair##*:}
    http_request "budget-$name" POST "$rest/api/v1/query" -H "$ctype" \
      --data-raw "{\"action\":\"$name\",\"query\":\"a.go\"}"
    has=$(printf '%s' "$HTTP_BODY" | jq -r 'has("_token_budget")' 2>/dev/null)
    if [ "$has" = true ]; then
      assert_json "[fixed] $name answer reports _token_budget.max $want" \
        '._token_budget.max' "$want" "$HTTP_BODY"
    else
      skip "$name: probe answer (HTTP $HTTP_STATUS) has no _token_budget marker; the marker is attached only past the cap, so max $want is not observable on this fixture (see captures/budget-$name.response)"
    fi
  done

  step "import of a subdirectory of the indexed project"
  http_request import-subdir POST "$rest/api/v1/import" -H "$ctype" \
    --data-raw '{"action":"repo","path":"sub"}'
  assert_eq "[fixed] import repo of a subdirectory answers 409" 409 "$HTTP_STATUS"
  assert_json "[guard] import repo of a subdirectory carries code LEANKG_ERROR_INDEX_ROOT_MISMATCH" \
    '.code' LEANKG_ERROR_INDEX_ROOT_MISMATCH "$HTTP_BODY"
  stop_server
}

# wait_listener_health LABEL URL: polls URL/health for up to 20 seconds. Sets HTTP_STATUS.
wait_listener_health() {
  local i
  for i in $(seq 1 100); do
    http_request "$1" GET "$2/health"
    [ "$HTTP_STATUS" = 200 ] && return 0
    sleep 0.2
  done
  return 1
}
