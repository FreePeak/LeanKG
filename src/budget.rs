//! Process-budget helper for heavy CLI/MCP operations.
//!
//! Wraps every long-running algorithm with hard caps on wall-clock time,
//! resident memory, and iteration count. Heavy tools (`impact`,
//! `export`, `check-consistency`, `gods`, `path`, `tunnels`, `report`,
//! `prs`) call `BudgetGuard::check()` inside their hot loops and abort
//! early when the budget is exhausted, instead of running for hours
//! against a 600k-element graph and OOM-killing the host.
//!
//! ## Configuration
//!
//! All defaults are tuned for a 32 GB workstation. Override via env vars:
//!
//! - `LEANKG_TOOL_TIMEOUT_SECS` (default `60`)  — per-call wall-clock cap
//! - `LEANKG_MAX_RSS_MB`       (default `4096`) — soft RSS cap; abort if exceeded
//! - `LEANKG_TOOL_BUDGET_OFF`  (default unset) — set `1` to disable all guards
//!
//! ## Usage
//!
//! ```ignore
//! let mut guard = BudgetGuard::for_tool("impact");
//! for i in 0..n {
//!     guard.tick();                          // increments iteration counter
//!     guard.check()?;                        // abort on timeout / RSS breach
//!     // ... expensive work ...
//! }
//! ```

use std::sync::atomic::{AtomicBool, Ordering};
use std::time::{Duration, Instant};

/// Reasons a budget guard can abort a loop.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum BudgetExceeded {
    /// Wall-clock time exceeded.
    Timeout {
        tool: String,
        elapsed_secs: u64,
        cap_secs: u64,
    },
    /// Resident memory exceeded.
    Memory {
        tool: String,
        rss_mb: u64,
        cap_mb: u64,
    },
    /// Explicit caller-chosen iteration cap.
    Iterations { tool: String, count: u64, cap: u64 },
}

impl std::fmt::Display for BudgetExceeded {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            BudgetExceeded::Timeout {
                tool,
                elapsed_secs,
                cap_secs,
            } => write!(
                f,
                "tool '{}' aborted: budget timeout ({elapsed_secs}s >= {cap_secs}s)",
                tool
            ),
            BudgetExceeded::Memory {
                tool,
                rss_mb,
                cap_mb,
            } => write!(
                f,
                "tool '{}' aborted: RSS budget exceeded ({rss_mb} MB >= {cap_mb} MB). \
                 Raise LEANKG_MAX_RSS_MB or scope the query smaller.",
                tool
            ),
            BudgetExceeded::Iterations { tool, count, cap } => write!(
                f,
                "tool '{}' aborted: iteration cap reached ({count} >= {cap})",
                tool
            ),
        }
    }
}

impl std::error::Error for BudgetExceeded {}

/// Serialize tests that mutate `LEANKG_TOOL_BUDGET_OFF`. The env
/// var is process-wide, so without this lock two tests can race:
/// one sets "1", another unsets it, and a third reads whichever
/// value is current — not the one it expected.
#[cfg(test)]
pub(crate) static ENV_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

/// Per-call guard that wraps an algorithm in a wall-clock + RSS + iteration
/// budget. Cheap to construct and check; intended to be hit on every loop
/// iteration.
pub struct BudgetGuard {
    tool: String,
    started: Instant,
    cap_secs: u64,
    cap_rss_mb: u64,
    cap_iters: u64,
    iters: u64,
    /// When true, all checks are no-ops. Used to bypass for tests / single-step calls.
    disabled: bool,
    /// One-shot flag so the guard's first breach is reported, not repeated.
    reported: AtomicBool,
}

impl BudgetGuard {
    /// Build a guard for `tool` using default caps (60s, 4 GB RSS, 1M iters).
    pub fn for_tool(tool: &str) -> Self {
        Self::with_caps(
            tool,
            default_timeout_secs(),
            default_rss_cap_mb(),
            1_000_000,
        )
    }

    /// Build a guard with explicit caps. `cap_iters` = 0 disables the iteration cap.
    pub fn with_caps(tool: &str, cap_secs: u64, cap_rss_mb: u64, cap_iters: u64) -> Self {
        let disabled = std::env::var("LEANKG_TOOL_BUDGET_OFF")
            .ok()
            .and_then(|v| v.parse::<u8>().ok())
            .map(|v| v != 0)
            .unwrap_or(false);
        Self {
            tool: tool.to_string(),
            started: Instant::now(),
            cap_secs,
            cap_rss_mb,
            cap_iters,
            iters: 0,
            disabled,
            reported: AtomicBool::new(false),
        }
    }

    /// Build a guard with only an iteration cap (no time / RSS).
    /// Use for bounded loops where time is naturally capped by iteration count.
    pub fn iter_only(tool: &str, cap_iters: u64) -> Self {
        Self::with_caps(tool, u64::MAX, u64::MAX, cap_iters)
    }

    /// Build a guard with no caps at all. Use for tools that have their own
    /// internal pagination (e.g. paginated DB queries).
    pub fn unlimited(tool: &str) -> Self {
        Self::with_caps(tool, u64::MAX, u64::MAX, 0)
    }

    /// Increment the iteration counter. Call once per loop body.
    pub fn tick(&mut self) {
        self.iters = self.iters.saturating_add(1);
    }

    /// Returns the elapsed time since this guard was created.
    pub fn elapsed(&self) -> Duration {
        self.started.elapsed()
    }

    /// Returns the current iteration count.
    pub fn iterations(&self) -> u64 {
        self.iters
    }

    /// Returns true iff the guard is in disabled mode.
    pub fn is_disabled(&self) -> bool {
        self.disabled
    }

    /// Returns true iff any cap has been breached.
    pub fn is_exhausted(&self) -> bool {
        if self.disabled {
            return false;
        }
        if self.cap_iters != 0 && self.iters >= self.cap_iters {
            return true;
        }
        if self.cap_secs != u64::MAX && self.started.elapsed().as_secs() >= self.cap_secs {
            return true;
        }
        if self.cap_rss_mb != u64::MAX {
            if let Ok(rss_mb) = current_rss_mb() {
                if rss_mb >= self.cap_rss_mb {
                    return true;
                }
            }
        }
        false
    }

    /// Returns Err with the first breach reason, or Ok(()) if everything is fine.
    pub fn check(&mut self) -> Result<(), BudgetExceeded> {
        if self.disabled {
            return Ok(());
        }
        // Iteration cap wins because it is cheapest to evaluate.
        if self.cap_iters != 0 && self.iters >= self.cap_iters {
            self.mark_reported();
            return Err(BudgetExceeded::Iterations {
                tool: self.tool.clone(),
                count: self.iters,
                cap: self.cap_iters,
            });
        }
        if self.cap_secs != u64::MAX {
            let elapsed = self.started.elapsed().as_secs();
            if elapsed >= self.cap_secs {
                self.mark_reported();
                return Err(BudgetExceeded::Timeout {
                    tool: self.tool.clone(),
                    elapsed_secs: elapsed,
                    cap_secs: self.cap_secs,
                });
            }
        }
        if self.cap_rss_mb != u64::MAX {
            if let Ok(rss_mb) = current_rss_mb() {
                if rss_mb >= self.cap_rss_mb {
                    self.mark_reported();
                    return Err(BudgetExceeded::Memory {
                        tool: self.tool.clone(),
                        rss_mb,
                        cap_mb: self.cap_rss_mb,
                    });
                }
            }
        }
        Ok(())
    }

    fn mark_reported(&self) {
        self.reported.store(true, Ordering::SeqCst);
    }

    /// Whether the guard has already reported a breach (useful to avoid
    /// emitting duplicate abort messages).
    pub fn already_reported(&self) -> bool {
        self.reported.load(Ordering::SeqCst)
    }
}

fn default_timeout_secs() -> u64 {
    std::env::var("LEANKG_TOOL_TIMEOUT_SECS")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(60)
}

fn default_rss_cap_mb() -> u64 {
    std::env::var("LEANKG_MAX_RSS_MB")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(4096)
}

/// Returns the current process's RSS in MiB. macOS / Linux / Windows
/// supported via the `sysinfo` crate at the caller site; here we use a
/// minimal libc-backed fallback so we don't pull in a new dependency
/// for one syscall.
///
/// On unknown platforms returns Ok(0) and the caller treats that as
/// "no RSS data, can't enforce".
pub fn current_rss_mb() -> Result<u64, std::io::Error> {
    current_rss_bytes().map(|b| b / (1024 * 1024))
}

#[cfg(target_os = "macos")]
fn current_rss_bytes() -> Result<u64, std::io::Error> {
    // Use proc_pidinfo with PROC_PIDTASKINFO which returns resident_size.
    // This is the documented public API; no need for raw Mach bindings.
    use libc::{c_int, c_void, pid_t};
    #[repr(C)]
    struct ProcTaskInfo {
        pti_virtual_size: u64,
        pti_resident_size: u64,
        pti_total_user: u64,
        pti_total_system: u64,
        pti_threads_user: u64,
        pti_threads_system: u64,
        pti_policy: i32,
        pti_faults: i32,
        pti_pageins: i32,
        pti_cow_faults: i32,
        pti_messages_sent: i32,
        pti_messages_received: i32,
        pti_syscalls_mach: i32,
        pti_syscalls_unix: i32,
        pti_csw: i32,
        pti_threadnum: i32,
        pti_numrunning: i32,
        pti_priority: i32,
    }
    const PROC_PIDTASKINFO: c_int = 4;
    extern "C" {
        fn proc_pidinfo(
            pid: pid_t,
            flavor: c_int,
            arg: u64,
            buffer: *mut c_void,
            buffersize: c_int,
        ) -> c_int;
    }
    let mut info: ProcTaskInfo = unsafe { std::mem::zeroed() };
    let bufsize = std::mem::size_of::<ProcTaskInfo>() as c_int;
    let pid = unsafe { libc::getpid() };
    let written = unsafe {
        proc_pidinfo(
            pid,
            PROC_PIDTASKINFO,
            0,
            &mut info as *mut _ as *mut c_void,
            bufsize,
        )
    };
    if written < bufsize {
        return Err(std::io::Error::last_os_error());
    }
    Ok(info.pti_resident_size)
}

#[cfg(target_os = "linux")]
fn current_rss_bytes() -> Result<u64, std::io::Error> {
    let content = std::fs::read_to_string("/proc/self/statm")?;
    // fields: size resident shared text lib data dt
    let rss_pages: u64 = content
        .split_whitespace()
        .nth(1)
        .and_then(|s| s.parse().ok())
        .unwrap_or(0);
    let page_size = unsafe { libc_getpagesize() } as u64;
    Ok(rss_pages * page_size)
}

#[cfg(target_os = "linux")]
unsafe fn libc_getpagesize() -> i32 {
    extern "C" {
        fn getpagesize() -> i32;
    }
    unsafe { getpagesize() }
}

#[cfg(not(any(target_os = "macos", target_os = "linux")))]
fn current_rss_bytes() -> Result<u64, std::io::Error> {
    Ok(0)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn guard_disabled_via_env_var() {
        // Lock to serialize against other tests that touch the
        // same env var; otherwise one test can unset it between
        // this test's set_var and the next assertion.
        let _guard = ENV_LOCK.lock().unwrap();
        std::env::set_var("LEANKG_TOOL_BUDGET_OFF", "1");
        let mut g = BudgetGuard::with_caps("test", 0, 0, 1);
        // Even with iteration cap = 1, we shouldn't abort because disabled.
        g.tick();
        g.tick();
        assert!(g.check().is_ok());
        assert!(!g.is_exhausted());
        std::env::remove_var("LEANKG_TOOL_BUDGET_OFF");
    }

    #[test]
    fn guard_iteration_cap_aborts() {
        let _guard = ENV_LOCK.lock().unwrap();
        std::env::remove_var("LEANKG_TOOL_BUDGET_OFF");
        let mut g = BudgetGuard::with_caps("test", u64::MAX, u64::MAX, 3);
        g.tick();
        g.tick();
        assert!(g.check().is_ok());
        g.tick();
        assert!(matches!(
            g.check(),
            Err(BudgetExceeded::Iterations {
                count: 3,
                cap: 3,
                ..
            })
        ));
    }

    #[test]
    fn guard_timeout_aborts() {
        let _guard = ENV_LOCK.lock().unwrap();
        std::env::remove_var("LEANKG_TOOL_BUDGET_OFF");
        // 0-second cap = any non-zero elapsed time triggers.
        // But elapsed >= 0 right after construction so we use iters=0 cap=0.
        let mut g = BudgetGuard::with_caps("test", 0, u64::MAX, 0);
        std::thread::sleep(Duration::from_millis(1100));
        assert!(matches!(g.check(), Err(BudgetExceeded::Timeout { .. })));
    }

    #[test]
    fn guard_unlimited_never_aborts() {
        let _guard = ENV_LOCK.lock().unwrap();
        std::env::remove_var("LEANKG_TOOL_BUDGET_OFF");
        let mut g = BudgetGuard::unlimited("test");
        for _ in 0..10_000 {
            g.tick();
        }
        assert!(g.check().is_ok());
    }

    #[test]
    fn guard_iter_only_no_time_or_rss() {
        let _guard = ENV_LOCK.lock().unwrap();
        std::env::remove_var("LEANKG_TOOL_BUDGET_OFF");
        let mut g = BudgetGuard::iter_only("test", 2);
        assert!(g.check().is_ok());
        g.tick();
        assert!(g.check().is_ok());
        g.tick();
        assert!(g.is_exhausted());
        assert!(g.check().is_err());
    }

    #[test]
    fn rss_reading_returns_zero_or_positive() {
        // We don't assert a specific value (CI runners vary), just that
        // the call doesn't panic and returns a plausible number.
        let _ = current_rss_mb().unwrap_or(0);
    }

    #[test]
    fn guard_already_reported_flag_is_set_after_first_breach() {
        let _guard = ENV_LOCK.lock().unwrap();
        std::env::remove_var("LEANKG_TOOL_BUDGET_OFF");
        let mut g = BudgetGuard::with_caps("test", u64::MAX, u64::MAX, 1);
        g.tick();
        assert!(g.check().is_err());
        assert!(g.already_reported());
    }

    #[test]
    fn guard_elapsed_grows_monotonically() {
        let g = BudgetGuard::for_tool("test");
        let e1 = g.elapsed();
        std::thread::sleep(Duration::from_millis(5));
        let e2 = g.elapsed();
        assert!(e2 >= e1);
    }
}
