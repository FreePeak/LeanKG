#!/usr/bin/env bash
# run_one_q.sh — Run a single (arm, question, run_idx) claude -p invocation.
#
# Usage:
#   run_one_q.sh <arm> <q_id> <run_idx> <model> <mcp_config_path> <prompt>
#
# Env (required from parent):
#   REPO_PATH, RESULTS_DIR, CLAUDE_BIN, DATE (or computed), DRY_RUN

set -euo pipefail

ARM="${1:?arm}"
Q_ID="${2:?q_id}"
RUN_IDX="${3:?run_idx}"
MODEL="${4:-}"
MCP_CONFIG_PATH="${5:?mcp_config}"
PROMPT="${6:?prompt}"

REPO_PATH="${REPO_PATH:?REPO_PATH required}"
RESULTS_DIR="${RESULTS_DIR:?RESULTS_DIR required}"
CLAUDE_BIN="${CLAUDE_BIN:-$(command -v claude)}"
DRY_RUN="${DRY_RUN:-0}"
DATE="${DATE:-$(date +%Y-%m-%d)}"

Q_OUTPUT="${RESULTS_DIR}/runs/${DATE}/${ARM}/${Q_ID}"
mkdir -p "${Q_OUTPUT}"
RUN_JSON="${Q_OUTPUT}/run_${RUN_IDX}.json"
RUN_STDERR="${Q_OUTPUT}/run_${RUN_IDX}.stderr.log"

if [[ "${DRY_RUN}" == "1" ]]; then
  echo "[${ARM}/${Q_ID}] dry run ${RUN_IDX}: ${PROMPT:0:60}..."
  exit 0
fi

START_NS=$(date +%s%N)
set +e
( cd "${REPO_PATH}" && \
  "${CLAUDE_BIN}" -p "${PROMPT}" \
    ${MODEL:+--model "${MODEL}"} \
    --mcp-config "${MCP_CONFIG_PATH}" \
    --strict-mcp-config \
    --output-format json \
    --dangerously-skip-permissions \
    --no-session-persistence \
) > "${RUN_JSON}" 2> "${RUN_STDERR}"
EXIT_CODE=$?
set -e
END_NS=$(date +%s%N)
DURATION_S=$(awk -v s="${START_NS}" -v e="${END_NS}" 'BEGIN { printf "%.3f", (e - s) / 1e9 }')

cost="0"; input_tok="0"; output_tok="0"; cache_tok="0"; turns="0"
stop_reason="unknown"; tool_calls="0"; file_reads="0"; result_chars="0"
actual_model=""; mcp_servers=""; mcp_tools="0"

if [[ -s "${RUN_JSON}" ]]; then
  PARSED=$(python3 - "${RUN_JSON}" <<'PYEOF'
import json, sys, pathlib, re
path = pathlib.Path(sys.argv[1])
try:
    raw = path.read_text(encoding="utf-8", errors="replace").strip()
except Exception as exc:
    print(f"PARSE_ERROR:{exc}"); sys.exit(0)
try:
    data = json.loads(raw)
except json.JSONDecodeError:
    m = re.search(r"(\{.*\}|\[.*\])", raw, flags=re.DOTALL)
    if not m:
        print("PARSE_ERROR:no_json"); sys.exit(0)
    try: data = json.loads(m.group(0))
    except Exception as exc:
        print(f"PARSE_ERROR:{exc}"); sys.exit(0)

def num(v, default=0):
    if isinstance(v, bool): return default
    if isinstance(v, (int, float)): return int(v) if isinstance(v, int) else v
    return default

def get_result(d):
    if isinstance(d, list):
        for e in reversed(d):
            if isinstance(e, dict) and e.get("type") == "result": return e
        return {}
    if isinstance(d, dict):
        if d.get("type") == "result" or "total_cost_usd" in d or "usage" in d: return d
    return {}

def get_init(d):
    for e in (d if isinstance(d, list) else [d]):
        if isinstance(e, dict) and e.get("type") == "system" and e.get("subtype") == "init":
            return e
    return {}

def walk_tools(d):
    tc = fr = 0
    for event in (d if isinstance(d, list) else [d]):
        if not isinstance(event, dict): continue
        msg = event.get("message") if isinstance(event.get("message"), dict) else None
        content = (msg or {}).get("content") if msg else event.get("content")
        if not isinstance(content, list): continue
        for b in content:
            if isinstance(b, dict) and b.get("type") == "tool_use":
                tc += 1
                if (b.get("name") or "").lower() == "read": fr += 1
    return tc, fr

result = get_result(data)
usage = result.get("usage", {}) if isinstance(result, dict) else {}
tc = num(result.get("tool_use_count", 0), 0)
fr = num(result.get("file_read_count", 0), 0)
if tc == 0 or fr == 0:
    wtc, wfr = walk_tools(data)
    if tc == 0: tc = wtc
    if fr == 0: fr = wfr
init = get_init(data)
servers = init.get("mcp_servers", []) or []
snames = [str(s.get("name","")) if isinstance(s, dict) else str(s) for s in servers if s]
tools = init.get("tools", []) or []
mcp_n = sum(1 for t in tools if isinstance(t, str) and t.startswith("mcp__"))
print(f"COST={num(result.get('total_cost_usd',0),0)}")
print(f"INPUT={num(usage.get('input_tokens',0),0)}")
print(f"OUTPUT={num(usage.get('output_tokens',0),0)}")
print(f"CACHE={num(usage.get('cache_read_input_tokens',0),0)}")
print(f"TURNS={num(result.get('num_turns',0),0)}")
print(f"STOP={result.get('stop_reason','unknown')}")
print(f"RESULT_CHARS={len(str(result.get('result','')))}")
print(f"TOOL_CALLS={tc}")
print(f"FILE_READS={fr}")
print(f"ACTUAL_MODEL={init.get('model','') or ''}")
print(f"MCP_SERVERS={','.join(snames)}")
print(f"MCP_TOOLS={mcp_n}")
PYEOF
  )
  while IFS='=' read -r key value; do
    case "${key}" in
      COST) cost="${value}" ;;
      INPUT) input_tok="${value}" ;;
      OUTPUT) output_tok="${value}" ;;
      CACHE) cache_tok="${value}" ;;
      TURNS) turns="${value}" ;;
      STOP) stop_reason="${value//\"/}" ;;
      RESULT_CHARS) result_chars="${value}" ;;
      TOOL_CALLS) tool_calls="${value}" ;;
      FILE_READS) file_reads="${value}" ;;
      ACTUAL_MODEL) actual_model="${value}" ;;
      MCP_SERVERS) mcp_servers="${value}" ;;
      MCP_TOOLS) mcp_tools="${value}" ;;
    esac
  done <<< "${PARSED}"
fi

valid="true"; invalid_reason=""
if [[ "${EXIT_CODE}" != "0" ]]; then
  valid="false"; invalid_reason="exit_code=${EXIT_CODE}"
elif [[ "${cost}" == "0" || "${cost}" == "0.0" ]]; then
  valid="false"; invalid_reason="zero_cost"
elif [[ "${ARM}" == "leankg" && -z "${mcp_servers}" ]]; then
  valid="false"; invalid_reason="no_mcp_attached"
elif [[ "${ARM}" == "codegraph" && -z "${mcp_servers}" ]]; then
  valid="false"; invalid_reason="no_mcp_attached"
fi

python3 - "${Q_ID}" "${ARM}" "${RUN_IDX}" "${MODEL}" "${PROMPT}" \
  "${EXIT_CODE}" "${DURATION_S}" "${cost}" "${input_tok}" \
  "${output_tok}" "${cache_tok}" "${tool_calls}" "${file_reads}" \
  "${turns}" "${stop_reason}" "${result_chars}" \
  "${actual_model}" "${mcp_servers}" "${mcp_tools}" \
  "${valid}" "${invalid_reason}" "${Q_OUTPUT}" <<'PY'
import json, pathlib, sys
(q_id, arm, run_idx, model, prompt, exit_code, duration_s,
 cost, input_tok, output_tok, cache_tok, tool_calls, file_reads,
 turns, stop_reason, result_chars,
 actual_model, mcp_servers, mcp_tools,
 valid, invalid_reason, output_dir) = sys.argv[1:]
record = {
    "question_id": q_id, "repo": "alamofire", "arm": arm,
    "run_idx": int(run_idx), "model": model or None,
    "actual_model": actual_model or None,
    "mcp_servers": [s for s in (mcp_servers or "").split(",") if s],
    "mcp_tool_count": int(mcp_tools),
    "valid": valid == "true",
    "invalid_reason": invalid_reason or None,
    "prompt_chars": len(prompt), "exit_code": int(exit_code),
    "duration_s": round(float(duration_s), 3),
    "total_cost_usd": float(cost),
    "input_tokens": int(input_tok), "output_tokens": int(output_tok),
    "cache_read_tokens": int(cache_tok),
    "tool_calls": int(tool_calls), "file_reads": int(file_reads),
    "num_turns": int(turns), "stop_reason": stop_reason,
    "result_chars": int(result_chars),
}
out = pathlib.Path(output_dir) / "runs.jsonl"
out.parent.mkdir(parents=True, exist_ok=True)
with out.open("a", encoding="utf-8") as fh:
    fh.write(json.dumps(record, ensure_ascii=False) + "\n")
PY

tag=""
[[ "${valid}" != "true" ]] && tag=" [INVALID: ${invalid_reason}]"
echo "[${ARM}/${Q_ID}] run ${RUN_IDX}: dur=${DURATION_S}s cost=\$${cost} tools=${tool_calls} reads=${file_reads} tok=${input_tok}/${output_tok} model=${actual_model:-?}${tag}"
