//! Writer microbench: measure `import_relations` throughput with/without
//! `LEANKG_COZO_ROCKS_BULK=1` (patched cozo: disable_wal + sync(false)).
//!
//! Usage:
//!   LEANKG_COZO_ROCKS_BULK=0 cargo run --release --example bench_embed_writer
//!   LEANKG_COZO_ROCKS_BULK=1 cargo run --release --example bench_embed_writer
//!
//! Prints vec/sec and extrapolated wall time for 371k rows.

use cozo::{DataValue, DbInstance, NamedRows};
use std::collections::BTreeMap;
use std::time::{Instant, SystemTime, UNIX_EPOCH};

const DIM: usize = 384;
const ROWS: usize = 20_000;
const CHUNK: usize = 5_000;
const TARGET_COLD: usize = 371_094;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let bulk = std::env::var("LEANKG_COZO_ROCKS_BULK").unwrap_or_else(|_| "(unset)".into());
    eprintln!("LEANKG_COZO_ROCKS_BULK={bulk}");

    let stamp = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_millis();
    let path = std::env::temp_dir().join(format!("leankg_bench_rocks_{stamp}"));
    std::fs::create_dir_all(&path)?;
    let path_str = path.to_string_lossy().to_string();
    eprintln!("db_path={path_str}");

    let db = DbInstance::new("rocksdb", &path_str, "")?;
    db.run_default(":create embedding_vectors {qualified_name: String => vector: <F32; 384>}")?;

    put_chunk(&db, 0, 64)?;

    let started = Instant::now();
    let mut written = 0usize;
    while written < ROWS {
        let n = (ROWS - written).min(CHUNK);
        put_chunk(&db, written, n)?;
        written += n;
    }
    let elapsed = started.elapsed();
    let secs = elapsed.as_secs_f64().max(1e-9);
    let rate = written as f64 / secs;
    let eta_371k_min = (TARGET_COLD as f64 / rate) / 60.0;

    println!(
        "wrote={written} elapsed_s={secs:.3} rate_vec_per_s={rate:.1} eta_371k_min={eta_371k_min:.1}"
    );

    let _ = std::fs::remove_dir_all(&path);
    Ok(())
}

fn put_chunk(db: &DbInstance, start: usize, n: usize) -> Result<(), Box<dyn std::error::Error>> {
    let mut rows: Vec<Vec<DataValue>> = Vec::with_capacity(n);
    for i in 0..n {
        let qn = format!("bench::fn_{}", start + i);
        let mut list = Vec::with_capacity(DIM);
        for d in 0..DIM {
            list.push(DataValue::from(((start + i + d) % 100) as f64 / 100.0));
        }
        rows.push(vec![DataValue::Str(qn.into()), DataValue::List(list)]);
    }
    let named = NamedRows::new(vec!["qualified_name".into(), "vector".into()], rows);
    let mut map = BTreeMap::new();
    map.insert("embedding_vectors".into(), named);
    db.import_relations(map)?;
    Ok(())
}
