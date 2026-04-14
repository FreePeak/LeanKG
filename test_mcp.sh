#!/bin/bash
# Test MCP tools with timeout

timeout=60

test_tool() {
    local name=$1
    local input=$2
    echo "=== Testing: $name ==="
    echo "$input" | timeout $timeout ./target/release/leankg mcp-stdio 2>&1 | head -30
    echo ""
}

INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}'
INITED='{"jsonrpc":"2.0","id":0,"method":"notifications/initialized","params":{}}'
TOOLS='{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'

HELLO='{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"mcp_hello","arguments":{}}}'
SEARCH='{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search_code","arguments":{"query":"main"}}}'
CLUSTERS='{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_clusters","arguments":{"limit":5}}}'
CONTEXT='{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"get_context","arguments":{"file":"src/main.rs"}}}'
CODE_TREE='{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"get_code_tree","arguments":{"max_files":5}}}'
TRACE='{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"get_traceability","arguments":{"element":"src/main.rs::main"}}}'
DEPS='{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"get_dependencies","arguments":{"file":"src/main.rs"}}}'
DOC_TREE='{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"get_doc_tree","arguments":{}}}'
ORCH='{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"orchestrate","arguments":{"intent":"context for main"}}}'
GETTERS='{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"get_dependents","arguments":{"file":"src/main.rs"}}}'
CALLERS='{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"get_callers","arguments":{"function":"main"}}}'
CALLGRAPH='{"jsonrpc":"2.0","id":14,"method":"tools/call","params":{"name":"get_call_graph","arguments":{"function":"src/main.rs::main"}}}'

echo "Starting LeanKG MCP tests..."

# Send init -> initialized -> tools list -> individual tools
(
    echo "$INIT"
    sleep 0.5
    echo "$INITED"
    sleep 0.5
    echo "$TOOLS"
    sleep 0.5
    echo "$HELLO"
    sleep 0.5
    echo "$SEARCH"
    sleep 0.5
    echo "$CLUSTERS"
    sleep 0.5
    echo "$CONTEXT"
    sleep 0.5
    echo "$CODE_TREE"
    sleep 0.5
    echo "$TRACE"
    sleep 0.5
    echo "$DEPS"
    sleep 0.5
    echo "$DOC_TREE"
    sleep 0.5
    echo "$ORCH"
    sleep 0.5
    echo "$GETTERS"
    sleep 0.5
    echo "$CALLERS"
    sleep 0.5
    echo "$CALLGRAPH"
) | timeout 120 ./target/release/leankg mcp-stdio 2>&1 | grep -v "^$" | grep -v "MCP stdio" | head -200
