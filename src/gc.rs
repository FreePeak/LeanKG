//! Memory pressure / GC helper for long-running leankg processes.
//!
//! Rust's default allocator doesn't compact, so when a daemon
//! (`mcp-stdio`, `mcp-http`, `watch`) holds onto a `Vec<CodeElement>`
//! for a request, then drops it, the heap pages are returned to the
//! allocator but not necessarily to the OS. The OS-level RSS only
//! shrinks when the allocator hands pages back via `malloc_trim`.
//!
//! This module exposes a [`MemoryGuard`] that:
//! - Polls RSS every N seconds via the same `proc_pidinfo` /
//!   `/proc/self/statm` syscall [`crate::budget::current_rss_mb`]
//!   uses.
//! - When the configured `idle_after_secs` passes with no recorded
//!   activity, runs the release callback **once per idle period**
//!   (resets on the next [`MemoryGuard::touch`]).
//! - When RSS exceeds `max_rss_mb`, runs an aggressive force-trim
//!   (throttled) so the supervisor has a chance to reclaim caches.
//!
//! Daemons (`mcp-http`, `mcp-stdio`, `watch`) are expected to call
//! [`MemoryGuard::touch`] after every successful request so the
//! idle timer resets.

use crate::budget::current_rss_mb;
use std::sync::atomic::{AtomicI64, AtomicU64, Ordering};
use std::time::{Duration, Instant};

/// A monotonic clock used by [`MemoryGuard::touch`] and the idle
/// detector. We keep it on the heap (AtomicU64 nanos since some
/// fixed point) so the value survives across moves of the guard.
static LAST_ACTIVITY_NANOS: AtomicU64 = AtomicU64::new(0);

/// Last RSS reading in MB, used by the diagnostic `rss_mb()` accessor.
static LAST_RSS_MB: AtomicI64 = AtomicI64::new(0);

/// Process-wide threshold at which we run an aggressive trim. When
/// RSS exceeds this we run `release_memory()` regardless of idle
/// state. Default 4 GB; override via `LEANKG_GC_MAX_RSS_MB`.
fn gc_max_rss_mb() -> u64 {
    std::env::var("LEANKG_GC_MAX_RSS_MB")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(4_096)
}

/// Idle interval after which the guard's first poll tries to release
/// memory. Default 60 s. Override via `LEANKG_GC_IDLE_AFTER_SECS`.
fn gc_idle_after_secs() -> u64 {
    std::env::var("LEANKG_GC_IDLE_AFTER_SECS")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(60)
}

/// Polling interval. Default 10 s. Override via
/// `LEANKG_GC_POLL_SECS`.
fn gc_poll_secs() -> u64 {
    std::env::var("LEANKG_GC_POLL_SECS")
        .ok()
        .and_then(|v| v.parse().ok())
        .filter(|n| *n > 0)
        .unwrap_or(10)
}

/// Minimum gap between force-trims while RSS stays over the cap.
const FORCE_TRIM_COOLDOWN_SECS: u64 = 30;

/// Callback the guard calls when it wants the daemon to drop its
/// in-RAM caches. Returns `true` when something was actually
/// released (so callers can avoid logging no-ops).
pub type ReleaseFn = Box<dyn Fn() -> bool + Send + Sync + 'static>;

/// Long-running memory-pressure guard. One per daemon process.
pub struct MemoryGuard {
    started: Instant,
    last_check: Instant,
    last_force_trim: Instant,
    /// `LAST_ACTIVITY_NANOS` value we already idle-trimmed for.
    /// Equal means this idle period was already handled; a newer
    /// activity stamp (from [`Self::touch`]) arms idle-trim again.
    last_idle_trim_activity: u64,
    release_fn: Option<ReleaseFn>,
}

impl MemoryGuard {
    /// Create a new guard. `release_fn` is invoked on idle-trim and
    /// force-trim; it should return whether caches were dropped.
    pub fn new(release_fn: Option<ReleaseFn>) -> Self {
        let now = Instant::now();
        // Initialize LAST_ACTIVITY_NANOS so the daemon doesn't think
        // it's been idle since the epoch.
        Self::record_activity();
        Self {
            started: now,
            last_check: now,
            last_force_trim: now,
            last_idle_trim_activity: LAST_ACTIVITY_NANOS.load(Ordering::Relaxed),
            release_fn,
        }
    }

    /// Configured poll interval (`LEANKG_GC_POLL_SECS`, default 10s).
    pub fn poll_interval() -> Duration {
        Duration::from_secs(gc_poll_secs())
    }

    /// Record that something happened (request served, file watched,
    /// etc.). Resets the idle timer.
    pub fn touch() {
        Self::record_activity();
    }

    fn record_activity() {
        let now_nanos = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_nanos() as u64)
            .unwrap_or(0);
        LAST_ACTIVITY_NANOS.store(now_nanos, Ordering::Relaxed);
    }

    fn activity_nanos() -> u64 {
        LAST_ACTIVITY_NANOS.load(Ordering::Relaxed)
    }

    fn idle_secs() -> u64 {
        let last = Self::activity_nanos();
        if last == 0 {
            return 0;
        }
        let now_nanos = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_nanos() as u64)
            .unwrap_or(0);
        (now_nanos.saturating_sub(last)) / 1_000_000_000
    }

    /// Returns the most recent RSS reading in MB, or 0 if unknown.
    pub fn rss_mb() -> u64 {
        LAST_RSS_MB.load(Ordering::Relaxed).max(0) as u64
    }

    /// One tick of the GC loop. Returns the action that was taken,
    /// if any. Daemons should call this from a periodic background
    /// task.
    pub fn tick(&mut self) -> GcAction {
        let now = Instant::now();
        if now.duration_since(self.last_check) < Duration::from_secs(gc_poll_secs()) {
            return GcAction::Skipped;
        }
        self.last_check = now;

        // Update RSS observation (single syscall per tick).
        let rss = match current_rss_mb() {
            Ok(rss) => {
                LAST_RSS_MB.store(rss as i64, Ordering::Relaxed);
                rss
            }
            Err(_) => Self::rss_mb(),
        };
        let idle = Self::idle_secs();
        let activity = Self::activity_nanos();

        // Force-trim: RSS over the hard cap (throttled).
        if rss >= gc_max_rss_mb() {
            if now.duration_since(self.last_force_trim)
                < Duration::from_secs(FORCE_TRIM_COOLDOWN_SECS)
            {
                return GcAction::Skipped;
            }
            self.last_force_trim = now;
            if self.run_release() {
                eprintln!(
                    "leankg::gc: RSS {} MB >= max {} MB; force-trimmed caches",
                    rss,
                    gc_max_rss_mb()
                );
                return GcAction::ForceTrim { rss_mb: rss };
            }
            return GcAction::NoOp { rss_mb: rss };
        }

        // Idle-trim: once per idle period (armed again only after touch).
        if idle >= gc_idle_after_secs() && activity != self.last_idle_trim_activity {
            self.last_idle_trim_activity = activity;
            if self.run_release() {
                eprintln!(
                    "leankg::gc: idle for {}s; trimmed caches (RSS {} MB)",
                    idle, rss
                );
                return GcAction::IdleTrim {
                    idle_secs: idle,
                    rss_mb: rss,
                };
            }
            return GcAction::NoOp { rss_mb: rss };
        }

        GcAction::NoOp { rss_mb: rss }
    }

    fn run_release(&self) -> bool {
        match &self.release_fn {
            Some(f) => f(),
            None => false,
        }
    }

    /// Total time this guard has been alive. Useful for logging.
    pub fn uptime(&self) -> Duration {
        self.started.elapsed()
    }
}

/// What the GC guard did on a tick.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum GcAction {
    /// Didn't run anything this tick.
    Skipped,
    /// RSS is healthy; no work needed (or release was a no-op).
    NoOp { rss_mb: u64 },
    /// Ran the release callback because the process had been idle.
    IdleTrim { idle_secs: u64, rss_mb: u64 },
    /// Ran the release callback because RSS exceeded the hard cap.
    ForceTrim { rss_mb: u64 },
}

/// Best-effort "release memory to the OS" hook. Drops the supplied
/// closure result and trims the global heap.
///
/// We do not depend on a third-party allocator; instead we ask
/// `libc::malloc_trim` (Linux / glibc) or just let it be a no-op on
/// other platforms. Rust's default allocator is system malloc on
/// Linux and macOS, so this works in practice.
pub fn trim_heap() -> bool {
    #[cfg(target_os = "linux")]
    unsafe {
        // malloc_trim(0) returns 1 on success, 0 on failure. We pass
        // 0 so all unused pages are released back to the OS.
        extern "C" {
            fn malloc_trim(pad: usize) -> i32;
        }
        malloc_trim(0) == 1
    }
    #[cfg(not(target_os = "linux"))]
    {
        // macOS allocator is not exposed via malloc_trim, but the
        // system's default allocator (called by the Rust runtime)
        // does return pages on large frees. There's no portable
        // hint to force it, so we just report false.
        false
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};

    #[test]
    fn guard_touch_resets_idle() {
        MemoryGuard::touch();
        assert_eq!(MemoryGuard::idle_secs(), 0);
    }

    #[test]
    fn rss_mb_returns_nonzero_or_zero() {
        // Don't assert a specific value (CI runners vary) — just
        // that the call doesn't panic.
        let _ = MemoryGuard::rss_mb();
    }

    #[test]
    fn trim_heap_does_not_panic() {
        let _ = trim_heap();
    }

    #[test]
    fn tick_returns_a_variant() {
        let mut g = MemoryGuard::new(None);
        let _ = g.tick();
    }

    #[test]
    fn tick_skips_when_poll_window_not_elapsed() {
        let mut g = MemoryGuard::new(None);
        g.last_check = Instant::now();
        let action = g.tick();
        assert_eq!(action, GcAction::Skipped);
    }

    #[test]
    fn poll_interval_is_positive() {
        assert!(MemoryGuard::poll_interval() >= Duration::from_secs(1));
    }

    #[test]
    fn idle_trim_runs_at_most_once_per_activity_period() {
        let called = std::sync::Arc::new(AtomicUsize::new(0));
        let called2 = called.clone();
        let mut g = MemoryGuard::new(Some(Box::new(move || {
            called2.fetch_add(1, Ordering::Relaxed);
            true
        })));

        // Make idle_secs() large without sleeping: store activity in the past.
        g.last_check = Instant::now() - Duration::from_secs(gc_poll_secs() + 1);
        let past = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_nanos() as u64)
            .unwrap_or(0)
            .saturating_sub((gc_idle_after_secs() + 5) * 1_000_000_000);
        LAST_ACTIVITY_NANOS.store(past, Ordering::Relaxed);
        g.last_idle_trim_activity = 0; // different from `past` → armed

        let first = g.tick();
        assert!(
            matches!(first, GcAction::IdleTrim { .. }),
            "expected IdleTrim, got {:?}",
            first
        );
        assert_eq!(called.load(Ordering::Relaxed), 1);

        // Same idle period: further ticks must not release again.
        g.last_check = Instant::now() - Duration::from_secs(gc_poll_secs() + 1);
        let second = g.tick();
        assert!(
            matches!(second, GcAction::NoOp { .. } | GcAction::Skipped),
            "expected NoOp/Skipped after once-per-idle, got {:?}",
            second
        );
        assert_eq!(called.load(Ordering::Relaxed), 1);

        // New activity arms idle-trim again after another idle window.
        MemoryGuard::touch();
        let past2 = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_nanos() as u64)
            .unwrap_or(0)
            .saturating_sub((gc_idle_after_secs() + 5) * 1_000_000_000);
        LAST_ACTIVITY_NANOS.store(past2, Ordering::Relaxed);
        g.last_check = Instant::now() - Duration::from_secs(gc_poll_secs() + 1);
        let third = g.tick();
        assert!(
            matches!(third, GcAction::IdleTrim { .. }),
            "expected IdleTrim after new activity, got {:?}",
            third
        );
        assert_eq!(called.load(Ordering::Relaxed), 2);
    }

    #[test]
    fn release_returning_false_is_noop_not_idle_trim() {
        let mut g = MemoryGuard::new(Some(Box::new(|| false)));
        g.last_check = Instant::now() - Duration::from_secs(gc_poll_secs() + 1);
        let past = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_nanos() as u64)
            .unwrap_or(0)
            .saturating_sub((gc_idle_after_secs() + 5) * 1_000_000_000);
        LAST_ACTIVITY_NANOS.store(past, Ordering::Relaxed);
        g.last_idle_trim_activity = 0;

        let action = g.tick();
        assert!(
            matches!(action, GcAction::NoOp { .. }),
            "expected NoOp when release returns false, got {:?}",
            action
        );
        // Period consumed so we do not retry every poll.
        assert_eq!(g.last_idle_trim_activity, past);
    }
}
