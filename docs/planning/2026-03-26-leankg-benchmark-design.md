# LeanKG Benchmark Design

## Overview

Benchmark LeanKG's effectiveness at providing concise, evidence-backed context for code tasks. Compare token usage and analysis quality with vs without LeanKG.

## Project Structure

```
benchmark/
├── README.md           # Summary + how to run
├── results/            # Generated benchmark results
└── prompts/
    ├── navigation.yaml    # Find files, understand structure
    ├── impact.yaml        # What breaks if I change X
    ├── debugging.yaml     # Trace a bug
    └── implementation.yaml # Build a feature
```

## Prompt File Format

```yaml
# benchmark/prompts/navigation.yaml
name: "Code Navigation Tasks"
description: "Find files, functions, understand structure"

tasks:
  - id: "find-handler"
    prompt: "Find the MCP server handler in LeanKG"
    expected:
      - "src/mcp/handler.rs"
      - "src/mcp/server.rs"
```

## Results Format

```json
// benchmark/results/YYYY-MM-DD-task-type.json
{
  "date": "2026-03-26",
  "task_type": "navigation",
  "without_leankg": {
    "total_tokens": 35070,
    "token_percent": 17,
    "build_time_seconds": 19.9,
    "success": true
  },
  "with_leankg": {
    "total_tokens": 39290,
    "token_percent": 19,
    "build_time_seconds": 27.9,
    "success": true
  },
  "token_overhead": 4220,
  "overhead_percent": 2
}
```

## Implementation

### BenchmarkRunner (`src/benchmark/runner.rs`)

```rust
pub struct BenchmarkRunner {
    opencode_path: PathBuf,
    output_dir: PathBuf,
}

impl BenchmarkRunner {
    pub fn run_prompt(&self, prompt: &str) -> BenchmarkResult;
    pub fn run_with_leankg(&self, prompt: &str) -> BenchmarkResult;
    pub fn run_without_leankg(&self, prompt: &str) -> BenchmarkResult;
}
```

### CLI Integration

```bash
cargo run --benchmark           # Run all benchmarks
cargo run --benchmark -- navigation  # Run specific category
```

## Metrics Tracked

- Total tokens used
- Token percentage of limit
- Build/execution time
- Task success (did it find expected results?)
- LeanKG token overhead (delta)

## Execution Modes

1. **Manual mode** - User runs prompts via OpenCode, captures output
2. **Scripted mode** - Script runs OpenCode commands and parses token output

Chosen: Scripted mode for automated comparison.

## Validation

User manually runs benchmark and verifies results match expected outcomes documented in prompt YAML files.
