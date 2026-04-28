pub mod cargo_test;
pub mod entropy;
pub mod git_diff;
pub mod litm;
pub mod modes;
pub mod reader;
pub mod response;
pub mod session_cache;
pub mod shell;
pub mod signatures;
pub mod symbol_map;

pub use cargo_test::CargoTestCompressor;
pub use git_diff::GitDiffCompressor;
pub use modes::ReadMode;
pub use reader::FileReader;
pub use response::ResponseCompressor;
pub use session_cache::SessionCache;
pub use shell::ShellCompressor;

pub const CHARS_PER_TOKEN: usize = 4;

pub fn estimate_tokens(text: &str) -> usize {
    text.len() / CHARS_PER_TOKEN
}

pub fn estimate_tokens_precise(text: &str) -> usize {
    let mut token_count = 0;
    let mut in_whitespace = true;

    for c in text.chars() {
        if c.is_whitespace() {
            in_whitespace = true;
        } else if in_whitespace {
            token_count += 1;
            in_whitespace = false;
        }
    }

    if !text.is_empty()
        && !text
            .chars()
            .last()
            .map(|c| c.is_whitespace())
            .unwrap_or(false)
    {
        token_count += 1;
    }

    token_count
}

pub struct LeanKGCompressor {
    shell: ShellCompressor,
    cargo_test: CargoTestCompressor,
    git_diff: GitDiffCompressor,
}

impl Default for LeanKGCompressor {
    fn default() -> Self {
        Self::new()
    }
}

impl LeanKGCompressor {
    pub fn new() -> Self {
        Self {
            shell: ShellCompressor::new(),
            cargo_test: CargoTestCompressor::new(),
            git_diff: GitDiffCompressor::new(),
        }
    }

    pub fn compress(&self, cmd: &str, output: &str) -> String {
        let cmd_lower = cmd.to_lowercase();

        if cmd_lower.contains("cargo test") && !cmd_lower.contains("--no-run") {
            return self.cargo_test.compress(output);
        }

        if cmd_lower.contains("git diff") && !cmd_lower.contains("--stat") {
            return self.git_diff.compress(output);
        }

        if cmd_lower.contains("git diff") && cmd_lower.contains("--stat") {
            return self.git_diff.compress_stat_only(output);
        }

        self.shell.compress(cmd, output)
    }

    pub fn estimate_savings(&self, original: &str, compressed: &str) -> f64 {
        let original_tokens = estimate_tokens(original);
        let compressed_tokens = estimate_tokens(compressed);
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
    fn test_estimate_tokens() {
        assert_eq!(estimate_tokens("hello world"), 2);
        assert_eq!(estimate_tokens("fn foo()"), 2);
    }

    #[test]
    fn test_estimate_tokens_precise() {
        let text = "fn main() {\n    println!(\"hello\");\n}";
        let tokens = estimate_tokens_precise(&text);
        assert!(tokens > 0);
    }

    #[test]
    fn test_leankg_compressor_cargo_test() {
        let compressor = LeanKGCompressor::new();
        let output = r#"running 2 tests
test test_one ... ok
test test_two ... ok
test result: ok. 2 passed; 0 ignored"#;
        let compressed = compressor.compress("cargo test", output);
        assert!(compressed.contains("ok. 2 passed"));
    }

    #[test]
    fn test_leankg_compressor_git_diff() {
        let compressor = LeanKGCompressor::new();
        let output = r#"diff --git a/src/lib.rs b/src/lib.rs
index 1234567..abcdefg 100644
--- a/src/lib.rs
+++ b/src/lib.rs
@@ -10,7 +10,7 @@
 fn main() {
-    println!("old");
+    println!("new");
 }"#;
        let compressed = compressor.compress("git diff", output);
        assert!(compressed.contains("[GIT DIFF SUMMARY]"));
    }

    #[test]
    fn test_estimate_savings() {
        let compressor = LeanKGCompressor::new();
        let original = "x".repeat(1000);
        let compressed = "x".repeat(100);
        let savings = compressor.estimate_savings(&original, &compressed);
        assert!((savings - 90.0).abs() < 0.1);
    }
}
