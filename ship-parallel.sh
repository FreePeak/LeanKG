#!/bin/bash
set -e

PROJECT_NAME=${1:-$(basename $(pwd))}
WORKTREE_DIR=".worktrees"
LOG_DIR="logs"
MAX_RETRIES=5

mkdir -p "$WORKTREE_DIR" "$LOG_DIR"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOG_DIR/ship-parallel.log"
}

usage() {
    echo "Usage: $0 <command> [options]"
    echo ""
    echo "Commands:"
    echo "  plan <task1,task2,task3>   Plan parallel tasks (comma-separated)"
    echo "  dispatch <num>             Dispatch N parallel subagents"
    echo "  status                     Show worktree status"
    echo "  merge                      Merge all completed worktrees to main"
    echo "  clean                      Remove all worktrees"
    echo ""
    echo "Examples:"
    echo "  $0 plan 'test-auth,test-api,test-db'"
    echo "  $0 dispatch 3"
    echo "  $0 merge"
}

# Ensure on main branch
git checkout main 2>/dev/null || git checkout master 2>/dev/null

case "$1" in
    plan)
        log "=== Planning parallel tasks ==="
        TASKS=$(echo "$2" | tr ',' '\n')
        IDX=0
        for TASK in $TASKS; do
            TASK=$(echo "$TASK" | xargs)
            BTASK=$(echo "$TASK" | tr '[:upper:]' '[:lower:]' | tr ' ' '-')
            log "Task $((++IDX)): $TASK → branch feature/$BTASK"
        done
        log "Ready to dispatch: $# tasks"
        ;;
    dispatch)
        NUM=${2:-3}
        log "=== Dispatching $NUM parallel subagents ==="
        
        # Get unchecked items
        ITEMS=$(grep "^\- \[ \]" TODO.md 2>/dev/null | head -n "$NUM" || echo "")
        if [ -z "$ITEMS" ]; then
            log "No unchecked items found in TODO.md"
            exit 1
        fi
        
        IDX=0
        declare -A WORKTREES
        
        # Create worktree for each task
        for ITEM in $ITEMS; do
            TASK_NAME=$(echo "$ITEM" | sed 's/- \[ \] //' | tr '[:upper:]' '[:lower:]' | tr ' ' '-' | cut -c1-30)
            BRANCH="feature/$(date +%s)-$TASK_NAME"
            
            log "Creating worktree: $BRANCH"
            git worktree add "$WORKTREE_DIR/$TASK_NAME" -b "$BRANCH" 2>/dev/null || {
                log "Worktree $TASK_NAME exists, skipping"
                continue
            }
            
            WORKTREES[$IDX]="$WORKTREE_DIR/$TASK_NAME"
            IDX=$((IDX + 1))
            
            if [ $IDX -ge $NUM ]; then
                break
            fi
        done
        
        log "Created ${#WORKTREES[@]} worktrees"
        
        # Dispatch subagent for each worktree
        IDX=0
        for WT in "${WORKTREES[@]}"; do
            TASK_NAME=$(basename "$WT")
            WT_ABS=$(realpath "$WT")
            
            log "Dispatching agent $((++IDX)) to $WT_ABS"
            
            (
                cd "$WT_ABS"
                opencode run "\
You are working in isolated worktree: $WT_ABS

MANDATORY WORKFLOW:
1. Use LeanKG first: mcp_status, then search_code, find_function, get_impact_radius
2. Load superpowers/test-driven-development - RED-GREEN-REFACTOR cycle
3. For your assigned task: implement, write tests, verify tests pass
4. Mark TODO item as [- [x]] in $WT_ABS/TODO.md
5. Commit: 'git add -A && git commit -m \"feat: <task>\"'
6. Return summary of changes
" --project "$PROJECT_NAME" 2>&1 | tee -a "$LOG_DIR/ship-parallel-$TASK_NAME.log"
            ) &
        done
        
        log "All $IDX agents dispatched in background"
        log "Monitor with: tail -f logs/ship-parallel-*.log"
        ;;
    status)
        log "=== Worktree Status ==="
        git worktree list
        echo ""
        for WT in $(ls -d $WORKTREE_DIR/*/ 2>/dev/null); do
            TASK=$(basename "$WT")
            COUNT=$(grep -c "^\- \[ \]" "$WT/TODO.md" 2>/dev/null || echo "?")
            log "$TASK: $COUNT items remaining"
        done
        ;;
    merge)
        log "=== Merging worktrees to main ==="
        git checkout main || git checkout master
        git pull
        
        for WT in $(ls -d $WORKTREE_DIR/*/ 2>/dev/null); do
            TASK=$(basename "$WT")
            BRANCH=$(git -C "$WT" rev-parse --abbrev-ref HEAD)
            
            log "Merging $BRANCH from $WT"
            git merge "$BRANCH" --no-edit || log "Merge conflict in $TASK - resolve manually"
            
            # Prune worktree after merge
            git worktree prune
            rm -rf "$WT"
        done
        
        log "Push changes:"
        echo "  git push"
        ;;
    clean)
        log "=== Cleaning worktrees ==="
        for WT in $(ls -d $WORKTREE_DIR/*/ 2>/dev/null); do
            TASK=$(basename "$WT")
            git worktree remove "$WT" --force 2>/dev/null || true
            log "Removed $TASK"
        done
        git worktree prune
        log "Clean complete"
        ;;
    *)
        usage
        exit 1
        ;;
esac

log "=== Done ==="