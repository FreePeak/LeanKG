//! # FR-ZCP-11: model-stamped vectors.
//!
//! Every vectors collection carries a stamp — the exact model identity it
//! was built with ({model_id, revision, dimensions, distance, provider}).
//! Before embedding, `check_stamp` compares the active model against the
//! persisted stamp: any mismatch is a HARD error demanding a rebuild, never
//! a silent mix of vectors from different model identities in one ANN
//! collection.

use crate::db::backend::DbBackend;
use crate::embeddings::registry::EmbeddingModelEntry;
use serde::{Deserialize, Serialize};

/// The per-collection model identity persisted alongside the vectors.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ModelStamp {
    pub model_id: String,
    pub revision: String,
    pub dimensions: usize,
    pub distance: String,
    pub provider: String,
}

impl ModelStamp {
    pub fn from_entry(entry: &EmbeddingModelEntry) -> Self {
        Self {
            model_id: entry.model_id.clone(),
            revision: entry.revision.clone(),
            dimensions: entry.dimensions,
            distance: entry.distance.clone(),
            provider: entry.provider.as_str().to_string(),
        }
    }
}

/// Result of comparing the persisted stamp against the active model.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum StampCheck {
    /// No stamp persisted yet (first build for this collection).
    Missing,
    /// Stamp matches the active model.
    Match,
    /// Stamp conflicts — the collection must be rebuilt.
    Mismatch(Box<MismatchDetail>),
}

/// Detail payload for [`StampCheck::Mismatch`] (boxed to keep the enum
/// small — the common arms are Missing/Match).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct MismatchDetail {
    pub persisted: ModelStamp,
    pub active: ModelStamp,
    pub reason: String,
}

fn stamp_relation(model_id: &str) -> String {
    // Per-model namespace, same discipline as the vectors/state relations.
    let sanitized: String = model_id
        .chars()
        .map(|c| if c.is_alphanumeric() { c } else { '_' })
        .collect();
    format!("emb_stamp_{}", sanitized.to_lowercase())
}

/// Persist (upsert) the stamp for a model collection.
pub fn write_stamp(
    db: &dyn DbBackend,
    entry: &EmbeddingModelEntry,
) -> Result<(), Box<dyn std::error::Error>> {
    let rel = stamp_relation(&entry.model_id);
    let stamp = ModelStamp::from_entry(entry);
    let json = serde_json::to_string(&stamp)?;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "rows".to_string(),
        serde_json::json!([[entry.model_id, json]]),
    );
    let script = format!(r#"?[model_key, stamp] <- $rows :put {rel} {{model_key, stamp}}"#);
    db.run_script(&script, params)?;
    Ok(())
}

/// Read the persisted stamp for a model collection; `None` when absent.
pub fn read_stamp(
    db: &dyn DbBackend,
    model_id: &str,
) -> Result<Option<ModelStamp>, Box<dyn std::error::Error>> {
    let rel = stamp_relation(model_id);
    let result = db.run_script(
        &format!(
            "?[stamp] := *{rel}{{model_key, stamp}}, model_key = \"{}\"",
            model_id
        ),
        Default::default(),
    );
    match result {
        Ok(r) if !r.rows.is_empty() => {
            let raw = r.rows[0][0].get_str().unwrap_or("{}");
            let parsed: Option<ModelStamp> = serde_json::from_str(raw).ok();
            tracing::debug!(
                target: "leankg::stamp",
                "read_stamp[{model_id}] rows={} parsed={}",
                r.rows.len(),
                parsed.is_some()
            );
            Ok(parsed)
        }
        Ok(_) => {
            tracing::debug!(target: "leankg::stamp", "read_stamp[{model_id}] relation exists, no row");
            Ok(None)
        }
        // Relation not created yet → Missing (a real query error is
        // surfaced, not swallowed).
        Err(e) => {
            let msg = e.to_string();
            if msg.contains("does not exist") || msg.contains("not found") {
                Ok(None)
            } else {
                Err(e)
            }
        }
    }
}

/// Compare the persisted stamp against the active model entry.
pub fn check_stamp(
    db: &dyn DbBackend,
    entry: &EmbeddingModelEntry,
) -> Result<StampCheck, Box<dyn std::error::Error>> {
    let active = ModelStamp::from_entry(entry);
    match read_stamp(db, &entry.model_id)? {
        None => Ok(StampCheck::Missing),
        Some(persisted) => {
            if persisted == active {
                return Ok(StampCheck::Match);
            }
            let mut reasons = Vec::new();
            if persisted.revision != active.revision {
                reasons.push(format!(
                    "revision {} -> {}",
                    persisted.revision, active.revision
                ));
            }
            if persisted.dimensions != active.dimensions {
                reasons.push(format!(
                    "dimensions {} -> {}",
                    persisted.dimensions, active.dimensions
                ));
            }
            if persisted.distance != active.distance {
                reasons.push(format!(
                    "distance {} -> {}",
                    persisted.distance, active.distance
                ));
            }
            if persisted.provider != active.provider {
                reasons.push(format!(
                    "provider {} -> {}",
                    persisted.provider, active.provider
                ));
            }
            Ok(StampCheck::Mismatch(Box::new(MismatchDetail {
                persisted,
                active,
                reason: reasons.join("; "),
            })))
        }
    }
}

/// FR-ZCP-11 query-side guard: `Some(reason)` when the collection's stamp
/// conflicts with the active model — the caller (semantic_search) degrades
/// to the keyword rung with this reason instead of querying mixed-model
/// vectors. `None` = stamp matches (or missing → not yet built).
pub fn stamp_mismatch_reason(
    db: &dyn DbBackend,
    entry: &EmbeddingModelEntry,
) -> Result<Option<String>, Box<dyn std::error::Error>> {
    match check_stamp(db, entry)? {
        StampCheck::Match | StampCheck::Missing => Ok(None),
        StampCheck::Mismatch(detail) => Ok(Some(format!(
            "collection `{}` was built by model identity {} ({}); active model is {} —              rebuild required (run `leankg embed --full`)",
            entry.vectors_relation(),
            detail.persisted.model_id,
            detail.reason,
            detail.active.model_id
        ))),
    }
}

/// Hard rebuild guard: `Err` with a rebuild directive when the persisted
/// stamp conflicts with the active model. `Ok(())` on Match or Missing
/// (a fresh collection gets stamped at build time).
pub fn require_stamp_match(
    db: &dyn DbBackend,
    entry: &EmbeddingModelEntry,
) -> Result<(), Box<dyn std::error::Error>> {
    match check_stamp(db, entry)? {
        StampCheck::Match | StampCheck::Missing => {
            write_stamp(db, entry)?;
            Ok(())
        }
        StampCheck::Mismatch(detail) => {
            let (persisted, reason) = (&detail.persisted, &detail.reason);
            Err(format!(
                "embedding model stamp mismatch for collection `{}` (persisted model_id={}, {}): \
                 the vectors were built by a different model identity — rebuild required \
                 (run `leankg embed --full` after switching, or `leankg embed --status` for details)",
                entry.vectors_relation(),
                persisted.model_id,
                reason
            )
            .into())
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::backend::init_db;
    use crate::embeddings::registry::RegistryProviderKind;
    use tempfile::TempDir;

    fn entry(model_id: &str, dims: usize, provider: RegistryProviderKind) -> EmbeddingModelEntry {
        EmbeddingModelEntry {
            model_id: model_id.into(),
            provider,
            model_name: format!("{model_id}-name"),
            dimensions: dims,
            distance: "cosine".into(),
            revision: "rev-1".into(),
        }
    }

    #[test]
    fn stamp_roundtrip_and_guard() {
        let tmp = TempDir::new().unwrap();
        let db = init_db(&tmp.path().join(".leankg")).unwrap();
        let e = entry("test-model-384", 384, RegistryProviderKind::Local);

        // Missing → guard stamps it fresh.
        assert_eq!(check_stamp(db.as_ref(), &e).unwrap(), StampCheck::Missing);
        require_stamp_match(db.as_ref(), &e).unwrap();
        assert_eq!(check_stamp(db.as_ref(), &e).unwrap(), StampCheck::Match);

        // Same identity, different revision → Mismatch with reason.
        let mut e2 = e.clone();
        e2.revision = "rev-2".into();
        match check_stamp(db.as_ref(), &e2).unwrap() {
            StampCheck::Mismatch(detail) => {
                assert!(
                    detail.reason.contains("revision rev-1 -> rev-2"),
                    "{}",
                    detail.reason
                );
            }
            other => panic!("expected Mismatch, got {other:?}"),
        }
        // The guard hard-fails on mismatch.
        assert!(require_stamp_match(db.as_ref(), &e2).is_err());

        // Dimensions drift also detected.
        let e3 = entry("test-model-384", 768, RegistryProviderKind::Local);
        match check_stamp(db.as_ref(), &e3).unwrap() {
            StampCheck::Mismatch(detail) => {
                assert!(
                    detail.reason.contains("dimensions 384 -> 768"),
                    "{}",
                    detail.reason
                );
            }
            other => panic!("expected Mismatch, got {other:?}"),
        }
    }
}
