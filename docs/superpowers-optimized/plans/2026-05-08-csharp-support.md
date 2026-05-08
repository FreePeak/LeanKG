# C# Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-optimized:subagent-driven-development (recommended) or superpowers-optimized:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `.cs` indexing support to LeanKG with CI-gated library-test coverage based on `examples/csharp/`, while keeping GitHub CI unchanged.
**Architecture:** C# support must be wired through both indexing paths: bulk parallel indexing and single-file/incremental indexing. The first implementation step is a shared language registration model so C# does not land in only one path. Tests and production `.cs` files use the same parser, while test files are classified heuristically.
**Tech Stack:** Rust, tree-sitter, `tree-sitter-c-sharp`, LeanKG `EntityExtractor`, LeanKG `CallGraphBuilder`
**Assumptions:** Assumes `.cs`-only MVP and file-based qualified names remain in place — will NOT work for `.csproj`/`.sln` semantics or namespace-qualified symbol identity. Assumes CI remains `cargo test --lib` — integration tests under `tests/` are not required for immediate enforcement.

---

## File Structure

**Modify:**
- `Cargo.toml` — add `tree-sitter-c-sharp`
- `README.md` — document C# support in public feature list
- `docs/requirement/prd-leankg.md` — document approved MVP scope and non-goals
- `docs/design/hld-leankg.md` — document architecture and supported language addition
- `src/indexer/mod.rs` — shared language registration usage, file discovery, bulk parsing, single-file indexing path
- `src/indexer/parser.rs` — parser slot/init for C# and aliases
- `src/indexer/extractor.rs` — entity/import/test classification logic for C#
- `src/indexer/call_graph.rs` — C# call graph support

**Use as canonical fixtures:**
- `examples/csharp/Calculator.cs`
- `examples/csharp/CalculatorTests.cs`

**Optional later:**
- `tests/integration.rs` — non-CI-gated end-to-end directory indexing coverage

## Tasks

### Task 1: Update Product and Architecture Docs

**Files:**
- Modify: `docs/requirement/prd-leankg.md`
- Modify: `docs/design/hld-leankg.md`
- Modify: `README.md`

**Does NOT cover:**
- implementation changes
- CI changes
- `.csproj` / `.sln` support

- [x] **Step 1: Update PRD with approved MVP scope**

```md
### v1.xx - C# Source Indexing MVP
- Add `.cs` source indexing via tree-sitter-c-sharp
- Parse both production and test `.cs` files with the same parser
- Add heuristic test classification for `*Test.cs` and `*Tests.cs`
- Reuse existing element and relationship types
- Non-goals: `.csproj`, `.sln`, namespace-qualified symbol identities, framework-aware test semantics
```

- [x] **Step 2: Update HLD changelog and language-support notes**

```md
- v1.xx - C# Source Indexing MVP:
  - Add tree-sitter-c-sharp parser support
  - Add shared language registration for file discovery and parser wiring
  - Add C# entity, import, inheritance, and basic call extraction
  - Parse tests with the normal C# parser and classify them heuristically
```

- [x] **Step 3: Update README supported-language text**

```md
- **Code Indexing** -- Parse and index Go, TypeScript, Python, Rust, Java, Kotlin, and C# codebases with tree-sitter.
```

- [x] **Step 4: Verify documentation updates**

Run: `rg -n "C#|csharp|\.cs|csproj|sln" "docs" "README.md"`
Expected: Matches appear in PRD, HLD, and README with `.cs` MVP and non-goals stated

### Task 2: Add Shared Language Registration

**Files:**
- Modify: `src/indexer/mod.rs`
- Modify: `src/indexer/parser.rs`

**Does NOT cover:**
- C# AST extraction
- call graph behavior
- docs or CI workflow changes

- [x] **Step 1: Write a failing library test that captures language drift**

```rust
#[test]
fn test_csharp_language_registration_consistency() {
    let mut pm = ParserManager::new();
    let _ = pm.init_parsers();

    // Single-file path alias contract
    assert!(pm.get_parser_for_language("cs").is_some());
    assert!(pm.get_parser_for_language("csharp").is_some());
}
```

- [x] **Step 2: Run the failing test**

Run: `cargo test --lib csharp_language_registration_consistency`
Expected: FAIL because C# is not yet wired into the parser/indexer paths

- [x] **Step 3: Implement a shared language inventory**

```rust
struct LanguageSpec {
    key: &'static str,
    exts: &'static [&'static str],
}

const LANGUAGE_SPECS: &[LanguageSpec] = &[
    LanguageSpec { key: "go", exts: &["go"] },
    LanguageSpec { key: "typescript", exts: &["ts", "js"] },
    LanguageSpec { key: "python", exts: &["py"] },
    LanguageSpec { key: "rust", exts: &["rs"] },
    LanguageSpec { key: "java", exts: &["java"] },
    LanguageSpec { key: "kotlin", exts: &["kt", "kts"] },
    LanguageSpec { key: "csharp", exts: &["cs"] },
];
```

Use this inventory from:
- `find_files_sync`
- bulk `get_language(...)`
- `index_file_sync`
- `ParserManager` alias handling

- [x] **Step 4: Verify parser-related library tests still pass**

Run: `cargo test --lib parser`
Expected: PASS

### Task 3: Add C# Parser Wiring

**Files:**
- Modify: `Cargo.toml`
- Modify: `src/indexer/parser.rs`
- Modify: `src/indexer/mod.rs`

**Does NOT cover:**
- C# import extraction
- test classification
- call graph support

- [x] **Step 1: Write failing parser tests**

```rust
#[test]
fn test_get_parser_for_csharp() {
    if let Some(mut pm) = init_parsers_if_compatible() {
        assert!(pm.get_parser_for_language("cs").is_some());
        assert!(pm.get_parser_for_language("csharp").is_some());
    }
}

#[test]
fn test_parser_parse_csharp_code() {
    if let Some(mut pm) = init_parsers_if_compatible() {
        let source = b"class Program { static void Main() {} }";
        let parser = pm.get_parser_for_language("csharp").unwrap();
        let tree = parser.parse(source, None);
        assert!(tree.is_some());
    }
}
```

- [x] **Step 2: Run the parser tests**

Run: `cargo test --lib csharp`
Expected: FAIL due to missing dependency/parser initialization

- [x] **Step 3: Implement minimal parser support**

```toml
tree-sitter-c-sharp = "0.23"
```

```rust
let csharp_lang: tree_sitter::Language = tree_sitter_c_sharp::LANGUAGE.into();
self.csharp_parser.set_language(&csharp_lang)?;
```

```rust
"csharp" | "cs" => Some(&mut self.csharp_parser)
```

- [x] **Step 4: Run parser verification**

Run: `cargo test --lib parser::tests`
Expected: PASS including new C# tests

### Task 4: Add C# Entity and Import Extraction From Real Fixtures

**Files:**
- Modify: `src/indexer/extractor.rs`
- Test: `examples/csharp/Calculator.cs`

**Does NOT cover:**
- `.csproj`/`.sln`
- namespace-qualified qualified names
- partial classes across files

- [x] **Step 1: Write failing fixture-backed library tests**

```rust
#[test]
fn test_extract_csharp_calculator_fixture() {
    let root = std::path::Path::new(env!("CARGO_MANIFEST_DIR"));
    let source = std::fs::read(root.join("examples/csharp/Calculator.cs")).unwrap();

    let mut parser = Parser::new();
    let lang: tree_sitter::Language = tree_sitter_c_sharp::LANGUAGE.into();
    parser.set_language(&lang).unwrap();
    let tree = parser.parse(&source, None).unwrap();

    let extractor = EntityExtractor::new(&source, "examples/csharp/Calculator.cs", "csharp");
    let (elements, relationships) = extractor.extract(&tree);

    assert!(elements.iter().any(|e| e.element_type == "class" && e.name == "Calculator"));
    assert!(elements.iter().any(|e| e.element_type == "method" && e.name == "Add"));
    assert!(relationships.iter().any(|r| r.rel_type == "imports" && r.target_qualified == "System"));
}
```

- [x] **Step 2: Run the extractor test**

Run: `cargo test --lib csharp_calculator_fixture`
Expected: FAIL because `using_directive` and/or C# node handling is missing

- [x] **Step 3: Implement C# entity and import support**

Add C# support to existing generic matching for:

```rust
"using_directive"
"class_declaration"
"struct_declaration"
"interface_declaration"
"record_declaration"
"enum_declaration"
"method_declaration"
"constructor_declaration"
"property_declaration"
"field_declaration"
```

Add C# `using` extraction behavior:

```rust
if node_type == "using_directive" && self.language == "csharp" {
    // collect identifier / qualified_name text into imports relationship targets
}
```

- [x] **Step 4: Run extractor verification**

Run: `cargo test --lib extractor`
Expected: PASS including the C# fixture test

### Task 5: Add C# Test Classification

**Files:**
- Modify: `src/indexer/extractor.rs`
- Test: `examples/csharp/CalculatorTests.cs`

**Does NOT cover:**
- xUnit/NUnit/MSTest semantics
- assertion parsing
- framework-aware coverage inference

- [x] **Step 1: Write failing fixture-backed classification tests**

```rust
#[test]
fn test_csharp_test_fixture_classification() {
    assert!(is_test_file("examples/csharp/CalculatorTests.cs"));

    let tested = get_tested_file_path("examples/csharp/CalculatorTests.cs").unwrap();
    assert!(tested.ends_with("Calculator.cs"));
}
```

```rust
#[test]
fn test_csharp_test_fixture_emits_tested_by() {
    let root = std::path::Path::new(env!("CARGO_MANIFEST_DIR"));
    let source = std::fs::read(root.join("examples/csharp/CalculatorTests.cs")).unwrap();

    let mut parser = Parser::new();
    let lang: tree_sitter::Language = tree_sitter_c_sharp::LANGUAGE.into();
    parser.set_language(&lang).unwrap();
    let tree = parser.parse(&source, None).unwrap();

    let extractor = EntityExtractor::new(&source, "examples/csharp/CalculatorTests.cs", "csharp");
    let (_, relationships) = extractor.extract(&tree);

    assert!(relationships.iter().any(|r| r.rel_type == "tested_by"));
}
```

- [x] **Step 2: Run the failing tests**

Run: `cargo test --lib csharp_test_fixture`
Expected: FAIL because `.cs` test heuristics are not yet present

- [x] **Step 3: Implement minimal `.cs` test heuristics**

```rust
"cs" => {
    file_name.ends_with("Test.cs") || file_name.ends_with("Tests.cs")
}
```

```rust
"cs" => {
    if file_name.ends_with("Test.cs") {
        Some(file_name.trim_end_matches("Test.cs").to_string() + ".cs")
    } else if file_name.ends_with("Tests.cs") {
        Some(file_name.trim_end_matches("Tests.cs").to_string() + ".cs")
    } else {
        None
    }
}
```

- [x] **Step 4: Run classification verification**

Run: `cargo test --lib extractor`
Expected: PASS including `.cs` test classification and `tested_by` coverage

### Task 6: Add C# Call Extraction and Resolution

**Files:**
- Modify: `src/indexer/call_graph.rs`
- Modify: `src/indexer/extractor.rs`
- Test: `examples/csharp/Calculator.cs`

**Does NOT cover:**
- overload resolution
- external project symbol resolution
- namespace-aware external call binding

- [x] **Step 1: Write failing call-graph fixture tests**

```rust
#[test]
fn test_csharp_call_graph_fixture() {
    let root = std::path::Path::new(env!("CARGO_MANIFEST_DIR"));
    let source = std::fs::read(root.join("examples/csharp/Calculator.cs")).unwrap();

    let mut parser = Parser::new();
    let lang: tree_sitter::Language = tree_sitter_c_sharp::LANGUAGE.into();
    parser.set_language(&lang).unwrap();
    let tree = parser.parse(&source, None).unwrap();

    let calls = extract_calls_with_resolution(
        &tree,
        &source,
        "examples/csharp/Calculator.cs",
        "csharp",
    );

    assert!(!calls.is_empty());
}
```

- [x] **Step 2: Run the failing tests**

Run: `cargo test --lib call_graph`
Expected: FAIL because `invocation_expression` and `member_access_expression` are not yet handled

- [x] **Step 3: Implement minimal C# call support**

Support these nodes in `CallGraphBuilder`:

```rust
"invocation_expression"
"member_access_expression"
```

Keep standard-library calls like `Console.WriteLine` visible in the graph; do not add hard-coded suppression for them in this task.

Treat C# class-like containers consistently during definition collection:

```rust
"class_declaration" | "struct_declaration" | "record_declaration" | "interface_declaration"
```

- [x] **Step 4: Run call-graph verification**

Run: `cargo test --lib call_graph`
Expected: PASS including C# fixture coverage

### Task 7: Full Library-Test Verification Aligned With CI

**Files:**
- Modify: `src/indexer/parser.rs`
- Modify: `src/indexer/extractor.rs`
- Modify: `src/indexer/call_graph.rs`

- [x] **Step 1: Confirm all CI-gated C# coverage lives in library tests**

```bash
rg -n "examples/csharp" src tests
```

Expected: core C# coverage appears under `src/indexer/*.rs` test modules; no requirement on `tests/`

- [x] **Step 2: Run the same command CI runs**

Run: `cargo test --lib`
Expected: PASS and exercises the `examples/csharp/` fixtures

- [x] **Step 3: Run wider verification**

Run: `cargo build && cargo test`
Expected: PASS, or only unrelated pre-existing failures outside this feature

## Self-Review

Spec coverage:
- shared language registration: covered by Task 2
- parser wiring: covered by Task 3
- entity/import extraction: covered by Task 4
- test classification: covered by Task 5
- call extraction: covered by Task 6
- CI-consistent verification using `cargo test --lib`: covered by Task 7

Placeholder scan:
- No `TODO`/`TBD` placeholders remain
- Tasks include exact files, commands, and concrete test/implementation snippets

Type consistency:
- Internal language key is consistently `csharp`
- fixture paths consistently use `examples/csharp/Calculator.cs` and `examples/csharp/CalculatorTests.cs`
