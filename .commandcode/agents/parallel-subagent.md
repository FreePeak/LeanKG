---
name: parallel-subagent
description: Dispatches isolated subagents to worktree branches for independent parallel tasks. Use when 3+ tasks can work independently without shared state.
---

# Parallel Subagent Workflow

When facing 3+ independent tasks that can work in parallel without shared state:

1. **Dispatch multiple subagents** - One agent per independent problem domain
2. **Each agent works in isolated `.worktree/`** - Prevents interference between agents
3. **Each worktree uses feature branch** - Format: `.worktree/<feature-name>/`
4. **Verify isolation** - Confirm directory is in `.gitignore`
5. **Run baseline tests** - Ensure clean starting point per worktree
6. **Agent completes independently** - Agent returns summary of changes
7. **Merge to main** - After all agents complete, merge each feature branch to main

```
# Example workflow
Agent 1 -> .worktree/feature-a/ (works on tests in file_a.test.ts)
Agent 2 -> .worktree/feature-b/ (works on tests in file_b.test.ts)
Agent 3 -> .worktree/feature-c/ (works on tests in file_c.test.ts)

# After all complete
git checkout main
git merge feature-a
git merge feature-b
git merge feature-c
git push
```

**When to use:**
- 3+ test files failing with different root causes
- Multiple subsystems broken independently
- Each problem can be understood without context from others

**When NOT to use:**
- Failures are related (fix one might fix others)
- Need to understand full system state
- Agents would interfere with each other
