//! Opt-in live source tests against real public/private endpoints.
//!
//! Run with:
//! `LEANKG_LIVE_SOURCE_TESTS=1 cargo test --test source_live_tests -- --ignored --nocapture`
//!
//! Git URI comes from `LEANKG_LIVE_GIT_URI`. Internal GitLab tests use
//! `GITLAB_TOKEN`/`GIT_TOKEN` without storing either.
//! GCS tests require `LEANKG_LIVE_GCS_URI` and a configured access token.

use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;
use leankg::indexer::{find_files_sync, index_files_parallel};
use leankg::sources::gcs::GcsSource;
use leankg::sources::git::GitSource;
use leankg::sources::{parse_source_uri, ProgressReporter, Source, SourceFactory, SourceUri};
use tempfile::TempDir;

struct SilentProgress;

impl ProgressReporter for SilentProgress {
    fn report(&mut self, _message: &str) {}
}

fn live_enabled() -> bool {
    std::env::var("LEANKG_LIVE_SOURCE_TESTS").as_deref() == Ok("1")
}

#[tokio::test]
#[ignore = "Requires explicit internet access; set LEANKG_LIVE_SOURCE_TESTS=1"]
async fn public_or_internal_gitlab_fingerprint_and_index_contract() {
    if !live_enabled() {
        return;
    }

    let uri = match std::env::var("LEANKG_LIVE_GIT_URI") {
        Ok(uri) => uri,
        Err(_) => return,
    };
    let parsed = parse_source_uri(&uri).expect("valid git source URI");
    let SourceUri::Git { url } = parsed else {
        panic!("live GitLab source must use git+ URI");
    };
    let token = std::env::var("GITLAB_TOKEN")
        .ok()
        .or_else(|| std::env::var("GIT_TOKEN").ok());
    let source = GitSource {
        url,
        auth: token,
        ref_name: std::env::var("LEANKG_LIVE_GIT_REF").unwrap_or_else(|_| "main".into()),
    };

    let first = source
        .remote_fingerprint()
        .await
        .expect("git ls-remote should reach live repository")
        .expect("live repository should expose selected ref");
    assert!(!first.is_empty());

    let tmp = TempDir::new().expect("staging tempdir");
    let mut progress = SilentProgress;
    let staged = source
        .sync_to_local(tmp.path(), &mut progress)
        .await
        .expect("live repository should materialize");
    let files = find_files_sync(&staged.to_string_lossy()).expect("discover staged files");
    assert!(
        !files.is_empty(),
        "live repository produced no indexable files"
    );

    let db = init_db(&tmp.path().join("graph.db")).expect("initialize graph");
    let graph = GraphEngine::new(db);
    let indexed = index_files_parallel(&graph, &files, false).expect("index live files");
    assert!(indexed > 0, "live repository produced no graph elements");
}

#[tokio::test]
#[ignore = "Requires explicit internet access and configured GCS credentials"]
async fn configured_gcs_source_lists_and_indexes_live_objects() {
    if !live_enabled() {
        return;
    }
    let uri = match std::env::var("LEANKG_LIVE_GCS_URI") {
        Ok(uri) => uri,
        Err(_) => return,
    };
    let SourceUri::Gcs { bucket, prefix } = parse_source_uri(&uri).expect("valid GCS URI") else {
        panic!("live GCS source must use gs:// URI");
    };
    let source = GcsSource {
        bucket,
        prefix,
        auth: std::env::var("GCS_ACCESS_TOKEN").ok(),
    };
    let tmp = TempDir::new().expect("staging tempdir");
    let mut progress = SilentProgress;
    let staged = source
        .sync_to_local(tmp.path(), &mut progress)
        .await
        .expect("configured GCS source should materialize");
    let files = find_files_sync(&staged.to_string_lossy()).expect("discover staged objects");
    assert!(
        !files.is_empty(),
        "live GCS source produced no indexable files"
    );
}

#[test]
#[ignore = "Requires explicit internet access; set LEANKG_LIVE_SOURCE_TESTS=1"]
fn live_source_factory_accepts_gitlab_uri() {
    if !live_enabled() {
        return;
    }
    let uri = match std::env::var("LEANKG_LIVE_GIT_URI") {
        Ok(uri) => uri,
        Err(_) => return,
    };
    let parsed = parse_source_uri(&uri).expect("valid GitLab URI");
    assert!(SourceFactory::create(&parsed, None, Some("main")).is_ok());
}
