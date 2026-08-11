//! Shared helper: re-exec the compat `leankg` binary with a mapped argv.
//!
//! Keeps `leankg-mcp` / `leankg-worker` as thin wrappers so all pipeline /
//! MCP handler logic stays in `src/main.rs` (compat facade).

use std::path::PathBuf;
use std::process::Command;

/// Resolve the sibling `leankg` / `leankg-internal` binary next to the current
/// executable. Prefers `leankg-internal` (the internal clone's binary, distinct
/// from the freepeak-opensource `leankg` that shares the sccache target dir),
/// falling back to `leankg`. Skips a candidate equal to `current_exe` so a
/// `cargo run` of the main binary doesn't exec itself recursively.
pub fn resolve_leankg_bin() -> Result<PathBuf, String> {
    let current = std::env::current_exe().map_err(|e| format!("current_exe: {e}"))?;
    let dir = current
        .parent()
        .ok_or_else(|| "current_exe has no parent directory".to_string())?;
    for name in ["leankg-internal", "leankg"] {
        let candidate = dir.join(name);
        if candidate.is_file() && candidate != current {
            return Ok(candidate);
        }
        // Windows / cargo naming fallbacks
        let candidate_exe = dir.join(format!("{name}.exe"));
        if candidate_exe.is_file() && candidate_exe != current {
            return Ok(candidate_exe);
        }
    }
    Err(format!(
        "compat binary not found next to {} (build `leankg-internal` alongside this binary)",
        dir.display()
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
