//! US-CBM-C5: Windows build + smoke.
//!
//! CI runs a `windows-latest` release build job (`.github/workflows/release.yml`
//! matrix) but never exercises the binary. This test runs the real CLI
//! end-to-end (init -> index -> query) on the host OS and is gated to
//! Windows with `#[cfg(windows)]`; on other platforms the suite compiles
//! but runs zero tests, so `cargo test` stays green everywhere.
//!
//! On Linux/macOS the same flow is already covered by
//! `tests/cli_full_coverage_tests.rs`; this file is the Windows-specific
//! smoke that catches path/quoting/CRLF regressions the release job
//! cannot see.

#![cfg(windows)]

use std::path::Path;
use std::process::Command;
use tempfile::TempDir;

const BIN: &str = env!("CARGO_BIN_EXE_leankg");

fn run_cli(cwd: &Path, args: &[&str]) -> String {
    let out = Command::new(BIN)
        .current_dir(cwd)
        .args(args)
        .output()
        .unwrap_or_else(|e| panic!("failed to run leankg {:?}: {}", args, e));
    assert!(
        out.status.success(),
        "leankg {:?} failed: stdout={} stderr={}",
        args,
        String::from_utf8_lossy(&out.stdout),
        String::from_utf8_lossy(&out.stderr)
    );
    String::from_utf8_lossy(&out.stdout).into_owned()
}

#[test]
fn windows_smoke_init_index_query_roundtrip() {
    let tmp = TempDir::new().expect("tempdir");
    let project = tmp.path().join("win-proj");
    std::fs::create_dir_all(&project).unwrap();

    let out = run_cli(&project, &["init"]);
    assert!(
        out.contains("Initialized LeanKG project"),
        "init stdout: {out}"
    );
    assert!(project.join(".leankg").is_dir(), ".leankg must exist");
    assert!(
        project.join("leankg.yaml").exists(),
        "leankg.yaml must exist"
    );

    let src = project.join("src");
    std::fs::create_dir_all(&src).unwrap();
    std::fs::write(
        src.join("main.rs"),
        "fn greet(name: &str) -> String { format!(\"hi {name}\") }\nfn main() {}\n",
    )
    .unwrap();

    let out = run_cli(&project, &["index"]);
    assert!(out.contains("elements"), "index stdout: {out}");

    // Smoke the graph query path — verifies the SQLite DB opened/queried
    // correctly on Windows (path separators, CozoDB file handle).
    let out = run_cli(&project, &["query", "greet", "--kind", "name"]);
    assert!(out.contains("greet"), "query must find greet symbol: {out}");

    // `version` must work from an arbitrary cwd (no project dependency).
    let out = run_cli(tmp.path(), &["version"]);
    assert!(out.contains("leankg"), "version stdout: {out}");
}
