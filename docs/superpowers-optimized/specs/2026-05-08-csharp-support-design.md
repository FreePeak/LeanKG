# C# Support Design

## Summary

Add LeanKG indexing support for C# source files (`.cs`) using `tree-sitter-c-sharp`.
The first milestone is a `.cs`-only MVP that supports production and test source files through the same parser, with test files classified heuristically rather than parsed by a separate test-specific pipeline.

## Scope

In scope:
- Discover `.cs` files during indexing
- Parse `.cs` files in both bulk and single-file/incremental indexing paths
- Extract C# entities from syntax trees
- Extract `using` import relationships
- Extract basic inheritance and interface relationships
- Extract basic same-file call graph information
- Classify test files via filename heuristics
- Reuse `examples/csharp/Calculator.cs` and `examples/csharp/CalculatorTests.cs` as canonical fixtures for library tests

Out of scope:
- `.csproj` parsing
- `.sln` parsing
- Namespace-qualified symbol identities
- Cross-file partial class merging
- Framework-aware test semantics for xUnit, NUnit, or MSTest
- Roslyn-level semantic resolution or overload resolution

## Goals

- Add reliable C# support without increasing indexing-path drift
- Keep CI unchanged and make `cargo test --lib` exercise the C# fixtures
- Preserve LeanKG's current file-based qualified-name model for v1
- Keep the implementation minimal and aligned with existing indexer patterns

## Non-Goals

- Do not redesign qualified names across the codebase
- Do not add a dedicated test parser for C#
- Do not broaden GitHub Actions coverage as part of this change

## Current Architecture Constraints

LeanKG currently indexes source through two separate paths:

1. Bulk parallel indexing in `src/indexer/mod.rs`
2. Single-file/incremental indexing through `ParserManager` in `src/indexer/parser.rs` and `index_file_sync` in `src/indexer/mod.rs`

The code already shows language-registration drift between file discovery, bulk language mapping, and `ParserManager`. Adding C# directly into each location without consolidation would make this worse and create asymmetric behavior where one indexing mode supports C# and another does not.

## Proposed Design

### 1. Shared Language Registration

Introduce a shared source of truth for language registration inside `src/indexer/`.

Responsibilities:
- map file extensions to internal language keys
- provide the list of source-file extensions accepted by `find_files_sync`
- drive bulk parsing language selection
- drive single-file parser selection aliases

Minimum inventory after the change:

```rust
[
    ("go", &["go"]),
    ("typescript", &["ts", "js"]),
    ("python", &["py"]),
    ("rust", &["rs"]),
    ("java", &["java"]),
    ("kotlin", &["kt", "kts"]),
    ("csharp", &["cs"]),
]
```

This is the most important structural change in the MVP because it eliminates the current drift and prevents C# support from landing in only one path.

### 2. Parser Support

Add `tree-sitter-c-sharp` to `Cargo.toml` and wire it into:
- thread-local bulk parser initialization in `src/indexer/mod.rs`
- `ParserManager` initialization in `src/indexer/parser.rs`
- parser alias resolution for `csharp` and `cs`

### 3. Entity Extraction

Use the generic `EntityExtractor` rather than creating a C#-specific extractor.
Extend the existing generic node matching to support the current `tree-sitter-c-sharp` grammar.

Required node support:
- `using_directive`
- `class_declaration`
- `struct_declaration`
- `interface_declaration`
- `record_declaration`
- `enum_declaration`
- `method_declaration`
- `constructor_declaration`
- `property_declaration`
- `field_declaration`
- `base_list`
- `namespace_declaration`
- `file_scoped_namespace_declaration`

Namespace support in v1 is structural only:
- the extractor should recurse through namespaces correctly
- qualified names remain file-based, not namespace-qualified

### 4. Import Extraction

Add C# `using` extraction to the existing generic import path.

Expected behavior:
- `using System;` creates an `imports` relationship from the file to `System`
- `using ExampleLib;` creates an `imports` relationship from the file to `ExampleLib`

Alias and static-import forms may be parsed but do not need specialized metadata in v1.

### 5. Test Classification

Do not create a test-only parser or skip test files.
Instead:
- parse all `.cs` files with the same parser
- classify test files by filename
- continue using file-level `tested_by` heuristics

Heuristics for v1:
- `*Test.cs`
- `*Tests.cs`

Mapping examples:
- `CalculatorTests.cs -> Calculator.cs`
- `FooTest.cs -> Foo.cs`

This preserves test-to-code linkage without contaminating the parser design with framework-specific rules.

### 6. Call Graph Support

Extend `src/indexer/call_graph.rs` to handle C# invocation nodes. Keep the generic extractor unchanged unless a future requirement needs it.

Required node support:
- `invocation_expression`
- `member_access_expression`

Resolution behavior for v1:
- same-file method definitions should be collected and resolved similarly to the existing Kotlin/Java model
- unresolved external calls may remain `__unresolved__...`
- standard-library calls such as `Console.WriteLine` remain visible unless the user explicitly chooses to suppress them at a query or presentation layer

### 7. Testing Strategy

GitHub Actions currently runs only:

```bash
cargo test --lib
```

Therefore, CI-gated C# coverage must live in library tests, not only integration tests.

Canonical fixtures:
- `examples/csharp/Calculator.cs`
- `examples/csharp/CalculatorTests.cs`

Required library-test coverage:
- parser registration for `cs` and `csharp`
- parsing valid C# code
- extraction of `Calculator` and `Add`
- extraction of `using System`
- classification of `CalculatorTests.cs` as a test file
- `tested_by` heuristic mapping from `CalculatorTests.cs` to `Calculator.cs`
- same-file/basic call extraction behavior for C#

Optional integration coverage may be added later, but it is not required for the MVP because CI does not run `tests/` today.

## Interfaces and Contracts

### Language Key Contract

- Internal language key for C# is `csharp`
- Parser aliases accepted by `ParserManager`: `csharp`, `cs`
- File extension accepted by discovery: `.cs`

### Test Classification Contract

- C# tests are parsed as normal C# source files
- A test file is marked by file heuristic only
- Test classification does not change extraction rules
- Live-code views may choose to filter test nodes and edges later, but the parser layer does not special-case them

## Error Handling

- Unsupported extensions continue to return no language match and skip indexing
- Parse failures continue to behave like existing language parse failures
- No new fatal behavior is introduced for malformed C# files

## Migration Notes

This change is additive.
No schema migration is required if C# elements reuse existing element and relationship types.

## Rollout

Roll out in one feature branch with the following order:
1. docs
2. language registration cleanup
3. parser wiring
4. extractor support
5. test classification
6. call graph support
7. verification through `cargo test --lib`

## Failure-Mode Check

### Failure Mode 1: C# works only in one indexing path

Severity: Critical

Why it could fail:
- bulk parsing and single-file parsing currently use different language registration logic

Mitigation:
- shared language registry is a required first implementation step
- verify through lib tests that exercise both parser registration and source discovery assumptions

### Failure Mode 2: C# entities index but calls are missing or noisy

Severity: Critical

Why it could fail:
- the historical attempt did not update `call_graph.rs`
- C# uses `invocation_expression` and `member_access_expression`, which are not part of the current generic happy path

Mitigation:
- add dedicated C# call-graph fixture tests
- keep standard-library calls visible unless a later query/view layer explicitly suppresses them

### Failure Mode 3: Test code pollutes live-code analysis unexpectedly

Severity: Minor in v1

Why it could fail:
- test files are parsed like normal source files by design

Mitigation:
- ensure file classification and `tested_by` heuristics are present from the MVP
- document that query/view-layer filtering is the right place for live-code-only views

## Decision

Implement the `.cs`-only MVP using shared language registration, generic extractor extensions, and fixture-backed library tests from `examples/csharp/`.
