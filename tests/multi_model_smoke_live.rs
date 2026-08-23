//! Live multi-model embed switch smoke against local PG (scratch schema).
//!
//! Proves switching providers between (1) local BGE ONNX 384-d and (2) a
//! mock OpenAI-compatible API (qwen 2560-d) preserves each model's vector
//! collection row counts (table-per-model).
//!
//! Requires:
//!   - local PG docker `leankg-pg-phase0` on :5433 (NEVER company PG)
//!   - mock OpenAI server: `python3 /tmp/leankg-mock-embed/mock_embed.py 18080`
//!   - ONNX BGE weights cached (first run downloads ~120 MB from HF)
//!
//! Run:
//! ```text
//! LEANKG_PG_URL=postgresql://postgres:postgres@localhost:5433/leankg \
//!   cargo test --release --features embeddings --test multi_model_smoke_live -- --nocapture
//! ```
//!
//! Safety: everything lands in the fixture's derived PG schema
//! (`schema_for_path` — a deterministic hash of the fixture project_path,
//! e.g. `leankg_p_...`), dropped on exit. Fixture lives in /tmp only.
//! No production code touched.

#![cfg(feature = "embeddings")]

#[allow(unused_imports)]
use leankg::db::backend::pg_connect;
use leankg::db::backend::{init_db, schema_for_path};
use leankg::embeddings::{self, build_index, parse_type_filter, BuildMode, BuildOptions};
use leankg::graph::GraphEngine;
use leankg::retrieval::{RetrieveOptions, SemanticRetrievalPipeline};
use std::sync::{Mutex, OnceLock};

static ENV_LOCK: OnceLock<Mutex<()>> = OnceLock::new();

const MOCK_BASE: &str = "http://127.0.0.1:18080";
const MOCK_MODEL: &str = "Qwen/Qwen3-Embedding-4B";
const FIXTURE_PATH: &str = "/tmp/leankg-embed-switch-fixture";

fn env_guard() -> std::sync::MutexGuard<'static, ()> {
    ENV_LOCK.get_or_init(|| Mutex::new(())).lock().unwrap()
}

/// Live mock API health probe (fail fast with a clear message, not a hang).
fn assert_mock_serving() {
    let ok = std::net::TcpStream::connect_timeout(
        &"127.0.0.1:18080".parse().unwrap(),
        std::time::Duration::from_secs(2),
    )
    .is_ok();
    assert!(
        ok,
        "mock embed API not listening on {MOCK_BASE} — start it with: \
         python3 /tmp/leankg-mock-embed/mock_embed.py 18080"
    );
}

/// Create the fixture's derived schema (`schema_for_path`, same derivation
/// as the writer) + run all migrations; returns the base URL for row counts.
/// The writer pins itself to this schema via `init_db` → `with_schema`, so
/// no URL search_path trickery is needed (and none would work — the writer
/// always derives its schema from the project identity).
fn scoped_pg_url(base: &str) -> (String, String) {
    let schema = schema_for_path(std::path::Path::new(FIXTURE_PATH).join(".leankg").as_path());
    let mut admin = pg_connect(base).unwrap_or_else(|e| panic!("cannot connect to {base}: {e}"));
    admin
        .batch_execute(&format!("DROP SCHEMA IF EXISTS {schema} CASCADE"))
        .expect("drop schema");
    admin
        .batch_execute(&format!("CREATE SCHEMA {schema}"))
        .expect("create schema");
    admin
        .batch_execute(&format!("SET search_path TO {schema}, public"))
        .expect("set search_path");
    leankg::db::pg::migrations::run_migrations(&mut admin).expect("run_migrations");
    std::mem::forget(admin);
    (base.to_string(), schema)
}

fn drop_schema(base: &str, schema: &str) {
    if let Ok(mut admin) = pg_connect(base) {
        let _ = admin.batch_execute(&format!("DROP SCHEMA IF EXISTS {schema} CASCADE"));
    }
}

fn count_rows(base_url: &str, schema: &str, table: &str) -> i64 {
    let mut c = pg_connect(base_url).expect("count connect");
    c.query_one(&format!("SELECT count(*) FROM {schema}.{table}"), &[])
        .expect("count query")
        .get(0)
}

fn set_env(url: &str, key: &str, val: &str) {
    std::env::set_var("LEANKG_PG_URL", url);
    std::env::set_var(key, val);
}

fn unset_api_env() {
    std::env::remove_var("LEANKG_EMBED_API_BASE_URL");
    std::env::remove_var("LEANKG_EMBED_API_KEY");
    std::env::remove_var("LEANKG_EMBED_API_MODEL");
    std::env::remove_var("LEANKG_EMBED_API_DIM");
}

fn write_fixture(dir: &std::path::Path) {
    let src = dir.join("src");
    std::fs::create_dir_all(&src).unwrap();
    // Explicit project_path: the writer derives its PG schema from this key
    // (schema_for_path), keeping the fixture deterministic across runs.
    std::fs::write(
        dir.join("leankg.yaml"),
        "project:\n  name: leankg-embed-switch-fixture\n  root: .\n  project_path: /tmp/leankg-embed-switch-fixture\n  languages:\n  - rust\n",
    )
    .unwrap();
    std::fs::write(
        dir.join("Cargo.toml"),
        "[package]\nname = \"leankg-embed-switch-fixture\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
    )
    .unwrap();
    std::fs::write(
        src.join("lib.rs"),
        "pub fn hello() -> &'static str {\n    \"leankg embed switch smoke fixture\"\n}\n\npub fn add(a: i64, b: i64) -> i64 {\n    a + b\n}\n\npub fn greet(name: &str) -> String {\n    format!(\"hello {name}\")\n}\n",
    )
    .unwrap();
}

fn setup_graph(fixture: &std::path::Path) -> GraphEngine {
    let db_path = fixture.join(".leankg");
    std::fs::create_dir_all(&db_path).unwrap();
    let db = init_db(&db_path).expect("init_db");
    let graph = GraphEngine::new(db);
    let files = leankg::indexer::find_files_sync(fixture.to_str().unwrap()).expect("find_files");
    assert!(!files.is_empty(), "fixture scan found no files");
    leankg::indexer::index_files_parallel(&graph, &files, false).expect("index_files_parallel");
    let n = graph.count_elements().expect("count_elements");
    assert!(n >= 3, "expected >=3 elements in fixture, got {n}");
    eprintln!("indexed fixture: {n} elements");
    graph
}

fn run_embed(graph: &GraphEngine, types: &str) -> embeddings::BuildReport {
    let opts = BuildOptions {
        mode: BuildMode::Incremental,
        batch_size: 4,
        reserve_capacity: None,
        type_filter: parse_type_filter(types),
        summary_primary_enabled: false,
        summary_only_enabled: false,
        summary_primary_file_cap: embeddings::build::SUMMARY_PRIMARY_DEFAULT_FILE_CAP,
        file_size_cache: std::collections::HashMap::new(),
        partial: false,
        max_rss_mb_override: Some(2048),
        write_vectors: true,
    };
    build_index(graph, std::path::Path::new(""), &opts).expect("build_index")
}

fn run_semantic(graph: &GraphEngine, query: &str) -> usize {
    let mut pipeline =
        SemanticRetrievalPipeline::new(graph.db_arc().clone()).expect("pipeline::new");
    let opts = RetrieveOptions {
        env: Some("local".to_string()),
        ann_top_k: Some(5),
        rerank_top_n: 3,
        ..Default::default()
    };
    let result = pipeline.retrieve(query, &opts).expect("retrieve");
    let n = result.seeds.len();
    eprintln!(
        "semantic_search '{query}': {n} seeds (reranker={})",
        match result.reranker_status {
            leankg::embeddings::RerankerStatus::Active => "active",
            leankg::embeddings::RerankerStatus::Fallback => "FALLBACK (ANN-only)",
        }
    );
    n
}

#[test]
fn model_switch_preserves_collections() {
    // Opt-in live test: needs local `leankg-pg-phase0` on :5433 AND the mock
    // embed API on :18080. A bare LEANKG_PG_URL in the environment must NOT
    // silently aim this at a shared/company database.
    if std::env::var("LEANKG_MULTI_MODEL_LIVE").ok().as_deref() != Some("1") {
        eprintln!("skipping: set LEANKG_MULTI_MODEL_LIVE=1 to run this live suite");
        return;
    }
    let Some(base_url) = std::env::var("LEANKG_PG_URL")
        .ok()
        .filter(|v| !v.trim().is_empty())
    else {
        eprintln!("skipping: LEANKG_PG_URL not set");
        return;
    };
    let _g = env_guard();
    assert_mock_serving();

    let (count_url, schema) = scoped_pg_url(&base_url);

    let fixture = std::env::temp_dir().join("leankg-embed-switch-fixture");
    let _ = std::fs::remove_dir_all(&fixture);
    write_fixture(&fixture);

    eprintln!("== A. embed under bge-small-en-v1.5-384 (provider=local) ==");
    set_env(
        &count_url,
        "LEANKG_EMBED_ACTIVE_MODEL",
        "bge-small-en-v1.5-384",
    );
    std::env::set_var("LEANKG_EMBED_PROVIDER", "local");
    unset_api_env();
    let graph = setup_graph(&fixture);
    let rep = run_embed(&graph, "");
    eprintln!(
        "  embedded={} index_size={}",
        rep.embedded_count, rep.index_size
    );
    let count_a1 = count_rows(&count_url, &schema, "embedding_vectors");
    assert!(
        count_a1 > 0,
        "FAIL: embedding_vectors count is 0 after local embed"
    );
    eprintln!("  embedding_vectors rows={count_a1}");

    eprintln!("== B. switch to qwen3-emb-4b-2560 (provider=openai, mock) ==");
    set_env(&count_url, "LEANKG_EMBED_ACTIVE_MODEL", "qwen3-emb-4b-2560");
    std::env::set_var("LEANKG_EMBED_PROVIDER", "openai");
    std::env::set_var("LEANKG_EMBED_API_BASE_URL", MOCK_BASE);
    std::env::set_var("LEANKG_EMBED_API_KEY", "mock");
    std::env::set_var("LEANKG_EMBED_API_MODEL", MOCK_MODEL);
    std::env::set_var("LEANKG_EMBED_API_DIM", "2560");
    let rep = run_embed(&graph, "");
    eprintln!(
        "  embedded={} index_size={}",
        rep.embedded_count, rep.index_size
    );
    let count_b1 = count_rows(&count_url, &schema, "embedding_vectors_qwen3_emb_4b_2560");
    let count_a2 = count_rows(&count_url, &schema, "embedding_vectors");
    assert!(
        count_b1 > 0,
        "FAIL: qwen collection count is 0 after API embed"
    );
    assert_eq!(
        count_a2, count_a1,
        "FAIL: embedding_vectors changed after switch (A1={count_a1} -> A2={count_a2})"
    );
    eprintln!(
        "  embedding_vectors_qwen3_emb_4b_2560 rows={count_b1}  embedding_vectors still={count_a2}"
    );

    eprintln!("== C. flip pointer back to BGE (no re-embed) ==");
    set_env(
        &count_url,
        "LEANKG_EMBED_ACTIVE_MODEL",
        "bge-small-en-v1.5-384",
    );
    std::env::set_var("LEANKG_EMBED_PROVIDER", "local");
    unset_api_env();
    let count_a3 = count_rows(&count_url, &schema, "embedding_vectors");
    let count_b2 = count_rows(&count_url, &schema, "embedding_vectors_qwen3_emb_4b_2560");
    assert_eq!(
        count_a3, count_a1,
        "FAIL: BGE count changed after flip back (A1={count_a1} -> A3={count_a3})"
    );
    assert_eq!(
        count_b2, count_b1,
        "FAIL: qwen count changed after flip back (B1={count_b1} -> B2={count_b2})"
    );
    eprintln!("  embedding_vectors={count_a3}  embedding_vectors_qwen3_emb_4b_2560={count_b2}");

    eprintln!("== D. semantic_search under each active model ==");
    let n_a = run_semantic(&graph, "greet a person by name");
    assert!(n_a > 0, "FAIL: semantic_search under BGE returned no seeds");
    std::env::set_var("LEANKG_EMBED_ACTIVE_MODEL", "qwen3-emb-4b-2560");
    std::env::set_var("LEANKG_EMBED_PROVIDER", "openai");
    std::env::set_var("LEANKG_EMBED_API_BASE_URL", MOCK_BASE);
    std::env::set_var("LEANKG_EMBED_API_KEY", "mock");
    std::env::set_var("LEANKG_EMBED_API_MODEL", MOCK_MODEL);
    std::env::set_var("LEANKG_EMBED_API_DIM", "2560");
    let n_b = run_semantic(&graph, "greet a person by name");
    assert!(
        n_b > 0,
        "FAIL: semantic_search under qwen returned no seeds"
    );

    eprintln!("\nOK: switch smoke passed — both collections intact across A -> B -> A");
    eprintln!("  A: embedding_vectors={count_a1} -> {count_a2} -> {count_a3}");
    eprintln!("  B: embedding_vectors_qwen3_emb_4b_2560={count_b1} -> {count_b2}");

    // Cleanup: drop scratch schema (LEANKG_PG_URL restored by guard drop).
    drop_schema(&base_url, &schema);
    eprintln!("dropped scratch schema {schema}");
}
