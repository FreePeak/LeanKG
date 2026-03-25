# LeanKG PRD - Product Requirements Document

**Version:** 1.5  
**Date:** 2026-03-25  
**Status:** In Progress - Phase 2 Features Implementation  
**Author:** Product Owner  
**Target Users:** Software developers using AI coding tools (Cursor, OpenCode, Claude Code, etc.)  
**Changelog:** 
- v1.6 - MCP Server Self-Initialization:
  - US-15: MCP server tools for init/index/install mirroring CLI behavior
  - US-16: Auto-initialization when MCP server starts without existing project
- v1.5 - Phase 2 Features:
  - US-10: Documentation-structure mapping (map docs/ directory to codebase)
  - US-11: Enhanced business logic tagging with doc links
  - US-12: Impact analysis improvements (fix qualified name mismatch)
  - US-13: Additional MCP tools for docs and pipeline queries
- v1.4 - Phase 2: Pipeline Information Extraction (US-09, FR-42 to FR-50)
  - US-09: Pipeline information extraction from CI/CD configuration files
  - FR-42 to FR-50: Pipeline parsing, graph integration, impact analysis, and MCP tools
  - Updated roadmap Phase 2 with pipeline extraction milestones
- v1.3 - MVP Implementation: US-01 to US-08 implementation started
  - US-01: Auto-indexing with TESTED_BY and incremental indexing
  - US-02: Auto documentation with AGENTS.md and CLAUDE.md templates
  - US-03: Business logic mapping with traceability queries
  - US-04: MCP server with all required tools
  - US-05: Full CLI interface with query and MCP server commands
  - US-06: Resource optimization with parser pooling and query caching
  - US-07: Web UI (stub implementation - handlers need completion)
  - US-08: Multi-language support for Go, TypeScript, Python
- v1.2.1 - Migrated database from SurrealDB to CozoDB (embedded SQLite-backed relational-graph with Datalog queries)
- v1.2 - Tech stack: Rust + SurrealDB
- v1.1 - Added impact radius analysis, TESTED_BY edges, review context, qualified names, auto-install MCP

---

## 1. Executive Summary

LeanKG is a lightweight, local-first knowledge graph solution designed for developers who use AI-assisted coding tools. The primary purpose is to provide AI models with accurate, concise codebase context without scanning unnecessary code, avoiding context window dilution, and ensuring documentation stays up-to-date with business logic mapping.

Unlike heavy frameworks like Graphiti that require external databases (Neo4j) and cloud infrastructure, LeanKG runs entirely locally on macOS and Linux with minimal resource consumption. It automatically generates and maintains documentation while mapping business logic to the existing codebase.

## 2. Problem Statement

### 2.1 Current Pain Points

| Pain Point | Description |
|------------|-------------|
| **Context Window Dilution** | AI tools scan entire codebases, including irrelevant files, wasting context window tokens |
| **Outdated Documentation** | Manual docs quickly become stale; AI receives wrong context |
| **Business Logic Disconnect** | No clear mapping between business requirements and code implementation |
| **Token Waste** | Redundant code scanning generates unnecessary token costs |
| **Poor Code Generation** | AI lacks accurate context, producing incorrect or suboptimal code |
| **Feature Transfer Difficulty** | Onboarding new developers requires extensive code exploration |

### 2.2 Why Graphiti Is Not Suitable

- Requires Neo4j or similar external database (operational complexity)
- LLM API calls required for every episode ingestion (ongoing costs)
- Memory-intensive entity resolution
- No embedded deployment option
- Cannot run offline (network required)

## 3. Product Overview

### 3.1 Product Name

**LeanKG** - Lightweight Knowledge Graph for AI-Assisted Development

### 3.2 Product Type

Local-first knowledge graph with CLI and MCP server interface

### 3.3 Core Value Proposition

LeanKG enables AI coding tools to understand exactly what they need—nothing more, nothing less. It provides precise codebase context, automatic documentation, and business logic mapping while running entirely locally with minimal resource usage.

### 3.4 Target Users

1. **Primary:** Developers using AI coding assistants (Cursor, OpenCode, Claude Code, Codex, Windsurf)
2. **Secondary:** Development teams wanting self-hosted codebase intelligence
3. **Tertiary:** Individual developers needing better AI code generation

---

## 4. User Stories

| ID | User Story | Priority |
|----|------------|----------|
| US-01 | As a developer, I want LeanKG to index my codebase automatically so that AI tools have accurate context | Must Have |
| US-02 | As a developer, I want LeanKG to generate and update documentation automatically so that I don't have to write docs manually | Must Have |
| US-03 | As a developer, I want LeanKG to map business logic to code so that AI understands the "why" behind implementation | Must Have |
| US-04 | As a developer, I want LeanKG to expose an MCP server so that my AI tools can query the knowledge graph | Must Have |
| US-05 | As a developer, I want LeanKG to run as a CLI so that I can integrate it into my workflow | Must Have |
| US-06 | As a developer, I want LeanKG to use minimal resources so that it doesn't slow down my machine | Must Have |
| US-07 | As a developer, I want LeanKG to provide a lightweight UI so that I can explore the knowledge graph visually | Should Have |
| US-08 | As a developer, I want LeanKG to support multiple languages so that it works with my tech stack | Must Have |
| US-09 | As a developer, I want LeanKG to extract pipeline information from CI/CD configuration files so that AI tools understand how code flows from commit to deployment | Should Have |
| US-10 | As a developer, I want LeanKG to map documentation structure to codebase elements so that AI understands which docs relate to which code | Should Have |
| US-11 | As a developer, I want LeanKG to enhance business logic tagging with doc links so that I can trace requirements to implementation | Should Have |
| US-12 | As a developer, I want LeanKG to fix impact radius calculation so that it correctly handles qualified names and returns accurate blast radius | Must Have |
| US-13 | As a developer, I want LeanKG to provide additional MCP tools for docs and pipeline queries so that AI tools have complete context | Should Have |
| US-14 | As a developer, I want to install LeanKG via npm without requiring Rust on my machine so that I can get started quickly | Must Have |
| US-15 | As a developer using an AI tool, I want the MCP server to expose init/index/install tools so that I can initialize and index the project via AI tool | Should Have |
| US-16 | As a developer, I want the MCP server to auto-initialize when it starts without an existing project so that the AI tool can use LeanKG immediately after installation | Should Have |

---

## 5. Functional Requirements

### 5.1 Core Features

#### 5.1.1 Code Indexing and Dependency Graph

**FR-01:** Parse source code files and extract structural information (files, functions, classes, imports, exports)

**FR-02:** Build a dependency graph showing relationships between code elements

**FR-03:** Support multiple programming languages (initially: Go, TypeScript/JavaScript, Python, Rust)

**FR-04:** Incremental indexing - only re-index changed files via git-based change detection

**FR-05:** Watch for file changes and auto-update the graph

**FR-06:** Extract TESTED_BY relationships - auto-detect when test files import/call production code

**FR-07:** Track dependent files - when a file changes, also re-index files that depend on it

#### 5.1.2 Auto Documentation Generation

**FR-08:** Generate markdown documentation from code structure

**FR-09:** Maintain documentation freshness - update on code changes

**FR-10:** Generate AGENTS.md, CLAUDE.md, and other AI context files

**FR-11:** Support custom documentation templates

**FR-12:** Include business logic descriptions in generated docs

#### 5.1.3 Business Logic to Code Mapping

**FR-13:** Allow annotating code with business logic descriptions

**FR-14:** Map user stories/features to specific code files and functions

**FR-15:** Generate feature-to-code traceability

**FR-16:** Support business logic queries ("which code handles user authentication?")

#### 5.1.4 Context Provisioning

**FR-17:** Provide targeted context to AI tools (not full codebase)

**FR-18:** Calculate and minimize token usage for context queries

**FR-19:** Support context templates (file summary, function summary, etc.)

**FR-20:** Query by relevance, not just file structure

**FR-21:** Generate review context - focused subgraph + structured prompt for code review

**FR-22:** Calculate impact radius (blast radius) - find all files affected by a change within N hops

#### 5.1.5 MCP Server Interface

**FR-23:** Expose knowledge graph via MCP protocol

**FR-24:** Provide tools for querying code relationships

**FR-25:** Support context retrieval for specific AI operations

**FR-26:** Authenticate MCP connections

**FR-27:** Auto-generate MCP config file for Claude Code/Cursor/OpenCode integration

#### 5.1.6 CLI Interface

**FR-28:** Initialize a new LeanKG project

**FR-29:** Index codebase with configurable options

**FR-30:** Query the knowledge graph from command line

**FR-31:** Generate documentation

**FR-32:** Manage business logic annotations

**FR-33:** Start/stop MCP server

**FR-34:** Calculate impact radius for a given file

**FR-35:** Auto-install MCP config for AI tools (`leankg install`)

**FR-36:** Find oversized functions by line count (code quality metric)

#### 5.1.7 Lightweight Web UI

**FR-37:** Visualize code dependency graph

**FR-38:** Browse and search code elements

**FR-39:** View and edit business logic annotations

**FR-40:** Simple documentation viewer

**FR-41:** Export interactive graph as self-contained HTML file

#### 5.1.8 Pipeline Information Extraction (Phase 2)

**FR-42:** Parse CI/CD configuration files and extract pipeline structure (stages, jobs, steps, triggers, artifacts)

Supported formats:
- GitHub Actions (`.github/workflows/*.yml`)
- GitLab CI (`.gitlab-ci.yml`)
- Jenkinsfile (declarative and scripted)
- Makefile
- Dockerfile and docker-compose.yml
- Azure Pipelines (`azure-pipelines.yml`)

**FR-43:** Build `pipeline` node type in the knowledge graph representing individual pipeline definitions (e.g., `build`, `test`, `deploy`)

**FR-44:** Build `pipeline_stage` and `pipeline_step` node types representing stages/jobs and individual steps within a pipeline

**FR-45:** Extract `triggers` relationships -- which source file changes or branch patterns trigger which pipelines

**FR-46:** Extract `builds` relationships -- which pipeline stages build, test, or deploy which source code modules

**FR-47:** Extract `depends_on` relationships between pipeline stages (job ordering, artifact dependencies)

**FR-48:** Extend impact analysis to include pipeline blast radius -- when a source file changes, report which pipeline stages and deployment targets are affected

**FR-49:** Provide MCP tools for pipeline queries:
- `get_pipeline_for_file` -- which pipelines are triggered by changes to a file
- `get_pipeline_stages` -- list all stages/jobs in a pipeline
- `get_deployment_targets` -- which environments/targets a file change can reach

**FR-50:** Include pipeline context in auto-generated documentation (AGENTS.md, CLAUDE.md) -- list available pipelines, their triggers, and deployment targets so AI tools understand the delivery workflow

#### 5.1.9 Documentation-Structure Mapping (Phase 2)

**FR-51:** Index documentation directory structure and parse markdown files

Supported documentation structure:
- `docs/planning/` - Planning documents (feature plans, roadmaps)
- `docs/requirement/` - Requirements documents (PRDs, specifications)
- `docs/analysis/` - Analysis documents (research, investigation)
- `docs/design/` - Design documents (HLDs, technical designs)
- `docs/business/` - Business logic documents
- `docs/api/` - API documentation
- `docs/ops/` - Operations guides (runbooks, deployment)
- Custom directories as configured

**FR-52:** Create `document` node type in the knowledge graph representing documentation files

**FR-53:** Extract `references` relationships -- which code elements are referenced in documentation

**FR-54:** Extract `documents` relationships -- which documentation files describe which code elements

**FR-55:** Build hierarchical `contains` relationships for documentation directory structure

**FR-56:** Provide MCP tools for documentation queries:
- `get_doc_for_file` -- which documentation files reference a code element
- `get_files_for_doc` -- which code elements are referenced in a documentation file
- `get_doc_structure` -- return documentation directory structure

#### 5.1.10 Enhanced Business Logic Tagging (Phase 2)

**FR-57:** Extend business logic annotations to include documentation references

**FR-58:** Support linking business logic annotations to specific documentation files

**FR-59:** Generate traceability reports linking requirements to code to documentation

**FR-60:** Provide MCP tools for traceability:
- `get_traceability` -- get full traceability chain for a code element (requirement -> doc -> code)
- `search_by_requirement` -- find code elements related to a specific requirement

#### 5.1.11 Impact Analysis Improvements (Phase 2)

**FR-61:** Fix qualified name mismatch in impact radius calculation (currently calls relationships store bare function names, not qualified names)

**FR-62:** Normalize function names when building CALLS relationships to use qualified names

**FR-63:** Improve BFS traversal to handle partial name matches

**FR-64:** Add caching for impact radius calculations

#### 5.1.12 Additional MCP Tools (Phase 2)

**FR-65:** Add MCP tool `get_doc_tree` to retrieve documentation structure

**FR-66:** Add MCP tool `get_doc_content` to retrieve specific documentation content

**FR-67:** Add MCP tool `get_code_tree` to retrieve codebase structure

**FR-68:** Add MCP tool `find_related_docs` to find documentation related to a code change

#### 5.1.13 NPM-Based Installation (Phase 2)

**FR-69:** Provide npm package (`leankg`) that downloads pre-built binaries for supported platforms (macOS x64, macOS ARM64, Linux x64, Linux ARM64)

**FR-70:** npm package auto-detects user's platform and downloads the correct binary

**FR-71:** Binary is installed to npm global bin directory for CLI access

**FR-72:** npm postinstall script handles binary extraction and PATH setup

#### 5.1.14 MCP Server Self-Initialization (Phase 2)

**FR-73:** MCP server exposes tools mirroring CLI commands:
- `mcp_init` -- Initialize LeanKG project (creates .leankg/, leankg.yaml)
- `mcp_index` -- Index codebase with options (path, incremental, lang, exclude)
- `mcp_install` -- Create .mcp.json for MCP client configuration
- `mcp_status` -- Show index status
- `mcp_impact` -- Calculate impact radius for a file

**FR-74:** MCP server auto-initialization on startup:
- When MCP server starts, search upward from current directory for project root
- Project root is identified by: `.leankg/` directory, `leankg.yaml` file, or `.git/` directory
- If no project markers found, use current directory as project root
- If not initialized, automatically run init + index in the project root
- This provides "plug and play" experience for AI tools
- Gracefully handles read-only filesystems (logs warning instead of crashing)

### 5.2 Non-Functional Requirements

#### 5.2.1 Performance

| Metric | Target |
|--------|--------|
| Cold start time | < 2 seconds |
| Indexing speed | > 10,000 lines/second |
| Query response time | < 100ms |
| Memory usage (idle) | < 100MB |
| Memory usage (indexing) | < 500MB |
| Disk space (per 100K lines) | < 50MB |

#### 5.2.2 Compatibility

- **Operating Systems:** macOS (Apple Silicon + Intel), Linux (x64, ARM64)
- **Languages Supported (MVP):** Go, TypeScript/JavaScript, Python
- **AI Tools:** Cursor, OpenCode, Claude Code (compatible MCP)

#### 5.2.3 Security

- All data stored locally (no cloud sync for MVP)
- No external API calls except for optional LLM
- MCP authentication via local tokens

---

## 6. Technical Architecture

### 6.1 Technology Stack

**Recommended Stack (Best Performance):**

| Component | Technology | Rationale |
|-----------|------------|-----------|
| Core Language | Rust | Single binary, excellent performance, memory safety |
| Database | CozoDB | Embedded SQLite-backed relational-graph, Datalog queries, no external process |
| Code Parsing | tree-sitter | Efficient, multi-language support, mature Rust bindings |
| MCP Server | Custom Rust | Standard MCP protocol, optimal performance |
| CLI | Clap | Standard Rust CLI patterns |
| Web UI | Axum | Rust web framework |
| Embeddings | Optional (local Ollama or cloud API) | For semantic search (Phase 2) |

**Why CozoDB for Graph:**
- Relational-graph hybrid: Datalog queries combine graph traversal with relational joins
- Embedded SQLite backend: lightweight, fast, no external process
- Datalog query language: expressive recursive queries for graph operations
- Lightweight: `cozo = "0.2"` with no heavy compile-time overhead (migrated from SurrealDB due to 6GB+ compile requirements)
- Supports recursive rules for multi-hop traversal (impact radius, dependency chains)
- Single binary deployment with embedded storage at `.leankg/leankg.db`

### 6.2 Data Model

**Node Identity:** Uses qualified_name (`file_path::parent::name`) as natural key instead of UUID. Example: `src/utils.rs::MyStruct::new`.

```
CodeElement:
  - qualified_name: string (PK) - format: file_path::parent::name
  - type: file | function | class | import | export | pipeline | pipeline_stage | pipeline_step
  - name: string
  - file_path: string
  - line_start: int
  - line_end: int
  - language: string
  - parent_qualified: string (optional)
  - metadata: JSON

Relationship:
  - id: integer (PK, auto-increment)
  - source_qualified: string (FK)
  - target_qualified: string (FK)
  - type: imports | implements | calls | contains | exports | tested_by | triggers | builds | depends_on
  - metadata: JSON

BusinessLogic:
  - id: integer (PK, auto-increment)
  - element_qualified: string (FK)
  - description: string
  - user_story_id: string (optional)
  - feature_id: string (optional)

Document:
  - id: integer (PK, auto-increment)
  - title: string
  - content: string
  - file_path: string
  - generated_from: string[] (qualified_names)
  - last_updated: timestamp
```

---

## 7. Out of Scope (MVP)

The following features are explicitly out of scope for MVP:

1. **Vector embeddings / semantic search** - Rule-based only
2. **Cloud sync** - Fully local
3. **Multi-user / team features** - Single user only
4. **Advanced authentication** - Local token only
5. **Plugin system** - Future consideration
6. **Enterprise integrations** - Future consideration
7. **All programming languages** - MVP: Go, TS/JS, Python only
8. **AI-powered entity extraction** - Cloud LLM integration optional, rule-based default

---

## 8. Success Metrics

| Metric | Target |
|--------|--------|
| Token reduction vs full scan | > 80% |
| Documentation accuracy | > 95% |
| Indexing time (10K LOC) | < 30 seconds |
| MCP query latency | < 100ms |
| User onboarding time | < 5 minutes |
| Crash-free usage | > 99.9% |

---

## 9. Release Criteria

### 9.1 MVP Release Criteria

- [x] Code indexing works for Go, TypeScript, Python
- [x] Dependency graph builds correctly with TESTED_BY edges
- [x] CLI commands functional (init, index, query, generate, install, impact)
- [x] MCP server exposes query tools including get_impact_radius and get_review_context
- [x] Documentation generation produces valid markdown
- [x] Business logic annotations can be created and queried
- [x] Impact radius analysis works (blast radius within N hops)
- [x] Auto-install MCP config works for Claude Code/OpenCode
- [ ] Web UI shows basic graph visualization (stub implementation)
- [ ] Resource usage within targets
- [ ] Documentation complete

### 9.2 Acceptance Criteria

1. Developer can install LeanKG via single command
2. Developer can initialize project with one command
3. Developer can index codebase with one command
4. AI tools can query LeanKG via MCP protocol
5. Generated documentation is accurate and usable
6. Business logic annotations are persisted and queryable
7. Resource usage stays within non-functional targets

---

## 10. Roadmap

### Phase 1: MVP (v0.1.0)
- Core indexing (Go, TS/JS, Python)
- Basic dependency graph
- CLI interface
- MCP server (basic queries)
- Documentation generation

### Phase 2: Enhanced Features (v0.2.0)
- Pipeline information extraction (US-09, FR-42 to FR-50)
  - CI/CD config parsing (GitHub Actions, GitLab CI, Jenkinsfile, Makefile, Dockerfile)
  - Pipeline nodes and relationships in knowledge graph
  - Pipeline-aware impact analysis (blast radius includes affected pipelines)
  - MCP tools for pipeline queries
  - Pipeline context in auto-generated documentation
- Documentation-structure mapping (US-10, FR-51 to FR-56)
  - Index docs/ directory structure to knowledge graph
  - Map documentation to code elements via references/documents relationships
  - MCP tools for documentation queries
- Enhanced business logic tagging (US-11, FR-57 to FR-60)
  - Link business logic annotations to documentation
  - Traceability reports (requirement -> doc -> code)
  - MCP tools for traceability queries
- Impact analysis improvements (US-12, FR-61 to FR-64)
  - Fix qualified name mismatch in CALLS relationships
  - Improve BFS traversal with partial name matching
  - Add caching for impact radius
- Additional MCP tools (US-13, FR-65 to FR-68)
  - Documentation structure and content tools
  - Codebase structure tools
  - Related docs finder
- Web UI improvements
- More language support
- Incremental indexing optimization

### Phase 3: Advanced (v0.3.0)
- Vector embeddings
- Semantic search
- Cloud sync (optional)
- Team features

---

## 11. Appendix

### 11.1 Glossary

| Term | Definition |
|------|------------|
| Knowledge Graph | Graph structure storing entities and relationships from codebase |
| Code Indexing | Process of parsing code and extracting structural information |
| MCP Server | Model Context Protocol server for AI tool integration |
| Context Window | AI model's input capacity; LeanKG minimizes tokens needed |
| Business Logic Mapping | Linking code to business requirements |
| Qualified Name | Natural node identifier: `file_path::parent::name` format |
| Blast Radius | All files affected by a change within N hops of graph traversal |
| Impact Radius | Same as blast radius - used to understand scope of modifications |
| Pipeline | A CI/CD workflow definition (e.g., GitHub Actions workflow, Jenkinsfile) parsed into the knowledge graph |
| Pipeline Stage | A named phase within a pipeline (e.g., build, test, deploy) |
| Pipeline Step | An individual action within a stage (e.g., run tests, push image) |
| Trigger | Relationship between source code paths/branches and pipeline execution |
| Documentation Mapping | Linking documentation files to code elements they reference |
| Traceability | Chain linking requirements -> documentation -> code elements |
| Blast Radius | All files affected by a change (also called impact radius) |

### 11.2 References

- CozoDB: https://github.com/cozodb/cozo (Embedded relational-graph database with Datalog queries)
- tree-sitter: https://tree-sitter.github.io/tree-sitter/ (Code parsing)
- MCP Protocol: https://modelcontextprotocol.io/ (AI tool integration)
- code-review-graph: https://github.com/tirth8205/code-review-graph (Inspiration for impact analysis)
- Comparison: Graphiti requires Neo4j; FalkorDB needs external process; CozoDB is embedded with SQLite backend and Datalog queries