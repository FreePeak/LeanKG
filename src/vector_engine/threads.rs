//! Dynamic rayon thread pool sizing (FR-VE-RT-THREADS).
//!
//! Local: leave **2 cores free** for OS/IDE. Cloud: utilize full machine.

use super::engine::EngineKind;

/// Cores reserved for OS/IDE on Local engine.
pub const LOCAL_RESERVED_CORES: usize = 2;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ThreadPlan {
    pub logical_cpus: usize,
    pub worker_threads: usize,
    pub reserved_cores: usize,
}

/// Compute worker thread count for the given engine kind.
pub fn plan_threads(logical_cpus: usize, kind: EngineKind) -> ThreadPlan {
    let cpus = logical_cpus.max(1);
    match kind {
        EngineKind::Local => {
            let workers = cpus.saturating_sub(LOCAL_RESERVED_CORES).max(1);
            ThreadPlan {
                logical_cpus: cpus,
                worker_threads: workers,
                reserved_cores: LOCAL_RESERVED_CORES.min(cpus.saturating_sub(1)),
            }
        }
        EngineKind::Cloud => ThreadPlan {
            logical_cpus: cpus,
            worker_threads: cpus,
            reserved_cores: 0,
        },
    }
}

/// Detect host logical CPUs (fallback 1).
pub fn logical_cpus() -> usize {
    std::thread::available_parallelism()
        .map(|n| n.get())
        .unwrap_or(1)
}

pub fn auto_tune_threads(kind: EngineKind) -> ThreadPlan {
    plan_threads(logical_cpus(), kind)
}

/// Build a rayon pool with the planned worker count.
pub fn build_rayon_pool(
    plan: ThreadPlan,
) -> Result<rayon::ThreadPool, rayon::ThreadPoolBuildError> {
    rayon::ThreadPoolBuilder::new()
        .num_threads(plan.worker_threads)
        .thread_name(|i| format!("leankg-ve-{i}"))
        .build()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn local_leaves_two_cores_free() {
        let plan = plan_threads(10, EngineKind::Local);
        assert_eq!(plan.worker_threads, 8);
        assert_eq!(plan.reserved_cores, 2);
    }

    #[test]
    fn local_on_two_cores_keeps_one_worker() {
        let plan = plan_threads(2, EngineKind::Local);
        assert_eq!(plan.worker_threads, 1);
    }

    #[test]
    fn cloud_uses_all_cores() {
        let plan = plan_threads(16, EngineKind::Cloud);
        assert_eq!(plan.worker_threads, 16);
        assert_eq!(plan.reserved_cores, 0);
    }

    #[test]
    fn build_pool_succeeds() {
        let plan = plan_threads(4, EngineKind::Local);
        let pool = build_rayon_pool(plan).unwrap();
        let sum: i32 = pool.install(|| (0..10).map(|x| x).sum());
        assert_eq!(sum, 45);
    }
}
