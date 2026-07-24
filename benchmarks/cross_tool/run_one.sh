#!/usr/bin/env bash
# Run a single (repo, arm, run_idx) headless `claude -p` invocation and emit
# exactly one JSON line on stdout describing the run. Any non-zero exit code
# from `claude` is captured but the harness always emits the JSON line so the
# aggregator can see failed runs.
#
# Usage:
#   run_one.sh <repo_path> <prompt> <arm> <run_idx> <model> <mcp_config_path> <output_path>
#
# Arguments:
#   repo_path        absolute path to the cloned repo to run inside
#   prompt           architecture question (passed verbatim to `claude -p`)
#   arm              "with" | "without"
#   run_idx          1..N (matches codegraph methodology = 4 runs per arm)
#   model            claude model id, e.g. "sonnet" or "opus"
#   mcp_config_path  absolute path to the strict-mcp-config JSON
#   output_path      absolute path to the .jsonl file we APPEND one line to
#
# Output JSON line shape:
#   {
#     "repo": "...", "arm": "with|without", "run_idx": 1,
#     "model": "...", "prompt_chars": 1234,
#     "exit_code": 0,
#     "duration_s": 41.2,
#     "total_cost_usd": 0.36,
#     "input_tokens": 265000, "output_tokens": 4500, "cache_read_tokens": 200000,
#     "tool_calls": 2, "file_reads": 0,
#     "num_turns": 1,
#     "stop_reason": "end_turn",
#     "result_chars": 1234
#   }
#
# Implementation notes:
#   - `claude -p --output-format json` returns a single JSON envelope on stdout
#     with the full session metrics (cost, tokens, num_turns, etc).
#   - `--dangerously-skip-permissions` mirrors codegraph's methodology (the
#     without-arm still has read access, this just removes the interactive
#     prompt guard).
#   - We measure wall-clock around the call and use the JSON envelope's
#     `total_cost_usd` for cost.

set -uo pipefail

REPO_PATH="${1:?repo_path required}"
PROMPT="${2:?prompt required}"
ARM="${3:?arm required}"
RUN_IDX="${4:?run_idx required}"
MODEL="${5:-}"              # optional: if empty, claude -p uses its default
MCP_CONFIG_PATH="${6:?mcp_config_path required}"
OUTPUT_PATH="${7:?output_path required}"

# Claude Code 2.1+ strips ToolSearch under --bare, which makes MCP tools
# undiscoverable to the agent. For the WITH arm we want MCP tools to be
# available, so we drop --bare there. WITHOUT arm keeps --bare for hermetic
# isolation (no global CLAUDE.md / hooks / plugins).
BARE_FLAG="--bare"
if [[ "${ARM}" == "with" ]]; then
  BARE_FLAG=""
fi

if [[ ! -d "${REPO_PATH}" ]]; then
  echo "ERROR: repo path does not exist: ${REPO_PATH}" >&2
  exit 2
fi
if [[ ! -f "${MCP_CONFIG_PATH}" ]]; then
  echo "ERROR: mcp config not found: ${MCP_CONFIG_PATH}" >&2
  exit 2
fi

# Per-run working directory (a transient scratch dir under results/)
SCRATCH_DIR="$(dirname "${OUTPUT_PATH}")/scratch/$(basename "${REPO_PATH}")/${ARM}/run_${RUN_IDX}"
mkdir -p "${SCRATCH_DIR}"

RUN_JSON="${SCRATCH_DIR}/claude.json"
RUN_STDERR="${SCRATCH_DIR}/claude.stderr.log"

# Capture stdout/stderr, measure wall-clock.
START_NS=$(date +%s%N)
set +e
( cd "${REPO_PATH}" && \
  claude -p "${PROMPT}" \
    ${MODEL:+--model "${MODEL}"} \
    ${BARE_FLAG} \
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

# Defaults (in case the JSON envelope is missing fields)
TOTAL_COST="0"
INPUT_TOKENS="0"
OUTPUT_TOKENS="0"
CACHE_READ_TOKENS="0"
TOOL_CALLS="0"
FILE_READS="0"
NUM_TURNS="0"
STOP_REASON="unknown"
RESULT_CHARS="0"
ACTUAL_MODEL=""
MCP_SERVERS=""
MCP_TOOLS="0"
PROMPT_CHARS=$(printf '%s' "${PROMPT}" | wc -c | tr -d ' ')

# Parse the JSON envelope. `claude -p --output-format json` returns a JSON
# ARRAY of message events with a final `{"type":"result", ...}` element. The
# CLI version has changed shape several times; we try several strategies and
# fall back to zeros on any failure so the run is still recorded.
if [[ -s "${RUN_JSON}" ]] && command -v python3 >/dev/null 2>&1; then
  PARSED=$(python3 - "${RUN_JSON}" <<'PY'
import json, sys, re, pathlib

path = pathlib.Path(sys.argv[1])
try:
    raw = path.read_text(encoding="utf-8", errors="replace")
except Exception as exc:
    print(f"PARSE_ERROR:{exc}")
    sys.exit(0)

# Strip leading whitespace/BOM and try json.loads.
raw = raw.strip()
try:
    data = json.loads(raw)
except json.JSONDecodeError:
    # Try to find the first JSON object or array in the text
    m = re.search(r"(\{.*\}|\[.*\])", raw, flags=re.DOTALL)
    if not m:
        print("PARSE_ERROR:no_json")
        sys.exit(0)
    try:
        data = json.loads(m.group(0))
    except json.JSONDecodeError as exc:
        print(f"PARSE_ERROR:{exc}")
        sys.exit(0)

def num(v, default=0):
    if isinstance(v, bool):
        return default
    if isinstance(v, (int, float)):
        return int(v) if isinstance(v, int) else v
    return default

def get_result_element(d):
    """Find the {type: 'result'} element regardless of envelope shape."""
    if isinstance(d, list):
        # New shape (2.1.201+): JSON array of events, last is the result.
        for elem in reversed(d):
            if isinstance(elem, dict) and elem.get("type") == "result":
                return elem
        return {}
    if isinstance(d, dict):
        if d.get("type") == "result":
            return d
        # Older shape: top-level result fields directly on the dict.
        if "total_cost_usd" in d or "usage" in d or "num_turns" in d:
            return d
    return {}

def get_init_element(d):
    """Find the {type: 'system', subtype: 'init'} element regardless of shape."""
    events = d if isinstance(d, list) else [d]
    for elem in events:
        if not isinstance(elem, dict):
            continue
        if elem.get("type") == "system" and elem.get("subtype") == "init":
            return elem
    return {}

def get_mcp_summary(init_elem):
    """Extract the actual model + MCP server/tool names from the init event.

    This is the ground truth of whether the MCP server attached: the harness
    can lie about flags, but the init event cannot. If `mcp_servers` is empty
    in this event, no MCP server reached the model — the run is invalid for
    the WITH arm.
    """
    if not isinstance(init_elem, dict):
        return "", [], 0
    actual_model = str(init_elem.get("model", "") or "")
    raw_servers = init_elem.get("mcp_servers", []) or []
    if not isinstance(raw_servers, list):
        raw_servers = []
    server_names = [
        str(s.get("name", "")) if isinstance(s, dict) else str(s)
        for s in raw_servers
        if s
    ]
    tools = init_elem.get("tools", []) or []
    if not isinstance(tools, list):
        tools = []
    mcp_tool_count = sum(
        1 for t in tools
        if isinstance(t, str) and t.startswith("mcp__")
    )
    return actual_model, server_names, mcp_tool_count

def walk_tool_uses(d):
    """Count tool_use blocks and Read invocations across all events."""
    tool_calls = 0
    file_reads = 0
    events = d if isinstance(d, list) else [d]
    for event in events:
        if not isinstance(event, dict):
            continue
        msg = event.get("message") if isinstance(event.get("message"), dict) else None
        if msg is None and event.get("type") == "assistant":
            msg = event.get("message") if isinstance(event.get("message"), dict) else None
        # Either event.message.content or event.content (older shape)
        content = None
        if msg is not None:
            content = msg.get("content")
        if content is None:
            content = event.get("content")
        if not isinstance(content, list):
            continue
        for block in content:
            if not isinstance(block, dict):
                continue
            if block.get("type") == "tool_use":
                tool_calls += 1
                name = (block.get("name") or "").lower()
                if name == "read":
                    file_reads += 1
                # MCP tools typically appear as mcp__leankg__<tool>; treat as
                # a tool call but not a Read.
            elif block.get("type") == "tool_result":
                # Don't double-count; tool_results follow tool_use blocks.
                pass
    return tool_calls, file_reads

result = get_result_element(data)
usage = result.get("usage", {}) if isinstance(result, dict) else {}

total_cost = num(result.get("total_cost_usd", 0), 0)
input_tokens = num(usage.get("input_tokens", 0), 0)
output_tokens = num(usage.get("output_tokens", 0), 0)
cache_read = num(usage.get("cache_read_input_tokens", 0), 0)
num_turns = num(result.get("num_turns", 0), 0)
stop_reason = str(result.get("stop_reason", "unknown"))
result_chars = len(str(result.get("result", "")))

# Tool calls: prefer the explicit envelope field, else walk the transcript.
tool_calls = num(result.get("tool_use_count", 0), 0)
file_reads = num(result.get("file_read_count", 0), 0)
if tool_calls == 0 or file_reads == 0:
    walk_calls, walk_reads = walk_tool_uses(data)
    if tool_calls == 0:
        tool_calls = walk_calls
    if file_reads == 0:
        file_reads = walk_reads

init_elem = get_init_element(data)
actual_model, mcp_server_names, mcp_tool_count = get_mcp_summary(init_elem)
print(f"COST={total_cost}")
print(f"INPUT={input_tokens}")
print(f"OUTPUT={output_tokens}")
print(f"CACHE={cache_read}")
print(f"TURNS={num_turns}")
print(f"STOP={stop_reason}")
print(f"RESULT_CHARS={result_chars}")
print(f"TOOL_CALLS={tool_calls}")
print(f"FILE_READS={file_reads}")
print(f"ACTUAL_MODEL={actual_model}")
print(f"MCP_SERVERS={','.join(mcp_server_names)}")
print(f"MCP_TOOLS={mcp_tool_count}")
PY
  )
  while IFS='=' read -r key value; do
    case "${key}" in
      COST) TOTAL_COST="${value}" ;;
      INPUT) INPUT_TOKENS="${value}" ;;
      OUTPUT) OUTPUT_TOKENS="${value}" ;;
      CACHE) CACHE_READ_TOKENS="${value}" ;;
      TURNS) NUM_TURNS="${value}" ;;
      STOP) STOP_REASON="${value//\"/}" ;;
      RESULT_CHARS) RESULT_CHARS="${value}" ;;
      TOOL_CALLS) TOOL_CALLS="${value}" ;;
      FILE_READS) FILE_READS="${value}" ;;
      ACTUAL_MODEL) ACTUAL_MODEL="${value}" ;;
      MCP_SERVERS) MCP_SERVERS="${value}" ;;
      MCP_TOOLS) MCP_TOOLS="${value}" ;;
    esac
  done <<< "${PARSED}"
fi

# Defaults if the parser didn't emit them (e.g. parse failure)
ACTUAL_MODEL="${ACTUAL_MODEL:-}"
MCP_SERVERS="${MCP_SERVERS:-}"
MCP_TOOLS="${MCP_TOOLS:-0}"

# Compute validity from the ground-truth init event. If any rule fails, the
# JSONL row still gets written (so we keep a paper trail) but is flagged
# `valid: false` with a reason. The aggregator filters these out.
INVALID_REASONS=()
if [[ "${EXIT_CODE}" != "0" ]]; then
  INVALID_REASONS+=("exit_code=${EXIT_CODE}")
fi
if [[ "${TOTAL_COST}" == "0" || "${TOTAL_COST}" == "0.0" ]]; then
  INVALID_REASONS+=("zero_cost")
fi
if [[ "${ARM}" == "with" && -z "${MCP_SERVERS}" ]]; then
  INVALID_REASONS+=("no_mcp_attached")
fi
if [[ -n "${INVALID_REASONS[*]:-}" ]]; then
  VALID="false"
  INVALID_REASON="$(IFS='|'; echo "${INVALID_REASONS[*]}")"
else
  VALID="true"
  INVALID_REASON=""
fi

# Emit the JSON line. Use python for safe quoting.
python3 - "${REPO_PATH}" "${ARM}" "${RUN_IDX}" "${MODEL}" "${PROMPT_CHARS}" \
  "${EXIT_CODE}" "${DURATION_S}" "${TOTAL_COST}" "${INPUT_TOKENS}" \
  "${OUTPUT_TOKENS}" "${CACHE_READ_TOKENS}" "${TOOL_CALLS}" "${FILE_READS}" \
  "${NUM_TURNS}" "${STOP_REASON}" "${RESULT_CHARS}" \
  "${ACTUAL_MODEL}" "${MCP_SERVERS}" "${MCP_TOOLS}" \
  "${VALID}" "${INVALID_REASON}" "${OUTPUT_PATH}" <<'PY'
import json, pathlib, sys

(repo_path, arm, run_idx, model, prompt_chars, exit_code, duration_s,
 total_cost, input_tokens, output_tokens, cache_read, tool_calls, file_reads,
 num_turns, stop_reason, result_chars,
 actual_model, mcp_servers, mcp_tools,
 valid, invalid_reason, output_path) = sys.argv[1:]

record = {
    "repo": pathlib.Path(repo_path).name,
    "arm": arm,
    "run_idx": int(run_idx),
    "model": model if model else None,
    "actual_model": actual_model or None,
    "mcp_servers": [s for s in (mcp_servers or "").split(",") if s],
    "mcp_tool_count": int(mcp_tools),
    "valid": valid == "true",
    "invalid_reason": invalid_reason or None,
    "prompt_chars": int(prompt_chars),
    "exit_code": int(exit_code),
    "duration_s": round(float(duration_s), 3),
    "total_cost_usd": float(total_cost),
    "input_tokens": int(input_tokens),
    "output_tokens": int(output_tokens),
    "cache_read_tokens": int(cache_read),
    "tool_calls": int(tool_calls),
    "file_reads": int(file_reads),
    "num_turns": int(num_turns),
    "stop_reason": stop_reason,
    "result_chars": int(result_chars),
}

out = pathlib.Path(output_path)
out.parent.mkdir(parents=True, exist_ok=True)
with out.open("a", encoding="utf-8") as fh:
    fh.write(json.dumps(record, ensure_ascii=False) + "\n")
PY

# Surface progress on stderr so the user can follow the long run
VALID_TAG=""
if [[ "${VALID}" != "true" ]]; then
  VALID_TAG=" [INVALID: ${INVALID_REASON}]"
fi
echo "  ${ARM} run ${RUN_IDX}: exit=${EXIT_CODE} dur=${DURATION_S}s cost=\$${TOTAL_COST} tools=${TOOL_CALLS} reads=${FILE_READS} model=${ACTUAL_MODEL:-?} mcp=[${MCP_SERVERS:-none}]${VALID_TAG}" >&2