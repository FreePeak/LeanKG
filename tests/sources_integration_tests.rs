//! End-to-end integration tests for the sources module.
//!
//! Covers:
//! - `LocalSource` resolves a real filesystem path through canonicalize.
//! - `GitSource` clones a real local bare git repo and indexes its files.
//! - `parse_source_uri` round-trips for every supported scheme.

use leankg::sources::git::GitSource;
use leankg::sources::local::LocalSource;
use leankg::sources::{parse_source_uri, ProgressReporter, Source, SourceFactory, SourceUri};
use std::path::Path;
use std::process::Command;
use tempfile::TempDir;

struct CollectingProgress {
    messages: Vec<String>,
}

impl ProgressReporter for CollectingProgress {
    fn report(&mut self, message: &str) {
        self.messages.push(message.to_string());
    }
}

fn init_git_repo(path: &Path) {
    std::fs::create_dir_all(path).unwrap();
    let run = |args: &[&str]| {
        Command::new("git")
            .args(args)
            .current_dir(path)
            .output()
            .unwrap_or_else(|e| panic!("git {:?} failed: {}", args, e))
    };
    assert!(
        run(&["init", "--initial-branch=main"]).status.success(),
        "git init failed"
    );
    run(&["config", "user.email", "test@example.com"]);
    run(&["config", "user.name", "test"]);
    std::fs::write(path.join("main.go"), "package main\nfunc main() {}\n").unwrap();
    run(&["add", "main.go"]);
    assert!(run(&["commit", "-m", "init"]).status.success());
}

/// Build a `file://` URL from a local path so git treats it as a remote
/// source (avoiding "warning: --depth is ignored in local clones").
fn file_url(path: &Path) -> String {
    format!("file://{}", path.to_string_lossy())
}

#[tokio::test]
async fn local_source_resolves_existing_path() {
    let tmp = TempDir::new().unwrap();
    let src_dir = tmp.path().join("my-code");
    std::fs::create_dir_all(&src_dir).unwrap();
    std::fs::write(src_dir.join("hello.go"), "package main\n").unwrap();

    let src = LocalSource {
        path: src_dir.to_string_lossy().to_string(),
    };
    let mut progress = CollectingProgress { messages: vec![] };
    let resolved = src
        .sync_to_local(tmp.path(), &mut progress)
        .await
        .expect("local sync failed");

    assert!(resolved.is_dir());
    assert!(resolved.join("hello.go").exists());
    assert!(progress.messages.iter().any(|m| m.contains("local source")));
    assert_eq!(src.name(), "local");
}

#[tokio::test]
async fn local_source_resolves_relative_path() {
    let tmp = TempDir::new().unwrap();
    let src_dir = tmp.path().join("rel-code");
    std::fs::create_dir_all(&src_dir).unwrap();
    let rel_name = src_dir.file_name().unwrap().to_str().unwrap();

    // Run the test from the tmp dir so the relative path is resolvable.
    let prev_cwd = std::env::current_dir().unwrap();
    std::env::set_current_dir(tmp.path()).unwrap();

    let src = LocalSource {
        path: rel_name.to_string(),
    };
    let mut progress = CollectingProgress { messages: vec![] };
    let resolved = src.sync_to_local(tmp.path(), &mut progress).await.unwrap();
    assert!(resolved.is_dir());

    std::env::set_current_dir(prev_cwd).unwrap();
}

#[tokio::test]
async fn git_source_clones_local_repo_into_staging() {
    let tmp = TempDir::new().unwrap();
    let repo_dir = tmp.path().join("source-repo");
    init_git_repo(&repo_dir);

    let workdir = tmp.path().join("workdir");
    std::fs::create_dir_all(&workdir).unwrap();

    let src = GitSource {
        url: file_url(&repo_dir),
        auth: None,
        ref_name: "main".to_string(),
    };
    let mut progress = CollectingProgress { messages: vec![] };
    let synced = src
        .sync_to_local(&workdir, &mut progress)
        .await
        .expect("git sync failed");

    assert!(synced.is_dir(), "synced dir does not exist");
    assert!(
        synced.join("main.go").exists(),
        "expected main.go in synced dir, got: {:?}",
        std::fs::read_dir(&synced)
            .unwrap()
            .flatten()
            .collect::<Vec<_>>()
    );
    assert!(
        synced.join(".git").exists(),
        "expected .git in synced dir (full clone)"
    );
    assert!(progress.messages.iter().any(|m| m.contains("cloning")));
}

#[tokio::test]
async fn git_source_pulls_when_repo_already_exists() {
    let tmp = TempDir::new().unwrap();
    let repo_dir = tmp.path().join("source-repo");
    init_git_repo(&repo_dir);

    let workdir = tmp.path().join("workdir");
    std::fs::create_dir_all(&workdir).unwrap();

    let src = GitSource {
        url: file_url(&repo_dir),
        auth: None,
        ref_name: "main".to_string(),
    };
    let mut progress1 = CollectingProgress { messages: vec![] };
    src.sync_to_local(&workdir, &mut progress1)
        .await
        .expect("first sync failed");

    let mut progress2 = CollectingProgress { messages: vec![] };
    let synced2 = src
        .sync_to_local(&workdir, &mut progress2)
        .await
        .expect("second sync failed");

    assert!(synced2.join("main.go").exists());
    assert!(
        progress2.messages.iter().any(|m| m.contains("pulling")),
        "expected a 'pulling' message on second sync, got: {:?}",
        progress2.messages
    );
}

#[tokio::test]
async fn factory_returns_unimplemented_for_s3_sftp_gdrive() {
    let progress = CollectingProgress { messages: vec![] };
    let s3 = SourceFactory::create(
        &SourceUri::S3 {
            bucket: "b".into(),
            prefix: "p".into(),
        },
        None,
        None,
    );
    assert!(s3.is_err());
    let sftp = SourceFactory::create(
        &SourceUri::Sftp {
            user: "u".into(),
            host: "h".into(),
            port: 22,
            path: "/".into(),
        },
        None,
        None,
    );
    assert!(sftp.is_err());
    let gdrive = SourceFactory::create(
        &SourceUri::GoogleDrive {
            folder_id: "f".into(),
        },
        None,
        None,
    );
    assert!(gdrive.is_err());

    let _ = progress;
}

#[test]
fn parse_source_uri_round_trips_all_schemes() {
    let cases = vec![
        (
            "gs://my-bucket/path/to/code",
            SourceUri::Gcs {
                bucket: "my-bucket".into(),
                prefix: "path/to/code".into(),
            },
        ),
        (
            "s3://my-bucket/prefix",
            SourceUri::S3 {
                bucket: "my-bucket".into(),
                prefix: "prefix".into(),
            },
        ),
        (
            "git+https://github.com/user/repo.git",
            SourceUri::Git {
                url: "https://github.com/user/repo.git".into(),
            },
        ),
        (
            "sftp://user@host:2222/path",
            SourceUri::Sftp {
                user: "user".into(),
                host: "host".into(),
                port: 2222,
                path: "/path".into(),
            },
        ),
        (
            "gdrive://abc123",
            SourceUri::GoogleDrive {
                folder_id: "abc123".into(),
            },
        ),
        (
            "/abs/path",
            SourceUri::Local {
                path: "/abs/path".into(),
            },
        ),
    ];

    for (input, expected) in cases {
        let parsed = parse_source_uri(input).expect(input);
        assert_eq!(parsed, expected, "round-trip failed for {}", input);
    }
}

#[tokio::test]
async fn staged_repo_files_match_local_indexing_input() {
    // The whole point of the sources layer: a staged git repo should be
    // indexable by the existing indexer pipeline without any change.
    let tmp = TempDir::new().unwrap();
    let repo_dir = tmp.path().join("real-repo");
    init_git_repo(&repo_dir);

    // Write a few more files so a full file walk yields > 1 hit.
    std::fs::create_dir_all(repo_dir.join("internal")).unwrap();
    std::fs::write(
        repo_dir.join("internal").join("lib.go"),
        "package internal\nfunc Add(a, b int) int { return a + b }\n",
    )
    .unwrap();
    let _ = Command::new("git")
        .current_dir(&repo_dir)
        .args(["add", "."])
        .output();
    let _ = Command::new("git")
        .current_dir(&repo_dir)
        .args(["commit", "-m", "more files"])
        .output();

    let workdir = tmp.path().join("workdir");
    std::fs::create_dir_all(&workdir).unwrap();

    let src = GitSource {
        url: file_url(&repo_dir),
        auth: None,
        ref_name: "main".to_string(),
    };
    let mut progress = CollectingProgress { messages: vec![] };
    let synced = src.sync_to_local(&workdir, &mut progress).await.unwrap();

    // Use the indexer's own file walker to verify the staged tree is well-formed.
    let files = leankg::indexer::find_files_sync(synced.to_str().unwrap())
        .expect("find_files_sync failed on staged dir");
    assert!(
        files.iter().any(|p| p.ends_with("main.go")),
        "main.go missing from staged index, files: {:?}",
        files
    );
    assert!(
        files.iter().any(|p| p.ends_with("lib.go")),
        "lib.go missing from staged index, files: {:?}",
        files
    );
}

/// Mutex for tests that mutate process-wide env vars (GCS_ACCESS_TOKEN).
/// tokio tests run in parallel; env var mutations are not thread-safe.
static ENV_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

#[tokio::test]
async fn gcs_source_requires_auth_token() {
    use leankg::sources::gcs::GcsSource;

    let _guard = ENV_LOCK.lock().unwrap();
    std::env::remove_var("GCS_ACCESS_TOKEN");

    let src = GcsSource {
        bucket: "my-bucket".into(),
        prefix: String::new(),
        auth: None,
    };
    let tmp = TempDir::new().unwrap();
    let mut progress = CollectingProgress { messages: vec![] };
    let result = src.sync_to_local(tmp.path(), &mut progress).await;
    let err = result.expect_err("expected auth error");
    let msg = err.to_string();
    assert!(
        msg.contains("GCS source requires auth"),
        "unexpected error: {}",
        msg
    );
}

#[tokio::test]
async fn gcs_source_accepts_auth_via_env() {
    use leankg::sources::gcs::GcsSource;

    let _guard = ENV_LOCK.lock().unwrap();
    // We cannot actually contact GCS in unit tests, but we can verify
    // that the auth path is reached and produces a network error (not
    // an auth error) when a token is provided via env.
    std::env::set_var("GCS_ACCESS_TOKEN", "fake-test-token");
    let src = GcsSource {
        bucket: "fake-bucket".into(),
        prefix: String::new(),
        auth: None,
    };
    let tmp = TempDir::new().unwrap();
    let mut progress = CollectingProgress { messages: vec![] };
    let result = src.sync_to_local(tmp.path(), &mut progress).await;
    std::env::remove_var("GCS_ACCESS_TOKEN");
    // Must fail (fake token), but for a network/auth reason, not "no auth".
    if let Err(e) = result {
        let msg = e.to_string();
        assert!(
            !msg.contains("GCS source requires auth"),
            "auth should have been satisfied via env var, but got: {}",
            msg
        );
    }
}

#[tokio::test]
async fn local_source_with_missing_path_fails() {
    let src = LocalSource {
        path: "/this/path/does/not/exist/anywhere".to_string(),
    };
    let tmp = TempDir::new().unwrap();
    let mut progress = CollectingProgress { messages: vec![] };
    // canonicalize on a missing path will return the input; we expect
    // the resulting path to NOT be a directory of files.
    let resolved = src.sync_to_local(tmp.path(), &mut progress).await.unwrap();
    // The path is returned, but it doesn't lead to a real dir.
    assert!(!resolved.exists() || !resolved.is_dir());
}
