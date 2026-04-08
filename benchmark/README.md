# LeanKG Benchmark

Compare token usage AND context correctness between LeanKG-assisted and baseline approaches.

## Latest Results (2026-04-08)

| Category | Tests | LeanKG F1 Wins | Token Overhead |
|----------|-------|----------------|----------------|
| Navigation | 3 | 1 | +22,661 |
| Implementation | 1 | 0 | +209 |
| Impact | 3 | 1 | +18,178 |

**Total:** 7 tests, LeanKG wins F1 on 2 tests, +41,048 token overhead

See [docs/analysis/ab-testing-results-2026-04-08.md](../docs/analysis/ab-testing-results-2026-04-08.md) for full analysis.

## Usage

```bash
# Run all benchmarks with Kilo (recommended - provides full context quality metrics)
cargo run -- benchmark --cli kilo

# Run with OpenCode
cargo run -- benchmark --cli opencode

# Run with Gemini
cargo run -- benchmark --cli gemini

# Run specific category
cargo run -- benchmark --cli kilo --category navigation
cargo run -- benchmark --cli kilo --category implementation
cargo run -- benchmark --cli kilo --category impact
cargo run -- benchmark --cli kilo --category debugging
```

## What Gets Measured

### Token Efficiency
- Total tokens used with LeanKG vs without
- Token overhead/savings
- Input vs cached token breakdown

### Context Correctness (Kilo only)
- **Precision**: Are the files LeanKG found correct? (no false positives)
- **Recall**: Did LeanKG find ALL relevant files? (no false negatives)
- **F1 Score**: Harmonic mean of precision and recall
- **Verdict**: EXCELLENT (0.9-1.0) > GOOD (0.7-0.9) > MODERATE (0.5-0.7) > POOR (<0.5)

## Output Format

```
=== Category: Code Navigation Tasks ===

Running: find-codeelement
  With LeanKG:    16,696 tokens (input: 14,282, cached: 2,336)
  Without LeanKG:  16,689 tokens (input: 14,282, cached: 2,336)
  Overhead: +7 tokens
  
  LeanKG Quality: Precision=0.00 | Recall=0.00 | F1=0.00 | POOR
    Missing (false negatives): ["src/db/models.rs"]
  
  Without LeanKG Quality: Precision=0.00 | Recall=0.00 | F1=0.00 | POOR
```

## Results

See `results/` directory for detailed JSON and Markdown outputs per task.

### JSON Output
Contains full benchmark data including:
- Token counts (total, input, cached)
- Context quality metrics (precision, recall, F1)
- Files referenced

### Markdown Output
Human-readable comparison with file lists.

## Adding New Benchmark Tasks

1. Edit prompt YAML files in `prompts/` directory
2. Add `expected_files` field for ground truth validation:

```yaml
tasks:
  - id: "my-task"
    prompt: "Find the user authentication module"
    expected:
      - "auth/login.rs"
    expected_files:
      - "src/auth/login.rs"
```

## Benchmark Categories

- `navigation`: Find files, functions, understand structure
- `implementation`: Build new features
- `impact`: Find what breaks if X changes
- `debugging`: Trace bugs through code

## Limitations

- **OpenCode/Gemini**: Token parsing works, but context quality shows `(not available)` because these tools don't expose stdout for parsing
- **Kilo**: Provides full context output with quality metrics
- **Timeout**: Complex queries (impact, debugging) may timeout after 45-60 seconds

## Known Issues

1. **Token overhead** - LeanKG currently uses more tokens than baseline (pending deduplication fix)
2. **False positives** - Context parser returns too many files in some cases
3. **Kilo timeouts** - Complex queries timeout; use OpenCode for faster token-only metrics
