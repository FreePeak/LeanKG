# LeanKG - Agent Collaboration Guide

## For AI Agents Working on This Project

Welcome! When you start a session in this repository, please:

1. **Read `CLAUDE.md`** for project context and workflow reference
2. **Follow `docs/workflow-opencode-agent.md`** for implementing features
3. **Always update docs before writing code** - PRD → HLD → README
4. **Commit one feature at a time** with clear messages

---

## Feature Implementation Checklist

Before committing any code, ensure:

- [ ] Documentation updated (PRD, HLD, README)
- [ ] Code compiles (`cargo build`)
- [ ] Tests pass (`cargo test`)
- [ ] New code follows existing patterns
- [ ] Commit message uses `feat:` / `fix:` / `docs:` prefix
- [ ] Pushed successfully before starting next task

---

## Important Context

### Data Model
- **CodeElement** uses `qualified_name` format: `src/file.rs::FunctionName`
- **Relationship** types: `imports`, `calls`, `tested_by`, `references`, `documented_by`
- **BusinessLogic** links code to business requirements

### Common Patterns

**Adding a new MCP tool:**
1. Define in `src/mcp/tools.rs` with ToolDefinition
2. Add handler method in `src/mcp/handler.rs`
3. Add match arm in `execute_tool()` method

**Adding a new relationship type:**
```rust
relationships.push(Relationship {
    id: None,
    source_qualified: format!("{}::{}", self.file_path, parent_name),
    target_qualified: target,
    rel_type: "your_new_type".to_string(),
    metadata: serde_json::json!({}),
});
```

**Adding to GraphEngine (private field access):**
```rust
// GraphEngine.db is private, use getter:
let db = graph.db();  // After adding pub fn db() method
```

---

## Communication

- **Commit messages** should be clear about WHY the change was made
- **One commit per logical feature** - don't mix unrelated changes
- **Push after each feature** - don't accumulate unpushed commits

---

## File Change Summary (2026-03-25)

Recent implementations:
- **doc_indexer module** - Index docs/ directory structure
- **Business logic + doc links** - Traceability models
- **Impact radius fix** - Qualified names in calls relationships
- **9 new MCP tools** - Doc and traceability queries

---

*For detailed workflow, see `docs/workflow-opencode-agent.md`*
