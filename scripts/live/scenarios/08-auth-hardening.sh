# Scenario 08: auth hardening (#586) through the real HTTP surface.
#  1. GET /api/v1/auth/token: only a site admin sees every token row; a viewer
#     bearer gets an empty list; no bearer is refused.
#  2. PUT /v1/default/banks/<bank> (hindsight-compat, needs --memory): viewer 403,
#     contributor 200, no bearer 401.
#  3. Login throttle: 20 concurrent wrong-password logins for one email give
#     exactly 5 generic "invalid credentials" and 15 rate-limited responses.
# Env tokens use the lkg_ prefix so the harness redaction covers them. Each
# server runs one at a time, with its own store.
scenario_main() {
  local dir="$WORK/s08/tokens" envfile="$WORK/s08/tokens.env" memdir="$WORK/s08/memory"
  local tok_admin tok_contrib tok_viewer user pass db_token rest_port rest
  local i body code burst="$WORK/raw/$SCENARIO_ID-login-burst" generic=0 limited=0 other=0
  local pids=()

  tok_admin="lkg_admin_$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  tok_contrib="lkg_contrib_$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  tok_viewer="lkg_viewer_$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  export LEANKG_TOKEN_ADMIN="$tok_admin" LEANKG_TOKEN_CONTRIBUTOR="$tok_contrib" \
    LEANKG_TOKEN_VIEWER="$tok_viewer"

  # --- Part 1 and 3: token list and login burst on a store with an admin account.
  write_fixture "$dir"
  run_cmd index "$LEANKG_BIN" index "$dir" --no-embed
  assert_eq "index for the token store exits 0" 0 "$RC"
  run_cmd admin-init "$LEANKG_BIN" auth admin init --project "$dir" --env-file "$envfile"
  assert_eq "auth admin init exits 0" 0 "$RC"
  user=$(sed -n 's/^LEANKG_ADMIN_USERNAME=//p' "$envfile")
  pass=$(sed -n 's/^LEANKG_ADMIN_PASSWORD=//p' "$envfile")

  rest_port=$(free_port)
  rest="http://127.0.0.1:$rest_port"
  start_server tokens "$dir" --rest "127.0.0.1:$rest_port" || return 0

  mcp_tool mint-db-token "$SERVER_URL" authenticate "{\"username\":\"$user\",\"password\":\"$pass\"}"
  db_token=$(printf '%s' "$MCP_RESULT" | jq -r '.token // empty' 2>/dev/null)
  assert_eq "login through authenticate mints a DB token" true \
    "$([ -n "$db_token" ] && echo true || echo false)"

  http_request token-list-anon GET "$rest/api/v1/auth/token"
  assert_eq "token list with no bearer is refused (not 200)" true \
    "$([ "$HTTP_STATUS" != 200 ] && echo true || echo false)"

  http_request token-list-viewer GET "$rest/api/v1/auth/token" \
    -H "Authorization: Bearer $tok_viewer"
  assert_eq "viewer bearer gets HTTP 200 on the token list" 200 "$HTTP_STATUS"
  assert_json "viewer bearer sees an empty token list" '.data | length' 0 "$HTTP_BODY"

  http_request token-list-admin GET "$rest/api/v1/auth/token" \
    -H "Authorization: Bearer $tok_admin"
  assert_eq "admin bearer gets HTTP 200 on the token list" 200 "$HTTP_STATUS"
  assert_json "admin bearer sees the minted DB token row" '.data | length >= 1' true "$HTTP_BODY"

  # Login burst: 20 concurrent wrong-password attempts for one email. The
  # throttle counts each attempt before the password check, so the first five
  # return the generic error and the rest are rate-limited.
  for i in $(seq 1 20); do
    curl -sS -o "$burst.$i.body" -w '%{http_code}' -X POST \
      -H 'Content-Type: application/json' \
      --data-raw "{\"email\":\"$user\",\"password\":\"wrong-$i\"}" \
      "$rest/api/v1/auth/login" >"$burst.$i.code" 2>"$burst.$i.err" &
    pids+=("$!")
  done
  wait "${pids[@]}"
  : >"$CAP_DIR/login-burst.txt"
  for i in $(seq 1 20); do
    code=$(cat "$burst.$i.code")
    body=$(cat "$burst.$i.body" 2>/dev/null)
    printf 'attempt %02d: HTTP %s %s\n' "$i" "$code" "$body" | redact >>"$CAP_DIR/login-burst.txt"
    case "$body" in
      *"too many failed attempts"*) limited=$((limited + 1)) ;;
      *"invalid credentials"*) generic=$((generic + 1)) ;;
      *) other=$((other + 1)) ;;
    esac
  done
  LAST_CAPTURE="scenarios/$SCENARIO_ID/captures/login-burst.txt"
  log "login burst (20 concurrent wrong-password logins): generic=$generic rate-limited=$limited other=$other"
  assert_eq "login burst: exactly 5 generic invalid-credentials responses" 5 "$generic"
  assert_eq "login burst: exactly 15 rate-limited responses" 15 "$limited"
  assert_eq "login burst: no other response shape" 0 "$other"
  stop_server

  # --- Part 2: hindsight-compat bank PUT on a memory-enabled server.
  write_fixture "$memdir"
  run_cmd memory-index "$LEANKG_BIN" index "$memdir" --no-embed
  assert_eq "index for the memory store exits 0" 0 "$RC"
  rest_port=$(free_port)
  rest="http://127.0.0.1:$rest_port"
  start_server memory "$memdir" --memory --hindsight-compat --rest "127.0.0.1:$rest_port" || return 0

  http_request hindsight-put-viewer PUT "$rest/v1/default/banks/s08-bank" \
    -H "Authorization: Bearer $tok_viewer" -H 'Content-Type: application/json' --data-raw '{}'
  assert_eq "viewer bearer gets 403 on PUT bank" 403 "$HTTP_STATUS"

  http_request hindsight-put-contrib PUT "$rest/v1/default/banks/s08-bank" \
    -H "Authorization: Bearer $tok_contrib" -H 'Content-Type: application/json' --data-raw '{}'
  assert_eq "contributor bearer gets 200 on PUT bank" 200 "$HTTP_STATUS"

  http_request hindsight-put-anon PUT "$rest/v1/default/banks/s08-bank" \
    -H 'Content-Type: application/json' --data-raw '{}'
  assert_eq "PUT bank with no bearer gets 401" 401 "$HTTP_STATUS"
  stop_server
}
