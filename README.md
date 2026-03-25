<p align="center">
  <img src="assets/icon.svg" alt="LeanKG" width="80" height="80">
</p>

# LeanKG

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Rust](https://img.shields.io/badge/rust-1.70%2B-orange?logo=rust&logoColor=white)](https://www.rust-lang.org/)
[![crates.io](https://img.shields.io/badge/crates.io-latest-orange)](https://crates.io/crates/leankg)
[![Discord](https://img.shields.io/badge/Discord-5865F2?logo=discord&logoColor=white)](https://discord.gg/leankg)

**Lightweight Knowledge Graph for AI-Assisted Development**

LeanKG is a local-first knowledge graph that gives AI coding tools accurate codebase context. It indexes your code, builds dependency graphs, generates documentation, and exposes an MCP server so tools like Cursor, OpenCode, and Claude Code can query the knowledge graph directly. No cloud services, no external databases—everything runs on your machine with minimal resources.

---

## How It Works

```mermaid
sequenceDiagram
    participant Dev as Developer
    participant CLI as LeanKG CLI
    participant Indexer as Code Indexer
    participant DB as CozoDB
    participant MCP as MCP Server
    participant AI as AI Tool (Claude/Cursor)

    Dev->>CLI: leankg init
    CLI->>DB: Initialize graph database
    
    Dev->>CLI: leankg index ./src
    CLI->>Indexer: Parse source files
    Indexer->>Indexer: Extract functions, imports, calls
    Indexer->>DB: Store code elements & relationships
    
    Dev->>CLI: leankg serve
    CLI->>MCP: Start MCP server
    
    AI->>MCP: "What's the impact of changing auth.rs?"
    MCP->>DB: Query impact radius (N hops)
    DB-->>MCP: Affected files list
    MCP-->>AI: Targeted context (13 tokens vs 835)
    
    Dev->>CLI: leankg watch
    CLI->>Index: Watch for file changes
    Index->>DB: Incremental update
```

**The Flow:**
1. **Index** - LeanKG parses your codebase and builds a graph of code elements (functions, classes, modules) and their relationships (imports, calls, tests)
2. **Query** - AI tools query the graph via MCP instead of scanning files
3. **Optimize** - Get targeted context with ~99% token reduction

---

## Why LeanKG?

AI coding tools waste tokens scanning entire codebases. LeanKG provides **targeted context** instead:

| Scenario | Without LeanKG | With LeanKG |
|----------|----------------|-------------|
| **File review** | Full content of changed files + diff | Blast radius + structural summary |
| **Impact analysis** | Manually trace dependencies | `get_impact_radius` returns affected files |
| **Token count** | 9,600+ tokens for full scan | 13-42 tokens with graph |

**LeanKG achieves 98-99% token reduction** (~100x) as measured on real benchmarks.

---

## Installation

### Quick Install via npm (Recommended - No Rust Required)

```bash
# Install via npm (works on any machine with Node.js)
npm install -g leankg

# Verify installation
leankg --version
```

This is the easiest way to get started - no Rust toolchain required. The npm package downloads pre-built binaries for your platform.

**Supported platforms:** macOS (x64, ARM64), Linux (x64, ARM64)

### Install via Cargo

```bash
# Requires Rust installed
cargo install leankg

# Verify installation
leankg --version
```

### Build from Source

```bash
git clone https://github.com/your-org/LeanKG.git
cd LeanKG
cargo build --release

# The binary will be at ./target/release/leankg
```

---

## Quick Start

```bash
# 1. Initialize LeanKG in your project
leankg init

# 2. Index your codebase
leankg index ./src

# 3. Start the MCP server (for AI tools)
leankg serve

# 4. Compute impact radius for a file
leankg impact src/main.rs --depth 3

# 5. Check index status
leankg status
```

---

## MCP Server Setup

LeanKG exposes a Model Context Protocol (MCP) server that AI tools can connect to.

### Option 1: Automated Setup (Recommended)

```bash
# Install MCP configuration for your AI tool
leankg install
```

This command detects your AI tool (Claude Code, OpenCode, Cursor, etc.) and installs the appropriate MCP configuration.

### Option 2: Manual Setup

#### Claude Code / Claude Desktop

Add to `~/.config/claude/settings.json`:

```json
{
  "mcpServers": {
    "leankg": {
      "command": "leankg",
      "args": ["mcp-stdio", "--watch"]
    }
  }
}
```

#### Cursor

Add to `~/.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "leankg": {
      "command": "leankg",
      "args": ["mcp-stdio", "--watch"]
    }
  }
}
```

#### OpenCode

Add to `~/.opencode/mcp.json`:

```json
{
  "mcpServers": {
    "leankg": {
      "command": "leankg",
      "args": ["mcp-stdio", "--watch"]
    }
  }
}
```

### Starting the MCP Server

```bash
# Stdio mode with auto-indexing (for local AI tools)
leankg mcp-stdio --watch

# Stdio mode without auto-indexing
leankg mcp-stdio
```

---

## Features

### Core Features

| Feature | Description |
|---------|-------------|
| **Code Indexing** | Parse and index Go, TypeScript, Python, and Rust codebases with tree-sitter |
| **Dependency Graph** | Build call graphs with `IMPORTS`, `CALLS`, and `TESTED_BY` edges |
| **Impact Radius** | Compute blast radius for any file to see downstream impact |
| **Auto Documentation** | Generate markdown docs from code structure automatically |
| **MCP Server** | Expose the graph via MCP protocol for AI tool integration |
| **File Watching** | Watch for changes and incrementally update the index |
| **CLI** | Single binary with init, index, serve, impact, and status commands |

### Business Logic Mapping

| Feature | Description |
|---------|-------------|
| **Annotations** | Annotate code elements with business logic descriptions |
| **Link to Features** | Link code elements to features |
| **Traceability** | Show feature-to-code traceability |
| **Find by Domain** | Find code elements by business domain |

### Documentation Mapping

| Feature | Description |
|---------|-------------|
| **Docs Structure** | Index docs/ directory structure |
| **Doc-to-Code Links** | Map documentation references to code elements |
| **Doc Queries** | Query docs by file, file by doc, doc structure |
| **Traceability** | Link requirements to code via documentation |

---

## Auto-Indexing

LeanKG watches your codebase and automatically keeps the knowledge graph up-to-date.

```bash
# Start file watcher - indexes changes automatically in background
leankg watch

# Incremental indexing - only re-index changed files (git-based)
leankg index --incremental

# Filter by language
leankg index --lang go,ts,py,rs

# Exclude patterns
leankg index --exclude vendor,node_modules,dist
```

**How Auto-Indexing Works:**

```mermaid
graph LR
    subgraph "File Watcher"
        FS[File System Events]
        Git[Git Status]
        Parse[Parser]
        DB[(CozoDB)]
    end
    
    FS -->|change detected| Git
    Git -->|only changed files| Parse
    Parse -->|update relationships| DB
    
    style DB fill:#f9f,stroke:#333
```

1. **Watch Mode**: `leankg watch` monitors your source directory for file changes
2. **Git-Based Delta**: Uses `git diff` to detect only modified files
3. **Incremental Update**: Re-parses only changed files and updates affected relationships
4. **Background Sync**: Runs in background while you code

---

## Roadmap (Next Phases)

### Phase 2 - Pipeline Integration

| Feature | Status | Description |
|---------|--------|-------------|
| **Pipeline Parsing** | Planned | Parse CI/CD config files (GitHub Actions, GitLab CI, Jenkins, Azure) |
| **Pipeline Graph** | Planned | Build pipeline, stage, step nodes |
| **Trigger Links** | Planned | Link source file changes to triggered pipelines |
| **Pipeline Impact** | Planned | Include pipelines in blast radius analysis |
| **Deployment Targets** | Planned | Track which stages deploy to which environments |

**Supported CI/CD Platforms (Coming Soon):**
- GitHub Actions (`.github/workflows/*.yml`)
- GitLab CI (`.gitlab-ci.yml`)
- Jenkins (`Jenkinsfile`)
- Azure Pipelines (`azure-pipelines.yml`)

### Future Features

| Feature | Description |
|---------|-------------|
| **Semantic Search** | AI-powered code search using embeddings |
| **Security Analysis** | Detect vulnerable dependencies and patterns |
| **Cost Estimation** | Cloud resource cost tracking via pipeline data |
| **Multi-Project** | Index and query across multiple repositories |

---

## Architecture

```mermaid
graph TB
    subgraph "AI Tools"
        Claude[Claude Code]
        Open[OpenCode]
        Cursor[Cursor]
        Antigravity[Google Antigravity]
    end

    subgraph "LeanKG"
        CLI[CLI Interface]
        MCP[MCP Server]
        Watcher[File Watcher]
        
        subgraph "Core"
            Indexer[tree-sitter Parser]
            Graph[Graph Engine]
            Cache[Query Cache]
        end
        
        subgraph "Storage"
            CozoDB[(CozoDB)]
        end
        
        Web[Web UI]
    end

    Claude --> MCP
    Open --> MCP
    Cursor --> MCP
    Antigravity --> MCP
    CLI --> Indexer
    CLI --> Graph
    Watcher --> Indexer
    Indexer --> CozoDB
    Graph --> CozoDB
    Graph --> Cache
    Web --> Graph
```

---

## CLI Commands

| Command | Description |
|---------|-------------|
| `leankg init` | Initialize LeanKG in the current directory |
| `leankg index [path]` | Index source files at the given path |
| `leankg index --incremental` | Only index changed files (git-based) |
| `leankg index --lang go,ts,py,rs` | Filter by language |
| `leankg index --exclude vendor,node_modules` | Exclude patterns |
| `leankg serve` | Start the MCP server (WebSocket) |
| `leankg serve --mcp-port 3000` | Custom MCP server port |
| `leankg mcp-stdio` | Start MCP server with stdio transport |
| `leankg impact <file> --depth N` | Compute blast radius for a file |
| `leankg status` | Show index statistics and status |
| `leankg generate` | Generate documentation from the graph |
| `leankg install` | Auto-install MCP config for AI tools |
| `leankg watch` | Start file watcher for auto-indexing |
| `leankg quality --min-lines N` | Find oversized functions by line count |
| `leankg query <text> --kind name` | Query the knowledge graph |
| `leankg annotate <element> -d <desc>` | Add business logic annotation |
| `leankg link <element> <id>` | Link element to feature |
| `leankg search-annotations <query>` | Search business logic annotations |
| `leankg show-annotations <element>` | Show annotations for a specific element |
| `leankg trace --feature <id>` | Show feature-to-code traceability |
| `leankg find-by-domain <domain>` | Find code by business domain |
| `leankg export` | Export graph data as JSON |
| `leankg docs --tree` | Show documentation directory structure |
| `leankg docs --for <file>` | Show docs referencing a code file |
| `leankg docs --link <doc> <element>` | Link documentation to code element |
| `leankg trace <element>` | Show traceability chain for element |
| `leankg trace --requirement <id>` | Trace code for a requirement |

---

## MCP Tools

| Tool | Description |
|------|-------------|
| `query_file` | Find file by name or pattern |
| `get_dependencies` | Get file dependencies (direct imports) |
| `get_dependents` | Get files depending on target |
| `get_impact_radius` | Get all files affected by change within N hops |
| `get_review_context` | Generate focused subgraph + structured review prompt |
| `get_context` | Get AI context for file (minimal, token-optimized) |
| `find_function` | Locate function definition |
| `get_call_graph` | Get function call chain (full depth) |
| `search_code` | Search code elements by name/type |
| `generate_doc` | Generate documentation for file |
| `find_large_functions` | Find oversized functions by line count |
| `get_tested_by` | Get test coverage for a function/file |
| `get_doc_for_file` | Get documentation files referencing a code element |
| `get_files_for_doc` | Get code elements referenced in a documentation file |
| `get_doc_structure` | Get documentation directory structure |
| `get_traceability` | Get full traceability chain for a code element |
| `search_by_requirement` | Find code elements related to a requirement |
| `get_doc_tree` | Get documentation tree structure |
| `get_code_tree` | Get codebase structure |
| `find_related_docs` | Find documentation related to a code change |

---

## Supported AI Tools

| Tool | Integration | Status |
|------|-------------|--------|
| **Claude Code** | MCP | ✅ Supported |
| **OpenCode** | MCP | ✅ Supported |
| **Cursor** | MCP | ✅ Supported |
| **Google Antigravity** | MCP | ✅ Supported |
| **Windsurf** | MCP | ✅ Supported |
| **Codex** | MCP | ✅ Supported |

---

## Token Savings Example (Benchmarked)

Real benchmark results from the [Go API Service example](examples/go-api-service/):

| Scenario | Without LeanKG | With LeanKG | Savings |
|----------|----------------|-------------|---------|
| Impact Analysis | 835 tokens | 13 tokens | **98.4%** |
| Full Feature Testing | 9,601 tokens | 42 tokens | **99.6%** |

```bash
# Run the benchmark yourself
cd examples/go-api-service
python3 benchmark.py
```

**Before LeanKG**: AI must scan entire codebase to understand dependencies (~9,600 tokens)

**After LeanKG**: LeanKG provides targeted subgraph with relationships pre-computed (~42 tokens)

---

## Requirements

### For npm installation (Recommended)
- **Node.js** 18+ (for npm installation)
- **npm** 8+

### For building from source
- **Rust** 1.70+
- **Platforms**: macOS, Linux

---

## Tech Stack

| Component | Technology |
|-----------|------------|
| Language | Rust |
| Database | CozoDB (embedded relational-graph, Datalog queries) |
| Parsing | tree-sitter |
| CLI | Clap |
| Web Server | Axum |
| Installer | Node.js (npm package for binary distribution) |

---

## Project Structure

```
src/
  cli/       - CLI commands (Clap)
  config/    - Project configuration
  db/        - CozoDB persistence layer
  doc/       - Documentation generator
  graph/     - Graph query engine
  indexer/   - Code parser (tree-sitter)
  doc_indexer/ - Documentation indexer
  mcp/       - MCP protocol handler
  watcher/   - File change watcher
  web/       - Web server (Axum)

docs/
  planning/      - Planning documents
  requirement/    - Requirements documents (PRD)
  analysis/       - Analysis documents
  design/         - Design documents (HLD)
  business/       - Business logic documents
```

---

## License

MIT