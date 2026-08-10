//! Schema-introspection helpers for the Postgres backend.
//!
//! Post-migration (Phase 8) these are the surviving pieces of the old
//! cozo storage layer: the canonical column lists + arity probes used to
//! describe the `code_elements` / `relationships` relations, and the
//! project-root resolution for legacy RocksDB path layouts.

use crate::db::backend::DbBackend;

/// Schema snapshot for one relation. Returned by `get_relation_schema`
/// and consumed by callers that need arity-correct rules (e.g. the
/// ontology self-test in `mcp/kg_self_test.rs`).
#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub struct RelationSchema {
    pub name: String,
    pub arity: usize,
    pub columns: Vec<String>,
    pub canonical: bool,
}

fn get_column_count(db: &dyn DbBackend, relation: &str) -> usize {
    let arity_probe = match relation {
        "code_elements" => Some(vec![
            (
                13,
                "?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] :limit 0",
            ),
            (
                12,
                "?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] :limit 0",
            ),
            (
                11,
                "?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] :limit 0",
            ),
        ]),
        "relationships" => Some(vec![
            (
                6,
                "?[source_qualified] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, env] :limit 0",
            ),
            (
                5,
                "?[source_qualified] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata] :limit 0",
            ),
        ]),
        _ => None,
    };

    if let Some(probes) = arity_probe {
        for (arity, query) in probes {
            if db.run_script(query, Default::default()).is_ok() {
                return arity;
            }
        }
    }

    let query = format!(":schema {}", relation);
    db.run_script(&query, Default::default())
        .map(|r| r.rows.len())
        .unwrap_or(0)
}

/// Canonical column lists per arity, keyed by relation name. Used by
/// `get_relation_schema` to translate an arity probe into a concrete list
/// of column names.
const CODE_ELEMENTS_13_COLUMNS: &[&str] = &[
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
];

const CODE_ELEMENTS_12_COLUMNS: &[&str] = &[
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
];

const CODE_ELEMENTS_11_COLUMNS: &[&str] = &[
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
];

const RELATIONSHIPS_6_COLUMNS: &[&str] = &[
    "source_qualified",
    "target_qualified",
    "rel_type",
    "confidence",
    "metadata",
    "env",
];

const RELATIONSHIPS_5_COLUMNS: &[&str] = &[
    "source_qualified",
    "target_qualified",
    "rel_type",
    "confidence",
    "metadata",
];

/// Returns the live schema for the named relation. `columns` is the ordered
/// list of column names as the relation is currently defined; `canonical`
/// is true when the live arity matches the current canonical schema
/// (13 for code_elements, 6 for relationships).
pub fn get_relation_schema(db: &dyn DbBackend, relation: &str) -> RelationSchema {
    let arity = get_column_count(db, relation);
    let columns: Vec<String> = match relation {
        "code_elements" => match arity {
            13 => CODE_ELEMENTS_13_COLUMNS
                .iter()
                .map(|s| s.to_string())
                .collect(),
            12 => CODE_ELEMENTS_12_COLUMNS
                .iter()
                .map(|s| s.to_string())
                .collect(),
            11 => CODE_ELEMENTS_11_COLUMNS
                .iter()
                .map(|s| s.to_string())
                .collect(),
            _ => Vec::new(),
        },
        "relationships" => match arity {
            6 => RELATIONSHIPS_6_COLUMNS
                .iter()
                .map(|s| s.to_string())
                .collect(),
            5 => RELATIONSHIPS_5_COLUMNS
                .iter()
                .map(|s| s.to_string())
                .collect(),
            _ => Vec::new(),
        },
        _ => Vec::new(),
    };
    let canonical = match relation {
        "code_elements" => arity == 13,
        "relationships" => arity == 6,
        _ => arity > 0,
    };
    RelationSchema {
        name: relation.to_string(),
        arity,
        columns,
        canonical,
    }
}

/// Convenience accessor for the code_elements relation.
pub fn code_elements_schema(db: &dyn DbBackend) -> RelationSchema {
    get_relation_schema(db, "code_elements")
}

/// Convenience accessor for the relationships relation.
pub fn relationships_schema(db: &dyn DbBackend) -> RelationSchema {
    get_relation_schema(db, "relationships")
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::backend::PostgresBackend;
    use tempfile::TempDir;

    /// A PostgresBackend pinned at a dead URL — connect is lazy, so
    /// `get_relation_schema` exercises the arity probes without a server.
    fn dead_backend() -> PostgresBackend {
        PostgresBackend {
            pg_url: "postgres://invalid-host-not-real:1/leankg".into(),
            schema: None,
            pool: std::sync::Arc::new(crate::db::backend::ClientPool::new(1)),
            ro_pool: std::sync::Arc::new(crate::db::backend::ClientPool::new(1)),
            read_only: false,
            write_bus: None,
        }
    }

    #[test]
    fn get_relation_schema_unknown_relation_returns_zero_columns() {
        let db = dead_backend();
        let schema = get_relation_schema(&db, "no_such_relation");
        assert_eq!(schema.name, "no_such_relation");
        assert_eq!(schema.arity, 0);
        assert!(schema.columns.is_empty());
        assert!(!schema.canonical);
    }

    #[test]
    fn column_lists_are_canonical() {
        assert_eq!(CODE_ELEMENTS_13_COLUMNS.len(), 13);
        assert_eq!(CODE_ELEMENTS_12_COLUMNS.len(), 12);
        assert_eq!(CODE_ELEMENTS_11_COLUMNS.len(), 11);
        assert_eq!(RELATIONSHIPS_6_COLUMNS.len(), 6);
        assert_eq!(RELATIONSHIPS_5_COLUMNS.len(), 5);
    }

    #[test]
    fn tempdir_compiles() {
        // Ensure tempfile still resolves (used by other schema tests via
        // the lib dev-deps).
        let _tmp = TempDir::new().unwrap();
    }
}
