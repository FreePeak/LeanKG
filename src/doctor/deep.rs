//! H9 — `leankg doctor --deep`: deployment self-diagnosis suite.
//!
//! Eight checks interrogate the deployment end-to-end: PG
//! reachability/latency, migration state, index freshness, embedding
//! coverage, pool env sanity, orphaned relationships, duplicate
//! qualified_names, and `.leankg` directory health. Each check yields a
//! severity-tagged finding (PASS/WARN/FAIL) with an actionable remediation
//! hint. Exit codes are CI-friendly: 0 all-pass, 1 any warn, 2 any fail.
//!
//! Checks are a pluggable [`CheckRegistry`]; every DB touch goes through the
//! injectable [`DeepProbes`] trait so unit tests stub probe results without a
//! live Postgres (mirroring how the rest of the crate tests against
//! `FakeBackend`-style seams). Production uses [`BackendProbes`] over a
//! `SharedDb`.

use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, HashSet};
use std::fmt;
use std::path::{Path, PathBuf};
use std::time::Instant;

use crate::db::backend::SharedDb;

/// Round-trip latency above this many ms is a WARN.
pub const LATENCY_WARN_MS: u64 = 500;
/// Round-trip latency above this many ms is a FAIL.
pub const LATENCY_FAIL_MS: u64 = 5_000;

/// Env-tunable WARN threshold (`LEANKG_DOCTOR_LATENCY_WARN_MS`) so doctor
/// runs against remote / managed Postgres don't flag routine WAN RTT as
/// slow. Only raises (values below [`LATENCY_WARN_MS`] are ignored) and is
/// clamped just under [`LATENCY_FAIL_MS`] so the Fail tier stays reachable.
pub fn latency_warn_ms() -> u64 {
    std::env::var("LEANKG_DOCTOR_LATENCY_WARN_MS")
        .ok()
        .and_then(|v| v.parse::<u64>().ok())
        .filter(|v| *v >= LATENCY_WARN_MS)
        .map(|v| v.min(LATENCY_FAIL_MS - 1))
        .unwrap_or(LATENCY_WARN_MS)
}
/// Edge sample cap for the orphan scan.
pub const ORPHAN_SAMPLE_LIMIT: usize = 1_000;
/// Duplicate-qualified_name offenders listed in findings.
pub const DUPLICATE_TOP_LIMIT: usize = 10;
/// Valid `LEANKG_PG_POOL_SIZE` upper bound.
pub const POOL_SIZE_MAX: u64 = 1_024;
/// Valid `LEANKG_PG_POOL_WAIT_MS` upper bound.
pub const POOL_WAIT_MAX_MS: i64 = 600_000;

// ---------------------------------------------------------------------------
// Report model
// ---------------------------------------------------------------------------

/// Severity of a single deep-check finding.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum CheckStatus {
    Pass,
    Warn,
    Fail,
}

impl fmt::Display for CheckStatus {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let s = match self {
            CheckStatus::Pass => "PASS",
            CheckStatus::Warn => "WARN",
            CheckStatus::Fail => "FAIL",
        };
        f.write_str(s)
    }
}

impl CheckStatus {
    /// Severity fold: keeps the worse of the two statuses.
    fn worst(self, other: CheckStatus) -> CheckStatus {
        match (self, other) {
            (CheckStatus::Fail, _) | (_, CheckStatus::Fail) => CheckStatus::Fail,
            (CheckStatus::Warn, _) | (_, CheckStatus::Warn) => CheckStatus::Warn,
            _ => CheckStatus::Pass,
        }
    }
}

/// One check's verdict: stable machine name + human detail + remediation.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Finding {
    pub check: String,
    pub status: CheckStatus,
    pub detail: String,
    pub hint: String,
}

impl Finding {
    pub fn new(
        check: &str,
        status: CheckStatus,
        detail: impl Into<String>,
        hint: impl Into<String>,
    ) -> Finding {
        Finding {
            check: check.to_string(),
            status,
            detail: detail.into(),
            hint: hint.into(),
        }
    }
}

/// PASS/WARN/FAIL tally across a report.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Counts {
    pub pass: u32,
    pub warn: u32,
    pub fail: u32,
}

/// Full deep-doctor result: one [`Finding`] per registered check, in
/// registry order.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct DoctorReport {
    pub findings: Vec<Finding>,
}

impl DoctorReport {
    pub fn counts(&self) -> Counts {
        let mut c = Counts::default();
        for f in &self.findings {
            match f.status {
                CheckStatus::Pass => c.pass += 1,
                CheckStatus::Warn => c.warn += 1,
                CheckStatus::Fail => c.fail += 1,
            }
        }
        c
    }

    /// CI exit code: 0 all-pass, 1 any warn (no fails), 2 any fail.
    pub fn exit_code(&self) -> i32 {
        if self.findings.iter().any(|f| f.status == CheckStatus::Fail) {
            2
        } else if self.findings.iter().any(|f| f.status == CheckStatus::Warn) {
            1
        } else {
            0
        }
    }

    /// Machine-readable JSON including the computed summary block.
    pub fn render_json(&self) -> String {
        #[derive(Serialize)]
        struct Envelope<'a> {
            findings: &'a [Finding],
            summary: Counts,
        }
        let env = Envelope {
            findings: &self.findings,
            summary: self.counts(),
        };
        serde_json::to_string_pretty(&env).unwrap_or_else(|_| "{}".to_string())
    }

    /// Aligned human table: `check | status | detail | hint`, then a
    /// summary row. Every row is padded to identical total width so
    /// columns line up in any terminal font.
    pub fn render_table(&self) -> String {
        let header = ["check", "status", "detail", "hint"];
        let mut rows: Vec<[String; 4]> = self
            .findings
            .iter()
            .map(|f| {
                [
                    f.check.clone(),
                    f.status.to_string(),
                    f.detail.clone(),
                    f.hint.clone(),
                ]
            })
            .collect();
        rows.push([
            String::new(),
            String::new(),
            format!(
                "{} pass, {} warn, {} fail — exit {}",
                self.counts().pass,
                self.counts().warn,
                self.counts().fail,
                self.exit_code()
            ),
            String::new(),
        ]);

        let mut widths = [4usize; 4];
        for (i, h) in header.iter().enumerate() {
            widths[i] = widths[i].max(h.chars().count());
        }
        for r in &rows {
            for (i, cell) in r.iter().enumerate() {
                widths[i] = widths[i].max(cell.chars().count());
            }
        }

        let mut out = String::new();
        let emit = |out: &mut String, cells: &[String; 4]| {
            out.push_str(&format!(
                "{:<w0$} | {:<w1$} | {:<w2$} | {:<w3$}",
                cells[0],
                cells[1],
                cells[2],
                cells[3],
                w0 = widths[0],
                w1 = widths[1],
                w2 = widths[2],
                w3 = widths[3]
            ));
            out.push('\n');
        };

        let header_cells: [String; 4] = header.map(str::to_string);
        emit(&mut out, &header_cells);
        let sep: Vec<String> = widths.iter().map(|w| "-".repeat(*w)).collect();
        out.push_str(&sep.join("-+-"));
        out.push('\n');
        for r in &rows {
            emit(&mut out, r);
        }
        out
    }
}

// ---------------------------------------------------------------------------
// Injectable probes
// ---------------------------------------------------------------------------

/// Everything the deep checks need from the deployment, as pure results.
///
/// Production wires [`BackendProbes`] (graph-script reads through a `SharedDb`);
/// unit tests inject canned values so every failure mode is provable
/// without Postgres.
pub trait DeepProbes: Send + Sync {
    /// One trivial round trip to PG, timed in milliseconds.
    fn ping_ms(&self) -> Result<u64, String>;
    /// Migration versions present in the applied ledger.
    fn applied_migrations(&self) -> Result<Vec<String>, String>;
    /// Distinct file paths currently indexed (`code_elements.file_path`).
    fn indexed_files(&self) -> Result<Vec<String>, String>;
    /// All qualified_names in `code_elements` (duplicates preserved —
    /// duplicate detection depends on them).
    fn qualified_names(&self) -> Result<Vec<String>, String>;
    /// Sampled relationship edges `(source, target, rel_type)`.
    fn relationship_edges(&self) -> Result<Vec<(String, String, String)>, String>;
    /// Qualified_names with embedding state, or `None` when the embedding
    /// tables do not exist (embeddings never built / feature off).
    fn embedded_names(&self) -> Result<Option<Vec<String>>, String>;
}

/// Production probes: graph-script reads through a shared backend handle.
pub struct BackendProbes {
    pub backend: SharedDb,
}

impl BackendProbes {
    pub fn new(backend: SharedDb) -> BackendProbes {
        BackendProbes { backend }
    }

    fn strings(&self, query: &str) -> Result<Vec<String>, String> {
        let rows = self
            .backend
            .run_script(query, BTreeMap::new())
            .map_err(|e| e.to_string())?;
        Ok(rows
            .rows
            .iter()
            .filter_map(|r| r.first().and_then(|v| v.get_str().map(str::to_string)))
            .collect())
    }
}

impl DeepProbes for BackendProbes {
    fn ping_ms(&self) -> Result<u64, String> {
        const PROBE: &str = "?[id] := *migrations[id] :limit 1";
        // Warm-up round trip: excludes lazy connect + TLS handshake from
        // the timed measurement so remote deployments report true
        // steady-state query latency, not one-time session setup.
        self.backend
            .run_script(PROBE, BTreeMap::new())
            .map_err(|e| e.to_string())?;
        let start = Instant::now();
        self.backend
            .run_script(PROBE, BTreeMap::new())
            .map_err(|e| e.to_string())?;
        Ok(start.elapsed().as_millis() as u64)
    }

    fn applied_migrations(&self) -> Result<Vec<String>, String> {
        self.strings("?[id] := *migrations[id]")
    }

    fn indexed_files(&self) -> Result<Vec<String>, String> {
        self.strings("?[file_path] := *code_elements[file_path]")
    }

    fn qualified_names(&self) -> Result<Vec<String>, String> {
        self.strings("?[qualified_name] := *code_elements[qualified_name]")
    }

    fn relationship_edges(&self) -> Result<Vec<(String, String, String)>, String> {
        let rows = self
            .backend
            .run_script(
                &format!(
                    "?[source_qualified, target_qualified, rel_type] := \
                     *relationships[source_qualified, target_qualified, rel_type] :limit {ORPHAN_SAMPLE_LIMIT}"
                ),
                BTreeMap::new(),
            )
            .map_err(|e| e.to_string())?;
        Ok(rows
            .rows
            .iter()
            .filter_map(|r| {
                let s = r.first()?.get_str()?.to_string();
                let t = r.get(1)?.get_str()?.to_string();
                let ty = r.get(2)?.get_str()?.to_string();
                Some((s, t, ty))
            })
            .collect())
    }

    fn embedded_names(&self) -> Result<Option<Vec<String>>, String> {
        match self.strings("?[qualified_name] := *embedding_state[qualified_name]") {
            Ok(names) => Ok(Some(names)),
            Err(e) if table_absent(&e) => Ok(None),
            Err(e) => Err(e),
        }
    }
}

/// Heuristic on translated-SQLError text distinguishing "the embedding
/// tables were never created" from a genuine probe failure.
fn table_absent(err: &str) -> bool {
    let lower = err.to_lowercase();
    lower.contains("does not exist")
        || lower.contains("no such table")
        || lower.contains("unknown relation")
        || lower.contains("not found in schema")
}

// ---------------------------------------------------------------------------
// Context + registry
// ---------------------------------------------------------------------------

/// Raw pool-related env snapshot, captured once at dispatch so checks stay
/// deterministic and tests avoid mutating process-global env.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct PoolEnvSnapshot {
    pub pool_size: Option<String>,
    pub pool_wait_ms: Option<String>,
}

impl PoolEnvSnapshot {
    pub fn from_env() -> PoolEnvSnapshot {
        PoolEnvSnapshot {
            pool_size: std::env::var("LEANKG_PG_POOL_SIZE").ok(),
            pool_wait_ms: std::env::var("LEANKG_PG_POOL_WAIT_MS").ok(),
        }
    }
}

/// Inputs handed to every [`DoctorCheck`].
pub struct DeepContext<'a> {
    pub probes: &'a dyn DeepProbes,
    /// Project root walked for freshness (absolute or relative, as given).
    pub project_root: &'a Path,
    /// The project's `.leankg` directory.
    pub leankg_dir: &'a Path,
    pub pool_env: PoolEnvSnapshot,
}

impl<'a> DeepContext<'a> {
    pub fn new(
        probes: &'a dyn DeepProbes,
        project_root: &'a Path,
        leankg_dir: &'a Path,
        pool_env: PoolEnvSnapshot,
    ) -> DeepContext<'a> {
        DeepContext {
            probes,
            project_root,
            leankg_dir,
            pool_env,
        }
    }
}

/// A pluggable deep-diagnosis check.
pub trait DoctorCheck: Send + Sync {
    /// Stable machine name (kebab-case), used as the table/JSON key.
    fn name(&self) -> &'static str;
    fn run(&self, ctx: &DeepContext) -> Finding;
}

/// Ordered set of checks. Order is presentation order and stable.
pub struct CheckRegistry {
    checks: Vec<Box<dyn DoctorCheck>>,
}

impl Default for CheckRegistry {
    fn default() -> Self {
        Self::with_defaults()
    }
}

impl CheckRegistry {
    /// The eight shipped H9 checks, in output order.
    pub fn with_defaults() -> CheckRegistry {
        CheckRegistry {
            checks: vec![
                Box::new(PgLatencyCheck),
                Box::new(MigrationsCheck),
                Box::new(IndexFreshnessCheck),
                Box::new(EmbeddingCoverageCheck),
                Box::new(PoolEnvCheck),
                Box::new(OrphanEdgesCheck),
                Box::new(DuplicateNamesCheck),
                Box::new(LeankgDirCheck),
            ],
        }
    }

    /// Append an extra check (builder style).
    pub fn with_check(mut self, check: Box<dyn DoctorCheck>) -> CheckRegistry {
        self.checks.push(check);
        self
    }

    pub fn names(&self) -> Vec<&'static str> {
        self.checks.iter().map(|c| c.name()).collect()
    }

    pub fn run_all(&self, ctx: &DeepContext) -> DoctorReport {
        DoctorReport {
            findings: self.checks.iter().map(|c| c.run(ctx)).collect(),
        }
    }
}

// ---------------------------------------------------------------------------
// The eight default checks
// ---------------------------------------------------------------------------

/// Check 1 — PG connectivity + round-trip latency.
struct PgLatencyCheck;

impl DoctorCheck for PgLatencyCheck {
    fn name(&self) -> &'static str {
        "pg-latency"
    }

    fn run(&self, ctx: &DeepContext) -> Finding {
        const CHECK: &str = "pg-latency";
        let warn_ms = latency_warn_ms();
        match ctx.probes.ping_ms() {
            Ok(ms) if ms <= warn_ms => {
                Finding::new(CHECK, CheckStatus::Pass, format!("{ms} ms round-trip"), "")
            }
            Ok(ms) if ms <= LATENCY_FAIL_MS => Finding::new(
                CHECK,
                CheckStatus::Warn,
                format!("round-trip {ms} ms (>{warn_ms} ms)"),
                "Postgres answers but slowly — check network distance to the DB host \
                 (cross-region links routinely exceed this); prefer a co-located replica.",
            ),
            Ok(ms) => Finding::new(
                CHECK,
                CheckStatus::Fail,
                format!("round-trip {ms} ms (>{LATENCY_FAIL_MS} ms)"),
                "Unusable latency — every query pays it. Move the workload next to \
                 the database or fix the degraded network path.",
            ),
            Err(e) => Finding::new(
                CHECK,
                CheckStatus::Fail,
                format!("unreachable: {e}"),
                "Verify LEANKG_PG_URL points at a running Postgres and that network \
                /firewall allows the connection (`psql \"$LEANKG_PG_URL\" -c 'select 1'`).",
            ),
        }
    }
}

/// Check 2 — applied migrations vs the embedded MIGRATIONS list.
struct MigrationsCheck;

impl DoctorCheck for MigrationsCheck {
    fn name(&self) -> &'static str {
        "migrations"
    }

    fn run(&self, ctx: &DeepContext) -> Finding {
        const CHECK: &str = "migrations";
        let applied = match ctx.probes.applied_migrations() {
            Ok(v) => v,
            Err(e) => {
                return Finding::new(
                    CHECK,
                    CheckStatus::Fail,
                    format!("cannot read migration ledger: {e}"),
                    "The migrations table must exist after `run_migrations`; verify PG \
                     connectivity and that the project schema was initialized.",
                )
            }
        };
        let applied_set: HashSet<&str> = applied.iter().map(String::as_str).collect();
        let embedded = embedded_migration_ids();
        let pending: Vec<&str> = embedded
            .iter()
            .filter(|id| !applied_set.contains(**id))
            .copied()
            .collect();
        let unknown: Vec<String> = applied
            .iter()
            .filter(|id| !embedded.contains(&id.as_str()))
            .cloned()
            .collect();
        if !pending.is_empty() {
            Finding::new(
                CHECK,
                CheckStatus::Fail,
                format!("{} pending: {}", pending.len(), pending.join(", ")),
                "Run any writer command (e.g. `leankg index`) — migrations apply \
                 automatically when a writer opens the schema.",
            )
        } else if !unknown.is_empty() {
            Finding::new(
                CHECK,
                CheckStatus::Warn,
                format!("schema ahead of binary: {}", unknown.join(", ")),
                "These migrations were applied by a newer leankg; upgrade this binary \
                 to match the database schema.",
            )
        } else {
            Finding::new(
                CHECK,
                CheckStatus::Pass,
                format!("all {} embedded migrations applied", applied.len()),
                "",
            )
        }
    }
}

/// Embedded migration ids, straight from the migrations module.
fn embedded_migration_ids() -> Vec<&'static str> {
    crate::db::pg::migrations::MIGRATIONS
        .iter()
        .map(|(id, _)| *id)
        .collect()
}

/// Check 3 — indexed files vs on-disk supported files.
struct IndexFreshnessCheck;

impl DoctorCheck for IndexFreshnessCheck {
    fn name(&self) -> &'static str {
        "index-freshness"
    }

    fn run(&self, ctx: &DeepContext) -> Finding {
        const CHECK: &str = "index-freshness";
        let indexed = match ctx.probes.indexed_files() {
            Ok(v) => v,
            Err(e) => {
                return Finding::new(
                    CHECK,
                    CheckStatus::Fail,
                    format!("cannot read indexed file list: {e}"),
                    "code_elements should be readable after an index; verify PG \
                     connectivity and schema.",
                )
            }
        };
        let disk = match crate::indexer::find_files_sync(&ctx.project_root.to_string_lossy()) {
            Ok(v) => v,
            Err(e) => {
                return Finding::new(
                    CHECK,
                    CheckStatus::Fail,
                    format!("cannot walk {}: {e}", ctx.project_root.display()),
                    "Fix filesystem permissions on the project root, or pass \
                     --project with the correct path.",
                )
            }
        };

        // Normalize BOTH sides to project-root-relative spellings so the
        // comparison is independent of (a) the doctor process CWD, (b) how
        // the indexer spelled paths (`./src/x.rs` vs `src/x.rs` vs absolute),
        // and (c) symlinked ancestors (macOS /tmp → /private/tmp): the root
        // is canonicalized once and every path resolved against it.
        let root_canon = crate::db::backend::canonical_project_root(ctx.project_root);
        let resolve = |p: &str| -> std::path::PathBuf {
            let pb = Path::new(p);
            if pb.is_absolute() {
                pb.to_path_buf()
            } else {
                root_canon.join(pb)
            }
        };
        let to_rel = |p: &str| -> String {
            let resolved = resolve(p);
            // Prefer stripping the canonical root; fall back to the raw
            // project-root spelling (macOS /tmp → /private/tmp makes these
            // differ), then to canonicalizing the file itself.
            if let Ok(r) = resolved.strip_prefix(&root_canon) {
                return r.to_string_lossy().into_owned();
            }
            if let Ok(r) = resolved.strip_prefix(ctx.project_root) {
                return r.to_string_lossy().into_owned();
            }
            if let Ok(canon_file) = std::fs::canonicalize(&resolved) {
                if let Ok(r) = canon_file.strip_prefix(&root_canon) {
                    return r.to_string_lossy().into_owned();
                }
            }
            p.to_string()
        };
        let disk_rel: Vec<String> = disk.iter().map(|d| to_rel(d)).collect();
        let indexed_rel: Vec<String> = indexed.iter().map(|i| to_rel(i)).collect();
        // Synthetic elements (dynamic ontology concepts, agent diaries, …)
        // live in code_elements with URI-style file_paths (`ontology://…`)
        // and have no filesystem backing — they are never "stale".
        let is_fs_path = |p: &str| !p.contains("://");
        let fs_indexed: Vec<(&String, &String)> = indexed
            .iter()
            .zip(indexed_rel.iter())
            .filter(|(raw, _)| is_fs_path(raw))
            .collect();
        let covered_by =
            |needle: &str, haystack: &[String]| -> bool { haystack.iter().any(|h| h == needle) };
        let stale: Vec<&str> = fs_indexed
            .iter()
            .filter(|(raw, rel)| {
                // Synthetic nodes legitimately point at directories
                // (Project/folder elements); "stale" means the filesystem
                // object is gone entirely, not merely non-regular.
                !resolve(raw).exists() && !covered_by(rel, &disk_rel)
            })
            .map(|(_, rel)| rel.as_str())
            .collect();
        let missing_count = disk_rel
            .iter()
            .filter(|d| !covered_by(d, &indexed_rel))
            .count();

        if indexed.is_empty() && !disk.is_empty() {
            return Finding::new(
                CHECK,
                CheckStatus::Fail,
                format!(
                    "index is empty; {} supported file(s) on disk are unindexed",
                    disk.len()
                ),
                "Run `leankg init` then `leankg index <path>` to build the index.",
            );
        }

        let stale_pct = if fs_indexed.is_empty() {
            0u64
        } else {
            (stale.len() as u64 * 100) / fs_indexed.len() as u64
        };

        if stale_pct > 50 {
            let samples: Vec<&str> = stale.iter().take(3).copied().collect();
            Finding::new(
                CHECK,
                CheckStatus::Fail,
                format!(
                    "{}/{} indexed paths ({stale_pct}%) no longer exist on disk; e.g. {}",
                    stale.len(),
                    fs_indexed.len(),
                    samples.join(", ")
                ),
                "The index points at a moved/rotated tree — re-index the current \
                 project root (`leankg index <path>`).",
            )
        } else if stale.is_empty() && missing_count == 0 {
            Finding::new(
                CHECK,
                CheckStatus::Pass,
                format!(
                    "{} indexed file(s), all present on disk ({} synthetic URI entries skipped)",
                    fs_indexed.len(),
                    indexed.len() - fs_indexed.len()
                ),
                "",
            )
        } else {
            Finding::new(
                CHECK,
                CheckStatus::Warn,
                format!(
                    "{} missing file(s) not indexed, {} stale ({stale_pct}%)",
                    missing_count,
                    stale.len()
                ),
                "Run `leankg index` (or watch mode) to refresh; a large delta usually \
                 means the index was built from a different root or env.",
            )
        }
    }
}

/// Check 4 — embedding_state coverage over code_elements.
struct EmbeddingCoverageCheck;

impl DoctorCheck for EmbeddingCoverageCheck {
    fn name(&self) -> &'static str {
        "embedding-coverage"
    }

    fn run(&self, ctx: &DeepContext) -> Finding {
        const CHECK: &str = "embedding-coverage";
        let qns = match ctx.probes.qualified_names() {
            Ok(v) => v,
            Err(e) => {
                return Finding::new(
                    CHECK,
                    CheckStatus::Fail,
                    format!("cannot read code_elements: {e}"),
                    "Coverage is measured against indexed elements; verify PG \
                     connectivity and that an index exists.",
                )
            }
        };
        let unique: HashSet<&str> = qns.iter().map(String::as_str).collect();
        match ctx.probes.embedded_names() {
            Err(e) => Finding::new(
                CHECK,
                CheckStatus::Fail,
                format!("cannot read embedding_state: {e}"),
                "Embedding tables are created by migrations; check PG permissions \
                 and schema state (`leankg doctor --deep` migrations row).",
            ),
            Ok(None) => Finding::new(
                CHECK,
                CheckStatus::Pass,
                "embedding tables absent — embeddings never built",
                "",
            ),
            Ok(Some(_embedded)) if unique.is_empty() => Finding::new(
                CHECK,
                CheckStatus::Pass,
                "no elements indexed yet; nothing to embed".to_string(),
                "",
            ),
            Ok(Some(embedded)) if embedded.is_empty() => Finding::new(
                CHECK,
                CheckStatus::Pass,
                format!("0/{} embedded (embeddings never built)", unique.len()),
                "",
            ),
            Ok(Some(embedded)) => {
                let embedded_set: HashSet<&str> = embedded.iter().map(String::as_str).collect();
                let covered = unique.intersection(&embedded_set).count();
                let total = unique.len();
                let uncovered_pct = 100 - (covered as u64 * 100 / total as u64);
                if uncovered_pct == 0 {
                    Finding::new(
                        CHECK,
                        CheckStatus::Pass,
                        format!("{covered}/{total} elements embedded"),
                        "",
                    )
                } else {
                    Finding::new(
                        CHECK,
                        CheckStatus::Warn,
                        format!("{uncovered_pct}% uncovered ({covered}/{total} embedded)"),
                        "Run `leankg embed` to build vectors for new/changed elements \
                         so semantic search stays complete.",
                    )
                }
            }
        }
    }
}

/// Check 5 — LEANKG_PG_POOL_SIZE / LEANKG_PG_POOL_WAIT_MS sanity.
struct PoolEnvCheck;

impl DoctorCheck for PoolEnvCheck {
    fn name(&self) -> &'static str {
        "pool-env"
    }

    fn run(&self, ctx: &DeepContext) -> Finding {
        const CHECK: &str = "pool-env";
        let mut status = CheckStatus::Pass;
        let mut details: Vec<String> = Vec::new();
        let mut hints: Vec<String> = Vec::new();

        match ctx.pool_env.pool_size.as_deref() {
            None => details.push("pool size: default 5".to_string()),
            Some(raw) => match raw.trim().parse::<u64>() {
                Ok(v) if (1..=POOL_SIZE_MAX).contains(&v) => details.push(format!("pool size {v}")),
                Ok(0) | Err(_) => {
                    status = status.worst(CheckStatus::Fail);
                    details.push(format!("invalid LEANKG_PG_POOL_SIZE={raw:?}"));
                    hints.push(format!(
                        "Set LEANKG_PG_POOL_SIZE to an integer between 1 and {POOL_SIZE_MAX}."
                    ));
                }
                Ok(v) => {
                    status = status.worst(CheckStatus::Warn);
                    details.push(format!("pool size {v} exceeds {POOL_SIZE_MAX}"));
                    hints.push(format!(
                        "Keep LEANKG_PG_POOL_SIZE within 1..={POOL_SIZE_MAX}; oversubscribing \
                         connections can exhaust Postgres max_connections."
                    ));
                }
            },
        }

        match ctx.pool_env.pool_wait_ms.as_deref() {
            None => details.push("pool wait: default 10000 ms".to_string()),
            Some(raw) => match raw.trim().parse::<i64>() {
                Ok(v) if (1..=POOL_WAIT_MAX_MS).contains(&v) => {
                    details.push(format!("pool wait {v} ms"))
                }
                Ok(v) if v > POOL_WAIT_MAX_MS => {
                    status = status.worst(CheckStatus::Warn);
                    details.push(format!("pool wait {v} ms exceeds {POOL_WAIT_MAX_MS} ms"));
                    hints.push(format!(
                        "Waits beyond {POOL_WAIT_MAX_MS} ms hide pool starvation instead of \
                         failing fast; lower it or raise the pool size."
                    ));
                }
                _ => {
                    status = status.worst(CheckStatus::Fail);
                    details.push(format!("invalid LEANKG_PG_POOL_WAIT_MS={raw:?}"));
                    hints.push(format!(
                        "Set LEANKG_PG_POOL_WAIT_MS to an integer between 1 and \
                         {POOL_WAIT_MAX_MS} (milliseconds)."
                    ));
                }
            },
        }

        let hint_text = if hints.is_empty() {
            String::new()
        } else {
            hints.join(" ")
        };
        Finding::new(CHECK, status, details.join("; "), &hint_text)
    }
}

/// Check 6 — relationships whose endpoints no longer exist.
struct OrphanEdgesCheck;

impl DoctorCheck for OrphanEdgesCheck {
    fn name(&self) -> &'static str {
        "orphaned-relationships"
    }

    fn run(&self, ctx: &DeepContext) -> Finding {
        const CHECK: &str = "orphaned-relationships";
        let edges = match ctx.probes.relationship_edges() {
            Ok(v) => v,
            Err(e) => {
                return Finding::new(
                    CHECK,
                    CheckStatus::Fail,
                    format!("cannot sample relationships: {e}"),
                    "Verify PG connectivity; the relationships table is created by \
                     migrations.",
                )
            }
        };
        let qns: HashSet<String> = match ctx.probes.qualified_names() {
            Ok(v) => v.into_iter().collect(),
            Err(e) => {
                return Finding::new(
                    CHECK,
                    CheckStatus::Fail,
                    format!("cannot read code_elements: {e}"),
                    "Orphan detection needs the element set to compare against.",
                )
            }
        };
        let orphans: Vec<&(String, String, String)> = edges
            .iter()
            .filter(|(s, t, _)| !qns.contains(s) || !qns.contains(t))
            .collect();
        if orphans.is_empty() {
            Finding::new(
                CHECK,
                CheckStatus::Pass,
                format!("{} sampled edge(s) all resolve", edges.len()),
                "",
            )
        } else {
            let (s, t, ty) = orphans[0];
            Finding::new(
                CHECK,
                CheckStatus::Fail,
                format!(
                    "{}/{} sampled edges reference missing elements; e.g. \
                     {ty}: {s} -> {t}",
                    orphans.len(),
                    edges.len()
                ),
                "Dangling edges break graph traversals — re-run `leankg index` to rebuild \
                 (delete-then-insert), or purge leftovers with `leankg gc`.",
            )
        }
    }
}

/// Check 7 — duplicated qualified_names in code_elements.
struct DuplicateNamesCheck;

impl DoctorCheck for DuplicateNamesCheck {
    fn name(&self) -> &'static str {
        "duplicate-names"
    }

    fn run(&self, ctx: &DeepContext) -> Finding {
        const CHECK: &str = "duplicate-names";
        let qns = match ctx.probes.qualified_names() {
            Ok(v) => v,
            Err(e) => {
                return Finding::new(
                    CHECK,
                    CheckStatus::Fail,
                    format!("cannot read code_elements: {e}"),
                    "Duplicate detection needs the full qualified_name column; verify PG \
                     connectivity.",
                )
            }
        };
        let mut counts: BTreeMap<String, u64> = BTreeMap::new();
        for qn in &qns {
            *counts.entry(qn.clone()).or_insert(0) += 1;
        }
        let mut dupes: Vec<(String, u64)> = counts.into_iter().filter(|(_, c)| *c > 1).collect();
        if dupes.is_empty() {
            return Finding::new(
                CHECK,
                CheckStatus::Pass,
                format!("{} qualified name(s), no duplicates", qns.len()),
                "",
            );
        }
        // Worst offenders first, then alphabetical for stable output.
        dupes.sort_by(|a, b| b.1.cmp(&a.1).then_with(|| a.0.cmp(&b.0)));
        dupes.truncate(DUPLICATE_TOP_LIMIT);
        let listed = dupes
            .iter()
            .map(|(n, c)| format!("{n}×{c}"))
            .collect::<Vec<_>>()
            .join(", ");
        Finding::new(
            CHECK,
            CheckStatus::Fail,
            format!(
                "{} duplicated qualified_name(s); top: {listed}",
                dupes.len()
            ),
            "Duplicate rows double-count symbols in queries — a full `leankg index` \
             wipes and reinserts per env, clearing them.",
        )
    }
}

/// Check 8 — `.leankg` directory writability + stray lock files.
struct LeankgDirCheck;

impl DoctorCheck for LeankgDirCheck {
    fn name(&self) -> &'static str {
        "leankg-dir"
    }

    fn run(&self, ctx: &DeepContext) -> Finding {
        const CHECK: &str = "leankg-dir";
        let dir = ctx.leankg_dir;
        if !dir.exists() {
            return Finding::new(
                CHECK,
                CheckStatus::Fail,
                format!(".leankg not found at {}", dir.display()),
                "Run `leankg init` in the project root to create it.",
            );
        }
        if !dir.is_dir() {
            return Finding::new(
                CHECK,
                CheckStatus::Fail,
                format!(".leankg exists but is not a directory: {}", dir.display()),
                "Remove the stray file and run `leankg init`.",
            );
        }

        let probe_path = dir.join(format!(".doctor_probe_{}", std::process::id()));
        if let Err(e) = std::fs::write(&probe_path, b"ok") {
            return Finding::new(
                CHECK,
                CheckStatus::Fail,
                format!("not writable: {e}"),
                format!(
                    "Fix ownership/permissions on {} so leankg can write state.",
                    dir.display()
                ),
            );
        }
        let _ = std::fs::remove_file(&probe_path);

        let locks: Vec<String> = match std::fs::read_dir(dir) {
            Ok(entries) => entries
                .flatten()
                .map(|e| e.file_name().to_string_lossy().to_string())
                .filter(|n| n.ends_with(".lock"))
                .collect(),
            Err(e) => {
                return Finding::new(
                    CHECK,
                    CheckStatus::Fail,
                    format!("cannot list {}: {e}", dir.display()),
                    "Fix filesystem permissions on the .leankg directory.",
                )
            }
        };
        if locks.is_empty() {
            Finding::new(
                CHECK,
                CheckStatus::Pass,
                "writable, no stray lock files",
                "",
            )
        } else {
            Finding::new(
                CHECK,
                CheckStatus::Warn,
                format!("stray lock file(s): {}", locks.join(", ")),
                "Locks are written by embed/watch runs; delete them if no such process \
                 is alive (`cat <lock>` shows the owning PID).",
            )
        }
    }
}

/// Convenience constructor used by the CLI dispatch path.
pub fn default_probes(db: SharedDb) -> BackendProbes {
    BackendProbes::new(db)
}

/// Run the full deep diagnosis for `project_root` using its `.leankg`
/// database handle. Returns the report plus the rendered human table.
pub fn run_deep(project_root: &Path) -> Result<(DoctorReport, PathBuf), String> {
    let db_path = project_root.join(".leankg");
    if !db_path.exists() {
        return Err(format!(
            ".leankg not found under {}; run `leankg init` first",
            project_root.display()
        ));
    }
    // The indexer keys its schema off the INDEXED directory (commonly
    // `<root>/src` when invoked as `leankg index ./src`), not the project
    // root. Try the likely identities in order and take the first that owns
    // a real per-project schema — never the shared `public` fallback, which
    // can serve unrelated rows on multi-tenant Postgres.
    let mut candidates = vec![db_path.clone(), project_root.join("src").join(".leankg")];
    if let Ok(yaml) = std::fs::read_to_string(project_root.join("leankg.yaml")) {
        if let Ok(config) = serde_yaml::from_str::<crate::config::ProjectConfig>(&yaml) {
            if let Some(pp) = config.project.project_path {
                let joined = crate::db::backend::canonical_project_root_in(&pp, project_root);
                candidates.push(joined.join(".leankg"));
            }
        }
    }
    candidates.dedup();
    let mut last_err = String::new();
    for cand in &candidates {
        match crate::db::backend::init_db_readonly_strict(cand) {
            Ok(db) => {
                let probes = BackendProbes::new(db);
                let registry = CheckRegistry::with_defaults();
                let ctx =
                    DeepContext::new(&probes, project_root, &db_path, PoolEnvSnapshot::from_env());
                return Ok((registry.run_all(&ctx), db_path));
            }
            Err(e) => last_err = e.to_string(),
        }
    }
    Err(format!(
        "no indexed project schema found for {} (tried {} identities); \
         last error: {last_err}. Re-run `leankg index <path>` against this project.",
        project_root.display(),
        candidates.len()
    ))
}

// ---------------------------------------------------------------------------
// Tests (TDD RED — written before the real check bodies)
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc;
    use tempfile::TempDir;

    type Edges = Vec<(String, String, String)>;

    /// Canned probe results. Defaults describe a tiny healthy deployment:
    /// fast ping, fully-applied migrations, two indexed/fresh files, full
    /// embedding coverage, clean edges.
    struct StubProbes {
        ping: Result<u64, String>,
        migrations: Result<Vec<String>, String>,
        indexed: Result<Vec<String>, String>,
        qns: Result<Vec<String>, String>,
        edges: Result<Edges, String>,
        embedded: Result<Option<Vec<String>>, String>,
    }

    impl Default for StubProbes {
        fn default() -> Self {
            StubProbes {
                ping: Ok(12),
                migrations: Ok(embedded_migration_ids()
                    .into_iter()
                    .map(str::to_string)
                    .collect()),
                indexed: Ok(vec!["/proj/a.rs".into(), "/proj/b.rs".into()]),
                qns: Ok(vec!["/proj/a.rs::a".into(), "/proj/b.rs::b".into()]),
                edges: Ok(vec![(
                    "/proj/a.rs::a".into(),
                    "/proj/b.rs::b".into(),
                    "CALLS".into(),
                )]),
                embedded: Ok(Some(vec!["/proj/a.rs::a".into(), "/proj/b.rs::b".into()])),
            }
        }
    }

    impl DeepProbes for StubProbes {
        fn ping_ms(&self) -> Result<u64, String> {
            self.ping.clone()
        }
        fn applied_migrations(&self) -> Result<Vec<String>, String> {
            self.migrations.clone()
        }
        fn indexed_files(&self) -> Result<Vec<String>, String> {
            self.indexed.clone()
        }
        fn qualified_names(&self) -> Result<Vec<String>, String> {
            self.qns.clone()
        }
        fn relationship_edges(&self) -> Result<Edges, String> {
            self.edges.clone()
        }
        fn embedded_names(&self) -> Result<Option<Vec<String>>, String> {
            self.embedded.clone()
        }
    }

    /// Tempdir fixture mirroring a healthy project root: writable
    /// `.leankg`, two supported source files on disk matching the stub's
    /// indexed list is done by rewriting paths per-fixture.
    struct Fixture {
        _dir: TempDir,
        root: PathBuf,
        leankg: PathBuf,
    }

    fn healthy_fixture(files: &[&str]) -> Fixture {
        let dir = tempfile::tempdir().expect("tempdir");
        let root = dir.path().to_path_buf();
        let leankg = root.join(".leankg");
        std::fs::create_dir_all(&leankg).expect("mk .leankg");
        for f in files {
            let p = root.join(f);
            if let Some(parent) = p.parent() {
                std::fs::create_dir_all(parent).expect("mkdir fixture parent");
            }
            std::fs::write(&p, "fn x() {}\n").expect("write fixture");
        }
        Fixture {
            _dir: dir,
            root,
            leankg,
        }
    }

    fn ctx<'a>(probes: &'a StubProbes, fixture: &'a Fixture) -> DeepContext<'a> {
        DeepContext::new(
            probes,
            &fixture.root,
            &fixture.leankg,
            PoolEnvSnapshot::default(),
        )
    }

    fn finding_of(check: &dyn DoctorCheck, probes: &StubProbes, fixture: &Fixture) -> Finding {
        check.run(&ctx(probes, fixture))
    }

    fn assert_hinted(f: &Finding) {
        assert!(
            !f.hint.trim().is_empty(),
            "{}: hint must be actionable",
            f.check
        );
        assert!(
            !f.detail.trim().is_empty(),
            "{}: detail must be filled",
            f.check
        );
    }

    #[test]
    fn healthy_environment_all_pass_exit_zero() {
        let fx = healthy_fixture(&["a.rs", "b.rs"]);
        let mut probes = StubProbes::default();
        probes.indexed = Ok(vec![
            fx.root.join("a.rs").to_string_lossy().to_string(),
            fx.root.join("b.rs").to_string_lossy().to_string(),
        ]);
        let reg = CheckRegistry::with_defaults();
        let report = reg.run_all(&ctx(&probes, &fx));
        assert_eq!(report.findings.len(), 8);
        for f in &report.findings {
            assert_eq!(f.status, CheckStatus::Pass, "{} should pass", f.check);
            assert!(!f.detail.is_empty(), "{} should have detail", f.check);
        }
        assert_eq!(report.exit_code(), 0);
        let c = report.counts();
        assert_eq!((c.pass, c.warn, c.fail), (8, 0, 0));
    }

    #[test]
    fn registry_has_eight_named_checks_in_order() {
        let reg = CheckRegistry::with_defaults();
        assert_eq!(
            reg.names(),
            vec![
                "pg-latency",
                "migrations",
                "index-freshness",
                "embedding-coverage",
                "pool-env",
                "orphaned-relationships",
                "duplicate-names",
                "leankg-dir",
            ]
        );
    }

    #[test]
    fn pg_latency_threshold_mapping() {
        let fx = healthy_fixture(&[]);
        let check = PgLatencyCheck;

        let mut p = StubProbes::default();
        p.ping = Ok(100);
        assert_eq!(finding_of(&check, &p, &fx).status, CheckStatus::Pass);

        p.ping = Ok(LATENCY_WARN_MS + 100);
        let f = finding_of(&check, &p, &fx);
        assert_eq!(f.status, CheckStatus::Warn);
        assert!(f.detail.contains("ms"));
        assert_hinted(&f);

        p.ping = Ok(LATENCY_FAIL_MS + 1_000);
        let f = finding_of(&check, &p, &fx);
        assert_eq!(f.status, CheckStatus::Fail);
        assert_hinted(&f);

        p.ping = Err("connection refused".into());
        let f = finding_of(&check, &p, &fx);
        assert_eq!(f.status, CheckStatus::Fail);
        assert!(f.detail.contains("connection refused"));
        assert!(f.hint.contains("LEANKG_PG_URL"));
    }

    #[test]
    fn migrations_pending_is_fail_unknown_is_warn() {
        let fx = healthy_fixture(&[]);
        let check = MigrationsCheck;

        let mut p = StubProbes::default();
        let all = embedded_migration_ids();
        p.migrations = Ok(all[..all.len() - 1].iter().map(|s| s.to_string()).collect());
        let f = finding_of(&check, &p, &fx);
        assert_eq!(f.status, CheckStatus::Fail);
        assert!(f.detail.contains(all[all.len() - 1]));
        assert_hinted(&f);

        p.migrations = Ok(all
            .iter()
            .map(|s| s.to_string())
            .chain(["099_from_the_future".to_string()])
            .collect());
        let f = finding_of(&check, &p, &fx);
        assert_eq!(f.status, CheckStatus::Warn);
        assert!(f.detail.contains("099_from_the_future"));

        // Pending beats unknown-warn when both are present.
        p.migrations = Ok(vec![
            "001_schema".to_string(),
            "099_from_the_future".to_string(),
        ]);
        assert_eq!(finding_of(&check, &p, &fx).status, CheckStatus::Fail);
    }

    #[test]
    fn migrations_probe_error_is_fail_with_hint() {
        let fx = healthy_fixture(&[]);
        let mut p = StubProbes::default();
        p.migrations = Err(r#"relation "migrations" does not exist"#.to_string());
        let f = finding_of(&MigrationsCheck, &p, &fx);
        assert_eq!(f.status, CheckStatus::Fail);
        assert_hinted(&f);
    }

    #[test]
    fn index_freshness_pass_warn_fail_modes() {
        let check = IndexFreshnessCheck;

        // Fresh: disk files exactly indexed.
        let fx = healthy_fixture(&["a.rs", "b.rs"]);
        let mut p = StubProbes::default();
        p.indexed = Ok(vec![
            fx.root.join("a.rs").to_string_lossy().to_string(),
            fx.root.join("b.rs").to_string_lossy().to_string(),
        ]);
        assert_eq!(finding_of(&check, &p, &fx).status, CheckStatus::Pass);

        // Missing: an on-disk file absent from the index → warn.
        p.indexed = Ok(vec![fx.root.join("a.rs").to_string_lossy().to_string()]);
        let f = finding_of(&check, &p, &fx);
        assert_eq!(f.status, CheckStatus::Warn);
        assert!(f.detail.contains("missing"), "detail: {}", f.detail);
        assert_hinted(&f);

        // Stale: indexed path that no longer exists on disk → warn.
        let fx2 = healthy_fixture(&["a.rs"]);
        p.indexed = Ok(vec![
            fx2.root.join("a.rs").to_string_lossy().to_string(),
            "/gone/dir/removed.rs".to_string(),
        ]);
        let f = finding_of(&check, &p, &fx2);
        assert_eq!(f.status, CheckStatus::Warn);
        assert!(f.detail.contains("stale"), "detail: {}", f.detail);

        // Empty index while files exist on disk → fail.
        p.indexed = Ok(vec![]);
        let f = finding_of(&check, &p, &fx2);
        assert_eq!(f.status, CheckStatus::Fail);
        assert_hinted(&f);
    }

    #[test]
    fn index_freshness_suffix_match_tolerates_relative_index_paths() {
        let fx = healthy_fixture(&["src/a.rs"]);
        let mut p = StubProbes::default();
        // Index stored the relative spelling; doctor walks absolute.
        p.indexed = Ok(vec!["src/a.rs".to_string()]);
        assert_eq!(
            finding_of(&IndexFreshnessCheck, &p, &fx).status,
            CheckStatus::Pass
        );
    }

    #[test]
    fn index_freshness_treats_directory_nodes_as_fresh() {
        // generate_physical_structure seeds Project/folder nodes whose
        // file_path is a DIRECTORY (the root itself). Those must count as
        // fresh — "stale" means the filesystem object is gone, not merely
        // that the entry is not a regular file.
        let fx = healthy_fixture(&["a.rs"]);
        let mut p = StubProbes::default();
        p.indexed = Ok(vec![
            fx.root.to_string_lossy().to_string(),
            fx.root.join("a.rs").to_string_lossy().to_string(),
        ]);
        let f = finding_of(&IndexFreshnessCheck, &p, &fx);
        assert_eq!(f.status, CheckStatus::Pass, "detail: {}", f.detail);
    }

    #[test]
    fn index_freshness_flags_vanished_directory_paths_as_stale() {
        let fx = healthy_fixture(&["a.rs"]);
        let mut p = StubProbes::default();
        p.indexed = Ok(vec![
            fx.root.join("a.rs").to_string_lossy().to_string(),
            "/deleted/tree".to_string(),
        ]);
        let f = finding_of(&IndexFreshnessCheck, &p, &fx);
        assert_eq!(f.status, CheckStatus::Warn);
        assert!(f.detail.contains("stale"), "detail: {}", f.detail);
    }

    #[test]
    fn index_freshness_skips_synthetic_uri_entries() {
        // Dynamic ontology concepts / agent diaries live in code_elements
        // with `scheme://` file_paths (e.g. ontology://local:agent:…:v1).
        // They have no filesystem backing and must never count as stale —
        // regression for doctor --deep reporting "100% no longer exist on
        // disk" on graphs that only contain synthetic entries.
        let fx = healthy_fixture(&["a.rs"]);
        let mut p = StubProbes::default();
        p.indexed = Ok(vec![
            fx.root.join("a.rs").to_string_lossy().to_string(),
            "ontology://local:agent:known_issue:agent-000000007af8b289:v1".to_string(),
            "ontology://local:concept:legacy_integration:v3".to_string(),
        ]);
        let f = finding_of(&IndexFreshnessCheck, &p, &fx);
        assert_eq!(f.status, CheckStatus::Pass, "detail: {}", f.detail);
        assert!(
            f.detail.contains("2 synthetic URI entries skipped"),
            "detail: {}",
            f.detail
        );
    }

    #[test]
    fn index_freshness_all_synthetic_entries_never_fail() {
        // Synthetic-only indexes must not report FAIL; unindexed real files
        // still warrant a WARN (useful signal), never the stale FAIL path.
        let fx = healthy_fixture(&["a.rs"]);
        let mut p = StubProbes::default();
        p.indexed = Ok(vec!["ontology://local:agent:x:v1".to_string()]);
        let f = finding_of(&IndexFreshnessCheck, &p, &fx);
        assert_ne!(f.status, CheckStatus::Fail, "detail: {}", f.detail);
        assert!(
            !f.detail.contains("no longer exist"),
            "detail: {}",
            f.detail
        );
    }

    #[test]
    fn embedding_coverage_modes() {
        let check = EmbeddingCoverageCheck;
        let fx = healthy_fixture(&[]);

        // Tables absent → informative pass.
        let mut p = StubProbes::default();
        p.embedded = Ok(None);
        let f = finding_of(&check, &p, &fx);
        assert_eq!(f.status, CheckStatus::Pass);
        assert!(!f.detail.is_empty());

        // Post-migration reality: the table EXISTS but embed never ran
        // (zero rows). Migrations always create embedding_state, so this —
        // not table-absence — is the canonical never-embedded state.
        p.embedded = Ok(Some(vec![]));
        let f = finding_of(&check, &p, &fx);
        assert_eq!(f.status, CheckStatus::Pass, "never-embedded must not warn");
        assert!(!f.detail.is_empty());

        // Full coverage → pass.
        p.embedded = Ok(Some(vec!["/proj/a.rs::a".into(), "/proj/b.rs::b".into()]));
        assert_eq!(finding_of(&check, &p, &fx).status, CheckStatus::Pass);

        // Half uncovered → warn with percentage.
        p.embedded = Ok(Some(vec!["/proj/a.rs::a".into()]));
        let f = finding_of(&check, &p, &fx);
        assert_eq!(f.status, CheckStatus::Warn);
        assert!(f.detail.contains('%'), "detail: {}", f.detail);
        assert_hinted(&f);

        // Genuine probe failure (not absence) → fail.
        p.embedded = Err("permission denied for table embedding_state".into());
        let f = finding_of(&check, &p, &fx);
        assert_eq!(f.status, CheckStatus::Fail);
        assert_hinted(&f);
    }

    #[test]
    fn pool_env_validation_ranges() {
        let check = PoolEnvCheck;
        let fx = healthy_fixture(&[]);
        let p = StubProbes::default();

        let mk = |size: Option<&str>, wait: Option<&str>| DeepContext {
            probes: &p,
            project_root: &fx.root,
            leankg_dir: &fx.leankg,
            pool_env: PoolEnvSnapshot {
                pool_size: size.map(str::to_string),
                pool_wait_ms: wait.map(str::to_string),
            },
        };

        // Unset → defaults, pass.
        let f = check.run(&mk(None, None));
        assert_eq!(f.status, CheckStatus::Pass);

        // Valid values → pass.
        assert_eq!(
            check.run(&mk(Some("8"), Some("20000"))).status,
            CheckStatus::Pass
        );

        // Invalid size → fail with range hint.
        for bad in ["0", "-3", "banana"] {
            let f = check.run(&mk(Some(bad), None));
            assert_eq!(f.status, CheckStatus::Fail, "size={bad}");
            assert!(f.hint.contains("LEANKG_PG_POOL_SIZE"));
        }

        // Oversize → warn.
        let f = check.run(&mk(Some(&(POOL_SIZE_MAX + 1).to_string()), None));
        assert_eq!(f.status, CheckStatus::Warn);

        // Invalid wait → fail; oversized wait → warn.
        assert_eq!(check.run(&mk(None, Some("abc"))).status, CheckStatus::Fail);
        assert_eq!(
            check
                .run(&mk(None, Some(&(POOL_WAIT_MAX_MS + 1).to_string())))
                .status,
            CheckStatus::Warn
        );
        assert_eq!(check.run(&mk(None, Some("-1"))).status, CheckStatus::Fail);
    }

    #[test]
    fn orphan_edges_detected_and_reported() {
        let check = OrphanEdgesCheck;
        let fx = healthy_fixture(&[]);

        // Clean graph → pass.
        let p = StubProbes::default();
        assert_eq!(finding_of(&check, &p, &fx).status, CheckStatus::Pass);

        // One dangling target → fail, sample shown.
        let mut p = StubProbes::default();
        p.edges = Ok(vec![(
            "/proj/a.rs::a".into(),
            "/ghost/deleted.rs::gone".into(),
            "CALLS".into(),
        )]);
        let f = finding_of(&check, &p, &fx);
        assert_eq!(f.status, CheckStatus::Fail);
        assert!(f.detail.contains("/ghost/deleted.rs::gone"));
        assert!(f.detail.contains("CALLS"));
        assert_hinted(&f);

        // Probe failure → fail.
        p.edges = Err("boom".into());
        assert_eq!(finding_of(&check, &p, &fx).status, CheckStatus::Fail);
    }

    #[test]
    fn duplicates_fail_with_top_offenders() {
        let check = DuplicateNamesCheck;
        let fx = healthy_fixture(&[]);

        let p = StubProbes::default();
        assert_eq!(finding_of(&check, &p, &fx).status, CheckStatus::Pass);

        let dup_qn = "/dup/x.rs::x";
        let mut qns: Vec<String> = vec![dup_qn.to_string(); 3];
        for _ in 0..7 {
            qns.push("/dup/y.rs::y".to_string());
        }
        let mut p = StubProbes::default();
        p.qns = Ok(qns);
        let f = finding_of(&check, &p, &fx);
        assert_eq!(f.status, CheckStatus::Fail);
        assert!(f.detail.contains(dup_qn), "detail: {}", f.detail);
        // Top offender (×7) must be listed first.
        assert!(
            f.detail.find("/dup/y.rs::y").unwrap_or(usize::MAX)
                < f.detail.find(dup_qn).unwrap_or(usize::MAX),
            "top offender ordering wrong: {}",
            f.detail
        );
        assert_hinted(&f);
    }

    #[test]
    fn leankg_dir_writability_and_locks() {
        let check = LeankgDirCheck;

        // Healthy dir → pass.
        let fx = healthy_fixture(&[]);
        let p = StubProbes::default();
        assert_eq!(finding_of(&check, &p, &fx).status, CheckStatus::Pass);

        // Stray lock file → warn naming it.
        std::fs::write(fx.root.join(".leankg/embed.lock"), "999999").unwrap();
        let f = finding_of(&check, &p, &fx);
        assert_eq!(f.status, CheckStatus::Warn);
        assert!(f.detail.contains("embed.lock"));
        assert_hinted(&f);

        // Missing dir → fail pointing at init.
        let dir = tempfile::tempdir().unwrap();
        let leankg = dir.path().join(".leankg");
        let missing = DeepContext {
            probes: &p,
            project_root: dir.path(),
            leankg_dir: &leankg,
            pool_env: PoolEnvSnapshot::default(),
        };
        let f = check.run(&missing);
        assert_eq!(f.status, CheckStatus::Fail);
        assert!(f.hint.contains("init"));

        // `.leankg` exists but is a regular file → fail.
        let dir2 = tempfile::tempdir().unwrap();
        std::fs::write(dir2.path().join(".leankg"), "not a dir").unwrap();
        let leankg2 = dir2.path().join(".leankg");
        let blocked = DeepContext {
            probes: &p,
            project_root: dir2.path(),
            leankg_dir: &leankg2,
            pool_env: PoolEnvSnapshot::default(),
        };
        assert_eq!(check.run(&blocked).status, CheckStatus::Fail);
    }

    #[test]
    fn json_round_trip_preserves_findings() {
        let report = DoctorReport {
            findings: vec![
                Finding::new("pg-latency", CheckStatus::Pass, "12 ms", ""),
                Finding::new(
                    "migrations",
                    CheckStatus::Warn,
                    "schema ahead",
                    "upgrade binary",
                ),
                Finding::new("duplicate-names", CheckStatus::Fail, "2 dupes", "re-index"),
            ],
        };
        let json = serde_json::to_string(&report).expect("serialize");
        let back: DoctorReport = serde_json::from_str(&json).expect("deserialize");
        assert_eq!(back, report);
    }

    #[test]
    fn json_render_includes_summary_block() {
        let mut report = DoctorReport { findings: vec![] };
        report.findings.push(Finding::new(
            "pg-latency",
            CheckStatus::Warn,
            "slow",
            "move closer",
        ));
        let value: serde_json::Value =
            serde_json::from_str(&report.render_json()).expect("valid json");
        assert_eq!(value["summary"]["warn"], 1);
        assert_eq!(value["findings"][0]["check"], "pg-latency");
        assert_eq!(value["findings"][0]["status"], "warn");
    }

    #[test]
    fn exit_code_aggregation_rules() {
        let base = || DoctorReport { findings: vec![] };
        assert_eq!(base().exit_code(), 0);

        let mut warns = base();
        warns
            .findings
            .push(Finding::new("x", CheckStatus::Warn, "d", "h"));
        assert_eq!(warns.exit_code(), 1);

        let mut fails = base();
        fails
            .findings
            .push(Finding::new("x", CheckStatus::Warn, "d", "h"));
        fails
            .findings
            .push(Finding::new("y", CheckStatus::Fail, "d", "h"));
        assert_eq!(fails.exit_code(), 2);
    }

    #[test]
    fn table_rows_are_aligned_and_uppercase() {
        let report = DoctorReport {
            findings: vec![
                Finding::new("pg-latency", CheckStatus::Pass, "12 ms", ""),
                Finding::new(
                    "orphaned-relationships",
                    CheckStatus::Fail,
                    "1 orphan",
                    "re-index",
                ),
            ],
        };
        let out = report.render_table();
        assert!(out.contains("PASS"));
        assert!(out.contains("FAIL"));
        let piped: Vec<usize> = out
            .lines()
            .filter(|l| l.contains('|'))
            .map(|l| l.chars().count())
            .collect();
        assert!(piped.len() >= 3, "expected header + separator-less rows");
        let all_equal = piped.iter().all(|w| *w == piped[0]);
        assert!(all_equal, "rows not aligned: {piped:?}");
        assert!(out.contains("exit"));
    }

    #[test]
    fn backend_probes_is_object_safe_and_shareable() {
        let db: SharedDb = Arc::new(crate::db::fake::FakeBackend::new());
        let probes = BackendProbes::new(db);
        // Ping fails cleanly (fake has no data wired for raw reads here);
        // we only assert the trait object composes.
        let _ = probes.ping_ms();
        let dyn_ref: &dyn DeepProbes = &probes;
        let _ = dyn_ref.applied_migrations();
    }
}
