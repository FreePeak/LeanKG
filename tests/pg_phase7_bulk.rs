//! Phase 7 — embedding bulk-load via COPY (plan T7.1-T7.2).
//!
//! Verifies the `PostgresBackend::import_relations` COPY bulk path:
//!   - COPY of 10k dim-384 vectors lands with correct row count
//!   - re-COPY with the same qualified_names upserts (count unchanged, data
//!     updated)
//!   - drop-index-during-bulk + reindex restores HNSW recall >= 98%
//!   - throughput measurement (assert >= 3k v/s; Phase 4 measured 3.8-4k)
//!
//! Requires the Phase 0 Postgres container (Postgres 18 + pgvector):
//!   docker exec leankg-pg-phase0 psql -U postgres -d leankg -c "CREATE EXTENSION IF NOT EXISTS vector;"
//!
//! Run only these:
//!   LEANKG_PG_URL=postgresql://postgres:postgres@localhost:5433/leankg \
//!     cargo test --release --test pg_phase7_bulk -- --include-ignored --test-threads=1
//!
//! Every test is `#[ignore]`-gated; flip with `--include-ignored`.

#[allow(unused_imports)]
use leankg::db::backend::pg_connect;
use leankg::db::backend::{ClientPool, PostgresBackend};
use std::collections::BTreeMap;
use std::env;
use std::io::Write;
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::Mutex;
use std::time::Instant;

/// Serialize integration tests: each DROPs/CREATEs a shared scratch schema.
/// Same PG_LOCK pattern as the other pg_* test files.
static PG_LOCK: Mutex<()> = Mutex::new(());

fn pg_lock() -> std::sync::MutexGuard<'static, ()> {
    PG_LOCK.lock().unwrap_or_else(|e| e.into_inner())
}

fn pg_url() -> String {
    env::var("LEANKG_PG_URL")
        .unwrap_or_else(|_| "postgresql://postgres:postgres@localhost:5433/leankg".to_string())
}

const VEC_DIM: usize = 384;

fn scratch_schema_name() -> String {
    static COUNTER: AtomicU32 = AtomicU32::new(0);
    format!(
        "leankg_p7_{}_{}",
        std::process::id(),
        COUNTER.fetch_add(1, Ordering::Relaxed)
    )
}

/// A scratch schema in the dev PG container, dropped on test exit. All
/// backends built from it pin `search_path` so tests never touch `public`.
struct Scratch {
    admin: postgres::Client,
    name: String,
    rw_url: String,
}

impl Scratch {
    fn new() -> Self {
        let base = pg_url();
        let name = scratch_schema_name();
        let mut admin =
            pg_connect(&base).unwrap_or_else(|e| panic!("cannot connect to {base}: {e}"));
        admin
            .batch_execute(&format!("DROP SCHEMA IF EXISTS {name} CASCADE"))
            .unwrap();
        admin
            .batch_execute(&format!("CREATE SCHEMA {name}"))
            .unwrap();
        admin
            .batch_execute(&format!("SET search_path TO {name}, public"))
            .unwrap();
        leankg::db::pg::migrations::run_migrations(&mut admin).unwrap();

        let sep = if base.contains('?') { '&' } else { '?' };
        let rw_url = format!("{base}{sep}options=-csearch_path%3D{name}%2Cpublic");
        Scratch {
            admin,
            name,
            rw_url,
        }
    }

    fn rw_backend(&self) -> std::sync::Arc<PostgresBackend> {
        std::sync::Arc::new(PostgresBackend {
            pg_url: self.rw_url.clone(),
            schema: Some(self.name.clone()),
            pool: std::sync::Arc::new(ClientPool::new(2)),
            ro_pool: std::sync::Arc::new(ClientPool::new(2)),
            read_only: false,
            write_bus: None,
        })
    }
}

impl Drop for Scratch {
    fn drop(&mut self) {
        let _ = self
            .admin
            .batch_execute(&format!("DROP SCHEMA IF EXISTS {} CASCADE", self.name));
    }
}

/// Deterministic PRNG (xoshiro256++-style, same as the Phase 0 spike).
struct Rng(u64);

impl Rng {
    fn new(seed: u64) -> Self {
        Rng(seed)
    }
    fn next_u64(&mut self) -> u64 {
        let mut x = self.0;
        x ^= x << 7;
        x ^= x >> 9;
        self.0 = x;
        x.wrapping_mul(0x9E37_79B9_7F4A_7C15)
    }
    fn next_f64(&mut self) -> f64 {
        (self.next_u64() >> 11) as f64 / (1u64 << 53) as f64
    }
}

/// Unit-norm random vector in R^VEC_DIM.
fn random_unit_vector(rng: &mut Rng, dim: usize) -> Vec<f32> {
    let mut v: Vec<f32> = (0..dim)
        .map(|_| {
            let u1 = rng.next_f64().max(f64::EPSILON);
            let u2 = rng.next_f64();
            ((-2.0 * u1.ln()).sqrt() * (2.0 * std::f64::consts::PI * u2).cos()) as f32
        })
        .collect();
    let norm: f32 = v.iter().map(|x| x * x).sum::<f32>().sqrt();
    for x in &mut v {
        *x /= norm;
    }
    v
}

/// Cosine distance = 1 - dot for unit vectors.
fn cosine_dist(a: &[f32], b: &[f32]) -> f64 {
    1.0 - a
        .iter()
        .zip(b)
        .map(|(x, y)| (*x as f64) * (*y as f64))
        .sum::<f64>()
}

fn jaccard(a: &[String], b: &[String]) -> f64 {
    let sa: std::collections::HashSet<&String> = a.iter().collect();
    let sb: std::collections::HashSet<&String> = b.iter().collect();
    let inter = sa.intersection(&sb).count();
    inter as f64 / sa.union(&sb).count() as f64
}

/// Build the NamedRows map `embedding_vectors -> (qualified_name, vector)`
/// that `import_relations` consumes — mirrors build.rs `upsert_pairs_to_db`.
fn named_vectors(pairs: &[(String, Vec<f32>)]) -> BTreeMap<String, leankg::db::backend::NamedRows> {
    use leankg::db::backend::DataValue;
    let mut rows: Vec<Vec<DataValue>> = Vec::with_capacity(pairs.len());
    for (qn, vec) in pairs {
        let mut row = Vec::with_capacity(2);
        row.push(DataValue::Str(qn.as_str().into()));
        let mut list = Vec::with_capacity(vec.len());
        for &f in vec.iter() {
            list.push(DataValue::from(f as f64));
        }
        row.push(DataValue::List(list));
        rows.push(row);
    }
    let named = leankg::db::backend::NamedRows::new(
        vec!["qualified_name".to_string(), "vector".to_string()],
        rows,
    );
    let mut map = BTreeMap::new();
    map.insert("embedding_vectors".to_string(), named);
    map
}

/// Build the NamedRows map for `embedding_state` (mirrors state.rs
/// `upsert_fresh`).
fn named_state(
    updates: &[(String, u64, String)],
) -> BTreeMap<String, leankg::db::backend::NamedRows> {
    use leankg::db::backend::DataValue;
    let now = "2026-08-05T00:00:00Z".to_string();
    let mut rows: Vec<Vec<DataValue>> = Vec::with_capacity(updates.len());
    for (qn, key, hash) in updates {
        rows.push(vec![
            DataValue::Str(qn.as_str().into()),
            DataValue::from(*key as i64),
            DataValue::Str(hash.as_str().into()),
            DataValue::Str("fresh".into()),
            DataValue::Str(now.as_str().into()),
        ]);
    }
    let named = leankg::db::backend::NamedRows::new(
        vec![
            "qualified_name".to_string(),
            "usearch_key".to_string(),
            "content_hash".to_string(),
            "state".to_string(),
            "embedded_at".to_string(),
        ],
        rows,
    );
    let mut map = BTreeMap::new();
    map.insert("embedding_state".to_string(), named);
    map
}

/// T7.1 — COPY bulk load of 10k dim-384 vectors: all rows land, and the
/// cold-bulk path (HNSW index dropped, as `leankg embed` does for large
/// cold embeds) exceeds 3k v/s. Also reports the index-live COPY rate so
/// the two are compared (the index maintenance tax).
/// Throughput floor for COPY benchmarks, calibrated for local-latency PG.
/// Override (e.g. LEANKG_TEST_MIN_COPY_VPS=200) when running against
/// managed remote Postgres where WAN bandwidth, not the DB, is the bound.
fn min_copy_vps() -> f64 {
    std::env::var("LEANKG_TEST_MIN_COPY_VPS")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(3_000.0)
}

#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]

fn copy_bulk_load_10k_vectors_row_count() {
    let _g = pg_lock();
    let mut s = Scratch::new();
    let db = s.rw_backend();

    let n = 10_000usize;
    let mut rng = Rng::new(0x7AB1);
    let pairs: Vec<(String, Vec<f32>)> = (0..n)
        .map(|i| (format!("bulk{i:05}"), random_unit_vector(&mut rng, VEC_DIM)))
        .collect();

    // Cold-bulk path: drop the HNSW index first (T7.2), then COPY.
    db.run_script("::hnsw drop embedding_vectors:vec_idx", Default::default())
        .unwrap();
    let t = Instant::now();
    db.import_relations(named_vectors(&pairs)).unwrap();
    let cold_ms = t.elapsed().as_millis();
    let cold_v_per_s = if cold_ms > 0 {
        (n as f64) / (cold_ms as f64 / 1000.0)
    } else {
        f64::INFINITY
    };
    println!("[phase7] COPY 10k (index dropped): {cold_ms} ms -> {cold_v_per_s:.0} v/s");

    // Index-live COPY: recreate, then re-COPY the SAME 10k (upsert). This
    // pays per-row HNSW maintenance and is the incremental-embed case.
    db.run_script(
        "::hnsw create embedding_vectors:vec_idx { dim: 384, distance: Cosine }",
        Default::default(),
    )
    .unwrap();
    let t = Instant::now();
    db.import_relations(named_vectors(&pairs)).unwrap();
    let live_ms = t.elapsed().as_millis();
    let live_v_per_s = if live_ms > 0 {
        (n as f64) / (live_ms as f64 / 1000.0)
    } else {
        f64::INFINITY
    };
    println!("[phase7] COPY 10k (index live): {live_ms} ms -> {live_v_per_s:.0} v/s");

    let count: i64 = s
        .admin
        .query_one("SELECT count(*) FROM embedding_vectors", &[])
        .unwrap()
        .get(0);
    assert_eq!(count, n as i64, "COPY must land exactly {n} rows");
    assert!(
        cold_v_per_s >= min_copy_vps(),
        "cold-bulk COPY throughput {cold_v_per_s:.0} v/s below {:.0} v/s target",
        min_copy_vps()
    );
}

/// T7.1 — COPY upsert semantics: re-COPY with the same qualified_names
/// updates in place (count unchanged, data replaced).
#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn copy_upsert_updates_in_place() {
    let _g = pg_lock();
    let mut s = Scratch::new();
    let db = s.rw_backend();

    let n = 2_000usize;
    let mut rng = Rng::new(0xBEEF);
    let names: Vec<String> = (0..n).map(|i| format!("up{i:05}")).collect();

    // First pass: vectors of norm 1.
    let v1: Vec<Vec<f32>> = (0..n)
        .map(|_| random_unit_vector(&mut rng, VEC_DIM))
        .collect();
    let pass1: Vec<(String, Vec<f32>)> = names.iter().cloned().zip(v1.clone()).collect();
    db.import_relations(named_vectors(&pass1)).unwrap();
    let count1: i64 = s
        .admin
        .query_one("SELECT count(*) FROM embedding_vectors", &[])
        .unwrap()
        .get(0);
    assert_eq!(count1, n as i64, "first COPY must land {n} rows");

    // Second pass: same names, DIFFERENT vectors.
    let v2: Vec<Vec<f32>> = (0..n)
        .map(|_| random_unit_vector(&mut rng, VEC_DIM + 1))
        .map(|mut v| {
            v.truncate(VEC_DIM);
            let norm: f32 = v.iter().map(|x| x * x).sum::<f32>().sqrt();
            for x in &mut v {
                *x /= norm;
            }
            v
        })
        .collect();
    let pass2: Vec<(String, Vec<f32>)> = names.iter().cloned().zip(v2.clone()).collect();
    db.import_relations(named_vectors(&pass2)).unwrap();

    let count2: i64 = s
        .admin
        .query_one("SELECT count(*) FROM embedding_vectors", &[])
        .unwrap()
        .get(0);
    assert_eq!(count2, n as i64, "re-COPY must not change row count");

    // Spot-check: the stored vector for one name is the pass-2 value.
    // Compare element-wise with tolerance — the exact string depends on how
    // f32 -> f64 promotion round-trips through pgvector's text formatter.
    let stored: String = s
        .admin
        .query_one(
            "SELECT vec::text FROM embedding_vectors WHERE qualified_name = 'up00000'",
            &[],
        )
        .unwrap()
        .get(0);
    let stored_parsed: Vec<f32> = stored
        .trim()
        .trim_start_matches('[')
        .trim_end_matches(']')
        .split(',')
        .map(|x| x.trim().parse::<f32>().unwrap())
        .collect();
    assert_eq!(stored_parsed.len(), VEC_DIM, "stored vector dim");
    for (i, (got, want)) in stored_parsed.iter().zip(v2[0].iter()).enumerate() {
        assert!(
            (got - want).abs() < 1e-4,
            "element {i} mismatch: stored {got} vs expected {want}"
        );
    }
}

/// T7.2 — drop index, COPY, recreate index: HNSW recall >= 98% vs brute force.
#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn drop_index_copy_reindex_restores_recall() {
    let _g = pg_lock();
    let mut s = Scratch::new();
    let db = s.rw_backend();

    let n = 10_000usize;
    let k = 50usize;
    let mut rng = Rng::new(0xD07AB7);
    let pairs: Vec<(String, Vec<f32>)> = (0..n)
        .map(|i| {
            (
                format!("recall{i:05}"),
                random_unit_vector(&mut rng, VEC_DIM),
            )
        })
        .collect();
    let q = random_unit_vector(&mut rng, VEC_DIM);

    // Drop the HNSW index (schema.sql pre-created it), then COPY all rows.
    let drop_started = Instant::now();
    db.run_script("::hnsw drop embedding_vectors:vec_idx", Default::default())
        .unwrap();
    let drop_ms = drop_started.elapsed().as_millis();

    let copy_started = Instant::now();
    db.import_relations(named_vectors(&pairs)).unwrap();
    let copy_ms = copy_started.elapsed().as_millis();

    // Recreate the index.
    let reidx_started = Instant::now();
    db.run_script(
        "::hnsw create embedding_vectors:vec_idx { dim: 384, distance: Cosine }",
        Default::default(),
    )
    .unwrap();
    let reidx_ms = reidx_started.elapsed().as_millis();
    println!("[phase7] drop={drop_ms}ms copy={copy_ms}ms reindex={reidx_ms}ms");

    // Verify the index exists.
    let idx: i64 = s
        .admin
        .query_one(
            "SELECT count(*) FROM pg_indexes \
             WHERE schemaname = $1 AND tablename = 'embedding_vectors' \
               AND indexname = 'embedding_vectors_vec_hnsw_idx'",
            &[&s.name],
        )
        .unwrap()
        .get(0);
    assert_eq!(idx, 1, "HNSW index must be recreated");

    // Recall: brute-force top-k vs HNSW top-k.
    let brute: Vec<String> = {
        let mut scored: Vec<(&str, f64)> = pairs
            .iter()
            .map(|(n, v)| (n.as_str(), cosine_dist(v, &q)))
            .collect();
        scored.sort_by(|a, b| a.1.partial_cmp(&b.1).unwrap().then_with(|| a.0.cmp(b.0)));
        scored
            .into_iter()
            .take(k)
            .map(|(n, _)| n.to_string())
            .collect()
    };

    let hnsw_rows = {
        let mut tx = s.admin.transaction().unwrap();
        tx.batch_execute("SET LOCAL hnsw.ef_search = 200").unwrap();
        let rows = tx
            .query(
                "SELECT qualified_name FROM embedding_vectors \
                 ORDER BY vec <-> $1::text::vector LIMIT $2::int8",
                &[
                    &format!(
                        "[{}]",
                        q.iter()
                            .map(|x| x.to_string())
                            .collect::<Vec<_>>()
                            .join(",")
                    ),
                    &(k as i64),
                ],
            )
            .unwrap();
        let names: Vec<String> = rows.iter().map(|r| r.get::<_, String>(0)).collect();
        tx.commit().unwrap();
        names
    };
    let rec = jaccard(&hnsw_rows, &brute);
    println!("[phase7] HNSW recall@{k} (Jaccard vs brute): {rec:.4}");
    assert!(rec >= 0.98, "recall {rec} < 0.98 after drop/reindex");
}

/// T7.1 — embedding_state COPY path: fresh rows upsert (count matches, then
/// re-Copy updates without growing).
#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn copy_state_rows_upsert_in_place() {
    let _g = pg_lock();
    let mut s = Scratch::new();
    let db = s.rw_backend();

    let n = 1_000usize;
    let names: Vec<String> = (0..n).map(|i| format!("st{i:05}")).collect();

    let pass1: Vec<(String, u64, String)> = names
        .iter()
        .map(|qn| (qn.clone(), 0, "hash-1".to_string()))
        .collect();
    db.import_relations(named_state(&pass1)).unwrap();
    let count1: i64 = s
        .admin
        .query_one("SELECT count(*) FROM embedding_state", &[])
        .unwrap()
        .get(0);
    assert_eq!(count1, n as i64, "first state COPY must land {n} rows");

    // Re-Copy with updated content_hash.
    let pass2: Vec<(String, u64, String)> = names
        .iter()
        .map(|qn| (qn.clone(), 0, "hash-2".to_string()))
        .collect();
    db.import_relations(named_state(&pass2)).unwrap();
    let count2: i64 = s
        .admin
        .query_one("SELECT count(*) FROM embedding_state", &[])
        .unwrap()
        .get(0);
    assert_eq!(count2, n as i64, "re-COPY must not grow embedding_state");

    let stored: String = s
        .admin
        .query_one(
            "SELECT content_hash FROM embedding_state WHERE qualified_name = 'st00000'",
            &[],
        )
        .unwrap()
        .get(0);
    assert_eq!(stored, "hash-2", "state re-COPY must update content_hash");
}

/// Direct probe of the sync postgres `copy_in` writer API (used by
/// `copy_upsert`), confirming COPY FROM STDIN works through a Transaction.
#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn direct_copy_in_via_transaction() {
    let _g = pg_lock();
    let mut s = Scratch::new();
    let v = random_unit_vector(&mut Rng::new(42), VEC_DIM);
    let literal = format!(
        "[{}]",
        v.iter()
            .map(|x| x.to_string())
            .collect::<Vec<_>>()
            .join(",")
    );

    let mut tx = s.admin.transaction().unwrap();
    tx.batch_execute(
        "CREATE TEMP TABLE copy_probe (qn TEXT PRIMARY KEY, vec vector(384)) ON COMMIT DROP",
    )
    .unwrap();
    let mut writer = tx.copy_in("COPY copy_probe (qn, vec) FROM STDIN").unwrap();
    writer
        .write_all(format!("probe-1\t{literal}\n").as_bytes())
        .unwrap();
    let rows = writer.finish().unwrap();
    tx.commit().unwrap();
    assert_eq!(rows, 1, "COPY must write exactly 1 row");
}

/// Phase 7 exit measurement: synthetic 50k cold embed through the real
/// `import_relations` COPY path (index dropped, as `leankg embed` does for
/// large cold embeds). Extrapolates to workspace-be (~371k functions): total
/// COPY time = (371k / measured v/s). The plan's exit criterion is a cold
/// embed on PG < legacy-engine time (legacy ≈ 700 v/s ≈ 9 min for 371k).
#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn synthetic_50k_cold_embed_measurement() {
    let _g = pg_lock();
    let mut s = Scratch::new();
    let db = s.rw_backend();

    let n = 50_000usize;
    let mut rng = Rng::new(0x50_000);
    let pairs: Vec<(String, Vec<f32>)> = (0..n)
        .map(|i| (format!("syn{i:06}"), random_unit_vector(&mut rng, VEC_DIM)))
        .collect();

    db.run_script("::hnsw drop embedding_vectors:vec_idx", Default::default())
        .unwrap();
    let t = Instant::now();
    db.import_relations(named_vectors(&pairs)).unwrap();
    let elapsed = t.elapsed();
    let ms = elapsed.as_millis();
    let v_per_s = if ms > 0 {
        (n as f64) / (ms as f64 / 1000.0)
    } else {
        f64::INFINITY
    };
    let est_371k_secs = 371_000.0 / v_per_s;
    println!(
        "[phase7] synthetic 50k cold COPY: {ms} ms -> {v_per_s:.0} v/s; \
         extrapolated 371k functions: {est_371k_secs:.0} s ({:.1} min)",
        est_371k_secs / 60.0
    );

    let count: i64 = s
        .admin
        .query_one("SELECT count(*) FROM embedding_vectors", &[])
        .unwrap()
        .get(0);
    assert_eq!(count, n as i64, "50k COPY must land all rows");
    assert!(
        v_per_s >= min_copy_vps(),
        "50k cold COPY throughput {v_per_s:.0} v/s below {:.0} v/s target",
        min_copy_vps()
    );
}
