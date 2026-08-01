//! US-SM-07 / FR-SM-12: retention / GC for session refs and low-heat
//! agent-memory artifacts.
//!
//! Session `refs/` markdown and low-heat `MEMORY_INDEX` entries older than
//! the retention TTL are candidates for reclaim. Pinned (`.md.pin` marker
//! or caller-provided path list) and high-heat items are exempt. Min
//! retention is 3 days when GC is enabled (FR-SM-12).
//!
//! Pure candidate selection (`gc_candidates`) + explicit reclaim
//! (`reclaim_gc_candidates`) — no silent background thread, no YAML writes.

use std::path::Path;

use serde::Serialize;

/// FR-SM-12: minimum retention when GC is enabled.
pub const MIN_GC_RETENTION_DAYS: u64 = 3;
/// Default TTL for session refs / low-heat memories (14 days).
pub const DEFAULT_GC_TTL_DAYS: u64 = 14;
/// Retention days surfaced by the MCP tool (same default).
pub const DEFAULT_GC_RETENTION_DAYS: u64 = 14;
/// Heat floor: items whose `heat_score` is below this are "low heat".
/// Mirrors `session::heat_score` (log1p recalls + 0.5^(age/86400)).
pub const GC_HEAT_THRESHOLD: f64 = 1.5;
/// Pinned marker suffix beside a ref file (`refs/<node_id>.md.pin`).
pub const PIN_SUFFIX: &str = ".pin";

/// One candidate: a file path + the metadata used to decide eligibility.
#[derive(Debug, Clone, PartialEq)]
pub struct GcCandidate {
    pub path: String,
    pub session_id: String,
    /// Last modification epoch secs (or last recall for memory items).
    pub last_activity_epoch_secs: u64,
    pub recalls: u64,
}

impl GcCandidate {
    pub fn new(path: &str, session_id: &str, last_activity_epoch_secs: u64, recalls: u64) -> Self {
        Self {
            path: path.to_string(),
            session_id: session_id.to_string(),
            last_activity_epoch_secs,
            recalls,
        }
    }
}

/// Result of a GC pass (FR-SM-12 "reclaim" accounting).
#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct GcReport {
    pub scanned: usize,
    pub removed: usize,
    pub failed: usize,
    pub exempt_pinned: usize,
    pub exempt_high_heat: usize,
    pub exempt_recent: usize,
    /// Human-readable summary line.
    pub summary: String,
}

/// Pure eligibility filter (FR-SM-12). A candidate is reclaimed when it is
/// BOTH older than `ttl_days` AND below the heat floor, UNLESS it is in
/// `pinned_paths`. Deterministic: same inputs → same output.
pub fn gc_candidates(
    candidates: &[GcCandidate],
    ttl_days: u64,
    heat_threshold: f64,
    pinned_paths: &[&str],
) -> Vec<GcCandidate> {
    let ttl_secs = ttl_days.max(MIN_GC_RETENTION_DAYS) * 86400;
    let now_secs = now_unix();
    candidates
        .iter()
        .filter(|c| {
            let age = now_secs.saturating_sub(c.last_activity_epoch_secs);
            let heat = crate::session::heat_score(c.recalls, c.last_activity_epoch_secs, now_secs);
            age >= ttl_secs && heat < heat_threshold && !pinned_paths.contains(&c.path.as_str())
        })
        .cloned()
        .collect()
}

/// Delete the candidate files and return a [`GcReport`]. Missing files are
/// tolerated (counted as removed=0, failed=0) so a concurrent recall cannot
/// make GC error. Never touches ontology YAML.
pub fn reclaim_gc_candidates(candidates: &[GcCandidate]) -> Result<GcReport, String> {
    let mut removed = 0usize;
    let mut failed = 0usize;
    for c in candidates {
        match std::fs::remove_file(&c.path) {
            Ok(()) => removed += 1,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
            Err(e) => {
                failed += 1;
                tracing::warn!(
                    target: "leankg::session",
                    error = %e,
                    path = %c.path,
                    "session GC failed to remove ref"
                );
            }
        }
    }
    Ok(GcReport {
        scanned: candidates.len(),
        removed,
        failed,
        exempt_pinned: 0,
        exempt_high_heat: 0,
        exempt_recent: 0,
        summary: format!(
            "session GC: removed {removed} of {} candidates",
            candidates.len()
        ),
    })
}

/// Convenience: is `path` pinned by a sidecar `<path><PIN_SUFFIX>` marker?
pub fn is_pinned(path: &Path) -> bool {
    let mut marker = path.as_os_str().to_os_string();
    marker.push(PIN_SUFFIX);
    Path::new(&marker).exists()
}

fn now_unix() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn unit_pin_suffix_is_dot_pin() {
        assert_eq!(PIN_SUFFIX, ".pin");
        assert!(is_pinned(Path::new("/x/refs/a.md.pin")) == false);
    }
}
