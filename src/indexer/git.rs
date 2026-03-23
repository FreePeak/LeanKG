use std::path::Path;
use std::process::Command;

#[derive(Debug, Clone)]
pub struct GitChangedFiles {
    pub modified: Vec<String>,
    pub added: Vec<String>,
    pub deleted: Vec<String>,
}

pub struct GitAnalyzer;

impl GitAnalyzer {
    pub fn get_changed_files(since: &str) -> Result<GitChangedFiles, Box<dyn std::error::Error>> {
        let output = Command::new("git")
            .args(["diff", "--name-status", &format!("{}...HEAD", since)])
            .output()?;

        if !output.status.success() {
            let output = Command::new("git")
                .args(["diff", "--name-status", "HEAD"])
                .output()?;

            if !output.status.success() {
                return Ok(GitChangedFiles {
                    modified: Vec::new(),
                    added: Vec::new(),
                    deleted: Vec::new(),
                });
            }

            return Ok(Self::parse_git_status(&String::from_utf8_lossy(
                &output.stdout,
            )));
        }

        Ok(Self::parse_git_status(&String::from_utf8_lossy(
            &output.stdout,
        )))
    }

    pub fn get_changed_files_since_last_commit(
    ) -> Result<GitChangedFiles, Box<dyn std::error::Error>> {
        let output = Command::new("git")
            .args(["diff", "--name-status", "HEAD"])
            .output()?;

        if !output.status.success() {
            return Ok(GitChangedFiles {
                modified: Vec::new(),
                added: Vec::new(),
                deleted: Vec::new(),
            });
        }

        Ok(Self::parse_git_status(&String::from_utf8_lossy(
            &output.stdout,
        )))
    }

    pub fn get_staged_files() -> Result<Vec<String>, Box<dyn std::error::Error>> {
        let output = Command::new("git")
            .args(["diff", "--name-status", "--cached"])
            .output()?;

        if !output.status.success() {
            return Ok(Vec::new());
        }

        let status = String::from_utf8_lossy(&output.stdout);
        let files = status
            .lines()
            .filter_map(|line| {
                let parts: Vec<&str> = line.split('\t').collect();
                if parts.len() >= 2 {
                    Some(parts[1].to_string())
                } else {
                    None
                }
            })
            .collect();

        Ok(files)
    }

    pub fn get_untracked_files() -> Result<Vec<String>, Box<dyn std::error::Error>> {
        let output = Command::new("git")
            .args(["ls_files", "--others", "--exclude-standard"])
            .output()?;

        if !output.status.success() {
            return Ok(Vec::new());
        }

        let files: Vec<String> = String::from_utf8_lossy(&output.stdout)
            .lines()
            .map(String::from)
            .collect();

        Ok(files)
    }

    fn parse_git_status(status: &str) -> GitChangedFiles {
        let mut modified = Vec::new();
        let mut added = Vec::new();
        let mut deleted = Vec::new();

        for line in status.lines() {
            let parts: Vec<&str> = line.split('\t').collect();
            if parts.len() >= 2 {
                let status_char = parts[0];
                let file = parts[1].to_string();

                match status_char {
                    "M" | "mm" => modified.push(file),
                    "A" | "am" => added.push(file),
                    "D" => deleted.push(file),
                    _ => modified.push(file),
                }
            }
        }

        GitChangedFiles {
            modified,
            added,
            deleted,
        }
    }

    pub fn is_git_repo() -> bool {
        Command::new("git")
            .args(["rev-parse", "--is-inside-work-tree"])
            .output()
            .map(|o| o.status.success())
            .unwrap_or(false)
    }

    pub fn get_repo_root() -> Option<String> {
        let output = Command::new("git")
            .args(["rev-parse", "--show-toplevel"])
            .output()
            .ok()?;

        if output.status.success() {
            Some(String::from_utf8_lossy(&output.stdout).trim().to_string())
        } else {
            None
        }
    }
}

pub fn find_dependents(target_file: &str, all_relationships: &[(String, String)]) -> Vec<String> {
    let mut dependents = Vec::new();
    let target_normalized = target_file.replace('\\', "/");

    for (source, target) in all_relationships {
        let target_norm = target.replace('\\', "/");
        if (target_norm == target_normalized || target_norm.ends_with(&target_normalized))
            && !dependents.contains(source)
        {
            dependents.push(source.clone());
        }
    }

    dependents
}

pub fn filter_indexable_files(files: &[String]) -> Vec<String> {
    let extensions = ["go", "ts", "js", "py"];

    files
        .iter()
        .filter(|f| {
            if let Some(ext) = Path::new(f).extension() {
                if let Some(ext_str) = ext.to_str() {
                    return extensions.contains(&ext_str);
                }
            }
            false
        })
        .cloned()
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_git_status() {
        let input = "M\tfile1.go\nA\tfile2.go\nD\tfile3.go";
        let result = GitAnalyzer::parse_git_status(input);

        assert_eq!(result.modified, vec!["file1.go"]);
        assert_eq!(result.added, vec!["file2.go"]);
        assert_eq!(result.deleted, vec!["file3.go"]);
    }

    #[test]
    fn test_filter_indexable_files() {
        let files = vec![
            "src/main.go".to_string(),
            "src/app.ts".to_string(),
            "readme.md".to_string(),
            "lib.py".to_string(),
        ];

        let filtered = filter_indexable_files(&files);
        assert_eq!(filtered.len(), 3);
        assert!(filtered.contains(&"src/main.go".to_string()));
        assert!(filtered.contains(&"src/app.ts".to_string()));
        assert!(filtered.contains(&"lib.py".to_string()));
    }

    #[test]
    fn test_find_dependents() {
        let relationships = vec![
            ("a.go".to_string(), "b.go".to_string()),
            ("c.go".to_string(), "b.go".to_string()),
            ("d.go".to_string(), "e.go".to_string()),
        ];

        let dependents = find_dependents("b.go", &relationships);
        assert_eq!(dependents.len(), 2);
        assert!(dependents.contains(&"a.go".to_string()));
        assert!(dependents.contains(&"c.go".to_string()));
    }
}
