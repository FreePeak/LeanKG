//! Redis HNSW writer microbench.
//!
//! Requires Redis Stack on LEANKG_REDIS_URL (default redis://127.0.0.1:6379).
//!
//!   cargo run --release --example bench_redis_writer --features embeddings

use std::time::Instant;

const ROWS: usize = 20_000;
const CHUNK: usize = 5_000;
const TARGET_COLD: usize = 371_094;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    std::env::set_var("LEANKG_EMBED_VECTOR_STORE", "redis");
    let url = std::env::var("LEANKG_REDIS_URL").unwrap_or_else(|_| "redis://127.0.0.1:6379".into());
    eprintln!("LEANKG_REDIS_URL={url}");

    // Inline bench using redis crate directly so we don't need the lib
    // feature graph for a quick spike if embeddings feature is heavy.
    let client = redis::Client::open(url.as_str())?;
    let mut conn = client.get_connection()?;

    // Bulk load WITHOUT a live HNSW index, then create the index once.
    // Live HNSW updates during HSET are far slower than plain HASH writes.
    let _ = redis::cmd("FT.DROPINDEX")
        .arg("leankg_emb")
        .arg("DD")
        .query::<()>(&mut conn);

    let keys: Vec<String> = redis::cmd("KEYS")
        .arg("leankg:emb:bench::*")
        .query(&mut conn)?;
    if !keys.is_empty() {
        let _: () = redis::cmd("DEL").arg(&keys).query(&mut conn)?;
    }

    let started = Instant::now();
    let mut written = 0usize;
    while written < ROWS {
        let n = (ROWS - written).min(CHUNK);
        let mut pipe = redis::pipe();
        pipe.atomic();
        for i in 0..n {
            let idx = written + i;
            let qn = format!("bench::fn_{idx}");
            let key = format!("leankg:emb:{qn}");
            let mut blob = Vec::with_capacity(384 * 4);
            for d in 0..384 {
                let f = ((idx + d) % 100) as f32 / 100.0;
                blob.extend_from_slice(&f.to_le_bytes());
            }
            pipe.cmd("HSET")
                .arg(&key)
                .arg("qn")
                .arg(&qn)
                .arg("vector")
                .arg(blob);
        }
        pipe.query::<()>(&mut conn)?;
        written += n;
    }
    let write_secs = started.elapsed().as_secs_f64().max(1e-9);
    let write_rate = written as f64 / write_secs;

    let idx_started = Instant::now();
    redis::cmd("FT.CREATE")
        .arg("leankg_emb")
        .arg("ON")
        .arg("HASH")
        .arg("PREFIX")
        .arg(1)
        .arg("leankg:emb:")
        .arg("SCHEMA")
        .arg("qn")
        .arg("TEXT")
        .arg("vector")
        .arg("VECTOR")
        .arg("HNSW")
        .arg(6)
        .arg("TYPE")
        .arg("FLOAT32")
        .arg("DIM")
        .arg(384)
        .arg("DISTANCE_METRIC")
        .arg("COSINE")
        .query::<()>(&mut conn)?;
    let idx_secs = idx_started.elapsed().as_secs_f64();

    let eta = (TARGET_COLD as f64 / write_rate) / 60.0;
    println!(
        "wrote={written} write_s={write_secs:.3} rate_vec_per_s={write_rate:.1} idx_build_s={idx_secs:.3} eta_371k_write_min={eta:.2}"
    );
    Ok(())
}
