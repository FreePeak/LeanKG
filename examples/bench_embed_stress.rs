//! 400k embedding stress test — exercises the fast runtime plan
//! (INT8 → explicit threads → fat batch → seq cap).
//!
//! ```bash
//! # Default fast path (target ≥500 vec/s on M-series)
//! N=20000 cargo run --release --features embeddings --example bench_embed_stress
//!
//! # Full 400k
//! N=400000 cargo run --release --features embeddings --example bench_embed_stress
//!
//! # Legacy multi-1-thread profile
//! LEANKG_EMBED_FAST=0 N=20000 WORKERS=8 BATCH=64 \
//!   cargo run --release --features embeddings --example bench_embed_stress
//! ```

use leankg::embeddings::{
    ensure_quantized_onnx, resolve_embed_runtime, DirectEmbedder, EmbedModelKind,
};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Instant;

const DEFAULT_N: usize = 20_000;
const TARGET_COLD: usize = 400_000;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let _ = tracing_subscriber::fmt::try_init();

    let n = env_usize("N", DEFAULT_N).max(1);
    let req_workers = env_usize("WORKERS", 8).max(1);
    let req_batch = env_usize("BATCH", 64).max(1);
    let text_chars = env_usize("TEXT_CHARS", 400);
    let progress_every = env_usize("PROGRESS_EVERY", 10_000).max(1);

    let mut plan = resolve_embed_runtime(req_workers, req_batch);
    if plan.kind == EmbedModelKind::BgeInt8 {
        if let Err(e) = ensure_quantized_onnx() {
            eprintln!("INT8 download/ensure failed ({e}); using FP32");
            std::env::set_var("LEANKG_EMBED_MODEL", "bge");
            plan = resolve_embed_runtime(req_workers, req_batch);
        }
    }
    plan.apply_env();

    let kind = EmbedModelKind::from_env();
    eprintln!(
        "=== embed stress (fast path) ===\n\
         kind={} n={} workers={} batch={} intra={} omp={} max_seq={} text_chars={}",
        kind.label(),
        n,
        plan.workers,
        plan.batch_size,
        plan.intra_threads,
        plan.omp_threads,
        plan.max_seq,
        text_chars
    );

    let templates = Arc::new(build_templates(text_chars));
    let workers = plan.workers;
    let batch = plan.batch_size;
    let intra = plan.intra_threads;

    eprintln!("loading {workers} DirectEmbedder session(s), intra_threads={intra}…");
    let load_t = Instant::now();
    let mut embedders = Vec::with_capacity(workers);
    for _ in 0..workers {
        embedders.push(DirectEmbedder::with_kind_and_intra(kind, intra)?);
    }
    let warm: Vec<String> = (0..batch.min(8))
        .map(|i| templates[i % templates.len()].clone())
        .collect();
    for e in &embedders {
        let _ = e.embed(&warm)?;
    }
    eprintln!("sessions ready in {:.2}s", load_t.elapsed().as_secs_f64());

    let done = Arc::new(AtomicUsize::new(0));
    let started = Instant::now();
    let mut handles = Vec::with_capacity(workers);

    for (w_id, embedder) in embedders.into_iter().enumerate() {
        let templates = templates.clone();
        let done = done.clone();
        handles.push(std::thread::spawn(move || -> Result<(), String> {
            let mut i = w_id;
            let mut buf: Vec<String> = Vec::with_capacity(batch);
            while i < n {
                buf.clear();
                while buf.len() < batch && i < n {
                    let t = &templates[i % templates.len()];
                    buf.push(format!("#{i}\n{t}"));
                    i += workers;
                }
                if buf.is_empty() {
                    break;
                }
                let vectors = embedder.embed(&buf).map_err(|e| e.to_string())?;
                if vectors.len() != buf.len() || vectors.iter().any(|v| v.len() != 384) {
                    return Err("bad vector shape".into());
                }
                let total = done.fetch_add(buf.len(), Ordering::Relaxed) + buf.len();
                if total % progress_every < buf.len() || total == n {
                    let elapsed = started.elapsed().as_secs_f64().max(1e-9);
                    let rate = total as f64 / elapsed;
                    eprintln!(
                        "progress {total}/{n} ({:.1}%) rate={rate:.1} vec/s eta_400k={:.1} min",
                        100.0 * total as f64 / n as f64,
                        (TARGET_COLD as f64 / rate) / 60.0
                    );
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
    let eta_400k_min = (TARGET_COLD as f64 / rate) / 60.0;

    println!(
        "RESULT kind={} wrote={written} elapsed_s={secs:.3} rate_vec_per_s={rate:.1} eta_400k_min={eta_400k_min:.2} workers={} batch={} intra={} max_seq={}",
        kind.label(),
        plan.workers,
        plan.batch_size,
        plan.intra_threads,
        plan.max_seq
    );
    if rate < 500.0 {
        println!(
            "NOTE: rate<{:.0} below 500 vec/s target — try TEXT_CHARS=200, or check CPU thermal/throttling",
            500.0
        );
    }
    Ok(())
}

fn build_templates(fixed_chars: usize) -> Vec<String> {
    let lengths: &[usize] = if fixed_chars == 0 {
        &[120, 400, 800, 1200, 1500]
    } else {
        &[fixed_chars.max(32)]
    };
    lengths
        .iter()
        .enumerate()
        .map(|(k, &target)| {
            let mut s = format!(
                "fn stress_tpl_{k}(x: i32) -> i32 {{ // synthetic code blob for ONNX throughput\n"
            );
            while s.len() < target {
                s.push_str(&format!("    let v{} = x.wrapping_add({});\n", k, s.len()));
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
