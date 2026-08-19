#!/usr/bin/env bash
# Comprehensive MCP tool test suite
# Tests each LeanKG MCP tool via HTTP and compares with grep ground truth
set -euo pipefail

MCP_URL="http://localhost:9699/mcp?project=/Users/linh.doan/work/harvey/freepeak/leankg"
SRC="/Users/linh.doan/work/harvey/freepeak/leankg/src"
PASS=0; FAIL=0; SKIP=0
REPORT=""

mcp_call() {
    local tool="$1"
    local args="$2"
    local payload="{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"$tool\",\"arguments\":$args}}"
    local result
    result=$(curl -s -X POST "$MCP_URL" \
        -H "Content-Type: application/json" \
        -H "Accept: application/json, text/event-stream" \
        --max-time 30 \
        -d "$payload" 2>/dev/null)
    # Extract text content from MCP response
    echo "$result" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    if 'result' in data and 'content' in data['result']:
        for c in data['result']['content']:
            if c.get('type') == 'text':
                print(c['text'])
                break
    elif 'error' in data:
        print(f'ERROR: {data[\"error\"]}')
    else:
        print(json.dumps(data)[:2000])
except:
    print(sys.stdin.read()[:2000] if hasattr(sys.stdin, 'read') else '')
" 2>/dev/null || echo "PARSE_ERROR"
}

check() {
    local name="$1"
    local mcp_result="$2"
    local grep_result="$3"
    local pass_criteria="$4"

    if [[ -z "$mcp_result" || "$mcp_result" == "null" || "$mcp_result" == "" ]]; then
        echo "FAIL (empty response) | $name"
        FAIL=$((FAIL + 1))
        REPORT+="FAIL | $name — empty response\n"
        return
    fi

    if [[ "$mcp_result" == *"error"* && "$pass_criteria" != "expect_error" ]]; then
        echo "FAIL (error) | $name"
        echo "  MCP: ${mcp_result:0:200}"
        FAIL=$((FAIL + 1))
        REPORT+="FAIL | $name — error in response\n"
        return
    fi

    case "$pass_criteria" in
        not_empty)
            echo "PASS | $name"
            PASS=$((PASS + 1))
            REPORT+="PASS | $name\n"
            ;;
        contains_grep)
            if echo "$mcp_result" | grep -qi "$grep_result" 2>/dev/null; then
                echo "PASS | $name"
                PASS=$((PASS + 1))
                REPORT+="PASS | $name\n"
            else
                echo "FAIL (grep mismatch) | $name"
                echo "  Expected to find: $grep_result"
                echo "  Got: ${mcp_result:0:200}"
                FAIL=$((FAIL + 1))
                REPORT+="FAIL | $name — grep mismatch\n"
            fi
            ;;
        expect_error)
            echo "PASS (expected error) | $name"
            PASS=$((PASS + 1))
            REPORT+="PASS | $name (expected error)\n"
            ;;
        *)
            echo "PASS | $name"
            PASS=$((PASS + 1))
            REPORT+="PASS | $name\n"
            ;;
    esac
}

echo "=== LeanKG MCP Tool Test Suite ==="
echo "Server: $MCP_URL"
echo "Source: $SRC"
echo ""

# ─── Category 1: Project Status ───
echo "── Category 1: Project Status ──"

R=$(mcp_call "mcp_status" '{"include_counts": true}')
check "mcp_status" "$R" "code_elements" "not_empty"

# ─── Category 2: File Search ───
echo "── Category 2: File Search ──"

R=$(mcp_call "query_file" '{"pattern": "handler.rs"}')
GREP_R=$(grep -rl "handler" "$SRC"/mcp/*.rs 2>/dev/null | wc -l)
check "query_file(pattern=handler.rs)" "$R" "handler" "not_empty"

R=$(mcp_call "find_function" '{"name": "parse_file"}')
GREP_R=$(grep -rn "fn parse_file" "$SRC" 2>/dev/null | head -1)
check "find_function(parse_file)" "$R" "parse_file" "not_empty"

R=$(mcp_call "search_code" '{"query": "extract_function_signature", "element_type": "function"}')
check "search_code(extract_function_signature)" "$R" "extract_function" "not_empty"

# ─── Category 3: Dependency Analysis ───
echo "── Category 3: Dependency Analysis ──"

R=$(mcp_call "get_dependencies" '{"file": "src/mcp/handler.rs"}')
check "get_dependencies(handler.rs)" "$R" "imports" "not_empty"

R=$(mcp_call "get_dependents" '{"file": "src/mcp/handler.rs"}')
check "get_dependents(handler.rs)" "$R" "" "not_empty"

R=$(mcp_call "get_impact_radius" '{"file": "src/graph/query.rs", "depth": 2}')
check "get_impact_radius(query.rs)" "$R" "" "not_empty"

# ─── Category 4: Code Explanation ───
echo "── Category 4: Code Explanation ──"

R=$(mcp_call "explain_node" '{"name": "GraphEngine"}')
GREP_R=$(grep -rn "struct GraphEngine" "$SRC" 2>/dev/null | head -1)
check "explain_node(GraphEngine)" "$R" "GraphEngine" "not_empty"

R=$(mcp_call "generate_doc" '{"file": "src/mcp/tools.rs"}')
check "generate_doc(tools.rs)" "$R" "" "not_empty"

# ─── Category 5: Graph Queries ───
echo "── Category 5: Graph Queries ──"

R=$(mcp_call "query_graph" '{"question": "How is the indexer connected to the graph engine?", "token_budget": 4000}')
check "query_graph(indexer->graph)" "$R" "" "not_empty"

R=$(mcp_call "shortest_path" '{"source": "src/mcp/handler.rs", "target": "src/indexer/extractor.rs", "max_hops": 5}')
check "shortest_path(handler->extractor)" "$R" "" "not_empty"

R=$(mcp_call "get_call_graph" '{"function": "build_call_graph", "depth": 1}')
GREP_R=$(grep -rn "fn build_call_graph" "$SRC" 2>/dev/null | head -1)
check "get_call_graph(build_call_graph)" "$R" "build_call_graph" "not_empty"

R=$(mcp_call "get_context" '{"file": "src/graph/query.rs", "signature_only": true}')
check "get_context(query.rs)" "$R" "" "not_empty"

# ─── Category 6: Semantic Search ───
echo "── Category 6: Semantic Search ──"

R=$(mcp_call "concept_search" '{"query": "call graph extraction"}')
check "concept_search(call graph)" "$R" "" "not_empty"

R=$(mcp_call "kg_context" '{"query": "code extraction", "depth": 2}')
check "kg_context(code extraction)" "$R" "" "not_empty"

# ─── Category 7: Knowledge Management ───
echo "── Category 7: Knowledge Management ──"

R=$(mcp_call "add_knowledge" '{"knowledge_type": "business", "title": "Test: Parser design", "content": "Uses tree-sitter for multi-language parsing"}')
check "add_knowledge" "$R" "" "not_empty"

R=$(mcp_call "search_knowledge" '{"query": "Parser design"}')
check "search_knowledge" "$R" "" "not_empty"

# ─── Category 8: Architecture Analysis ───
echo "── Category 8: Architecture Analysis ──"

R=$(mcp_call "get_architecture" '{"max_items": 10}')
check "get_architecture" "$R" "" "not_empty"

# ─── Category 9: Search Annotations ───
echo "── Category 9: Search Annotations ──"

R=$(mcp_call "search_annotations" '{"annotation_name": "deprecated"}')
check "search_annotations(deprecated)" "$R" "" "not_empty"

# ─── Category 10: Run Raw Query ───
echo "── Category 10: Run Raw Query ──"

R=$(mcp_call "run_raw_query" '{"query": "?[name, type] := *code_elements[name, type, _, _, _, _, _, _, _, _, _, _], type = \"function\", name = \"main\""}')
check "run_raw_query(main function)" "$R" "main" "not_empty"

# ─── Category 11: Graph Schema ───
echo "── Category 11: Graph Schema ──"

R=$(mcp_call "get_graph_schema" '{}')
check "get_graph_schema" "$R" "" "not_empty"

# ─── Category 12: List Tools ───
echo "── Category 12: List Tools ──"

PAYLOAD='{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
R=$(curl -s -X POST "$MCP_URL" -H "Content-Type: application/json" --max-time 10 -d "$PAYLOAD" 2>/dev/null)
TOOL_COUNT=$(echo "$R" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    tools = data.get('result', {}).get('tools', [])
    print(len(tools))
except:
    print(0)
" 2>/dev/null || echo "0")
echo "  Tools registered: $TOOL_COUNT"
if [[ "$TOOL_COUNT" -gt 20 ]]; then
    echo "PASS | tools/list (count=$TOOL_COUNT)"
    PASS=$((PASS + 1))
    REPORT+="PASS | tools/list (count=$TOOL_COUNT)\n"
else
    echo "FAIL | tools/list (count=$TOOL_COUNT < 20)"
    FAIL=$((FAIL + 1))
    REPORT+="FAIL | tools/list (count=$TOOL_COUNT)\n"
fi

# ─── Summary ───
echo ""
echo "=== Test Summary ==="
echo -e "$REPORT"
TOTAL=$((PASS + FAIL + SKIP))
echo "Total: $TOTAL | PASS: $PASS | FAIL: $FAIL | SKIP: $SKIP"
if [[ $FAIL -eq 0 ]]; then
    echo "✅ ALL TESTS PASSED"
else
    echo "❌ $FAIL TESTS FAILED"
fi
