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

pub struct ContextParser;

impl ContextParser {
    pub fn parse_file_paths(stdout: &str) -> Vec<String> {
        let mut files = Vec::new();
        let patterns = [
            r"src/[\w\./-]+",
            r"lib/[\w\./-]+",
            r"tests/[\w\./-]+",
            r"bin/[\w\./-]+",
            r"cmd/[\w\./-]+",
            r"pkg/[\w\./-]+",
        ];

        for pattern in &patterns {
            if let Ok(re) = regex::Regex::new(pattern) {
                for cap in re.find_iter(stdout) {
                    let path = cap.as_str().to_string();
                    if !files.contains(&path) && Self::looks_like_valid_path(&path) {
                        files.push(path);
                    }
                }
            }
        }
        files
    }

    fn looks_like_valid_path(path: &str) -> bool {
        if path.len() < 4 {
            return false;
        }
        if path.contains('\n') || path.contains('\r') || path.contains('\t') {
            return false;
        }
        if path.starts_with('\"')
            || path.starts_with('\'')
            || path.ends_with('\"')
            || path.ends_with('\'')
        {
            return false;
        }
        if path.contains("\\n") || path.contains("\\r") || path.contains("\\t") {
            return false;
        }
        let ext = path.split('.').next_back().unwrap_or("");
        matches!(
            ext,
            "rs" | "go"
                | "py"
                | "ts"
                | "js"
                | "tsx"
                | "jsx"
                | "java"
                | "c"
                | "cpp"
                | "h"
                | "hpp"
                | "cs"
                | "rb"
                | "swift"
                | "kt"
                | "scala"
                | "yaml"
                | "yml"
                | "toml"
                | "json"
                | "xml"
                | "md"
                | "txt"
                | "sh"
                | "bash"
                | "zsh"
                | "sql"
                | "html"
                | "css"
                | "scss"
        )
    }
}
