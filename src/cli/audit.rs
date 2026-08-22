//! FR-ENT-1 (backlog H2): `leankg audit export` / `leankg audit verify`.
//!
//! Thin CLI surface over the audit ledger read APIs. Time filters accept
//! RFC 3339 timestamps, unix-epoch seconds, or relative durations
//! (`90s`, `30m`, `24h`, `7d`).

use crate::audit::{rows_to_jsonl, verify_chain, AuditEntry, VerifyError};
use crate::db::backend::SharedDb;
use std::time::{Duration, SystemTime};

/// Parse a user-supplied time filter into an absolute [`SystemTime`].
///
/// Accepted forms:
/// - Relative to now: `90s`, `30m`, `24h`, `7d`
/// - Unix epoch seconds: `1770000000`
/// - RFC 3339 / ISO-8601: `2026-08-22T10:00:00Z`
pub fn parse_time_filter(raw: &str) -> Result<SystemTime, String> {
    let raw = raw.trim();
    if raw.is_empty() {
        return Err("empty time filter".to_string());
    }

    // Relative duration: <digits><s|m|h|d>
    if raw.len() > 1 && raw[..raw.len() - 1].chars().all(|c| c.is_ascii_digit()) {
        if let Some(unit) = raw
            .chars()
            .last()
            .and_then(|c| ("smhd".contains(c)).then_some(c))
        {
            let n: u64 = raw[..raw.len() - 1]
                .parse()
                .map_err(|e| format!("invalid time filter `{raw}`: {e}"))?;
            let secs = match unit {
                's' => n,
                'm' => n * 60,
                'h' => n * 3600,
                _ => n * 86_400,
            };
            return SystemTime::now()
                .checked_sub(Duration::from_secs(secs))
                .ok_or_else(|| format!("time filter `{raw}` underflows the clock"));
        }
    }

    // Unix epoch seconds.
    if raw.chars().all(|c| c.is_ascii_digit()) {
        if let Ok(secs) = raw.parse::<u64>() {
            return Ok(SystemTime::UNIX_EPOCH + Duration::from_secs(secs));
        }
    }

    // RFC 3339 via chrono (already in the dependency tree).
    let dt = chrono::DateTime::parse_from_rfc3339(raw)
        .map_err(|e| format!("cannot parse time filter `{raw}`: {e}"))?;
    Ok(SystemTime::UNIX_EPOCH + Duration::from_millis(dt.timestamp_millis() as u64))
}

/// Export ledger rows in the window as JSONL text (one object per line).
pub fn export_ledger_jsonl(
    backend: &SharedDb,
    since: Option<SystemTime>,
    until: Option<SystemTime>,
) -> Result<String, String> {
    let rows: Vec<AuditEntry> = backend
        .query_audit(since, until)
        .map_err(|e| format!("cannot read audit ledger: {e}"))?;
    Ok(rows_to_jsonl(&rows))
}

/// Verify the chain over the ledger window. `Ok(count)` when intact.
pub fn verify_ledger(
    backend: &SharedDb,
    since: Option<SystemTime>,
    until: Option<SystemTime>,
) -> Result<usize, VerifyError> {
    let rows: Vec<AuditEntry> = backend.query_audit(since, until).map_err(|e| VerifyError {
        seq: -1,
        reason: format!("cannot read audit ledger: {e}"),
    })?;
    verify_chain(&rows).map(|()| rows.len())
}

#[cfg(test)]
mod tests {
    use crate::audit::{verify_chain, AuditRecord};
    use crate::db::backend::{DbBackend, SharedDb};
    use crate::db::fake::FakeBackend;
    use std::sync::Arc;
    use std::time::{Duration, SystemTime};

    #[test]
    fn parse_time_filter_accepts_relative_durations() {
        let now = SystemTime::now();
        for (raw, approx_secs) in [
            ("24h", 86_400u64),
            ("30m", 1_800),
            ("7d", 604_800),
            ("90s", 90),
        ] {
            let t = super::parse_time_filter(raw).unwrap_or_else(|e| panic!("{raw}: {e}"));
            let delta = now.duration_since(t).unwrap().as_secs();
            assert!(
                delta >= approx_secs.saturating_sub(5) && delta <= approx_secs + 5,
                "{raw} should resolve to ~{approx_secs}s ago, got {delta}s"
            );
        }
    }

    #[test]
    fn parse_time_filter_accepts_rfc3339_and_epoch_seconds() {
        let t = super::parse_time_filter("2026-08-22T10:00:00Z").unwrap();
        assert_eq!(
            t.duration_since(SystemTime::UNIX_EPOCH).unwrap().as_secs(),
            1_787_392_800
        );
        let t = super::parse_time_filter("0").unwrap();
        assert_eq!(t, SystemTime::UNIX_EPOCH);
    }

    #[test]
    fn parse_time_filter_rejects_garbage() {
        for bad in ["", "yesterday", "2026-13-99T99:00:00Z", "-5m"] {
            assert!(
                super::parse_time_filter(bad).is_err(),
                "`{bad}` must not parse"
            );
        }
    }

    fn ts(ms: u64) -> SystemTime {
        SystemTime::UNIX_EPOCH + Duration::from_millis(ms)
    }

    fn rec(ms: u64, tool: &str) -> AuditRecord {
        AuditRecord {
            ts: ts(ms),
            actor: "local".into(),
            agent_client: "test".into(),
            tool: tool.into(),
            project: None,
            args_hash: "a".repeat(64),
            result_status: "ok".into(),
        }
    }

    fn seeded_backend() -> Arc<FakeBackend> {
        let b = Arc::new(FakeBackend::new());
        let entries = crate::audit::chain_records(
            &[rec(100, "t1"), rec(200, "t2"), rec(300, "t3")],
            crate::audit::GENESIS_HASH,
        );
        b.insert_audit_batch(&entries).unwrap();
        b
    }

    #[test]
    fn export_ledger_jsonl_round_trips_the_window() {
        let b = seeded_backend();
        let shared: SharedDb = b.clone();
        let jsonl = super::export_ledger_jsonl(&shared, Some(ts(150)), Some(ts(250))).unwrap();
        let lines: Vec<&str> = jsonl.lines().collect();
        assert_eq!(lines.len(), 1);
        let v: serde_json::Value = serde_json::from_str(lines[0]).unwrap();
        assert_eq!(v["tool"], "t2");
        assert_eq!(v["entry_hash"].as_str().unwrap().len(), 64);
    }

    #[test]
    fn verify_ledger_ok_names_count_and_tamper_fails_with_seq() {
        let b = seeded_backend();
        let shared: SharedDb = b.clone();

        assert_eq!(super::verify_ledger(&shared, None, None).unwrap(), 3);
        assert_eq!(
            super::verify_ledger(&shared, Some(ts(150)), None).unwrap(),
            2
        );

        // Tamper with row 2 directly in the backend buffer → verify names seq.
        let mut entries = b.query_audit(None, None).unwrap();
        entries[1].result_status = "error".to_string();
        let err = verify_chain(&entries).expect_err("tampered ledger must fail");
        assert_eq!(err.seq, entries[1].id);
    }

    #[test]
    fn verify_ledger_on_empty_backend_is_zero_ok() {
        let shared: SharedDb = Arc::new(FakeBackend::new());
        assert_eq!(super::verify_ledger(&shared, None, None).unwrap(), 0);
    }
}
