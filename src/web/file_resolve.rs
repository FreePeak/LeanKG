//! Resolve `/api/file` paths under the active project and optional sibling mounts.
//!
//! Docker multi-root setups (`LEANKG_PROJECT_DIRS`) can leave graph rows whose
//! relative `file_path` exists on another mount than `current_project_path`.
//! Without a sibling probe, CodePanel gets opaque HTTP 400 "File not found".

use std::path::{Path, PathBuf};

#[derive(Debug, PartialEq, Eq)]
pub enum FileResolveError {
    Directory {
        path: String,
    },
    OutsideProject,
    NotFound {
        relative: String,
        tried: Vec<String>,
    },
}

/// Strip `./` and map absolute paths under `proj_path` to project-relative.
pub fn clean_project_relative_path(raw: &str, proj_path: &Path) -> String {
    let mut clean_path = raw.strip_prefix("./").unwrap_or(raw).to_string();
    if let Ok(canonical_proj) = proj_path.canonicalize() {
        let proj_str = canonical_proj.to_string_lossy();
        if let Some(remainder) = clean_path.strip_prefix(proj_str.as_ref()) {
            let stripped = remainder.trim_start_matches('/');
            clean_path = if stripped.is_empty() {
                ".".to_string()
            } else {
                stripped.to_string()
            };
        } else if clean_path.starts_with('/') {
            let proj_raw = proj_path.to_string_lossy();
            if let Some(remainder) = clean_path.strip_prefix(proj_raw.as_ref()) {
                let stripped = remainder.trim_start_matches('/');
                clean_path = if stripped.is_empty() {
                    ".".to_string()
                } else {
                    stripped.to_string()
                };
            }
        }
    }
    clean_path
}

/// Parse `LEANKG_PROJECT_DIRS` (comma-separated container/host roots).
pub fn project_dirs_from_env() -> Vec<PathBuf> {
    std::env::var("LEANKG_PROJECT_DIRS")
        .ok()
        .map(|s| {
            s.split(',')
                .map(|p| p.trim())
                .filter(|p| !p.is_empty())
                .map(PathBuf::from)
                .collect()
        })
        .unwrap_or_default()
}

fn try_under_root(clean_path: &str, root: &Path) -> Result<PathBuf, Option<FileResolveError>> {
    let target = if clean_path == "." || clean_path.is_empty() {
        root.to_path_buf()
    } else {
        root.join(clean_path)
    };
    let canonical_root = root.canonicalize().unwrap_or_else(|_| root.to_path_buf());
    match target.canonicalize() {
        Ok(resolved) if resolved.starts_with(&canonical_root) => {
            if resolved.is_dir() {
                Err(Some(FileResolveError::Directory {
                    path: clean_path.to_string(),
                }))
            } else {
                Ok(resolved)
            }
        }
        Ok(_) => Err(Some(FileResolveError::OutsideProject)),
        Err(_) => Err(None), // missing under this root — try next
    }
}

/// Resolve a readable file under `primary`, then any extra project roots.
pub fn resolve_readable_file(
    clean_path: &str,
    primary: &Path,
    extras: &[PathBuf],
) -> Result<PathBuf, FileResolveError> {
    let mut roots: Vec<PathBuf> = Vec::with_capacity(1 + extras.len());
    roots.push(primary.to_path_buf());
    for extra in extras {
        if extra != primary && !roots.iter().any(|r| r == extra) {
            roots.push(extra.clone());
        }
    }

    let mut tried: Vec<String> = Vec::new();
    for root in &roots {
        let candidate = if clean_path == "." || clean_path.is_empty() {
            root.display().to_string()
        } else {
            root.join(clean_path).display().to_string()
        };
        tried.push(candidate);

        match try_under_root(clean_path, root) {
            Ok(path) => return Ok(path),
            Err(Some(FileResolveError::Directory { path })) => {
                return Err(FileResolveError::Directory { path });
            }
            Err(Some(FileResolveError::OutsideProject)) => {
                // Traversal under this root — keep searching siblings only for
                // plain relative paths (no leading `/` or `..` segments).
                if clean_path.starts_with('/') || clean_path.split('/').any(|s| s == "..") {
                    return Err(FileResolveError::OutsideProject);
                }
            }
            Err(None) | Err(Some(FileResolveError::NotFound { .. })) => {}
        }
    }

    Err(FileResolveError::NotFound {
        relative: clean_path.to_string(),
        tried,
    })
}

pub fn not_found_message(err: &FileResolveError) -> String {
    match err {
        FileResolveError::NotFound { relative, tried } => format!(
            "File not found '{}'. Indexed path is missing under the active project root; \
             tried {}. If this path belongs to another mount in LEANKG_PROJECT_DIRS, \
             switch project or reindex the active root so the graph matches disk.",
            relative,
            tried.join(", ")
        ),
        FileResolveError::Directory { path } => format!(
            "Path '{}' is a directory; use /api/graph/expand-service?path=…&all=true to load its subgraph",
            path
        ),
        FileResolveError::OutsideProject => {
            "Access denied: path is outside project directory".to_string()
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;
    use std::io::Write;

    fn fresh(name: &str) -> PathBuf {
        let dir = std::env::temp_dir().join(format!(
            "leankg-file-resolve-{}-{}",
            name,
            std::process::id()
        ));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).unwrap();
        dir
    }

    #[test]
    fn clean_strips_dot_slash() {
        let root = PathBuf::from("/workspace");
        assert_eq!(
            clean_project_relative_path("./src/main.rs", &root),
            "src/main.rs"
        );
    }

    #[test]
    fn resolves_under_primary() {
        let primary = fresh("primary");
        let nested = primary.join("src");
        fs::create_dir_all(&nested).unwrap();
        let file = nested.join("main.rs");
        fs::File::create(&file)
            .unwrap()
            .write_all(b"fn main() {}")
            .unwrap();

        let got = resolve_readable_file("src/main.rs", &primary, &[]).unwrap();
        assert_eq!(got.canonicalize().unwrap(), file.canonicalize().unwrap());
        fs::remove_dir_all(&primary).ok();
    }

    #[test]
    fn falls_back_to_sibling_mount() {
        let primary = fresh("prim");
        let sibling = fresh("sib");
        let nested = sibling.join("claude-mem/plugin/ui");
        fs::create_dir_all(&nested).unwrap();
        let file = nested.join("viewer-bundle.js");
        fs::File::create(&file)
            .unwrap()
            .write_all(b"console.log(1)")
            .unwrap();

        // Missing under primary — present under sibling (Docker multi-root).
        let got = resolve_readable_file(
            "claude-mem/plugin/ui/viewer-bundle.js",
            &primary,
            &[sibling.clone()],
        )
        .unwrap();
        assert_eq!(got.canonicalize().unwrap(), file.canonicalize().unwrap());

        fs::remove_dir_all(&primary).ok();
        fs::remove_dir_all(&sibling).ok();
    }

    #[test]
    fn missing_everywhere_is_not_found() {
        let primary = fresh("gone");
        let err = resolve_readable_file("nope.js", &primary, &[]).unwrap_err();
        assert!(matches!(err, FileResolveError::NotFound { .. }));
        fs::remove_dir_all(&primary).ok();
    }
}
