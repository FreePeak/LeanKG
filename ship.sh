#!/bin/bash
set -e

PROJECT_NAME=${1:-$(basename $(pwd))}
LOG_DIR="logs"
LOG_FILE="$LOG_DIR/ship.log"
MAX_RETRIES=5
RETRY_COUNT=0

mkdir -p "$LOG_DIR"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOG_FILE"
}

log "=== Ship starting for $PROJECT_NAME (Superpowers Edition) ==="

if [ ! -f TODO.md ]; then
    if [ -f docs/PRD.md ]; then
        log "No TODO.md found. Creating from PRD..."
        opencode run "Create TODO.md from docs/PRD.md with unchecked items [- ] for each task" --project "$PROJECT_NAME"
    else
        log "ERROR: No TODO.md or docs/PRD.md found. Create TODO.md to begin."
        exit 1
    fi
fi

TASK_PROMPT="You are working on project '$PROJECT_NAME' with Superpowers enabled.

MANDATORY WORKFLOW (follow in order):

1. Use LeanKG first: Run mcp_status to check if ready. Use tools like search_code, find_function, get_impact_radius, get_dependencies BEFORE using grep.

2. For each unchecked item [- ] in TODO.md:
   
   a) BRAINSTORM: Load superpowers/brainstorming skill. Refine the task through questions, present design in sections for validation.
   
   b) PLAN: Load superpowers/writing-plans skill. Break work into 2-5 minute tasks with exact file paths and verification steps.
   
   c) WORKTREE: Load superpowers/using-git-worktrees skill. Create isolated branch workspace.
   
   d) TDD: Load superpowers/test-driven-development skill. Follow RED-GREEN-REFACTOR:
      - Write failing test
      - Watch it fail
      - Write minimal code
      - Watch it pass
      - Delete code written before tests
      - Commit
   
   e) REVIEW: Load superpowers/requesting-code-review skill. Review against plan, report critical issues.
   
   f) REPEAT for next item

3. When all items complete:
   - Load superpowers/finishing-a-development-branch skill
   - Verify tests pass
   - Present options: merge/PR/keep/discard
   - Clean up worktree

4. Mark each completed item as [- [x]] in TODO.md

Model priority: minimax-m2.7 (primary) → minimax-m2.5-free (fallback)"

while true; do
    UNDONE=$(grep -c "^\- \[ \]" TODO.md 2>/dev/null || echo "0")
    log "$UNDONE items remaining"
    
    if [ "$UNDONE" -eq 0 ]; then
        log "All items complete! Ship finished."
        break
    fi
    
    log "Running iteration with Superpowers..."
    if opencode run "$TASK_PROMPT" --project "$PROJECT_NAME" 2>&1 | tee -a "$LOG_FILE"; then
        RETRY_COUNT=0
    else
        RETRY_COUNT=$((RETRY_COUNT + 1))
        log "Iteration failed. Retry $RETRY_COUNT/$MAX_RETRIES"
        if [ $RETRY_COUNT -ge $MAX_RETRIES ]; then
            log "Max retries reached. Stopping."
            break
        fi
    fi
    
    sleep 2
done

log "=== Ship complete at $(date) ==="