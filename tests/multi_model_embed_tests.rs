//! Multi-model embedding integration tests (table-per-model).
//!
//! Proves:
//! 1. Local BGE 384-d (default active model) embeds into the LEGACY
//!    `embedding_vectors` / `embedding_state` tables.
//! 2. Remote OpenAI-compatible qwen3-emb-4b-2560 embeds into its OWN
//!    `embedding_vectors_qwen3_emb_4b_2560` collection, against a mock
//!    `POST /embeddings` TCP stub (mirrors provider.rs unit test).
//! 3. Switching active model A → B → A preserves both collections' counts
//!    (the CLI smoke script scripts/smoke-embed-model-switch.sh semantics).
//!
//! Run (needs the local dev Postgres on :5433, e.g. `leankg-pg-phase0`):
//! ```bash
//! cargo test --features embeddings --test multi_model_embed_tests
//! ```

#![cfg(feature = "embeddings")]

use leankg::db::backend::init_db;
use leankg::embeddings::provider::{validate_provider, EmbedProvider};
use leankg::embeddings::registry::{
    create_provider_for_active_model, lookup_model, resolve_active_model,
    vectors_relation_for_model_id, DEFAULT_BGE_MODEL_ID,
};
use leankg::embeddings::state::{ensure_embedding_state_table, upsert_fresh, FreshRow};
use std::io::{Read, Write};
use std::net::TcpListener;
use std::sync::{Mutex, OnceLock};
use tempfile::TempDir;

const QWEN_MODEL_ID: &str = "qwen3-emb-4b-2560";
const QWEN_DIM: usize = 2560;
const LEGACY_VECTORS: &str = "embedding_vectors";
const QWEN_VECTORS: &str = "embedding_vectors_qwen3_emb_4b_2560";
const LEGACY_STATE: &str = "embedding_state";
const QWEN_STATE: &str = "embedding_state_qwen3_emb_4b_2560";

/// Serialize env-mutating tests (env is process-global; cargo runs tests in
/// parallel threads). Same pattern as provider.rs / registry.rs unit tests.
fn env_lock() -> MutexGuard<'static, ()> {
    static LOCK: OnceLock<Mutex<()>> = OnceLock::new();
    // into_inner: a panicking test must not poison env for the rest of the
    // suite (PoisonError cascades).
    LOCK.get_or_init(|| Mutex::new(()))
        .lock()
        .unwrap_or_else(|e| e.into_inner())
}
use std::sync::MutexGuard;

/// Skip (not fail) when the local dev Postgres is unreachable.
fn require_pg() -> Option<()> {
    match std::env::var("LEANKG_PG_URL") {
        Ok(v) if !v.trim().is_empty() => Some(()),
        _ => {
            // Unset: init_db would still build a PostgresBackend (lazy
            // connect), so probe the default dev URL directly.
            let Ok(mut c) = postgres::Client::connect(
                "postgresql://postgres:postgres@localhost:5433/leankg",
                postgres::NoTls,
            ) else {
                eprintln!(
                    "skipping: no Postgres on :5433 (set LEANKG_PG_URL or start leankg-pg-phase0)"
                );
                return None;
            };
            let _ = c.batch_execute("SELECT 1");
            Some(())
        }
    }
}

fn fresh_db() -> leankg::db::backend::SharedDb {
    let tmp = TempDir::new().expect("tempdir");
    let db_path = tmp.path().join("test.db");
    std::mem::forget(tmp);
    init_db(&db_path).expect("init_db")
}

fn count_rows(db: &dyn leankg::db::backend::DbBackend, rel: &str) -> usize {
    let result = db
        .run_script(&format!("?[count(qn)] := *{rel}[qn]"), Default::default())
        .expect("count query");
    result
        .rows
        .first()
        .and_then(|row| row.first().and_then(|v| v.get_int()))
        .unwrap_or(0) as usize
}

fn seed_state(db: &dyn leankg::db::backend::DbBackend, qns: &[&str]) {
    let rows: Vec<FreshRow> = qns
        .iter()
        .enumerate()
        .map(|(i, qn)| FreshRow {
            qualified_name: qn.to_string(),
            usearch_key: i as u64 + 1,
            content_hash: format!("hash-{qn}"),
        })
        .collect();
    upsert_fresh(db, &rows).expect("upsert_fresh");
}

/// Mock OpenAI-compatible `/embeddings` TCP stub on an ephemeral port.
/// Accepts `connections` requests, verifying each, and replies with a JSON
/// body whose embedding has EXACTLY `dim` floats. Mirrors the in-process
/// one-shot stub in provider.rs
/// (`openai_compatible_provider_posts_and_parses_vectors`), extended to
/// serve repeated embed_batch calls. The base URL is a bare origin; the
/// provider appends `/embeddings` (no `/v1` segment — the version prefix is
/// part of the base, e.g. `https://api.openai.com/v1`).
fn mock_embeddings_server(dim: usize, connections: usize) -> (String, std::thread::JoinHandle<()>) {
    let listener = TcpListener::bind("127.0.0.1:0").expect("bind");
    let addr = listener.local_addr().unwrap();
    let handle = std::thread::spawn(move || {
        for _ in 0..connections {
            let (mut stream, _) = listener.accept().expect("accept");
            let mut buf = [0u8; 8192];
            let n = stream.read(&mut buf).expect("read request");
            let req = String::from_utf8_lossy(&buf[..n]);
            assert!(
                req.contains("POST /embeddings"),
                "expected embeddings path, got: {req}"
            );
            assert!(req.contains("Bearer sk-test"), "missing auth: {req}");
            assert!(req.contains("\"model\""), "missing model: {req}");

            let embedding: Vec<f32> = (0..dim).map(|i| i as f32 * 0.001).collect();
            let body = serde_json::json!({
                "data": [{ "embedding": embedding, "index": 0 }]
            });
            let body_str = body.to_string();
            let resp = format!(
                "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
                body_str.len(),
                body_str
            );
            stream.write_all(resp.as_bytes()).expect("write response");
        }
    });
    (format!("http://{addr}"), handle)
}

/// qwen entry dims come from the registry, not a hardcoded 384.
#[test]
fn registry_env_selects_qwen_with_registry_dim() {
    let _g = env_lock();
    std::env::set_var("LEANKG_EMBED_ACTIVE_MODEL", QWEN_MODEL_ID);
    let entry = resolve_active_model().expect("qwen resolves");
    std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
    assert_eq!(entry.dimensions, QWEN_DIM);
    assert_eq!(
        entry.provider,
        leankg::embeddings::registry::RegistryProviderKind::OpenAi
    );
    assert_eq!(entry.vectors_relation(), QWEN_VECTORS);
    assert_eq!(entry.state_relation(), QWEN_STATE);
}

#[test]
fn registry_bge_keeps_legacy_relation_names() {
    let entry = lookup_model(DEFAULT_BGE_MODEL_ID).expect("bge registered");
    assert_eq!(entry.dimensions, 384);
    assert_eq!(entry.vectors_relation(), LEGACY_VECTORS);
    assert_eq!(entry.state_relation(), LEGACY_STATE);
    assert_eq!(vectors_relation_for_model_id(QWEN_MODEL_ID), QWEN_VECTORS);
}

#[test]
fn registry_unknown_active_model_errors() {
    let _g = env_lock();
    std::env::set_var("LEANKG_EMBED_ACTIVE_MODEL", "no-such-model");
    let err = resolve_active_model().expect_err("unknown model");
    std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
    assert!(err.to_string().contains("no-such-model"));
}

#[test]
fn registry_sanitize_model_id_for_table() {
    assert_eq!(
        leankg::embeddings::registry::sanitize_model_id_for_table("qwen3-emb-4b-2560"),
        "qwen3_emb_4b_2560"
    );
}

/// Gemini entries resolve to 3072-d OpenAI-compatible collections (Google's
/// default output dim — the provider sends no `output_dimensionality`).
#[test]
fn registry_gemini_entries_resolve_with_3072() {
    for (model_id, model_name) in [
        ("gemini-embedding-2-3072", "gemini-embedding-2"),
        ("gemini-embedding-001-3072", "gemini-embedding-001"),
    ] {
        let entry = lookup_model(model_id).expect("gemini entry resolves");
        assert_eq!(entry.dimensions, 3072);
        assert_eq!(entry.model_name, model_name);
        assert_eq!(
            entry.provider,
            leankg::embeddings::registry::RegistryProviderKind::OpenAi
        );
        assert_eq!(
            entry.vectors_relation(),
            leankg::embeddings::registry::vectors_relation_for_model_id(model_id)
        );
    }
}

/// OpenAI provider against the mock stub: 2560-d provider, 2560-vector back.
#[test]
fn openai_provider_mock_stub_embeds_2560_vector() {
    let (base, handle) = mock_embeddings_server(QWEN_DIM, 1);
    let provider = leankg::embeddings::provider::OpenAiCompatibleProvider::new(
        base,
        "sk-test",
        "Qwen/Qwen3-Embedding-4B",
        QWEN_DIM,
    )
    .expect("provider");
    validate_provider(&provider, QWEN_DIM).expect("dim ok");
    let vecs = provider
        .embed_batch(&[String::from("hello")])
        .expect("embed_batch");
    assert_eq!(vecs.len(), 1);
    assert_eq!(vecs[0].len(), QWEN_DIM);
    assert!((vecs[0][1] - 0.001).abs() < 1e-6, "value roundtrip");
    handle.join().expect("stub thread");
}

/// validate_provider rejects a 384-d provider when the expected dim is 2560.
#[test]
fn validate_provider_rejects_384_against_2560() {
    let fake = leankg::embeddings::provider::FakeEmbedProvider::new(384);
    let err = validate_provider(&fake, QWEN_DIM).expect_err("must reject");
    let msg = err.to_string();
    assert!(msg.contains("384"), "msg: {msg}");
    assert!(msg.contains("2560"), "msg: {msg}");
}

/// Registry dim flows into the factory-built OpenAI provider.
#[test]
fn create_provider_for_active_model_uses_registry_dim() {
    let _g = env_lock();
    let (base, handle) = mock_embeddings_server(QWEN_DIM, 1);
    std::env::set_var("LEANKG_EMBED_ACTIVE_MODEL", QWEN_MODEL_ID);
    std::env::set_var("LEANKG_EMBED_PROVIDER", "openai");
    std::env::set_var("LEANKG_EMBED_API_BASE_URL", base);
    std::env::set_var("LEANKG_EMBED_API_KEY", "sk-test");
    std::env::set_var("LEANKG_EMBED_API_MODEL", "Qwen/Qwen3-Embedding-4B");
    std::env::remove_var("LEANKG_EMBED_API_DIM");

    let result = create_provider_for_active_model();
    std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
    std::env::remove_var("LEANKG_EMBED_PROVIDER");
    std::env::remove_var("LEANKG_EMBED_API_BASE_URL");
    std::env::remove_var("LEANKG_EMBED_API_KEY");
    std::env::remove_var("LEANKG_EMBED_API_MODEL");

    let provider = result.expect("factory");
    assert_eq!(provider.dimensions(), QWEN_DIM);
    let v =
        leankg::embeddings::provider::embed_query(provider.as_ref(), "hello").expect("query embed");
    assert_eq!(v.len(), QWEN_DIM);
    handle.join().expect("stub thread");
}

/// ensure_embedding_state_table routes by active model: qwen → table-per-model,
/// BGE → legacy tables. Both tables must be independently writable and the
/// ANN index on the qwen collection must carry the registry dim.
#[test]
fn ensure_tables_are_created_per_active_model() {
    let _g = env_lock();
    let Some(()) = require_pg() else { return };

    // qwen collections first.
    std::env::set_var("LEANKG_EMBED_ACTIVE_MODEL", QWEN_MODEL_ID);
    {
        let db = fresh_db();
        ensure_embedding_state_table(db.as_ref()).expect("ensure qwen");
        ensure_embedding_state_table(db.as_ref()).expect("idempotent");
        let _ = db.run_script("::relations", Default::default()).map(|r| {
            let rels: Vec<String> = r
                .rows
                .iter()
                .filter_map(|row| row.first().and_then(|v| v.get_str().map(String::from)))
                .collect();
            assert!(
                rels.contains(&QWEN_VECTORS.to_string()),
                "qwen vectors relation missing: {rels:?}"
            );
            assert!(
                rels.contains(&QWEN_STATE.to_string()),
                "qwen state relation missing: {rels:?}"
            );
        });
        // qwen writes must land in the per-model table.
        seed_state(db.as_ref(), &["q1", "q2"]);
        assert_eq!(count_rows(db.as_ref(), QWEN_STATE), 2);
        assert_eq!(count_rows(db.as_ref(), LEGACY_STATE), 0);
    }

    // BGE default → legacy tables.
    std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
    {
        let db = fresh_db();
        ensure_embedding_state_table(db.as_ref()).expect("ensure bge");
        seed_state(db.as_ref(), &["b1", "b2", "b3"]);
        assert_eq!(count_rows(db.as_ref(), LEGACY_STATE), 3);
        assert_eq!(count_rows(db.as_ref(), QWEN_STATE), 0);
    }
}

/// End-to-end: local BGE provider (fake, 384-d) writes 384-vectors into the
/// LEGACY `embedding_vectors` table. State upsert + vector import are the
/// real embed-write paths.
#[test]
fn bge_local_provider_embeds_into_legacy_vectors_table() {
    let _g = env_lock();
    let Some(()) = require_pg() else { return };

    std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
    std::env::remove_var("LEANKG_EMBED_PROVIDER");
    let entry = resolve_active_model().expect("default bge");
    assert_eq!(entry.vectors_relation(), LEGACY_VECTORS);

    let db = fresh_db();
    ensure_embedding_state_table(db.as_ref()).expect("ensure");

    let fake = leankg::embeddings::provider::FakeEmbedProvider::new(entry.dimensions);
    validate_provider(&fake, entry.dimensions).expect("384-d fake ok");

    let pairs: Vec<(String, Vec<f32>)> = (0..3)
        .map(|i| {
            (
                format!("src/bge{i}.rs::fn{i}"),
                fake.embed_batch(&[format!("blob-{i}")]).expect("embed")[0].clone(),
            )
        })
        .collect();
    for (qn, v) in &pairs {
        assert_eq!(v.len(), 384, "bge vectors must be 384-d");
        upsert_vectors(db.as_ref(), LEGACY_VECTORS, qn, v).expect("upsert vector");
        let _ = upsert_fresh(
            db.as_ref(),
            &[FreshRow {
                qualified_name: qn.clone(),
                usearch_key: 0,
                content_hash: format!("h-{qn}"),
            }],
        );
    }
    assert_eq!(count_rows(db.as_ref(), LEGACY_VECTORS), 3);
    assert_eq!(count_rows(db.as_ref(), LEGACY_STATE), 3);
    assert_eq!(count_rows(db.as_ref(), QWEN_VECTORS), 0);
}

/// End-to-end: qwen provider against the mock HTTP stub embeds 2560-vectors
/// into its OWN table; the legacy table stays untouched.
#[test]
fn qwen_remote_provider_embeds_into_own_vectors_table() {
    let _g = env_lock();
    let Some(()) = require_pg() else { return };

    let (base, handle) = mock_embeddings_server(QWEN_DIM, 2);
    std::env::set_var("LEANKG_EMBED_ACTIVE_MODEL", QWEN_MODEL_ID);
    std::env::set_var("LEANKG_EMBED_PROVIDER", "openai");
    std::env::set_var("LEANKG_EMBED_API_BASE_URL", base);
    std::env::set_var("LEANKG_EMBED_API_KEY", "sk-test");
    std::env::set_var("LEANKG_EMBED_API_MODEL", "Qwen/Qwen3-Embedding-4B");
    std::env::remove_var("LEANKG_EMBED_API_DIM");

    let provider = create_provider_for_active_model().expect("qwen provider");
    assert_eq!(provider.dimensions(), QWEN_DIM);

    let db = fresh_db();
    ensure_embedding_state_table(db.as_ref()).expect("ensure qwen");

    let pairs: Vec<(String, Vec<f32>)> = (0..2)
        .map(|i| {
            let v = provider.embed_batch(&[format!("blob-{i}")]).expect("embed")[0].clone();
            assert_eq!(v.len(), QWEN_DIM, "qwen vectors must be 2560-d");
            (format!("src/qwen{i}.rs::fn{i}"), v)
        })
        .collect();
    for (qn, v) in &pairs {
        upsert_vectors(db.as_ref(), QWEN_VECTORS, qn, v).expect("upsert vector");
        let _ = upsert_fresh(
            db.as_ref(),
            &[FreshRow {
                qualified_name: qn.clone(),
                usearch_key: 0,
                content_hash: format!("h-{qn}"),
            }],
        );
    }
    assert_eq!(count_rows(db.as_ref(), QWEN_VECTORS), 2);
    assert_eq!(count_rows(db.as_ref(), QWEN_STATE), 2);
    assert_eq!(count_rows(db.as_ref(), LEGACY_VECTORS), 0);

    // clean env for the next test
    std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
    std::env::remove_var("LEANKG_EMBED_PROVIDER");
    std::env::remove_var("LEANKG_EMBED_API_BASE_URL");
    std::env::remove_var("LEANKG_EMBED_API_KEY");
    std::env::remove_var("LEANKG_EMBED_API_MODEL");
    handle.join().expect("stub thread");
}

/// A → B → A switch (smoke-embed-model-switch.sh semantics): each model's
/// collection count is preserved across active-model flips. Vectors already
/// stored under model A must survive embedding under B and flipping back.
#[test]
fn switching_active_model_preserves_both_collections() {
    let _g = env_lock();
    let Some(()) = require_pg() else { return };

    // --- A: local BGE into legacy tables ---
    std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
    std::env::remove_var("LEANKG_EMBED_PROVIDER");
    let bge = resolve_active_model().expect("bge");
    let bge_provider = leankg::embeddings::provider::FakeEmbedProvider::new(bge.dimensions);

    let db = fresh_db();
    ensure_embedding_state_table(db.as_ref()).expect("ensure bge");
    let a_rows: Vec<(String, Vec<f32>)> = (0..2)
        .map(|i| {
            let v = bge_provider
                .embed_batch(&[format!("a-blob-{i}")])
                .expect("embed")[0]
                .clone();
            (format!("src/a{i}.rs::fn{i}"), v)
        })
        .collect();
    for (qn, v) in &a_rows {
        upsert_vectors(db.as_ref(), LEGACY_VECTORS, qn, v).expect("upsert a");
        seed_state(db.as_ref(), &[qn.as_str()]);
    }
    let count_a1 = count_rows(db.as_ref(), LEGACY_VECTORS);
    assert_eq!(count_a1, 2);

    // --- B: qwen via mock stub into its own tables ---
    let (base, handle_b) = mock_embeddings_server(QWEN_DIM, 3);
    std::env::set_var("LEANKG_EMBED_ACTIVE_MODEL", QWEN_MODEL_ID);
    std::env::set_var("LEANKG_EMBED_PROVIDER", "openai");
    std::env::set_var("LEANKG_EMBED_API_BASE_URL", base);
    std::env::set_var("LEANKG_EMBED_API_KEY", "sk-test");
    std::env::set_var("LEANKG_EMBED_API_MODEL", "Qwen/Qwen3-Embedding-4B");
    std::env::remove_var("LEANKG_EMBED_API_DIM");

    let qwen = create_provider_for_active_model().expect("qwen provider");
    ensure_embedding_state_table(db.as_ref()).expect("ensure qwen");
    let b_rows: Vec<(String, Vec<f32>)> = (0..3)
        .map(|i| {
            let v = qwen.embed_batch(&[format!("b-blob-{i}")]).expect("embed")[0].clone();
            assert_eq!(v.len(), QWEN_DIM);
            (format!("src/b{i}.rs::fn{i}"), v)
        })
        .collect();
    for (qn, v) in &b_rows {
        upsert_vectors(db.as_ref(), QWEN_VECTORS, qn, v).expect("upsert b");
        seed_state(db.as_ref(), &[qn.as_str()]);
    }
    let count_b1 = count_rows(db.as_ref(), QWEN_VECTORS);
    assert_eq!(count_b1, 3);
    handle_b.join().expect("stub thread b");

    // A's collection untouched by the B embed.
    let count_a2 = count_rows(db.as_ref(), LEGACY_VECTORS);
    assert_eq!(count_a2, count_a1, "A count changed after switch to B");

    // --- back to A: pointer-only flip, no re-embed ---
    std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
    std::env::remove_var("LEANKG_EMBED_PROVIDER");
    std::env::remove_var("LEANKG_EMBED_API_BASE_URL");
    std::env::remove_var("LEANKG_EMBED_API_KEY");
    std::env::remove_var("LEANKG_EMBED_API_MODEL");

    let count_a3 = count_rows(db.as_ref(), LEGACY_VECTORS);
    let count_b2 = count_rows(db.as_ref(), QWEN_VECTORS);
    assert_eq!(count_a3, count_a1, "A count changed after flip back");
    assert_eq!(count_b2, count_b1, "B count changed after flip back");
}

/// Named-row upsert of a (qualified_name, vector) pair into `rel`
/// (legacy `embedding_vectors` or a per-model `embedding_vectors_*` table).
fn upsert_vectors(
    db: &dyn leankg::db::backend::DbBackend,
    rel: &str,
    qn: &str,
    vector: &[f32],
) -> Result<(), Box<dyn std::error::Error>> {
    use leankg::db::backend::{DataValue, NamedRows};
    let list: Vec<DataValue> = vector.iter().map(|&f| DataValue::from(f as f64)).collect();
    let named = NamedRows::new(
        vec!["qualified_name".to_string(), "vector".to_string()],
        vec![vec![DataValue::Str(qn.into()), DataValue::List(list)]],
    );
    let mut map = std::collections::BTreeMap::new();
    map.insert(rel.to_string(), named);
    db.import_relations(map)
}
