# Agent Guidelines for LeanKG

## Project Overview

LeanKG is a Rust-based knowledge graph system that indexes codebases using tree-sitter parsers, stores data in CozoDB, and exposes functionality via CLI and MCP protocol.

**Tech Stack**: Rust 1.70+, CozoDB (embedded relational-graph), tree-sitter, Axum, Clap, Tokio

---

## Build Commands

### Standard Build
```bash
cargo build                    # Debug build
cargo build --release          # Release build
```

### Testing
```bash
cargo test                     # Run all tests
cargo test <test_name>         # Run specific test (partial name matches)
cargo test --package <pkg>     # Test specific package
cargo test -- --nocapture      # Show println output during tests
```

### Code Quality
```bash
cargo fmt                      # Format code
cargo fmt -- --check           # Check formatting without changes
cargo clippy                   # Run linter
cargo clippy -- -D warnings    # Treat warnings as errors
cargo check                    # Type check without building
cargo doc                      # Build documentation
```

### Codebase Indexing & Server
```bash
cargo run -- init              # Initialize LeanKG project
cargo run -- index ./src       # Index codebase
cargo run -- serve             # Start MCP server
cargo run -- impact <file> --depth 3   # Calculate impact radius
cargo run -- status            # Show index status
```

---

## Code Structure Overview

This codebase contains 339 elements and 262 relationships.

### Key Modules

```
src/
├── cli/          # Clap CLI commands
├── config/       # Project configuration
├── db/           # CozoDB layer (models, schema)
├── doc/          # Documentation generator
├── graph/        # Graph engine, query, traversal
├── indexer/      # tree-sitter parsers, entity extraction
├── mcp/          # MCP protocol implementation
├── watcher/      # File system watcher
├── web/          # Axum web server
└── main.rs       # CLI entry point
```

### Files


### Functions

- `./src/config/project.rs::default` (./src/config/project.rs:46)
- `./src/config/project.rs::test_config_indexer_excludes` (./src/config/project.rs:98)
- `./src/config/project.rs::test_config_project_settings` (./src/config/project.rs:91)
- `./src/config/project.rs::test_config_web_documentation` (./src/config/project.rs:109)
- `./src/config/project.rs::test_default_config` (./src/config/project.rs:83)
- `./src/db/mod.rs::all_business_logic` (./src/db/mod.rs:205)
- `./src/db/mod.rs::all_feature_traceability` (./src/db/mod.rs:301)
- `./src/db/mod.rs::all_user_story_traceability` (./src/db/mod.rs:333)
- `./src/db/mod.rs::create_business_logic` (./src/db/mod.rs:11)
- `./src/db/mod.rs::delete_business_logic` (./src/db/mod.rs:100)
- `./src/db/mod.rs::find_by_business_domain` (./src/db/mod.rs:365)
- `./src/db/mod.rs::get_business_logic` (./src/db/mod.rs:41)
- `./src/db/mod.rs::get_by_feature` (./src/db/mod.rs:143)
- `./src/db/mod.rs::get_by_user_story` (./src/db/mod.rs:113)
- `./src/db/mod.rs::get_feature_traceability` (./src/db/mod.rs:259)
- `./src/db/mod.rs::get_user_story_traceability` (./src/db/mod.rs:280)
- `./src/db/mod.rs::search_business_logic` (./src/db/mod.rs:173)
- `./src/db/mod.rs::update_business_logic` (./src/db/mod.rs:70)
- `./src/db/models.rs::test_code_element_creation` (./src/db/models.rs:52)
- `./src/db/models.rs::test_relationship_creation` (./src/db/models.rs:68)
- `./src/db/schema.rs::init_db` (./src/db/schema.rs:6)
- `./src/db/schema.rs::init_schema` (./src/db/schema.rs:22)
- `./src/doc/generator.rs::generate_agents_md` (./src/doc/generator.rs:155)
- `./src/doc/generator.rs::generate_claude_md` (./src/doc/generator.rs:268)
- `./src/doc/generator.rs::generate_for_element` (./src/doc/generator.rs:39)
- `./src/doc/generator.rs::generate_for_element_with_annotation` (./src/doc/generator.rs:81)
- `./src/doc/generator.rs::generate_for_element_with_template` (./src/doc/generator.rs:101)
- `./src/doc/generator.rs::get_doc_tracking_info` (./src/doc/generator.rs:412)
- `./src/doc/generator.rs::new` (./src/doc/generator.rs:26)
- `./src/doc/generator.rs::regenerate_for_file` (./src/doc/generator.rs:136)
- `./src/doc/generator.rs::sync_docs_for_file` (./src/doc/generator.rs:384)
- `./src/doc/generator.rs::with_templates_path` (./src/doc/generator.rs:34)
- `./src/doc/templates.rs::get_default_agents_template` (./src/doc/templates.rs:140)
- `./src/doc/templates.rs::get_default_claude_template` (./src/doc/templates.rs:193)
- `./src/doc/templates.rs::list_templates` (./src/doc/templates.rs:115)
- `./src/doc/templates.rs::load_template` (./src/doc/templates.rs:24)
- `./src/doc/templates.rs::new` (./src/doc/templates.rs:20)
- `./src/doc/templates.rs::render_agents_template` (./src/doc/templates.rs:69)
- `./src/doc/templates.rs::render_claude_template` (./src/doc/templates.rs:82)
- `./src/doc/templates.rs::render_custom_template` (./src/doc/templates.rs:131)
- `./src/doc/templates.rs::render_element_template` (./src/doc/templates.rs:46)
- `./src/doc/templates.rs::render_file_summary` (./src/doc/templates.rs:90)
- `./src/doc/templates.rs::render_template` (./src/doc/templates.rs:37)
- `./src/doc/templates.rs::save_template` (./src/doc/templates.rs:108)
- `./src/doc/templates.rs::test_get_default_agents_template` (./src/doc/templates.rs:272)
- `./src/doc/templates.rs::test_get_default_claude_template` (./src/doc/templates.rs:280)
- `./src/doc/templates.rs::test_render_agents_template_empty` (./src/doc/templates.rs:230)
- `./src/doc/templates.rs::test_render_agents_template_with_elements` (./src/doc/templates.rs:237)
- `./src/doc/templates.rs::test_render_claude_template` (./src/doc/templates.rs:245)
- `./src/doc/templates.rs::test_render_file_summary` (./src/doc/templates.rs:252)
- ... and 236 more functions

### Classes/Structs

- `./src/config/project.rs::DocConfig` (./src/config/project.rs:40)
- `./src/config/project.rs::IndexerConfig` (./src/config/project.rs:21)
- `./src/config/project.rs::McpConfig` (./src/config/project.rs:27)
- `./src/config/project.rs::ProjectConfig` (./src/config/project.rs:5)
- `./src/config/project.rs::ProjectSettings` (./src/config/project.rs:14)
- `./src/config/project.rs::WebConfig` (./src/config/project.rs:34)
- `./src/db/mod.rs::FeatureTraceEntry` (./src/db/mod.rs:232)
- `./src/db/mod.rs::FeatureTraceability` (./src/db/mod.rs:239)
- `./src/db/mod.rs::UserStoryTraceEntry` (./src/db/mod.rs:246)
- `./src/db/mod.rs::UserStoryTraceability` (./src/db/mod.rs:253)
- `./src/db/models.rs::BusinessLogic` (./src/db/models.rs:27)
- `./src/db/models.rs::CodeElement` (./src/db/models.rs:4)
- `./src/db/models.rs::Document` (./src/db/models.rs:37)
- `./src/db/models.rs::Relationship` (./src/db/models.rs:17)
- `./src/doc/generator.rs::DocGenerator` (./src/doc/generator.rs:18)
- `./src/doc/generator.rs::DocSyncResult` (./src/doc/generator.rs:445)
- `./src/doc/generator.rs::DocTrackingInfo` (./src/doc/generator.rs:453)
- `./src/doc/templates.rs::TemplateEngine` (./src/doc/templates.rs:15)
- `./src/graph/cache.rs::CacheEntry` (./src/graph/cache.rs:8)
- `./src/graph/cache.rs::QueryCache` (./src/graph/cache.rs:91)
- `./src/graph/cache.rs::TimedCache` (./src/graph/cache.rs:13)
- `./src/graph/context.rs::ContextElement` (./src/graph/context.rs:16)
- `./src/graph/context.rs::ContextProvider` (./src/graph/context.rs:64)
- `./src/graph/context.rs::ContextResult` (./src/graph/context.rs:23)
- `./src/graph/query.rs::GraphEngine` (./src/graph/query.rs:8)
- `./src/graph/traversal.rs::ImpactAnalyzer` (./src/graph/traversal.rs:5)
- `./src/graph/traversal.rs::ImpactResult` (./src/graph/traversal.rs:66)
- `./src/indexer/extractor.rs::EntityExtractor` (./src/indexer/extractor.rs:5)
- `./src/indexer/git.rs::GitAnalyzer` (./src/indexer/git.rs:11)
- `./src/indexer/git.rs::GitChangedFiles` (./src/indexer/git.rs:5)
- ... and 23 more classes

---

## Relationship Types

- `calls`: 211 occurrences
- `imports`: 51 occurrences

---

## Testing Guidelines

1. Unit tests are placed in `#[cfg(test)]` modules within each source file
2. Integration tests are located in the `tests/` directory
3. Use `tempfile::TempDir` for tests requiring filesystem access
4. Use `tokio::test` for async tests
5. Follow Arrange-Act-Assert pattern in all tests

