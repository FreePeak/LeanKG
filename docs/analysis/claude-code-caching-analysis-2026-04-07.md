# Claude Code Caching-on-Demand Analysis for LeanKG

**Date:** 2026-04-07
**Source:** https://sathwick.xyz/blog/claude-code.html (Reverse-Engineering Claude Code)
**Purpose:** Identify patterns for providing **correct AND concise** context (not just token reduction)

---

## Executive Summary: Correct + Concise Context

The goal is NOT just "less context" - it is **the right context, once**:

| Problem | Claude Code Solution | LeanKG Gap |
|---------|---------------------|------------|
| **Redundant context** | Deduplication + single-element-per-query | Same element appears via multiple paths |
| **Irrelevant context** | Query-specific prioritization | All elements treated equally |
| **Duplicate results** | HashSet visited tracking | `affected_with_confidence` can have duplicates |
| **No clustering** | Cluster-based grouping for relevance | Has cluster data but doesn't use it |

---

## 1. Claude Code Context Management Architecture

Claude Code uses a **multi-tiered compaction system** that activates based on token budget thresholds:

### 1.1 The Token Budget Hierarchy

```
context_window - 13,000 tokens → Auto-Compaction triggers
context_window - 50,000 tokens → Microcompaction activates
API returns 413 → Context Collapse (lazy staged)
```

### 1.2 Four-Tier Compaction System

| Tier | Name | Trigger | Mechanism |
|------|------|---------|-----------|
| 1 | **Auto-Compaction** | Token threshold | Full summarization via compaction model |
| 2 | **Microcompaction** | Size/Time TTL | Tool result truncation, cache-aware preservation |
| 3 | **Snip Compaction** | Feature gate | History truncation with protected tail |
| 4 | **Context Collapse** | 413 error | Lazy commit of staged collapses |

---

## 2. Core Problem: LeanKG Redundancy Issues

### 2.1 Current LeanKG Problems (Evidence from Code)

**Problem 1: Duplicate in Impact Results**
```rust
// traversal.rs:84-87 - returns both deduplicated AND non-deduplicated
let affected_elements: Vec<CodeElement> = affected_with_confidence
    .iter()
    .map(|a| a.element.clone())  // This dedupes
    .collect();                    // But affected_with_confidence may have dups

// affected_with_confidence (line 111) is NOT deduplicated
// An element reachable via multiple paths appears multiple times
```

**Problem 2: No Deduplication in ContextProvider**
```rust
// context.rs:99-131 - collects from TWO sources without dedup
let file_elements = self.graph.get_elements_by_file(file_path)?;
// ... adds to context_elements
let relationships = self.graph.get_relationships(file_path)?;
// ... adds target elements - SAME element can appear twice!
```

**Problem 3: Same Element via Multiple Relationship Paths**
If function A imports module B AND calls function B::foo:
- A appears in file_elements
- A appears via "imports" relationship
- A appears via "calls" relationship
- Result: A is returned 3 times

---

## 3. Claude Code Patterns for Correctness

### 3.1 Auto-Compaction (Most Relevant for LeanKG)

**Mechanism:**
1. When token count exceeds `context_window - 13,000`
2. Strip images/documents from older messages (replace with `[image]` markers)
3. Group messages by API round (assistant + tool results)
4. Call **compaction model** to generate a summary
5. Replace old messages with `CompactBoundaryMessage`
6. Re-inject up to **5 files + skills** post-compaction (50K token budget for files, 25K for skills)

**Key Insight:** The compaction is **selective** - not all context is compressed equally. High-value context (files, skills) is preserved at budget limits.

**LeanKG Applicability:**
```
Current LeanKG: Returns full graph data on every query
Claude Code: Returns minimal summary, re-injects high-value context on-demand

For LeanKG get_context(file):
- Instead of returning ALL related elements
- Return compact summary + top N (5-10) most relevant
- Provide "load more" mechanism for additional context
```

### 3.2 Microcompaction (Tool Result Budgeting)

**Mechanism:**
- **Time-based TTL:** Clear tool results older than a threshold
- **Size-based truncation:** Truncate when accumulated exceeds threshold
- **Tool-specific:** Only compacts FileRead, Bash, Grep, Glob, WebSearch, WebFetch, FileEdit, FileWrite
- **Cache-aware variant:** Preserves prompt cache integrity via `CacheEditsBlock`

**Claude Code Tool Result Limits:**
| Tool | Limit |
|------|-------|
| BashTool | 30,000 chars |
| GrepTool | 20,000 chars |
| FileReadTool | Infinity (exempt - would create circular dependency) |

**LeanKG Applicability:**
```
For get_impact_radius:
- Current: Returns ALL dependents/dependencies within depth
- LeanKG should: Return top N by confidence/severity, truncate remainder

For search_code:
- Current: Returns all matches (unbounded)
- LeanKG should: Return top N (20-50) with relevance scores, indicate truncation
```

### 3.3 Memoized System Context

**Mechanism:**
- Git status, CLAUDE.md contents, current date - computed once per session
- Memoized and reused across all queries
- Token cost paid only once

**Claude Code System Context (memoized per session):**
- Git status (branch, recent commits, file status - truncated at 2000 chars)
- Cache breaker (optional debug injection)
- CLAUDE.md file contents (auto-discovered from project + parent directories)
- Current date (ISO format)

**LeanKG Applicability:**
```
LeanKG already has this with get_context(file) - BUT:

1. The context is NOT memoized per session
2. Every call to get_context re-fetches from CozoDB
3. Every call to get_impact_radius re-executes the full traversal

Memorization patterns for LeanKG:
- Cache: git status, project structure overview, recent changes
- TTL: Invalidate on file system change events
- Budget: Pre-compute "hot" contexts at index time
```

### 3.4 Context Collapse (Lazy Staged Commits)

**Mechanism:**
1. Staged collapses are prepared but NOT committed immediately
2. Only committed when API returns 413 (prompt too long)
3. If insufficient after collapse drain → Reactive compact (full summarization)
4. If still insufficient → Surface error to user

**Key Insight:** The error is **withheld** from the SDK until recovery paths are exhausted. User never sees a 413 if compaction can resolve it.

**LeanKG Applicability:**
```
For LeanKG queries that might exceed token budgets:
1. Track accumulated response size
2. If approaching limit mid-query, truncate and add marker
3. Provide "continuation" mechanism for remaining results
4. Never return 413-equivalent errors - handle gracefully
```

### 3.5 Deferred Tool Discovery

**Mechanism:**
- ~18 tools marked `shouldDefer: true` are hidden from base prompt
- Model explicitly searches via `ToolSearchTool` to discover
- Keeps base prompt under 200K tokens

**LeanKG Applicability:**
```
LeanKG has 20+ MCP tools - not all needed for every query:

Deferral strategy:
- get_clusters, get_cluster_context → Defer until explicitly needed
- get_traceability, search_by_requirement → Defer until business logic queries
- generate_doc → Defer until documentation requested

Result: Base prompt stays small, tools discovered on-demand
```

---

## 3. Claude Code Query Loop State Machine

The query loop manages context with a sophisticated state machine:

```
queryLoop():
  while(true):
    1. Prefetch memory + skills (parallel)
    2. Apply message compaction (snip, microcompact, context collapse)
    3. Call API with streaming
    4. Handle streaming errors (fallback, retry)
    5. Execute tools (concurrent or serial)
    6. Check recovery paths (compact, collapse drain, token escalation)
    7. Continue loop or return
```

**Key Pattern:** Tools are partitioned by concurrency safety:
- Read-only tools (glob, grep, file reads) → run concurrently (max=10)
- Write tools (edits) → run serially with context propagation

**LeanKG Applicability:**
```
For multi-file operations:
- Group queries by read vs write
- Execute reads in parallel
- Serialize writes with dependency tracking
```

---

## 4. Deduplication: The Core Correctness Problem

Claude Code ensures **each element appears exactly once**. LeanKG has three deduplication failures:

### 4.1 Fix 1: Deduplicate Impact Results

**Current (traversal.rs:84-94):**
```rust
// affected_elements is deduplicated but affected_with_confidence is NOT
affected_elements: Vec<CodeElement> = affected_with_confidence
    .iter()
    .map(|a| a.element.clone())
    .collect();
```

**Fix: Use HashSet throughout traversal:**
```rust
pub fn calculate_impact_radius_with_confidence(
    &self,
    start_file: &str,
    depth: u32,
    min_confidence: f64,
) -> Result<ImpactResult, Box<dyn std::error::Error>> {
    let mut visited: HashSet<String> = HashSet::new();  // Deduplication by qualified_name
    let mut affected_with_confidence: Vec<AffectedElementWithConfidence> = Vec::new();

    // ... traversal logic ...

    // When adding to result:
    if !visited.contains(&rel.target_qualified) {
        visited.insert(rel.target_qualified.clone());
        // Only add FIRST occurrence (highest confidence path)
        affected_with_confidence.push(AffectedElementWithConfidence { ... });
    }
    // If already visited, SKIP - don't add duplicate
}
```

### 4.2 Fix 2: Deduplicate ContextProvider

**Current (context.rs:99-131):**
```rust
// Collects from file_elements AND relationships - can add same element twice
let file_elements = self.graph.get_elements_by_file(file_path)?;
for elem in file_elements { context_elements.push(...) }
let relationships = self.graph.get_relationships(file_path)?;
for rel in relationships {
    if let Some(element) = self.graph.find_element(&rel.target_qualified)? {
        // Same element from file_elements could be added again!
        context_elements.push(ContextElement { element, ... });
    }
}
```

**Fix: Use HashSet for deduplication:**
```rust
pub fn get_context_for_file(&self, file_path: &str) -> Result<ContextResult, ...> {
    let mut seen: HashSet<String> = HashSet::new();  // Deduplication
    let mut context_elements = Vec::new();

    // Phase 1: Collect with deduplication
    let file_elements = self.graph.get_elements_by_file(file_path)?;
    for elem in file_elements {
        if seen.insert(elem.qualified_name.clone()) {  // Returns false if already exists
            context_elements.push(build_context_element(elem, ContextPriority::Contained));
        }
    }

    let relationships = self.graph.get_relationships(file_path)?;
    for rel in relationships {
        if let Some(element) = self.graph.find_element(&rel.target_qualified)? {
            if seen.insert(element.qualified_name.clone()) {  // Skip if already added
                let priority = match rel.rel_type.as_str() {
                    "imports" => ContextPriority::Imported,
                    _ => ContextPriority::Contained,
                };
                context_elements.push(build_context_element(element, priority));
            }
        }
    }

    // Phase 2: Sort and truncate
    context_elements.sort_by(...);
    // ... token budgeting ...
}
```

### 4.3 Fix 3: Confidence-Based Selection (Not Just Deduplication)

When the same element is reachable via multiple paths, **choose the highest-confidence path**:
```rust
// Instead of just skipping duplicates, track best confidence
struct BestPath {
    element: CodeElement,
    confidence: f64,
    path_types: Vec<String>,  // How it was reached
}

let mut best_paths: HashMap<String, BestPath> = HashMap::new();

for rel in relationships {
    let target = &rel.target_qualified;
    match best_paths.get_mut(target) {
        Some(existing) if rel.confidence > existing.confidence => {
            // Replace with higher-confidence path
            *existing = BestPath { confidence: rel.confidence, ... };
        }
        None => {
            best_paths.insert(target.clone(), BestPath { confidence: rel.confidence, ... });
        }
        _ => {} // Keep existing, lower confidence
    }
}
```

---

## 5. LeanKG-Specific Recommendations

### 5.1 Correctness-First (Priority: CRITICAL)

| Fix | Location | Impact |
|-----|----------|--------|
| **Deduplicate ImpactResult** | `traversal.rs` | Remove duplicate elements in blast radius |
| **Deduplicate ContextProvider** | `context.rs` | Remove duplicate elements in context |
| **Track best-confidence path** | `traversal.rs` | Return highest-confidence relationship only |

### 5.2 Conciseness (Priority: HIGH)

| Fix | Location | Impact |
|-----|----------|--------|
| **Add `max_results`** | `handler.rs`, `traversal.rs` | Bound response size |
| **Add `signature_only`** | `handler.rs:460-501` | Return only signatures, not full bodies |
| **Add continuation token** | All list operations | Enable pagination |

### 5.3 Implementation Roadmap

**Phase 1: CRITICAL - Correctness Fixes (Low Effort)**
1. Fix deduplication in `traversal.rs` - use HashSet throughout
2. Fix deduplication in `context.rs` - use HashSet when merging sources
3. Verify: no duplicate `qualified_name` in any response

**Phase 2: HIGH - Conciseness (Moderate Effort)**
1. Add `max_results: Option<usize>` to `get_impact_radius`
2. Add `max_results: Option<usize>` to `search_code`  
3. Enable `signature_only` in `get_context` (already implemented, just not default)
4. Add `continuation` field to paginated responses

**Phase 3: MEDIUM - Optimization (Higher Effort)**
1. Session-level memoization for `get_context`
2. Pre-compute "hot" contexts at index time
3. Deferred loading for `get_clusters`, `get_traceability`

### 5.4 Configuration Schema

```yaml
# leankg.yaml - proposed token optimization config
token_optimization:
  enabled: true
  max_context_tokens: 4000        # Budget per query
  max_results_per_query: 20        # Cap for list operations
  signature_only_default: false    # Default to full context
  memoize_ttl_seconds: 300        # Cache invalidation
  deduplicate: true               # Ensure unique elements
  deferred_tools:
    - get_clusters
    - get_cluster_context
    - get_traceability
    - search_by_requirement
```

### 5.5 Code Changes Required

**src/graph/traversal.rs - Deduplication fix:**
```rust
pub fn calculate_impact_radius_with_confidence(
    &self,
    start_file: &str,
    depth: u32,
    min_confidence: f64,
    max_results: Option<usize>,  // NEW
) -> Result<ImpactResult, Box<dyn std::error::Error>> {
    let mut visited: HashSet<String> = HashSet::new();
    let mut affected_with_confidence: Vec<AffectedElementWithConfidence> = Vec::new();

    queue.push_back((start_file.to_string(), 0));
    visited.insert(start_file.to_string());

    while let Some((current, current_depth)) = queue.pop_front() {
        if current_depth >= depth { continue; }

        // Process relationships - visit EACH target only ONCE
        for rel in relationships {
            if rel.confidence < min_confidence { continue; }
            // KEY FIX: Only process first occurrence
            if visited.insert(rel.target_qualified.clone()) {
                // New element - add with confidence and path
                // ...
            }
            // If already visited, SKIP - don't add duplicate
        }

        // Same for dependents
        for rel in dependents {
            if rel.confidence < min_confidence { continue; }
            if visited.insert(rel.source_qualified.clone()) {
                // ...
            }
        }
    }

    // Sort by confidence, truncate if needed
    affected_with_confidence.sort_by(|a, b| b.confidence.partial_cmp(&a.confidence).unwrap());
    if let Some(max) = max_results {
        affected_with_confidence.truncate(max);
    }

    Ok(ImpactResult {
        has_continuation: affected_with_confidence.len() == max_results,
        ..result
    })
}
```

**src/graph/context.rs - Deduplication fix:**
```rust
pub fn get_context_for_file(&self, file_path: &str) -> Result<ContextResult, ...> {
    let mut seen: HashSet<String> = HashSet::new();  // KEY: Deduplication
    
    let file_elements = self.graph.get_elements_by_file(file_path)?;
    for elem in file_elements {
        if seen.insert(elem.qualified_name.clone()) {  // false = already exists
            context_elements.push(build(elem, Contained));
        }
        // Skip duplicate - already in context from file_elements
    }

    let relationships = self.graph.get_relationships(file_path)?;
    for rel in relationships {
        if let Some(element) = self.graph.find_element(&rel.target_qualified)? {
            if seen.insert(element.qualified_name.clone()) {  // Skip dups
                // ...
            }
        }
    }
    
    // Now safe to sort and truncate - no duplicates exist
}
```

---

## 6. Comparison: Claude Code vs LeanKG Context

| Aspect | Claude Code | LeanKG | Opportunity |
|--------|-------------|--------|-------------|
| **Deduplication** | HashSet visited tracking | Broken - duplicates in results | Fix immediately |
| **Confidence selection** | Highest-confidence path only | Returns ALL paths | Choose best path |
| **Context Budget** | 200K base, 13K buffer | No limit | Add bounds |
| **Compaction** | Multi-tier automatic | None | Add threshold-based |
| **Memoization** | Per-session | Per-call | Add session cache |
| **Tool Loading** | Deferred discovery | All at once | Add deferral |
| **Error Handling** | Withheld until recovery | Fail fast | Add recovery |

---

## 7. Conclusion

Claude Code's approach is NOT just "less context" - it is **correct context, once**:

### Correctness (The Primary Goal)
1. **HashSet deduplication** - Every element appears exactly once
2. **Best-path confidence** - When reachable via multiple paths, keep highest-confidence only
3. **No redundant fetches** - Don't re-fetch what you already have

### Conciseness (Secondary)
1. **Bounded responses** - Cap at max_results, signal continuation
2. **Signature-only mode** - Return headers, not full bodies
3. **Priority sorting** - Most relevant first, truncate lowest priority

### The LeanKG Correctness Bugs (Evidence from Code)
| File | Line | Bug |
|------|------|-----|
| `traversal.rs` | 84-94 | `affected_with_confidence` contains duplicates |
| `context.rs` | 99-131 | Same element from `file_elements` AND `relationships` |
| `traversal.rs` | 52-58 | No visited check before adding to queue |

**Immediate next steps (fix correctness FIRST):**
1. Add `HashSet<String>` visited tracking in `traversal.rs`
2. Add `HashSet<String>` seen tracking in `context.rs`
3. Verify no duplicate `qualified_name` in any response
4. Then add `max_results` bounds for conciseness

---

## References

- Claude Code Reverse Engineering: https://sathwick.xyz/blog/claude-code.html
- Section 10: Context Management - Fighting the Token Limit
- Section 4: The Query Engine - State machine with compaction
- LeanKG Architectural Review: `docs/analysis/leankg-architectural-review-2026-03-27.md`
- LeanKG GitNexus Analysis: `docs/analysis/gitnexus-analysis-2026-03-27.md`
