//! In-memory `DbBackend` for unit tests (no Postgres needed).
//!
//! The graph/ontology/pack unit tests seed `code_elements`, `relationships`,
//! and `business_logic` then query them back with a small legacy
//! Datalog-style script subset.
//! This fake interprets exactly that subset and fails loudly (never silently
//! wrong) on anything outside it.
//!
//! Subset served:
//! - `?[cols] <- $batch_data :put rel { cols }` and `<- [[...]]` literal rows
//! - `?[cols] := *rel[...]` with equality / param / `or` / `regex_matches` /
//!   `lowercase` filters, `{tail}` column suffix, `:limit` / `:offset`
//! - `?[node, count(node)] := *rel[...]` aggregate reads
//! - `:rm rel { pk }` / `:rm rel` (delete all) / `:delete rel where ...`
//! - `::relations`, `VACUUM`, `query_cache` -> no-op / empty

use super::backend::DbBackend;
use super::value::{DataValue, NamedRows, Num};
use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};

/// Primary-key column per relation, mirroring `pg::translate::pk_for_table`
/// so the fake's `:put` upserts on the same key the PG translator does.
fn pk_for_table(rel: &str) -> Option<&'static str> {
    Some(match rel {
        "embedding_state" | "embedding_vectors" => "qualified_name",
        "index_inventory" => "key",
        "index_hashes" => "path",
        "migrations" => "id",
        "api_keys" => "key_hash",
        "accounts" | "orgs" | "access_tokens" => "id",
        // Composite keys: mirror translate.rs so the fake upserts on the same
        // columns PG's `ON CONFLICT (org_id, account_id)` targets.
        "org_memberships" => "org_id, account_id",
        "team_members" => "team_id, account_id",
        t if t.starts_with("embedding_state_") || t.starts_with("embedding_vectors_") => {
            "qualified_name"
        }
        _ => return None,
    })
}

/// Column order for each relation the fake understands. Mirrors the PG
/// `schema.sql` / `models.rs` layout for `code_elements`, `relationships`,
/// and `business_logic`.
fn table_columns(rel: &str) -> Option<&'static [&'static str]> {
    Some(match rel {
        "code_elements" => &[
            "qualified_name",
            "element_type",
            "name",
            "file_path",
            "line_start",
            "line_end",
            "language",
            "parent_qualified",
            "cluster_id",
            "cluster_label",
            "metadata",
            "env",
            "ontology_layer",
        ],
        "relationships" => &[
            "source_qualified",
            "target_qualified",
            "rel_type",
            "confidence",
            "metadata",
            "env",
        ],
        "business_logic" => &[
            "element_qualified",
            "description",
            "user_story_id",
            "feature_id",
        ],
        "index_inventory" => &[
            "key",
            "computed_at",
            "total_elements",
            "total_relationships",
            "total_vectors",
            "total_documents",
            "total_doc_sections",
            "elements_by_type_json",
            "relationships_by_type_json",
            "vectors_by_type_json",
            "estimated_vector_bytes",
            "estimated_hnsw_bytes",
            "notes",
        ],
        "embedding_state" => &[
            "qualified_name",
            "usearch_key",
            "content_hash",
            "state",
            "embedded_at",
        ],
        "embedding_vectors" => &["qualified_name", "vector"],
        // Per-model embed collections share the legacy shapes.
        t if t.starts_with("embedding_state_") => &[
            "qualified_name",
            "usearch_key",
            "content_hash",
            "state",
            "embedded_at",
        ],
        t if t.starts_with("embedding_vectors_") => &["qualified_name", "vector"],
        // OAuth2-style auth tables (004_auth).
        "accounts" => &[
            "id",
            "email",
            "name",
            "password_hash",
            "status",
            "created_at",
            "updated_at",
        ],
        "orgs" => &["id", "name", "owner_account_id", "created_at", "updated_at"],
        "org_memberships" => &["org_id", "account_id", "role", "joined_at"],
        "team_members" => &["team_id", "account_id", "role", "joined_at"],
        "access_tokens" => &[
            "id",
            "account_id",
            "org_id",
            "token_hash",
            "name",
            "role",
            "scopes",
            "expires_at",
            "created_at",
            "revoked_at",
            "last_used_at",
        ],
        "resource_ownership" => &[
            "resource_type",
            "resource_id",
            "owner_account_id",
            "org_id",
            "created_at",
        ],
        _ => return None,
    })
}

/// An in-memory relation: rows stored as column-name -> value maps so
/// projection can pick any subset of columns.
type Store = Arc<Mutex<BTreeMap<String, Vec<Vec<DataValue>>>>>;

/// Shared fake backend. Cheap to clone; all clones share one store.
#[derive(Clone)]
pub struct FakeBackend {
    store: Store,
    /// FR-ENT-1: buffered audit ledger so recorder unit tests run without a
    /// live Postgres. Shared across clones like `store`.
    audit: Arc<Mutex<Vec<crate::audit::AuditEntry>>>,
    /// FR-ZCP-03: test-flippable pg_trgm availability for the router's L2
    /// rung (the in-memory fake has no real trigram index).
    trgm_available: std::sync::Arc<std::sync::atomic::AtomicBool>,
}

impl FakeBackend {
    pub fn new() -> Self {
        Self {
            store: Arc::new(Mutex::new(BTreeMap::new())),
            audit: Arc::new(Mutex::new(Vec::new())),
            trgm_available: std::sync::Arc::new(std::sync::atomic::AtomicBool::new(false)),
        }
    }

    /// Snapshot of the buffered audit ledger (FR-ENT-1 tests).
    pub fn audit_entries(&self) -> Vec<crate::audit::AuditEntry> {
        self.audit.lock().unwrap_or_else(|e| e.into_inner()).clone()
    }

    /// FR-ZCP-03: flip the trigram-availability flag the
    /// `DbBackend::trgm_available` override reports.
    pub fn set_trgm_available(&self, available: bool) {
        self.trgm_available
            .store(available, std::sync::atomic::Ordering::Relaxed);
    }

    /// A fake whose store is keyed by a test `db_path`, so `init_db(path)`
    /// and `init_db_readonly(path)` on the SAME path share one store —
    /// mirroring the old scratch-schema mapping in `test_scratch_schema`.
    pub fn for_path(db_path: &std::path::Path) -> Self {
        use std::collections::HashMap;
        use std::sync::OnceLock;

        static STORES: OnceLock<Mutex<HashMap<std::path::PathBuf, Store>>> = OnceLock::new();
        let map = STORES.get_or_init(|| Mutex::new(HashMap::new()));
        let mut guard = map.lock().unwrap_or_else(|e| e.into_inner());
        let key = db_path.to_path_buf();
        let store = guard
            .entry(key)
            .or_insert_with(|| Arc::new(Mutex::new(BTreeMap::new())))
            .clone();
        Self {
            store,
            audit: Arc::new(Mutex::new(Vec::new())),
            trgm_available: std::sync::Arc::new(std::sync::atomic::AtomicBool::new(false)),
        }
    }

    /// Trigram word similarity used by `suggest_element_names` when the
    /// test flips `trgm_available` on. Small ordered-subset metric — not
    /// pg_trgm's real trigram math, just deterministic test ordering.
    fn fake_word_similarity(needle: &str, name: &str) -> f64 {
        let needle = needle.to_lowercase();
        let name_l = name.to_lowercase();
        if name_l == needle {
            return 1.0;
        }
        if name_l.contains(&needle) {
            return 0.8;
        }
        // Longest common subsequence ratio, good enough for assertions.
        let n: Vec<char> = needle.chars().collect();
        let m: Vec<char> = name_l.chars().collect();
        let mut dp = vec![vec![0usize; m.len() + 1]; n.len() + 1];
        for i in 1..=n.len() {
            for j in 1..=m.len() {
                dp[i][j] = if n[i - 1] == m[j - 1] {
                    dp[i - 1][j - 1] + 1
                } else {
                    dp[i - 1][j].max(dp[i][j - 1])
                };
            }
        }
        dp[n.len()][m.len()] as f64 / n.len().max(1) as f64
    }

    fn fake_trigram_similarity(a: &str, b: &str) -> f64 {
        Self::fake_word_similarity(a, b)
    }

    /// FR-ZCP-03: in-memory fuzzy recall mirroring the seam contract —
    /// degraded ILIKE-substring by default; trigram ranking when the test
    /// flips `trgm_available` on. Never a hard error.
    fn fake_fuzzy_find(
        &self,
        query: &str,
        limit: usize,
    ) -> Result<Vec<crate::db::models::CodeElement>, Box<dyn std::error::Error>> {
        let query = query.trim();
        if query.is_empty() {
            return Ok(Vec::new());
        }
        let limit = limit.clamp(1, 100);
        let i_qn = col_index("code_elements", "qualified_name").unwrap_or(0);
        let i_name = col_index("code_elements", "name").unwrap_or(2);
        let q_lower = query.to_lowercase();
        let mut scored: Vec<(f64, crate::db::models::CodeElement)> = Vec::new();
        for row in self.rows("code_elements") {
            let qn = row[i_qn].get_str().unwrap_or("").to_string();
            let name = row[i_name].get_str().unwrap_or("").to_string();
            let name_l = name.to_lowercase();
            let qn_l = qn.to_lowercase();
            let hit = if self.trgm_available() {
                self.trgm_score_of(&q_lower, &name_l, &qn_l) > 0.0
            } else {
                name_l.contains(&q_lower) || qn_l.contains(&q_lower)
            };
            if !hit {
                continue;
            }
            let element = code_element_from_cells(&row, true);
            let score = if self.trgm_available() {
                self.trgm_score_of(&q_lower, &name_l, &qn_l)
            } else {
                0.0
            };
            scored.push((score, element));
        }
        scored.sort_by(|a, b| {
            b.0.partial_cmp(&a.0)
                .unwrap_or(std::cmp::Ordering::Equal)
                .then_with(|| a.1.name.cmp(&b.1.name))
        });
        Ok(scored.into_iter().take(limit).map(|(_, e)| e).collect())
    }

    fn trgm_score_of(&self, needle: &str, name: &str, qn: &str) -> f64 {
        Self::fake_trigram_similarity(needle, name).max(Self::fake_trigram_similarity(needle, qn))
    }

    fn fake_suggest(
        &self,
        query: &str,
        limit: usize,
    ) -> Result<Vec<String>, Box<dyn std::error::Error>> {
        let query = query.trim();
        if query.is_empty() {
            return Ok(Vec::new());
        }
        let limit = limit.clamp(1, 100);
        let i_name = col_index("code_elements", "name").unwrap_or(2);
        let q_lower = query.to_lowercase();
        let mut scored: Vec<(f64, String)> = Vec::new();
        let mut seen = std::collections::HashSet::new();
        for row in self.rows("code_elements") {
            let name = row[i_name].get_str().unwrap_or("").to_string();
            if !seen.insert(name.clone()) {
                continue;
            }
            let name_l = name.to_lowercase();
            let score = Self::fake_word_similarity(&q_lower, &name_l);
            let hit = if self.trgm_available() {
                score >= 0.5
            } else {
                name_l.contains(&q_lower)
            };
            if hit {
                scored.push((score, name));
            }
        }
        scored.sort_by(|a, b| {
            b.0.partial_cmp(&a.0)
                .unwrap_or(std::cmp::Ordering::Equal)
                .then_with(|| a.1.cmp(&b.1))
        });
        Ok(scored.into_iter().take(limit).map(|(_, n)| n).collect())
    }

    fn tables(&self) -> Vec<String> {
        self.store.lock().unwrap().keys().cloned().collect()
    }

    fn rows(&self, rel: &str) -> Vec<Vec<DataValue>> {
        self.store
            .lock()
            .unwrap()
            .get(rel)
            .cloned()
            .unwrap_or_default()
    }

    fn set_rows(&self, rel: &str, rows: Vec<Vec<DataValue>>) {
        self.store.lock().unwrap().insert(rel.to_string(), rows);
    }

    fn append_rows(&self, rel: &str, rows: Vec<Vec<DataValue>>) {
        let mut guard = self.store.lock().unwrap();
        let entry = guard.entry(rel.to_string()).or_default();
        entry.extend(rows);
    }
}

impl Default for FakeBackend {
    fn default() -> Self {
        Self::new()
    }
}

impl DbBackend for FakeBackend {
    /// FR-ZCP-03 test seam: the in-memory fake has no pg_trgm, so the
    /// router's L2 rung must exercise its ILIKE-only degradation path
    /// against this backend. Flip via `FakeBackend::set_trgm_available`.
    fn trgm_available(&self) -> bool {
        self.trgm_available
            .load(std::sync::atomic::Ordering::Relaxed)
    }

    fn fuzzy_find_elements(
        &self,
        query: &str,
        limit: usize,
    ) -> Result<Vec<crate::db::models::CodeElement>, Box<dyn std::error::Error>> {
        self.fake_fuzzy_find(query, limit)
    }

    fn suggest_element_names(
        &self,
        query: &str,
        limit: usize,
    ) -> Result<Vec<String>, Box<dyn std::error::Error>> {
        self.fake_suggest(query, limit)
    }

    #[cfg(test)]
    fn as_any(&self) -> &dyn std::any::Any {
        self
    }
    /// FR-ENT-1: buffer entries; assign sequential ids like the BIGSERIAL.
    fn insert_audit_batch(
        &self,
        entries: &[crate::audit::AuditEntry],
    ) -> Result<(), Box<dyn std::error::Error>> {
        let mut ledger = self.audit.lock().unwrap_or_else(|e| e.into_inner());
        let mut next_id = ledger.last().map(|e| e.id + 1).unwrap_or(1);
        for e in entries {
            let mut e = e.clone();
            e.id = next_id;
            next_id += 1;
            ledger.push(e);
        }
        Ok(())
    }

    fn last_audit_entry_hash(&self) -> Result<Option<String>, Box<dyn std::error::Error>> {
        Ok(self
            .audit
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .last()
            .map(|e| e.entry_hash.clone()))
    }

    /// FR-ENT-1: windowed read of the buffered ledger (inclusive bounds).
    fn query_audit(
        &self,
        since: Option<std::time::SystemTime>,
        until: Option<std::time::SystemTime>,
    ) -> Result<Vec<crate::audit::AuditEntry>, Box<dyn std::error::Error>> {
        let ledger = self.audit.lock().unwrap_or_else(|e| e.into_inner());
        Ok(ledger
            .iter()
            .filter(|e| since.map_or(true, |s| e.ts >= s) && until.map_or(true, |u| e.ts <= u))
            .cloned()
            .collect())
    }

    fn run_script(
        &self,
        query: &str,
        params: BTreeMap<String, serde_json::Value>,
    ) -> Result<NamedRows, Box<dyn std::error::Error>> {
        let q = query.trim();

        // No-op / admin commands.
        if q.starts_with("::relations") {
            return Ok(NamedRows::new(vec!["relation".to_string()], Vec::new()));
        }
        if q == "VACUUM" || q.starts_with("VACUUM") {
            return Ok(NamedRows::new(Vec::new(), Vec::new()));
        }
        if q.contains("query_cache") {
            return Ok(NamedRows::new(Vec::new(), Vec::new()));
        }
        // `::hnsw create` / `::hnsw drop` / `::hnsw ensure` — the in-memory
        // mock has no vector index; treat HNSW admin as a no-op so the embed
        // write paths (`put_pairs_to_db_script`, `state::create_hnsw_index`)
        // are exercisable in unit tests.
        if q.starts_with("::hnsw") {
            return Ok(NamedRows::new(Vec::new(), Vec::new()));
        }

        // `:create rel { ... }` — record the table so later reads find it.
        if q.starts_with(":create") {
            let rel = q
                .strip_prefix(":create")
                .and_then(|r| r.trim().split_whitespace().next())
                .ok_or_else(|| fake_err(":create missing relation name"))?;
            if table_columns(rel).is_none() {
                self.set_rows(rel, Vec::new());
            }
            return Ok(NamedRows::new(Vec::new(), Vec::new()));
        }

        // Write forms: `?[cols] <- ... :put rel { cols }` (also `:rm`, `:replace`, `:delete`).
        if let Some(op) = find_write_op(q) {
            return self.run_write(q, &op, &params);
        }

        // Read forms: `?[cols] := *rel[...] ...` (also `<-` for literal rows,
        // which are writes only in our subset; a `<-` read means an error).
        if q.starts_with("?[") {
            return self.run_read(q, &params);
        }

        Err(fake_err(&format!("FakeBackend: unsupported query: {q}")))
    }

    fn import_relations(
        &self,
        data: BTreeMap<String, NamedRows>,
    ) -> Result<(), Box<dyn std::error::Error>> {
        for (rel, rows) in data {
            for row in rows.rows {
                self.append_rows(&rel, vec![row]);
            }
        }
        Ok(())
    }

    fn redacted_url(&self) -> String {
        "fake://in-memory".to_string()
    }

    fn mutability_for(&self, query: &str) -> super::pg::mutability::ScriptMutability {
        super::pg::mutability::mutability_for(query)
    }

    // ---- W8 wave-2: SQL-first code_elements reads (in-memory parity) ----

    fn find_element_by_key(
        &self,
        qualified_name: &str,
    ) -> Result<Option<crate::db::models::CodeElement>, Box<dyn std::error::Error>> {
        let qn_idx = col_index("code_elements", "qualified_name").unwrap_or(0);
        let rows = self.rows("code_elements");
        Ok(rows
            .iter()
            .find(|r| r.get(qn_idx).and_then(|v| v.get_str()) == Some(qualified_name))
            .map(|r| code_element_from_cells(r, false)))
    }

    fn find_element_by_name_col(
        &self,
        name: &str,
    ) -> Result<Option<crate::db::models::CodeElement>, Box<dyn std::error::Error>> {
        let nm_idx = col_index("code_elements", "name").unwrap_or(2);
        let rows = self.rows("code_elements");
        Ok(rows
            .iter()
            .find(|r| r.get(nm_idx).and_then(|v| v.get_str()) == Some(name))
            .map(|r| code_element_from_cells(r, false)))
    }

    fn elements_by_qualified_names(
        &self,
        qualified_names: &[String],
    ) -> Result<Vec<crate::db::models::CodeElement>, Box<dyn std::error::Error>> {
        let qn_idx = col_index("code_elements", "qualified_name").unwrap_or(0);
        let wanted: std::collections::HashSet<&str> =
            qualified_names.iter().map(|s| s.as_str()).collect();
        Ok(self
            .rows("code_elements")
            .iter()
            .filter(|r| {
                r.get(qn_idx)
                    .and_then(|v| v.get_str())
                    .map(|qn| wanted.contains(qn))
                    .unwrap_or(false)
            })
            .map(|r| code_element_from_cells(r, true))
            .collect())
    }
}

fn fake_err(msg: &str) -> Box<dyn std::error::Error> {
    Box::new(std::io::Error::new(
        std::io::ErrorKind::InvalidInput,
        msg.to_string(),
    ))
}

/// Find a trailing write operator and its name (`:put`/`:rm`/`:replace`/`:delete`).
fn find_write_op(q: &str) -> Option<String> {
    for op in [":put", ":rm", ":replace", ":delete"] {
        // The operator sits after the rule body, usually after `:put` on the
        // same line. Match `:put rel { ... }` or `:rm rel { ... }`.
        if let Some(idx) = q.find(op) {
            return Some(op.to_string());
        }
    }
    None
}

impl FakeBackend {
    fn run_write(
        &self,
        q: &str,
        op: &str,
        params: &BTreeMap<String, serde_json::Value>,
    ) -> Result<NamedRows, Box<dyn std::error::Error>> {
        // Locate the relation + column list after the operator:
        //   `... :put code_elements { qualified_name, ... }`
        let after = q
            .find(op)
            .map(|i| &q[i + op.len()..])
            .ok_or_else(|| fake_err("write op missing"))?;
        let (rel, rest) = parse_rel_and_cols(after)?;

        // Extract the rows to write from the `<- ...` source (the part before
        // the operator). Source is either `$batch_data`, `$param`, a literal
        // `[[...]]` block, or (for rule-based `:rm`) a `?[cols] := ...` read.
        let source = &q[..q.find(op).unwrap()];

        match op {
            ":put" | ":replace" => {
                let rows = extract_write_rows(source, &rel, &rest.cols, params)?;
                // Upsert semantics: a `:put` on a known-PK table replaces the
                // row with the same PK (PG `ON CONFLICT ... DO UPDATE`).
                if let Some(pk) = pk_for_table(&rel) {
                    // Composite PKs are comma-separated (`org_id, account_id`).
                    let pk_cols: Vec<&str> = pk.split(", ").collect();
                    let pk_idx: Vec<usize> = pk_cols
                        .iter()
                        .map(|c| col_index(&rel, c).unwrap_or(0))
                        .collect();
                    let matches_pk = |r: &Vec<DataValue>, row: &Vec<DataValue>| {
                        pk_idx.iter().all(|&i| {
                            r.get(i).cloned().unwrap_or(DataValue::Null)
                                == row.get(i).cloned().unwrap_or(DataValue::Null)
                        })
                    };
                    let mut current = self.rows(&rel);
                    for row in rows {
                        current.retain(|r| !matches_pk(r, &row));
                        current.push(row);
                    }
                    self.set_rows(&rel, current);
                } else {
                    self.append_rows(&rel, rows);
                }
            }
            ":rm" | ":delete" => {
                if rest.cols.is_empty() {
                    // `:rm rel` — delete all rows of the relation.
                    self.set_rows(&rel, Vec::new());
                } else if source.contains(":=") {
                    // Rule-based delete: `?[cols] := *rel[...], <filters> :rm rel {cols}`.
                    // Evaluate the read side to find matching rows, then delete
                    // them by comparing all listed key columns.
                    let read = self.run_read(source.trim(), params)?;
                    let match_cols = &rest.cols;
                    let current = self.rows(&rel);
                    let kept: Vec<Vec<DataValue>> = current
                        .into_iter()
                        .filter(|row| {
                            // Keep rows that are NOT in the derived delete set.
                            !read.rows.iter().any(|d| {
                                match_cols.iter().enumerate().all(|(i, col)| {
                                    let col_idx = col_index(&rel, col).unwrap_or(usize::MAX);
                                    let row_v =
                                        row.get(col_idx).cloned().unwrap_or(DataValue::Null);
                                    let del_v = d.get(i).cloned().unwrap_or(DataValue::Null);
                                    row_v == del_v
                                })
                            })
                        })
                        .collect();
                    self.set_rows(&rel, kept);
                } else {
                    // `:rm rel { pk }` — delete by matching the projected key
                    // columns against params.
                    let current = self.rows(&rel);
                    let pk_vals: Vec<DataValue> = rest
                        .cols
                        .iter()
                        .map(|c| params.get(c).map(json_to_dv).unwrap_or(DataValue::Null))
                        .collect();
                    let kept: Vec<Vec<DataValue>> = current
                        .into_iter()
                        .filter(|row| {
                            // Keep rows whose pk columns do NOT all match the
                            // deletion keys.
                            rest.cols.iter().enumerate().any(|(i, col)| {
                                let col_idx = col_index(&rel, col).unwrap_or(usize::MAX);
                                let row_v = row.get(col_idx).cloned().unwrap_or(DataValue::Null);
                                let key_v = pk_vals.get(i).cloned().unwrap_or(DataValue::Null);
                                row_v != key_v
                            })
                        })
                        .collect();
                    self.set_rows(&rel, kept);
                }
            }
            _ => return Err(fake_err(&format!("FakeBackend: unsupported op {op}"))),
        }
        Ok(NamedRows::new(Vec::new(), Vec::new()))
    }

    fn run_read(
        &self,
        q: &str,
        params: &BTreeMap<String, serde_json::Value>,
    ) -> Result<NamedRows, Box<dyn std::error::Error>> {
        // Parse head columns: `?[a, b, count(c)] := ...`
        let head = parse_head(q)?;
        let body = q.split_once(":=").map(|(_, b)| b).unwrap_or("");
        // Strip trailing :limit/:offset (pagination).
        let (body, limit, offset) = parse_pagination(body);

        // The relation block: `*rel[cols, ...]`.
        let (rel, pat_cols) = parse_relation_block(body)?;

        let cols = table_columns(&rel)
            .ok_or_else(|| fake_err(&format!("FakeBackend: unknown relation {rel}")))?;

        let mut rows = self.rows(&rel);

        // Filter by the relation's bound pattern columns.
        for (i, pcol) in pat_cols.iter().enumerate() {
            if i >= cols.len() {
                break;
            }
            let _actual_col = cols[i];
            rows = match pcol {
                PatCol::Var(_) | PatCol::Wildcard => rows,
                PatCol::Param(name) => {
                    let want = params.get(name).map(json_to_dv).unwrap_or(DataValue::Null);
                    rows.into_iter()
                        .filter(|r| r.get(i).cloned().unwrap_or(DataValue::Null) == want)
                        .collect()
                }
                PatCol::Const(s) => rows
                    .into_iter()
                    .filter(|r| {
                        r.get(i).cloned().unwrap_or(DataValue::Null)
                            == DataValue::Str(s.to_string())
                    })
                    .collect(),
            };
        }

        // Apply extra filters in the rule body (`qualified_name = $qn`,
        // `element_type = "file"`, `regex_matches(...)`, `(a = $x or a = $y)`,
        // `name = $nm`, `fp >= $lo and fp < $hi`).
        //
        // NOTE: the primary relation's filters are those BEFORE any join block.
        // A `*code_elements[...]` join references columns (file_path) not in
        // the primary relation, so apply_filters must only see the primary-side
        // clauses — the join block and its filters are handled separately below.
        let primary_filters = if parse_join_block(body, &rel).is_some() {
            // Filter string up to the join block: everything from the end of
            // the primary relation block to the start of the join block.
            let marker = format!("*{rel}[");
            let start = body.find(&marker).map(|i| i + marker.len()).unwrap_or(0);
            let primary_end = body[start..]
                .find(']')
                .map(|i| start + i + 1)
                .unwrap_or(body.len());
            if let Some(ji) = body.find("*code_elements[") {
                &body[primary_end..ji]
            } else {
                ""
            }
        } else {
            body_after_rel(body, &rel)
        };
        rows = apply_filters(rows, primary_filters, cols, &pat_cols, params)?;

        // Cross-relation join: `*relationships[...], *code_elements[source_qualified, ...], regex_matches(file_path, ...)`.
        // Used by clear_ontology_layer: relationships whose source is an
        // ontology element (file_path ~ "^ontology://"). Join on the first
        // column of each relation and apply remaining filters against the
        // joined relation's columns.
        if let Some((join_rel, join_pat, join_filters)) = parse_join_block(body, &rel) {
            if let Some(join_cols) = table_columns(&join_rel) {
                let joined_rows = self.rows(&join_rel);
                // Join key: the primary relation's source/target column that
                // the shared variable binds. `*relationships[source_qualified, ...]`
                // + `*code_elements[source_qualified, ...]` joins on the first
                // pattern position of each. We join on code_elements col 0
                // (qualified_name) against the SAME pattern position in the
                // primary relation.
                let join_idx = pat_cols
                    .iter()
                    .position(|p| matches!(p, PatCol::Var(v) if !v.contains('{')))
                    .unwrap_or(0);
                let kept: Vec<Vec<DataValue>> = rows
                    .into_iter()
                    .filter(|row| {
                        let key = row.get(join_idx).cloned().unwrap_or(DataValue::Null);
                        joined_rows.iter().any(|jr| {
                            let qn = jr.get(0).cloned().unwrap_or(DataValue::Null);
                            if qn != key {
                                return false;
                            }
                            apply_filters(
                                vec![jr.clone()],
                                &join_filters,
                                join_cols,
                                &join_pat,
                                params,
                            )
                            .map(|f| !f.is_empty())
                            .unwrap_or(false)
                        })
                    })
                    .collect();
                rows = kept;
            }
        }

        // Aggregate `count(x)` in the head -> group-free count of rows.
        let has_count = head.iter().any(|h| h.contains("count("));
        if has_count {
            return self.run_count(head, rows, &pat_cols);
        }

        // Project the head columns from the relation row positions. A head
        // column may be a real column name OR a pattern-variable binding
        // (`?[tgt] := *relationships[_, tgt, ...]`).
        let mut projected: Vec<Vec<DataValue>> = Vec::new();
        for row in rows {
            let mut out = Vec::new();
            for h in &head {
                let idx = resolve_col_idx(h, cols, &pat_cols);
                match idx {
                    Some(i) => out.push(row.get(i).cloned().unwrap_or(DataValue::Null)),
                    None => out.push(DataValue::Null),
                }
            }
            projected.push(out);
        }

        // Dedup (Datalog semantics: sets of rows).
        let mut seen = std::collections::HashSet::new();
        let mut deduped: Vec<Vec<DataValue>> = Vec::new();
        for row in projected {
            let key = format!("{:?}", row);
            if seen.insert(key) {
                deduped.push(row);
            }
        }
        let mut out = if offset > 0 {
            deduped.into_iter().skip(offset).collect::<Vec<_>>()
        } else {
            deduped
        };
        if let Some(limit) = limit {
            out.truncate(limit);
        }

        Ok(NamedRows::new(head.clone(), out))
    }

    fn run_count(
        &self,
        head: Vec<String>,
        rows: Vec<Vec<DataValue>>,
        pat_cols: &[PatCol],
    ) -> Result<NamedRows, Box<dyn std::error::Error>> {
        // `?[node, count(node)] := *relationships[node, _, ...]`
        // Groups by the non-count head columns and counts rows per group.
        // A head column maps to a pattern position via `pat_cols` (the bound
        // variable name in `*rel[var, _, ...]`).
        let group_cols: Vec<String> = head
            .iter()
            .filter(|h| !h.contains("count("))
            .cloned()
            .collect();

        if group_cols.is_empty() {
            return Ok(NamedRows::new(
                head.clone(),
                vec![vec![DataValue::Num(Num::Int(rows.len() as i64))]],
            ));
        }

        // Map each group head column to its pattern position.
        let idxs: Vec<usize> = group_cols
            .iter()
            .map(|h| {
                pat_cols
                    .iter()
                    .position(
                        |p| matches!(p, PatCol::Var(v) if v == h || v.starts_with(h.as_str())),
                    )
                    .unwrap_or(usize::MAX)
            })
            .collect();

        let mut groups: Vec<(Vec<DataValue>, i64)> = Vec::new();
        for row in &rows {
            let key: Vec<DataValue> = idxs
                .iter()
                .map(|&i| {
                    if i == usize::MAX {
                        DataValue::Null
                    } else {
                        row.get(i).cloned().unwrap_or(DataValue::Null)
                    }
                })
                .collect();
            if let Some(g) = groups.iter_mut().find(|(k, _)| *k == key) {
                g.1 += 1;
            } else {
                groups.push((key, 1));
            }
        }

        let out: Vec<Vec<DataValue>> = groups
            .into_iter()
            .map(|(mut k, cnt)| {
                // Replace count placeholder in head.
                k.push(DataValue::Num(Num::Int(cnt)));
                k
            })
            .collect();

        Ok(NamedRows::new(head, out))
    }
}

/// A pattern-column in a `*rel[...]` binding.
enum PatCol {
    Var(String),
    Wildcard,
    Param(String),
    Const(String),
}

/// Detect a second relation block (`*other_rel[cols, ...]`) in the rule body
/// after the primary relation. Returns (join rel name, join pat cols, filters
/// that follow the join block). Join columns pair by pattern position.
fn parse_join_block(body: &str, primary: &str) -> Option<(String, Vec<PatCol>, String)> {
    let marker = format!("*{primary}[");
    let start = body.find(&marker)? + marker.len();
    // Skip past the primary relation's closing `]`.
    let primary_end = body[start..].find(']')? + start + 1;
    let rest = &body[primary_end..];
    // Find the next `*rel[` block.
    let join_start = rest.find("*")?;
    let join_block = &rest[join_start..];
    let (rel, rest2) = join_block[1..].split_once('[')?;
    let inner_end = rest2.find(']')?;
    let inner = &rest2[..inner_end];
    let pat_cols: Vec<PatCol> = inner
        .split(',')
        .filter(|p| !p.trim().is_empty())
        .map(|p| {
            let p = p.trim();
            if p == "_" {
                PatCol::Wildcard
            } else if p.starts_with('$') {
                PatCol::Param(p[1..].to_string())
            } else {
                PatCol::Var(p.to_string())
            }
        })
        .collect();
    let filters = rest2[inner_end + 1..].to_string();
    Some((rel.to_string(), pat_cols, filters))
}

/// `*rel[qualified_name, element_type, ...]` -> (rel, pattern cols).
fn parse_relation_block(body: &str) -> Result<(String, Vec<PatCol>), Box<dyn std::error::Error>> {
    let body = body.trim();
    let (rel, rest) = body
        .strip_prefix('*')
        .and_then(|r| {
            let (rel, rest) = r.split_once('[')?;
            Some((rel.to_string(), rest))
        })
        .ok_or_else(|| fake_err("rule body must start with *relation[...]"))?;

    let inner = rest.split_once(']').map(|(i, _)| i).unwrap_or(rest);
    let mut pat_cols = Vec::new();
    // Split on commas, but keep `{tail}` and `...` markers.
    for part in inner.split(',') {
        let p = part.trim();
        if p.is_empty() {
            continue;
        }
        if p == "_" {
            pat_cols.push(PatCol::Wildcard);
        } else if p.starts_with('$') {
            pat_cols.push(PatCol::Param(p[1..].to_string()));
        } else if p.contains('{') || p.contains("...") {
            // `metadata{tail}` / `metadata{...}` — treat as var.
            pat_cols.push(PatCol::Var(p.to_string()));
        } else if is_string_literal(p) {
            pat_cols.push(PatCol::Const(unescape_str(p)));
        } else {
            pat_cols.push(PatCol::Var(p.to_string()));
        }
    }
    Ok((rel, pat_cols))
}

fn is_string_literal(s: &str) -> bool {
    s.starts_with('"') || s.starts_with('\'')
}

fn unescape_str(s: &str) -> String {
    s.trim_matches('"').trim_matches('\'').to_string()
}

fn parse_head(q: &str) -> Result<Vec<String>, Box<dyn std::error::Error>> {
    q.split_once("?[")
        .and_then(|(_, rest)| rest.split_once(']'))
        .map(|(inner, _)| {
            inner
                .split(',')
                .map(|s| s.trim().to_string())
                .filter(|s| !s.is_empty())
                .collect()
        })
        .ok_or_else(|| fake_err("read must start with ?[cols]"))
}

/// `rel { col1, col2 }` from the text after a write op.
struct RelCols {
    cols: Vec<String>,
}

fn parse_rel_and_cols(after: &str) -> Result<(String, RelCols), Box<dyn std::error::Error>> {
    let after = after.trim();
    let (rel, rest) = after
        .split_once(|c: char| c.is_whitespace())
        .map(|(r, rest)| (r.to_string(), rest))
        .unwrap_or_else(|| (after.to_string(), ""));
    let inner = rest.trim().trim_start_matches('{').trim_end_matches('}');
    let cols: Vec<String> = if inner.contains("=>") {
        inner
            .split("=>")
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
            .collect()
    } else {
        inner
            .split(',')
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
            .collect()
    };
    Ok((rel, RelCols { cols }))
}

/// Extract rows from the source before a write op:
/// `?[cols] <- $batch_data :put rel { cols }` or `?[cols] <- [[...]] :put ...`.
fn extract_write_rows(
    source: &str,
    rel: &str,
    cols: &[String],
    params: &BTreeMap<String, serde_json::Value>,
) -> Result<Vec<Vec<DataValue>>, Box<dyn std::error::Error>> {
    let arrow = source
        .find("<-")
        .ok_or_else(|| fake_err("write source missing <-"))?;
    let src = source[arrow + 2..].trim();

    // `$param` or `$batch_data`.
    if let Some(param) = src.strip_prefix('$') {
        let param = param.trim().to_string();
        let val = params
            .get(&param)
            .ok_or_else(|| fake_err(&format!("missing param ${param}")))?;
        return rows_from_json(val, cols);
    }

    // Literal `[[...]]` block or single `[...]` row, possibly with `$param`
    // references inside (`[[ $qn, $et, ... ]]`). Interpolate each `$name`
    // with its JSON value before parsing.
    if src.starts_with('[') {
        let json: serde_json::Value =
            serde_json::from_str(&strip_legacy_vec_literals(&interpolate(src, params)))
                .map_err(|e| fake_err(&format!("bad literal: {e}")))?;
        return rows_from_json(&json, cols);
    }

    Err(fake_err(&format!(
        "FakeBackend: unsupported write source: {src}"
    )))
}

/// Replace `$param` references with their JSON text (suitably quoted).
fn interpolate(src: &str, params: &BTreeMap<String, serde_json::Value>) -> String {
    let mut out = String::with_capacity(src.len());
    let chars: Vec<char> = src.chars().collect();
    let mut i = 0;
    while i < chars.len() {
        if chars[i] == '$' {
            // Read the identifier.
            let start = i + 1;
            let mut j = start;
            while j < chars.len() && (chars[j].is_alphanumeric() || chars[j] == '_') {
                j += 1;
            }
            let name: String = chars[start..j].iter().collect();
            if let Some(val) = params.get(&name) {
                let text = match val {
                    serde_json::Value::String(s) => format!("\"{}\"", s.replace('"', "\\\"")),
                    v => v.to_string(),
                };
                out.push_str(&text);
                i = j;
                continue;
            }
        }
        out.push(chars[i]);
        i += 1;
    }
    out
}

/// Strip legacy vector literals `vec([1.0, 2.0])` → `[1.0, 2.0]` so the
/// write source parses as JSON. The embeddings writer (`put_pairs_to_db_script`)
/// emits vector cells in this legacy dialect form, which is not valid JSON.
/// The inner content is a float list (no `]`), so the match is unambiguous and
/// never touches `])` sequences inside string cells (qualified names / blobs).
fn strip_legacy_vec_literals(src: &str) -> String {
    let re = regex::Regex::new(r"vec\(\[([^\]]*)\]\)").expect("valid vec-literal regex");
    re.replace_all(src, "[$1]").into_owned()
}

fn rows_from_json(
    val: &serde_json::Value,
    cols: &[String],
) -> Result<Vec<Vec<DataValue>>, Box<dyn std::error::Error>> {
    let mut out = Vec::new();
    match val {
        serde_json::Value::Array(rows) => {
            for row in rows {
                match row {
                    serde_json::Value::Array(cells) => {
                        out.push(cells.iter().map(json_to_dv).collect());
                    }
                    serde_json::Value::Object(map) => {
                        let mut r: Vec<DataValue> = Vec::with_capacity(cols.len());
                        for c in cols {
                            r.push(map.get(c).map(json_to_dv).unwrap_or(DataValue::Null));
                        }
                        out.push(r);
                    }
                    _ => return Err(fake_err("row must be array or object")),
                }
            }
        }
        serde_json::Value::Object(map) => {
            let mut r: Vec<DataValue> = Vec::with_capacity(cols.len());
            for c in cols {
                r.push(map.get(c).map(json_to_dv).unwrap_or(DataValue::Null));
            }
            out.push(r);
        }
        _ => return Err(fake_err("write source must be array or object")),
    }
    Ok(out)
}

fn json_to_dv(v: &serde_json::Value) -> DataValue {
    match v {
        serde_json::Value::Null => DataValue::Null,
        serde_json::Value::Bool(b) => DataValue::Bool(*b),
        serde_json::Value::Number(n) => {
            if let Some(i) = n.as_i64() {
                DataValue::Num(Num::Int(i))
            } else if let Some(f) = n.as_f64() {
                DataValue::Num(Num::Float(f))
            } else {
                DataValue::Null
            }
        }
        serde_json::Value::String(s) => DataValue::Str(s.clone()),
        serde_json::Value::Array(a) => DataValue::List(a.iter().map(json_to_dv).collect()),
        serde_json::Value::Object(o) => {
            DataValue::Json(serde_json::to_string(o).unwrap_or_default())
        }
    }
}

fn col_index(rel: &str, col: &str) -> Option<usize> {
    table_columns(rel)?.iter().position(|c| *c == col)
}

/// Map one in-memory `code_elements` row (positional per `table_columns`) to
/// a [`CodeElement`]. Mirrors `element_from_row` in `graph/query.rs`:
/// optional columns NULL → `None`, `metadata` parsed as JSON when present,
/// `env` selected only when `include_env` is true; missing/Null `env` cell
/// → schema default `"local"` (matches the 11-col `:put` rows from
/// `GraphEngine::insert_element`).
fn code_element_from_cells(row: &[DataValue], include_env: bool) -> crate::db::models::CodeElement {
    let text = |idx: usize| -> Option<String> {
        match row.get(idx) {
            Some(DataValue::Str(s)) => Some(s.clone()),
            Some(DataValue::Json(j)) => Some(j.clone()),
            Some(DataValue::Null) | Some(DataValue::Bot) | None => None,
            Some(other) => Some(other.to_string()),
        }
    };
    let text_or_default = |idx: usize| text(idx).unwrap_or_default();
    let int_or_zero = |idx: usize| -> i64 { row.get(idx).and_then(|v| v.get_int()).unwrap_or(0) };
    let metadata_str = match row.get(10) {
        Some(DataValue::Str(s)) => Some(s.clone()),
        Some(DataValue::Json(j)) => Some(j.clone()),
        _ => None,
    };
    let env_idx = 11;
    crate::db::models::CodeElement {
        qualified_name: text_or_default(0),
        element_type: text_or_default(1),
        name: text_or_default(2),
        file_path: text_or_default(3),
        line_start: int_or_zero(4) as u32,
        line_end: int_or_zero(5) as u32,
        language: text_or_default(6),
        parent_qualified: text(7),
        cluster_id: text(8),
        cluster_label: text(9),
        metadata: metadata_str
            .and_then(|s| serde_json::from_str(&s).ok())
            .unwrap_or(serde_json::json!({})),
        env: if include_env {
            text(env_idx).unwrap_or_else(|| "local".to_string())
        } else {
            "local".to_string()
        },
    }
}

/// Split `:limit N :offset M` off the body. Both tokens are scanned
/// independently, since `:limit 1 :offset 50000` may appear in either order.
fn parse_pagination(body: &str) -> (&str, Option<usize>, usize) {
    let mut limit = None;
    let mut offset = 0;
    if let Some(i) = body.find(":limit") {
        let after = &body[i + ":limit".len()..];
        if let Some(n) = after
            .split_whitespace()
            .next()
            .and_then(|n| n.parse::<usize>().ok())
        {
            limit = Some(n);
        }
    }
    if let Some(i) = body.find(":offset") {
        let after = &body[i + ":offset".len()..];
        if let Some(n) = after
            .split_whitespace()
            .next()
            .and_then(|n| n.parse::<usize>().ok())
        {
            offset = n;
        }
    }
    // Remove the `:limit ... :offset ...` tail from the body.
    let strip_from = body
        .find(":limit")
        .map(|i| i)
        .or_else(|| body.find(":offset"))
        .unwrap_or(body.len());
    (body[..strip_from].trim_end(), limit, offset)
}

/// The rule-body filter clauses after the relation block. We take everything
/// after the closing `]` of `*rel[...]`, minus any trailing `:limit/:offset`.
fn body_after_rel<'a>(body: &'a str, rel: &str) -> &'a str {
    // Find `*rel[` ... `]` then take the rest.
    if let Some(start) = body.find(&format!("*{rel}[")) {
        let after_open = &body[start + rel.len() + 2..];
        if let Some(close) = after_open.find(']') {
            return &after_open[close + 1..];
        }
    }
    ""
}

/// Apply comma-separated filter clauses: `qualified_name = $qn`, `name = $nm`,
/// `element_type = "file"`, `(a = $x or b = $y)`, `regex_matches(col, $pat)`,
/// `lowercase(desc) regex_matches(...)`, `fp >= $lo and fp < $hi`.
/// Resolve a column reference in a filter clause to a row index. Prefers the
/// relation's real column names; falls back to pattern-variable names (`rel`,
/// `tgt`, `et`, `fp`, ...) which bind positionally.
fn resolve_col_idx(col: &str, cols: &[&str], pat_cols: &[PatCol]) -> Option<usize> {
    if let Some(i) = cols.iter().position(|c| *c == col) {
        return Some(i);
    }
    // Pattern variables: `rel` in `*relationships[_, tgt, rel, ...]` sits at
    // the position where `PatCol::Var("rel")` appears.
    pat_cols
        .iter()
        .position(|p| matches!(p, PatCol::Var(v) if v == col || v.strip_prefix(col).is_some() || v.starts_with(col)))
}

fn apply_filters(
    rows: Vec<Vec<DataValue>>,
    filters: &str,
    cols: &[&str],
    pat_cols: &[PatCol],
    params: &BTreeMap<String, serde_json::Value>,
) -> Result<Vec<Vec<DataValue>>, Box<dyn std::error::Error>> {
    let mut out = rows;
    for clause in split_top_level_commas(filters) {
        let c = clause.trim();
        if c.is_empty() || c.starts_with(":") {
            continue;
        }

        // `or` group: `(a = $x or b = $y)` / `str_includes(...) or ...` /
        // `(file_path = "x" or regex_matches(...) or regex_matches(...))`.
        if c.contains(" or ") {
            // Only trim the outer parens when the WHOLE clause is parenthesized.
            let inner = if c.starts_with('(') && c.ends_with(')') {
                c[1..c.len().saturating_sub(1)].trim()
            } else {
                c
            };
            out = out
                .into_iter()
                .filter(|row| {
                    inner.split(" or ").any(|alt| {
                        let alt = alt.trim();
                        if alt.contains("str_includes") {
                            str_includes_match(row, alt, cols, pat_cols).unwrap_or(false)
                        } else if alt.contains("regex_matches") {
                            let (col, pat, lower) = parse_regex_clause(alt, params)
                                .unwrap_or_else(|_| ("".into(), "".into(), false));
                            if col.is_empty() {
                                return false;
                            }
                            let col_idx =
                                resolve_col_idx(&col, cols, pat_cols).unwrap_or(usize::MAX);
                            if col_idx == usize::MAX {
                                return false;
                            }
                            let regex = regex::Regex::new(&pat).map_err(|_| ()).ok();
                            match regex {
                                Some(re) => row
                                    .get(col_idx)
                                    .and_then(|v| v.get_str())
                                    .map(|s| {
                                        let s = if lower {
                                            s.to_lowercase()
                                        } else {
                                            s.to_string()
                                        };
                                        re.is_match(&s)
                                    })
                                    .unwrap_or(false),
                                None => false,
                            }
                        } else {
                            let r =
                                eval_equality(row, alt, cols, pat_cols, params).unwrap_or(false);
                            r
                        }
                    })
                })
                .collect();
            continue;
        }

        // Negation: `!regex_matches(col, "pat")`.
        if c.starts_with('!') {
            let rest = c[1..].trim_start();
            if rest.contains("regex_matches") {
                let (col_name, pat, _lower) = parse_regex_clause(rest, params)?;
                let col_idx = resolve_col_idx(&col_name, cols, pat_cols)
                    .ok_or_else(|| fake_err(&format!("negated regex col {col_name} not found")))?;
                let regex = regex::Regex::new(&pat)
                    .map_err(|e| fake_err(&format!("bad regex {pat}: {e}")))?;
                out = out
                    .into_iter()
                    .filter(|row| {
                        !row.get(col_idx)
                            .and_then(|v| v.get_str())
                            .map(|s| regex.is_match(s))
                            .unwrap_or(false)
                    })
                    .collect();
                continue;
            }
        }

        // `str_includes(lowercase(col), "pat")` — case-insensitive substring.
        if c.contains("str_includes") {
            out = out
                .into_iter()
                .filter(|row| str_includes_match(row, c, cols, pat_cols).unwrap_or(false))
                .collect();
            continue;
        }

        // `a = $x and b = $y` chains.
        if c.contains(" and ") {
            let mut keep = out;
            for part in c.split(" and ") {
                let part = part.trim();
                keep = keep
                    .into_iter()
                    .filter(|row| eval_equality(row, part, cols, pat_cols, params).unwrap_or(false))
                    .collect();
            }
            out = keep;
            continue;
        }

        // `regex_matches(...)` / `regex_matches(lowercase(col), ...)`.
        if c.contains("regex_matches") {
            let (col_name, pat, lower) = parse_regex_clause(c, params)?;
            let col_idx = resolve_col_idx(&col_name, cols, pat_cols)
                .ok_or_else(|| fake_err(&format!("regex col {col_name} not found")))?;
            let regex =
                regex::Regex::new(&pat).map_err(|e| fake_err(&format!("bad regex {pat}: {e}")))?;
            out = out
                .into_iter()
                .filter(|row| {
                    row.get(col_idx)
                        .and_then(|v| v.get_str())
                        .map(|s| {
                            let s = if lower {
                                s.to_lowercase()
                            } else {
                                s.to_string()
                            };
                            regex.is_match(&s)
                        })
                        .unwrap_or(false)
                })
                .collect();
            continue;
        }

        // Simple equality `col = $param` / `col = "const"` / range `col >= $lo`.
        if let Some(keep) = eval_simple(out.clone(), c, cols, pat_cols, params)? {
            out = keep;
        }
    }
    Ok(out)
}

/// `str_includes(lowercase(col), "sub")` / `str_includes(col, "sub")` —
/// case-insensitive substring match.
fn str_includes_match(
    row: &[DataValue],
    c: &str,
    cols: &[&str],
    pat_cols: &[PatCol],
) -> Result<bool, Box<dyn std::error::Error>> {
    let (open, close) = c
        .find('(')
        .zip(c.rfind(')'))
        .ok_or_else(|| fake_err("str_includes missing ()"))?;
    let inner = &c[open + 1..close];
    let (col, pat) = inner
        .split_once(',')
        .ok_or_else(|| fake_err("str_includes needs (col, pat)"))?;
    let col = col.trim();
    let (col, lower) = if let Some(c) = col.strip_prefix("lowercase(") {
        (c.trim_end_matches(')').trim(), true)
    } else {
        (col, false)
    };
    let pat = unescape_str(pat.trim()).to_lowercase();
    let col_idx = resolve_col_idx(col, cols, pat_cols)
        .ok_or_else(|| fake_err(&format!("str_includes col {col} not found")))?;
    Ok(row
        .get(col_idx)
        .and_then(|v| v.get_str())
        .map(|s| {
            let s = if lower {
                s.to_lowercase()
            } else {
                s.to_string()
            };
            s.contains(&pat)
        })
        .unwrap_or(false))
}

/// Split a filter string on commas that are NOT inside parentheses/brackets
/// (so `regex_matches(lowercase(name), "a,b")` stays one clause).
fn split_top_level_commas(s: &str) -> Vec<&str> {
    let mut out = Vec::new();
    let mut depth = 0i32;
    let mut start = 0;
    for (i, ch) in s.char_indices() {
        match ch {
            '(' | '[' => depth += 1,
            ')' | ']' => depth -= 1,
            ',' if depth == 0 => {
                out.push(&s[start..i]);
                start = i + 1;
            }
            _ => {}
        }
    }
    out.push(&s[start..]);
    out
}

fn eval_equality(
    row: &[DataValue],
    clause: &str,
    cols: &[&str],
    pat_cols: &[PatCol],
    params: &BTreeMap<String, serde_json::Value>,
) -> Result<bool, Box<dyn std::error::Error>> {
    if let Some(keep) = eval_simple(vec![row.to_vec()], clause, cols, pat_cols, params)? {
        Ok(!keep.is_empty())
    } else {
        Ok(false)
    }
}

/// `col = $param` / `col = "const"` / `col >= $lo and col < $hi` — returns
/// filtered rows, or None if the clause is not a supported equality.
fn eval_simple(
    rows: Vec<Vec<DataValue>>,
    clause: &str,
    cols: &[&str],
    pat_cols: &[PatCol],
    params: &BTreeMap<String, serde_json::Value>,
) -> Result<Option<Vec<Vec<DataValue>>>, Box<dyn std::error::Error>> {
    let c = clause.trim();

    // `col in $param_array` or `col in ["a", "b"]` — membership filter.
    if let Some((col, items)) = c.split_once(" in ") {
        let col = col.trim();
        let col_idx = resolve_col_idx(col, cols, pat_cols);
        let Some(col_idx) = col_idx else {
            return Ok(None);
        };
        let values: Vec<DataValue> = match items.trim() {
            s if s.starts_with('$') => {
                let p = s.strip_prefix('$').unwrap().trim();
                params
                    .get(p)
                    .map(|v| match v {
                        serde_json::Value::Array(a) => a.iter().map(json_to_dv).collect(),
                        other => vec![json_to_dv(other)],
                    })
                    .unwrap_or_default()
            }
            s if s.starts_with('[') => {
                // `["function", "method", ...]` — parse as a JSON array.
                serde_json::from_str::<serde_json::Value>(s)
                    .ok()
                    .and_then(|v| v.as_array().cloned())
                    .map(|a| a.iter().map(json_to_dv).collect())
                    .unwrap_or_default()
            }
            _ => return Ok(None),
        };
        let filtered: Vec<Vec<DataValue>> = rows
            .into_iter()
            .filter(|row| {
                let v = row.get(col_idx).cloned().unwrap_or(DataValue::Null);
                values.contains(&v)
            })
            .collect();
        return Ok(Some(filtered));
    }

    // `col != $param` / `col != "const"` — negated equality.
    if let Some((col, op)) = c.split_once("!=") {
        let col = col.trim();
        let col_idx = resolve_col_idx(col, cols, pat_cols);
        let Some(col_idx) = col_idx else {
            return Ok(None);
        };
        let op = op.trim();
        let want = if let Some(param) = op.strip_prefix('$') {
            params
                .get(param.trim())
                .map(json_to_dv)
                .unwrap_or(DataValue::Null)
        } else if is_string_literal(op) {
            DataValue::Str(unescape_str(op))
        } else if let Ok(i) = op.parse::<i64>() {
            DataValue::Num(Num::Int(i))
        } else {
            return Ok(None);
        };
        let filtered: Vec<Vec<DataValue>> = rows
            .into_iter()
            .filter(|row| {
                let v = row.get(col_idx).cloned().unwrap_or(DataValue::Null);
                v != want
            })
            .collect();
        return Ok(Some(filtered));
    }

    // Computed expression: `(line_end - line_start) >= 19` or
    // `span = line_end - line_start:order -span` (head alias). For the
    // arithmetic comparison we resolve the two operands to row columns.
    if c.starts_with('(') && c.contains(") >=") {
        let inner = &c[1..c.find(')').unwrap()];
        let rhs = c.split(") >=").nth(1).unwrap_or("").trim();
        if let Some((a, b)) = inner.split_once('-') {
            let a = a.trim();
            let b = b.trim();
            let a_idx = resolve_col_idx(a, cols, pat_cols);
            let b_idx = resolve_col_idx(b, cols, pat_cols);
            let want: i64 = rhs.parse().unwrap_or(i64::MAX);
            if let (Some(ai), Some(bi)) = (a_idx, b_idx) {
                let filtered: Vec<Vec<DataValue>> = rows
                    .into_iter()
                    .filter(|row| {
                        let av = row.get(ai).and_then(|v| v.get_int()).unwrap_or(0);
                        let bv = row.get(bi).and_then(|v| v.get_int()).unwrap_or(0);
                        av - bv >= want
                    })
                    .collect();
                return Ok(Some(filtered));
            }
        }
    }

    let (col, op) = if let Some((col, op)) = split_op(c, "==") {
        (col.trim(), op.trim())
    } else if let Some((col, op)) = split_op(c, "=") {
        (col.trim(), op.trim())
    } else if let Some((col, op)) = split_op(c, ">=") {
        (col.trim(), op.trim())
    } else if let Some((col, op)) = split_op(c, "<") {
        (col.trim(), op.trim())
    } else if let Some((col, op)) = split_op(c, ">") {
        (col.trim(), op.trim())
    } else {
        return Ok(None);
    };

    let col_idx = resolve_col_idx(col, cols, pat_cols);
    let Some(col_idx) = col_idx else {
        return Ok(None); // unknown column — not a filter we can apply
    };

    let want = if let Some(param) = op.strip_prefix('$') {
        params
            .get(param.trim())
            .map(json_to_dv)
            .unwrap_or(DataValue::Null)
    } else if is_string_literal(op) {
        DataValue::Str(unescape_str(op))
    } else if let Ok(i) = op.parse::<i64>() {
        DataValue::Num(Num::Int(i))
    } else if let Ok(f) = op.parse::<f64>() {
        DataValue::Num(Num::Float(f))
    } else {
        return Ok(None);
    };

    let filtered: Vec<Vec<DataValue>> = rows
        .into_iter()
        .filter(|row| {
            let v = row.get(col_idx).cloned().unwrap_or(DataValue::Null);
            if op.starts_with(">=") {
                cmp_ge(&v, &want)
            } else if op.starts_with('<') {
                cmp_lt(&v, &want)
            } else if op.starts_with('>') {
                cmp_gt(&v, &want)
            } else {
                v == want
            }
        })
        .collect();

    Ok(Some(filtered))
}

fn split_op<'a>(s: &'a str, op: &str) -> Option<(&'a str, &'a str)> {
    s.split_once(op)
}

fn cmp_ge(a: &DataValue, b: &DataValue) -> bool {
    use std::cmp::Ordering;
    match (a, b) {
        (DataValue::Num(x), DataValue::Num(y)) => x.cmp(y) != Ordering::Less,
        (DataValue::Str(x), DataValue::Str(y)) => x >= y,
        _ => false,
    }
}

fn cmp_gt(a: &DataValue, b: &DataValue) -> bool {
    use std::cmp::Ordering;
    match (a, b) {
        (DataValue::Num(x), DataValue::Num(y)) => x.cmp(y) == Ordering::Greater,
        (DataValue::Str(x), DataValue::Str(y)) => x > y,
        _ => false,
    }
}

fn cmp_lt(a: &DataValue, b: &DataValue) -> bool {
    use std::cmp::Ordering;
    match (a, b) {
        (DataValue::Num(x), DataValue::Num(y)) => x.cmp(y) == Ordering::Less,
        (DataValue::Str(x), DataValue::Str(y)) => x < y,
        _ => false,
    }
}

/// `regex_matches(col, $pat)` / `regex_matches(col, "literal")` /
/// `regex_matches(lowercase(col), ...)`. Returns (col, pattern, lowercase?).
fn parse_regex_clause(
    c: &str,
    params: &BTreeMap<String, serde_json::Value>,
) -> Result<(String, String, bool), Box<dyn std::error::Error>> {
    // Match the outer `regex_matches( ... )` — the inner pattern may contain
    // parens (`lowercase(name)`), so take everything up to the LAST `)`.
    let (open, close) = c
        .find('(')
        .zip(c.rfind(')'))
        .ok_or_else(|| fake_err("regex_matches missing ()"))?;
    let inner = &c[open + 1..close];
    let (col, pat) = inner
        .split_once(',')
        .ok_or_else(|| fake_err("regex_matches needs (col, pat)"))?;
    let col = col.trim().to_string();
    let (col, lower) = if let Some(inner_col) = col.strip_prefix("lowercase(") {
        (inner_col.trim_end_matches(')').trim().to_string(), true)
    } else {
        (col, false)
    };
    let pat = pat.trim();
    let pat = if let Some(p) = pat.strip_prefix('$') {
        params
            .get(p.trim())
            .and_then(|v| v.as_str())
            .ok_or_else(|| fake_err(&format!("missing regex param ${p}")))?
            .to_string()
    } else if is_string_literal(pat) {
        unescape_str(pat)
    } else {
        pat.to_string()
    };
    Ok((col, pat, lower))
}

#[cfg(test)]
mod audit_query_tests {
    use super::*;
    use crate::audit::{chain_records, AuditRecord, GENESIS_HASH};
    use std::time::{Duration, UNIX_EPOCH};

    fn ts(ms: u64) -> std::time::SystemTime {
        UNIX_EPOCH + Duration::from_millis(ms)
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

    #[test]
    fn query_audit_returns_inserted_rows_in_insertion_order() {
        let b = FakeBackend::new();
        let entries = chain_records(
            &[rec(100, "t1"), rec(200, "t2"), rec(300, "t3")],
            GENESIS_HASH,
        );
        b.insert_audit_batch(&entries).unwrap();
        let got = b.query_audit(None, None).unwrap();
        assert_eq!(got.len(), 3);
        assert_eq!(got[0].tool, "t1");
        assert_eq!(got[1].tool, "t2");
        assert_eq!(got[2].tool, "t3");
    }

    #[test]
    fn query_audit_filters_by_inclusive_ts_window() {
        let b = FakeBackend::new();
        let entries = chain_records(
            &[rec(100, "t1"), rec(200, "t2"), rec(300, "t3")],
            GENESIS_HASH,
        );
        b.insert_audit_batch(&entries).unwrap();

        // since=150 until=250 → only t2.
        let got = b.query_audit(Some(ts(150)), Some(ts(250))).unwrap();
        assert_eq!(
            got.iter().map(|e| e.tool.as_str()).collect::<Vec<_>>(),
            ["t2"]
        );

        // Inclusive bounds: since=200 until=200 still yields t2.
        let got = b.query_audit(Some(ts(200)), Some(ts(200))).unwrap();
        assert_eq!(
            got.iter().map(|e| e.tool.as_str()).collect::<Vec<_>>(),
            ["t2"]
        );

        // Open-ended lower bound: until=150 → t1 only.
        let got = b.query_audit(None, Some(ts(150))).unwrap();
        assert_eq!(
            got.iter().map(|e| e.tool.as_str()).collect::<Vec<_>>(),
            ["t1"]
        );

        // Open-ended upper bound: since=300 → t3 only.
        let got = b.query_audit(Some(ts(300)), None).unwrap();
        assert_eq!(
            got.iter().map(|e| e.tool.as_str()).collect::<Vec<_>>(),
            ["t3"]
        );
    }

    #[test]
    fn query_audit_on_empty_ledger_is_empty_ok() {
        let b = FakeBackend::new();
        assert!(b.query_audit(None, None).unwrap().is_empty());
    }
}
