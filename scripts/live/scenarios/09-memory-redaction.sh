# Scenario 09: memory redaction regression for #591. A document_id that carries a
# secret-looking value is stored redacted, so DELETE and retain-replacing must match
# it through the same redaction. A bare lkg_ token in retained content is stored
# redacted. Drives the Hindsight-wire REST surface of a --memory server.
# Uses its own project so its memory bank does not touch other scenarios.
scenario_main() {
  local dir="$WORK/s09/memory" rest_port rest base doc='sync-token=abc123'
  local hex token texts leak i
  write_fixture "$dir"
  run_cmd index "$LEANKG_BIN" index "$dir" --no-embed
  assert_eq "index for the memory store exits 0" 0 "$RC"

  rest_port=$(free_port)
  start_server memory "$dir" --memory --hindsight-compat --rest "127.0.0.1:$rest_port" || return 0
  rest="http://127.0.0.1:$rest_port"
  # start_server only waits on --http; poll the REST listener too.
  for i in $(seq 1 100); do
    http_request rest-health GET "$rest/health"
    [ "$HTTP_STATUS" = 200 ] && break
    sleep 0.2
  done
  assert_eq "REST listener answers /health" 200 "$HTTP_STATUS"
  base="$rest/v1/default/banks/probe"

  # 1. DELETE by the raw document id must find the row retained under that id.
  step "document delete with a redacted-form document id"
  http_request s1-retain POST "$base/memories" -H 'Content-Type: application/json' \
    --data-raw "$(jq -cn --arg d "$doc" '{items: [{content: "release gate note one", document_id: $d}]}')"
  assert_eq "retain with document_id sync-token=abc123 returns 200" 200 "$HTTP_STATUS"
  assert_json "retain reports one row written" '.written' 1 "$HTTP_BODY"

  http_request s1-list-before GET "$base/memories?limit=100"
  assert_json "one row is stored before the delete" '.total' 1 "$HTTP_BODY"

  http_request s1-delete DELETE "$rest/v1/default/banks/probe/documents/$doc"
  assert_eq "DELETE of the document returns 200" 200 "$HTTP_STATUS"
  assert_json "DELETE reports success true" '.success' true "$HTTP_BODY"
  assert_json "DELETE reports memory_units_deleted 1 for the raw document id" \
    '.memory_units_deleted' 1 "$HTTP_BODY"

  http_request s1-list-after GET "$base/memories?limit=100"
  assert_json "list no longer holds the deleted document's row" '.total' 0 "$HTTP_BODY"

  # 2. Retain-replacing with the same raw document id replaces the row. Own bank,
  # so step 1's outcome does not leak into this count.
  step "retain-replacing with one raw document id"
  local replace_base="$rest/v1/default/banks/replace"
  for i in 1 2; do
    http_request "s2-retain-$i" POST "$replace_base/memories" -H 'Content-Type: application/json' \
      --data-raw "$(jq -cn --arg d "$doc" --arg c "replace revision $i" '{items: [{content: $c, document_id: $d}]}')"
    assert_eq "retain revision $i returns 200" 200 "$HTTP_STATUS"
  done
  http_request s2-list GET "$replace_base/memories?limit=100"
  assert_json "two retains of one document id leave one row" '.total' 1 "$HTTP_BODY"
  assert_json "the surviving row is the second revision" \
    '.results[0].text' "replace revision 2" "$HTTP_BODY"

  # 3. A bare lkg_ token in retained content is stored redacted. The hex is random
  # per run; redact() masks lkg_ tokens in every capture.
  step "bare lkg_ token in retained content"
  hex=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
  token="lkg_$hex"
  local token_base="$rest/v1/default/banks/token"
  http_request s3-retain POST "$token_base/memories" -H 'Content-Type: application/json' \
    --data-raw "$(jq -cn --arg c "probe note keeps $token for later" '{items: [{content: $c}]}')"
  assert_eq "retain of content with a bare lkg_ token returns 200" 200 "$HTTP_STATUS"

  http_request s3-list GET "$token_base/memories?limit=100"
  texts=$(printf '%s' "$HTTP_BODY" | jq -r '.results[].text' 2>/dev/null)
  assert_contains "readback holds the probe note, so the absence check is not vacuous" \
    "probe note keeps" "$texts"
  assert_contains "stored token row carries the [REDACTED:key] marker" \
    "[REDACTED:key]" "$texts"
  if printf '%s' "$HTTP_BODY" | grep -qF "$hex"; then leak=present; else leak=absent; fi
  assert_eq "raw lkg_ token hex is absent from the readback" absent "$leak"
  stop_server
}
