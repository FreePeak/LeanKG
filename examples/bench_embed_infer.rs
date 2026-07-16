//! Pure-inference microbench (no CozoDB): measure DirectEmbedder vec/sec.
//!
//! Usage:
//!   cargo run --release --features embeddings --example bench_embed_infer
//!   LEANKG_EMBED_MODEL=bge-q cargo run --release --features embeddings --example bench_embed_infer
//!   LEANKG_EMBED_MODEL=minilm BATCH=64 N=2048 cargo run --release --features embeddings --example bench_embed_infer
//!
//! Env:
//!   LEANKG_EMBED_MODEL   bge | bge-q | minilm   (default bge)
//!   BATCH                texts per embed call   (default 64)
//!   N                    total texts            (default 2048)
//!   WORKERS              parallel DirectEmbedder sessions (default 4)
//!   LENGTH_SORT          1 = sort texts by len before batching (default 1)
//!   TEXT_CHARS           synthetic blob length  (default 400; 0 = mixed)

use leankg::embeddings::{DirectEmbedder, EmbedModelKind};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Instant;

const TARGET_COLD: usize = 371_094;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let _ = tracing_subscriber::fmt::try_init();

    let kind = EmbedModelKind::from_env();
    let batch = env_usize("BATCH", 64).max(1);
    let n = env_usize("N", 2048).max(batch);
    let workers = env_usize("WORKERS", 4).max(1);
    let length_sort = env_bool("LENGTH_SORT", true);
    let text_chars = env_usize("TEXT_CHARS", 400);

    eprintln!(
        "kind={} batch={} n={} workers={} length_sort={} text_chars={}",
        kind.label(),
        batch,
        n,
        workers,
        length_sort,
        text_chars
    );

    let mut texts = synthesize_texts(n, text_chars);
    if length_sort {
        texts.sort_by_key(|t| t.len());
    }
    let texts = Arc::new(texts);

    // Pre-build workers OUTSIDE the timed window (model load is not infer).
    eprintln!("loading {workers} DirectEmbedder session(s)…");
    let load_started = Instant::now();
    let mut embedders: Vec<DirectEmbedder> = Vec::with_capacity(workers);
    for _ in 0..workers {
        embedders.push(DirectEmbedder::with_kind_and_intra(kind, 1)?);
    }
    // Warm each session with one small batch so ORT graphs are ready.
    let warm_n = batch.min(texts.len()).min(8);
    let warm_slice: Vec<String> = texts[..warm_n].to_vec();
    for e in &embedders {
        let _ = e.embed(&warm_slice)?;
    }
    eprintln!(
        "sessions ready in {:.2}s",
        load_started.elapsed().as_secs_f64()
    );

    let done = Arc::new(AtomicUsize::new(0));
    let started = Instant::now();
    let mut handles = Vec::with_capacity(workers);
    for (w_id, embedder) in embedders.into_iter().enumerate() {
        let texts = texts.clone();
        let done = done.clone();
        handles.push(std::thread::spawn(move || -> Result<(), String> {
            let shards: Vec<&[String]> = texts.chunks(batch * workers).collect();
            for shard in shards.iter().skip(w_id).step_by(workers) {
                for chunk in shard.chunks(batch) {
                    let owned: Vec<String> = chunk.to_vec();
                    let vectors = embedder.embed(&owned).map_err(|e| e.to_string())?;
                    if vectors.len() != owned.len() {
                        return Err(format!(
                            "len mismatch: texts={} vectors={}",
                            owned.len(),
                            vectors.len()
                        ));
                    }
                    if vectors.iter().any(|v| v.len() != 384) {
                        return Err("non-384-d vector".into());
                    }
                    done.fetch_add(owned.len(), Ordering::Relaxed);
                }
            }
            Ok(())
        }));
    }
    for h in handles {
        h.join().map_err(|_| "worker panicked")??;
    }
    let elapsed = started.elapsed();
    let written = done.load(Ordering::Relaxed);
    let secs = elapsed.as_secs_f64().max(1e-9);
    let rate = written as f64 / secs;
    let eta_371k_min = (TARGET_COLD as f64 / rate) / 60.0;

    println!(
        "kind={} wrote={written} elapsed_s={secs:.3} rate_vec_per_s={rate:.1} eta_371k_min={eta_371k_min:.1}",
        kind.label()
    );
    Ok(())
}

fn synthesize_texts(n: usize, fixed_chars: usize) -> Vec<String> {
    let lengths: &[usize] = if fixed_chars == 0 {
        &[120, 400, 800, 1200, 1500]
    } else {
        &[fixed_chars]
    };
    (0..n)
        .map(|i| {
            let target = lengths[i % lengths.len()];
            let mut s = format!(
                "fn embed_bench_{i}(x: i32) -> i32 {{ // synthetic code blob for ONNX throughput\n"
            );
            while s.len() < target {
                s.push_str(&format!("    let v{i} = x.wrapping_add({});\n", s.len()));
            }
            s.truncate(target);
            s
        })
        .collect()
}

fn env_usize(key: &str, default: usize) -> usize {
    std::env::var(key)
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(default)
}

fn env_bool(key: &str, default: bool) -> bool {
    match std::env::var(key) {
        Ok(v) => matches!(
            v.trim().to_ascii_lowercase().as_str(),
            "1" | "true" | "yes" | "on"
        ),
        Err(_) => default,
    }
}
