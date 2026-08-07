//! Shared helper: re-exec the compat `leankg` binary with a mapped argv.
//!
//! Keeps `leankg-mcp` / `leankg-worker` as thin wrappers so all pipeline /
//! MCP handler logic stays in `src/main.rs` (compat facade).

use std::path::PathBuf;
use std::process::Command;

/// Resolve the sibling `leankg` binary next to the current executable.
pub fn resolve_leankg_bin() -> Result<PathBuf, String> {
    let current = std::env::current_exe().map_err(|e| format!("current_exe: {e}"))?;
    let dir = current
        .parent()
        .ok_or_else(|| "current_exe has no parent directory".to_string())?;
    let candidate = dir.join("leankg");
    if candidate.is_file() {
        return Ok(candidate);
    }
    // Windows / cargo naming fallbacks
    let candidate_exe = dir.join("leankg.exe");
    if candidate_exe.is_file() {
        return Ok(candidate_exe);
    }
    Err(format!(
        "compat binary not found at {} (build `leankg` alongside this binary)",
        candidate.display()
    ))
}

/// Replace this process with `leankg <argv…>` (Unix `exec`), or spawn+wait
/// and exit with the child's status when `exec` is unavailable.
pub fn reexec_leankg(argv: &[String]) -> Result<(), Box<dyn std::error::Error>> {
    let bin = resolve_leankg_bin()?;
    #[cfg(unix)]
    {
        use std::os::unix::process::CommandExt;
        let err = Command::new(&bin).args(argv).exec();
        Err(format!("failed to exec {}: {err}", bin.display()).into())
    }
    #[cfg(not(unix))]
    {
        use std::process::Stdio;
        let status = Command::new(&bin)
            .args(argv)
            .stdin(Stdio::inherit())
            .stdout(Stdio::inherit())
            .stderr(Stdio::inherit())
            .status()?;
        if status.success() {
            Ok(())
        } else {
            Err(format!("leankg exited with {status}").into())
        }
    }
}
