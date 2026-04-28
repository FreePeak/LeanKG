use regex::Regex;

pub struct GitDiffCompressor {
    stats_re: Regex,
    file_re: Regex,
    hunk_re: Regex,
}

impl Default for GitDiffCompressor {
    fn default() -> Self {
        Self::new()
    }
}

impl GitDiffCompressor {
    pub fn new() -> Self {
        Self {
            stats_re: Regex::new(r"^\s*(.+?)\s*\|\s*\d+\s*([+\-]+)$").unwrap(),
            file_re: Regex::new(r"^(diff --git|new file|deleted file|index|mode)").unwrap(),
            hunk_re: Regex::new(r"^@@\s*-\d+(?:,\d+)?\s*\+\d+(?:,\d+)?\s*@@").unwrap(),
        }
    }

    pub fn compress(&self, output: &str) -> String {
        let lines: Vec<&str> = output.lines().collect();
        let mut result = Vec::new();
        let mut in_diff = false;
        let mut stats_lines = Vec::new();
        let mut file_changes = 0;
        let mut insertions = 0;
        let mut deletions = 0;

        for line in lines {
            let line = line.trim();

            if line.is_empty() {
                continue;
            }

            if line.starts_with("diff --git") {
                in_diff = true;
                continue;
            }

            if line.starts_with("--- ") || line.starts_with("+++ ") {
                continue;
            }

            if self.file_re.is_match(line) {
                file_changes += 1;
                continue;
            }

            if self.stats_re.is_match(line) {
                if let Some(caps) = self.stats_re.captures(line) {
                    let insertions_delta = caps
                        .get(2)
                        .map_or(0, |m| m.as_str().chars().filter(|&c| c == '+').count());
                    let deletions_delta = caps
                        .get(2)
                        .map_or(0, |m| m.as_str().chars().filter(|&c| c == '-').count());
                    insertions += insertions_delta;
                    deletions += deletions_delta;
                    stats_lines.push(caps.get(1).map_or("", |m| m.as_str()).to_string());
                }
                continue;
            }

            if self.hunk_re.is_match(line) {
                continue;
            }

            if line.starts_with("+") && !line.starts_with("+++") {
                insertions += 1;
                continue;
            }

            if line.starts_with("-") && !line.starts_with("---") {
                deletions += 1;
                continue;
            }

            if in_diff && (line.starts_with(" ") || line.len() < 3) {
                continue;
            }
        }

        result.push("[GIT DIFF SUMMARY]".to_string());
        result.push(format!(
            "{} file(s) changed, +{} insertions, -{} deletions",
            file_changes, insertions, deletions
        ));

        if !stats_lines.is_empty() && stats_lines.len() <= 20 {
            result.push("Changed files:".to_string());
            for f in stats_lines {
                result.push(format!("  - {}", f));
            }
        }

        result.join("\n")
    }

    pub fn compress_stat_only(&self, output: &str) -> String {
        let lines: Vec<&str> = output.lines().collect();
        let mut stats_lines = Vec::new();
        let mut file_count = 0;

        for line in lines {
            let line = line.trim();

            if self.stats_re.is_match(line) {
                if let Some(caps) = self.stats_re.captures(line) {
                    stats_lines.push(caps.get(1).map_or("", |m| m.as_str()).to_string());
                    file_count += 1;
                }
            }
        }

        if stats_lines.is_empty() {
            return "[GIT DIFF] No changes".to_string();
        }

        let mut result = Vec::new();
        result.push(format!("{} file(s) changed:", file_count));

        for f in stats_lines.iter().take(20) {
            result.push(format!("  {}", f));
        }

        if stats_lines.len() > 20 {
            result.push(format!("  ... and {} more", stats_lines.len() - 20));
        }

        result.join("\n")
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
        let compressor = GitDiffCompressor::new();
        let result = compressor.compress("");
        assert!(result.contains("[GIT DIFF SUMMARY]"));
    }

    #[test]
    fn test_compress_stat() {
        let compressor = GitDiffCompressor::new();
        let output = r#"diff --git a/src/lib.rs b/src/lib.rs
index 1234567..abcdefg 100644
--- a/src/lib.rs
+++ b/src/lib.rs
@@ -10,7 +10,7 @@
 fn main() {
-    println!("old");
+    println!("new");
 }"#;
        let result = compressor.compress(output);
        assert!(result.contains("[GIT DIFF SUMMARY]"));
        assert!(result.contains("1 file(s) changed"));
    }

    #[test]
    fn test_compress_stat_only() {
        let compressor = GitDiffCompressor::new();
        let output = r#" src/lib.rs | 5 +++--
 src/main.rs | 2 ++
 2 files changed, 3 insertions(+), 2 deletions(-)"#;
        let result = compressor.compress_stat_only(output);
        assert!(result.contains("2 file(s) changed"));
        assert!(result.contains("src/lib.rs"));
    }

    #[test]
    fn test_estimate_savings() {
        let compressor = GitDiffCompressor::new();
        let original = "x".repeat(1000);
        let compressed = "x".repeat(100);
        let savings = compressor.estimate_savings(&original, &compressed);
        assert!((savings - 90.0).abs() < 0.1);
    }
}
