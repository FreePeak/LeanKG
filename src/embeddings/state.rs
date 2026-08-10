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
//!
//! FR-HNSW-B (v3.6.2): `embedding_vectors:vec_idx` is the **canonical ANN**
//! for all discovery. LeanKG does not run a parallel ANN stack (no
//! `Cozo ::lsh`, no custom LSH — those were removed in FR-HNSW-A). Any
//! new feature that needs "find similar X by meaning" must route through
//! `~embedding_vectors:vec_idx` plus cross-encoder rerank
//! (`src/retrieval/pipeline.rs::SemanticRetrievalPipeline`).

const CREATE_EMBEDDING_STATE: &str = r#":create embedding_state {qualified_name: String => usearch_key: Int, content_hash: String, state: String, embedded_at: String}"#;

const CREATE_QN_INDEX: &str = r#"::index create embedding_state:qn_index { qualified_name }"#;

const CREATE_KEY_INDEX: &str =
    r#"::index create embedding_state:usearch_key_index { usearch_key }"#;

const CREATE_STATE_INDEX: &str = r#"::index create embedding_state:state_index { state }"#;

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

/// Idempotently create the `embedding_state` table and the `embedding_vectors`
/// relation + HNSW index. Called from `init_schema` on every DB open, so it
/// must be cheap when both already exist.
pub fn ensure_embedding_state_table(
    db: &dyn crate::db::backend::DbBackend,
) -> Result<(), Box<dyn std::error::Error>> {
    let existing: std::collections::HashSet<String> = db
        .run_script("::relations", Default::default())
        .map(|r| {
            r.rows
                .iter()
                .filter_map(|row| row.first().and_then(|v| v.get_str().map(String::from)))
                .collect()
        })
        .unwrap_or_default();

    if !existing.contains("embedding_state") {
        db.run_script(CREATE_EMBEDDING_STATE, Default::default())?;
        for idx in &[CREATE_QN_INDEX, CREATE_KEY_INDEX, CREATE_STATE_INDEX] {
            if let Err(e) = db.run_script(idx, Default::default()) {
                tracing::debug!("embedding_state index note: {:?}", e);
            }
        }
        tracing::info!("created embedding_state table");
    }

    // HNSW-backed vector store. qualified_name is the only key (=> separator),
    // so :put acts as upsert and `:rm embedding_vectors {qualified_name}` is
    // sufficient for deletes. The HNSW index uses Cosine distance + f32 (the
    // default fastembed output type for BGE-small-en-v1.5, 384-dim).
    if !existing.contains("embedding_vectors") {
        db.run_script(CREATE_EMBEDDING_VECTORS, Default::default())?;
        tracing::info!("created embedding_vectors relation");
    }
    // Check the index separately — earlier runs may have created the relation
    // but failed silently on HNSW (e.g., the index create is not idempotent
    // and gets skipped if the relation check is coupled to it).
    if !existing.contains("embedding_vectors:vec_idx") {
        let hnsw_create = build_hnsw_create_stmt();
        match db.run_script(&hnsw_create, Default::default()) {
            Ok(_) => tracing::info!("created HNSW index embedding_vectors:vec_idx"),
            Err(e) => tracing::warn!(
                "failed to create HNSW index on embedding_vectors (query len={}): {:?}",
                hnsw_create.len(),
                e
            ),
        }
    }

    Ok(())
}

const CREATE_EMBEDDING_VECTORS: &str =
    r#":create embedding_vectors {qualified_name: String => vector: <F32; 384>}"#;

/// Drop the HNSW index on `embedding_vectors:vec_idx` so a bulk insert can
/// proceed without paying the per-vector HNSW update cost. The CozoDB
/// `::hnsw` operator is idempotent for `drop` — if the index is missing
/// the call is a no-op, which is the only error path we swallow here.
pub fn drop_hnsw_index(
    db: &dyn crate::db::backend::DbBackend,
) -> Result<(), Box<dyn std::error::Error>> {
    // LEANKG_EMBED_KEEP_HNSW=1 skips the drop so a bulk embed keeps the index
    // live. Rationale: dropping the index then letting the PG pool re-create
    // it on every connection (`ensure_embedding_state_table`) makes N
    // concurrent `CREATE INDEX IF NOT EXISTS` build the full HNSW graph and
    // deadlock the writer at ~170k rows. Keeping the index pays per-insert
    // HNSW maintenance but avoids the rebuild race entirely.
    if std::env::var("LEANKG_EMBED_KEEP_HNSW")
        .map(|v| matches!(v.as_str(), "1" | "true" | "on"))
        .unwrap_or(false)
    {
        return Ok(());
    }
    let _ = db.run_script("::hnsw drop embedding_vectors:vec_idx", Default::default());
    Ok(())
}

/// Recreate the HNSW index on `embedding_vectors:vec_idx` after a bulk
/// insert. Reads `LEANKG_HNSW_M` / `LEANKG_HNSW_EF_CONST` (see
/// `build_hnsw_create_stmt`) and returns the index to a queryable state.
pub fn create_hnsw_index(
    db: &dyn crate::db::backend::DbBackend,
) -> Result<(), Box<dyn std::error::Error>> {
    let stmt = build_hnsw_create_stmt();
    db.run_script(&stmt, Default::default())?;
    Ok(())
}

// FR-HNSW-F mega-graph HNSW knobs (build-time).
//
// `m` (max connections per node) and `ef_construction` are baked into the
// `::hnsw create` statement built by `build_hnsw_create_stmt` and cannot
// be retuned without re-creating the index. For a one-shot re-tune on a
// mega-graph workspace, delete the `.leankg` directory and re-run
// `leankg index` + `leankg embed`. The defaults target a recall/footprint
// sweet spot for ≤ 1M vectors; raise `m` for higher recall at the cost of
// RAM, or lower it for tighter RSS on memory-bound containers.
//
// `ef` (search-time, query-side) lives in `src/retrieval/pipeline.rs` and
// is overridable via the `LEANKG_HNSW_EF` env var without re-indexing.
fn build_hnsw_create_stmt() -> String {
    let m = std::env::var("LEANKG_HNSW_M")
        .ok()
        .and_then(|v| v.parse::<usize>().ok())
        .filter(|v| (4..=256).contains(v))
        .unwrap_or(50);
    let ef_construction = std::env::var("LEANKG_HNSW_EF_CONST")
        .ok()
        .and_then(|v| v.parse::<usize>().ok())
        .filter(|v| (1..=2000).contains(v))
        .unwrap_or(20)
        // pgvector requires ef_construction >= 2*m (CozoDB accepted any pair).
        .max(2 * m);
    format!(
        r#"::hnsw create embedding_vectors:vec_idx {{
    dim: 384,
    dtype: F32,
    fields: [vector],
    distance: Cosine,
    ef_construction: {ef_construction},
    m: {m},
    extend_candidates: false,
    keep_pruned_connections: false
}}"#
    )
}

/// Mark rows stale only when embed content actually changed.
///
/// FR-EMBED-RESUME-04: a no-op / identical-content full index must **not**
/// force a full re-embed. Rows that are already `fresh` with a matching
/// `content_hash` are left untouched.
///
/// Returns `(marked, skipped_unchanged)`.
pub fn mark_stale_if_changed(
    db: &dyn crate::db::backend::DbBackend,
    items: &[(String, String)],
) -> Result<(usize, usize), Box<dyn std::error::Error>> {
    if items.is_empty() {
        return Ok((0, 0));
    }
    let existing: std::collections::HashMap<String, EmbeddingStateRow> = list_all(db)?
        .into_iter()
        .map(|r| (r.qualified_name.clone(), r))
        .collect();

    let mut to_mark: Vec<String> = Vec::new();
    let mut skipped = 0usize;
    for (qn, current_hash) in items {
        match existing.get(qn) {
            Some(row)
                if row.state == "fresh"
                    && !row.content_hash.is_empty()
                    && row.content_hash == *current_hash =>
            {
                skipped += 1;
            }
            _ => to_mark.push(qn.clone()),
        }
    }
    mark_stale_for_qualified_names(db, &to_mark)?;
    Ok((to_mark.len(), skipped))
}

/// Mark a batch of qualified_names as stale. Idempotent: rows that already
/// exist flip to `state="stale"`; rows that don't exist are inserted with a
/// placeholder (`content_hash=""`) so the next `embed` run picks them up.
///
/// Prefer [`mark_stale_if_changed`] from the indexer so unchanged fresh
/// rows survive a no-op full index (FR-EMBED-RESUME-04).
///
/// `usearch_key` is computed deterministically from each qualified_name and
/// stored even on first insert, so the embed step can lookup the key without
/// recomputing.
pub fn mark_stale_for_qualified_names(
    db: &dyn crate::db::backend::DbBackend,
    qualified_names: &[String],
) -> Result<(), Box<dyn std::error::Error>> {
    if qualified_names.is_empty() {
        return Ok(());
    }
    // Dedupe: a single import with two rows for the same PK fails with
    // E21000 ("ON CONFLICT DO UPDATE command cannot affect row a second
    // time"). The indexer can hand us the same qualified_name twice (e.g.
    // host-path + workspace-path copies of the same file).
    let now = now_iso();
    let mut seen = std::collections::HashSet::with_capacity(qualified_names.len());
    for chunk in qualified_names.chunks(UPSERT_CHUNK) {
        // Parameterized import (DataValue) instead of string-interpolating
        // qualified_names into a Datalog literal. A QN can contain a
        // double-quote / backslash / control char (e.g. the minified JS
        // `vis-network.min.js` extractor or a markdown heading with
        // `"..."`); a string literal `["qn", ...]` with an unescaped quote
        // breaks the literal parser, which mis-parses rows and can merge two
        // distinct QNs into one duplicate PK → E21000. import_relations skips
        // script parsing entirely (same safe path as upsert_fresh).
        use crate::db::backend::DataValue;
        let rows: Vec<Vec<DataValue>> = chunk
            .iter()
            .filter(|qn| seen.insert(qn.to_string()))
            .map(|qn| {
                vec![
                    DataValue::Str(qn.as_str().into()),
                    DataValue::from(0i64),
                    DataValue::Str("".into()),
                    DataValue::Str("stale".into()),
                    DataValue::Str(now.as_str().into()),
                ]
            })
            .collect();
        if rows.is_empty() {
            continue;
        }
        let named_rows = crate::db::backend::NamedRows::new(
            vec![
                "qualified_name".to_string(),
                "usearch_key".to_string(),
                "content_hash".to_string(),
                "state".to_string(),
                "embedded_at".to_string(),
            ],
            rows,
        );
        let mut map = std::collections::BTreeMap::new();
        map.insert("embedding_state".to_string(), named_rows);
        db.import_relations(map)?;
    }
    Ok(())
}

/// Return every row whose `state != "fresh"`. Includes newly-inserted
/// placeholders (state="stale", content_hash="") and existing rows that were
/// re-touched by the indexer.
pub fn list_stale(
    db: &dyn crate::db::backend::DbBackend,
) -> Result<Vec<EmbeddingStateRow>, Box<dyn std::error::Error>> {
    let query = r#"?[qualified_name, usearch_key, content_hash, state, embedded_at] := *embedding_state[qualified_name, usearch_key, content_hash, state, embedded_at], state != "fresh""#;
    let result = db.run_script(query, Default::default())?;
    Ok(result
        .rows
        .iter()
        .filter_map(|row| row_to_state_row(row))
        .collect())
}

/// Return every state row whose qualified_name no longer exists in
/// `code_elements`. The embed step reaps these (removes the vector from
/// usearch and deletes the state row).
pub fn list_orphans(
    db: &dyn crate::db::backend::DbBackend,
) -> Result<Vec<EmbeddingStateRow>, Box<dyn std::error::Error>> {
    let query = r#"
        ?[qualified_name, usearch_key, content_hash, state, embedded_at] :=
            *embedding_state[qualified_name, usearch_key, content_hash, state, embedded_at],
            not *code_elements[qualified_name, _, _, _, _, _, _, _, _, _, _, _, _]
    "#;
    let result = db.run_script(query, Default::default())?;
    Ok(result
        .rows
        .iter()
        .filter_map(|row| row_to_state_row(row))
        .collect())
}

/// Return all state rows. Used by `embed --full` to re-embed every existing
/// vector.
pub fn list_all(
    db: &dyn crate::db::backend::DbBackend,
) -> Result<Vec<EmbeddingStateRow>, Box<dyn std::error::Error>> {
    let query = r#"?[qualified_name, usearch_key, content_hash, state, embedded_at] := *embedding_state[qualified_name, usearch_key, content_hash, state, embedded_at]"#;
    let result = db.run_script(query, Default::default())?;
    Ok(result
        .rows
        .iter()
        .filter_map(|row| row_to_state_row(row))
        .collect())
}

/// Cheap non-empty probe for MCP HNSW gating (FR-SEM-07).
/// Avoids loading every `embedding_state` row via [`list_all`].
pub fn has_any(db: &dyn crate::db::backend::DbBackend) -> Result<bool, Box<dyn std::error::Error>> {
    let query = r#"?[qualified_name] := *embedding_state[qualified_name, usearch_key, content_hash, state, embedded_at] :limit 1"#;
    let result = db.run_script(query, Default::default())?;
    Ok(!result.rows.is_empty())
}

/// Maximum number of rows to inline into a single CozoDB `<~ [...]` literal.
/// CozoDB's pest grammar parser recurses on large literals and can blow the
/// stack or hit internal limits on thousand-row repos; chunking keeps each
/// statement bounded.
const UPSERT_CHUNK: usize = 500;

/// Batch upsert: mark rows fresh and stamp their content_hash + embedded_at.
/// Called by the embed step after vectors land in usearch.
pub fn upsert_fresh(
    db: &dyn crate::db::backend::DbBackend,
    updates: &[FreshRow],
) -> Result<(), Box<dyn std::error::Error>> {
    if updates.is_empty() {
        return Ok(());
    }
    let now = now_iso();
    for chunk in updates.chunks(UPSERT_CHUNK) {
        // Parameterized import (DataValue) instead of string-interpolating
        // qualified_name / content_hash into a Datalog query. A QN containing
        // characters that serde_json::Value::String emits raw (e.g. non-ASCII
        // bytes, backslashes, control chars) broke the CozoDB query parser at
        // a fixed byte offset mid-batch. import_relations skips script parsing
        // entirely, matching the safe path already used by upsert_vectors.
        use crate::db::backend::DataValue;
        let rows: Vec<Vec<DataValue>> = chunk
            .iter()
            .map(|u| {
                vec![
                    DataValue::Str(u.qualified_name.as_str().into()),
                    DataValue::from(u.usearch_key as i64),
                    DataValue::Str(u.content_hash.as_str().into()),
                    DataValue::Str("fresh".into()),
                    DataValue::Str(now.as_str().into()),
                ]
            })
            .collect();
        let named_rows = crate::db::backend::NamedRows::new(
            vec![
                "qualified_name".to_string(),
                "usearch_key".to_string(),
                "content_hash".to_string(),
                "state".to_string(),
                "embedded_at".to_string(),
            ],
            rows,
        );
        let mut map = std::collections::BTreeMap::new();
        map.insert("embedding_state".to_string(), named_rows);
        db.import_relations(map)?;
    }
    Ok(())
}

/// Delete state rows for a set of orphan qualified_names. Called after the
/// embed step removes orphan vectors. With the CozoDB 0.7.x schema
/// (`qualified_name: String => ...`), only the key column is needed for `:rm`.
pub fn delete_state_rows(
    db: &dyn crate::db::backend::DbBackend,
    rows: &[EmbeddingStateRow],
) -> Result<(), Box<dyn std::error::Error>> {
    if rows.is_empty() {
        return Ok(());
    }
    for chunk in rows.chunks(UPSERT_CHUNK) {
        // Parameterized `:rm` (`?[qn] <- $qns :rm embedding_state {qualified_name}`)
        // — a QN with `"`/`\`/control chars breaks the inline `[[...]]`
        // literal (mis-parses → E21000 or wrong deletes), same as the
        // stale-mark path.
        let qns: Vec<serde_json::Value> = chunk
            .iter()
            .map(|r| serde_json::Value::String(r.qualified_name.clone()))
            .collect();
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "qns".to_string(),
            serde_json::Value::Array(
                qns.into_iter()
                    .map(|q| serde_json::Value::Array(vec![q]))
                    .collect(),
            ),
        );
        let query = r#"?[qualified_name] <- $qns :rm embedding_state {qualified_name}"#;
        db.run_script(query, params)?;
    }
    Ok(())
}

/// Count of fresh vs stale rows, for diagnostics.
pub fn count_by_state(
    db: &dyn crate::db::backend::DbBackend,
) -> Result<StateCounts, Box<dyn std::error::Error>> {
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

fn row_to_state_row(row: &[crate::db::backend::DataValue]) -> Option<EmbeddingStateRow> {
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
    use super::*;

    #[test]
    fn now_iso_returns_numeric_string() {
        let ts = now_iso();
        assert!(
            ts.chars().all(|c| c.is_ascii_digit()),
            "now_iso must be numeric: {ts}"
        );
        assert!(!ts.is_empty());
    }

    #[test]
    fn state_counts_default_is_all_zero() {
        let counts = StateCounts::default();
        assert_eq!(counts.fresh, 0);
        assert_eq!(counts.stale, 0);
        assert_eq!(counts.other, 0);
    }

    #[test]
    fn fresh_row_fields_are_accessible() {
        let row = FreshRow {
            qualified_name: "src/main.rs::main".to_string(),
            usearch_key: 42,
            content_hash: "abc123".to_string(),
        };
        assert_eq!(row.qualified_name, "src/main.rs::main");
        assert_eq!(row.usearch_key, 42);
        assert_eq!(row.content_hash, "abc123");
    }

    #[test]
    fn embedding_state_row_fields_are_accessible() {
        let row = EmbeddingStateRow {
            qualified_name: "q".to_string(),
            usearch_key: 7,
            content_hash: "h".to_string(),
            state: "fresh".to_string(),
            embedded_at: "12345".to_string(),
        };
        assert_eq!(row.qualified_name, "q");
        assert_eq!(row.usearch_key, 7);
        assert_eq!(row.content_hash, "h");
        assert_eq!(row.state, "fresh");
        assert_eq!(row.embedded_at, "12345");
    }

    #[test]
    fn row_to_state_row_parses_valid_row() {
        let row = vec![
            crate::db::backend::DataValue::Str("qn".into()),
            crate::db::backend::DataValue::Num(crate::db::value::Num::Int(5)),
            crate::db::backend::DataValue::Str("hash".into()),
            crate::db::backend::DataValue::Str("stale".into()),
            crate::db::backend::DataValue::Str("999".into()),
        ];
        let parsed = row_to_state_row(&row).expect("should parse");
        assert_eq!(parsed.qualified_name, "qn");
        assert_eq!(parsed.usearch_key, 5);
        assert_eq!(parsed.content_hash, "hash");
        assert_eq!(parsed.state, "stale");
        assert_eq!(parsed.embedded_at, "999");
    }

    #[test]
    fn row_to_state_row_returns_none_for_empty_row() {
        let row: Vec<crate::db::backend::DataValue> = vec![];
        assert!(row_to_state_row(&row).is_none());
    }

    #[test]
    fn row_to_state_row_returns_none_for_short_row() {
        // Only 2 columns instead of 5 — missing fields.
        let row = vec![
            crate::db::backend::DataValue::Str("qn".into()),
            crate::db::backend::DataValue::Num(crate::db::value::Num::Int(5)),
        ];
        assert!(row_to_state_row(&row).is_none());
    }

    #[test]
    fn has_any_false_on_empty_then_true_after_upsert() {
        // FR-SEM-07: MCP HNSW gate must use :limit 1, not list_all.
        use crate::db::backend::init_db;
        let tmp = tempfile::TempDir::new().unwrap();
        let db = init_db(&tmp.path().join("has_any.db")).unwrap();
        ensure_embedding_state_table(db.as_ref()).unwrap();
        assert!(!has_any(db.as_ref()).unwrap());
        upsert_fresh(
            db.as_ref(),
            &[FreshRow {
                qualified_name: "src/x.rs::f".to_string(),
                usearch_key: 1,
                content_hash: "h".to_string(),
            }],
        )
        .unwrap();
        assert!(has_any(db.as_ref()).unwrap());
    }

    #[test]
    fn mark_stale_dedupes_duplicate_qualified_names() {
        // Regression: duplicate qualified_names in one call must not emit a
        // `:put` with two rows for the same PK (E21000 on PG).
        use crate::db::backend::PostgresBackend;
        let Ok(db) = PostgresBackend::from_env() else {
            eprintln!("skipping: LEANKG_PG_URL not set");
            return;
        };
        let boxed: Box<dyn crate::db::backend::DbBackend> = Box::new(db);
        let dupes: Vec<String> = vec![
            "/dup/qn".to_string(),
            "/dup/qn".to_string(),
            "/dup/qn".to_string(),
            "/other/qn".to_string(),
        ];
        // Should not error (no E21000) even though /dup/qn appears 3x.
        mark_stale_for_qualified_names(boxed.as_ref(), &dupes).expect("dedupe must prevent E21000");
        // Real indexer shape: UPSERT_CHUNK=500 long-path QNs, all distinct.
        let big: Vec<String> = (0..500)
            .map(|i| format!("/Users/linh.doan/work/harvey/freepeak/leankg/tests/load_test_1m_nodes.rs::load_test_fn_{i}"))
            .collect();
        mark_stale_for_qualified_names(boxed.as_ref(), &big).expect("500 distinct must not E21000");
        // Clean up.
        let clean_qns: Vec<String> = (0..500)
            .map(|i| format!("/Users/linh.doan/work/harvey/freepeak/leankg/tests/load_test_1m_nodes.rs::load_test_fn_{i}"))
            .collect();
        let literals: Vec<String> = clean_qns.iter().map(|q| format!("[\"{}\"]", q)).collect();
        let clean = format!(
            "?[qualified_name] <- [{}] :rm embedding_state {{qualified_name}}",
            literals.join(", ")
        );
        let _ = boxed.run_script(&clean, std::collections::BTreeMap::new());
        // Clean up the rows we inserted.
        let clean = r#"?[qualified_name] <- [["/dup/qn"], ["/other/qn"]]
:rm embedding_state {qualified_name}"#;
        let _ = boxed.run_script(clean, std::collections::BTreeMap::new());
    }

    #[test]
    fn mark_stale_many_identical_qns_no_e21000() {
        // Reproduces the indexer on a real codebase: vis-network.min.js
        // yields 171 identical qualified_names. A single `:put` with all of
        // them must not E21000 (dedupe collapses to one row).
        use crate::db::backend::PostgresBackend;
        let Ok(db) = PostgresBackend::from_env() else {
            eprintln!("skipping: LEANKG_PG_URL not set");
            return;
        };
        let boxed: Box<dyn crate::db::backend::DbBackend> = Box::new(db);
        let dupes: Vec<String> = (0..171).map(|_| "/vis/constructor".to_string()).collect();
        mark_stale_for_qualified_names(boxed.as_ref(), &dupes)
            .expect("171 identical qns must not E21000");
        let clean = r#"?[qualified_name] <- [["/vis/constructor"]]
:rm embedding_state {qualified_name}"#;
        let _ = boxed.run_script(clean, std::collections::BTreeMap::new());
    }
}
