//! MCP/CLI embed control: resume preflight, cooperative cancel, partial RSS policy.
//!
//! FR-EMBED-RESUME-07 / FR-EMBED-TOGGLE-01 / FR-EMBED-PARTIAL-01.

use crate::db::CozoDb;
use serde_json::{json, Value};
use std::path::Path;
use std::sync::atomic::{AtomicBool, AtomicU8, Ordering};
use std::sync::Mutex;

use super::build::{BackgroundEmbedConfig, IN_PROCESS_BG_EMBED_ACTIVE};
use super::state;

/// Cooperative cancel for in-process embed (Docker PID 1 safe).
static CANCEL_REQUESTED: AtomicBool = AtomicBool::new(false);

/// Armed by MCP `embed_control on` until off / auto-disarm on complete.
static EMBED_ARMED: AtomicBool = AtomicBool::new(false);

/// 0=idle 1=waiting_idle 2=running 3=paused_yield 4=completed 5=failed 6=cancelled
static EMBED_PHASE: AtomicU8 = AtomicU8::new(0);

static ARMED_CFG: Mutex<Option<BackgroundEmbedConfig>> = Mutex::new(None);

/// Live progress counters written by the embed worker for honest status.
static LIVE_SKIPPED_FRESH: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);
static LIVE_TO_EMBED: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);
static LIVE_VECTORS: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);
static LIVE_CONSIDERED: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);

pub const PHASE_IDLE: u8 = 0;
pub const PHASE_WAITING: u8 = 1;
pub const PHASE_RUNNING: u8 = 2;
pub const PHASE_PAUSED: u8 = 3;
pub const PHASE_COMPLETED: u8 = 4;
pub const PHASE_FAILED: u8 = 5;
pub const PHASE_CANCELLED: u8 = 6;

#[derive(Debug, Clone)]
pub struct EmbedResumePreflight {
    pub vectors_existing: u64,
    pub fresh: u64,
    pub stale: u64,
    pub other: u64,
    pub has_embed_data: bool,
}

#[derive(Debug, Clone)]
pub struct PartialEmbedPolicy {
    pub batches_per_slice: usize,
    pub pause_ms: u64,
    pub yield_on_activity: bool,
    pub rss_fraction: f64,
}

impl Default for PartialEmbedPolicy {
    fn default() -> Self {
        Self {
            batches_per_slice: embed_partial_batches(),
            pause_ms: embed_partial_pause_ms(),
            yield_on_activity: true,
            rss_fraction: embed_rss_fraction(),
        }
    }
}

pub fn clear_cancel() {
    CANCEL_REQUESTED.store(false, Ordering::SeqCst);
}

pub fn request_cancel_in_process_embed() -> bool {
    CANCEL_REQUESTED.store(true, Ordering::SeqCst);
    IN_PROCESS_BG_EMBED_ACTIVE.load(Ordering::SeqCst)
}

pub fn is_cancel_requested() -> bool {
    CANCEL_REQUESTED.load(Ordering::SeqCst)
}

pub fn set_phase(phase: u8) {
    EMBED_PHASE.store(phase, Ordering::SeqCst);
}

pub fn phase() -> u8 {
    EMBED_PHASE.load(Ordering::SeqCst)
}

pub fn phase_name(phase: u8) -> &'static str {
    match phase {
        PHASE_WAITING => "waiting_idle",
        PHASE_RUNNING => "running",
        PHASE_PAUSED => "paused_yield",
        PHASE_COMPLETED => "completed",
        PHASE_FAILED => "failed",
        PHASE_CANCELLED => "cancelled",
        _ => "idle",
    }
}

pub fn arm_embed(cfg: BackgroundEmbedConfig) {
    clear_cancel();
    if let Ok(mut g) = ARMED_CFG.lock() {
        *g = Some(cfg);
    }
    EMBED_ARMED.store(true, Ordering::SeqCst);
    set_phase(PHASE_WAITING);
}

pub fn disarm_embed() {
    EMBED_ARMED.store(false, Ordering::SeqCst);
    if let Ok(mut g) = ARMED_CFG.lock() {
        *g = None;
    }
}

pub fn is_armed() -> bool {
    EMBED_ARMED.load(Ordering::SeqCst)
}

pub fn is_in_process_embed_active() -> bool {
    IN_PROCESS_BG_EMBED_ACTIVE.load(Ordering::SeqCst)
}

pub fn take_armed_config() -> Option<BackgroundEmbedConfig> {
    let cfg = ARMED_CFG.lock().ok().and_then(|mut g| g.take());
    if cfg.is_some() {
        // One-shot: clear armed so the idle scheduler cannot re-spawn.
        EMBED_ARMED.store(false, Ordering::SeqCst);
    }
    cfg
}

pub fn set_live_progress(considered: u64, skipped_fresh: u64, to_embed: u64, vectors: u64) {
    LIVE_CONSIDERED.store(considered, Ordering::Relaxed);
    LIVE_SKIPPED_FRESH.store(skipped_fresh, Ordering::Relaxed);
    LIVE_TO_EMBED.store(to_embed, Ordering::Relaxed);
    LIVE_VECTORS.store(vectors, Ordering::Relaxed);
}

pub fn live_progress() -> (u64, u64, u64, u64) {
    (
        LIVE_CONSIDERED.load(Ordering::Relaxed),
        LIVE_SKIPPED_FRESH.load(Ordering::Relaxed),
        LIVE_TO_EMBED.load(Ordering::Relaxed),
        LIVE_VECTORS.load(Ordering::Relaxed),
    )
}

/// Cheap resume preflight — counts only, no `all_elements`.
pub fn embed_resume_preflight(db: &CozoDb) -> Result<EmbedResumePreflight, String> {
    let vectors_existing = count_embedding_vectors(db).unwrap_or(0) as u64;
    let counts = state::count_by_state(db).map_err(|e| e.to_string())?;
    let has_embed_data = vectors_existing > 0 || counts.fresh + counts.stale + counts.other > 0;
    Ok(EmbedResumePreflight {
        vectors_existing,
        fresh: counts.fresh as u64,
        stale: counts.stale as u64,
        other: counts.other as u64,
        has_embed_data,
    })
}

pub fn count_embedding_vectors(db: &CozoDb) -> Result<usize, Box<dyn std::error::Error>> {
    let result = crate::db::schema::run_script(
        db,
        "?[qualified_name] := *embedding_vectors{qualified_name}",
        Default::default(),
    )?;
    Ok(result.rows.len())
}

/// Soft embed budget: fraction of cgroup mem limit, clamped by `LEANKG_EMBED_MAX_MB`.
pub fn resolve_partial_embed_budget_mb(rss_fraction: f64) -> u64 {
    let fraction = if rss_fraction > 0.0 && rss_fraction <= 1.0 {
        rss_fraction
    } else {
        embed_rss_fraction()
    };
    let container = detect_cgroup_memory_limit_mb().unwrap_or(0);
    let from_fraction = if container > 0 {
        ((container as f64) * fraction) as u64
    } else {
        0
    };
    let hard = super::build::embed_max_rss_mb();
    let mut budget = if from_fraction > 0 {
        from_fraction
    } else if hard > 0 {
        ((hard as f64) * fraction) as u64
    } else {
        512
    };
    if hard > 0 {
        budget = budget.min(hard);
    }
    budget.max(256)
}

/// Prefer incremental HNSW `:put` when dirty set is small vs existing index.
pub fn should_use_incremental_hnsw_puts(dirty_count: usize, total_vectors: usize) -> bool {
    if dirty_count == 0 {
        return false;
    }
    let threshold = (total_vectors / 20).max(1_000);
    dirty_count <= threshold
}

pub fn embed_idle_after_secs() -> u64 {
    std::env::var("LEANKG_EMBED_IDLE_AFTER_SECS")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or_else(|| {
            std::env::var("LEANKG_GC_IDLE_AFTER_SECS")
                .ok()
                .and_then(|v| v.parse().ok())
                .unwrap_or(60)
        })
}

pub fn embed_rss_fraction() -> f64 {
    std::env::var("LEANKG_EMBED_RSS_FRACTION")
        .ok()
        .and_then(|v| v.parse().ok())
        .filter(|f: &f64| *f > 0.0 && *f <= 1.0)
        .unwrap_or(0.40)
}

pub fn embed_partial_batches() -> usize {
    std::env::var("LEANKG_EMBED_PARTIAL_BATCHES")
        .ok()
        .and_then(|v| v.parse().ok())
        .filter(|n: &usize| *n >= 1)
        .unwrap_or(4)
}

pub fn embed_partial_pause_ms() -> u64 {
    std::env::var("LEANKG_EMBED_PARTIAL_PAUSE_MS")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(500)
}

pub fn mcp_is_idle_for_embed() -> bool {
    crate::gc::MemoryGuard::idle_secs_public() >= embed_idle_after_secs()
}

pub fn wait_until_idle_or_cancel(idle_secs: u64) -> bool {
    loop {
        if is_cancel_requested() || !is_armed() {
            return false;
        }
        if crate::gc::MemoryGuard::idle_secs_public() >= idle_secs {
            return true;
        }
        set_phase(PHASE_WAITING);
        std::thread::sleep(std::time::Duration::from_millis(500));
    }
}

/// Yield while MCP is busy (activity advanced); return false if cancelled.
pub fn yield_while_mcp_busy() -> bool {
    set_phase(PHASE_PAUSED);
    let idle_need = embed_idle_after_secs().min(15).max(1);
    loop {
        if is_cancel_requested() {
            return false;
        }
        if crate::gc::MemoryGuard::idle_secs_public() >= idle_need {
            set_phase(PHASE_RUNNING);
            return true;
        }
        std::thread::sleep(std::time::Duration::from_millis(200));
    }
}

pub fn embed_job_status(leankg_dir: &Path) -> Value {
    let status_path = leankg_dir.join("embed_status.json");
    let lock_path = leankg_dir.join("embed.lock");
    let file_status = std::fs::read_to_string(&status_path)
        .ok()
        .and_then(|s| serde_json::from_str::<Value>(&s).ok());
    let lock_pid = std::fs::read_to_string(&lock_path)
        .ok()
        .and_then(|s| s.trim().parse::<u64>().ok());
    let phase = phase();
    json!({
        "armed": is_armed(),
        "phase": phase_name(phase),
        "phase_code": phase,
        "in_process_active": IN_PROCESS_BG_EMBED_ACTIVE.load(Ordering::SeqCst),
        "cancel_requested": is_cancel_requested(),
        "lock_pid": lock_pid,
        "considered": LIVE_CONSIDERED.load(Ordering::Relaxed),
        "skipped_fresh": LIVE_SKIPPED_FRESH.load(Ordering::Relaxed),
        "to_embed": LIVE_TO_EMBED.load(Ordering::Relaxed),
        "vectors_existing": LIVE_VECTORS.load(Ordering::Relaxed),
        "file_status": file_status,
    })
}

fn detect_cgroup_memory_limit_mb() -> Option<u64> {
    // cgroup v2
    for path in [
        "/sys/fs/cgroup/memory.max",
        "/sys/fs/cgroup/memory/memory.limit_in_bytes",
    ] {
        if let Ok(raw) = std::fs::read_to_string(path) {
            let t = raw.trim();
            if t == "max" || t.is_empty() {
                continue;
            }
            if let Ok(bytes) = t.parse::<u64>() {
                // Ignore absurd host-wide defaults
                if bytes > 1 << 50 {
                    continue;
                }
                return Some(bytes / (1024 * 1024));
            }
        }
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn incremental_hnsw_puts_for_small_dirty() {
        assert!(should_use_incremental_hnsw_puts(50, 100_000));
        assert!(!should_use_incremental_hnsw_puts(50_000, 100_000));
        assert!(!should_use_incremental_hnsw_puts(0, 100_000));
    }

    #[test]
    fn partial_budget_respects_fraction_floor() {
        let b = resolve_partial_embed_budget_mb(0.40);
        assert!(b >= 256);
    }

    #[test]
    fn arm_disarm_roundtrip() {
        disarm_embed();
        arm_embed(BackgroundEmbedConfig::default());
        assert!(is_armed());
        assert_eq!(phase(), PHASE_WAITING);
        disarm_embed();
        assert!(!is_armed());
    }

    #[test]
    fn take_armed_config_is_one_shot() {
        disarm_embed();
        arm_embed(BackgroundEmbedConfig::default());
        assert!(is_armed());
        assert!(take_armed_config().is_some());
        assert!(!is_armed());
        assert!(take_armed_config().is_none());
    }

    #[test]
    fn cancel_flag_roundtrip() {
        clear_cancel();
        assert!(!is_cancel_requested());
        request_cancel_in_process_embed();
        assert!(is_cancel_requested());
        clear_cancel();
        assert!(!is_cancel_requested());
    }
}
