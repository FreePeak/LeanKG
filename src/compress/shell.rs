use serde::{Deserialize, Serialize};
use std::collections::HashMap;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum CommandCategory {
    Git,
    Docker,
    Npm,
    Cargo,
    Kubectl,
    GitHub,
    TestRunner,
    Linter,
    Build,
    Python,
    Aws,
    Database,
    Terraform,
    #[allow(dead_code)]
    Other,
}

impl CommandCategory {
    pub fn from_command(cmd: &str) -> Option<Self> {
        let cmd_lower = cmd.to_lowercase();
        if cmd_lower.contains("git")
            || cmd_lower.starts_with("g")
            || [
                "status", "log", "diff", "commit", "push", "pull", "branch", "checkout",
            ]
            .iter()
            .any(|c| cmd_lower.contains(c))
        {
            Some(CommandCategory::Git)
        } else if cmd_lower.contains("docker") || cmd_lower.starts_with("d") {
            Some(CommandCategory::Docker)
        } else if cmd_lower.contains("npm")
            || cmd_lower.contains("pnpm")
            || cmd_lower.contains("yarn")
        {
            Some(CommandCategory::Npm)
        } else if cmd_lower.contains("cargo") || cmd_lower.starts_with("c") {
            Some(CommandCategory::Cargo)
        } else if cmd_lower.contains("kubectl") || cmd_lower.starts_with("k") {
            Some(CommandCategory::Kubectl)
        } else if cmd_lower.contains("gh ") || cmd_lower.contains("github") {
            Some(CommandCategory::GitHub)
        } else if ["jest", "vitest", "pytest", "go test", "playwright", "rspec"]
            .iter()
            .any(|c| cmd_lower.contains(c))
        {
            Some(CommandCategory::TestRunner)
        } else if ["eslint", "prettier", "ruff", "clippy"]
            .iter()
            .any(|c| cmd_lower.contains(c))
        {
            Some(CommandCategory::Linter)
        } else if ["tsc", "vite", "next"]
            .iter()
            .any(|c| cmd_lower.contains(c))
        {
            Some(CommandCategory::Build)
        } else if cmd_lower.contains("aws") {
            Some(CommandCategory::Aws)
        } else if cmd_lower.contains("psql") || cmd_lower.contains("mysql") {
            Some(CommandCategory::Database)
        } else if cmd_lower.contains("terraform") || cmd_lower.contains("tf") {
            Some(CommandCategory::Terraform)
        } else if ["python", "pip", "pip3"]
            .iter()
            .any(|c| cmd_lower.contains(c))
        {
            Some(CommandCategory::Python)
        } else {
            None
        }
    }
}

pub struct ShellCompressor {
    patterns: HashMap<CommandCategory, Vec<CompressionPattern>>,
}

#[derive(Debug, Clone)]
pub struct CompressionPattern {
    pub regex: regex::Regex,
    pub replacement: String,
    pub description: &'static str,
}

impl Default for ShellCompressor {
    fn default() -> Self {
        Self::new()
    }
}

impl ShellCompressor {
    pub fn new() -> Self {
        let mut patterns = HashMap::new();

        patterns.insert(
            CommandCategory::Git,
            vec![
                CompressionPattern {
                    regex: regex::Regex::new(r"^On branch .+$").unwrap(),
                    replacement: "→ branch".to_string(),
                    description: "Current branch",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"^Your branch is (up to date|ahead|behind).+$")
                        .unwrap(),
                    replacement: "".to_string(),
                    description: "Branch sync status",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"^Changes (not staged|to be committed):$").unwrap(),
                    replacement: "Changes:".to_string(),
                    description: "Change section header",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"^\s+(modified|new file|deleted):\s+").unwrap(),
                    replacement: "  • ".to_string(),
                    description: "File change indicator",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"^commit [a-f0-9]{7,}$").unwrap(),
                    replacement: "commit @".to_string(),
                    description: "Commit hash",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"\d+ files? changed").unwrap(),
                    replacement: "~ files changed".to_string(),
                    description: "File count",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"\d+ insertions?\(\+\)").unwrap(),
                    replacement: "+ lines".to_string(),
                    description: "Insertions",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"\d+ deletions?\(\-\)").unwrap(),
                    replacement: "- lines".to_string(),
                    description: "Deletions",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"Author: .+$").unwrap(),
                    replacement: "Author: @".to_string(),
                    description: "Author",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"Date: .+$").unwrap(),
                    replacement: "Date: @".to_string(),
                    description: "Date",
                },
            ],
        );

        patterns.insert(
            CommandCategory::Docker,
            vec![
                CompressionPattern {
                    regex: regex::Regex::new(r"^[a-f0-9]{12}$").unwrap(),
                    replacement: "IMAGE_ID".to_string(),
                    description: "Docker image ID",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"^\d+\.\d+\.\d+\s+").unwrap(),
                    replacement: "v".to_string(),
                    description: "Version prefix",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"About a minute ago").unwrap(),
                    replacement: "~1m ago".to_string(),
                    description: "Time ago",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"About an hour ago").unwrap(),
                    replacement: "~1h ago".to_string(),
                    description: "Time ago",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"\d+ hours ago").unwrap(),
                    replacement: "~Nh ago".to_string(),
                    description: "Hours ago",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"Exited \(\d+\)").unwrap(),
                    replacement: "Exited".to_string(),
                    description: "Exit status",
                },
            ],
        );

        patterns.insert(
            CommandCategory::Npm,
            vec![
                CompressionPattern {
                    regex: regex::Regex::new(r"^\s+├──\s+").unwrap(),
                    replacement: "├─ ".to_string(),
                    description: "Tree branch",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"^\s+└──\s+").unwrap(),
                    replacement: "└─ ".to_string(),
                    description: "Tree end",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"^\+--- .+$").unwrap(),
                    replacement: "+--- ".to_string(),
                    description: "Package tree",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"^added \d+ packages? in .+$").unwrap(),
                    replacement: "packages added".to_string(),
                    description: "Packages added",
                },
            ],
        );

        patterns.insert(
            CommandCategory::Cargo,
            vec![
                CompressionPattern {
                    regex: regex::Regex::new(r"^\s+Compiling .+$").unwrap(),
                    replacement: "  compile".to_string(),
                    description: "Compiling",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"^\s+Finished .+$").unwrap(),
                    replacement: "  done".to_string(),
                    description: "Finished",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"^\s+Running .+$").unwrap(),
                    replacement: "  run".to_string(),
                    description: "Running",
                },
            ],
        );

        patterns.insert(
            CommandCategory::Kubectl,
            vec![
                CompressionPattern {
                    regex: regex::Regex::new(r"NAME\s+READY\s+STATUS").unwrap(),
                    replacement: "NAME  RDY  ST".to_string(),
                    description: "K8s header",
                },
                CompressionPattern {
                    regex: regex::Regex::new(r"^\d+/\d+\s+").unwrap(),
                    replacement: "x/y ".to_string(),
                    description: "Ready count",
                },
            ],
        );

        Self { patterns }
    }

    pub fn compress(&self, cmd: &str, output: &str) -> String {
        let category = CommandCategory::from_command(cmd);

        let mut result = output.to_string();

        if let Some(cat) = category {
            if let Some(patterns) = self.patterns.get(&cat) {
                for pattern in patterns {
                    result = pattern
                        .regex
                        .replace_all(&result, pattern.replacement.as_str())
                        .to_string();
                }
            }
        }

        result
            .lines()
            .filter(|line| !line.trim().is_empty())
            .take(50)
            .collect::<Vec<_>>()
            .join("\n")
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_command_category_git() {
        assert_eq!(
            CommandCategory::from_command("git status"),
            Some(CommandCategory::Git)
        );
        assert_eq!(
            CommandCategory::from_command("git log --oneline"),
            Some(CommandCategory::Git)
        );
        assert_eq!(
            CommandCategory::from_command("git diff"),
            Some(CommandCategory::Git)
        );
    }

    #[test]
    fn test_command_category_docker() {
        assert_eq!(
            CommandCategory::from_command("docker ps"),
            Some(CommandCategory::Docker)
        );
        assert_eq!(
            CommandCategory::from_command("docker-compose up"),
            Some(CommandCategory::Docker)
        );
    }

    #[test]
    fn test_shell_compressor() {
        let compressor = ShellCompressor::new();
        let output = "On branch main\nYour branch is up to date with 'origin/main'.\n\nChanges not staged for commit:\n  modified:   src/main.rs\n  modified:   src/lib.rs";

        let compressed = compressor.compress("git status", output);
        assert!(compressed.contains("branch"));
    }
}
