use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TestResult {
    pub name: String,
    pub status: TestStatus,
    pub duration_ms: Option<u64>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum TestStatus {
    Passed,
    Failed,
    Ignored,
}

pub struct CargoTestCompressor {
    max_failures: usize,
}

impl Default for CargoTestCompressor {
    fn default() -> Self {
        Self::new()
    }
}

impl CargoTestCompressor {
    pub fn new() -> Self {
        Self { max_failures: 10 }
    }

    pub fn with_max_failures(max: usize) -> Self {
        Self { max_failures: max }
    }

    pub fn compress(&self, output: &str) -> String {
        let lines: Vec<&str> = output.lines().collect();
        let mut result = Vec::new();
        let mut passed_count = 0;
        let mut _failed_count = 0;
        let mut failed_tests = Vec::new();
        let mut ignored_count = 0;
        let mut in_failures = false;

        for line in lines {
            let line = line.trim();

            if line.starts_with("test result:") {
                if let Some(stats) = self.parse_test_result(line) {
                    passed_count = stats.0;
                    _failed_count = stats.1;
                    ignored_count = stats.2;
                }
                continue;
            }

            if line.starts_with("failures:") {
                in_failures = true;
                continue;
            }

            if in_failures {
                if line.is_empty() {
                    in_failures = false;
                    continue;
                }
                if failed_tests.len() < self.max_failures {
                    if let Some(test_name) = self.extract_failed_test(line) {
                        failed_tests.push(test_name);
                    }
                }
                continue;
            }

            if self.is_noise_line(line) {
                continue;
            }

            if line.starts_with("running ") || line.starts_with("test ") {
                if line.contains(" ... ") {
                    if let Some(test) = self.parse_test_line(line) {
                        match test.status {
                            TestStatus::Passed => passed_count += 1,
                            TestStatus::Failed => {
                                if failed_tests.len() < self.max_failures {
                                    failed_tests.push(test.name);
                                }
                            }
                            TestStatus::Ignored => ignored_count += 1,
                        }
                    }
                }
                continue;
            }

            if self.is_summary_line(line) {
                if let Some(stats) = self.parse_test_result(line) {
                    passed_count = stats.0;
                    _failed_count = stats.1;
                    ignored_count = stats.2;
                }
                continue;
            }
        }

        result.push("[TEST SUMMARY]".to_string());

        if failed_tests.is_empty() {
            result.push(format!(
                "ok. {} passed; {} ignored",
                passed_count, ignored_count
            ));
        } else {
            result.push(format!(
                "FAIL: {}/{} failed",
                failed_tests.len(),
                passed_count + failed_tests.len()
            ));
            result.push("Failures:".to_string());
            for (i, test) in failed_tests.iter().enumerate() {
                if i >= self.max_failures {
                    result.push(format!(
                        "  ... and {} more",
                        failed_tests.len() - self.max_failures
                    ));
                    break;
                }
                result.push(format!("  - {}", test));
            }
        }

        result.join("\n")
    }

    fn parse_test_result(&self, line: &str) -> Option<(usize, usize, usize)> {
        let parts: Vec<&str> = line.split(';').collect();
        let mut passed = 0;
        let mut failed_count = 0;
        let mut ignored = 0;

        for part in parts {
            let part = part.trim();
            if part.contains("passed") {
                if let Some(n) = self.extract_number(part) {
                    passed = n;
                }
            } else if part.contains("failed") {
                if let Some(n) = self.extract_number(part) {
                    failed_count = n;
                }
            } else if part.contains("ignored") {
                if let Some(n) = self.extract_number(part) {
                    ignored = n;
                }
            }
        }

        if passed > 0 || failed_count > 0 || ignored > 0 {
            Some((passed, failed_count, ignored))
        } else {
            None
        }
    }

    fn parse_test_line(&self, line: &str) -> Option<TestResult> {
        let parts: Vec<&str> = line.split(" ... ").collect();
        if parts.len() != 2 {
            return None;
        }

        let name = parts[0]
            .trim()
            .strip_prefix("test ")
            .map(|s| s.to_string())?;
        let status_str = parts[1].trim();

        let status = if status_str.starts_with("ok") {
            TestStatus::Passed
        } else if status_str.starts_with("FAILED") {
            TestStatus::Failed
        } else if status_str.starts_with("ignored") {
            TestStatus::Ignored
        } else {
            return None;
        };

        Some(TestResult {
            name,
            status,
            duration_ms: None,
        })
    }

    fn extract_failed_test(&self, line: &str) -> Option<String> {
        let line = line.trim();
        if line.starts_with("test ") {
            Some(line.strip_prefix("test ")?.to_string())
        } else {
            None
        }
    }

    fn is_noise_line(&self, line: &str) -> bool {
        line.starts_with("Compiling")
            || line.starts_with("Finished")
            || line.starts_with("Running")
            || line.contains("warning: unused")
            || line.contains("warning: field ")
            || line.contains("warning: method ")
            || line.starts_with("   Compiling")
            || line.starts_with("   Finished")
            || line.contains("note: ")
            || line.starts_with("     Running")
            || line.is_empty()
    }

    fn is_summary_line(&self, line: &str) -> bool {
        line.starts_with("test result:")
    }

    fn extract_number(&self, part: &str) -> Option<usize> {
        let digits: String = part.chars().filter(|c| c.is_ascii_digit()).collect();
        digits.parse().ok()
    }

    pub fn estimate_savings(&self, original: &str, compressed: &str) -> f64 {
        let original_tokens = original.len() / 4;
        let compressed_tokens = compressed.len() / 4;
        if original_tokens == 0 {
            return 0.0;
        }
        ((original_tokens - compressed_tokens) as f64 / original_tokens as f64) * 100.0
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_compress_empty() {
        let compressor = CargoTestCompressor::new();
        let result = compressor.compress("");
        assert!(result.contains("[TEST SUMMARY]"));
    }

    #[test]
    fn test_compress_passed_tests() {
        let compressor = CargoTestCompressor::new();
        let output = r#"running 2 tests
test test_one ... ok
test test_two ... ok
test result: ok. 2 passed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s"#;
        let result = compressor.compress(output);
        assert!(result.contains("ok. 2 passed"));
    }

    #[test]
    fn test_compress_failed_tests() {
        let compressor = CargoTestCompressor::new();
        let output = r#"running 3 tests
test test_one ... FAILED
test test_two ... ok
test test_three ... ok
test result: FAILED. 2 passed; 1 failed; 0 ignored"#;
        let result = compressor.compress(output);
        assert!(result.contains("FAIL:"));
        assert!(result.contains("test_one"));
    }

    #[test]
    fn test_strips_noise() {
        let compressor = CargoTestCompressor::new();
        let output = r#"Compiling leankg v0.8.3
   Compiling leankg v0.8.3
warning: unused variable: `foo`
  --> src/lib.rs:10:5
Finished dev [unoptimized] target(s)
running 1 test
test test_one ... ok
test result: ok. 1 passed; 0 ignored"#;
        let result = compressor.compress(output);
        assert!(!result.contains("Compiling"));
        assert!(!result.contains("Finished"));
        assert!(!result.contains("warning:"));
    }

    #[test]
    fn test_estimate_savings() {
        let compressor = CargoTestCompressor::new();
        let original = "x".repeat(1000);
        let compressed = "x".repeat(100);
        let savings = compressor.estimate_savings(&original, &compressed);
        assert!((savings - 90.0).abs() < 0.1);
    }
}
