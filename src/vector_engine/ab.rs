//! Agent A/B harness hook (FR-VE-BENCH-AB).
//!
//! Floors (vs grep/cat baseline, ≥100 tasks):
//! - ≥60% token reduction
//! - ≥80% tool-call reduction
//! - ≥2× faster time-to-resolution
//! - success rate ≥ baseline
//!
//! Runs via `scripts/run_kilo_ab_final.sh` (or the existing agent harness).
//! This module records the floors for gate checks.

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct AbFloors {
    pub token_reduction: f64,
    pub tool_call_reduction: f64,
    pub speedup: f64,
}

impl Default for AbFloors {
    fn default() -> Self {
        Self {
            token_reduction: 0.60,
            tool_call_reduction: 0.80,
            speedup: 2.0,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct AbResult {
    pub token_reduction: f64,
    pub tool_call_reduction: f64,
    pub speedup: f64,
    pub success_ge_baseline: bool,
}

impl AbResult {
    pub fn meets_floors(&self, floors: AbFloors) -> bool {
        self.token_reduction >= floors.token_reduction
            && self.tool_call_reduction >= floors.tool_call_reduction
            && self.speedup >= floors.speedup
            && self.success_ge_baseline
    }
}

/// Placeholder until the external harness is wired; returns not-ready.
pub fn load_ab_result_from_env() -> Option<AbResult> {
    // Optional: LEANKG_VE_AB_JSON='{"token_reduction":0.61,...}'
    let raw = std::env::var("LEANKG_VE_AB_JSON").ok()?;
    // Minimal parse without serde dependency on free-form — use serde_json.
    let v: serde_json::Value = serde_json::from_str(&raw).ok()?;
    Some(AbResult {
        token_reduction: v.get("token_reduction")?.as_f64()?,
        tool_call_reduction: v.get("tool_call_reduction")?.as_f64()?,
        speedup: v.get("speedup")?.as_f64()?,
        success_ge_baseline: v.get("success_ge_baseline")?.as_bool()?,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn floors_match_prd() {
        let f = AbFloors::default();
        assert!((f.token_reduction - 0.60).abs() < 1e-9);
        assert!((f.tool_call_reduction - 0.80).abs() < 1e-9);
        assert!((f.speedup - 2.0).abs() < 1e-9);
    }

    #[test]
    fn result_meets_floors() {
        let r = AbResult {
            token_reduction: 0.61,
            tool_call_reduction: 0.84,
            speedup: 2.1,
            success_ge_baseline: true,
        };
        assert!(r.meets_floors(AbFloors::default()));
    }
}
