# LeanKG CLI Reference

Complete reference for all LeanKG CLI commands.

## CLI Commands

| Command | Description |
|---------|-------------|
| `leankg version` | Show LeanKG version |
| `leankg init [--path <path>]` | Initialize LeanKG in the current directory |
| `leankg index [--path <path>] [--incremental] [--lang <lang>] [--exclude <patterns>]` | Index source files |
| `leankg query [--kind <type>]` | Query the knowledge graph |
| `leankg serve [--port <port>]` | Start the MCP server (deprecated, use `mcp-stdio`) |
| `leankg web [--port <port>]` | Start the embedded web UI server |
| `leankg mcp-stdio [--watch] [--project-path <path>]` | Start MCP server with stdio transport |
| `leankg impact <file> [--depth <depth>]` | Compute blast radius for a file |
| `leankg status` | Show index statistics and status |
| `leankg install` | Auto-install MCP config for AI tools |
| `leankg watch [--path <path>]` | Start file watcher for auto-indexing |
| `leankg quality [--min-lines <lines>] [--lang <lang>]` | Find oversized functions |
| `leankg export [--output <file>] [--format <json\|dot\|mermaid\|html\|svg\|graphml\|neo4j>] [--file <file>] [--depth <depth>]` | Export knowledge graph |
| `leankg annotate <element> --description <desc> [--user-story <id>] [--feature <id>]` | Annotate code element |
| `leankg link <element> <id> [--kind <story\|feature>]` | Link code element to user story/feature |
| `leankg search-annotations <query>` | Search business logic annotations |
| `leankg show-annotations <element>` | Show annotations for an element |
| `leankg trace [--feature <id>] [--user-story <id>] [--all]` | Show feature-to-code traceability |
| `leankg find-by-domain <domain>` | Find code elements by business domain |
| `leankg benchmark [--category <cat>] [--cli <opencode\|gemini\|kilo>]` | Run benchmark comparison |
| `leankg register <name>` | Register repository in global registry |
| `leankg unregister <name>` | Unregister repository |
| `leankg list` | List all registered repositories |
| `leankg status-repo <name>` | Show status for registered repository |
| `leankg setup` | Global setup: configure MCP for all registered repos |
| `leankg run [--compress] -- <command>` | Run shell command with RTK-style compression |
| `leankg detect-clusters [--path <path>] [--min-hub-edges <n>]` | Run community detection |
| `leankg api-serve [--port <port>] [--auth]` | Start REST API server |
| `leankg api-key create --name <name>` | Create API key |
| `leankg api-key list` | List API keys |
| `leankg api-key revoke --id <id>` | Revoke API key |
| `leankg hooks install` | Install LeanKG git hooks |
| `leankg hooks uninstall` | Uninstall LeanKG git hooks |
| `leankg hooks status` | Check git hooks status |
| `leankg hooks watch [--path <path>]` | Watch git events and sync index |
| `leankg wiki [--output <dir>]` | Generate wiki |
| `leankg metrics [--since <period>] [--tool <name>] [--json] [--session] [--reset] [--retention <days>] [--cleanup] [--seed]` | Show context metrics |
| `leankg help` | Show help |

## Quick Start

```bash
# 1. Initialize LeanKG in your project
leankg init

# 2. Index your codebase
leankg index ./src

# 3. Start the MCP server (for AI tools)
leankg mcp-stdio --watch

# 4. Compute impact radius for a file
leankg impact src/main.rs --depth 3

# 5. Check index status
leankg status

# 6. Start web UI (optional)
leankg web
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

## Graph Export Formats

LeanKG supports multiple export formats:

```bash
# Export as JSON (default)
leankg export --output graph.json --format json

# Export as Graphviz DOT
leankg export --output graph.dot --format dot

# Export as Mermaid diagram
leankg export --output graph.mmd --format mermaid

# Export as interactive HTML
leankg export --output graph.html --format html

# Export as SVG
leankg export --output graph.svg --format svg

# Export as GraphML (for Gephi, etc.)
leankg export --output graph.graphml --format graphml

# Export for Neo4j
leankg export --output graph.cypher --format neo4j
```

## Context Metrics

Track token savings and usage. Only entries with positive savings (where LeanKG outputs fewer tokens than baseline) are displayed.

```bash
# View metrics summary
leankg metrics

# View with JSON output
leankg metrics --json

# Filter by time period
leankg metrics --since 7d

# Filter by tool name
leankg metrics --tool search_code

# Seed test data for demo
leankg metrics --seed

# Reset all metrics
leankg metrics --reset

# Cleanup old metrics
leankg metrics --cleanup --retention 60
```

**Example output:**
```
=== LeanKG Context Metrics ===

Total Savings: 64,160 tokens across 7 calls
Average Savings: 99.4%
Retention: 30 days

By Tool:
  search_code:        2 calls,  avg 100% saved, 25,903 tokens saved
  get_impact_radius:  1 calls,  avg 99% saved, 24,820 tokens saved
  get_context:        1 calls,  avg 100% saved, 7,965 tokens saved
  find_function:      1 calls,  avg 100% saved, 5,972 tokens saved
```

## Benchmark

Compare LeanKG against other tools:

```bash
# Run all benchmark categories
leankg benchmark

# Run specific category
leankg benchmark --category navigation

# Compare against specific CLI
leankg benchmark --cli opencode
```

## REST API

Start the REST API server:

```bash
# Start without auth (for development)
leankg api-serve --port 8080

# Start with authentication
leankg api-serve --port 8080 --auth

# Create API key
leankg api-key create --name "my-app"

# List API keys
leankg api-key list

# Revoke API key
leankg api-key revoke --id <key-id>
```

## Compression Modes

For `ctx_read` and `orchestrate` tools:

| Mode | Description |
|------|-------------|
| `adaptive` | Auto-select based on file size |
| `full` | Full file content |
| `map` | Map/signature view |
| `signatures` | Function signatures only |
| `diff` | Diff-like view |
| `aggressive` | Maximum compression |
| `entropy` | High-entropy regions |
| `lines` | Specific lines (requires `lines` parameter) |
