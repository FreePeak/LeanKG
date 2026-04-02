# LeanKG CLI Reference

Complete reference for all LeanKG CLI commands.

## CLI Commands

| Command | Description |
|---------|-------------|
| `leankg version` | Show LeanKG version |
| `leankg init` | Initialize LeanKG in the current directory |
| `leankg index [path]` | Index source files at the given path |
| `leankg index --incremental` | Only index changed files (git-based) |
| `leankg index --lang go,ts,py,rs,java,kotlin` | Filter by language |
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

## Auto-Indexing

```bash
# Start file watcher -- indexes changes automatically in background
leankg watch

# Incremental indexing -- only re-index changed files (git-based)
leankg index --incremental

# Filter by language
leankg index --lang go,ts,py,rs,java,kotlin

# Exclude patterns
leankg index --exclude vendor,node_modules,dist
```
