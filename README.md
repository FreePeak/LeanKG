<p align="center">
  <img src="assets/icon.svg" alt="LeanKG" width="80" height="80">
</p>

# LeanKG

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Rust](https://img.shields.io/badge/rust-1.70%2B-orange?logo=rust&logoColor=white)](https://www.rust-lang.org/)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-6c757d)](README.md#requirements)

**Lightweight Knowledge Graph for AI-Assisted Development**

LeanKG is a local-first knowledge graph that gives AI coding tools accurate codebase context. It indexes your code, builds dependency graphs, generates documentation, and exposes an MCP server so tools like Cursor, OpenCode, and Claude Code can query the knowledge graph directly. No cloud services, no external databases—everything runs on your machine with minimal resources.

---

## Features

| Feature | Description |
|---------|-------------|
| **Code Indexing** | Parse and index Go, TypeScript, and Python codebases with tree-sitter |
| **Dependency Graph** | Build call graphs with `IMPORTS`, `CALLS`, and `TESTED_BY` edges |
| **Impact Radius** | Compute blast radius for any file to see downstream impact |
| **Auto Documentation** | Generate markdown docs from code structure automatically |
| **MCP Server** | Expose the graph via MCP protocol for AI tool integration |
| **File Watching** | Watch for changes and incrementally update the index |
| **CLI** | Single binary with init, index, serve, impact, and status commands |

---

## Requirements

- **Rust** 1.70+ (for building from source)
- **Platforms**: macOS, Linux

---

## Installation

### From Source

```bash
git clone https://github.com/YOUR_ORG/LeanKG.git
cd LeanKG
cargo build --release
```

The binary will be at `./target/release/leankg`. Add it to your PATH or use `cargo install --path .` for a global install.

### Cargo Install (when published)

```bash
cargo install leankg
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

# 4. Optional: compute impact radius for a file
leankg impact src/main.rs --depth 3

# 5. Optional: generate documentation
leankg generate

# 6. Check index status
leankg status
```

---

## CLI Reference

| Command | Description |
|---------|-------------|
| `leankg init` | Initialize LeanKG in the current directory |
| `leankg index [path]` | Index source files at the given path |
| `leankg serve` | Start the MCP server and web UI |
| `leankg impact <file> [--depth N]` | Compute blast radius for a file |
| `leankg status` | Show index statistics and status |
| `leankg generate` | Generate documentation from the graph |

---

## Architecture

```
src/
  cli/       - CLI commands (Clap)
  config/    - Project configuration
  db/        - SurrealDB persistence layer
  doc/       - Documentation generator
  graph/     - Graph query engine
  indexer/   - Code parser (tree-sitter)
  mcp/       - MCP protocol handler
  watcher/   - File change watcher
  web/       - Web server (Axum)
```

---

## Tech Stack

| Component | Technology |
|-----------|------------|
| Language | Rust |
| Database | SurrealDB (embedded RocksDB backend) |
| Parsing | tree-sitter |
| CLI | Clap |
| Web Server | Axum |

---

## Documentation

| Document | Description |
|----------|-------------|
| [PRD](./docs/requirement/prd-leankg.md) | Product Requirements Document |
| [HLD](./docs/design/hld-leankg.md) | High Level Design |

---

## License

MIT
