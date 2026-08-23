#!/usr/bin/env bash
# tool_contract_test.sh — H7 / FR-PLG-5: tests for scripts/gen_tool_contract.sh
#
# Verifies the MCP tool contract generator:
#   1. output contains every tool name from a fixtures list (registered tools)
#   2. --verify FAILS when a fixtures list contains an unregistered fake tool
#   3. generation is deterministic (two runs, identical bytes)
#   4. generated doc carries the GENERATED-BY header
#
# Usage: bash tests/tool_contract_test.sh    (exit 0 = pass)
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GEN="$ROOT/scripts/gen_tool_contract.sh"
TOOLS_RS="$ROOT/src/mcp/tools.rs"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

[ -x "$GEN" ] || fail "gen script missing or not executable: $GEN"
[ -f "$TOOLS_RS" ] || fail "tools.rs missing: $TOOLS_RS"

# --- Fixtures: every tool expected in the registry at time of writing. -------
FIXTURES="$TMP/fixtures.txt"
cat > "$FIXTURES" <<'EOF'
add_annotation
add_documentation
add_knowledge
add_ontology_concept
add_ontology_workflow
agent_diary_read
agent_diary_write
agent_focus
check_consistency
concept_search
ctx_read
delete_knowledge
delete_ontology_concept
detect_changes
embed_control
explain_node
export_graph_snapshot
export_html
find_env_conflicts
find_large_functions
find_related_docs
find_route
find_tunnels
generate_doc
get_architecture
get_call_graph
get_cluster_skill
get_clusters
get_code_tree
get_context
get_dependencies
get_dependents
get_doc_tree
get_feature_flow
get_files_for_doc
get_god_nodes
get_graph_report
get_impact_radius
get_nav_callers
get_nav_graph
get_overview_context
get_pr_impact
get_review_context
get_screen_args
get_service_context
get_service_graph
get_team_map
get_tested_by
get_traceability
get_traceability_matrix
get_upcoming_changes
index_prd
kg_context
kg_ontology_status
kg_semantic_context
kg_trace_workflow
link_element
mcp_index
mcp_index_docs
mcp_init
mcp_install
mcp_status
ontology_control
orchestrate
promote_environment
query_graph
query_incidents
report_query_outcome
resolve_with_lsp
run_raw_query
search_by_requirement
search_code
search_knowledge
semantic_search
set_embed_model
shortest_path
temporal_query
timeline
update_knowledge
EOF

FAKE_TOOL="totally_fake_tool_xyz"

# --- Test 1: gen output contains every fixture tool name ---------------------
OUT="$TMP/contract.md"
bash "$GEN" --stdout > "$OUT" 2>"$TMP/gen.err" || fail "generator exited non-zero: $(cat "$TMP/gen.err")"
[ -s "$OUT" ] || fail "generator produced empty output"
MISSING=()
while IFS= read -r tool; do
  [ -n "$tool" ] || continue
  grep -q "| \`$tool\`" "$OUT" || MISSING+=("$tool")
done < "$FIXTURES"
[ ${#MISSING[@]} -eq 0 ] || fail "fixtures missing from generated contract: ${MISSING[*]}"
pass "all $(wc -l < "$FIXTURES" | tr -d ' ') fixture tools present in generated contract"

# --- Test 2: --verify fails on unregistered fake tool ------------------------
BADLIST="$TMP/badlist.txt"
{ cat "$FIXTURES"; echo "$FAKE_TOOL"; } > "$BADLIST"
if bash "$GEN" --verify "$BADLIST" >/dev/null 2>&1; then
  fail "--verify accepted unregistered fake tool '$FAKE_TOOL' (should exit non-zero)"
fi
pass "--verify rejects unregistered fake tool '$FAKE_TOOL'"

# --- Test 2b: --verify passes on the clean fixture list ----------------------
bash "$GEN" --verify "$FIXTURES" >/dev/null 2>&1 || fail "--verify rejected valid fixture list"
pass "--verify accepts registered fixture list"

# --- Test 3: deterministic output --------------------------------------------
OUT2="$TMP/contract2.md"
bash "$GEN" --stdout > "$OUT2" || fail "second generator run failed"
cmp -s "$OUT" "$OUT2" || fail "generator output is not deterministic across runs"
pass "generation is byte-deterministic across runs"

# --- Test 4: GENERATED-BY header present -------------------------------------
grep -q "GENERATED-BY: scripts/gen_tool_contract.sh" "$OUT" \
  || fail "generated doc missing GENERATED-BY header pointing at scripts/gen_tool_contract.sh"
pass "GENERATED-BY header points at scripts/gen_tool_contract.sh"

# --- Test 5: committed doc (if present) matches regeneration -----------------
DOC="$ROOT/docs/mcp-tool-contract.md"
if [ -f "$DOC" ]; then
  cmp -s "$DOC" "$OUT" || fail "docs/mcp-tool-contract.md drifted from src/mcp/tools.rs — run scripts/gen_tool_contract.sh"
  pass "committed docs/mcp-tool-contract.md is in sync with registry"
else
  echo "SKIP: docs/mcp-tool-contract.md not yet committed"
fi

echo "OK: all tool-contract tests passed"
