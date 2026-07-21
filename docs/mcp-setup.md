# MCP Server Setup

LeanKG exposes a Model Context Protocol (MCP) server that AI tools can connect to.

**Tool prefer-order:** See [MCP tools](mcp-tools.md) — overview (`get_overview_context`), search (`concept_search` → `semantic_search` → `search_code`), environment (`env=` on search/`kg_*`). Hard-removed: `wake_up`, `search_by_environment`, and others listed there (~81 tools with embeddings).

## Automated Setup (Recommended)

Use the install script to install and configure MCP for your AI tool:

```bash
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- <target>
```

See [Installation](../README.md#installation) for supported targets.

## Manual Setup

### OpenCode AI

Add to `~/.config/opencode/opencode.json`:

```json
{
  "mcp": {
    "leankg_dev": {
      "type": "local",
      "command": ["leankg", "mcp-stdio", "--watch"],
      "enabled": true
    }
  }
}
```

### Cursor AI

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

### Claude Code / Claude Desktop

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

### Gemini CLI

Add to `~/.config/gemini-cli/mcp.json`:

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

### Google Antigravity

Add to `~/.gemini/antigravity/mcp_config.json`:

```json
{
  "mcpServers": [
    {
      "name": "leankg",
      "transport": "stdio",
      "command": "leankg",
      "args": ["mcp-stdio", "--watch"],
      "enabled": true
    }
  ]
}
```

### Kilo Code

Add to `~/.config/kilo/kilo.json`:

```json
{
  "$schema": "https://kilo.ai/config.json",
  "mcp": {
    "leankg": {
      "type": "local",
      "command": ["leankg", "mcp-stdio", "--watch"],
      "enabled": true
    }
  }
}
```

## Starting the MCP Server

```bash
# Stdio mode with auto-indexing (for local AI tools)
leankg mcp-stdio --watch

# Stdio mode without auto-indexing
leankg mcp-stdio
```

## Auto-Initialization

When the MCP server starts without an existing LeanKG project, it automatically initializes and indexes the current directory. This provides a "plug and play" experience for AI tools.

## Auto-Indexing

When the MCP server starts with an existing LeanKG project, it checks if the index is stale (by comparing git HEAD commit time vs database file modification time). If stale, it automatically runs incremental indexing to ensure AI tools have up-to-date context.

## Fallback

If the MCP server reports "LeanKG not initialized", manually run `leankg init` in your project directory, then restart the AI tool.

## Embedding Retrieval (optional, `embeddings` feature)

The `kg_semantic_context` tool — vector retrieve + cross-encoder rerank + adaptive KG traversal — only ships when LeanKG is built with `--features embeddings`. Default builds skip it to keep the binary lean.

### Building with the feature

```bash
# From a LeanKG checkout:
cargo build --release --features embeddings

# Or directly install the binary with the feature on:
cargo install --path . --features embeddings
```

This pulls in `fastembed` (ONNX-backed embedding + reranker inference) and `usearch` (HNSW ANN index). The first build downloads ONNX Runtime binaries via fastembed's deps.

### One-time setup per machine

```bash
# 1. Pre-download embedding (BGE-small-en-v1.5, ~130MB) and reranker
#    (bge-reranker-v2-m3, ~600MB) into ~/.cache/leankg/models:
leankg embed --init

# 2. Index your project (if not already indexed):
leankg index ./src

# 3. Build the embedding index (~seconds for incremental, minutes for a
#    fresh 10k-node repo on CPU):
leankg embed
```

### Index lifecycle

`leankg embed` (default) is **incremental**: it reads the `embedding_state` CozoDB table that tracks per-node freshness and only re-embeds nodes that are stale (touched by a recent `index` run), missing (newly added), or whose text blob hash changed. Orphans (state rows whose `qualified_name` is no longer in `code_elements`) are reaped.

`leankg embed --full` ignores state and re-embeds every node. Use after a model swap or suspected index corruption.

The `index` command marks touched elements stale but does **not** trigger `embed` automatically — embedding is a separate explicit step. The MCP tool surfaces a stale-embeddings warning in `diagnostics.embeddings_stale` so callers know when to re-run `embed`.

### Worktree exclusion

By default, `kg_semantic_context` filters out paths under `.worktrees/`, `.claude/worktrees/`, and `.opencode/worktrees/` to avoid duplicate-noise from agent scratch copies. Pass `include_worktrees: true` to include them.

### Reranker fallback

If the reranker fails to load or score, the tool falls back to ANN-order top-N (no cross-encoder). `diagnostics.reranker` will be `"fallback_ann"` instead of `"bge-reranker-v2-m3"`. The most common cause is a partial model download — re-running `leankg embed --init` fixes it.

