//! FR-ENT-1 audit-log foundation (backlog H2).
//!
//! Append-only, hash-chained ledger of every MCP tool call and mutating REST
//! call: who (actor), which agent client, what tool, which project, hash of
//! the arguments (never the raw args — NFR-2), result status, timestamp.
//!
//! Hot path: fire-and-forget through a bounded tokio mpsc channel; a single
//! background batcher hashes + inserts up to [`AUDIT_BATCH_MAX`] records every
//! [`AUDIT_FLUSH_INTERVAL_MS`] via one multi-row INSERT, keeping added caller
//! latency well under the 2 ms budget. Overflow drops the record and bumps a
//! counter (tracing::warn) rather than stalling the caller.

use serde::Serialize;
use sha2::{Digest, Sha256};
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex, OnceLock};

/// First link of the chain: the prev_hash of the very first entry.
pub const GENESIS_HASH: &str = "0000000000000000000000000000000000000000000000000000000000000000";

/// Bounded channel capacity. Above this, records are DROPPED (counted), never
/// blocking the hot path.
pub const AUDIT_CHANNEL_CAP: usize = 1024;

/// Max records per multi-row INSERT flush.
pub const AUDIT_BATCH_MAX: usize = 50;

/// Batcher idle flush interval.
pub const AUDIT_FLUSH_INTERVAL_MS: u64 = 100;

/// One audit event. `ts` is stamped by the recorder at enqueue time (the DB
/// column is TIMESTAMPTZ NOT NULL; we always supply the value explicitly).
///
/// Hash coverage note: the chain covers the six caller-known record fields —
/// NOT `ts` — so clock skew between processes can never silently break
/// verifiability of an otherwise-intact ledger.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct AuditRecord {
    pub ts: std::time::SystemTime,
    pub actor: String,
    pub agent_client: String,
    pub tool: String,
    pub project: Option<String>,
    pub args_hash: String,
    pub result_status: String,
}

/// A stored audit row: record + chain links + DB-assigned sequence id.
/// `id == 0` marks a not-yet-inserted entry (the DB BIGSERIAL assigns it).
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct AuditEntry {
    pub id: i64,
    pub ts: std::time::SystemTime,
    pub actor: String,
    pub agent_client: String,
    pub tool: String,
    pub project: Option<String>,
    pub args_hash: String,
    pub result_status: String,
    pub prev_hash: String,
    pub entry_hash: String,
}

impl AuditEntry {
    pub fn from_record(id: i64, rec: AuditRecord, prev_hash: String, entry_hash: String) -> Self {
        Self {
            id,
            ts: rec.ts,
            actor: rec.actor,
            agent_client: rec.agent_client,
            tool: rec.tool,
            project: rec.project,
            args_hash: rec.args_hash,
            result_status: rec.result_status,
            prev_hash,
            entry_hash,
        }
    }

    /// Rebuild the hash-input record view (the six hashed fields).
    fn record_view(&self) -> AuditRecord {
        AuditRecord {
            ts: self.ts,
            actor: self.actor.clone(),
            agent_client: self.agent_client.clone(),
            tool: self.tool.clone(),
            project: self.project.clone(),
            args_hash: self.args_hash.clone(),
            result_status: self.result_status.clone(),
        }
    }

    /// Epoch milliseconds rendering used by the JSONL exporter.
    fn ts_epoch_ms(&self) -> u128 {
        self.ts
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_millis())
            .unwrap_or(0)
    }
}

/// Why a chain verification failed. `seq` is the audit_log sequence id of the
/// offending row (so an operator can jump straight to the tampered line).
#[derive(Debug, Clone)]
pub struct VerifyError {
    pub seq: i64,
    pub reason: String,
}

impl std::fmt::Display for VerifyError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "audit chain broken at seq {}: {}", self.seq, self.reason)
    }
}

impl std::error::Error for VerifyError {}

// ---------------------------------------------------------------------------
// Pure chain math + serialization
// ---------------------------------------------------------------------------

/// Canonical JSON of the six hashed record fields (fixed field order via
/// struct serialization — deterministic across builds and languages).
#[derive(Serialize)]
struct CanonicalFields<'a> {
    actor: &'a str,
    agent_client: &'a str,
    tool: &'a str,
    project: &'a Option<String>,
    args_hash: &'a str,
    result_status: &'a str,
}

pub fn canonical_record_json(rec: &AuditRecord) -> String {
    let fields = CanonicalFields {
        actor: &rec.actor,
        agent_client: &rec.agent_client,
        tool: &rec.tool,
        project: &rec.project,
        args_hash: &rec.args_hash,
        result_status: &rec.result_status,
    };
    serde_json::to_string(&fields).unwrap_or_default()
}

/// SHA-256 hex of `prev_hash || canonical_json(record_fields)`.
pub fn compute_entry_hash(prev_hash: &str, rec: &AuditRecord) -> String {
    let mut hasher = Sha256::new();
    hasher.update(prev_hash.as_bytes());
    hasher.update(canonical_record_json(rec).as_bytes());
    hex_encode(&hasher.finalize())
}

/// SHA-256 hex of the serialized tool arguments. Raw arguments are NEVER
/// persisted (NFR-2) — only this digest.
pub fn hash_args(args: &serde_json::Value) -> String {
    let serialized = serde_json::to_vec(args).unwrap_or_default();
    let mut hasher = Sha256::new();
    hasher.update(&serialized);
    hex_encode(&hasher.finalize())
}

fn hex_encode(bytes: &[u8]) -> String {
    let mut out = String::with_capacity(bytes.len() * 2);
    for b in bytes {
        use std::fmt::Write as _;
        let _ = write!(out, "{b:02x}");
    }
    out
}

/// Link a batch of records onto the chain starting at `start_prev`, returning
/// fully-hashed entries with `id = 0` placeholders (the DB BIGSERIAL assigns
/// real ids on insert).
pub fn chain_records(records: &[AuditRecord], start_prev: &str) -> Vec<AuditEntry> {
    let mut prev = start_prev.to_string();
    records
        .iter()
        .map(|rec| {
            let entry_hash = compute_entry_hash(&prev, rec);
            let entry = AuditEntry::from_record(0, rec.clone(), prev.clone(), entry_hash.clone());
            prev = entry_hash;
            entry
        })
        .collect()
}

/// Recompute every hash sequentially and check linkage. Any break names the
/// sequence id (`VerifyError.seq`) of the offending row.
///
/// Anchor policy: the first row's own `prev_hash` is hashed INTO its
/// entry_hash, so its content is verified regardless of ancestry. When that
/// prev_hash is [`GENESIS_HASH`] the whole ledger is anchored from event #1;
/// a non-genesis head means a `--since`-filtered window was requested and the
/// head acts as a recomputed-but-trusted anchor for linkage onward.
pub fn verify_chain(entries: &[AuditEntry]) -> Result<(), VerifyError> {
    let mut expected_prev: Option<&String> = None;
    for e in entries {
        if let Some(prev) = expected_prev {
            if &e.prev_hash != prev {
                return Err(VerifyError {
                    seq: e.id,
                    reason: format!(
                        "prev_hash {} does not match previous entry_hash {}",
                        e.prev_hash, prev
                    ),
                });
            }
        }
        let recomputed = compute_entry_hash(&e.prev_hash, &e.record_view());
        if recomputed != e.entry_hash {
            return Err(VerifyError {
                seq: e.id,
                reason: format!(
                    "entry_hash mismatch: stored {}, recomputed {}",
                    e.entry_hash, recomputed
                ),
            });
        }
        expected_prev = Some(&e.entry_hash);
    }
    Ok(())
}

/// One JSON object per line: id, ts (epoch millis), the six record fields,
/// prev_hash, entry_hash.
pub fn rows_to_jsonl(entries: &[AuditEntry]) -> String {
    let mut out = String::new();
    for e in entries {
        let line = serde_json::json!({
            "id": e.id,
            "ts": e.ts_epoch_ms(),
            "actor": e.actor,
            "agent_client": e.agent_client,
            "tool": e.tool,
            "project": e.project,
            "args_hash": e.args_hash,
            "result_status": e.result_status,
            "prev_hash": e.prev_hash,
            "entry_hash": e.entry_hash,
        });
        out.push_str(&line.to_string());
        out.push('\n');
    }
    out
}

/// Parse exported JSONL back into entries (tests + SIEM-style consumers).
pub fn jsonl_to_rows(jsonl: &str) -> Result<Vec<AuditEntry>, String> {
    let mut out = Vec::new();
    for (i, line) in jsonl.lines().enumerate() {
        if line.trim().is_empty() {
            continue;
        }
        let v: serde_json::Value =
            serde_json::from_str(line).map_err(|e| format!("line {}: {e}", i + 1))?;
        let ts_ms = v
            .get("ts")
            .and_then(serde_json::Value::as_u64)
            .ok_or_else(|| format!("line {}: missing ts", i + 1))?;
        let str_field = |k: &str| {
            v.get(k)
                .and_then(serde_json::Value::as_str)
                .map(str::to_string)
                .ok_or_else(|| format!("line {}: missing {k}", i + 1))
        };
        let opt_str_field = |k: &str| {
            v.get(k)
                .and_then(serde_json::Value::as_str)
                .map(str::to_string)
        };
        out.push(AuditEntry {
            id: v
                .get("id")
                .and_then(serde_json::Value::as_i64)
                .ok_or_else(|| format!("line {}: missing id", i + 1))?,
            ts: std::time::UNIX_EPOCH + std::time::Duration::from_millis(ts_ms),
            actor: str_field("actor")?,
            agent_client: str_field("agent_client")?,
            tool: str_field("tool")?,
            project: opt_str_field("project"),
            args_hash: str_field("args_hash")?,
            result_status: str_field("result_status")?,
            prev_hash: str_field("prev_hash")?,
            entry_hash: str_field("entry_hash")?,
        });
    }
    Ok(out)
}

// ---------------------------------------------------------------------------
// Recorder: bounded channel + background batching worker
// ---------------------------------------------------------------------------

enum Msg {
    Rec(AuditRecord),
    Flush(tokio::sync::oneshot::Sender<()>),
}

struct RecorderInner {
    tx: tokio::sync::mpsc::Sender<Msg>,
    /// Worker receiver, moved into the spawned task on first use — keeps sync
    /// constructors runtime-free (same pattern as InProcessWriteBus).
    rx: Mutex<Option<tokio::sync::mpsc::Receiver<Msg>>>,
    spawned: OnceLock<()>,
    disabled: AtomicBool,
    dropped: AtomicU64,
    warn_logged: AtomicBool,
    backend: crate::db::backend::SharedDb,
}

/// Fire-and-forget audit recorder. The hot path is one `try_send`; a single
/// background task hashes batches onto the chain and persists them via ONE
/// multi-row INSERT every ~[`AUDIT_FLUSH_INTERVAL_MS`] or
/// [`AUDIT_BATCH_MAX`] records, whichever comes first.
///
/// Failure policy (FR-ENT-1): if the audit_log table is missing or the
/// backend errors, the batcher logs ONCE and disables itself — callers keep
/// working against pre-migration schemas, never crash.
pub struct AuditRecorder {
    inner: Arc<RecorderInner>,
}

impl AuditRecorder {
    /// Production constructor over a shared DB backend.
    pub fn shared(backend: crate::db::backend::SharedDb) -> Arc<Self> {
        Self::with_capacity(backend, AUDIT_CHANNEL_CAP)
    }

    /// Constructor with an explicit channel capacity (tests / tuning).
    pub fn with_capacity(backend: crate::db::backend::SharedDb, capacity: usize) -> Arc<Self> {
        let (tx, rx) = tokio::sync::mpsc::channel::<Msg>(capacity.max(1));
        Arc::new(Self {
            inner: Arc::new(RecorderInner {
                tx,
                rx: Mutex::new(Some(rx)),
                spawned: OnceLock::new(),
                disabled: AtomicBool::new(false),
                dropped: AtomicU64::new(0),
                warn_logged: AtomicBool::new(false),
                backend,
            }),
        })
    }

    /// Enqueue one event. Never blocks the caller: on overflow the record is
    /// DROPPED, counted, and warned about.
    pub fn record(&self, rec: AuditRecord) {
        if self.inner.disabled.load(Ordering::Relaxed) {
            return;
        }
        self.ensure_worker();
        if let Err(err) = self.inner.tx.try_send(Msg::Rec(rec)) {
            let total = self.inner.dropped.fetch_add(1, Ordering::Relaxed) + 1;
            tracing::warn!(
                total_dropped = total,
                error = %err,
                "audit record dropped (channel full); hot path is fire-and-forget by design"
            );
        }
    }

    /// Wait until every record enqueued BEFORE this call has been persisted
    /// (deterministic drain for tests + graceful shutdown). No-op when the
    /// batcher is disabled.
    pub async fn flush(&self) {
        if self.inner.disabled.load(Ordering::Relaxed) {
            return;
        }
        self.ensure_worker();
        let (ack_tx, ack_rx) = tokio::sync::oneshot::channel();
        if self.inner.tx.send(Msg::Flush(ack_tx)).await.is_err() {
            return; // worker gone; nothing more we can do
        }
        let _ = ack_rx.await;
    }

    /// Records dropped due to channel overflow since construction.
    pub fn dropped_count(&self) -> u64 {
        self.inner.dropped.load(Ordering::Relaxed)
    }

    /// False once the batcher permanently disabled itself (audit table
    /// missing, backend unreachable — logged exactly once).
    pub fn is_enabled(&self) -> bool {
        !self.inner.disabled.load(Ordering::Relaxed)
    }

    /// Spawn the worker exactly once (first record/flush). Must run inside a
    /// Tokio runtime — all production callers are async contexts.
    fn ensure_worker(&self) {
        self.inner.spawned.get_or_init(|| {
            let rx = self
                .inner
                .rx
                .lock()
                .unwrap_or_else(|e| e.into_inner())
                .take()
                .expect("audit worker started once");
            let inner = Arc::clone(&self.inner);
            tokio::spawn(async move {
                Self::worker_loop(inner, rx).await;
            });
        });
    }

    async fn worker_loop(inner: Arc<RecorderInner>, mut rx: tokio::sync::mpsc::Receiver<Msg>) {
        // Chain head: None until loaded from the ledger on first flush.
        let mut last_hash: Option<String> = None;
        loop {
            let first = match rx.recv().await {
                Some(m) => m,
                None => break, // channel closed
            };
            let mut ack: Option<tokio::sync::oneshot::Sender<()>> = None;
            let mut batch: Vec<AuditRecord> = Vec::with_capacity(AUDIT_BATCH_MAX);
            match first {
                Msg::Rec(rec) => batch.push(rec),
                Msg::Flush(a) => ack = Some(a),
            }
            // Batch-receive without blocking up to AUDIT_BATCH_MAX.
            while batch.len() < AUDIT_BATCH_MAX {
                match rx.try_recv() {
                    Ok(Msg::Rec(rec)) => batch.push(rec),
                    Ok(Msg::Flush(a)) => {
                        ack = Some(a);
                        break;
                    }
                    Err(_) => break,
                }
            }

            if !batch.is_empty() && inner.disabled.load(Ordering::Relaxed) {
                // Disabled after enqueue: drop silently (already warned).
                batch.clear();
            } else if !batch.is_empty() {
                Self::process_batch(&inner, &mut last_hash, batch);
            }
            if let Some(a) = ack {
                let _ = a.send(());
            }
        }
    }

    /// Hash `batch` onto the chain head and persist in one multi-row INSERT.
    /// Any backend error permanently disables the recorder (logged once).
    fn process_batch(
        inner: &RecorderInner,
        last_hash: &mut Option<String>,
        batch: Vec<AuditRecord>,
    ) {
        if last_hash.is_none() {
            match inner.backend.last_audit_entry_hash() {
                Ok(head) => {
                    *last_hash = Some(head.unwrap_or_else(|| GENESIS_HASH.to_string()));
                }
                Err(e) => {
                    Self::disable(inner, &format!("cannot read audit chain head: {e}"));
                    return;
                }
            }
        }
        let start_prev = last_hash
            .clone()
            .unwrap_or_else(|| GENESIS_HASH.to_string());
        let entries = chain_records(&batch, &start_prev);
        let tail = entries
            .last()
            .map(|e| e.entry_hash.clone())
            .unwrap_or(start_prev);
        if let Err(e) = inner.backend.insert_audit_batch(&entries) {
            Self::disable(
                inner,
                &format!("cannot persist {} audit entries: {e}", entries.len()),
            );
            return;
        }
        *last_hash = Some(tail);
    }

    /// One-shot disable: log exactly once, flip the flag.
    fn disable(inner: &RecorderInner, reason: &str) {
        if !inner.warn_logged.swap(true, Ordering::Relaxed) {
            tracing::warn!(
                "{reason}; audit recording DISABLED for this process                  (run `leankg migrate` to create the audit_log table)"
            );
        }
        inner.disabled.store(true, Ordering::Relaxed);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::{Duration, UNIX_EPOCH};

    /// Deterministic synthetic record #n (no DB involved).
    fn rec(n: usize) -> AuditRecord {
        AuditRecord {
            ts: UNIX_EPOCH + Duration::from_millis(1_700_000_000_000 + n as u64),
            actor: format!("actor-{n}"),
            agent_client: format!("client-{n}"),
            tool: format!("tool_{n}"),
            project: if n % 2 == 0 {
                None
            } else {
                Some(format!("/proj/{n}"))
            },
            args_hash: format!("{n:064x}"),
            result_status: if n % 3 == 0 { "error" } else { "ok" }.to_string(),
        }
    }

    /// Chain N records from genesis and assign ids 1..=N (mirrors what the
    /// recorder + BIGSERIAL produce together).
    fn chained_from_genesis(n: usize) -> Vec<AuditEntry> {
        let records: Vec<AuditRecord> = (0..n).map(rec).collect();
        let mut entries = chain_records(&records, GENESIS_HASH);
        for (i, e) in entries.iter_mut().enumerate() {
            e.id = i as i64 + 1;
        }
        entries
    }

    // -- Hash chain math ---------------------------------------------------

    /// Golden vectors computed INDEPENDENTLY with python3 hashlib over the
    /// same canonicalization (see docs/analysis/hackathon-backlog.md H2).
    /// Pins entry_hash against accidental changes in hash input or field
    /// order that self-consistent recomputation could never catch.
    #[test]
    fn entry_hash_matches_independent_sha256_golden_vectors() {
        let entries = chained_from_genesis(5);
        assert_eq!(
            entries[0].entry_hash,
            "adfaff553983aeb15f0f8c9057f5f13d3a9576e5cf508fdcf7714b808c802cca"
        );
        assert_eq!(
            entries[1].entry_hash,
            "b179ff46f7c4268584ab7255432e44a3d66ab3c40976a1be8adb8fd564a9c692"
        );
        assert_eq!(
            entries[4].entry_hash,
            "2028692c188108aa7b73fe5d7ac34ef26373263ec257426cffbc9e76fed3a70e"
        );
    }

    #[test]
    fn chain_of_five_synthetic_records_verifies() {
        let entries = chained_from_genesis(5);
        assert_eq!(entries.len(), 5);
        verify_chain(&entries).expect("a freshly built 5-record chain must verify");
    }

    #[test]
    fn genesis_row_carries_the_all_zero_prev_hash() {
        let entries = chained_from_genesis(5);
        assert_eq!(entries[0].prev_hash, GENESIS_HASH);
        assert_ne!(entries[1].prev_hash, GENESIS_HASH);
        assert_eq!(entries[0].entry_hash.len(), 64);
    }

    #[test]
    fn every_entry_hash_is_distinct_and_links_forward() {
        let entries = chained_from_genesis(5);
        for i in 1..entries.len() {
            assert_eq!(entries[i].prev_hash, entries[i - 1].entry_hash);
            assert_ne!(entries[i].entry_hash, entries[i - 1].entry_hash);
        }
    }

    #[test]
    fn empty_ledger_verifies() {
        verify_chain(&[]).expect("an empty ledger is trivially intact");
    }

    #[test]
    fn canonical_json_is_deterministic_and_field_ordered() {
        let a = canonical_record_json(&rec(1));
        let b = canonical_record_json(&rec(1));
        assert_eq!(a, b, "same record must hash identically");
        assert!(a.contains("\"actor\":\"actor-1\""));
        // Fixed field order: actor first, result_status last.
        let actor_pos = a.find("\"actor\"").unwrap();
        let status_pos = a.find("\"result_status\"").unwrap();
        assert!(actor_pos < status_pos);
    }

    #[test]
    fn hash_args_digests_serialized_value() {
        let h1 = hash_args(&serde_json::json!({"query": "fn main"}));
        let h2 = hash_args(&serde_json::json!({"query": "fn main"}));
        let h3 = hash_args(&serde_json::json!({"query": "fn other"}));
        assert_eq!(h1, h2);
        assert_ne!(h1, h3);
        assert_eq!(h1.len(), 64);
    }

    // -- Tamper evidence ----------------------------------------------------

    #[test]
    fn mutated_field_in_exported_jsonl_fails_verify_naming_seq() {
        let entries = chained_from_genesis(5);
        let jsonl = rows_to_jsonl(&entries);
        let parsed = jsonl_to_rows(&jsonl).expect("export must round-trip");
        verify_chain(&parsed).expect("untampered export verifies");

        // Mutate one field on line 3 (seq id 3).
        let mut tampered = parsed;
        tampered[2].actor = "mallory".to_string();
        let err = verify_chain(&tampered).expect_err("field tampering must be detected");
        assert_eq!(err.seq, 3, "error must name the tampered sequence id");
    }

    #[test]
    fn tampered_entry_hash_in_export_fails_naming_seq() {
        let entries = chained_from_genesis(5);
        let mut rows = jsonl_to_rows(&rows_to_jsonl(&entries)).unwrap();
        rows[4].entry_hash = "f".repeat(64);
        let err = verify_chain(&rows).expect_err("forged entry_hash must be caught");
        assert_eq!(err.seq, 5);
    }

    #[test]
    fn broken_linkage_between_rows_fails_naming_seq() {
        let entries = chained_from_genesis(5);
        let mut rows = entries;
        // Rewire row 4 to skip row 3's hash (simulates a deleted row).
        rows[3].prev_hash = rows[1].entry_hash.clone();
        let err = verify_chain(&rows).expect_err("link break must be detected");
        assert_eq!(err.seq, 4);
    }

    #[test]
    fn mid_ledger_window_anchors_on_first_row_and_still_verifies() {
        // `--since` exports start mid-chain; the window head acts as anchor.
        let full = chained_from_genesis(6);
        let window = &full[2..];
        verify_chain(window).expect("anchored window must verify");
    }

    #[test]
    fn jsonl_round_trip_preserves_all_fields() {
        let entries = chained_from_genesis(5);
        let back = jsonl_to_rows(&rows_to_jsonl(&entries)).unwrap();
        assert_eq!(back.len(), 5);
        for (orig, parsed) in entries.iter().zip(back.iter()) {
            assert_eq!(orig.id, parsed.id);
            assert_eq!(orig.actor, parsed.actor);
            assert_eq!(orig.agent_client, parsed.agent_client);
            assert_eq!(orig.tool, parsed.tool);
            assert_eq!(orig.project, parsed.project);
            assert_eq!(orig.args_hash, parsed.args_hash);
            assert_eq!(orig.result_status, parsed.result_status);
            assert_eq!(orig.prev_hash, parsed.prev_hash);
            assert_eq!(orig.entry_hash, parsed.entry_hash);
        }
    }

    #[test]
    fn jsonl_lines_carry_required_fields() {
        let entries = chained_from_genesis(2);
        let jsonl = rows_to_jsonl(&entries);
        let first = jsonl.lines().next().unwrap();
        let v: serde_json::Value = serde_json::from_str(first).unwrap();
        for key in [
            "id",
            "ts",
            "actor",
            "agent_client",
            "tool",
            "project",
            "args_hash",
            "result_status",
            "prev_hash",
            "entry_hash",
        ] {
            assert!(v.get(key).is_some(), "JSONL line must carry `{key}`");
        }
        assert_eq!(v["id"], 1);
    }

    // -- Recorder hot-path overhead -----------------------------------------
    //
    // FR-ENT-1 AC: < 2 ms added latency per call. The hot path is a
    // fire-and-forget bounded-channel send; this bench-style assertion keeps
    // it honest (500 sequential calls, average per-call cost).

    #[tokio::test(flavor = "multi_thread")]
    async fn record_send_overhead_well_under_2ms_per_call() {
        use crate::db::fake::FakeBackend;
        use std::sync::Arc;

        let backend: Arc<dyn crate::db::backend::DbBackend> = Arc::new(FakeBackend::new());
        let recorder = super::AuditRecorder::shared(backend);

        let started = std::time::Instant::now();
        for n in 0..500 {
            recorder.record(rec(n));
        }
        let elapsed = started.elapsed();
        recorder.flush().await;

        let avg_us = elapsed.as_micros() / 500;
        assert!(
            elapsed.as_millis() < 1000,
            "500 fire-and-forget sends took {elapsed:?}; avg {avg_us}us/call"
        );
    }

    /// Overflow policy: capacity-1 channel on a current-thread runtime — the
    /// batcher task cannot run until we hit an await, so sends 2..100 must be
    /// dropped and COUNTED, never block the caller.
    #[tokio::test]
    async fn recorder_drops_and_counts_when_channel_is_saturated() {
        use crate::db::fake::FakeBackend;

        let backend: Arc<dyn crate::db::backend::DbBackend> = Arc::new(FakeBackend::new());
        let recorder = super::AuditRecorder::with_capacity(backend, 1);
        for n in 0..100 {
            recorder.record(rec(n));
        }
        assert_eq!(
            recorder.dropped_count(),
            99,
            "capacity-1 channel holds one record; the other 99 must drop+count"
        );
        recorder.flush().await;
    }
}
