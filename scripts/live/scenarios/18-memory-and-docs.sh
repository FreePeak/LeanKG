# Scenario 18 (wave 2, issue #591 remainder and #592 coverage):
#  A. Pre-redaction memory rows. A bank file written before write-side redaction
#     keeps its raw secret on disk. Compaction (the serve maintenance pass, first
#     run ~30 s after start) and a REST retain of the redacted form must leave one
#     redacted row per memory, and no raw secret.
#  B. Markdown ownership is independent of run order. `import docs` on a file the
#     index already walked must not leave docindex rows once `index` runs, and the
#     result must match the order where the index ran first.
#  C. The tool contract names the docindex split for action=docs (tools/list).
#  D. Core-file bound measures the redacted text (MEMORY.md, import memory add).
#  E. Unit gates for fixes with no deterministic CLI/HTTP/MCP path here: the
#     import repo status_error field (every Status-only read is also read by the
#     inventory refresh that runs first, so no input isolates it), the sessionlink
#     transcript bound (memory is not observable), the specialist rollback on a
#     cancelled directory (needs a cancel at a precise point), and the stale vector
#     drop (vectors need an embedding provider).
# Each [fixed] assertion must fail on the baseline binary and pass on the new one.
# [guard] assertions hold on both.

# s18_rows FILE: the number of non-empty lines of a bank or document file.
s18_rows() {
  local n
  n=$(grep -c . "$1" 2>/dev/null || true)
  printf '%s' "${n:-0}"
}

# s18_wait_pass LOG: wait until the server logged its first maintenance pass,
# which runs after the memory compaction of that pass. Bounded at 100 s.
s18_wait_pass() {
  local i
  for i in $(seq 1 100); do
    grep -q 'pass(es) over' "$1" 2>/dev/null && return 0
    sleep 1
  done
  return 1
}

# s18_guide_names ROOT: the qualified names of docs/guide.md in the project store,
# one per line, sorted. The store is read with plain SELECTs after the server stops.
s18_guide_names() {
  sqlite3 "$1/.leankg/leankg.db" \
    "SELECT qualified_name FROM code_elements WHERE file_path = 'docs/guide.md' ORDER BY qualified_name"
}

# A. Compaction and REST retain of pre-redaction rows.
s18_compaction() {
  local cdir="$WORK/s18/compact" banks rest_port rest hex raw leak
  hex=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
  raw="lkg_$hex"
  write_fixture "$cdir"
  run_cmd compact-index "$LEANKG_BIN" index "$cdir" --no-embed
  assert_eq "index for the compaction store exits 0" 0 "$RC"

  # Rows as a store written before write-side redaction holds them. "pair" also
  # holds the redacted twin a post-fix write would store for the same memory.
  banks="$cdir/.leankg/memory/banks"
  mkdir -p "$banks"
  jq -cn --arg c "probe note keeps $raw for later" \
    '{id: "r1", content: $c, source: "", timestamp: 1, importance: 0, cwd: "", metadata: null}' >"$banks/pair.jsonl"
  jq -cn --arg c "probe note keeps [REDACTED:key] for later" \
    '{id: "r2", content: $c, source: "", timestamp: 2, importance: 0, cwd: "", metadata: null}' >>"$banks/pair.jsonl"
  jq -cn --arg c "probe note keeps $raw for later" \
    '{id: "s1", content: $c, source: "", timestamp: 1, importance: 0, cwd: "", metadata: null}' >"$banks/solo.jsonl"
  jq -cn --arg c "probe note keeps $raw for later" \
    '{id: "t1", content: $c, source: "", timestamp: 1, importance: 0, cwd: "", metadata: null}' >"$banks/retain.jsonl"

  rest_port=$(free_port)
  rest="http://127.0.0.1:$rest_port"
  start_server compact "$cdir" --memory --hindsight-compat --rest "127.0.0.1:$rest_port" || return 0
  wait_listener_health compact-rest-health "$rest"

  # The REST retain of the redacted form, before any compaction runs.
  http_request compact-retain POST "$rest/v1/default/banks/retain/memories" \
    -H 'Content-Type: application/json' \
    --data-raw "$(jq -cn '{items: [{content: "probe note keeps [REDACTED:key] for later"}]}')"
  assert_eq "REST retain of the redacted twin returns 200" 200 "$HTTP_STATUS"
  assert_eq "[fixed] retain of the redacted twin replaces the pre-redaction row (one row on disk)" \
    1 "$(s18_rows "$banks/retain.jsonl")"

  step "waiting for the first maintenance pass (memory compaction runs in it)"
  if ! s18_wait_pass "$WORK/raw/server-18-compact.log"; then
    record "[fixed] maintenance pass ran" "pass(es) over" "none in 100 s" FAIL "logs/server-18-compact.log"
    log "FAIL  maintenance pass did not run within 100 s"
  fi
  stop_server

  assert_eq "[fixed] pair bank holds one row after compaction (raw row and its twin are one memory)" \
    1 "$(s18_rows "$banks/pair.jsonl")"
  if grep -qF "$hex" "$banks/pair.jsonl"; then leak=present; else leak=absent; fi
  assert_eq "[fixed] pair bank holds no raw secret after compaction" absent "$leak"
  if grep -qF "$hex" "$banks/solo.jsonl"; then leak=present; else leak=absent; fi
  assert_eq "[fixed] solo bank holds no raw secret after compaction" absent "$leak"
  assert_contains "[fixed] solo row carries the [REDACTED:key] marker" "[REDACTED:key]" "$(cat "$banks/solo.jsonl")"
  assert_eq "[guard] solo bank still holds its one row" 1 "$(s18_rows "$banks/solo.jsonl")"
  assert_eq "[fixed] retain bank still holds its one row after compaction" 1 "$(s18_rows "$banks/retain.jsonl")"
}

# B. Markdown ownership across run orders. The file contents are fixed so the
# expected rows are fixed: two headings, so two index rows and no docindex rows.
s18_ownership() {
  local guide='# Guide
intro
## Setup
steps
' root names
  if ! command -v sqlite3 >/dev/null 2>&1; then
    skip "sqlite3 is not installed; the ownership check reads the store with it"
    return 0
  fi

  # Order 1 (the failing one before the fix): the index stamps the store first,
  # then the file appears and `import docs` runs before `index` again.
  root="$WORK/s18/owner-docs-after-index"
  write_fixture "$root"
  run_cmd owner1-index "$LEANKG_BIN" index "$root" --no-embed
  assert_eq "index of the stamped store exits 0" 0 "$RC"
  mkdir -p "$root/docs"
  printf '%s' "$guide" >"$root/docs/guide.md"
  start_server owner1 "$root" || return 0
  mcp_request owner1-tools "$SERVER_URL" tools/list '{}'
  local contract
  contract=$(printf '%s' "$MCP_JSON" | jq -r '.result.tools[] | select(.name == "import") | .inputSchema.properties.action.description' 2>/dev/null)
  assert_contains "[fixed] import tool contract names the docindex split for action=docs" \
    "docs = markdown under path via docindex" "$contract"
  mcp_tool owner1-docs "$SERVER_URL" import '{"action":"docs","path":"docs"}'
  assert_eq "import docs over the new file succeeds" "" "$MCP_ERROR"
  mcp_tool owner1-repo "$SERVER_URL" import "$(jq -cn --arg p "$root" '{action: "repo", path: $p}')"
  assert_eq "import repo after import docs succeeds" "" "$MCP_ERROR"
  stop_server
  names=$(s18_guide_names "$root")
  printf '%s\n' "$names" | redact >"$CAP_DIR/owner1-guide-rows.txt"
  assert_eq "[fixed] docs after index, then index: guide.md holds no docindex rows" 0 \
    "$(printf '%s\n' "$names" | grep -c '#' || true)"
  assert_eq "[fixed] docs after index, then index: guide.md holds its two index rows" 2 \
    "$(printf '%s\n' "$names" | grep -c '::' || true)"

  # Order 2 (passes on both): `import docs` on a fresh store, then `index`.
  root="$WORK/s18/owner-docs-first"
  write_fixture "$root"
  mkdir -p "$root/docs"
  printf '%s' "$guide" >"$root/docs/guide.md"
  start_server owner2 "$root" || return 0
  mcp_tool owner2-docs "$SERVER_URL" import '{"action":"docs","path":"docs"}'
  assert_eq "import docs on a fresh store succeeds" "" "$MCP_ERROR"
  mcp_tool owner2-repo "$SERVER_URL" import "$(jq -cn --arg p "$root" '{action: "repo", path: $p}')"
  stop_server
  names=$(s18_guide_names "$root")
  assert_eq "[guard] docs first on a fresh store, then index: no docindex rows" 0 \
    "$(printf '%s\n' "$names" | grep -c '#' || true)"
  assert_eq "[guard] docs first on a fresh store, then index: two index rows" 2 \
    "$(printf '%s\n' "$names" | grep -c '::' || true)"

  # Order 3 (passes on both): index, then `import docs`, then `index`.
  root="$WORK/s18/owner-index-first"
  write_fixture "$root"
  mkdir -p "$root/docs"
  printf '%s' "$guide" >"$root/docs/guide.md"
  run_cmd owner3-index "$LEANKG_BIN" index "$root" --no-embed
  assert_eq "index before import docs exits 0" 0 "$RC"
  start_server owner3 "$root" || return 0
  mcp_tool owner3-docs "$SERVER_URL" import '{"action":"docs","path":"docs"}'
  mcp_tool owner3-repo "$SERVER_URL" import "$(jq -cn --arg p "$root" '{action: "repo", path: $p}')"
  stop_server
  names=$(s18_guide_names "$root")
  assert_eq "[guard] index first, then docs, then index: no docindex rows" 0 \
    "$(printf '%s\n' "$names" | grep -c '#' || true)"
  assert_eq "[guard] index first, then docs, then index: two index rows" 2 \
    "$(printf '%s\n' "$names" | grep -c '::' || true)"
}

# D. Core-file bound measures the redacted text. `token=x` is 7 bytes raw and
# 16 bytes redacted, so 200 of them are 1600 bytes raw and 3400 bytes redacted:
# under the 2200-byte bound as typed, over it as written.
s18_core_bound() {
  local mdir="$WORK/s18/core" core expanded plain
  write_fixture "$mdir"
  run_cmd core-index "$LEANKG_BIN" index "$mdir" --no-embed
  assert_eq "index for the core-bound store exits 0" 0 "$RC"
  start_server core "$mdir" --memory || return 0
  expanded=$(printf 'token=x %.0s' $(seq 200))
  mcp_tool core-expanded "$SERVER_URL" import \
    "$(jq -cn --arg t "$expanded" '{action: "memory", command: "add", file: "MEMORY.md", path: "MEMORY.md", text: $t}')"
  assert_contains "[fixed] add whose redacted text passes the 2200-byte bound is refused" \
    "over the 2200-byte limit" "$MCP_ERROR"
  core=$(find "$mdir/.leankg/memory" -name MEMORY.md 2>/dev/null | head -1)
  assert_eq "[fixed] the refused add leaves MEMORY.md without the token text" 0 \
    "$(grep -c 'token=' "$core" 2>/dev/null || true)"

  plain=$(printf 'lorem ipsum %.0s' $(seq 300))
  mcp_tool core-plain "$SERVER_URL" import \
    "$(jq -cn --arg t "$plain" '{action: "memory", command: "add", file: "MEMORY.md", path: "MEMORY.md", text: $t}')"
  assert_contains "[guard] add whose raw text is over the bound is refused" \
    "over the 2200-byte limit" "$MCP_ERROR"
  stop_server
}

scenario_main() {
  s18_compaction
  s18_ownership
  s18_core_bound

  unit_gate ./internal/memory/ TestCompactDropsVectorOfRedactedRow \
    "stale vector of a redacted row (no embedding provider in the harness)"
  unit_gate ./internal/core/ TestImportIndexNamesStatusFailure \
    "import repo names a Status failure in status_error"
  unit_gate ./internal/sessionlink/ TestParsedBoundsKeptTranscripts \
    "sessionlink keeps at most 8 transcripts"
  unit_gate ./internal/index/ TestCancelRollsBackSpecialistFiles \
    "specialist files roll back on a cancelled directory"
}
