# CozoDB 0.2 Parsing Issue Fix

**Date:** 2026-03-25
**Issue:** CozoDB 0.2 query parsing errors ("parser::pest") when running Datalog queries

## Problem Description

After migrating from SurrealDB to CozoDB 0.2, the application encountered two distinct issues:

### 1. Schema Creation Conflict
When `init_schema()` was called, it attempted to create relations using `:create` even if they already existed, causing "stored_relation_conflict" errors.

**Root Cause:** The `:create` command in CozoDB fails if a relation with the same name already exists.

**Solution:** Check if relations exist before creating them using the `::relations` system command.

```rust
// Before (problematic)
let create_code_elements = r#":create code_elements {...}"#;
db.run_script(create_code_elements, Default::default())?;

// After (fixed)
let check_relations = r#"::relations"#;
let relations_result = db.run_script(check_relations, Default::default())?;
let existing_relations: HashSet<String> = relations_result.rows.iter()
    .filter_map(|row| row.get(0).and_then(|v| v.as_str().map(String::from)))
    .collect();

if !existing_relations.contains("code_elements") {
    db.run_script(create_code_elements, Default::default())?;
}
```

### 2. Regex Operator Syntax Error
Queries using the `=~` operator for regex matching failed with "parser::pest" errors.

**Root Cause:** The `=~` operator is not valid in CozoDB 0.2. The correct way to perform regex matching is using the `regex_matches()` function.

**Solution:** Replace `=~` with `regex_matches()` function calls.

```rust
// Before (broken)
let query = r#"?[...] := *code_elements[...], name =~ "{}""#;

// After (fixed)
let query = r#"?[...] := *code_elements[...], regex_matches(lowercase(name), "{}")"#;
```

## Files Modified

1. `src/db/schema.rs` - Fixed schema initialization to check for existing relations
2. `src/db/mod.rs` - Fixed `search_business_logic()` regex syntax
3. `src/graph/query.rs` - Fixed `search_by_name()`, `search_annotations()`, and `search_by_pattern()` regex syntax

## Verification

After the fix:
- Schema initialization succeeds without errors
- Query commands work correctly
- All integration tests pass

## References

- [CozoDB Tutorial](https://docs.cozodb.org/en/latest/tutorial.html)
- [CozoDB Functions Documentation](https://docs.cozodb.org/en/latest/functions.html)