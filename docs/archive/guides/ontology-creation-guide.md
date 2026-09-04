# LeanKG Ontology Creation Guide

This document provides guidelines for AI agents to create concept ontologies for LeanKG projects.

## Quick Reference

| Task | Command |
|------|---------|
| Sync ontology | `cargo run --release -- ontology sync` |
| Check status | `cargo run --release -- ontology status` |
| Test MCP | `curl -s -X POST "http://localhost:9699/mcp" -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"kg_ontology_status","arguments":{}},"id":1}'` |

**File locations:**
```
ontology/concepts.yaml    ← loaded by sync (copy source here)
ontology/concepts.yaml    ← source of truth
ontology/workflows.yaml   ← procedural ontology
```

## Overview

LeanKG's ontology system captures domain knowledge as YAML concept nodes. When starting with a new codebase, the agent should create a real ontology that reflects actual code structures, not generic examples.

## Ontology File Location

```
ontology/concepts.yaml        # Main concepts file (loaded by sync)
ontology/concepts/real_concepts.yaml  # Source of truth (can be duplicated)
ontology/workflows.yaml        # Procedural ontology (workflows, steps, failure modes)
```

## Concept Types

### Domain Entities (conceptual things the system deals with)

| Type | Description |
|------|-------------|
| `domain_entity` | Real-world concepts (e.g., Code Indexing, MCP Server) |
| `service` | Microservices or bounded contexts |
| `api_endpoint` | API routes/endpoints |
| `data_store` | Databases, caches, storage |
| `environment` | Runtime environments (dev, staging, prod) |
| `known_issue` | Technical debt, bugs, limitations |
| `team_knowledge` | Decisions, rationale, tribal knowledge |

### Procedural Types (workflows and processes)

| Type | Description |
|------|-------------|
| `workflow` | End-to-end processes |
| `workflow_step` | Individual steps in a workflow |
| `decision_point` | Decision nodes with branches |
| `failure_mode` | What can go wrong at each step |

## Creating Real Ontology Concepts

### Step 1: Analyze the Codebase

Identify the main modules and their responsibilities:

```bash
# List source directories
ls -la src/

# Identify key modules
grep -r "^pub mod" src/ --include="*.rs" | head -20

# Find domain boundaries
grep -r "^pub struct\|^pub enum" src/ --include="*.rs" | head -30
```

### Step 2: Create Concept Definitions

Each concept in `ontology/concepts.yaml` follows this structure:

```yaml
concepts:
  - id: concept_id          # Unique kebab-case identifier
    type_: domain_entity    # Must use type_ (underscore) for YAML
    name: Human Readable Name
    env: local              # Environment: local, dev, staging, prod
    aliases:                # Alternative names for search
      - alias1
      - alias2
    description: >          # What this concept represents
      A paragraph describing this concept
    owned_by:               # Team or component responsible
      - team_name
    code_refs:              # Actual source files/functions
      - src/module/file.rs
      - src/module/file.rs::function_name
    docs:                   # Related documentation
      - docs/some-doc.md
```

### Step 3: Map Code References

For each concept, identify real code references:

1. **Main modules**: `src/<module>/mod.rs`
2. **Key functions**: `src/<module>/mod.rs::function_name`
3. **Related files**: `src/<module>/<file>.rs`

Use the format: `file_path::symbol_name` for functions/structs

### Step 4: Add Aliases

Aliases improve semantic search recall. Add variations of the name:

- Full name: "Code Indexing"
- Short forms: "indexing", "code extraction"
- Related terms: "parsing", "element extraction"
- Acronyms: "AST parsing" (if applicable)

## Example: Creating Ontology for a New Project

```bash
# 1. Analyze project structure
cargo run --release -- index ./src --dry-run 2>/dev/null | head -20

# 2. Identify main domain areas from src/ subdirectories
ls src/

# 3. Look for domain-specific patterns
grep -r "pub fn\|^pub struct" src/ --include="*.rs" | \
  sed 's|.*/src/\([^/]*\)/.*|\1|' | sort -u
```

## Syncing Ontology to LeanKG

```bash
# Sync concepts from YAML to database
cargo run --release -- ontology sync

# Verify loaded concepts
cargo run --release -- ontology status

# Check via MCP
curl -s -X POST "http://localhost:9699/mcp" -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"kg_ontology_status","arguments":{}},"id":1}'
```

## Validating Ontology

### Test Concept Matching

```bash
# Test exact name match
curl -s -X POST "http://localhost:9699/mcp" -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"kg_context","arguments":{"query":"<concept_name>"}},"id":1}'

# Test semantic search
curl -s -X POST "http://localhost:9699/mcp" -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"semantic_search","arguments":{"query":"<search_term>"}},"id":1}'
```

### Validation Checklist

- [ ] All concepts have `code_refs` pointing to real files
- [ ] All concepts have descriptive `description` fields
- [ ] Aliases are specific to this codebase (not generic)
- [ ] `kg_ontology_status` shows correct counts
- [ ] `kg_context` returns matched concepts for queries
- [ ] `semantic_search` finds concepts via aliases

## Procedural Ontology (Workflows)

For workflow-based ontologies, create `ontology/workflows.yaml`:

```yaml
workflows:
  - id: checkout-flow
    name: Customer Checkout Flow
    description: End-to-end purchase process
    steps:
      - id: validate_cart
        name: Validate Cart
        description: Check items and pricing
        failure_modes:
          - invalid_items
          - price_mismatch
      - id: process_payment
        name: Process Payment
        description: Handle payment transaction
        failure_modes:
          - payment_declined
          - timeout
```

## Review and Update Checklist

When reviewing or updating the ontology, check:

- [ ] **Real code refs**: `code_refs` point to existing files/functions (run `grep -r "code_refs" ontology/` to audit)
- [ ] **Descriptive aliases**: Each concept has 2+ aliases that are project-specific
- [ ] **No fake concepts**: Concepts should come from actual codebase analysis
- [ ] **YAML syntax**: Use `type_` (underscore) not `type:` for the type field
- [ ] **Synced to DB**: After changes, run `cargo run --release -- ontology sync`

### Audit Script

```bash
#!/bin/bash
# Audit ontology concepts for completeness

echo "=== Concept Counts ==="
cargo run --release -- ontology status 2>/dev/null || echo "Run 'cargo run --release -- ontology sync' first"

echo ""
echo "=== Code Refs Validation ==="
for f in ontology/concepts.yaml ontology/concepts/real_concepts.yaml; do
  if [ -f "$f" ]; then
    echo "Checking $f..."
    grep -c "code_refs" "$f" || echo "  No code_refs found"
  fi
done

echo ""
echo "=== Missing Aliases ==="
grep -A5 "^- id:" ontology/concepts.yaml | grep -B2 -c "^$" || echo "All concepts have aliases"
```

## Anti-Patterns to Avoid

1. **Fake examples**: Don't create "checkout", "refund" concepts that don't exist in the code
2. **Generic aliases**: "test", "data" are too generic - use project-specific terms
3. **Missing code_refs**: Every concept should reference actual source files
4. **Wrong file location**: Use `ontology/concepts.yaml` (not `ontology/concepts/concepts.yaml`)

## Quick Start Template

```yaml
# ontology/concepts.yaml
concepts:
  - id: my_feature
    type_: domain_entity
    name: My Feature
    env: local
    aliases:
      - feature
      - my-feature
    description: >
      Description of what this feature does in this codebase
    owned_by:
      - team
    code_refs:
      - src/my_feature/mod.rs
      - src/my_feature/handler.rs::handle_request
    docs:
      - docs/my-feature.md
```

## Testing the Ontology

```bash
# 1. Build and start MCP server
cargo run --release -- serve &

# 2. Sync ontology
cargo run --release -- ontology sync

# 3. Run validation queries
for query in "my_feature" "feature" "handle_request"; do
  curl -s -X POST "http://localhost:9699/mcp" \
    -H "Content-Type: application/json" \
    -d "{\"jsonrpc\":\"2.0\",\"method\":\"tools/call\",\"params\":{\"name\":\"kg_context\",\"arguments\":{\"query\":\"$query\"}},\"id\":1}"
  echo ""
done
```

---

## Agent Workflow: Creating Ontology for a New Project

When an agent encounters a LeanKG project without ontology, follow this workflow:

### Phase 1: Analyze (Do this FIRST)

```bash
# 1. List src/ structure to find domain boundaries
ls -la src/

# 2. Find all pub mod declarations to understand modules
grep -r "^pub mod" src/ --include="*.rs" | awk -F: '{print $1}' | sort -u

# 3. Find pub struct/enum to identify domain objects
grep -r "^pub struct\|^pub enum" src/ --include="*.rs" | head -40

# 4. Check for existing documentation
ls docs/ 2>/dev/null || echo "No docs/"
```

### Phase 2: Create Concepts

Based on the analysis, create `ontology/concepts.yaml` with concepts that:

1. **Match actual modules**: If you see `src/indexer/`, `src/graph/`, create concepts for them
2. **Reference real files**: Each `code_refs` entry must exist in the codebase
3. **Use project-specific aliases**: Don't use generic words like "data", "item"

### Phase 3: Sync and Validate

```bash
# 1. Sync to database
cargo run --release -- ontology sync

# 2. Verify loaded
cargo run --release -- ontology status

# 3. Test via MCP (server must be running)
cargo run --release -- serve &
sleep 2
curl -s -X POST "http://localhost:9699/mcp" -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"kg_ontology_status","arguments":{}},"id":1}'
```

### Phase 4: Commit

```bash
git add ontology/ && git commit -m "feat: add ontology concepts for <project-name>"
```

---

*For LeanKG version 0.17+*