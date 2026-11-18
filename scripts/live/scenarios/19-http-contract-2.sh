# Scenario 19: HTTP contract, second part (#590).
#  1. [fixed] A REST or UI body over 1 MiB answers 413 with LEANKG_ERROR_REQUEST_TOO_LARGE
#     (baseline: accepted). [guard] a 100 KiB body is still answered, not refused.
#  2. [fixed] Store failure: on a copy whose code_elements table is dropped, REST status
#     answers 500 (baseline 400); the UI index status answers 503 with
#     LEANKG_ERROR_INDEX_UNAVAILABLE (baseline 400, no code). [guard] the driver error
#     names the missing table.
#  3. [unit-gate] A Postgres connect failure maps to LEANKG_ERROR_PG_UNREACHABLE. No HTTP
#     path reaches it: serve opens its store at startup and refuses an unreachable
#     Postgres, so the mapping is asserted by the unit test. [guard] that startup refusal
#     names the code on both binaries.
#  4. [fixed] import{action:"read"} over its 8000-token cap is cut and carries the budget
#     marker on REST (baseline: uncapped). The same cut applies on ConnectRPC (unit test).
#  5. [fixed] Tool budgets beyond the 1000-token default: history, cochange and churn 2000,
#     prd 4000, incidents 2000, service_context 800, env_conflicts 2000 (baseline: the
#     1000 default). The fixture is a generated git repo (300 commits co-changing one hub
#     file), a generated PRD of 240 requirements and 40 billing incidents. env_conflicts
#     has no writer for env snapshots (orgknowledge.SnapshotElements has no caller), so its
#     answer is made over the cap by a 4800-character service argument.
#  6. [fixed] Dashboard consent 400s carry LEANKG_ERROR_INVALID_PARAM (baseline: no code).
#     [guard] they stay 400. Consent is only ever sent invalid bodies here, so no consent
#     is written; LEANKG_HOME is the run's own home.

# repeat_text TEXT N: TEXT repeated N times, separated by spaces.
repeat_text() {
  local text=$1 n=$2 i out=""
  for ((i = 0; i < n; i++)); do out+="$text "; done
  printf '%s' "$out"
}

# build_project DIR: a Go module that is a git repo with 300 commits. Each commit
# rewrites hub/hub.go and adds hub/p<N>.go, so the hub co-changes with 300 partners.
# docs/prd.md holds 240 requirements; bulk/big.go is about 130 KB.
build_project() {
  local dir=$1 i
  write_fixture "$dir"
  mkdir -p "$dir/hub" "$dir/bulk" "$dir/docs"
  {
    printf 'package bulk\n\n'
    for i in $(seq 1 2000); do
      printf 'func Filler%d() { /* filler line for the read budget */ }\n' "$i"
    done
  } >"$dir/bulk/big.go"
  git -C "$dir" init -q -b main || return 1
  for i in $(seq 1 300); do
    printf 'package hub\n\n// revision %d\nfunc Hub%d() int { return %d }\n' "$i" "$i" "$i" >"$dir/hub/hub.go"
    printf 'package hub\n\nfunc Partner%d() {}\n' "$i" >"$dir/hub/p$i.go"
    git -C "$dir" add -A || return 1
    git -C "$dir" -c user.name=harness -c user.email=harness@example.invalid -c commit.gpgsign=false \
      commit -q -m "couple hub with p$i" || return 1
  done
  for i in $(seq 1 240); do
    printf '**FR-GEN-%03d — Generated requirement %d for the budget fixture (Must Have, P1)**\n\n' "$i" "$i"
    printf -- '- The system answers generated requirement %d with a deterministic response.\n' "$i"
    printf -- '- AC: requirement %d returns a non-error answer within the agreed window.\n\n' "$i"
  done >"$dir/docs/prd.md"
  printf '# Product requirements\n\n## Generated milestone\n\n' | cat - "$dir/docs/prd.md" >"$dir/docs/prd.tmp" && mv "$dir/docs/prd.tmp" "$dir/docs/prd.md"
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

# wait_http LABEL URL: polls URL until it answers 200 (for listeners with no /health).
wait_http() {
  local i
  for i in $(seq 1 100); do
    http_request "$1" GET "$2"
    [ "$HTTP_STATUS" = 200 ] && return 0
    sleep 0.2
  done
  return 1
}

# budget_probe LABEL BODY WANT: one REST query; asserts _token_budget.max.
budget_probe() {
  local label=$1 body=$2 want=$3 max
  http_request "budget-$label" POST "$REST_URL/api/v1/query" -H "$CTYPE" --data-raw "$body"
  max=$(printf '%s' "$HTTP_BODY" | jq -r '._token_budget.max // "none"' 2>/dev/null)
  assert_eq "[guard] $label query answers 200" 200 "$HTTP_STATUS"
  assert_eq "[fixed] $label answer reports _token_budget.max $want" "$want" "$max"
}

CTYPE='Content-Type: application/json'

scenario_main() {
  local base="$WORK/s19" dir rest_port ui_port i added=0 long body big small max
  dir="$base/proj"
  mkdir -p "$base"
  build_project "$dir" || { record "fixture: git repo and PRD are generated" 0 1 FAIL ""; return 0; }
  run_cmd index "$LEANKG_BIN" index "$dir" --no-embed
  assert_eq "fixture: the budget project indexes with exit 0" 0 "$RC"

  for i in $(seq 1 40); do
    if "$LEANKG_BIN" incident add --project "$dir" --title "Billing incident $i" --severity P2 \
      --affected svc-billing --root-cause "$(repeat_text 'Root cause detail for the billing incident.' 14) $i" \
      --resolution "rolled back $i" --prevention "$(repeat_text 'Prevention advice: keep the retry budget bounded.' 6) $i" \
      >/dev/null 2>&1; then
      added=$((added + 1))
    fi
  done
  assert_eq "fixture: 40 billing incidents are recorded" 40 "$added"

  rest_port=$(free_port)
  ui_port=$(free_port)
  REST_URL="http://127.0.0.1:$rest_port"
  UI_URL="http://127.0.0.1:$ui_port"
  start_server http "$dir" --rest "127.0.0.1:$rest_port" --ui "127.0.0.1:$ui_port" || return 0
  wait_listener_health rest-health "$REST_URL" || { record "REST listener answers /health" 200 "$HTTP_STATUS" FAIL ""; return 0; }
  wait_listener_health ui-health "$UI_URL" || { record "UI listener answers /health" 200 "$HTTP_STATUS" FAIL ""; return 0; }

  step "request body cap (1 MiB)"
  big=$base/over-1mib.json
  { printf '{"query":"'; head -c 1258291 /dev/zero | tr '\0' a; printf '"}'; } >"$big"
  http_request body-cap-rest POST "$REST_URL/api/v1/query" -H "$CTYPE" --data-binary "@$big"
  assert_eq "[fixed] REST query body over 1 MiB answers 413" 413 "$HTTP_STATUS"
  assert_json "[fixed] REST query body over 1 MiB carries code LEANKG_ERROR_REQUEST_TOO_LARGE" \
    '.code' LEANKG_ERROR_REQUEST_TOO_LARGE "$HTTP_BODY"
  http_request body-cap-ui POST "$UI_URL/api/query" -H "$CTYPE" --data-binary "@$big"
  assert_eq "[fixed] UI query body over 1 MiB answers 413" 413 "$HTTP_STATUS"
  assert_json "[fixed] UI query body over 1 MiB carries code LEANKG_ERROR_REQUEST_TOO_LARGE" \
    '.code' LEANKG_ERROR_REQUEST_TOO_LARGE "$HTTP_BODY"
  small=$base/under-100kib.json
  { printf '{"query":"'; head -c 102400 /dev/zero | tr '\0' a; printf '"}'; } >"$small"
  http_request body-under-cap POST "$REST_URL/api/v1/query" -H "$CTYPE" --data-binary "@$small"
  assert_eq "[guard] REST query body of 100 KiB is answered, not refused" 200 "$HTTP_STATUS"

  step "git history, import budget and tool budgets"
  http_request import-git POST "$REST_URL/api/v1/import" -H "$CTYPE" --data-raw '{"action":"git"}'
  assert_eq "[guard] import git answers 200" 200 "$HTTP_STATUS"
  http_request import-prd POST "$REST_URL/api/v1/import" -H "$CTYPE" --data-raw '{"action":"prd","path":"docs/prd.md"}'
  assert_eq "[guard] import prd of the generated PRD answers 200" 200 "$HTTP_STATUS"
  http_request import-read POST "$REST_URL/api/v1/import" -H "$CTYPE" --data-raw '{"action":"read","path":"bulk/big.go"}'
  assert_eq "[guard] import read of the large file answers 200" 200 "$HTTP_STATUS"
  max=$(printf '%s' "$HTTP_BODY" | jq -r '._token_budget.max // "none"' 2>/dev/null)
  assert_eq "[fixed] import read answer reports _token_budget.max 8000" 8000 "$max"

  budget_probe history "$(jq -cn '{action:"history",query:"hub/hub.go",limit:300}')" 2000
  budget_probe cochange "$(jq -cn '{action:"cochange",query:"hub/hub.go",limit:300}')" 2000
  budget_probe churn "$(jq -cn '{action:"churn",limit:300}')" 2000
  budget_probe prd '{"action":"prd","query":""}' 4000
  budget_probe incidents "$(jq -cn '{action:"incidents",args:{service:"svc-billing"},limit:50}')" 2000
  budget_probe service_context "$(jq -cn '{action:"service_context",args:{service:"svc-billing",env:"production"}}')" 800
  long=$(repeat_text 'svc-long-service-name' 240)
  budget_probe env_conflicts "$(jq -cn --arg s "$long" '{action:"env_conflicts",args:{service:$s}}')" 2000
  stop_server

  step "store failure: a copy whose code_elements table is gone"
  if ! command -v sqlite3 >/dev/null 2>&1; then
    skip "sqlite3 is needed to drop a table from a copied store (the store-failure checks are not run)"
  else
    cp -R "$dir" "$base/broken"
    run_cmd broken-drop sqlite3 "$base/broken/.leankg/leankg.db" 'DROP TABLE code_elements;'
    assert_eq "store copy: code_elements is dropped" 0 "$RC"
    rest_port=$(free_port)
    ui_port=$(free_port)
    REST_URL="http://127.0.0.1:$rest_port"
    UI_URL="http://127.0.0.1:$ui_port"
    start_server broken "$base/broken" --rest "127.0.0.1:$rest_port" --ui "127.0.0.1:$ui_port" || return 0
    wait_listener_health broken-rest-health "$REST_URL" || { record "broken REST listener answers /health" 200 "$HTTP_STATUS" FAIL ""; return 0; }
    http_request broken-status GET "$REST_URL/api/v1/status"
    assert_eq "[fixed] REST status on the store with no code_elements answers 500" 500 "$HTTP_STATUS"
    assert_contains "[guard] the status error names the missing table" "no such table: code_elements" "$HTTP_BODY"
    http_request broken-index-status GET "$UI_URL/api/index/status"
    assert_eq "[fixed] UI index status on the same store answers 503" 503 "$HTTP_STATUS"
    assert_json "[fixed] UI index status carries code LEANKG_ERROR_INDEX_UNAVAILABLE" \
      '.code' LEANKG_ERROR_INDEX_UNAVAILABLE "$HTTP_BODY"
    assert_contains "[guard] the UI error names the missing table" "no such table: code_elements" "$HTTP_BODY"
    stop_server
  fi

  step "Postgres connect failure"
  run_cmd unit-gate-pg-unreachable bash -c 'cd "$1" && go test -run "^TestFailMapsAnUnreachablePostgresToPGUnreachable$" -count=1 -v ./internal/store' _ "$REPO_ROOT"
  assert_eq "[unit-gate] Postgres connect failure maps to PG_UNREACHABLE: the unit test passes (no HTTP path, serve refuses to start)" 0 "$RC"
  assert_contains "[unit-gate] the unit test prints its PASS line" "--- PASS: TestFailMapsAnUnreachablePostgresToPGUnreachable" "$OUT"
  run_cmd pg-unreachable-startup perl -e 'alarm 30; exec @ARGV' env LEANKG_DB_ENGINE=postgres \
    "LEANKG_PG_URL=postgres://leankg@127.0.0.1:$(free_port)/leankg" \
    "$LEANKG_BIN" serve --http "127.0.0.1:$(free_port)" --project "$dir"
  assert_eq "[guard] serve on an unreachable Postgres exits 1" 1 "$RC"
  assert_contains "[guard] the startup refusal names LEANKG_ERROR_PG_UNREACHABLE" "LEANKG_ERROR_PG_UNREACHABLE" "$ERR"

  step "dashboard consent: invalid bodies"
  local dash_port dash_url dash_pid tok
  dash_port=$(free_port)
  dash_url="http://127.0.0.1:$dash_port"
  "$LEANKG_BIN" dashboard --addr "127.0.0.1:$dash_port" --no-open >"$WORK/raw/dashboard-19.log" 2>&1 &
  dash_pid=$!
  echo "$dash_pid" >>"$WORK/servers.pids"
  if wait_http dashboard-consent-get "$dash_url/api/dashboard/v1/consent"; then
    tok=$(printf '%s' "$HTTP_BODY" | jq -r '.csrf_token // empty')
    perl -pi -e 's/"csrf_token": *"[^"]*"/"csrf_token":"REDACTED"/g' "$CAP_DIR/dashboard-consent-get.response"
    consent_probe() {
      local label=$1 body=$2 want=$3
      http_request "consent-$label" POST "$dash_url/api/dashboard/v1/consent" -H "$CTYPE" \
        -H "Origin: $dash_url" -H "X-CSRF-Token: $tok" --data-raw "$body"
      assert_eq "[guard] consent $label answers 400" 400 "$HTTP_STATUS"
      assert_json "[fixed] consent $label carries code LEANKG_ERROR_INVALID_PARAM" '.code' LEANKG_ERROR_INVALID_PARAM "$HTTP_BODY"
    }
    consent_probe bad-json 'not json'
    consent_probe bad-capture '{"capture":"everything"}'
    consent_probe unknown-client '{"capture":"metadata","sessions":["not-a-client"]}'
  else
    record "dashboard listener answers consent GET" 200 "$HTTP_STATUS" FAIL "$LAST_CAPTURE"
  fi
  kill "$dash_pid" 2>/dev/null
  wait "$dash_pid" 2>/dev/null
}
