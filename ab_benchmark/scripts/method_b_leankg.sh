#!/bin/bash
# Method B: LeanKG approach using MCP tools
# This uses the LeanKG MCP server to fetch targeted subgraph context

QUERY="$1"
WORKTREE_DIR="/Users/linh.doan/work/harvey/freepeak/.worktree/leankg-ab-benchmark"

CONTEXT=""

extract_key_terms() {
    echo "$QUERY" | tr ' ' '\n' | grep -v "the\|and\|or\|for\|to\|in\|of\|what\|how\|where\|is\|are\|does\|if\|I\|a\|an" | head -5
}

query_leankg() {
    local term="$1"
    
    search_result=$(cd "${WORKTREE_DIR}" && cargo run --quiet -- search "$term" 2>/dev/null || true)
    if [ -n "$search_result" ]; then
        CONTEXT="${CONTEXT}
===== LEANKG SEARCH: ${term} =====
${search_result}
"
    fi
    
    context_result=$(cd "${WORKTREE_DIR}" && cargo run --quiet -- context "$term" 2>/dev/null || true)
    if [ -n "$context_result" ]; then
        CONTEXT="${CONTEXT}
===== LEANKG CONTEXT: ${term} =====
${context_result}
"
    fi
}

get_file_context() {
    local file="$1"
    if [ -f "${WORKTREE_DIR}/${file}" ]; then
        CONTEXT="${CONTEXT}
===== FILE: ${file} (via LeanKG context) =====
$(cat "${WORKTREE_DIR}/${file}" 2>/dev/null || true)
"
    fi
}

find_and_get_context() {
    local term="$1"
    
    search_result=$(cd "${WORKTREE_DIR}" && cargo run --quiet -- find "$term" 2>/dev/null || true)
    if [ -n "$search_result" ]; then
        CONTEXT="${CONTEXT}
===== LEANKG FIND: ${term} =====
${search_result}
"
        
        file_path=$(echo "$search_result" | grep -oE 'src/[^:]+\.(rs|txt|md)' | head -1)
        if [ -n "$file_path" ]; then
            get_file_context "$file_path"
        fi
    fi
}

for term in $(extract_key_terms); do
    query_leankg "$term"
    find_and_get_context "$term"
done

if [ -z "$CONTEXT" ]; then
    CONTEXT="LeanKG returned no results for query: ${QUERY}"
fi

echo "${CONTEXT}"
