# Token Optimization: Deduplication Fix

**Date:** 2026-04-07
**Type:** Bug Fix + Token Optimization
**Scope:** Fix deduplication bugs in `traversal.rs` and `context.rs`
**Status:** Draft

---

## 1. Problem Statement

LeanKG returns duplicate code elements in query results, causing:
1. **Token waste** - Same element counted multiple times
2. **Incorrect results** - Element reachable via multiple paths appears multiple times

### Evidence from Code

| File | Line | Bug |
|------|------|-----|
| `traversal.rs` | 84-94 | `affected_with_confidence` contains duplicates |
| `context.rs` | 99-131 | Same element from `file_elements` AND `relationships` |
| `traversal.rs` | 52-58 | No visited check before adding to queue |

---

## 2. Solution

Use `HashSet<String>` to track seen `qualified_name` values. Only add first occurrence.

### 2.1 traversal.rs Changes

**Current (buggy):**
```rust
// affected_with_confidence can have duplicates
affected_with_confidence.push(AffectedElementWithConfidence { ... });
```

**Fix:**
```rust
let mut seen: HashSet<String> = HashSet::new();

// When adding to results:
if seen.insert(element.qualified_name.clone()) {
    // First occurrence - add to results
    affected_with_confidence.push(...);
}
// else: duplicate - skip
```

### 2.2 context.rs Changes

**Current (buggy):**
```rust
let file_elements = self.graph.get_elements_by_file(file_path)?;
for elem in file_elements { context_elements.push(...); }

let relationships = self.graph.get_relationships(file_path)?;
// Same element from file_elements could be added again!
```

**Fix:**
```rust
let mut seen: HashSet<String> = HashSet::new();

let file_elements = self.graph.get_elements_by_file(file_path)?;
for elem in file_elements {
    if seen.insert(elem.qualified_name.clone()) {
        context_elements.push(build_context_element(elem, ContextPriority::Contained));
    }
}

let relationships = self.graph.get_relationships(file_path)?;
for rel in relationships {
    if let Some(element) = self.graph.find_element(&rel.target_qualified)? {
        if seen.insert(element.qualified_name.clone()) {
            // Only add if not already added from file_elements
            context_elements.push(build_context_element(element, priority));
        }
    }
}
```

---

## 3. Acceptance Criteria

- [ ] `get_impact_radius` returns each element exactly once
- [ ] `get_context` returns each element exactly once
- [ ] `affected_with_confidence` has no duplicates by `qualified_name`
- [ ] `context_elements` has no duplicates by `qualified_name`
- [ ] Existing tests pass

---

## 4. Files to Change

| File | Change |
|------|--------|
| `src/graph/traversal.rs` | Add HashSet deduplication in `calculate_impact_radius_with_confidence` |
| `src/graph/context.rs` | Add HashSet deduplication in `get_context_for_file` |

---

## 5. Test Plan

**Unit tests to add:**
1. `test_impact_radius_no_duplicates` - verify same element via 2 paths appears once
2. `test_context_no_duplicates` - verify file_elements + relationships merge produces unique set
3. `test_confidence_highest_wins` - when same element reachable via multiple paths, keep highest confidence

---

## 6. Effort

**Estimated:** 2-3 hours (low effort, high impact)
