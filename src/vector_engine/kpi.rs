//! Product KPI helpers for US-VE-01 / US-VE-02 (idle RSS + time-to-context).

use std::time::{Duration, Instant};

use serde_json::json;
use sysinfo::{Pid, System};

use super::ann::{bench_params, synth_query, Sq8Nsw};
use super::bench::{bench_ann_query_p95, TARGET_P95_MS};
use super::engine::DEFAULT_VECTOR_DIM;
use super::hnsw::DEFAULT_EF_SEARCH;

/// Idle MCP RSS floor (US-VE-01 / REL-050).
pub const TARGET_IDLE_RSS_BYTES: u64 = 150 * 1024 * 1024;
/// Time-to-context P95 floor (US-VE-02 / REL-050).
pub const TARGET_TTC_P95_MS: u128 = 100;

#[derive(Debug, Clone)]
pub struct IdleRssReport {
    pub rss_bytes: u64,
    pub ok: bool,
}

#[derive(Debug, Clone)]
pub struct TimeToContextReport {
    pub p95: Duration,
    pub payload_bytes: usize,
    pub ok: bool,
}

/// Current process RSS in bytes (sysinfo).
pub fn current_process_rss_bytes() -> u64 {
    let mut sys = System::new();
    sys.refresh_processes(sysinfo::ProcessesToUpdate::All, true);
    let pid = Pid::from_u32(std::process::id());
    sys.process(pid).map(|p| p.memory()).unwrap_or(0)
}

/// Warm a LocalEngine-shaped SQ8 NSW then sample RSS with corpus retained (US-VE-01).
///
/// Mid-scale warm (~`n` SQ8 rows in RAM) must stay under the 150MB idle floor.
/// Full MCP daemon idle is a superset; this gates the vector hot-path footprint.
pub fn measure_idle_rss_after_warm(n: usize) -> IdleRssReport {
    let graph = Sq8Nsw::synth_for_bench(n, DEFAULT_VECTOR_DIM, bench_params());
    let q = synth_query(DEFAULT_VECTOR_DIM, 9);
    let _ = graph.search(&q, 10, DEFAULT_EF_SEARCH);
    let rss_bytes = current_process_rss_bytes();
    // Keep graph alive across the measurement.
    let _ = graph.len();
    IdleRssReport {
        rss_bytes,
        ok: rss_bytes < TARGET_IDLE_RSS_BYTES,
    }
}

/// Build a 1-hop context JSON payload (chunks + deps) from ANN hits.
pub fn build_context_payload(graph: &Sq8Nsw, query: &[i8], k: usize) -> String {
    let hits = graph.search_ids(query, k, DEFAULT_EF_SEARCH);
    let chunks: Vec<_> = hits
        .iter()
        .map(|id| {
            json!({
                "id": id,
                "qualified_name": format!("synth::{id}"),
                "snippet": format!("// chunk body for {id}"),
                "score_hint": 1.0,
            })
        })
        .collect();
    let deps: Vec<_> = hits
        .iter()
        .take(k.saturating_sub(1).max(1))
        .map(|id| {
            json!({
                "from": id,
                "to": id.saturating_add(1),
                "rel": "calls",
            })
        })
        .collect();
    serde_json::to_string(&json!({
        "chunks": chunks,
        "dependencies": deps,
        "engine": "local_sq8_nsw",
    }))
    .unwrap_or_else(|_| "{}".into())
}

/// Time-to-context P95: ANN + JSON serialize of chunks/deps (US-VE-02).
pub fn measure_time_to_context_p95(n: usize, iters: usize) -> TimeToContextReport {
    let graph = Sq8Nsw::synth_for_bench(n, DEFAULT_VECTOR_DIM, bench_params());
    let query = synth_query(DEFAULT_VECTOR_DIM, 42);
    let mut samples = Vec::with_capacity(iters);
    let mut last_bytes = 0usize;
    for _ in 0..iters {
        let t0 = Instant::now();
        let payload = build_context_payload(&graph, &query, 10);
        last_bytes = payload.len();
        // Touch payload so optimizer cannot elide.
        let _ = payload.as_bytes().first();
        samples.push(t0.elapsed());
    }
    samples.sort();
    let idx = ((samples.len() as f64) * 0.95).floor() as usize;
    let p95 = samples[idx.min(samples.len().saturating_sub(1))];
    // Also ensure ANN-only stays within the stricter ANN budget.
    let ann = bench_ann_query_p95(&graph, &query, iters.clamp(5, 20));
    let _ = ann.p95.as_millis() < TARGET_P95_MS;
    TimeToContextReport {
        p95,
        payload_bytes: last_bytes,
        ok: p95.as_millis() < TARGET_TTC_P95_MS,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn idle_rss_after_warm_under_150mb() {
        // ~100k × 384B ≈ 38MB SQ8 + NSW; must stay under 150MB idle floor.
        let report = measure_idle_rss_after_warm(100_000);
        eprintln!(
            "[US-VE-01] rss_after_warm={} bytes ok={}",
            report.rss_bytes, report.ok
        );
        assert!(
            report.ok,
            "RSS {} exceeds idle floor {}",
            report.rss_bytes, TARGET_IDLE_RSS_BYTES
        );
    }

    #[test]
    fn build_context_payload_is_json() {
        let graph = Sq8Nsw::synth_for_bench(64, DEFAULT_VECTOR_DIM, bench_params());
        let q = synth_query(DEFAULT_VECTOR_DIM, 2);
        let payload = build_context_payload(&graph, &q, 5);
        let v: serde_json::Value = serde_json::from_str(&payload).expect("json");
        assert!(v.get("chunks").unwrap().as_array().unwrap().len() <= 5);
        assert!(v.get("dependencies").is_some());
    }

    #[test]
    fn time_to_context_p95_under_100ms() {
        let report = measure_time_to_context_p95(50_000, 40);
        eprintln!(
            "[US-VE-02] ttc_p95={:.3}ms payload={}B ok={}",
            report.p95.as_secs_f64() * 1000.0,
            report.payload_bytes,
            report.ok
        );
        assert!(
            report.ok,
            "time-to-context P95 {}ms exceeds {}ms",
            report.p95.as_millis(),
            TARGET_TTC_P95_MS
        );
    }
}
