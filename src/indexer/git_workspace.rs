//! Multi-repo workspace helpers for LeanKG auto-index.
//!
//! Some project roots (e.g. a polyrepo mount like `/workspace-be`) are not
//! themselves git repositories but contain many nested repos under platform
//! folders. Auto-index previously required the CWD to be a single git root
//! and skipped those workspaces entirely.

use crate::indexer::git::{GitAnalyzer, GitChangedFiles};
use std::path::{Path, PathBuf};

/// Max directory depth below the workspace root when searching for nested `.git`.
const NESTED_GIT_MAX_DEPTH: usize = 4;

const SKIP_DIR_NAMES: &[&str] = &[
    ".git",
    "node_modules",
    "vendor",
    "target",
    "dist",
    "build",
    ".worktrees",
    ".claude",
    "browser-data",
];

/// True when `root` is a git work tree, or contains nested git repos.
pub fn has_git_context(root: &Path) -> bool {
    GitAnalyzer::is_git_repo_at(root) || !discover_nested_git_repos(root).is_empty()
}

/// Discover nested git repository roots under `root` (bounded depth).
///
/// A directory is treated as a repo root when it contains a `.git` directory
/// or a `.git` file (worktree / submodule gitfile). Once a repo is found, that
/// tree is not walked further.
pub fn discover_nested_git_repos(root: &Path) -> Vec<PathBuf> {
    let mut repos = Vec::new();
    if !root.is_dir() {
        return repos;
    }

    // Root itself may be a repo; callers usually check that first, but include
    // it for completeness when scanning from a parent.
    if GitAnalyzer::is_git_repo_at(root) {
        repos.push(root.to_path_buf());
        return repos;
    }

    let mut stack: Vec<(PathBuf, usize)> = vec![(root.to_path_buf(), 0)];
    while let Some((dir, depth)) = stack.pop() {
        if depth >= NESTED_GIT_MAX_DEPTH {
            continue;
        }
        let entries = match std::fs::read_dir(&dir) {
            Ok(e) => e,
            Err(_) => continue,
        };
        for entry in entries.flatten() {
            let path = entry.path();
            let file_type = match entry.file_type() {
                Ok(ft) => ft,
                Err(_) => continue,
            };
            if !file_type.is_dir() {
                continue;
            }
            let name = entry.file_name();
            let name_str = name.to_string_lossy();
            if SKIP_DIR_NAMES.iter().any(|s| *s == name_str.as_ref()) {
                continue;
            }

            let git_marker = path.join(".git");
            if git_marker.exists() {
                // Nested repo found — record and do not descend.
                repos.push(path);
                continue;
            }
            stack.push((path, depth + 1));
        }
    }

    repos.sort();
    repos
}

/// Latest HEAD commit timestamp across the root repo or all nested repos.
pub fn workspace_last_commit_time(root: &Path) -> Result<i64, Box<dyn std::error::Error>> {
    if GitAnalyzer::is_git_repo_at(root) {
        return GitAnalyzer::get_last_commit_time_at(root);
    }

    let repos = discover_nested_git_repos(root);
    if repos.is_empty() {
        return Err("No git repository found at workspace root or nested paths".into());
    }

    let mut max_ts: i64 = 0;
    let mut any_ok = false;
    for repo in &repos {
        match GitAnalyzer::get_last_commit_time_at(repo) {
            Ok(ts) => {
                any_ok = true;
                max_ts = max_ts.max(ts);
            }
            Err(e) => {
                tracing::debug!(
                    "Skipping nested repo {} for commit time: {}",
                    repo.display(),
                    e
                );
            }
        }
    }

    if !any_ok {
        return Err("Failed to read commit time from any nested git repo".into());
    }
    Ok(max_ts)
}

/// Aggregate working-tree changes from every nested repo, paths relative to `root`.
pub fn workspace_changed_files(root: &Path) -> Result<GitChangedFiles, Box<dyn std::error::Error>> {
    if GitAnalyzer::is_git_repo_at(root) {
        return GitAnalyzer::get_changed_files_since_last_commit_at(root);
    }

    let repos = discover_nested_git_repos(root);
    if repos.is_empty() {
        return Err("No git repository found for incremental indexing".into());
    }

    let mut modified = Vec::new();
    let mut added = Vec::new();
    let mut deleted = Vec::new();

    for repo in &repos {
        let prefix = relative_prefix(root, repo);
        let changed = GitAnalyzer::get_changed_files_since_last_commit_at(repo)?;
        modified.extend(prefix_paths(&prefix, changed.modified));
        added.extend(prefix_paths(&prefix, changed.added));
        deleted.extend(prefix_paths(&prefix, changed.deleted));
    }

    Ok(GitChangedFiles {
        modified,
        added,
        deleted,
    })
}

/// Aggregate untracked files from every nested repo, paths relative to `root`.
pub fn workspace_untracked_files(root: &Path) -> Result<Vec<String>, Box<dyn std::error::Error>> {
    if GitAnalyzer::is_git_repo_at(root) {
        return GitAnalyzer::get_untracked_files_at(root);
    }

    let repos = discover_nested_git_repos(root);
    let mut out = Vec::new();
    for repo in &repos {
        let prefix = relative_prefix(root, repo);
        let files = GitAnalyzer::get_untracked_files_at(repo)?;
        out.extend(prefix_paths(&prefix, files));
    }
    Ok(out)
}

fn relative_prefix(root: &Path, repo: &Path) -> String {
    repo.strip_prefix(root)
        .map(|p| p.to_string_lossy().replace('\\', "/"))
        .unwrap_or_default()
}

fn prefix_paths(prefix: &str, files: Vec<String>) -> Vec<String> {
    if prefix.is_empty() {
        return files;
    }
    files
        .into_iter()
        .map(|f| format!("{}/{}", prefix, f.replace('\\', "/")))
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::process::Command;

    fn init_repo(path: &Path) {
        std::fs::create_dir_all(path).unwrap();
        assert!(Command::new("git")
            .args(["init"])
            .current_dir(path)
            .status()
            .unwrap()
            .success());
        // Identity required for commit on some CI images.
        let _ = Command::new("git")
            .args(["config", "user.email", "test@example.com"])
            .current_dir(path)
            .status();
        let _ = Command::new("git")
            .args(["config", "user.name", "test"])
            .current_dir(path)
            .status();
        std::fs::write(path.join("main.go"), "package main\n").unwrap();
        assert!(Command::new("git")
            .args(["add", "main.go"])
            .current_dir(path)
            .status()
            .unwrap()
            .success());
        assert!(Command::new("git")
            .args(["commit", "-m", "init"])
            .current_dir(path)
            .status()
            .unwrap()
            .success());
    }

    #[test]
    fn has_git_context_false_for_empty_dir() {
        let dir = tempfile::tempdir().unwrap();
        assert!(!has_git_context(dir.path()));
    }

    #[test]
    fn has_git_context_true_for_root_repo() {
        let dir = tempfile::tempdir().unwrap();
        init_repo(dir.path());
        assert!(has_git_context(dir.path()));
    }

    #[test]
    fn discovers_nested_repos_under_platform_layout() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        let nested_a = root.join("platform-food").join("be-restaurant");
        let nested_b = root.join("platform-core").join("be-autos");
        init_repo(&nested_a);
        init_repo(&nested_b);
        // Non-git platform folder should not count.
        std::fs::create_dir_all(root.join("docs")).unwrap();

        let repos = discover_nested_git_repos(root);
        assert_eq!(repos.len(), 2);
        assert!(has_git_context(root));
        assert!(!GitAnalyzer::is_git_repo_at(root));

        let ts = workspace_last_commit_time(root).unwrap();
        assert!(ts > 0);
    }

    #[test]
    fn workspace_changed_files_prefixes_nested_paths() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        let nested = root.join("platform-food").join("be-mailer");
        init_repo(&nested);
        std::fs::write(nested.join("extra.go"), "package main\n").unwrap();

        let changed = workspace_changed_files(root).unwrap();
        let untracked = workspace_untracked_files(root).unwrap();
        assert!(
            untracked
                .iter()
                .any(|f| f == "platform-food/be-mailer/extra.go"),
            "untracked={:?}",
            untracked
        );
        // dirty working tree may also appear as untracked only; ensure no panic.
        let _ = changed;
    }
}
