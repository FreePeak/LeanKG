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

# Postgres: PSQL="psql …" or LEANKG_PG_URL or docker exec leankg-pg-phase0.
psql_at() {
  local sql="$1"
  if [[ -n "${PSQL:-}" ]]; then
    # shellcheck disable=SC2086
    $PSQL -Atc "$sql"
  elif [[ -n "${LEANKG_PG_URL:-}" ]]; then
    psql "$LEANKG_PG_URL" -Atc "$sql"
  elif command -v docker >/dev/null 2>&1 && docker ps --format '{{.Names}}' 2>/dev/null | grep -qx 'leankg-pg-phase0'; then
    docker exec leankg-pg-phase0 psql -U postgres -d leankg -Atc "$sql"
  else
    echo "Set PSQL or LEANKG_PG_URL (or run leankg-pg-phase0 container)" >&2
    exit 1
  fi
}

model_table_sanitized() {
  echo "${1//-/_}"
}

has_column() {
  local table="$1" col="$2"
  psql_at "SELECT 1 FROM information_schema.columns \
    WHERE table_schema = current_schema() AND table_name = '${table}' AND column_name = '${col}' LIMIT 1" \
    | grep -q 1
}

table_exists() {
  local table="$1"
  psql_at "SELECT to_regclass('${table}') IS NOT NULL" | grep -q t
}

count_for() {
  local mid="$1"
  local sanitized
  sanitized="$(model_table_sanitized "$mid")"

  if has_column "embedding_vectors" "model_id"; then
    psql_at "SELECT count(*) FROM embedding_vectors WHERE model_id = '${mid}'"
  elif table_exists "embedding_vectors_${sanitized}"; then
    psql_at "SELECT count(*) FROM embedding_vectors_${sanitized}"
  elif [[ "$mid" == "$MODEL_A" ]] && table_exists "embedding_vectors"; then
    echo "WARN: legacy single-table embedding_vectors (no model_id); counting all rows for MODEL_A only" >&2
    psql_at "SELECT count(*) FROM embedding_vectors"
  else
    echo 0
  fi
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
  COUNT_A1="$(count_for "$MODEL_A")"
  echo "MODEL_A rows after embed: ${COUNT_A1}"
  if [[ "${COUNT_A1}" -le 0 ]]; then
    echo "FAIL: MODEL_A count is 0 after local embed" >&2
    exit 1
  fi

  embed_under_model "3." "$MODEL_B" "openai"
  COUNT_B1="$(count_for "$MODEL_B")"
  COUNT_A2="$(count_for "$MODEL_A")"
  echo "MODEL_B rows=${COUNT_B1}  MODEL_A rows still=${COUNT_A2}"
  if [[ "${COUNT_B1}" -le 0 ]]; then
    echo "FAIL: MODEL_B count is 0 after API embed" >&2
    exit 1
  fi
  if [[ "${COUNT_A2}" != "${COUNT_A1}" ]]; then
    echo "FAIL: MODEL_A collection changed after switch to MODEL_B (${COUNT_A1} -> ${COUNT_A2})" >&2
    exit 1
  fi

  echo "== 4. Switch back to MODEL_A (pointer only; no re-embed required) =="
  export LEANKG_EMBED_ACTIVE_MODEL="$MODEL_A"
  export LEANKG_EMBED_PROVIDER=local
  export LEANKG_EMBED_FAST="${LEANKG_EMBED_FAST:-1}"
  export LEANKG_EMBED_MODEL="${LEANKG_EMBED_MODEL:-bge-q}"
  COUNT_A3="$(count_for "$MODEL_A")"
  COUNT_B2="$(count_for "$MODEL_B")"
  echo "MODEL_A rows=${COUNT_A3}  MODEL_B rows still=${COUNT_B2}"
  if [[ "${COUNT_A3}" != "${COUNT_A1}" ]]; then
    echo "FAIL: MODEL_A count changed after flip back (${COUNT_A1} -> ${COUNT_A3})" >&2
    exit 1
  fi
  if [[ "${COUNT_B2}" != "${COUNT_B1}" ]]; then
    echo "FAIL: MODEL_B count changed after flip back (${COUNT_B1} -> ${COUNT_B2})" >&2
    exit 1
  fi

  optional_semantic_search

  echo ""
  echo "OK: switch smoke passed — both collections intact across A → B → A"
}

main "$@"
