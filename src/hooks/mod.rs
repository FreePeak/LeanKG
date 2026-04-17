use std::path::PathBuf;
use std::process::Command;

#[derive(Debug, Clone)]
pub struct StagedChange {
    pub path: PathBuf,
    pub status: ChangeStatus,
}

#[derive(Debug, Clone)]
pub enum ChangeStatus {
    Added,
    Modified,
    Deleted,
    Renamed,
}

#[derive(Debug, Clone)]
pub enum HookRecommendation {
    Allow(String),
    Warn(String, Vec<String>),
    Block(String, Vec<String>),
}

#[derive(Debug, Clone)]
pub struct IndexStatus {
    pub is_stale: bool,
    pub last_indexed_commit: Option<String>,
    pub current_commit: String,
    pub affected_files: Vec<PathBuf>,
}

pub struct GitHooks {
    project_path: PathBuf,
}

impl GitHooks {
    pub fn new(project_path: PathBuf) -> Self {
        Self { project_path }
    }

    pub fn install_pre_commit(&self) -> Result<(), HookError> {
        let hooks_dir = self.project_path.join(".git").join("hooks");
        std::fs::create_dir_all(&hooks_dir).map_err(|e| HookError::Io(e.to_string()))?;

        let pre_commit_path = hooks_dir.join("pre-commit");
        let script = self.generate_pre_commit_script()?;

        if pre_commit_path.exists() {
            let existing = std::fs::read_to_string(&pre_commit_path)
                .map_err(|e| HookError::Io(e.to_string()))?;

            if existing.contains("LeanKG") {
                return Err(HookError::AlreadyInstalled(
                    "pre-commit hook already installed by LeanKG".to_string(),
                ));
            }

            let backup_path = hooks_dir.join("pre-commit.leankg.backup");
            std::fs::write(&backup_path, &existing).map_err(|e| HookError::Io(e.to_string()))?;
        }

        std::fs::write(&pre_commit_path, script).map_err(|e| HookError::Io(e.to_string()))?;

        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mut perms = std::fs::metadata(&pre_commit_path)
                .map_err(|e| HookError::Io(e.to_string()))?
                .permissions();
            perms.set_mode(0o755);
            std::fs::set_permissions(&pre_commit_path, perms)
                .map_err(|e| HookError::Io(e.to_string()))?;
        }

        println!(
            "Installed LeanKG pre-commit hook at {}",
            pre_commit_path.display()
        );
        Ok(())
    }

    pub fn install_post_commit(&self) -> Result<(), HookError> {
        let hooks_dir = self.project_path.join(".git").join("hooks");
        std::fs::create_dir_all(&hooks_dir).map_err(|e| HookError::Io(e.to_string()))?;

        let post_commit_path = hooks_dir.join("post-commit");
        let script = self.generate_post_commit_script()?;

        if post_commit_path.exists() {
            let existing = std::fs::read_to_string(&post_commit_path)
                .map_err(|e| HookError::Io(e.to_string()))?;

            if existing.contains("LeanKG") {
                return Err(HookError::AlreadyInstalled(
                    "post-commit hook already installed by LeanKG".to_string(),
                ));
            }

            let backup_path = hooks_dir.join("post-commit.leankg.backup");
            std::fs::write(&backup_path, &existing).map_err(|e| HookError::Io(e.to_string()))?;
        }

        std::fs::write(&post_commit_path, script).map_err(|e| HookError::Io(e.to_string()))?;

        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mut perms = std::fs::metadata(&post_commit_path)
                .map_err(|e| HookError::Io(e.to_string()))?
                .permissions();
            perms.set_mode(0o755);
            std::fs::set_permissions(&post_commit_path, perms)
                .map_err(|e| HookError::Io(e.to_string()))?;
        }

        println!(
            "Installed LeanKG post-commit hook at {}",
            post_commit_path.display()
        );
        Ok(())
    }

    pub fn install_post_checkout(&self) -> Result<(), HookError> {
        let hooks_dir = self.project_path.join(".git").join("hooks");
        std::fs::create_dir_all(&hooks_dir).map_err(|e| HookError::Io(e.to_string()))?;

        let post_checkout_path = hooks_dir.join("post-checkout");
        let script = self.generate_post_checkout_script()?;

        if post_checkout_path.exists() {
            let existing = std::fs::read_to_string(&post_checkout_path)
                .map_err(|e| HookError::Io(e.to_string()))?;

            if existing.contains("LeanKG") {
                return Err(HookError::AlreadyInstalled(
                    "post-checkout hook already installed by LeanKG".to_string(),
                ));
            }

            let backup_path = hooks_dir.join("post-checkout.leankg.backup");
            std::fs::write(&backup_path, &existing).map_err(|e| HookError::Io(e.to_string()))?;
        }

        std::fs::write(&post_checkout_path, script).map_err(|e| HookError::Io(e.to_string()))?;

        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mut perms = std::fs::metadata(&post_checkout_path)
                .map_err(|e| HookError::Io(e.to_string()))?
                .permissions();
            perms.set_mode(0o755);
            std::fs::set_permissions(&post_checkout_path, perms)
                .map_err(|e| HookError::Io(e.to_string()))?;
        }

        println!(
            "Installed LeanKG post-checkout hook at {}",
            post_checkout_path.display()
        );
        Ok(())
    }

    pub fn uninstall_hooks(&self) -> Result<(), HookError> {
        let hooks_dir = self.project_path.join(".git").join("hooks");

        for hook_name in ["pre-commit", "post-commit", "post-checkout"] {
            let hook_path = hooks_dir.join(hook_name);
            if hook_path.exists() {
                let content = std::fs::read_to_string(&hook_path)
                    .map_err(|e| HookError::Io(e.to_string()))?;

                if content.contains("LeanKG") {
                    std::fs::remove_file(&hook_path).map_err(|e| HookError::Io(e.to_string()))?;
                    println!("Removed LeanKG {} hook", hook_name);
                }

                let backup_path = hooks_dir.join(format!("{}.leankg.backup", hook_name));
                if backup_path.exists() {
                    std::fs::rename(&backup_path, &hook_path)
                        .map_err(|e| HookError::Io(e.to_string()))?;
                    println!("Restored original {} hook from backup", hook_name);
                }
            }
        }

        Ok(())
    }

    pub fn check_hooks_status(&self) -> Result<HooksStatus, HookError> {
        let hooks_dir = self.project_path.join(".git").join("hooks");

        let mut status = HooksStatus {
            pre_commit_installed: false,
            post_commit_installed: false,
            post_checkout_installed: false,
            pre_commit_backup_exists: false,
            post_commit_backup_exists: false,
            post_checkout_backup_exists: false,
        };

        for hook_name in ["pre-commit", "post-commit", "post-checkout"] {
            let hook_path = hooks_dir.join(hook_name);
            let backup_path = hooks_dir.join(format!("{}.leankg.backup", hook_name));

            if hook_path.exists() {
                let content = std::fs::read_to_string(&hook_path)
                    .map_err(|e| HookError::Io(e.to_string()))?;

                match hook_name {
                    "pre-commit" => status.pre_commit_installed = content.contains("LeanKG"),
                    "post-commit" => status.post_commit_installed = content.contains("LeanKG"),
                    "post-checkout" => status.post_checkout_installed = content.contains("LeanKG"),
                    _ => {}
                }
            }

            match hook_name {
                "pre-commit" => status.pre_commit_backup_exists = backup_path.exists(),
                "post-commit" => status.post_commit_backup_exists = backup_path.exists(),
                "post-checkout" => status.post_checkout_backup_exists = backup_path.exists(),
                _ => {}
            }
        }

        Ok(status)
    }

    pub fn detect_staged_changes(&self) -> Result<Vec<StagedChange>, HookError> {
        let output = Command::new("git")
            .args(["diff", "--cached", "--name-status"])
            .current_dir(&self.project_path)
            .output()
            .map_err(|e| HookError::Git(e.to_string()))?;

        if !output.status.success() {
            return Err(HookError::Git(
                String::from_utf8_lossy(&output.stderr).to_string(),
            ));
        }

        let mut changes = Vec::new();
        for line in String::from_utf8_lossy(&output.stdout).lines() {
            let parts: Vec<&str> = line.split('\t').collect();
            if parts.len() >= 2 {
                let status = match parts[0] {
                    "A" => ChangeStatus::Added,
                    "M" => ChangeStatus::Modified,
                    "D" => ChangeStatus::Deleted,
                    "R" => ChangeStatus::Renamed,
                    _ => continue,
                };
                changes.push(StagedChange {
                    path: PathBuf::from(parts[1]),
                    status,
                });
            }
        }

        Ok(changes)
    }

    pub fn check_critical_files(
        &self,
        changes: &[StagedChange],
    ) -> Result<HookRecommendation, HookError> {
        let critical_patterns = ["src/lib.rs", "src/main.rs", "Cargo.toml", "leankg.yaml"];

        let mut critical_changes = Vec::new();
        let mut warnings = Vec::new();

        for change in changes {
            let path_str = change.path.to_string_lossy();
            for pattern in &critical_patterns {
                if path_str.contains(pattern) {
                    critical_changes.push(path_str.to_string());
                    break;
                }
            }

            if path_str.contains("src/hooks/") || path_str.contains("src/watcher/") {
                warnings.push(format!(
                    "{}: hook/watcher changes may affect auto-indexing",
                    path_str
                ));
            }
        }

        if !critical_changes.is_empty() {
            Ok(HookRecommendation::Block(
                format!("Critical files modified: {}", critical_changes.join(", ")),
                critical_changes,
            ))
        } else if !warnings.is_empty() {
            Ok(HookRecommendation::Warn(
                "Potential issues detected".to_string(),
                warnings,
            ))
        } else {
            Ok(HookRecommendation::Allow(
                "No critical files affected".to_string(),
            ))
        }
    }

    fn generate_pre_commit_script(&self) -> Result<String, HookError> {
        Ok(r#"#!/bin/sh
# LeanKG pre-commit hook
# Generated by LeanKG

# Run leankg detect-changes on staged files
STAGED_FILES=$(git diff --cached --name-only)
if [ -n "$STAGED_FILES" ]; then
    echo "LeanKG: Checking staged changes..."
    # Run detect-changes if available
    if command -v leankg >/dev/null 2>&1; then
        # Check for critical files
        CRITICAL_FILES="src/lib.rs src/main.rs Cargo.toml"
        for file in $STAGED_FILES; do
            for critical in $CRITICAL_FILES; do
                if echo "$file" | grep -q "$critical"; then
                    echo "LeanKG: WARNING - modifying critical file: $file"
                fi
            done
        done
    fi
fi

# Allow commit to proceed
exit 0
"#
        .to_string())
    }

    fn generate_post_commit_script(&self) -> Result<String, HookError> {
        Ok(r#"#!/bin/sh
# LeanKG post-commit hook
# Generated by LeanKG

# Run incremental index after commit
if command -v leankg >/dev/null 2>&1; then
    leankg index --incremental --quiet 2>/dev/null
    if [ $? -ne 0 ]; then
        echo "LeanKG: WARNING - incremental reindex failed" >&2
    fi
fi

exit 0
"#
        .to_string())
    }

    fn generate_post_checkout_script(&self) -> Result<String, HookError> {
        Ok(r#"#!/bin/sh
# LeanKG post-checkout hook
# Generated by LeanKG

# Run incremental index after branch switch
if command -v leankg >/dev/null 2>&1; then
    leankg index --incremental --quiet 2>/dev/null
    if [ $? -ne 0 ]; then
        echo "LeanKG: WARNING - incremental reindex failed after checkout" >&2
    fi
fi

exit 0
"#
        .to_string())
    }
}

#[derive(Debug, Clone)]
pub struct HooksStatus {
    pub pre_commit_installed: bool,
    pub post_commit_installed: bool,
    pub post_checkout_installed: bool,
    pub pre_commit_backup_exists: bool,
    pub post_commit_backup_exists: bool,
    pub post_checkout_backup_exists: bool,
}

pub struct GitWatcher {
    project_path: PathBuf,
    db_path: PathBuf,
}

impl GitWatcher {
    pub fn new(project_path: PathBuf, db_path: PathBuf) -> Self {
        Self {
            project_path,
            db_path,
        }
    }

    pub fn check_index_status(&self) -> Result<IndexStatus, HookError> {
        let current_commit = self.get_current_commit()?;
        let last_indexed_commit = self.get_last_indexed_commit()?;

        let is_stale = last_indexed_commit
            .as_ref()
            .map(|last| last != &current_commit)
            .unwrap_or(true);

        let affected_files = if is_stale {
            self.get_changed_files_since_index()?
        } else {
            Vec::new()
        };

        Ok(IndexStatus {
            is_stale,
            last_indexed_commit,
            current_commit,
            affected_files,
        })
    }

    pub fn sync_on_branch_change(&self, new_branch: &str) -> Result<(), HookError> {
        println!("Branch changed to: {}", new_branch);

        let status = self.check_index_status()?;
        if status.is_stale {
            println!("Index is stale, syncing...");
            self.run_incremental_index()?;
        } else {
            println!("Index is up to date");
        }

        Ok(())
    }

    pub fn run_incremental_index(&self) -> Result<(), HookError> {
        let output = Command::new("leankg")
            .args(["index", "--incremental"])
            .current_dir(&self.project_path)
            .output()
            .map_err(|e| HookError::Io(e.to_string()))?;

        if !output.status.success() {
            return Err(HookError::HookExecution(format!(
                "Incremental index failed: {}",
                String::from_utf8_lossy(&output.stderr)
            )));
        }

        self.update_last_indexed_commit()?;

        Ok(())
    }

    fn get_current_commit(&self) -> Result<String, HookError> {
        let output = Command::new("git")
            .args(["rev-parse", "HEAD"])
            .current_dir(&self.project_path)
            .output()
            .map_err(|e| HookError::Git(e.to_string()))?;

        if !output.status.success() {
            return Err(HookError::Git(
                String::from_utf8_lossy(&output.stderr).to_string(),
            ));
        }

        Ok(String::from_utf8_lossy(&output.stdout).trim().to_string())
    }

    fn get_last_indexed_commit(&self) -> Result<Option<String>, HookError> {
        let marker_path = self.db_path.join(".last_indexed_commit");

        if marker_path.exists() {
            let content =
                std::fs::read_to_string(&marker_path).map_err(|e| HookError::Io(e.to_string()))?;
            Ok(Some(content.trim().to_string()))
        } else {
            Ok(None)
        }
    }

    fn update_last_indexed_commit(&self) -> Result<(), HookError> {
        let commit = self.get_current_commit()?;
        let marker_path = self.db_path.join(".last_indexed_commit");

        std::fs::write(&marker_path, &commit).map_err(|e| HookError::Io(e.to_string()))?;

        Ok(())
    }

    fn get_changed_files_since_index(&self) -> Result<Vec<PathBuf>, HookError> {
        let last_commit = self
            .get_last_indexed_commit()?
            .ok_or(HookError::HookExecution(
                "No previous commit found".to_string(),
            ))?;

        let output = Command::new("git")
            .args(["diff", "--name-only", &last_commit, "HEAD"])
            .current_dir(&self.project_path)
            .output()
            .map_err(|e| HookError::Git(e.to_string()))?;

        if !output.status.success() {
            return Err(HookError::Git(
                String::from_utf8_lossy(&output.stderr).to_string(),
            ));
        }

        let files: Vec<PathBuf> = String::from_utf8_lossy(&output.stdout)
            .lines()
            .filter(|line| !line.is_empty())
            .map(PathBuf::from)
            .collect();

        Ok(files)
    }
}

#[derive(Debug)]
pub enum HookError {
    Io(String),
    Git(String),
    AlreadyInstalled(String),
    HookExecution(String),
}

impl std::fmt::Display for HookError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            HookError::Io(msg) => write!(f, "IO error: {}", msg),
            HookError::Git(msg) => write!(f, "Git error: {}", msg),
            HookError::AlreadyInstalled(msg) => write!(f, "Hook already installed: {}", msg),
            HookError::HookExecution(msg) => write!(f, "Hook execution error: {}", msg),
        }
    }
}

impl std::error::Error for HookError {}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_githooks_new() {
        let path = PathBuf::from("/test/path");
        let hooks = GitHooks::new(path.clone());
        assert_eq!(hooks.project_path, path);
    }

    #[test]
    fn test_gitwatcher_new() {
        let project = PathBuf::from("/test/project");
        let db = PathBuf::from("/test/project/.leankg");
        let watcher = GitWatcher::new(project.clone(), db.clone());
        assert_eq!(watcher.project_path, project);
        assert_eq!(watcher.db_path, db);
    }

    #[test]
    fn test_hook_error_display() {
        let err = HookError::Io("test error".to_string());
        assert_eq!(format!("{}", err), "IO error: test error");

        let err = HookError::Git("git error".to_string());
        assert_eq!(format!("{}", err), "Git error: git error");
    }

    #[test]
    fn test_staged_change_status() {
        let change = StagedChange {
            path: PathBuf::from("test.rs"),
            status: ChangeStatus::Modified,
        };
        assert!(matches!(change.status, ChangeStatus::Modified));
    }

    #[test]
    fn test_hooks_status_defaults() {
        let status = HooksStatus {
            pre_commit_installed: false,
            post_commit_installed: false,
            post_checkout_installed: false,
            pre_commit_backup_exists: false,
            post_commit_backup_exists: false,
            post_checkout_backup_exists: false,
        };
        assert!(!status.pre_commit_installed);
    }
}
