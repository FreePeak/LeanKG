//! Server-side setup pipeline: clone repos -> index -> embed.
//!
//! Pure Rust, no shell script dependency. The repo list comes from
//! `LEANKG_PROJECT_DIRS` (comma-separated, already-mounted dirs -> skip
//! clone) or `LEANKG_REPOS` (comma-separated `host/namespace` paths to
//! clone). Clone/pull goes through the `git` CLI as a subprocess (git is a
//! system binary); indexing reuses the `leankg index` CLI and embedding
//! reuses the `leankg embed --wait` path.
//!
//! Trigger: `leankg setup --clone --index --embed` from the CLI, or
//! `LEANKG_SETUP=1` on `leankg mcp-http` (runs once after the server binds).

use std::path::{Path, PathBuf};
use std::process::Command;

/// Git host for clone URLs (override via LEANKG_GIT_HOST).
pub fn git_host() -> String {
    std::env::var("LEANKG_GIT_HOST")
        .ok()
        .filter(|v| !v.trim().is_empty())
        .unwrap_or_else(|| "github.com".to_string())
}

/// The default clone root. Override via LEANKG_CLONE_ROOT or CLONE_ROOT;
/// otherwise the current working directory.
pub fn clone_root() -> PathBuf {
    std::env::var("LEANKG_CLONE_ROOT")
        .ok()
        .or_else(|| std::env::var("CLONE_ROOT").ok())
        .filter(|v| !v.trim().is_empty())
        .map(PathBuf::from)
        .unwrap_or_else(|| std::env::current_dir().unwrap_or_else(|_| PathBuf::from("/app")))
}

/// The git ref to clone/pull. Override via LEANKG_GIT_REF or GIT_REF.
pub fn git_ref() -> String {
    std::env::var("LEANKG_GIT_REF")
        .ok()
        .or_else(|| std::env::var("GIT_REF").ok())
        .filter(|v| !v.trim().is_empty())
        .unwrap_or_else(|| "main".to_string())
}

/// Resolve the git access token from env.
fn git_token() -> Option<String> {
    if let Ok(t) = std::env::var("GITLAB_TOKEN") {
        if !t.trim().is_empty() {
            return Some(t);
        }
    }
    if let Ok(t) = std::env::var("GIT_TOKEN") {
        if !t.trim().is_empty() {
            return Some(t);
        }
    }
    std::env::var("GITHUB_TOKEN")
        .ok()
        .filter(|t| !t.trim().is_empty())
}

/// A resolved repo: display name, clone URL, and local clone destination.
#[derive(Debug, Clone)]
pub struct RepoSpec {
    pub name: String,
    pub url: String,
    pub dest: PathBuf,
}

/// Recursively find every git repo (a dir containing `.git`) under `root`,
/// walking at most `max_depth` levels. Used by [`resolve_repos`] when
/// `LEANKG_WORKSPACE_DIR` is set: the workspace is a monorepo of nested git
/// repos, and per-project index/embed wants each repo as its own project.
fn discover_git_repos(root: &Path, max_depth: usize) -> Vec<PathBuf> {
    fn walk(dir: &Path, depth: usize, max_depth: usize, out: &mut Vec<PathBuf>) {
        if depth > max_depth {
            return;
        }
        let Ok(entries) = std::fs::read_dir(dir) else {
            return;
        };
        for entry in entries.flatten() {
            let path = entry.path();
            // Skip hidden dirs and the workspace's own `.leankg` caches.
            if let Some(name) = path.file_name().and_then(|n| n.to_str()) {
                if name.starts_with('.') || name == "target" || name == "node_modules" {
                    continue;
                }
            }
            if path.is_dir() {
                if path.join(".git").exists() {
                    out.push(path);
                } else {
                    walk(&path, depth + 1, max_depth, out);
                }
            }
        }
    }
    let mut out = Vec::new();
    walk(root, 0, max_depth, &mut out);
    out.sort();
    out
}

/// Resolve the repo list.
///
/// Discovery precedence:
/// 1. `LEANKG_WORKSPACE_DIR` (a monorepo dir) → every nested git repo found
///    by walking up to `LEANKG_WORKSPACE_MAX_DEPTH` (default 3).
/// 2. `LEANKG_PROJECT_DIRS` (comma-separated mounted dirs) → returned as-is
///    (skip clone).
/// 3. Otherwise `LEANKG_REPOS` (comma-separated `host/namespace` paths, e.g.
///    `github.com/org/repo`) drives the clone list, with each repo cloned to
///    `<clone_root>/<namespace>`.
pub fn resolve_repos() -> Result<Vec<RepoSpec>, Box<dyn std::error::Error>> {
    if let Ok(ws) = std::env::var("LEANKG_WORKSPACE_DIR") {
        let ws = PathBuf::from(ws);
        if ws.is_dir() {
            let max_depth = std::env::var("LEANKG_WORKSPACE_MAX_DEPTH")
                .ok()
                .and_then(|v| v.parse::<usize>().ok())
                .unwrap_or(3);
            let repos = discover_git_repos(&ws, max_depth);
            let specs: Vec<RepoSpec> = repos
                .iter()
                .map(|dest| {
                    let name = dest
                        .file_name()
                        .map(|n| n.to_string_lossy().to_string())
                        .unwrap_or_else(|| dest.display().to_string());
                    RepoSpec {
                        name,
                        url: String::new(), // mounted workspace repos are not cloned
                        dest: dest.clone(),
                    }
                })
                .collect();
            println!(
                "Workspace {}: discovered {} git repos (depth <= {max_depth})",
                ws.display(),
                specs.len()
            );
            return Ok(specs);
        }
        println!(
            "WARN: LEANKG_WORKSPACE_DIR {} is not a directory; falling through",
            ws.display()
        );
    }

    if let Ok(dirs) = std::env::var("LEANKG_PROJECT_DIRS") {
        let root = clone_root();
        let specs: Vec<RepoSpec> = dirs
            .split(',')
            .map(str::trim)
            .filter(|s| !s.is_empty())
            .map(|d| {
                let dest = PathBuf::from(d);
                let name = dest
                    .file_name()
                    .map(|n| n.to_string_lossy().to_string())
                    .unwrap_or_else(|| d.to_string());
                RepoSpec {
                    name,
                    url: String::new(), // mounted dirs are not cloned
                    dest,
                }
            })
            .collect();
        if !specs.is_empty() {
            return Ok(specs);
        }
    }

    let host = git_host();
    let root = clone_root();
    let mut specs = Vec::new();
    if let Ok(repos) = std::env::var("LEANKG_REPOS") {
        for entry in repos.split(',').map(str::trim).filter(|s| !s.is_empty()) {
            let ns: String = if entry.contains('/') {
                let (o, rest) = entry.split_once('/').expect("split_once");
                let h = if o.contains('.') { o } else { host.as_str() };
                format!("{}/{}", h, rest)
            } else {
                let owner =
                    std::env::var("LEANKG_GIT_OWNER").unwrap_or_else(|_| "user".to_string());
                format!("{}/{}", host, owner) + "/" + entry
            };
            specs.push(RepoSpec::new(entry, &format!("https://{}", ns), &root, &ns));
        }
    }
    Ok(specs)
}

impl RepoSpec {
    fn new(name: &str, url: &str, root: &Path, subpath: &str) -> Self {
        Self {
            name: name.to_string(),
            url: url.to_string(),
            dest: root.join(subpath),
        }
    }
}

/// Clone or pull every repo into [`clone_root`]. Returns the list of dirs.
pub fn clone_repos(specs: &[RepoSpec]) -> Result<Vec<PathBuf>, Box<dyn std::error::Error>> {
    let token = git_token().ok_or("no git token: set GITLAB_TOKEN, GIT_TOKEN, or GITHUB_TOKEN")?;
    let ref_name = git_ref();
    let mut dirs = Vec::with_capacity(specs.len());

    for spec in specs {
        // LEANKG_PROJECT_DIRS mode: dirs already exist on disk, nothing to clone.
        if spec.dest.exists() && spec.dest.join(".git").exists() {
            fetch_and_checkout(&spec.dest, &ref_name)?;
            dirs.push(spec.dest.clone());
            continue;
        }

        let url = spec
            .url
            .replace("https://", &format!("https://oauth2:{}@", token));
        println!(
            "Cloning {} -> {} (ref={ref_name})",
            spec.url,
            spec.dest.display()
        );
        let parent = spec
            .dest
            .parent()
            .ok_or_else(|| format!("invalid dest: {}", spec.dest.display()))?;
        std::fs::create_dir_all(parent)?;

        let status = Command::new("git")
            .args(["clone", "--depth", "1", "--branch", &ref_name, &url])
            .arg(&spec.dest)
            .status();
        if let Ok(s) = status {
            if s.success() {
                dirs.push(spec.dest.clone());
                continue;
            }
        }
        // Fall back to default-branch clone when the ref doesn't exist.
        let status = Command::new("git")
            .args(["clone", "--depth", "1", &url])
            .arg(&spec.dest)
            .status()?;
        if !status.success() {
            return Err(format!("git clone failed for {}", spec.url).into());
        }
        dirs.push(spec.dest.clone());
    }
    Ok(dirs)
}

/// Fetch + checkout the given ref (shallow) into an existing clone.
fn fetch_and_checkout(dir: &Path, ref_name: &str) -> Result<(), Box<dyn std::error::Error>> {
    let fetch = Command::new("git")
        .current_dir(dir)
        .args(["fetch", "--depth", "1", "origin", ref_name])
        .status()?;
    if fetch.success() {
        let _ = Command::new("git")
            .current_dir(dir)
            .args(["checkout", "-q", "FETCH_HEAD"])
            .status();
    }
    Ok(())
}

/// Write a minimal `.leankg/leankg.yaml` project config for a repo dir.
///
/// N1 (cycle-2 R2a): when a config already exists, this is read-modify-write
/// — user fields (including the `project.project_path` identity anchor and
/// keys serde does not model) are preserved and only MISSING template keys
/// are filled in. An unparseable existing file is left untouched.
pub fn write_project_config(dir: &Path) -> Result<PathBuf, Box<dyn std::error::Error>> {
    let name = dir
        .file_name()
        .map(|n| n.to_string_lossy().to_string())
        .unwrap_or_else(|| "repo".to_string());
    let leankg_dir = dir.join(".leankg");
    std::fs::create_dir_all(&leankg_dir)?;
    let config_path = leankg_dir.join("leankg.yaml");
    let yaml = format!(
        r#"project:
  name: "{name}"
  root: .
  project_path: "{path}"
  languages:
    - go
    - typescript
    - python
    - java
    - kotlin
    - rust
mcp:
  enabled: true
  auto_index_on_start: true
  auto_index_threshold_minutes: 60
  auto_index_on_db_write: false
  require_git_for_auto_index: false
indexer:
  exclude:
    - "**/node_modules/**"
    - "**/vendor/**"
  include:
    - "*.go"
    - "*.ts"
    - "*.tsx"
    - "*.js"
    - "*.py"
    - "*.java"
    - "*.kt"
    - "*.rs"
"#,
        name = name,
        path = dir.display(),
    );
    match std::fs::read_to_string(&config_path) {
        Ok(existing) => {
            // Merge the template UNDER the existing document; skip the write
            // entirely when nothing was missing.
            let Ok(mut merged) = serde_yaml::from_str::<serde_yaml::Value>(&existing) else {
                return Ok(config_path);
            };
            if !merged.is_mapping() {
                // A scalar/sequence document is not project config — leave it
                // exactly as the user wrote it.
                return Ok(config_path);
            }
            let Ok(template) = serde_yaml::from_str::<serde_yaml::Value>(&yaml) else {
                return Ok(config_path);
            };
            let before = merged.clone();
            crate::config::fill_missing_yaml_keys(&mut merged, &template);
            if merged == before {
                return Ok(config_path);
            }
            let out = serde_yaml::to_string(&merged)?;
            std::fs::write(&config_path, out)?;
            Ok(config_path)
        }
        Err(_) => {
            std::fs::write(&config_path, yaml)?;
            Ok(config_path)
        }
    }
}

/// Re-invoke the `leankg` binary for a subcommand inside a repo dir.
///
/// Reuses the exact CLI path for `index` and `embed` without duplicating the
/// orchestration that lives in `main.rs`. Returns the exit status.
fn run_leankg_sub(dir: &Path, args: &[&str]) -> Result<(), Box<dyn std::error::Error>> {
    // Resolve the binary next to the current executable (the compat `leankg`
    // / `leankg-internal` facade), not a PATH lookup — setup may run from a
    // container or a distinct internal binary.
    let bin = crate::cli::reexec::resolve_leankg_bin()?;
    let status = Command::new(&bin)
        .current_dir(dir)
        .args(args)
        .status()
        .map_err(|e| {
            format!(
                "failed to spawn `{} {}`: {e}",
                bin.display(),
                args.join(" ")
            )
        })?;
    if !status.success() {
        return Err(format!(
            "`{} {}` exited {:?} in {}",
            bin.display(),
            args.join(" "),
            status.code(),
            dir.display()
        )
        .into());
    }
    Ok(())
}

/// Full index (delete-then-insert via the CLI) for a repo dir. Returns the
/// number of elements indexed, or None when the count isn't surfaced.
pub fn index_one(
    dir: &Path,
    env: &str,
    verbose: bool,
) -> Result<Option<usize>, Box<dyn std::error::Error>> {
    let mut args = vec!["index", "."];
    if verbose {
        args.push("--verbose");
    }
    if !env.is_empty() {
        args.push("--env");
        args.push(env);
    }
    run_leankg_sub(dir, &args)?;
    Ok(None)
}

/// Run `leankg embed --wait --project <dir>` for a repo dir. Blocking; shares
/// the `.leankg` project config written above.
pub fn embed_one(dir: &Path) -> Result<(), Box<dyn std::error::Error>> {
    run_leankg_sub(dir, &["embed", "--wait", "--project", "."])
}

/// Full setup pipeline for a list of dirs.
pub fn run_setup(
    do_clone: bool,
    do_index: bool,
    do_embed: bool,
    status_only: bool,
) -> Result<(), Box<dyn std::error::Error>> {
    let specs = resolve_repos()?;

    if status_only || !(do_clone || do_index || do_embed) {
        println!("=== leankg setup --status ===");
        for spec in &specs {
            let state = if spec.dest.exists() {
                "exists"
            } else {
                "missing"
            };
            println!(
                "  {} | {} | {} ({state})",
                spec.name,
                spec.url,
                spec.dest.display()
            );
        }
        return Ok(());
    }

    let dirs: Vec<PathBuf> = if do_clone {
        match clone_repos(&specs) {
            Ok(d) => d,
            Err(e) if std::env::var("LEANKG_PROJECT_DIRS").is_ok() => {
                // No git token but dirs are mounted — fall back to indexing
                // whatever already exists on disk (skip clone).
                println!("WARN: clone skipped ({e}); using mounted dirs only.");
                specs.iter().map(|s| s.dest.clone()).collect()
            }
            Err(e) => return Err(e),
        }
    } else {
        specs.iter().map(|s| s.dest.clone()).collect()
    };

    let env = std::env::var("LEANKG_ENV").unwrap_or_else(|_| "local".to_string());
    let mut registry = crate::registry::Registry::load()?;
    let mut processed = 0usize;

    for dir in &dirs {
        if !dir.exists() {
            println!("WARN: {} does not exist, skipping", dir.display());
            continue;
        }
        processed += 1;
        let name = dir
            .file_name()
            .map(|n| n.to_string_lossy().to_string())
            .unwrap_or_else(|| "repo".to_string());

        let config_path = write_project_config(dir)?;
        println!("=== {name}: config {} ===", config_path.display());

        if do_index {
            println!("=== {name}: index ===");
            match index_one(dir, &env, true) {
                Ok(_) => {
                    let _ = registry.register(name.clone(), dir.display().to_string());
                }
                Err(e) => {
                    println!("WARN: index failed for {name}: {e}");
                }
            }
        }

        if do_embed {
            println!("=== {name}: embed ===");
            if let Err(e) = embed_one(dir) {
                println!("WARN: embed failed for {name}: {e}");
            }
        }
    }

    if processed == 0 && (do_index || do_embed || do_clone) {
        return Err(format!(
            "no project dirs found (clone_root={}, LEANKG_PROJECT_DIRS={:?}, LEANKG_REPOS={:?}); \
             set LEANKG_PROJECT_DIRS to mounted dirs or LEANKG_REPOS to a repo list",
            clone_root().display(),
            std::env::var("LEANKG_PROJECT_DIRS").unwrap_or_default(),
            std::env::var("LEANKG_REPOS").unwrap_or_default()
        )
        .into());
    }

    // Record the run marker so the post-bind trigger only runs once.
    // Soft-fail: an unwritable clone root must not abort a successful index/embed.
    let marker = clone_root().join(".leankg").join("setup.done");
    if let Some(parent) = marker.parent() {
        match std::fs::create_dir_all(parent)
            .and_then(|_| std::fs::write(&marker, env::now_rfc3339()))
        {
            Ok(()) => println!("Setup marker written: {}", marker.display()),
            Err(e) => println!(
                "WARN: could not write setup marker {}: {e}",
                marker.display()
            ),
        }
    }
    Ok(())
}

/// Whether the setup pipeline has already completed for this clone root.
pub fn setup_done() -> bool {
    clone_root().join(".leankg").join("setup.done").exists()
}

mod env {
    /// RFC3339-ish timestamp (seconds since epoch is fine for a marker).
    pub fn now_rfc3339() -> String {
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_secs().to_string())
            .unwrap_or_else(|_| "0".to_string())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::{Mutex, OnceLock};

    /// Serialize env-var-mutating tests — `set_var`/`remove_var` are
    /// process-global and tests run in parallel.
    fn env_lock() -> &'static Mutex<()> {
        static LOCK: OnceLock<Mutex<()>> = OnceLock::new();
        LOCK.get_or_init(|| Mutex::new(()))
    }

    /// Run `f` with the given (var, value) pairs set, restoring the previous
    /// values after. Sets ALL vars under ONE lock acquisition — `Mutex` is not
    /// re-entrant, so nested `with_envs` calls deadlock on the same thread.
    fn with_envs(vars: &[(&str, Option<&str>)], f: impl FnOnce()) {
        let _guard = env_lock().lock().unwrap_or_else(|e| e.into_inner());
        let mut prev = Vec::with_capacity(vars.len());
        for (var, value) in vars {
            prev.push((*var, std::env::var(var).ok()));
            match value {
                Some(v) => std::env::set_var(var, v),
                None => std::env::remove_var(var),
            }
        }
        f();
        for (var, old) in prev {
            match old {
                Some(p) => std::env::set_var(var, p),
                None => std::env::remove_var(var),
            }
        }
    }

    /// LEANKG_PROJECT_DIRS drives resolve_repos (skip-clone mode).
    #[test]
    fn resolve_repos_uses_project_dirs() {
        with_envs(
            &[
                ("LEANKG_PROJECT_DIRS", Some("/a/go-repo,/b/ts-repo")),
                ("LEANKG_CLONE_ROOT", Some("/")),
            ],
            || {
                let specs = resolve_repos().expect("resolve");
                assert_eq!(specs.len(), 2);
                assert_eq!(specs[0].name, "go-repo");
                assert_eq!(specs[0].dest, PathBuf::from("/a/go-repo"));
                assert_eq!(specs[1].name, "ts-repo");
            },
        );
    }

    /// LEANKG_REPOS drives clone-mode resolve with host + namespace split.
    #[test]
    fn resolve_repos_uses_repo_list() {
        with_envs(
            &[
                ("LEANKG_PROJECT_DIRS", Some("")),
                (
                    "LEANKG_REPOS",
                    Some("github.com/freepeak/leankg,github.com/org/other"),
                ),
                ("LEANKG_CLONE_ROOT", Some("/tmp/lkg-clone")),
                ("LEANKG_GIT_HOST", Some("github.com")),
            ],
            || {
                let specs = resolve_repos().expect("resolve");
                assert_eq!(specs.len(), 2);
                assert_eq!(specs[0].name, "github.com/freepeak/leankg");
                assert_eq!(specs[0].url, "https://github.com/freepeak/leankg");
                assert_eq!(
                    specs[0].dest,
                    PathBuf::from("/tmp/lkg-clone/github.com/freepeak/leankg")
                );
            },
        );
    }

    /// Empty LEANKG_PROJECT_DIRS + no LEANKG_REPOS falls back to an empty list
    /// (no baked internal table in the opensource fork).
    #[test]
    fn resolve_repos_empty_without_env() {
        with_envs(
            &[
                ("LEANKG_PROJECT_DIRS", None),
                ("LEANKG_REPOS", None),
                ("LEANKG_CLONE_ROOT", Some("/tmp/lkg-clone-empty")),
            ],
            || {
                let specs = resolve_repos().expect("resolve");
                assert!(specs.is_empty());
            },
        );
    }

    /// git_ref / clone_root / git_host env overrides.
    #[test]
    fn env_helpers_honor_overrides() {
        with_envs(
            &[
                ("LEANKG_GIT_REF", Some("master")),
                ("LEANKG_CLONE_ROOT", Some("/srv/repos")),
                ("LEANKG_GIT_HOST", Some("gitlab.example.com")),
            ],
            || {
                assert_eq!(git_ref(), "master");
                assert_eq!(clone_root(), PathBuf::from("/srv/repos"));
                assert_eq!(git_host(), "gitlab.example.com");
            },
        );
    }

    /// Defaults when no overrides.
    #[test]
    fn env_helpers_defaults() {
        with_envs(
            &[
                ("LEANKG_GIT_REF", None),
                ("GIT_REF", None),
                ("LEANKG_CLONE_ROOT", None),
                ("CLONE_ROOT", None),
                ("LEANKG_GIT_HOST", None),
            ],
            || {
                let cwd = std::env::current_dir().expect("cwd");
                assert_eq!(git_ref(), "main");
                assert_eq!(clone_root(), cwd);
                assert_eq!(git_host(), "github.com");
            },
        );
    }

    /// write_project_config creates a .leankg/leankg.yaml with the repo name.
    #[test]
    fn write_project_config_creates_yaml() {
        let tmp = tempfile::tempdir().expect("tempdir");
        let dir = tmp.path().join("be-food-order");
        std::fs::create_dir_all(&dir).expect("mkdir");
        let cfg = write_project_config(&dir).expect("write config");
        assert!(cfg.exists());
        let content = std::fs::read_to_string(&cfg).expect("read");
        assert!(content.contains("be-food-order"), "name missing: {content}");
        assert!(content.contains("languages"), "no languages block");
        assert!(content.contains("auto_index_on_start"), "no mcp block");
    }

    /// write_project_config is idempotent (doesn't overwrite existing).
    #[test]
    fn write_project_config_is_idempotent() {
        let tmp = tempfile::tempdir().expect("tempdir");
        let dir = tmp.path().join("repo-x");
        std::fs::create_dir_all(&dir).expect("mkdir");
        let first = write_project_config(&dir).expect("write");
        std::fs::write(&first, "sentinel").expect("overwrite");
        let second = write_project_config(&dir).expect("write again");
        assert_eq!(first, second);
        assert_eq!(std::fs::read_to_string(&second).expect("read"), "sentinel");
    }

    /// N1 (cycle-2 R2a): re-running setup over an EXISTING user-edited
    /// leankg.yaml must preserve every user field — including unmodeled
    /// custom keys and the project_path identity anchor.
    #[test]
    fn write_project_config_preserves_user_fields() {
        let tmp = tempfile::tempdir().expect("tempdir");
        let dir = tmp.path().join("repo-y");
        std::fs::create_dir_all(&dir).expect("mkdir");
        std::fs::write(
            dir.join(".leankg").join("leankg.yaml"),
            format!(
                "project:\n  name: user-name\n  root: ./src\n  project_path: {}\n  team_probe: keep-us\n",
                dir.display()
            ),
        )
        .expect("seed yaml");
        write_project_config(&dir).expect("rewrite");
        let out = std::fs::read_to_string(dir.join(".leankg").join("leankg.yaml")).unwrap();
        assert!(out.contains("user-name"), "name kept:\n{out}");
        assert!(out.contains("./src"), "root kept:\n{out}");
        assert!(out.contains("team_probe"), "custom key kept:\n{out}");
        assert!(
            out.contains(&dir.display().to_string()),
            "anchor kept:\n{out}"
        );
    }

    /// N1: when an existing yaml LOST its project_path anchor (the R2 sweep
    /// corruption), setup fills ONLY that missing key and leaves everything
    /// else untouched.
    #[test]
    fn write_project_config_refills_missing_identity_anchor() {
        let tmp = tempfile::tempdir().expect("tempdir");
        let dir = tmp.path().join("repo-z");
        std::fs::create_dir_all(&dir).expect("mkdir");
        std::fs::write(
            dir.join(".leankg").join("leankg.yaml"),
            "project:\n  name: anchored\n  team_probe: keep-us\n",
        )
        .expect("seed yaml");
        write_project_config(&dir).expect("fill");
        let out = std::fs::read_to_string(dir.join(".leankg").join("leankg.yaml")).unwrap();
        assert!(out.contains("project_path:"), "anchor refilled:\n{out}");
        assert!(out.contains("anchored"), "name kept:\n{out}");
        assert!(out.contains("keep-us"), "custom key kept:\n{out}");
    }

    /// setup_done respects the marker.
    #[test]
    fn setup_done_reflects_marker() {
        with_envs(
            &[("LEANKG_CLONE_ROOT", Some("/tmp/leankg-unit-marker"))],
            || {
                let root = clone_root();
                let _ = std::fs::remove_dir_all(&root);
                assert!(!setup_done());
                std::fs::create_dir_all(root.join(".leankg")).expect("mkdir");
                std::fs::write(root.join(".leankg").join("setup.done"), "1").expect("write marker");
                assert!(setup_done());
                let _ = std::fs::remove_dir_all(root);
            },
        );
    }

    /// discover_git_repos walks a nested-monorepo workspace for git repos.
    #[test]
    fn discover_git_repos_finds_nested_repos() {
        let tmp = tempfile::tempdir().unwrap();
        let root = tmp.path();
        for rel in [
            "platform-food/be-food-order",
            "platform-food/be-food-gateway",
            "platform-core/be-mailer",
        ] {
            let repo = root.join(rel);
            std::fs::create_dir_all(&repo).unwrap();
            std::fs::write(repo.join(".git"), "").unwrap();
        }
        // A non-repo dir + hidden + target must be skipped.
        std::fs::create_dir_all(root.join("misc")).unwrap();
        std::fs::create_dir_all(root.join("target")).unwrap();
        std::fs::create_dir_all(root.join(".hidden")).unwrap();
        let repos = discover_git_repos(root, 3);
        assert_eq!(repos.len(), 3);
        assert!(repos.iter().any(|p| p.ends_with("be-food-order")));
        assert!(repos.iter().any(|p| p.ends_with("be-mailer")));
    }

    /// LEANKG_WORKSPACE_DIR drives resolve_repos (monorepo discovery, skip clone).
    #[test]
    fn resolve_repos_uses_workspace_dir() {
        let tmp = tempfile::tempdir().unwrap();
        let root = tmp.path();
        for rel in ["platform-food/be-food-order", "platform-core/be-mailer"] {
            let repo = root.join(rel);
            std::fs::create_dir_all(&repo).unwrap();
            std::fs::write(repo.join(".git"), "").unwrap();
        }
        with_envs(
            &[
                ("LEANKG_PROJECT_DIRS", None),
                ("LEANKG_REPOS", None),
                ("LEANKG_CLONE_ROOT", Some("/")),
                (
                    "LEANKG_WORKSPACE_DIR",
                    Some(root.to_string_lossy().as_ref()),
                ),
            ],
            || {
                let specs = resolve_repos().expect("resolve");
                assert_eq!(specs.len(), 2, "workspace must drive discovery");
                assert!(
                    specs.iter().all(|s| s.url.is_empty()),
                    "mounted → skip clone"
                );
                assert!(specs.iter().any(|s| s.name == "be-food-order"));
                assert!(specs.iter().any(|s| s.name == "be-mailer"));
            },
        );
    }
}
