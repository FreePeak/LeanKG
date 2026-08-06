//! PG pool reconnect e2e: sequential queries on the same `PostgresBackend`
//! must all succeed — regression for the stale-connection `Error::Closed`
//! bug where `ClientPool` returned closed idle connections blindly.
//!
//! Before the fix: the second sequential query failed with
//! `Error::Closed` / `connection closed` after the pool's idle clients went
//! stale (long-running mcp-http + host.docker.internal route flapping).
//! After the fix: `count_elements` returns the real row count (804k on be).
//!
//! Requires a live PG reachable via LEANKG_PG_URL (e.g. run inside a Docker
//! container on the leankg network). Ignored by default.
#![cfg(feature = "embeddings")]

use leankg::db::backend::init_db;
use leankg::embeddings::state::{list_all, list_stale};
use leankg::graph::GraphEngine;

#[test]
#[ignore = "needs live PG via LEANKG_PG_URL"]
fn two_sequential_queries_both_succeed() {
    let tmp = tempfile::tempdir().unwrap();
    let db = init_db(tmp.path().join("x.db").as_path()).unwrap();
    let ge = GraphEngine::new(db.clone());
    let a = ge
        .count_elements()
        .unwrap_or_else(|e| panic!("count a: {e}"));
    let b = ge
        .count_elements()
        .unwrap_or_else(|e| panic!("count b: {e}"));
    eprintln!("count a={a} b={b}");
    // The exact queries the embed preflight runs — these hit `embedding_state`
    // and previously surfaced `connection closed` on the second call.
    let stale = list_stale(db.as_ref()).unwrap_or_else(|e| panic!("list_stale failed: {e}"));
    eprintln!("stale = {}", stale.len());
    let all = list_all(db.as_ref()).unwrap_or_else(|e| panic!("list_all failed: {e}"));
    eprintln!("all = {}", all.len());
}

#[test]
#[ignore = "needs live PG via LEANKG_PG_URL"]
fn count_then_list_stale_succeeds() {
    let tmp = tempfile::tempdir().unwrap();
    let db = init_db(tmp.path().join("x.db").as_path()).unwrap();
    let ge = GraphEngine::new(db.clone());
    let total = ge
        .count_elements()
        .unwrap_or_else(|e| panic!("count_elements: {e}"));
    eprintln!("count_elements = {total}");
    let stale = list_stale(db.as_ref()).unwrap_or_else(|e| panic!("list_stale failed: {e}"));
    eprintln!("stale rows = {}", stale.len());
}
