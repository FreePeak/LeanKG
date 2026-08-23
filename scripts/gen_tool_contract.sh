#!/usr/bin/env bash
# gen_tool_contract.sh — H7 / FR-PLG-5: stable MCP tool contract generator.
#
# Parses src/mcp/tools.rs (ToolRegistry::list_tools) and emits
# docs/mcp-tool-contract.md: every tool name, purpose, stability tier,
# input-schema summary, and approximate introduction version.
#
# Deterministic: output depends only on tools.rs + the embedded since/tier
# table below; records are emitted in registry order. POSIX awk only
# (works under mawk on GitHub runners).
#
# Usage:
#   scripts/gen_tool_contract.sh                 # regenerate docs/mcp-tool-contract.md
#   scripts/gen_tool_contract.sh --stdout        # print contract to stdout
#   scripts/gen_tool_contract.sh --verify LIST   # exit 1 if any name in LIST file
#                                                # is absent from the registry
#
# Adding a tool: append it to SINCE_TABLE below (since=unreleased, tier=beta)
# then re-run this script; CI fails on drift until the doc is committed.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TOOLS_RS="${LEANKG_TOOLS_RS:-$ROOT/src/mcp/tools.rs}"
DOC="$ROOT/docs/mcp-tool-contract.md"

mode="${1:-write}"

# tool|since|tier — curated at each release; new tools default to
# unreleased/beta until promoted. Tier policy: beta -> stable requires one
# full minor release without schema change.
SINCE_TABLE='add_annotation|v0.17.1|stable
add_documentation|v0.17.1|stable
add_knowledge|v0.17.1|stable
add_ontology_concept|v0.19.7|beta
add_ontology_workflow|v0.19.7|beta
agent_diary_read|v0.18.0|stable
agent_diary_write|v0.18.0|stable
agent_focus|v0.18.0|stable
check_consistency|v0.18.0|stable
concept_search|v0.17.7|stable
ctx_read|v0.8.5|stable
delete_knowledge|v0.17.1|stable
delete_ontology_concept|v0.19.7|beta
detect_changes|v0.6.0|stable
embed_control|v0.19.2|beta
explain_node|v0.18.0|stable
export_graph_snapshot|v0.18.0|stable
export_html|v0.19.11|beta
find_env_conflicts|v0.17.1|stable
find_large_functions|v0.0.1|stable
find_related_docs|v0.0.1|stable
find_route|v0.16.6|stable
find_tunnels|v0.18.0|stable
generate_doc|v0.0.1|stable
get_architecture|v0.17.9|stable
get_call_graph|v0.0.1|stable
get_cluster_skill|v0.18.0|stable
get_clusters|v0.6.0|stable
get_code_tree|v0.0.1|stable
get_context|v0.0.1|stable
get_dependencies|v0.0.1|stable
get_dependents|v0.0.1|stable
get_doc_tree|v0.0.1|stable
get_feature_flow|v0.19.9|beta
get_files_for_doc|v0.0.1|stable
get_god_nodes|v0.18.0|stable
get_impact_radius|v0.0.1|stable
get_nav_callers|v0.16.6|stable
get_nav_graph|v0.16.6|stable
get_overview_context|v0.18.0|stable
get_pr_impact|v0.18.0|stable
get_review_context|v0.0.1|stable
get_screen_args|v0.16.6|stable
get_service_context|v0.17.1|stable
get_service_graph|v0.15.2|stable
get_team_map|v0.18.0|stable
get_tested_by|v0.0.1|stable
get_traceability|v0.0.1|stable
get_traceability_matrix|v0.19.9|beta
get_upcoming_changes|v0.17.1|stable
index_prd|v0.19.9|beta
kg_context|v0.17.1|stable
kg_ontology_status|v0.17.1|stable
kg_semantic_context|v0.17.8|stable
kg_trace_workflow|v0.17.1|stable
link_element|v0.17.1|stable
mcp_index|v0.2.7|stable
mcp_index_docs|v0.5.2|stable
mcp_init|v0.2.7|stable
mcp_install|v0.2.7|stable
mcp_status|v0.2.7|stable
ontology_control|v0.19.3|beta
promote_environment|v0.17.1|stable
query_graph|v0.19.2|beta
query_incidents|v0.17.1|stable
report_query_outcome|v0.18.0|stable
resolve_with_lsp|v0.18.0|stable
run_raw_query|v0.15.2|stable
search_code|v0.0.1|stable
search_knowledge|v0.17.1|stable
semantic_search|v0.17.1|stable
set_embed_model|v0.26.0|beta
shortest_path|v0.18.0|stable
temporal_query|v0.18.0|stable
timeline|v0.18.0|stable
update_knowledge|v0.17.1|stable'

# tool|removed_in|replacement — deprecation history (H6 consolidation round 2).
# removed_in stays "unreleased" until the release ships, then pin the version.
REMOVED_TABLE='get_graph_report|unreleased (v0.28)|get_god_nodes + get_architecture
orchestrate|unreleased (v0.28)|query_graph / kg_context / search_code
search_by_requirement|unreleased (v0.28)|get_traceability / get_traceability_matrix'

[ -f "$TOOLS_RS" ] || { echo "error: tools registry not found: $TOOLS_RS" >&2; exit 2; }

# Materialize tables to temp files (multi-line -v is not portable across awks)
TABLE_FILE="$(mktemp)"
trap 'rm -f "$TABLE_FILE" "$REMOVED_FILE"' EXIT
printf '%s\n' "$SINCE_TABLE" > "$TABLE_FILE"
REMOVED_FILE="$(mktemp)"
printf '%s\n' "$REMOVED_TABLE" > "$REMOVED_FILE"

# --- Extract registry (POSIX awk; no gawk capture groups) ----------------------
# Emits records: NAME \t SINCE \t TIER \t DESCRIPTION \t PROPS \t REQUIRED
EXTRACTED="$(awk '
FNR == NR {                       # first file = since/tier table
  split($0, f, "|")
  since[f[1]] = f[2]; tier[f[1]] = f[3]
  next
}
BEGIN {
  SP24 = "                        "   # 24 spaces: top-level property indent
  SP20 = "                    "       # 20 spaces: required-array indent
}
function flush() {
  if (name == "") return
  gsub(/^[ \t]+|[ \t]+$/, "", desc)
  gsub(/ {3,}/, " ", desc)             # collapse rust continuation indent
  gsub(/\|/, "\\|", desc)
  printf "%s\t%s\t%s\t%s\t%s\t%s\n", name,
    (name in since ? since[name] : "unreleased"),
    (name in tier ? tier[name] : "beta"),
    desc, props, req
  name = ""; desc = ""; props = ""; req = ""; state = ""
}
{
  line = $0

  # Record start:  name: "tool_name".to_string(),
  if (match(line, /name: "[a-zA-Z_0-9]+"\.to_string\(\)/)) {
    flush()
    seg = substr(line, RSTART, RLENGTH)
    sub(/"[^"]*\.to_string\(\)$/, "", seg)   # drop closing quote + .to_string()
    name = substr(seg, 8)
    state = "want_desc"
    next
  }

  # Description start (Rust field, not JSON key):  description: "..."
  if (state == "want_desc" && (i = index(line, "description: \"")) > 0) {
    state = "in_desc"
    line = substr(line, i + length("description: \""))
  }

  if (state == "in_desc") {
    if ((t = index(line, ".to_string()")) > 0) {
      part = substr(line, 1, t - 1)
      sub(/[ \t]*"$/, "", part)
      desc = desc part
      state = "schema"
    } else {
      sub(/[ \t]*\\$/, "", line)          # rust line-continuation backslash
      sub(/[ \t]*$/, "", line)
      sub(/"$/, "", line)                 # closing quote on its own segment
      desc = desc line " "
    }
    next
  }

  if (name != "") {
    # Top-level property: 24-space indent + "prop": {"type": "T"
    if (substr(line, 1, 24) == SP24 && substr(line, 25, 1) == "\"" &&
        (ci = index(line, ": {\"type\": \"")) > 0) {
      rest = substr(line, 25)
      q = index(substr(rest, 2), "\"")          # closing quote of prop name
      pname = substr(rest, 2, q - 1)
      ti = ci - 24 + length(": {\"type\": \"")   # offset within rest
      tseg = substr(rest, ti)
      te = index(tseg, "\"")
      ptype = substr(tseg, 1, te - 1)
      props = props (props == "" ? "" : ", ") pname ":" ptype
    }
    # Required list: 20-space indent + "required": [ ... ]
    else if (substr(line, 1, 20) == SP20 &&
             substr(line, 21, 13) == "\"required\": [") {
      tail = substr(line, 34)
      eb = index(tail, "]")
      req = eb > 1 ? substr(tail, 1, eb - 1) : ""
    }
    # Record close: 16-space indent + }),
    else if (line ~ /^ {16}\}\),/) {
      flush()
    }
  }
}
END { flush() }
' "$TABLE_FILE" "$TOOLS_RS")"

RECORD_COUNT="$(printf '%s\n' "$EXTRACTED" | grep -c . || true)"
[ "${RECORD_COUNT:-0}" -gt 0 ] || { echo "error: no ToolDefinition entries parsed from $TOOLS_RS" >&2; exit 2; }

# --- Modes --------------------------------------------------------------------
case "$mode" in
  --verify)
    list_file="${2:-}"
    [ -f "$list_file" ] || { echo "usage: $0 --verify LIST_FILE" >&2; exit 2; }
    rc=0
    while IFS= read -r want; do
      [ -n "$want" ] || continue
      if ! printf '%s\n' "$EXTRACTED" | awk -F'\t' -v w="$want" '$1 == w { found = 1 } END { exit found ? 0 : 1 }'; then
        echo "UNREGISTERED TOOL: $want (not in $TOOLS_RS)" >&2
        rc=1
      fi
    done < "$list_file"
    if [ "$rc" -eq 0 ]; then echo "OK: all listed tools are registered"; fi
    exit "$rc"
    ;;
  --stdout|--write|write|"") ;;
  *) echo "usage: $0 [--stdout | --verify LIST_FILE]" >&2; exit 2 ;;
esac

# --- Render -------------------------------------------------------------------
render() {
  printf '%s\n' \
'<!-- GENERATED-BY: scripts/gen_tool_contract.sh --><!-- DO NOT EDIT BY HAND -->' \
'' \
'# MCP Tool Contract' \
'' \
"Regenerated from \`src/mcp/tools.rs\` (\`ToolRegistry::list_tools\`). **${RECORD_COUNT} tools.**" \
'To change the surface: edit the registry, run `scripts/gen_tool_contract.sh`, commit both.' \
'' \
'## Stability tiers' \
'' \
'- **stable** — input schema and output shape are contractual; breaking changes follow the deprecation policy below.' \
'- **beta** — may change or be removed in any minor release; feedback welcome.' \
'- New tools enter as **beta** (`since: unreleased`) and are promoted after one minor release without schema change.' \
'' \
'## Deprecation policy' \
'' \
'- Tool removal requires **2 minor releases** of deprecation notices (doc + tool description marked deprecated).' \
'- A breaking input-schema change to a stable tool requires a **minor version bump treated as major-equivalent**, plus a release notice.' \
'- Additive optional properties do not break the contract.' \
'' \
'## Deprecation history' \
'' \
'Removed tools, their removal release, and the surviving replacement surface.' \
'' \
'| Tool | Removed in | Replacement |' \
'|------|------------|-------------|'

  printf '%s\n' "$REMOVED_TABLE" | awk -F'|' '
    { printf "| `%s` | %s | %s |\n", $1, $2, $3 }'

  printf '%s\n' \
'' \
'## Tools' \
'' \
'| Tool | Tier | Since | Purpose | Input schema |' \
'|------|------|-------|---------|--------------|'

  printf '%s\n' "$EXTRACTED" | awk -F'\t' '
    function schema_summary(p, req,   out, arr, i, n, kv, pname, star) {
      out = ""
      n = split(p, arr, ", ")
      for (i = 1; i <= n; i++) {
        split(arr[i], kv, ":")
        pname = kv[1]; if (pname == "") continue
        star = (index(req, "\"" pname "\"") > 0) ? "*" : ""
        out = out (out == "" ? "" : ", ") star pname ":" kv[2]
      }
      if (req != "" && req != "[]") {
        gsub(/\"/, "", req)
        out = out " — required: " req
      }
      return (out == "" ? "(none)" : "`" out "`")
    }
    {
      printf "| `%s` | %s | %s | %s | %s |\n", $1, $2, $3, $4, schema_summary($5, $6)
    }'
}

if [ "$mode" = "--stdout" ]; then
  render
else
  mkdir -p "$(dirname "$DOC")"
  render > "${DOC}.tmp"
  mv "${DOC}.tmp" "$DOC"
  echo "wrote $DOC (${RECORD_COUNT} tools)"
fi
