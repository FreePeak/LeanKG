//! `embedding_state` CozoDB table and helpers.
//!
//! Tracks per-CodeElement embedding freshness so `embed` runs incrementally.
//! See plan §"Incremental embedding & staleness".
//!
//! Lifecycle:
//! 1. `index` upserts CodeElements, then calls `mark_stale_for_qualified_names`
//!    on every touched qualified_name. Existing rows flip to `state="stale"`;
//!    new rows get a placeholder (`content_hash=""`, `state="stale"`).
//! 2. `embed` queries for rows where `state != "fresh"` OR `content_hash` no
//!    longer matches the current blob, embeds them, and calls `upsert_fresh`.
//! 3. `embed` also reaps orphans: state rows whose qualified_name is no longer
//!    in `code_elements`. Their usearch vectors are removed and the state row
//!    is deleted.

use crate::db::schema::CozoDb;

const CREATE_EMBEDDING_STATE: &str =
    r#":create embedding_state {qualified_name: String => usearch_key: Int, content_hash: String, state: String, embedded_at: String}"#;

const CREATE_QN_INDEX: &str =
    r#"::index create embedding_state:qn_index { qualified_name }"#;

const CREATE_KEY_INDEX: &str =
    r#"::index create embedding_state:usearch_key_index { usearch_key }"#;

const CREATE_STATE_INDEX: &str =
    r#"::index create embedding_state:state_index { state }"#;

#[derive(Debug, Clone)]
pub struct EmbeddingStateRow {
    pub qualified_name: String,
    /// Stored in CozoDB as i64; cast to u64 when feeding usearch. Bit pattern
    /// is preserved across the cast.
    pub usearch_key: i64,
    pub content_hash: String,
    pub state: String,
    pub embedded_at: String,
}

/// Idempotently create the `embedding_state` table. Called from `init_schema`
/// on every DB open, so it must be cheap when the table already exists.
pub fn ensure_embedding_state_table(db: &CozoDb) -> Result<(), Box<dyn std::error::Error>> {
    let existing: std::collections::HashSet<String> = crate::db::schema::run_script(db, "::relations", Default::default())
        .map(|r| {
            r.rows
                .iter()
                .filter_map(|row| row.first().and_then(|v| v.get_str().map(String::from)))
                .collect()
        })
        .unwrap_or_default();

    if !existing.contains("embedding_state") {
        crate::db::schema::run_script(db, CREATE_EMBEDDING_STATE, Default::default())?;
        for idx in &[CREATE_QN_INDEX, CREATE_KEY_INDEX, CREATE_STATE_INDEX] {
            if let Err(e) = crate::db::schema::run_script(db, idx, Default::default()) {
                tracing::debug!("embedding_state index note: {:?}", e);
            }
        }
        tracing::info!("created embedding_state table");
    }
    Ok(())
}

/// Mark a batch of qualified_names as stale. Idempotent: rows that already
/// exist flip to `state="stale"`; rows that don't exist are inserted with a
/// placeholder (`content_hash=""`) so the next `embed` run picks them up.
///
/// `usearch_key` is computed deterministically from each qualified_name and
/// stored even on first insert, so the embed step can lookup the key without
/// recomputing.
pub fn mark_stale_for_qualified_names(
    db: &CozoDb,
    qualified_names: &[String],
) -> Result<(), Box<dyn std::error::Error>> {
    if qualified_names.is_empty() {
        return Ok(());
    }
    let now = now_iso();
    for chunk in qualified_names.chunks(UPSERT_CHUNK) {
        let rows: Vec<String> = chunk
            .iter()
            .map(|qn| {
                let key_i64 = crate::embeddings::text_blob::usearch_key_for(qn) as i64;
                format!(
                    "[{}, {}, {}, {}, {}]",
                    serde_json::Value::String(qn.clone()),
                    serde_json::Value::Number(key_i64.into()),
                    serde_json::Value::String("".to_string()),
                    serde_json::Value::String("stale".to_string()),
                    serde_json::Value::String(now.clone()),
                )
            })
            .collect();
        let values_clause = rows.join(", ");

        let query = format!(
            r#"?[qualified_name, usearch_key, content_hash, state, embedded_at] <- [{values_clause}]
               :put embedding_state {{qualified_name, usearch_key, content_hash, state, embedded_at}}"#
        );
        crate::db::schema::run_script(db, &query, Default::default())?;
    }
    Ok(())
}

/// Return every row whose `state != "fresh"`. Includes newly-inserted
/// placeholders (state="stale", content_hash="") and existing rows that were
/// re-touched by the indexer.
pub fn list_stale(db: &CozoDb) -> Result<Vec<EmbeddingStateRow>, Box<dyn std::error::Error>> {
    let query =
        r#"?[qualified_name, usearch_key, content_hash, state, embedded_at] := *embedding_state[qualified_name, usearch_key, content_hash, state, embedded_at], state != "fresh""#;
    let result = crate::db::schema::run_script(db, query, Default::default())?;
    Ok(result
        .rows
        .iter()
        .filter_map(row_to_state_row)
        .collect())
}

/// Return every state row whose qualified_name no longer exists in
/// `code_elements`. The embed step reaps these (removes the vector from
/// usearch and deletes the state row).
pub fn list_orphans(db: &CozoDb) -> Result<Vec<EmbeddingStateRow>, Box<dyn std::error::Error>> {
    let query = r#"
        ?[qualified_name, usearch_key, content_hash, state, embedded_at] :=
            *embedding_state[qualified_name, usearch_key, content_hash, state, embedded_at],
            not *code_elements[qualified_name, _, _, _, _, _, _, _, _, _, _, _, _]
    "#;
    let result = crate::db::schema::run_script(db, query, Default::default())?;
    Ok(result
        .rows
        .iter()
        .filter_map(row_to_state_row)
        .collect())
}

/// Return all state rows. Used by `embed --full` to re-embed every existing
/// vector.
pub fn list_all(db: &CozoDb) -> Result<Vec<EmbeddingStateRow>, Box<dyn std::error::Error>> {
    let query =
        r#"?[qualified_name, usearch_key, content_hash, state, embedded_at] := *embedding_state[qualified_name, usearch_key, content_hash, state, embedded_at]"#;
    let result = crate::db::schema::run_script(db, query, Default::default())?;
    Ok(result
        .rows
        .iter()
        .filter_map(row_to_state_row)
        .collect())
}

/// Lookup the usearch key for a single qualified_name. Returns None if the
/// row is missing (e.g., the element was never indexed).
pub fn lookup_usearch_key(
    db: &CozoDb,
    qualified_name: &str,
) -> Result<Option<u64>, Box<dyn std::error::Error>> {
    let query =
        r#"?[usearch_key] := *embedding_state[qualified_name, usearch_key, _, _, _], qualified_name = $qn"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "qn".to_string(),
        serde_json::Value::String(qualified_name.to_string()),
    );
    let result = crate::db::schema::run_script(db, query, params)?;
    Ok(result
        .rows
        .first()
        .and_then(|row| row.first())
        .and_then(|v| v.get_int())
        .map(|i| i as u64))
}

/// Maximum number of rows to inline into a single CozoDB `<~ [...]` literal.
/// CozoDB's pest grammar parser recurses on large literals and can blow the
/// stack or hit internal limits on thousand-row repos; chunking keeps each
/// statement bounded.
const UPSERT_CHUNK: usize = 500;

/// Batch upsert: mark rows fresh and stamp their content_hash + embedded_at.
/// Called by the embed step after vectors land in usearch.
pub fn upsert_fresh(
    db: &CozoDb,
    updates: &[FreshRow],
) -> Result<(), Box<dyn std::error::Error>> {
    if updates.is_empty() {
        return Ok(());
    }
    let now = now_iso();
    for chunk in updates.chunks(UPSERT_CHUNK) {
        let rows: Vec<String> = chunk
            .iter()
            .map(|u| {
                let key_i64 = u.usearch_key as i64;
                format!(
                    "[{}, {}, {}, {}, {}]",
                    serde_json::Value::String(u.qualified_name.clone()),
                    serde_json::Value::Number(key_i64.into()),
                    serde_json::Value::String(u.content_hash.clone()),
                    serde_json::Value::String("fresh".to_string()),
                    serde_json::Value::String(now.clone()),
                )
            })
            .collect();
        let values_clause = rows.join(", ");
        let query = format!(
            r#"?[qualified_name, usearch_key, content_hash, state, embedded_at] <- [{values_clause}]
               :put embedding_state {{qualified_name, usearch_key, content_hash, state, embedded_at}}"#
        );
        crate::db::schema::run_script(db, &query, Default::default())?;
    }
    Ok(())
}

/// Delete state rows for a set of orphan qualified_names. Called after the
/// embed step removes orphan vectors. With the CozoDB 0.7.x schema
/// (`qualified_name: String => ...`), only the key column is needed for `:rm`.
pub fn delete_state_rows(
    db: &CozoDb,
    rows: &[EmbeddingStateRow],
) -> Result<(), Box<dyn std::error::Error>> {
    if rows.is_empty() {
        return Ok(());
    }
    for chunk in rows.chunks(UPSERT_CHUNK) {
        let literals: Vec<String> = chunk
            .iter()
            .map(|r| format!("[{}]", serde_json::Value::String(r.qualified_name.clone())))
            .collect();
        let values_clause = literals.join(", ");
        let query = format!(
            r#"?[qualified_name] <- [{values_clause}] :rm embedding_state {{qualified_name}}"#
        );
        crate::db::schema::run_script(db, &query, Default::default())?;
    }
    Ok(())
}

/// Count of fresh vs stale rows, for diagnostics.
pub fn count_by_state(db: &CozoDb) -> Result<StateCounts, Box<dyn std::error::Error>> {
    // Aggregate in Rust — CozoDB 0.7.x has stricter handling of underscore
    // bindings and `count()` placement that makes the inline aggregation fragile.
    let all = list_all(db)?;
    let mut counts = StateCounts::default();
    for row in all {
        match row.state.as_str() {
            "fresh" => counts.fresh += 1,
            "stale" => counts.stale += 1,
            _ => counts.other += 1,
        }
    }
    Ok(counts)
}

#[derive(Debug, Clone, Default)]
pub struct StateCounts {
    pub fresh: usize,
    pub stale: usize,
    pub other: usize,
}

#[derive(Debug, Clone)]
pub struct FreshRow {
    pub qualified_name: String,
    pub usearch_key: u64,
    pub content_hash: String,
}

fn row_to_state_row(row: &Vec<cozo::DataValue>) -> Option<EmbeddingStateRow> {
    let qualified_name = row.first()?.get_str()?.to_string();
    let usearch_key = row.get(1)?.get_int()?;
    let content_hash = row.get(2)?.get_str()?.to_string();
    let state = row.get(3)?.get_str()?.to_string();
    let embedded_at = row.get(4)?.get_str()?.to_string();
    Some(EmbeddingStateRow {
        qualified_name,
        usearch_key,
        content_hash,
        state,
        embedded_at,
    })
}

fn now_iso() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    let secs = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    format!("{}", secs)
}

#[cfg(test)]
mod tests {
    // Integration tests live in /tests; these are unit-level guards for the
    // SQL builders. The state helpers themselves require a live CozoDB and
    // are exercised by tests/embeddings_state_e2e.rs (added in Phase 6).
}
