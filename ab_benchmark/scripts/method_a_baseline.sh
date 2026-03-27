#!/bin/bash
# Method A: Baseline approach using standard file reading
# This simulates what an AI tool does WITHOUT LeanKG - reading entire files
# or using basic grep to find relevant content

QUERY="$1"
WORKTREE_DIR="/Users/linh.doan/work/harvey/freepeak/.worktree/leankg-ab-benchmark"

CONTEXT=""

search_files() {
    local term="$1"
    local files=$(grep -rl "$term" "${WORKTREE_DIR}/src" 2>/dev/null | head -5 || true)
    for file in $files; do
        if [ -f "$file" ]; then
            CONTEXT="${CONTEXT}
===== FILE: ${file} =====
$(cat "$file" 2>/dev/null || echo "Could not read $file")
"
        fi
    done
}

extract_context() {
    local term="$1"
    local result=$(grep -rn "$term" "${WORKTREE_DIR}/src" 2>/dev/null | head -20 || true)
    if [ -n "$result" ]; then
        CONTEXT="${CONTEXT}
===== SEARCH RESULTS for '${term}' =====
${result}
"
    fi
}

for word in $QUERY; do
    search_files "$word"
done

if [ -z "$CONTEXT" ]; then
    CONTEXT="No relevant files found for query: ${QUERY}"
fi

echo "${CONTEXT}"
