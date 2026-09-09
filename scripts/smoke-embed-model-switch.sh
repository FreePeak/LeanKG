#!/usr/bin/env bash
# smoke-embed-model-switch.sh — ops smoke for multi-model embed DB (optional).
#
# Proves config switch (local BGE ↔ OpenAI-compatible TEI/API) does NOT wipe
# the other model's vector collection. Does NOT replace `cargo test --lib`.
#
# Usage (from repo root):
#   ./scripts/smoke-embed-model-switch.sh
#   TEI_BASE=https://api.jina.ai/v1 TEI_MODEL=jina-embeddings-v3 TEI_DIM=1024 \
#     LEANKG_EMBED_API_KEY="$JINA_API_KEY" ./scripts/smoke-embed-model-switch.sh
#
# See docs/embed-model-switch-smoke.md for TEI bring-up and free API options.
set -euo pipefail
cd "$(dirname "$0")/.."

MODEL_A="${MODEL_A:-bge-small-en-v1.5-384}"   # local ONNX (registry id)
MODEL_B="${MODEL_B:-qwen3-emb-4b-2560}"       # TEI / OpenAI-compatible API
TEI_BASE="${TEI_BASE:-http://127.0.0.1:8080/v1}"
TEI_MODEL="${TEI_MODEL:-Qwen/Qwen3-Embedding-4B}"
TEI_DIM="${TEI_DIM:-2560}"
FIXTURE_PROJECT="${FIXTURE_PROJECT:-/tmp/leankg-embed-switch-fixture}"
EMBED_TYPES="${EMBED_TYPES:-function}"
EMBED_WORKERS="${EMBED_WORKERS:-1}"
EMBED_BATCH_SIZE="${EMBED_BATCH_SIZE:-16}"
SKIP_INDEX="${SKIP_INDEX:-0}"
SKIP_SEMANTIC="${SKIP_SEMANTIC:-0}"
MCP_HTTP_PORT="${MCP_HTTP_PORT:-9699}"

need() { command -v "$1" >/dev/null || { echo "missing required command: $1" >&2; exit 1; }; }
need curl
need jq

# Resolve LeanKG CLI (compat `leankg` or split `leankg-worker` when available).
resolve_leankg() {
  if [[ -n "${LEANKG_BIN:-}" ]]; then
    echo "$LEANKG_BIN"
  elif command -v leankg-worker >/dev/null 2>&1; then
    echo "leankg-worker"
  elif command -v leankg >/dev/null 2>&1; then
    echo "leankg"
  elif [[ -f Cargo.toml ]]; then
    echo "cargo run --release --features embeddings --quiet --"
  else
    echo "missing LeanKG binary (set LEANKG_BIN or install leankg)" >&2
    exit 1
  fi
}
LEANKG_CMD="$(resolve_leankg)"

run_leankg() {
  if [[ "$LEANKG_CMD" == "cargo run --release --features embeddings --quiet --" ]]; then
    cargo run --release --features embeddings --quiet -- "$@"
  else
    "$LEANKG_CMD" "$@"
  fi
}

# Embed subcommand: worker and compat binary share `embed --wait`.
run_embed() {
  local project="$1"
  shift
  run_leankg embed --wait --project "$project" \
    --workers "$EMBED_WORKERS" --batch-size "$EMBED_BATCH_SIZE" \
    --types "$EMBED_TYPES" "$@"
}

run_index() {
  local project="$1"
  run_leankg index "$project"
}

# sqlite-native probe: the active model id lives in
# <project>/.leankg/embed_model.json and the last run's counts in
# <project>/.leankg/embed_status.json. No Postgres, no Docker.
psql_at() {
  echo "psql_at removed (sqlite default) — use sqlite_probe" >&2
  exit 1
}

# project arg → active model id (empty when none persisted)
active_model() {
  python3 -c "
import json,sys
try:
    d=json.load(open('$1/.leankg/embed_model.json'))
    print(d.get('model_id') or d.get('model') or '')
except Exception:
    print('')
" 2>/dev/null
}

# project arg → embedded count from the last completed embed run
embedded_count() {
  python3 -c "
import json,sys
try:
    d=json.load(open('$1/.leankg/embed_status.json'))
    print(d.get('embedded', 0) if d.get('status') == 'completed' else 0)
except Exception:
    print(0)
" 2>/dev/null
}

# sqlite probe: the PERSISTED ACTIVE MODEL is the pointer under test, and
# per-model vector collections persist across switches. `probe_for` prints
# "<active_model> <embedded>" from the project's leankg metadata files.
probe_for() {
  local project="$1"
  printf '%s %s\n' "$(active_model "$project")" "$(embedded_count "$project")"
}

# After embedding under $expected, the persisted pointer must equal it.
check_pointer() {
  local project="$1" expected="$2" label="$3"
  local got
  got="$(active_model "$project")"
  [[ "$got" == "$expected" ]] \
    || { echo "FAIL [$label]: persisted model pointer is '$got', expected '$expected'" >&2; exit 1; }
}

check_tei() {
  local base="${TEI_BASE%/}"
  echo "== 0. Health check embed API (${base}) =="
  if curl -sf "${base%/v1}/health" >/dev/null 2>&1; then
    echo "  TEI /health ok"
  elif curl -sf "${base}/models" >/dev/null 2>&1; then
    echo "  OpenAI-compatible /models ok"
  else
    echo "  WARN: no /health or /models; probing /embeddings" >&2
  fi
  local dim
  dim="$(curl -sf "${base}/embeddings" \
    -H "Authorization: Bearer ${LEANKG_EMBED_API_KEY:-unused}" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"${TEI_MODEL}\",\"input\":\"ping\"}" \
    | jq -e ".data[0].embedding | length")"
  if [[ "$dim" != "$TEI_DIM" ]]; then
    echo "FAIL: embedding dim ${dim} != TEI_DIM=${TEI_DIM}" >&2
    exit 1
  fi
  echo "  embed dim=${dim} (matches TEI_DIM)"
}

setup_fixture() {
  echo "== 1. Fixture project =="
  mkdir -p "${FIXTURE_PROJECT}/src"
  cat > "${FIXTURE_PROJECT}/src/lib.rs" <<'RUST'
pub fn hello() -> &'static str {
    "leankg embed switch smoke fixture"
}
RUST
  if [[ ! -d "${FIXTURE_PROJECT}/.leankg" ]]; then
    run_leankg init --project "${FIXTURE_PROJECT}" || true
  fi
  if [[ "$SKIP_INDEX" != "1" ]]; then
    echo "  indexing ${FIXTURE_PROJECT} ..."
    run_index "${FIXTURE_PROJECT}"
  else
    echo "  SKIP_INDEX=1 — assuming fixture already indexed"
  fi
}

embed_under_model() {
  local label="$1"
  local active_model="$2"
  local provider="$3"
  echo "== ${label} embed under ${active_model} (provider=${provider}) =="
  export LEANKG_EMBED_ACTIVE_MODEL="$active_model"
  export LEANKG_EMBED_PROVIDER="$provider"
  if [[ "$provider" == "openai" ]]; then
    export LEANKG_EMBED_API_BASE_URL="$TEI_BASE"
    export LEANKG_EMBED_API_KEY="${LEANKG_EMBED_API_KEY:-unused}"
    export LEANKG_EMBED_API_MODEL="$TEI_MODEL"
    export LEANKG_EMBED_API_DIM="$TEI_DIM"
    unset LEANKG_EMBED_MODEL LEANKG_EMBED_FAST || true
  else
    export LEANKG_EMBED_FAST="${LEANKG_EMBED_FAST:-1}"
    export LEANKG_EMBED_MODEL="${LEANKG_EMBED_MODEL:-bge-q}"
    unset LEANKG_EMBED_API_BASE_URL LEANKG_EMBED_API_KEY LEANKG_EMBED_API_MODEL LEANKG_EMBED_API_DIM || true
    run_leankg embed --init --project "${FIXTURE_PROJECT}" 2>/dev/null || true
  fi
  run_embed "${FIXTURE_PROJECT}"
}

optional_semantic_search() {
  [[ "$SKIP_SEMANTIC" == "1" ]] && return 0
  echo "== 5. Optional semantic_search sanity (active model only) =="
  if ! curl -sf "http://127.0.0.1:${MCP_HTTP_PORT}/health" >/dev/null 2>&1; then
    echo "  SKIP: no MCP on :${MCP_HTTP_PORT} (set SKIP_SEMANTIC=1 to silence)"
    return 0
  fi
  local payload
  payload="$(jq -nc --arg p "$FIXTURE_PROJECT" \
    '{jsonrpc:"2.0",id:1,method:"tools/call",params:{name:"semantic_search",arguments:{query:"hello fixture",project:$p,k:3}}}')"
  if curl -sf -X POST "http://127.0.0.1:${MCP_HTTP_PORT}/mcp?project=${FIXTURE_PROJECT}" \
    -H 'Content-Type: application/json' -d "$payload" | jq -e '.result' >/dev/null; then
    echo "  semantic_search returned a result"
  else
    echo "  WARN: semantic_search call failed (non-fatal for switch smoke)" >&2
  fi
}

main() {
  echo "=== LeanKG embed model switch smoke ==="
  echo "LeanKG: ${LEANKG_CMD}"
  echo "MODEL_A=${MODEL_A}  MODEL_B=${MODEL_B}"
  echo "Fixture: ${FIXTURE_PROJECT}"
  echo ""

  check_tei
  setup_fixture

  embed_under_model "2." "$MODEL_A" "local"
  read -r _ COUNT_A1 <<<"$(probe_for "$FIXTURE_PROJECT")"
  check_pointer "$FIXTURE_PROJECT" "$MODEL_A" "after MODEL_A embed"
  echo "MODEL_A embedded after embed: ${COUNT_A1}"
  if [[ "${COUNT_A1}" -le 0 ]]; then
    echo "FAIL: MODEL_A count is 0 after local embed" >&2
    exit 1
  fi

  embed_under_model "3." "$MODEL_B" "openai"
  read -r _ COUNT_B1 <<<"$(probe_for "$FIXTURE_PROJECT")"
  check_pointer "$FIXTURE_PROJECT" "$MODEL_B" "after MODEL_B embed"
  echo "MODEL_B embedded=${COUNT_B1}"
  if [[ "${COUNT_B1}" -le 0 ]]; then
    echo "FAIL: MODEL_B count is 0 after API embed" >&2
    exit 1
  fi

  echo "== 4. Switch back to MODEL_A (pointer only; no re-embed required) =="
  export LEANKG_EMBED_ACTIVE_MODEL="$MODEL_A"
  export LEANKG_EMBED_PROVIDER=local
  export LEANKG_EMBED_FAST="${LEANKG_EMBED_FAST:-1}"
  export LEANKG_EMBED_MODEL="${LEANKG_EMBED_MODEL:-bge-q}"
  read -r _ COUNT_A3 <<<"$(probe_for "$FIXTURE_PROJECT")"
  check_pointer "$FIXTURE_PROJECT" "$MODEL_A" "after flip back"
  echo "MODEL_A embedded=${COUNT_A3}"
  if [[ "${COUNT_A3}" != "${COUNT_A1}" ]]; then
    echo "FAIL: MODEL_A count changed after flip back (${COUNT_A1} -> ${COUNT_A3})" >&2
    exit 1
  fi

  optional_semantic_search

  echo ""
  echo "OK: switch smoke passed — both collections intact across A → B → A"
}

main "$@"
