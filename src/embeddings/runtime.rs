//! Fast-path embed runtime: quantize → threads → batch → cap seq.
//!
//! Small BERT encoders on Apple Silicon commonly clear **500+ vec/s** when
//! all four levers are set together. The old default (FP32 + N workers ×
//! `intra_threads=1`) left ~3–5× on the table via memory-bandwidth contention.

// PR #127 churn revert: keep the pre-#127 `.max().min()` form instead of
// `.clamp(128, 256)`. Silences the newer clippy lint that PR #127 worked
// around.
#![allow(clippy::manual_clamp)]

use super::models::{cache_dir, EmbedModelKind, MAX_SEQ_LEN};

/// Soft toggle. Default **off** — the quality path (FP32 BGE, full 512-token
/// window) is the default. Operators can set `LEANKG_EMBED_FAST=1` for the
/// legacy INT8 / seq-cap / fat-batch profile when embedding speed matters more
/// than vector quality (e.g. incremental runs).
pub fn embed_fast_enabled() -> bool {
    match std::env::var("LEANKG_EMBED_FAST") {
        Ok(v) => {
            let t = v.trim();
            !(t == "0" || t.eq_ignore_ascii_case("false") || t.eq_ignore_ascii_case("off"))
        }
        // Default OFF: quality over speed. The 512-token window is the model's
        // full context; truncating to 128 (the fast path) loses meaning on
        // doc / first-paragraph blobs.
        Err(_) => false,
    }
}

fn env_usize(key: &str) -> Option<usize> {
    std::env::var(key).ok().and_then(|v| v.parse().ok())
}

fn perf_cores() -> usize {
    std::thread::available_parallelism()
        .map(|n| n.get())
        .unwrap_or(4)
        .clamp(2, 10)
}

/// True when Xenova INT8 weights are on disk.
pub fn quantized_onnx_available() -> bool {
    let cache = cache_dir();
    let Ok(snap) = first_snapshot_dir(cache.join("models--Xenova--bge-small-en-v1.5")) else {
        return false;
    };
    snap.join("onnx/model_quantized.onnx").exists()
}

fn first_snapshot_dir(repo: std::path::PathBuf) -> Result<std::path::PathBuf, ()> {
    let snapshots = repo.join("snapshots");
    let entry = std::fs::read_dir(&snapshots)
        .map_err(|_| ())?
        .filter_map(|e| e.ok())
        .find(|e| e.path().is_dir())
        .ok_or(())?;
    Ok(entry.path())
}

/// Download Xenova `model_quantized.onnx` into the local HF-style cache if missing.
pub fn ensure_quantized_onnx() -> Result<std::path::PathBuf, Box<dyn std::error::Error>> {
    let cache = cache_dir();
    let snap =
        first_snapshot_dir(cache.join("models--Xenova--bge-small-en-v1.5")).map_err(|_| {
            "Xenova bge-small cache missing — run `leankg embed --init` first".to_string()
        })?;
    let dest = snap.join("onnx/model_quantized.onnx");
    if dest.exists() {
        return Ok(dest);
    }
    std::fs::create_dir_all(dest.parent().unwrap())?;
    let url =
        "https://huggingface.co/Xenova/bge-small-en-v1.5/resolve/main/onnx/model_quantized.onnx";
    tracing::info!("downloading INT8 ONNX → {}", dest.display());
    let resp = reqwest::blocking::get(url)?.error_for_status()?;
    let bytes = resp.bytes()?;
    let tmp = dest.with_extension("onnx.partial");
    std::fs::write(&tmp, &bytes)?;
    std::fs::rename(&tmp, &dest)?;
    tracing::info!("INT8 ONNX ready ({} MB)", bytes.len() / (1024 * 1024));
    Ok(dest)
}

/// Resolved knobs for one embed / stress run.
#[derive(Debug, Clone, Copy)]
pub struct EmbedRuntimePlan {
    pub kind: EmbedModelKind,
    pub max_seq: usize,
    pub workers: usize,
    pub batch_size: usize,
    /// ORT `SessionBuilder::with_intra_threads`.
    pub intra_threads: usize,
    /// Value written to `OMP_NUM_THREADS` before session create.
    pub omp_threads: usize,
}

impl EmbedRuntimePlan {
    /// Apply process env that DirectEmbedder / workers read (`LEANKG_EMBED_MODEL`,
    /// `LEANKG_EMBED_MAX_SEQ`, `LEANKG_EMBED_DIRECT_INTRA`, `OMP_NUM_THREADS`).
    pub fn apply_env(self) {
        // Only set when caller hasn't pinned a value — preserve explicit overrides.
        if std::env::var_os("LEANKG_EMBED_MODEL").is_none() {
            let label = match self.kind {
                EmbedModelKind::BgeInt8 => "bge-q",
                EmbedModelKind::BgeFp16 => "bge-fp16",
                EmbedModelKind::MiniLm => "minilm",
                EmbedModelKind::BgeFp32 => "bge",
            };
            std::env::set_var("LEANKG_EMBED_MODEL", label);
        }
        if std::env::var_os("LEANKG_EMBED_MAX_SEQ").is_none() {
            std::env::set_var("LEANKG_EMBED_MAX_SEQ", self.max_seq.to_string());
        }
        if std::env::var_os("LEANKG_EMBED_DIRECT_INTRA").is_none() {
            std::env::set_var("LEANKG_EMBED_DIRECT_INTRA", self.intra_threads.to_string());
        }
        // Always set OMP to match the plan (safe: called before workers spawn).
        std::env::set_var("OMP_NUM_THREADS", self.omp_threads.to_string());
    }
}

/// Build the runtime plan from CLI-requested workers/batch + env.
///
/// Fast path (default):
/// 1. INT8 (`bge-q`) when weights available / downloadable
/// 2. Explicit ORT intra-threads on **one** session (M-series sweet spot)
/// 3. Large batch (≥64)
/// 4. Seq cap 128
pub fn resolve_embed_runtime(requested_workers: usize, requested_batch: usize) -> EmbedRuntimePlan {
    let fast = embed_fast_enabled();
    let cores = perf_cores();

    let kind = if let Ok(raw) = std::env::var("LEANKG_EMBED_MODEL") {
        // Honor explicit model even when empty-ish — from_env handles it.
        let _ = raw;
        EmbedModelKind::from_env()
    } else if fast {
        EmbedModelKind::BgeInt8
    } else {
        EmbedModelKind::BgeFp32
    };

    // Max token window per element. Configurable via `LEANKG_EMBED_MAX_SEQ`;
    // defaults to the model's full 512-token window. Fast mode only trims to
    // 128 when the operator did not pin an explicit max_seq — explicit config
    // always wins.
    let max_seq = match env_usize("LEANKG_EMBED_MAX_SEQ") {
        Some(n) => n.clamp(64, MAX_SEQ_LEN),
        None => {
            if fast {
                128
            } else {
                MAX_SEQ_LEN
            }
        }
    };

    // Threading strategy (Apple Silicon / small BERT):
    // Empirically, N×`intra=1` data-parallel sessions beat 1×`intra=N`
    // (ORT thread-pool overhead dominates tiny graphs). Fast path still
    // forces INT8 + seq cap + fat batches — the real 2–4× levers.
    let explicit_intra = env_usize("LEANKG_EMBED_DIRECT_INTRA").filter(|n| (1..=128).contains(n));
    let (workers, intra_threads, omp_threads) = if let Some(intra) = explicit_intra {
        let w = requested_workers.max(1);
        let omp = if w > 1 { 1 } else { intra };
        (w, intra, omp)
    } else if fast {
        // Honor low worker counts (MCP background embed defaults to 1–2).
        // Only bump toward P-core count when the caller already asked for
        // parallelism (≥4) — otherwise Docker/background jobs OOM on FP32
        // fallback or small mem_limit.
        let w = if requested_workers <= 2 {
            requested_workers.max(1)
        } else {
            requested_workers.max(1).max(cores.clamp(4, 8)).min(8)
        };
        (w, 1, 1)
    } else {
        let w = requested_workers.max(1);
        (w, 1, 1)
    };

    let batch_size = {
        let b = requested_batch.max(1);
        if fast {
            // Prefer fat batches for cold CLI runs; keep small batches for
            // background / memory-constrained callers (≤32).
            if b <= 32 {
                b
            } else {
                b.max(128).min(256)
            }
        } else {
            b
        }
    };

    EmbedRuntimePlan {
        kind,
        max_seq,
        workers,
        batch_size,
        intra_threads,
        omp_threads,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // These tests mutate process env (LEANKG_EMBED_FAST / LEANKG_EMBED_MAX_SEQ),
    // so they must not run concurrently with each other or with other
    // env-dependent tests in the same binary.
    fn env_guard() -> impl Drop {
        crate::embeddings::test_env::lock()
    }

    #[test]
    fn default_plan_is_quality_not_fast() {
        // Default (no LEANKG_EMBED_FAST): quality path — FP32 model, full
        // 512-token window. Fast must be an explicit opt-in.
        let plan = {
            let _g = env_guard();
            std::env::remove_var("LEANKG_EMBED_FAST");
            std::env::remove_var("LEANKG_EMBED_MAX_SEQ");
            std::env::remove_var("LEANKG_EMBED_MODEL");
            std::env::remove_var("LEANKG_EMBED_DIRECT_INTRA");
            resolve_embed_runtime(4, 32)
        }; // guard dropped before asserts — a panic here cannot poison the lock
        assert_eq!(plan.kind, EmbedModelKind::BgeFp32);
        assert_eq!(plan.max_seq, MAX_SEQ_LEN, "default must be full 512 window");
        assert_eq!(plan.workers, 4);
        assert_eq!(plan.intra_threads, 1);
    }

    #[test]
    fn explicit_fast_uses_int8_seq_cap_and_fat_batch() {
        let plan = {
            let _g = env_guard();
            std::env::set_var("LEANKG_EMBED_FAST", "1");
            std::env::set_var("LEANKG_EMBED_MAX_SEQ", "128"); // pin — shell may leak 512
            std::env::remove_var("LEANKG_EMBED_MODEL");
            std::env::remove_var("LEANKG_EMBED_DIRECT_INTRA");
            let plan = resolve_embed_runtime(4, 32);
            std::env::remove_var("LEANKG_EMBED_FAST");
            std::env::remove_var("LEANKG_EMBED_MAX_SEQ");
            plan
        };
        assert!(plan.workers >= 4, "workers={}", plan.workers);
        assert_eq!(plan.intra_threads, 1);
        assert_eq!(plan.max_seq, 128);
        assert_eq!(plan.batch_size, 32, "small requested batch stays unchanged");
        assert_eq!(plan.kind, EmbedModelKind::BgeInt8);
    }

    #[test]
    fn explicit_max_seq_wins_over_fast_cap() {
        // Operator-pinned max_seq always wins, even in fast mode.
        let plan = {
            let _g = env_guard();
            std::env::set_var("LEANKG_EMBED_FAST", "1");
            std::env::set_var("LEANKG_EMBED_MAX_SEQ", "256");
            std::env::remove_var("LEANKG_EMBED_MODEL");
            std::env::remove_var("LEANKG_EMBED_DIRECT_INTRA");
            let plan = resolve_embed_runtime(4, 32);
            std::env::remove_var("LEANKG_EMBED_FAST");
            std::env::remove_var("LEANKG_EMBED_MAX_SEQ");
            plan
        };
        assert_eq!(plan.max_seq, 256);
    }

    #[test]
    fn slow_plan_keeps_multi_worker() {
        let plan = {
            let _g = env_guard();
            std::env::set_var("LEANKG_EMBED_FAST", "0");
            std::env::remove_var("LEANKG_EMBED_MODEL");
            std::env::remove_var("LEANKG_EMBED_DIRECT_INTRA");
            let plan = resolve_embed_runtime(4, 32);
            std::env::remove_var("LEANKG_EMBED_FAST");
            plan
        };
        assert_eq!(plan.workers, 4);
        assert_eq!(plan.intra_threads, 1);
        assert_eq!(plan.kind, EmbedModelKind::BgeFp32);
    }
}
