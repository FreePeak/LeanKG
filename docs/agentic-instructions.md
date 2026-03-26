# LeanKG Agentic Instructions

LeanKG can instruct AI coding agents to use it **first** before falling back to naive search. This works similarly to the [Morph plugin for OpenCode](https://github.com/morphllm/opencode-morph-plugin).

## How It Works

1. LeanKG embeds an **instructions file** that tells AI agents when to use LeanKG tools
2. The instructions are written to `instructions/leankg-tools.md` during `leankg install`
3. OpenCode reads these instructions and follows them automatically

## Setup

```bash
# 1. Install LeanKG with MCP config
leankg install

# 2. Copy instructions to OpenCode config directory
mkdir -p ~/.config/opencode
cp instructions/leankg-tools.md ~/.config/opencode/

# 3. Verify OpenCode picks up the instructions
opencode
# You should see LeanKG tools listed first in available tools
```

## What the Instructions Do

The instructions tell AI agents:

| Task | Use LeanKG Instead Of |
|------|----------------------|
| Find "where is X?" | grep/ripgrep |
| "What tests cover this?" | glob + content search |
| "What would break if I change X?" | Manual dependency tracing |
| "How does X work?" | Reading entire files |

**Decision flow for AI agents:**
```
User asks about codebase ->
  First check LeanKG tools (mcp_status) ->
    If not initialized, run mcp_init first ->
    Use appropriate LeanKG tool ->
      NEVER fall back to naive search until LeanKG is exhausted
```

## Tools AI Agents Learn to Use First

| Priority | Tool | Use Case |
|----------|------|----------|
| 1 | `search_code` | Find code by name/type |
| 2 | `get_impact_radius` | Blast radius before changes |
| 3 | `get_call_graph` | Understand function flow |
| 4 | `get_tested_by` | Find related tests |
| 5 | `get_dependencies` | Understand imports |
