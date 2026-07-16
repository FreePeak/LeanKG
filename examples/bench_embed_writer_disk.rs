//! Disk-path writer bench (home dir, not tmpfs) + optional second relation.
use cozo::{DataValue, DbInstance, NamedRows};
use std::collections::BTreeMap;
use std::time::Instant;

const DIM: usize = 384;
const ROWS: usize = 50_000;
const CHUNK: usize = 5_000;
const TARGET_COLD: usize = 371_094;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let bulk = std::env::var("LEANKG_COZO_ROCKS_BULK").unwrap_or_else(|_| "(unset)".into());
    let dual = std::env::var("LEANKG_BENCH_DUAL").unwrap_or_else(|_| "0".into());
    eprintln!("LEANKG_COZO_ROCKS_BULK={bulk} DUAL={dual}");

    let path = dirs_next_or_home();
    std::fs::create_dir_all(&path)?;
    let path_str = path.to_string_lossy().to_string();
    eprintln!("db_path={path_str}");

    let db = DbInstance::new("rocksdb", &path_str, "")?;
    db.run_default(":create embedding_vectors {qualified_name: String => vector: <F32; 384>}")?;
    if dual == "1" {
        let _ = db.run_default(":create embedding_state {qualified_name: String => content_hash: String, state: String, updated_at: String}");
    }

    put_vec(&db, 0, 64)?;
    let started = Instant::now();
    let mut written = 0usize;
    while written < ROWS {
        let n = (ROWS - written).min(CHUNK);
        put_vec(&db, written, n)?;
        if dual == "1" {
            put_state(&db, written, n)?;
        }
        written += n;
        eprint!(".");
    }
    eprintln!();
    let secs = started.elapsed().as_secs_f64().max(1e-9);
    let rate = written as f64 / secs;
    println!(
        "wrote={written} elapsed_s={secs:.3} rate_vec_per_s={rate:.1} eta_371k_min={:.1}",
        (TARGET_COLD as f64 / rate) / 60.0
    );
    let _ = std::fs::remove_dir_all(&path);
    Ok(())
}

fn dirs_next_or_home() -> std::path::PathBuf {
    let home = std::env::var("HOME").unwrap_or_else(|_| "/tmp".into());
    std::path::PathBuf::from(home)
        .join(".leankg-bench-rocks")
        .join(format!("{}", std::process::id()))
}

fn put_vec(db: &DbInstance, start: usize, n: usize) -> Result<(), Box<dyn std::error::Error>> {
    let mut rows = Vec::with_capacity(n);
    for i in 0..n {
        let qn = format!("bench::fn_{}", start + i);
        let mut list = Vec::with_capacity(DIM);
        for d in 0..DIM {
            list.push(DataValue::from(((start + i + d) % 100) as f64 / 100.0));
        }
        rows.push(vec![DataValue::Str(qn.into()), DataValue::List(list)]);
    }
    let mut map = BTreeMap::new();
    map.insert(
        "embedding_vectors".into(),
        NamedRows::new(vec!["qualified_name".into(), "vector".into()], rows),
    );
    db.import_relations(map)?;
    Ok(())
}

fn put_state(db: &DbInstance, start: usize, n: usize) -> Result<(), Box<dyn std::error::Error>> {
    let mut rows = Vec::with_capacity(n);
    for i in 0..n {
        let qn = format!("bench::fn_{}", start + i);
        rows.push(vec![
            DataValue::Str(qn.into()),
            DataValue::Str("hash".into()),
            DataValue::Str("fresh".into()),
            DataValue::Str("now".into()),
        ]);
    }
    let mut map = BTreeMap::new();
    map.insert(
        "embedding_state".into(),
        NamedRows::new(
            vec![
                "qualified_name".into(),
                "content_hash".into(),
                "state".into(),
                "updated_at".into(),
            ],
            rows,
        ),
    );
    db.import_relations(map)?;
    Ok(())
}
