# LeanKG Benchmark

Compare token usage AND context correctness between LeanKG-assisted and baseline approaches.

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
cargo run -- benchmark -- cli kilo --category debugging
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
  With LeanKG:    17,363 tokens (input: 438, cached: 17,216)
  Without LeanKG:  18,933 tokens (input: 723, cached: 17,920)
  Overhead: -1,570 tokens
  
  LeanKG Quality: Precision=1.00 | Recall=1.00 | F1=1.00 | EXCELLENT
    Correct Files: ["src/db/models.rs"]
    Incorrect (false positives): []
    Missing (false negatives): []
  
  Without LeanKG Quality: Precision=1.00 | Recall=1.00 | F1=1.00 | EXCELLENT
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
