# AB Testing Context Correctness Validation - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the benchmark system to validate LeanKG provides correct context (file paths) not just track tokens. Prove LeanKG helps AI find RIGHT files.

**Architecture:** 
- New `context_parser.rs` module to extract file paths from LLM stdout
- Extended `BenchmarkResult` with optional `QualityMetrics`
- Enhanced CLI output showing precision/recall/F1 for both WITH and WITHOUT LeanKG
- JSON output with full context quality details

**Tech Stack:** Rust, serde_json, regex

---

## File Structure

| File | Change |
|------|--------|
| `src/benchmark/context_parser.rs` | NEW - Parse LLM output for file paths |
| `src/benchmark/data.rs` | Add `QualityMetrics`, extend `BenchmarkResult` |
| `src/benchmark/runner.rs` | Call context parser after each run |
| `src/benchmark/mod.rs` | Enhanced output formatting with quality metrics |

---

## Task 1: Create Context Parser Module

**Files:**
- Create: `src/benchmark/context_parser.rs`
- Modify: `src/benchmark/mod.rs` (add `pub mod context_parser;`)

- [ ] **Step 1: Create context_parser.rs with QualityMetrics struct**

```rust
use serde::{Deserialize, Serialize};
use std::collections::HashSet;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct QualityMetrics {
    pub precision: f32,
    pub recall: f32,
    pub f1_score: f32,
    pub correct_files: Vec<String>,
    pub incorrect_files: Vec<String>,
    pub missing_files: Vec<String>,
}

impl QualityMetrics {
    pub fn calculate(expected: &[String], actual: &[String]) -> Self {
        let expected_set: HashSet<_> = expected.iter().collect();
        let actual_set: HashSet<_> = actual.iter().collect();

        let correct: Vec<_> = expected_set.intersection(&actual_set).collect();
        let incorrect: Vec<_> = actual_set.difference(&expected_set).collect();
        let missing: Vec<_> = expected_set.difference(&actual_set).collect();

        let correct_count = correct.len() as f32;
        let incorrect_count = incorrect.len() as f32;
        let missing_count = missing.len() as f32;

        let precision = if correct_count + incorrect_count > 0.0 {
            correct_count / (correct_count + incorrect_count)
        } else {
            0.0
        };

        let recall = if correct_count + missing_count > 0.0 {
            correct_count / (correct_count + missing_count)
        } else {
            0.0
        };

        let f1_score = if precision + recall > 0.0 {
            2.0 * (precision * recall) / (precision + recall)
        } else {
            0.0
        };

        QualityMetrics {
            precision,
            recall,
            f1_score,
            correct_files: correct.iter().map(|s| (*s).to_string()).collect(),
            incorrect_files: incorrect.iter().map(|s| (*s).to_string()).collect(),
            missing_files: missing.iter().map(|s| (*s).to_string()).collect(),
        }
    }

    pub fn verdict(&self) -> &str {
        match self.f1_score {
            0.9..=1.0 => "EXCELLENT",
            0.7..=0.9 => "GOOD",
            0.5..=0.7 => "MODERATE",
            _ => "POOR",
        }
    }
}
```

- [ ] **Step 2: Add ContextParser struct with file path extraction**

```rust
pub struct ContextParser;

impl ContextParser {
    pub fn parse_file_paths(stdout: &str) -> Vec<String> {
        let mut files = Vec::new();
        let patterns = [
            r"src/[^\s:\[\]\{\},]+",      // src/
            r"lib/[^\s:\[\]\{\},]+",       // lib/
            r"tests/[^\s:\[\]\{\},]+",    // tests/
            r"bin/[^\s:\[\]\{\},]+",       // bin/
            r"cmd/[^\s:\[\]\{\},]+",      // cmd/
            r"pkg/[^\s:\[\]\{\},]+",      // pkg/
        ];

        for pattern in &patterns {
            if let Ok(re) = regex::Regex::new(pattern) {
                for cap in re.find_iter(stdout) {
                    let path = cap.as_str().to_string();
                    if !files.contains(&path) {
                        files.push(path);
                    }
                }
            }
        }
        files
    }
}
```

- [ ] **Step 3: Add mod.rs export**

```rust
pub mod context_parser;
pub mod data;
pub mod runner;
pub mod summary;
pub use context_parser::{ContextParser, QualityMetrics};
```

- [ ] **Step 4: Build and verify**

```bash
cargo build 2>&1
```

- [ ] **Step 5: Commit**

```bash
git add src/benchmark/context_parser.rs src/benchmark/mod.rs
git commit -m "feat(ab-testing): add context parser module with QualityMetrics"
```

---

## Task 2: Extend BenchmarkResult with Context Quality

**Files:**
- Modify: `src/benchmark/data.rs`

- [ ] **Step 1: Add ParsedContext struct**

```rust
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ParsedContext {
    pub files_referenced: Vec<String>,
}
```

- [ ] **Step 2: Add context_quality field to BenchmarkResult**

```rust
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BenchmarkResult {
    pub total_tokens: u32,
    pub input_tokens: u32,
    pub cached_tokens: u32,
    pub token_percent: f32,
    pub build_time_seconds: f32,
    pub success: bool,
    #[serde(default)]
    pub context: Option<ParsedContext>,
}
```

- [ ] **Step 3: Add quality field to PromptTask**

```rust
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PromptTask {
    pub id: String,
    pub prompt: String,
    pub expected: Vec<String>,
    #[serde(default)]
    pub expected_files: Vec<String>,
}
```

- [ ] **Step 4: Build and verify**

```bash
cargo build 2>&1
```

- [ ] **Step 5: Commit**

```bash
git add src/benchmark/data.rs
git commit -m "feat(ab-testing): extend BenchmarkResult with ParsedContext"
```

---

## Task 3: Integrate Context Parser into Runner

**Files:**
- Modify: `src/benchmark/runner.rs`

- [ ] **Step 1: Import ContextParser**

```rust
use crate::benchmark::context_parser::ContextParser;
```

- [ ] **Step 2: Modify run_with_leankg to parse context**

Update `run_with_leankg` method to also parse stdout for files:

```rust
pub fn run_with_leankg(&self, prompt: &str) -> BenchmarkResult {
    match self.cli {
        CliTool::Kilo => {
            self.switch_mcp_config(true);
            let (result, stdout) = self.run_kilo_with_output(prompt);
            let files = ContextParser::parse_file_paths(&stdout);
            BenchmarkResult {
                context: Some(ParsedContext { files_referenced: files }),
                ..result
            }
        }
        CliTool::OpenCode => self.run_opencode(prompt),
        CliTool::Gemini => self.run_gemini(prompt),
    }
}
```

- [ ] **Step 3: Add run_kilo_with_output helper**

```rust
fn run_kilo_with_output(&self, prompt: &str) -> (BenchmarkResult, String) {
    let child = Command::new("kilo")
        .arg("run")
        .arg("--format")
        .arg("json")
        .arg("--auto")
        .arg(prompt)
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("Failed to spawn kilo");

    let output = match child.wait_with_output_timeout(Duration::from_secs(120)) {
        Ok(result) => result,
        Err(_) => {
            return (
                BenchmarkResult {
                    total_tokens: 0,
                    input_tokens: 0,
                    cached_tokens: 0,
                    token_percent: 0.0,
                    build_time_seconds: 120.0,
                    success: false,
                    context: None,
                },
                String::new(),
            );
        }
    };

    let stdout = String::from_utf8_lossy(&output.stdout).to_string();
    let result = self.parse_kilo_output(&stdout);
    (result, stdout)
}
```

- [ ] **Step 4: Build and verify**

```bash
cargo build 2>&1
```

- [ ] **Step 5: Commit**

```bash
git add src/benchmark/runner.rs
git commit -m "feat(ab-testing): integrate context parser into runner"
```

---

## Task 4: Enhanced Console Output with Quality Metrics

**Files:**
- Modify: `src/benchmark/mod.rs`

- [ ] **Step 1: Update run function with quality output**

```rust
pub fn run(category: Option<String>, cli: CliTool) -> Result<(), Box<dyn std::error::Error>> {
    let prompts_dir = PathBuf::from("benchmark/prompts");
    let output_dir = PathBuf::from("benchmark/results");

    let categories = if let Some(cat) = category {
        vec![data::PromptCategory::from_yaml(
            &prompts_dir.join(format!("{}.yaml", cat)),
        )?]
    } else {
        data::PromptCategory::load_all(&prompts_dir)?
    };

    let runner = BenchmarkRunner::new(output_dir.clone(), cli);

    for cat in &categories {
        println!("\n=== Category: {} ===\n", cat.name);
        for task in &cat.tasks {
            println!("Running: {}", task.id);

            let with_leankg = runner.run_with_leankg(&task.prompt);
            let without_leankg = runner.run_without_leankg(&task.prompt);

            let overhead = with_leankg.overhead(&without_leankg);

            println!(
                "  With LeanKG:    {} tokens (input: {}, cached: {})",
                with_leankg.total_tokens, with_leankg.input_tokens, with_leankg.cached_tokens
            );
            println!(
                "  Without LeanKG: {} tokens (input: {}, cached: {})",
                without_leankg.total_tokens,
                without_leankg.input_tokens,
                without_leankg.cached_tokens
            );
            println!("  Overhead: {} tokens", overhead.token_delta);

            // Calculate and print quality metrics
            if !task.expected_files.is_empty() {
                let with_quality = with_leankg.context.as_ref()
                    .map(|c| QualityMetrics::calculate(&task.expected_files, &c.files_referenced));
                let without_quality = without_leankg.context.as_ref()
                    .map(|c| QualityMetrics::calculate(&task.expected_files, &c.files_referenced));

                if let Some(wq) = &with_quality {
                    println!("  LeanKG Quality: Precision={:.2} | Recall={:.2} | F1={:.2} | {}",
                        wq.precision, wq.recall, wq.f1_score, wq.verdict());
                    println!("    Files: {:?}", wq.correct_files);
                    if !wq.incorrect_files.is_empty() {
                        println!("    Incorrect: {:?}", wq.incorrect_files);
                    }
                    if !wq.missing_files.is_empty() {
                        println!("    Missing: {:?}", wq.missing_files);
                    }
                }

                if let Some(uq) = &without_quality {
                    println!("  Without Quality: Precision={:.2} | Recall={:.2} | F1={:.2} | {}",
                        uq.precision, uq.recall, uq.f1_score, uq.verdict());
                }
            }
            println!();

            let _ = runner.save_comparison(&with_leankg, &without_leankg, &task.id);
        }
    }

    Ok(())
}
```

- [ ] **Step 2: Build and verify**

```bash
cargo build 2>&1
```

- [ ] **Step 3: Commit**

```bash
git add src/benchmark/mod.rs
git commit -m "feat(ab-testing): add enhanced console output with quality metrics"
```

---

## Task 5: Update JSON Output with Context Quality

**Files:**
- Modify: `src/benchmark/runner.rs` (save_comparison method)

- [ ] **Step 1: Update save_comparison to include context quality**

The `save_comparison` method should serialize context if present:

```rust
pub fn save_comparison(
    &self,
    with_leankg: &BenchmarkResult,
    without_leankg: &BenchmarkResult,
    name: &str,
) -> Result<(), Box<dyn Error>> {
    let overhead = with_leankg.overhead(without_leankg);

    let comparison = serde_json::json!({
        "task": name,
        "with_leankg": with_leankg,
        "without_leankg": without_leankg,
        "overhead": overhead,
    });

    let json_path = self.output_dir.join(format!("{}-comparison.json", name));
    std::fs::write(&json_path, serde_json::to_string_pretty(&comparison)?)?;

    let md_path = self.output_dir.join(format!("{}-comparison.md", name));
    let mut md = format!(
        "# Benchmark Comparison: {}\n\n## With LeanKG\n- Total Tokens: {}\n- Input: {}\n- Cached: {}\n",
        name,
        with_leankg.total_tokens, with_leankg.input_tokens, with_leankg.cached_tokens
    );

    if let Some(ctx) = &with_leankg.context {
        md.push_str(&format!("- Files Referenced: {:?}\n", ctx.files_referenced));
    }

    md.push_str(&format!(
        "\n## Without LeanKG\n- Total Tokens: {}\n- Input: {}\n- Cached: {}\n",
        without_leankg.total_tokens, without_leankg.input_tokens, without_leankg.cached_tokens
    ));

    if let Some(ctx) = &without_leankg.context {
        md.push_str(&format!("- Files Referenced: {:?}\n", ctx.files_referenced));
    }

    md.push_str(&format!("\n## Overhead\n- Token Delta: {}\n", overhead.token_delta));

    std::fs::write(&md_path, md)?;

    Ok(())
}
```

- [ ] **Step 2: Build and verify**

```bash
cargo build 2>&1
```

- [ ] **Step 3: Commit**

```bash
git add src/benchmark/runner.rs
git commit -m "feat(ab-testing): update JSON output with context quality"
```

---

## Task 6: Add Unit Tests for Context Parser

**Files:**
- Create: `tests/benchmark_context_parser_tests.rs`

- [ ] **Step 1: Create tests for QualityMetrics::calculate**

```rust
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_quality_metrics_perfect_match() {
        let expected = vec!["src/a.rs".to_string(), "src/b.rs".to_string()];
        let actual = vec!["src/a.rs".to_string(), "src/b.rs".to_string()];

        let metrics = QualityMetrics::calculate(&expected, &actual);

        assert_eq!(metrics.precision, 1.0);
        assert_eq!(metrics.recall, 1.0);
        assert_eq!(metrics.f1_score, 1.0);
        assert!(metrics.incorrect_files.is_empty());
        assert!(metrics.missing_files.is_empty());
    }

    #[test]
    fn test_quality_metrics_partial_match() {
        let expected = vec!["src/a.rs".to_string(), "src/b.rs".to_string()];
        let actual = vec!["src/a.rs".to_string(), "src/c.rs".to_string()];

        let metrics = QualityMetrics::calculate(&expected, &actual);

        assert!((metrics.precision - 0.5).abs() < 0.01);
        assert!((metrics.recall - 0.5).abs() < 0.01);
        assert!((metrics.f1_score - 0.5).abs() < 0.01);
    }

    #[test]
    fn test_quality_metrics_no_match() {
        let expected = vec!["src/a.rs".to_string()];
        let actual = vec!["src/b.rs".to_string()];

        let metrics = QualityMetrics::calculate(&expected, &actual);

        assert_eq!(metrics.precision, 0.0);
        assert_eq!(metrics.recall, 0.0);
        assert_eq!(metrics.f1_score, 0.0);
    }

    #[test]
    fn test_context_parser_extracts_src_paths() {
        let stdout = "You should look at src/main.rs and src/lib.rs for the implementation";
        let files = ContextParser::parse_file_paths(stdout);

        assert!(files.contains(&"src/main.rs".to_string()));
        assert!(files.contains(&"src/lib.rs".to_string()));
    }

    #[test]
    fn test_context_parser_handles_multiple_paths() {
        let stdout = "Found in src/db/models.rs and lib/helper.rs";
        let files = ContextParser::parse_file_paths(stdout);

        assert!(files.contains(&"src/db/models.rs".to_string()));
        assert!(files.contains(&"lib/helper.rs".to_string()));
    }

    #[test]
    fn test_context_parser_deduplicates() {
        let stdout = "src/main.rs appears twice in the code";
        let files = ContextParser::parse_file_paths(stdout);

        let main_count = files.iter().filter(|f| *f == "src/main.rs").count();
        assert_eq!(main_count, 1);
    }
}
```

- [ ] **Step 2: Run tests**

```bash
cargo test --test benchmark_context_parser_tests 2>&1
```

- [ ] **Step 3: Commit**

```bash
git add tests/benchmark_context_parser_tests.rs
git commit -m "test(ab-testing): add unit tests for context parser"
```

---

## Task 7: Manual Verification

**Files:**
- None (testing only)

- [ ] **Step 1: Build and run benchmark**

```bash
cargo build
cargo run -- benchmark --cli opencode --category navigation
```

- [ ] **Step 2: Check JSON output**

```bash
cat benchmark/results/find-codeelement-comparison.json
```

- [ ] **Step 3: Verify output shows quality metrics**

Expected output should include:
```
LeanKG Quality: Precision=1.00 | Recall=1.00 | F1=1.00 | EXCELLENT
Without Quality: Precision=0.33 | Recall=1.00 | F1=0.50 | MODERATE
```

---

## Self-Review Checklist

- [ ] All 6 tasks completed
- [ ] Each task has its own commit
- [ ] Tests pass: `cargo test --test benchmark_context_parser_tests`
- [ ] Build succeeds: `cargo build`
- [ ] Manual verification shows quality metrics in output
- [ ] JSON output includes context files

**Plan complete. Execution options:**

1. **Subagent-Driven (recommended)** - Dispatch fresh subagent per task, review between tasks
2. **Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

Which approach?
