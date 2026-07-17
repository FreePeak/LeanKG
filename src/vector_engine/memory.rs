//! RocksDB block-cache auto-tune from cgroups / sysinfo (FR-VE-RT-MEM).
//!
//! Local survival envelope: 2GB hard. Cloud may use 50–80% of available RAM.

use super::engine::EngineKind;

/// Default Local survival cap (bytes) from PRD §5.14 / NFR.
pub const LOCAL_SURVIVAL_CAP_BYTES: u64 = 2 * 1024 * 1024 * 1024;

/// Fraction of available RAM for Cloud block cache (mid of 50–80%).
pub const CLOUD_RAM_FRACTION: f64 = 0.65;

/// Minimum block cache so RocksDB stays usable under tiny budgets.
pub const MIN_BLOCK_CACHE_BYTES: u64 = 8 * 1024 * 1024;

/// Cap fraction of the survival/available budget reserved for SQ8 + OS.
pub const BLOCK_CACHE_BUDGET_FRACTION: f64 = 0.25;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct MemoryPlan {
    pub available_bytes: u64,
    pub block_cache_bytes: u64,
    pub survival_cap_bytes: u64,
}

/// Plan RocksDB block cache given available RAM and engine kind.
pub fn plan_block_cache(available_bytes: u64, kind: EngineKind) -> MemoryPlan {
    let survival_cap = match kind {
        EngineKind::Local => LOCAL_SURVIVAL_CAP_BYTES.min(available_bytes),
        EngineKind::Cloud => {
            let cloud = ((available_bytes as f64) * CLOUD_RAM_FRACTION) as u64;
            cloud.max(MIN_BLOCK_CACHE_BYTES)
        }
    };
    let budget = ((survival_cap as f64) * BLOCK_CACHE_BUDGET_FRACTION) as u64;
    let block_cache = budget.clamp(MIN_BLOCK_CACHE_BYTES, survival_cap);
    MemoryPlan {
        available_bytes,
        block_cache_bytes: block_cache,
        survival_cap_bytes: survival_cap,
    }
}

/// Read available system memory via sysinfo (fallback: Local survival cap).
pub fn available_memory_bytes() -> u64 {
    use sysinfo::System;
    let mut sys = System::new();
    sys.refresh_memory();
    let avail = sys.available_memory();
    if avail == 0 {
        LOCAL_SURVIVAL_CAP_BYTES
    } else {
        avail
    }
}

/// Auto-tune for the current host + engine kind.
pub fn auto_tune_block_cache(kind: EngineKind) -> MemoryPlan {
    plan_block_cache(available_memory_bytes(), kind)
}

/// Simulate a 2GB cgroup (FR-VE-BENCH-OOM helper).
pub fn plan_under_2gb_cgroup(kind: EngineKind) -> MemoryPlan {
    plan_block_cache(LOCAL_SURVIVAL_CAP_BYTES, kind)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn local_under_2gb_stays_within_cap() {
        let plan = plan_under_2gb_cgroup(EngineKind::Local);
        assert!(plan.block_cache_bytes <= LOCAL_SURVIVAL_CAP_BYTES);
        assert!(plan.block_cache_bytes >= MIN_BLOCK_CACHE_BYTES);
        assert_eq!(plan.survival_cap_bytes, LOCAL_SURVIVAL_CAP_BYTES);
    }

    #[test]
    fn cloud_uses_fraction_of_large_ram() {
        let avail = 64 * 1024 * 1024 * 1024u64;
        let plan = plan_block_cache(avail, EngineKind::Cloud);
        assert!(plan.survival_cap_bytes > LOCAL_SURVIVAL_CAP_BYTES);
        assert!(plan.block_cache_bytes <= plan.survival_cap_bytes);
    }

    #[test]
    fn tiny_available_still_meets_minimum() {
        let plan = plan_block_cache(32 * 1024 * 1024, EngineKind::Local);
        assert_eq!(plan.block_cache_bytes, MIN_BLOCK_CACHE_BYTES);
    }
}
