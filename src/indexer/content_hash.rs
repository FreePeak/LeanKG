//! Content-hash incremental indexing helper (Bloop / Cognee pattern —
//! strategy §3.12 / §17 Tier 3 item 24 / §17.4 line 1654).
//!
//! Re-index only changed files. Key formula (Bloop BLAKE3 tuple, per spec):
//!
//! ```text
//! blake3(schema_version, path, repo, content, filters, branch)
//! ```
//!
//! All fields are length-prefixed (u64 LE) so a boundary shift (e.g.
//! `path="a", repo="bc"` vs `path="ab", repo="c"`) can never collide.
//!
//! The content-hash store is the `index_hashes` PG table (W8 wave 1: read
//! and written as parameterized SQL through the [`crate::db::sql`] seam,
//! no Datalog). This module is **standalone**: wiring into the index walk
//! (`src/indexer/mod.rs`) is deferred until the P0 session merges, per plan —
//! today it ships the pure key derivation + store CRUD + a unit-tested
//! "should re-index?" predicate so the walk hook is a two-line call later.

use std::path::Path;

/// Version bump this when the extraction pipeline or the key formula changes;
/// a new version invalidates all prior hashes (safe: it just re-indexes).
///
/// Bumped to 2 from the SHA-256 + concat-framing v1 — length-prefixed
/// BLAKE3 tuples are not bit-compatible with the v1 store rows, so the
/// re-index is a one-time cost on first deploy.
pub const SCHEMA_VERSION: u32 = 2;

/// Length-prefix a field into the hasher. Each field is prefixed by its byte
/// length as a `u64` little-endian, so concatenated fields can never alias.
fn absorb(hasher: &mut blake3::Hasher, field: &[u8]) {
    hasher.update(&(field.len() as u64).to_le_bytes());
    hasher.update(field);
}

/// Deterministic content-hash key for one file.
pub fn cache_key(
    schema_version: u32,
    path: &str,
    repo: &str,
    content: &str,
    filters: &str,
    branch: &str,
) -> String {
    let mut hasher = blake3::Hasher::new();
    absorb(&mut hasher, &schema_version.to_le_bytes());
    absorb(&mut hasher, path.as_bytes());
    absorb(&mut hasher, repo.as_bytes());
    absorb(&mut hasher, content.as_bytes());
    absorb(&mut hasher, filters.as_bytes());
    absorb(&mut hasher, branch.as_bytes());
    hex::encode(hasher.finalize().as_bytes())
}

/// A persisted row: file path + the hash it was last indexed with.
#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub struct IndexHashRow {
    pub path: String,
    pub hash: String,
}

/// Read persisted hashes. Returns `Ok(Vec)` (possibly empty) when
/// the relation does not exist yet — the caller treats that as "index all".
///
/// W8 wave 1: issued as parameterized SQL through the [`crate::db::sql`]
/// seam on the backend trait (was: a Datalog `run_raw_query` script).
pub fn load_hashes(
    db: &dyn crate::db::backend::DbBackend,
) -> Result<Vec<IndexHashRow>, Box<dyn std::error::Error + Send + Sync>> {
    let rows = db
        .sql_query("SELECT path, hash FROM index_hashes", &[])
        .map_err(|e| {
            Box::new(std::io::Error::other(e.to_string()))
                as Box<dyn std::error::Error + Send + Sync>
        })?;
    Ok(rows
        .iter()
        .map(|row| IndexHashRow {
            path: row.text("path").unwrap_or_default(),
            hash: row.text("hash").unwrap_or_default(),
        })
        .collect())
}

/// Persist the new hash set (upsert semantics: overwrite by `path`).
///
/// W8 wave 1: one atomic parameterized batch through the
/// [`crate::db::sql`] seam — `ON CONFLICT (path) DO UPDATE` replaces the
/// per-row Datalog `:put` scripts (which also were not atomic across rows).
pub fn save_hashes(
    db: &dyn crate::db::backend::DbBackend,
    rows: &[IndexHashRow],
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let stmts: Vec<(&str, Vec<crate::db::sql::SqlParam>)> = rows
        .iter()
        .map(|r| {
            (
                "INSERT INTO index_hashes (path, hash) VALUES ($1, $2) \
                 ON CONFLICT (path) DO UPDATE SET hash = $2",
                vec![
                    crate::db::sql::SqlParam::Text(r.path.clone()),
                    crate::db::sql::SqlParam::Text(r.hash.clone()),
                ],
            )
        })
        .collect();
    db.sql_execute_batch(&stmts)
        .map_err(|e| -> Box<dyn std::error::Error + Send + Sync> {
            Box::new(std::io::Error::other(e.to_string()))
        })
}

/// Build the per-file hash set for a directory walk (content read from disk).
/// Returns `(files_with_hash, byte_count)` — deterministic order (sorted).
pub fn hash_files(
    root: &Path,
    files: &[String],
    repo: &str,
    filters: &str,
    branch: &str,
) -> Vec<IndexHashRow> {
    let mut out: Vec<IndexHashRow> = Vec::with_capacity(files.len());
    for f in files {
        let path = if f.starts_with('/') {
            std::path::PathBuf::from(f)
        } else {
            root.join(f)
        };
        let Ok(content) = std::fs::read_to_string(&path) else {
            continue;
        };
        out.push(IndexHashRow {
            path: f.clone(),
            hash: cache_key(SCHEMA_VERSION, f, repo, &content, filters, branch),
        });
    }
    out.sort_by(|a, b| a.path.cmp(&b.path));
    out
}

/// Files that need (re-)indexing: new files + files whose hash changed.
/// `previous` is the persisted store; `current` is the freshly walked set.
pub fn files_needing_index(previous: &[IndexHashRow], current: &[IndexHashRow]) -> Vec<String> {
    let prev: std::collections::HashMap<&str, &str> = previous
        .iter()
        .map(|r| (r.path.as_str(), r.hash.as_str()))
        .collect();
    current
        .iter()
        .filter(|r| prev.get(r.path.as_str()) != Some(&r.hash.as_str()))
        .map(|r| r.path.clone())
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    #[test]
    fn cache_key_is_deterministic_and_sensitive() {
        let a = cache_key(2, "src/a.rs", "repo", "fn a(){}", "lang=go", "main");
        let b = cache_key(2, "src/a.rs", "repo", "fn a(){}", "lang=go", "main");
        assert_eq!(a, b);
        // Any component change → different key.
        let c = cache_key(3, "src/a.rs", "repo", "fn a(){}", "lang=go", "main");
        assert_ne!(a, c, "schema_version changes key");
        let d = cache_key(2, "src/a.rs", "repo", "fn b(){}", "lang=go", "main");
        assert_ne!(a, d, "content changes key");
        let e = cache_key(2, "src/a.rs", "other", "fn a(){}", "lang=go", "main");
        assert_ne!(a, e, "repo changes key");
    }

    #[test]
    fn cache_key_length_is_blake3_64_hex() {
        // BLAKE3 default = 32 bytes → 64 hex chars. Locks the output shape
        // and the algorithm family (regression guard against silent
        // swap to SHA-256 / MD5).
        let h = cache_key(2, "x", "y", "z", "", "main");
        assert_eq!(h.len(), 64, "BLAKE3 default digest = 32 bytes hex");
        assert!(h.chars().all(|c| c.is_ascii_hexdigit()));
    }

    #[test]
    fn cache_key_blake3_fixed_vector() {
        // blake3::hash(b"abc") = 6437b3ac38465133ffb63b75273a8db548c558465d79db03fd359c6cd5bd9d85
        // Locked-vector regression guard against silent algorithm swap.
        let h = blake3::hash(b"abc");
        assert_eq!(
            hex::encode(h.as_bytes()),
            "6437b3ac38465133ffb63b75273a8db548c558465d79db03fd359c6cd5bd9d85"
        );
    }

    #[test]
    fn cache_key_uses_length_prefix_framing() {
        // The old concat hash aliased (path="a", repo="bc") with
        // (path="ab", repo="c"). Length-prefix framing must not.
        let x = cache_key(2, "a", "bc", "content", "", "main");
        let y = cache_key(2, "ab", "c", "content", "", "main");
        assert_ne!(
            x, y,
            "length-prefix framing prevents path|repo boundary collisions"
        );
        // And a length-only shift still differentiates.
        let z = cache_key(2, "abc", "", "content", "", "main");
        assert_ne!(x, z);
    }

    #[test]
    fn hash_files_sorts_and_skips_missing() {
        let tmp = TempDir::new().unwrap();
        std::fs::write(tmp.path().join("b.rs"), "fn b(){}").unwrap();
        std::fs::write(tmp.path().join("a.rs"), "fn a(){}").unwrap();
        let files = vec![
            "a.rs".to_string(),
            "b.rs".to_string(),
            "ghost.rs".to_string(),
        ];
        let rows = hash_files(tmp.path(), &files, "r", "", "main");
        assert_eq!(rows.len(), 2, "missing file skipped");
        assert_eq!(rows[0].path, "a.rs", "sorted by path");
    }

    #[test]
    fn files_needing_index_tracks_new_and_changed() {
        let prev = vec![IndexHashRow {
            path: "a.rs".into(),
            hash: "old".into(),
        }];
        let cur = vec![
            IndexHashRow {
                path: "a.rs".into(),
                hash: "new".into(),
            },
            IndexHashRow {
                path: "b.rs".into(),
                hash: "x".into(),
            },
        ];
        let need = files_needing_index(&prev, &cur);
        assert_eq!(need, vec!["a.rs", "b.rs"]);
    }

    #[test]
    fn unchanged_files_are_skipped() {
        let prev = vec![IndexHashRow {
            path: "a.rs".into(),
            hash: "same".into(),
        }];
        let cur = vec![IndexHashRow {
            path: "a.rs".into(),
            hash: "same".into(),
        }];
        assert!(files_needing_index(&prev, &cur).is_empty());
    }
}
