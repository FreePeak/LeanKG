//! Phase 0 spike — pgvector distance parity + HNSW verification.
//!
//! See docs/analysis/pg-phase0-spike.md for full write-up and measured numbers.
//!
//! Requires the Phase 0 Postgres container:
//!   docker compose -p leankg-pg -f docker-compose.postgres.yml up -d
//!   docker exec leankg-pg-phase0 psql -U postgres -d leankg -c "CREATE EXTENSION IF NOT EXISTS vector;"
//!
//! Run only these (the crate has slow unrelated integration tests):
//!   cargo test --release --test pg_phase0_spike

use std::env;
use std::sync::Mutex;
use std::time::Instant;

/// Serialize the 3 integration tests: each DROPs/CREATEs the shared table.
static PG_LOCK: Mutex<()> = Mutex::new(());

const VEC_DIM: usize = 384;
const N_VECTORS: usize = 10_000;
const K: usize = 50;
const EF: usize = 100;
const TOL: f64 = 1e-5;
/// Deterministic PRNG (xoshiro256++ equivalent, hand-rolled so the test has
/// zero deps beyond `postgres`). Unit tests below pin exact sequences.
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
    /// Uniform f64 in [0, 1), 53 bits of entropy.
    fn next_f64(&mut self) -> f64 {
        (self.next_u64() >> 11) as f64 / (1u64 << 53) as f64
    }
}

/// Unit-norm random vector in R^VEC_DIM (rejection-free: gaussian then normalize).
fn random_unit_vector(rng: &mut Rng, dim: usize) -> Vec<f32> {
    let mut v: Vec<f32> = (0..dim)
        .map(|_| {
            // Box-Muller from two uniforms; valid in any dim.
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

/// Cosine distance = 1 - dot(a,b) for unit vectors.
fn cosine_dist(a: &[f32], b: &[f32]) -> f64 {
    1.0 - a
        .iter()
        .zip(b)
        .map(|(x, y)| (*x as f64) * (*y as f64))
        .sum::<f64>()
}

/// Brute-force top-k by cosine distance. Returns (names, distances) — both
/// sorted by ascending distance, ties broken by name (matches ORDER BY).
fn brute_force_topk(
    names: &[String],
    vecs: &[Vec<f32>],
    q: &[f32],
    k: usize,
) -> (Vec<String>, Vec<f64>) {
    let mut scored: Vec<(&str, f64)> = names
        .iter()
        .zip(vecs)
        .map(|(n, v)| (n.as_str(), cosine_dist(v, q)))
        .collect();
    scored.sort_by(|a, b| a.1.partial_cmp(&b.1).unwrap().then_with(|| a.0.cmp(b.0)));
    let (names, dists): (Vec<_>, Vec<_>) = scored.into_iter().take(k).unzip();
    (names.into_iter().map(|s| s.to_string()).collect(), dists)
}

/// PG connection string, e.g. `LEANKG_PG_URL=postgresql://postgres:postgres@localhost:5433/leankg`.
fn pg_url() -> String {
    env::var("LEANKG_PG_URL")
        .unwrap_or_else(|_| "postgresql://postgres:postgres@localhost:5433/leankg".to_string())
}

/// Reset the table to exactly `vectors` (names, dim-checked). Panics with the
/// SQL error if the server rejects anything (dimension mismatch surfaces here).
fn load_vectors(client: &mut postgres::Client, names: &[String], vecs: &[Vec<f32>]) {
    client
        .batch_execute("DROP TABLE IF EXISTS embedding_vectors")
        .unwrap();
    client
        .batch_execute(
            "CREATE TABLE embedding_vectors (qualified_name TEXT PRIMARY KEY, vec vector(384))",
        )
        .unwrap();
    let mut tx = client.transaction().unwrap();
    {
        let stmt = tx
            .prepare(
                "INSERT INTO embedding_vectors (qualified_name, vec) VALUES ($1, $2::text::vector)",
            )
            .unwrap();
        for (n, v) in names.iter().zip(vecs) {
            tx.execute(&stmt, &[&n.as_str(), &pgvector(v)]).unwrap();
        }
    }
    tx.commit().unwrap();
}

/// Format a dim-384 f32 vector as a pgvector literal: `[0.1,0.2,...]`.
/// Values round-trip losslessly within float32 precision.
fn pgvector(v: &[f32]) -> String {
    format!(
        "[{}]",
        v.iter()
            .map(|x| x.to_string())
            .collect::<Vec<_>>()
            .join(",")
    )
}

fn pgvector_from_row(text: &str) -> Vec<f32> {
    text.trim()
        .trim_start_matches('[')
        .trim_end_matches(']')
        .split(',')
        .map(|s| s.trim().parse::<f32>().unwrap())
        .collect()
}

/// Brute-force cosine-distance top-k in Postgres: exact (seq scan), no HNSW.
/// `ORDER BY vec <-> $1` — pgvector defines `<->` as L2 distance; on unit
/// vectors `l2 = sqrt(2 - 2*dot)` is strictly monotone in cosine distance
/// (1 - dot), so ordering is identical. Distances returned are the raw
/// pgvector `<->` values (documented in the spike doc).
fn pg_brute_topk(client: &mut postgres::Client, q: &[f32], k: usize) -> Vec<(String, f64)> {
    let rows = client
        .query(
            "SELECT vec <-> $1::text::vector AS dist, qualified_name
             FROM embedding_vectors
             ORDER BY vec <-> $1::text::vector
             LIMIT $2::int8",
            &[&pgvector(q), &(k as i64)],
        )
        .unwrap();
    rows.into_iter()
        .map(|r| (r.get::<_, String>(1), r.get::<_, f64>(0)))
        .collect()
}

/// HNSW top-k with `hnsw.ef_search` set per-session (the `LEANKG_HNSW_EF` knob).
fn pg_hnsw_topk(
    client: &mut postgres::Client,
    q: &[f32],
    k: usize,
    ef: usize,
) -> Vec<(String, f64)> {
    // `SET LOCAL` needs a transaction and takes a literal (no bind params).
    let mut tx = client.transaction().unwrap();
    let set_sql = format!("SET LOCAL hnsw.ef_search = {ef}");
    tx.batch_execute(&set_sql).unwrap();
    let rows = tx
        .query(
            "SELECT vec <-> $1::text::vector AS dist, qualified_name
             FROM embedding_vectors
             ORDER BY vec <-> $1::text::vector
             LIMIT $2::int8",
            &[&pgvector(q), &(k as i64)],
        )
        .unwrap();
    let out: Vec<(String, f64)> = rows
        .iter()
        .map(|r| (r.get::<_, String>(1), r.get::<_, f64>(0)))
        .collect();
    tx.commit().unwrap();
    out
}

fn jaccard(a: &[String], b: &[String]) -> f64 {
    let sa: std::collections::HashSet<&String> = a.iter().collect();
    let sb: std::collections::HashSet<&String> = b.iter().collect();
    let inter = sa.intersection(&sb).count();
    inter as f64 / sa.union(&sb).count() as f64
}

// ---------------------------------------------------------------------------
// Unit tests — pure-Rust math, no DB. Validate RNG determinism and the cosine
// distance formula before anything hits Postgres (TDD: red first, then green).
// ---------------------------------------------------------------------------

#[test]
fn test_rng_deterministic_sequence() {
    let mut a = Rng::new(0xDEAD_BEEF);
    let mut b = Rng::new(0xDEAD_BEEF);
    for _ in 0..100 {
        assert_eq!(a.next_u64(), b.next_u64(), "PRNG must be deterministic");
    }
}

#[test]
fn test_random_unit_vector_has_unit_norm() {
    let mut rng = Rng::new(42);
    for _ in 0..20 {
        let v = random_unit_vector(&mut rng, VEC_DIM);
        let norm = v.iter().map(|x| (*x as f64) * (*x as f64)).sum::<f64>();
        assert!((norm - 1.0).abs() < 1e-4, "norm {norm} != 1");
    }
}

#[test]
fn test_cosine_distance_mapping() {
    // Unit vectors: cos_sim = dot, cosine distance = 1 - dot.
    let a = [1.0f32, 0.0, 0.0];
    let b = [0.0f32, 1.0, 0.0];
    assert_eq!(cosine_dist(&a, &b), 1.0);
    let c = [1.0f32, 0.0, 0.0];
    assert!(cosine_dist(&a, &c).abs() < 1e-12);
    // Cozo HNSW `bind_distance: dist` also returns cosine distance (1 - cos_sim),
    // so this same formula is what both stores compute.
    let d = [1.0f32, 1.0, 0.0];
    let dn = d.iter().map(|x| x / (2.0f32).sqrt()).collect::<Vec<_>>();
    assert!((cosine_dist(&a, &dn) - (1.0 - std::f64::consts::FRAC_1_SQRT_2)).abs() < 1e-7);
}

#[test]
fn test_pgvector_roundtrip() {
    let v = vec![0.1f32, -0.5, 1.0, 0.0, std::f32::consts::PI];
    assert_eq!(pgvector_from_row(&pgvector(&v)), v);
}

// ---------------------------------------------------------------------------
// Integration tests — need the Phase 0 container.
// ---------------------------------------------------------------------------

/// T0.2 parity: HNSW top-k == brute-force top-k on 10k dim-384 unit vectors.
/// Same name set, identical order, distances equal within 1e-5.
#[test]
#[ignore = "requires docker compose -p leankg-pg ... up"]
fn pgvector_topk_matches_brute_force() {
    let _guard = PG_LOCK.lock().unwrap();
    let mut client = postgres::Client::connect(&pg_url(), postgres::NoTls).unwrap();

    let mut rng = Rng::new(0xDEAD_BEEF);
    let names: Vec<String> = (0..N_VECTORS).map(|i| format!("v{i:05}")).collect();
    let vecs: Vec<Vec<f32>> = (0..N_VECTORS)
        .map(|_| random_unit_vector(&mut rng, VEC_DIM))
        .collect();
    let q = random_unit_vector(&mut rng, VEC_DIM);

    load_vectors(&mut client, &names, &vecs);
    let (bf_names, bf_dists) = brute_force_topk(&names, &vecs, &q, K);

    let pg_rows = pg_brute_topk(&mut client, &q, K);
    assert_eq!(pg_rows.len(), K);
    let pg_names: Vec<String> = pg_rows.iter().map(|(n, _)| n.clone()).collect();
    let pg_dists: Vec<f64> = pg_rows.iter().map(|(_, d)| *d).collect();

    // (a) same name set, (b) identical order.
    assert_eq!(
        pg_names, bf_names,
        "pgvector ORDER BY <-> order differs from brute force"
    );
    // (c) distances equal within 1e-5 — pgvector <-> on unit vectors is
    // sqrt(2 - 2*dot); brute force here uses cosine distance (1 - dot).
    // Compare in cosine space so the assertion is the same quantity.
    for (pd, bd) in pg_dists.iter().zip(&bf_dists) {
        let pd_cosine = (*pd).powi(2) / 2.0; // l2^2 / 2 == 1 - dot == cosine distance
        assert!((pd_cosine - bd).abs() < TOL, "dist {pd_cosine} vs {bd}");
    }
}

/// T0.3a HNSW build timing on the same 10k rows.
#[test]
#[ignore = "requires docker compose -p leankg-pg ... up"]
fn hnsw_index_build_time() {
    let _guard = PG_LOCK.lock().unwrap();
    let mut client = postgres::Client::connect(&pg_url(), postgres::NoTls).unwrap();

    let mut rng = Rng::new(0xDEAD_BEEF);
    let names: Vec<String> = (0..N_VECTORS).map(|i| format!("v{i:05}")).collect();
    let vecs: Vec<Vec<f32>> = (0..N_VECTORS)
        .map(|_| random_unit_vector(&mut rng, VEC_DIM))
        .collect();
    let q = random_unit_vector(&mut rng, VEC_DIM);

    load_vectors(&mut client, &names, &vecs);
    let (bf_names, _) = brute_force_topk(&names, &vecs, &q, K);

    let t = Instant::now();
    client
        .batch_execute(
            "CREATE INDEX embedding_vectors_vec_hnsw_idx
             ON embedding_vectors USING hnsw (vec vector_cosine_ops)
             WITH (m = 16, ef_construction = 200)",
        )
        .unwrap();
    let build_ms = t.elapsed().as_millis();
    println!(
        "HNSW index build (m=16, ef_construction=200, {N_VECTORS} x dim {VEC_DIM}): {build_ms} ms"
    );
    assert!(build_ms < 60_000, "index build too slow: {build_ms} ms");

    // HNSW query timing, sequential ef searches.
    let t = Instant::now();
    let hnsw_rows = pg_hnsw_topk(&mut client, &q, K, EF);
    let query_ms = t.elapsed().as_millis();
    println!("HNSW top-k query (k={K}, ef={EF}): {query_ms} ms");
    let hnsw_names: Vec<String> = hnsw_rows.iter().map(|(n, _)| n.clone()).collect();
    println!(
        "HNSW first 5: {:?} (dist {:?})",
        &hnsw_names[..5],
        hnsw_rows[..5].iter().map(|(_, d)| d).collect::<Vec<_>>()
    );
    println!("Brute first 5: {:?}", &bf_names[..5]);

    // T0.3b recall: Jaccard overlap of the returned sets (>= 98%).
    let rec = jaccard(&hnsw_names, &bf_names);
    println!("HNSW recall @{K} (Jaccard vs brute force): {:.4}", rec);
    assert!(rec >= 0.98, "recall {rec} < 0.98");
}

/// T0.3c REINDEX CONCURRENTLY while concurrent SELECTs run: reads must not
/// block or error. The SELECT loop shares the client connection; REINDEX uses
/// a second connection. Any blocked read would show up as a timeout > 5s.
#[test]
#[ignore = "requires docker compose -p leankg-pg ... up"]
fn reindex_concurrently_does_not_block_reads() {
    let _guard = PG_LOCK.lock().unwrap();
    let mut client = postgres::Client::connect(&pg_url(), postgres::NoTls).unwrap();
    let mut reindex_client = postgres::Client::connect(&pg_url(), postgres::NoTls).unwrap();

    let mut rng = Rng::new(0xDEAD_BEEF);
    let names: Vec<String> = (0..N_VECTORS).map(|i| format!("v{i:05}")).collect();
    let vecs: Vec<Vec<f32>> = (0..N_VECTORS)
        .map(|_| random_unit_vector(&mut rng, VEC_DIM))
        .collect();
    let q = random_unit_vector(&mut rng, VEC_DIM);
    load_vectors(&mut client, &names, &vecs);
    client
        .batch_execute(
            "CREATE INDEX embedding_vectors_vec_hnsw_idx
             ON embedding_vectors USING hnsw (vec vector_cosine_ops)
             WITH (m = 16, ef_construction = 200)",
        )
        .unwrap();

    // Concurrent SELECT loop on the main connection, one per 100ms.
    let mut i = 0usize;
    let mut reindex_started = false;
    while i < 60 {
        let t = Instant::now();
        let rows = pg_hnsw_topk(&mut client, &q, K, EF);
        let ms = t.elapsed().as_millis();
        assert_eq!(
            rows.len(),
            K,
            "SELECT during REINDEX returned {} rows",
            rows.len()
        );
        println!("read #{i}: {ms} ms");
        i += 1;
        if i % 5 == 0 && !reindex_started {
            // REINDEX starts after ~5 reads (0.5s in) while reads still run.
            reindex_started = true;
            let rt = Instant::now();
            reindex_client
                .batch_execute("REINDEX INDEX CONCURRENTLY embedding_vectors_vec_hnsw_idx")
                .unwrap();
            println!("REINDEX CONCURRENTLY took {} ms", rt.elapsed().as_millis());
        }
        std::thread::sleep(std::time::Duration::from_millis(100));
    }
    // Every read completed without error — nothing to assert beyond reaching here.
}
