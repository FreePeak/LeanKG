//! FR-BENCH-HNSW: deterministic CozoDB HNSW recall@k smoke test.
//!
//! Inserts synthetic 384-dim vectors (no fastembed / model download), queries
//! via `~embedding_vectors:vec_idx`, and asserts recall@k against brute-force
//! cosine ground truth.
//!
//! ```bash
//! cargo test --release --features embeddings --test hnsw_recall_e2e
//! ```

#![cfg(feature = "embeddings")]

use std::collections::HashSet;

use leankg::db::schema::{init_db, run_script, CozoDb};
use leankg::embeddings::state::ensure_embedding_state_table;

const DIM: usize = 384;
const N_VECTORS: usize = 48;
const K: usize = 10;
/// ANN can disagree with brute-force on near-tie ranks; 0.8 is enough for
/// smoke. The golden cluster membership assert below is the hard gate.
const RECALL_THRESHOLD: f32 = 0.8;

fn fresh_db() -> CozoDb {
    // Pin HNSW knobs before index create (m / ef_construction are bake-time).
    std::env::set_var("LEANKG_HNSW_M", "16");
    std::env::set_var("LEANKG_HNSW_EF_CONST", "40");
    std::env::set_var("LEANKG_HNSW_EF", "100");

    let tmp = tempfile::tempdir().expect("tempdir");
    let db_path = tmp.path().join("hnsw_recall.db");
    std::mem::forget(tmp);
    let db = init_db(&db_path).expect("init_db");
    ensure_embedding_state_table(&db).expect("ensure_embedding_state_table");
    db
}

/// Build a unit vector with energy concentrated on a few dimensions so
/// near-neighbors are easy to identify by cosine similarity.
fn make_vector(seed: usize, cluster: usize) -> Vec<f32> {
    let mut v = vec![0.0f32; DIM];
    // Cluster centroid axis — shared by all members of the cluster.
    let axis = (cluster * 7) % DIM;
    v[axis] = 1.0;
    // Per-item perturbation on a secondary axis so vectors are distinct.
    let pert = (seed * 13 + cluster * 3) % DIM;
    if pert != axis {
        v[pert] = 0.15;
    }
    // Tiny unique fingerprint so no two vectors are identical.
    v[seed % DIM] += 0.01 * ((seed % 17) as f32 + 1.0);
    l2_normalize(&mut v);
    v
}

fn l2_normalize(v: &mut [f32]) {
    let norm: f32 = v.iter().map(|x| x * x).sum::<f32>().sqrt();
    if norm > 0.0 {
        for x in v.iter_mut() {
            *x /= norm;
        }
    }
}

fn cosine_similarity(a: &[f32], b: &[f32]) -> f32 {
    a.iter().zip(b.iter()).map(|(x, y)| x * y).sum()
}

fn vec_literal(v: &[f32]) -> String {
    v.iter()
        .map(|f| format!("{:.6}", f))
        .collect::<Vec<_>>()
        .join(", ")
}

fn put_vectors(db: &CozoDb, items: &[(String, Vec<f32>)]) {
    let rows: Vec<String> = items
        .iter()
        .map(|(qn, vector)| {
            format!(
                "[{}, vec([{}])]",
                serde_json::Value::String(qn.clone()),
                vec_literal(vector)
            )
        })
        .collect();
    let values_clause = rows.join(", ");
    let query = format!(
        r#"?[qualified_name, vector] <- [{values_clause}]
           :put embedding_vectors {{qualified_name => vector}}"#
    );
    run_script(db, &query, Default::default()).expect("put embedding_vectors");
}

fn hnsw_query(db: &CozoDb, qvec: &[f32], k: usize) -> Vec<String> {
    let ef: usize = std::env::var("LEANKG_HNSW_EF")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(100);
    let query = format!(
        r#"?[dist, qualified_name] := ~embedding_vectors:vec_idx {{
                qualified_name |
                query: vec([{vec}]),
                k: {k},
                ef: {ef},
                bind_distance: dist
            }}"#,
        vec = vec_literal(qvec),
    );
    let result = run_script(db, &query, Default::default()).expect("hnsw query");
    result
        .rows
        .iter()
        .filter_map(|row| {
            row.get(1)
                .and_then(|v| v.get_str())
                .map(|s| s.to_string())
                .filter(|s| !s.is_empty())
        })
        .collect()
}

fn brute_force_top_k(items: &[(String, Vec<f32>)], qvec: &[f32], k: usize) -> Vec<String> {
    let mut scored: Vec<(f32, &str)> = items
        .iter()
        .map(|(qn, v)| (cosine_similarity(qvec, v), qn.as_str()))
        .collect();
    // Higher cosine similarity = closer (Cosine distance in Cozo is 1 - sim).
    scored.sort_by(|a, b| b.0.partial_cmp(&a.0).unwrap_or(std::cmp::Ordering::Equal));
    scored
        .into_iter()
        .take(k)
        .map(|(_, qn)| qn.to_string())
        .collect()
}

fn recall_at_k(hnsw: &[String], brute: &[String]) -> f32 {
    if brute.is_empty() {
        return 1.0;
    }
    let hnsw_set: HashSet<&str> = hnsw.iter().map(|s| s.as_str()).collect();
    let hit = brute
        .iter()
        .filter(|qn| hnsw_set.contains(qn.as_str()))
        .count();
    hit as f32 / brute.len() as f32
}

#[test]
fn hnsw_recall_at_k_meets_threshold() {
    let db = fresh_db();

    // 6 clusters × 8 vectors = 48. Golden query sits near cluster 0.
    let mut items: Vec<(String, Vec<f32>)> = Vec::with_capacity(N_VECTORS);
    for i in 0..N_VECTORS {
        let cluster = i % 6;
        let qn = format!("src/cluster{cluster}.rs::fn_{i}");
        items.push((qn, make_vector(i, cluster)));
    }
    put_vectors(&db, &items);

    // Query = near-centroid of cluster 0 (same axis, tiny noise).
    let mut query = make_vector(0, 0);
    // Nudge slightly toward another cluster-0 member so the golden QN is
    // not trivially identical to the query vector itself.
    let golden_qn = "src/cluster0.rs::fn_6".to_string();
    let golden_vec = items
        .iter()
        .find(|(qn, _)| qn == &golden_qn)
        .expect("golden vector present")
        .1
        .clone();
    for (q, g) in query.iter_mut().zip(golden_vec.iter()) {
        *q = 0.7 * *q + 0.3 * *g;
    }
    l2_normalize(&mut query);

    let brute = brute_force_top_k(&items, &query, K);
    let hnsw = hnsw_query(&db, &query, K);

    assert_eq!(
        hnsw.len(),
        K,
        "HNSW should return exactly k={K} hits, got {}",
        hnsw.len()
    );

    let recall = recall_at_k(&hnsw, &brute);
    assert!(
        recall >= RECALL_THRESHOLD,
        "HNSW recall@{K}={recall:.3} < {RECALL_THRESHOLD}; brute={brute:?} hnsw={hnsw:?}"
    );

    // Hard golden gate (FR-BENCH-HNSW): every cluster-0 qualified_name must
    // appear in top-k when the query sits near that cluster.
    let golden_qns: Vec<String> = items
        .iter()
        .filter(|(qn, _)| qn.starts_with("src/cluster0.rs::"))
        .map(|(qn, _)| qn.clone())
        .collect();
    assert_eq!(golden_qns.len(), 8, "expected 8 cluster0 vectors");
    let missing: Vec<&str> = golden_qns
        .iter()
        .filter(|qn| !hnsw.contains(qn))
        .map(|s| s.as_str())
        .collect();
    assert!(
        missing.is_empty(),
        "HNSW top-{K} missing cluster0 golden QNs {missing:?}; got {hnsw:?}"
    );
    assert!(
        hnsw.contains(&golden_qn),
        "expected blended golden QN {golden_qn} in HNSW top-{K}; got {hnsw:?}"
    );
}

#[test]
fn hnsw_exact_neighbor_is_rank_one() {
    let db = fresh_db();

    let mut items: Vec<(String, Vec<f32>)> = Vec::with_capacity(32);
    for i in 0..32 {
        let cluster = i % 4;
        items.push((
            format!("exact/c{cluster}::item_{i}"),
            make_vector(i + 100, cluster),
        ));
    }
    put_vectors(&db, &items);

    // Query with the exact stored vector for item 0 — must be distance ~0 / rank 1.
    let target_qn = "exact/c0::item_0".to_string();
    let qvec = items
        .iter()
        .find(|(qn, _)| qn == &target_qn)
        .expect("target")
        .1
        .clone();

    let hnsw = hnsw_query(&db, &qvec, 5);
    assert_eq!(
        hnsw.first().map(|s| s.as_str()),
        Some(target_qn.as_str()),
        "exact self-query must rank #1; got {hnsw:?}"
    );
}
