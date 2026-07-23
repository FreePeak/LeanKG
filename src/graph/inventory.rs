//! CozoDB-persisted index inventory (FR-INDEX-INV-*).

use crate::db::schema::CozoDb;
use crate::graph::GraphEngine;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

pub const INVENTORY_KEY_LATEST: &str = "latest";

const CREATE_INDEX_INVENTORY: &str = r#":create index_inventory {
    key: String =>
    computed_at: String,
    total_elements: Int,
    total_relationships: Int,
    total_vectors: Int,
    total_documents: Int,
    total_doc_sections: Int,
    elements_by_type_json: String,
    relationships_by_type_json: String,
    vectors_by_type_json: String,
    estimated_vector_bytes: Int,
    estimated_hnsw_bytes: Int,
    notes: String
}"#;

const VECTOR_BYTES_PER: i64 = 384 * 4;
const HNSW_OVERHEAD_FACTOR: i64 = 3;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct IndexInventory {
    pub key: String,
    pub computed_at: String,
    pub total_elements: i64,
    pub total_relationships: i64,
    pub total_vectors: i64,
    pub total_documents: i64,
    pub total_doc_sections: i64,
    pub elements_by_type_json: String,
    pub relationships_by_type_json: String,
    pub vectors_by_type_json: String,
    pub estimated_vector_bytes: i64,
    pub estimated_hnsw_bytes: i64,
    pub notes: String,
}

pub fn ensure_index_inventory_table(db: &CozoDb) -> Result<(), Box<dyn std::error::Error>> {
    let existing: std::collections::HashSet<String> =
        crate::db::schema::run_script(db, "::relations", Default::default())?
            .rows
            .iter()
            .filter_map(|r| r.first().and_then(|v| v.get_str()).map(String::from))
            .collect();
    if !existing.contains("index_inventory") {
        crate::db::schema::run_script(db, CREATE_INDEX_INVENTORY, Default::default())?;
    }
    Ok(())
}

type ElementRelCounts = (BTreeMap<String, i64>, BTreeMap<String, i64>);

fn type_count_maps(graph: &GraphEngine) -> Result<ElementRelCounts, Box<dyn std::error::Error>> {
    let schema = graph.get_graph_schema(None)?;
    let mut elements = BTreeMap::new();
    if let Some(arr) = schema.get("element_types").and_then(|v| v.as_array()) {
        for row in arr {
            if let (Some(t), Some(c)) = (
                row.get("element_type").and_then(|v| v.as_str()),
                row.get("count").and_then(|v| v.as_i64()),
            ) {
                elements.insert(t.to_string(), c);
            }
        }
    }
    let mut relationships = BTreeMap::new();
    if let Some(arr) = schema.get("relationship_types").and_then(|v| v.as_array()) {
        for row in arr {
            if let (Some(t), Some(c)) = (
                row.get("rel_type").and_then(|v| v.as_str()),
                row.get("count").and_then(|v| v.as_i64()),
            ) {
                relationships.insert(t.to_string(), c);
            }
        }
    }
    Ok((elements, relationships))
}

#[cfg(feature = "embeddings")]
fn count_vectors(db: &CozoDb) -> Result<i64, Box<dyn std::error::Error>> {
    Ok(crate::embeddings::control::count_embedding_vectors(db)? as i64)
}

#[cfg(not(feature = "embeddings"))]
fn count_vectors(_db: &CozoDb) -> Result<i64, Box<dyn std::error::Error>> {
    Ok(0)
}

pub fn refresh_index_inventory(
    graph: &GraphEngine,
    notes: &str,
) -> Result<IndexInventory, Box<dyn std::error::Error>> {
    let db = graph.db();
    ensure_index_inventory_table(db)?;

    let (elements, relationships) = type_count_maps(graph)?;
    let total_elements = graph.count_elements()? as i64;
    let total_relationships = graph.count_relationships()? as i64;
    let total_vectors = count_vectors(db)?;
    let total_documents = elements.get("document").copied().unwrap_or(0);
    let total_doc_sections = elements.get("doc_section").copied().unwrap_or(0);
    let estimated_vector_bytes = total_vectors * VECTOR_BYTES_PER;
    let estimated_hnsw_bytes = estimated_vector_bytes * HNSW_OVERHEAD_FACTOR;

    let inv = IndexInventory {
        key: INVENTORY_KEY_LATEST.to_string(),
        computed_at: now_iso(),
        total_elements,
        total_relationships,
        total_vectors,
        total_documents,
        total_doc_sections,
        elements_by_type_json: serde_json::to_string(&elements)?,
        relationships_by_type_json: serde_json::to_string(&relationships)?,
        vectors_by_type_json: "{}".to_string(),
        estimated_vector_bytes,
        estimated_hnsw_bytes,
        notes: notes.to_string(),
    };
    upsert_inventory(db, &inv)?;
    Ok(inv)
}

fn upsert_inventory(db: &CozoDb, inv: &IndexInventory) -> Result<(), Box<dyn std::error::Error>> {
    let query = r#"?[key, computed_at, total_elements, total_relationships, total_vectors, total_documents, total_doc_sections, elements_by_type_json, relationships_by_type_json, vectors_by_type_json, estimated_vector_bytes, estimated_hnsw_bytes, notes] <- [[$key, $computed_at, $total_elements, $total_relationships, $total_vectors, $total_documents, $total_doc_sections, $elements_by_type_json, $relationships_by_type_json, $vectors_by_type_json, $estimated_vector_bytes, $estimated_hnsw_bytes, $notes]]
        :put index_inventory {key => computed_at, total_elements, total_relationships, total_vectors, total_documents, total_doc_sections, elements_by_type_json, relationships_by_type_json, vectors_by_type_json, estimated_vector_bytes, estimated_hnsw_bytes, notes}"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert("key".into(), serde_json::Value::String(inv.key.clone()));
    params.insert(
        "computed_at".into(),
        serde_json::Value::String(inv.computed_at.clone()),
    );
    params.insert(
        "total_elements".into(),
        serde_json::Value::Number(inv.total_elements.into()),
    );
    params.insert(
        "total_relationships".into(),
        serde_json::Value::Number(inv.total_relationships.into()),
    );
    params.insert(
        "total_vectors".into(),
        serde_json::Value::Number(inv.total_vectors.into()),
    );
    params.insert(
        "total_documents".into(),
        serde_json::Value::Number(inv.total_documents.into()),
    );
    params.insert(
        "total_doc_sections".into(),
        serde_json::Value::Number(inv.total_doc_sections.into()),
    );
    params.insert(
        "elements_by_type_json".into(),
        serde_json::Value::String(inv.elements_by_type_json.clone()),
    );
    params.insert(
        "relationships_by_type_json".into(),
        serde_json::Value::String(inv.relationships_by_type_json.clone()),
    );
    params.insert(
        "vectors_by_type_json".into(),
        serde_json::Value::String(inv.vectors_by_type_json.clone()),
    );
    params.insert(
        "estimated_vector_bytes".into(),
        serde_json::Value::Number(inv.estimated_vector_bytes.into()),
    );
    params.insert(
        "estimated_hnsw_bytes".into(),
        serde_json::Value::Number(inv.estimated_hnsw_bytes.into()),
    );
    params.insert("notes".into(), serde_json::Value::String(inv.notes.clone()));
    crate::db::schema::run_script(db, query, params)?;
    Ok(())
}

fn now_iso() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0)
        .to_string()
}

pub fn load_latest_inventory(
    db: &CozoDb,
) -> Result<Option<IndexInventory>, Box<dyn std::error::Error>> {
    ensure_index_inventory_table(db)?;
    let query = r#"?[key, computed_at, total_elements, total_relationships, total_vectors, total_documents, total_doc_sections, elements_by_type_json, relationships_by_type_json, vectors_by_type_json, estimated_vector_bytes, estimated_hnsw_bytes, notes] :=
        *index_inventory[key, computed_at, total_elements, total_relationships, total_vectors, total_documents, total_doc_sections, elements_by_type_json, relationships_by_type_json, vectors_by_type_json, estimated_vector_bytes, estimated_hnsw_bytes, notes],
        key = "latest""#;
    let result = crate::db::schema::run_script(db, query, Default::default())?;
    let row = match result.rows.first() {
        Some(r) => r,
        None => return Ok(None),
    };
    Ok(Some(IndexInventory {
        key: row[0].get_str().unwrap_or("latest").to_string(),
        computed_at: row[1].get_str().unwrap_or("").to_string(),
        total_elements: row[2].get_int().unwrap_or(0),
        total_relationships: row[3].get_int().unwrap_or(0),
        total_vectors: row[4].get_int().unwrap_or(0),
        total_documents: row[5].get_int().unwrap_or(0),
        total_doc_sections: row[6].get_int().unwrap_or(0),
        elements_by_type_json: row[7].get_str().unwrap_or("{}").to_string(),
        relationships_by_type_json: row[8].get_str().unwrap_or("{}").to_string(),
        vectors_by_type_json: row[9].get_str().unwrap_or("{}").to_string(),
        estimated_vector_bytes: row[10].get_int().unwrap_or(0),
        estimated_hnsw_bytes: row[11].get_int().unwrap_or(0),
        notes: row[12].get_str().unwrap_or("").to_string(),
    }))
}

pub fn inventory_to_json(inv: &IndexInventory) -> serde_json::Value {
    serde_json::json!({
        "key": inv.key,
        "computed_at": inv.computed_at,
        "total_elements": inv.total_elements,
        "total_relationships": inv.total_relationships,
        "total_vectors": inv.total_vectors,
        "total_documents": inv.total_documents,
        "total_doc_sections": inv.total_doc_sections,
        "elements_by_type": serde_json::from_str::<serde_json::Value>(&inv.elements_by_type_json).unwrap_or(serde_json::json!({})),
        "relationships_by_type": serde_json::from_str::<serde_json::Value>(&inv.relationships_by_type_json).unwrap_or(serde_json::json!({})),
        "estimated_vector_bytes": inv.estimated_vector_bytes,
        "estimated_hnsw_bytes": inv.estimated_hnsw_bytes,
        "notes": inv.notes,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::schema::init_db;
    use tempfile::TempDir;

    #[test]
    fn inventory_round_trip_empty_graph() {
        let dir = TempDir::new().unwrap();
        let db = init_db(dir.path()).unwrap();
        let graph = GraphEngine::new(db);
        let inv = refresh_index_inventory(&graph, "test").unwrap();
        assert_eq!(inv.total_elements, 0);
        let loaded = load_latest_inventory(graph.db()).unwrap().unwrap();
        assert_eq!(loaded.total_elements, 0);
        assert_eq!(loaded.notes, "test");
    }
}
