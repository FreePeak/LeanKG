# LeanKG Development Workflow for OpenCode AI Agent

## Overview

This document defines the workflow pattern for OpenCode AI agent to implement features in LeanKG. Each feature implementation follows a structured process: **Update Docs → Implement → Test → Commit → Create PR → Review & Merge → Release**.

## Core Principle: One Feature Per Branch

Every distinct feature or fix should be:
1. Documented before implementation
2. Implemented in isolation on a dedicated branch
3. Tested
4. Committed with a clear message
5. Pushed and PR created via gh
6. Reviewed and merged via gh
7. Released as a new version after merge

---

## Standard Feature Implementation Workflow

### Step 0: Understand the Task

Before doing anything:
1. Explore the codebase to understand current structure
2. Read existing relevant code and documentation
3. Understand the data models and relationships
4. Identify where changes need to be made

```bash
# Use explore agent for large-scale understanding
task(description="Explore LeanKG codebase", subagent_type="explore", prompt="...")

# Use Read/grep for targeted understanding
read(filePath="src/db/models.rs")
grep(pattern="BusinessLogic", path="src")
```

### Step 1: Update Documentation (PRD → HLD → README)

**Always update documentation BEFORE writing any code.**

#### 1.1 Update PRD (`docs/requirement/prd-leankg.md`)

- Bump version number and update changelog
- Add new User Story (US-XX)
- Add new Functional Requirements (FR-XX)
- Update roadmap if needed
- Add new terms to glossary

```markdown
**Changelog:**
- v1.X - New Feature: Feature name
  - US-XX: User story description
  - FR-XX to FR-XX: New functional requirements
```

#### 1.2 Update HLD (`docs/design/hld-leankg.md`)

- Update version and changelog
- Update C4 Container diagram with new components
- Add new component to component table
- Add new data flow diagrams (sequence diagrams)
- Add new relationship types to data model
- Add new CLI commands and MCP tools to interface specs
- Update glossary with new terms

#### 1.3 Update README

- Add feature to Features table
- Add new CLI commands to CLI Commands table
- Add new MCP tools to MCP Tools table
- Update verification status table
- Update project structure if adding new modules

### Step 2: Implement the Feature

#### 2.1 For New Modules

```bash
# Create module directory
mkdir -p src/new_module/
```

Create `src/new_module/mod.rs` with:
- Data structures (models)
- Public API functions
- Integration with existing modules

#### 2.2 For Existing Modules

Follow existing code patterns:
- Use same error handling style
- Match naming conventions
- Follow existing function signatures

#### 2.3 Key Files to Modify

| File | Purpose |
|------|---------|
| `src/lib.rs` | Add `pub mod new_module;` |
| `Cargo.toml` | Add dependencies |
| `src/db/models.rs` | Add new data structures |
| `src/db/mod.rs` | Add database operations |
| `src/graph/query.rs` | Add graph query methods |
| `src/mcp/tools.rs` | Add MCP tool definitions |
| `src/mcp/handler.rs` | Add tool execution handlers |

### Step 3: Build and Test

```bash
# Build to catch compilation errors
cargo build 2>&1 | head -50

# If errors, fix them and rebuild
# Common issues:
# - Missing imports
# - Private field access (add getter methods to GraphEngine)
# - Type mismatches
# - Method not found errors

# Run tests
cargo test 2>&1 | tail -30

# Fix any failing tests
```

### Step 4: Commit with Clear Message

Follow conventional commit format:

```bash
git add -A
git commit -m "feat|fix|docs|chore: Brief description

Detailed explanation of what was done.
- Added new functionality X
- Fixed issue Y
- Updated Z"
```

**Commit types:**
- `feat:` New feature
- `fix:` Bug fix
- `docs:` Documentation only
- `chore:` Build/tooling changes

### Step 5: Create Branch and Push

```bash
# Create a new branch for this feature
git checkout -b feature/<ticket-id>-short-description

# Push the branch to origin
git push -u origin feature/<ticket-id>-short-description
```

### Step 6: Create Pull Request via gh

```bash
# Create PR to main branch
gh pr create --title "feat: Short description" --body "$(cat <<'EOF'
## Summary
- Brief description of what changed
- Key changes made

## Test Plan
- [ ] cargo build passes
- [ ] cargo test passes
- [ ] Manual verification steps (if applicable)

## Checklist
- [ ] Documentation updated (PRD, HLD, README)
- [ ] Code follows existing patterns
- [ ] No debug/placeholder code left in
EOF
)"
```

### Step 7: Review and Merge via gh

After PR is created:

```bash
# View PR details
gh pr view

# Check PR diff
gh pr diff

# Merge the PR (squash merge)
gh pr merge --squash --delete-branch

# Alternative: Merge with merge commit
# gh pr merge --admin --delete-branch
```

### Step 8: Release New Version

After merge:

```bash
# Pull latest main
git checkout main
git pull origin main

# Bump version in Cargo.toml
# Edit version from X.Y.Z to X.Y.Z+1

# Run cargo build to update Cargo.lock (required to sync Cargo.lock with new version)
cargo build

# Commit version bump (both Cargo.toml and updated Cargo.lock)
git add -A
git commit -m "release: vX.Y.Z"

# Push
git push origin main

# Create and push tag
git tag -a vX.Y.Z -m "Release vX.Y.Z"
git push origin vX.Y.Z
```

---

## LeanKG-Specific Patterns

### Adding a New Data Model

1. Add struct to `src/db/models.rs`:

```rust
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct NewModel {
    pub id: Option<String>,
    pub name: String,
    pub related_qualified: Option<String>,
    pub metadata: serde_json::Value,
}
```

2. Add database operations to `src/db/mod.rs`
3. Add query methods to `src/graph/query.rs`

### Adding a New Relationship Type

1. Store relationship with descriptive metadata:

```rust
relationships.push(Relationship {
    id: None,
    source_qualified: source,
    target_qualified: target,
    rel_type: "new_relationship".to_string(),
    metadata: serde_json::json!({
        "context": "description",
        "line": line_number,
    }),
});
```

### Adding a New MCP Tool

1. Define tool in `src/mcp/tools.rs`:

```rust
ToolDefinition {
    name: "new_tool".to_string(),
    description: "Description".to_string(),
    input_schema: json!({
        "type": "object",
        "properties": {
            "param": {"type": "string"}
        }
    }),
}
```

2. Add handler method in `src/mcp/handler.rs`:

```rust
fn new_tool(&self, args: &Value) -> Result<Value, String> {
    let param = args["param"].as_str().ok_or("Missing 'param'")?;
    // Implementation
    Ok(json!({ "result": result }))
}
```

3. Add match arm in `execute_tool`:

```rust
"new_tool" => self.new_tool(arguments),
```

### Adding CLI Commands

CLI commands are defined in `src/cli/mod.rs` using Clap. Follow existing command patterns.

---

## Handling Git Rebase Conflicts

When `git pull --rebase` shows conflicts:

```bash
# See conflicted files
git diff --name-only --diff-filter=U

# View conflict
git diff README.md | head -50

# Read file to see conflict markers
read(filePath="README.md", offset=100, limit=50)

# Edit to resolve conflict
edit(filePath="README.md", oldString="<<<<<<< HEAD\n=======\n<<<<<<< commit", newString="resolved content")

# Continue rebase
git add README.md
GIT_EDITOR="cat" git rebase --continue
```

---

## Quality Checklist

Before creating PR, verify:

- [ ] Documentation updated (PRD, HLD, README)
- [ ] Code compiles without errors
- [ ] Tests pass
- [ ] New code follows existing patterns
- [ ] No debug/placeholder code left in
- [ ] Commit message is clear
- [ ] Branch name follows convention (feature/<ticket>-description)
- [ ] PR created with clear title and description

Before merging, verify:
- [ ] PR title follows conventional commits (feat:, fix:, etc.)
- [ ] Review completed (self-review or code review)
- [ ] All checks pass

After merging, verify:
- [ ] Version bumped in Cargo.toml
- [ ] Tag created and pushed

---

## Example: Complete Feature Workflow

```bash
# 1. Understand
task(description="Explore db module", prompt="Explore src/db/ to understand data models...")

# 2. Update docs first
edit(filePath="docs/requirement/prd-leankg.md", oldString="...", newString="...")
edit(filePath="docs/design/hld-leankg.md", oldString="...", newString="...")
edit(filePath="README.md", oldString="...", newString="...")

# 3. Implement
write(content="...", filePath="src/new_module/mod.rs")
edit(filePath="src/lib.rs", oldString="...", newString="...")

# 4. Build and test
cargo build
cargo test

# 5. Commit
git add -A
git commit -m "feat: Add new feature

- Added new module for X
- Implemented Y functionality
- Added Z relationship type"

# 6. Create branch and push
git checkout -b feature/US-XX-new-feature
git push -u origin feature/US-XX-new-feature

# 7. Create PR
gh pr create --title "feat: Add new feature" --body "..."

# 8. Review and merge
gh pr merge --squash --delete-branch

# 9. Release
git checkout main
git pull origin main
# Edit Cargo.toml version
git add -A
git commit -m "release: Bump version to X.Y.Z"
git push origin main
git tag -a vX.Y.Z -m "Release vX.Y.Z"
git push origin vX.Y.Z
```

---

## Quick Reference Commands

```bash
# Build
cargo build 2>&1 | tail -20

# Test
cargo test 2>&1 | tail -30

# Full test with output
cargo test 2>&1

# Check git status
git status

# See recent commits
git log --oneline -5

# Stash changes
git stash

# Pop stash
git stash pop

# GitHub CLI (gh) Commands
gh pr create --title "feat: Description" --body "..."
gh pr view
gh pr diff
gh pr merge --squash --delete-branch
gh pr checkout <branch>   # Checkout PR branch locally
gh pr merge --admin       # Merge with merge commit
gh pr merge --rebase      # Merge with rebase
gh release list
gh release create vX.Y.Z --notes "Release notes"
```

---

## Document Revision

**Version:** 1.0  
**Date:** 2026-03-25  
**Based on:** LeanKG Phase 2 implementation session
