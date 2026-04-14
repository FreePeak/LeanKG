use crate::db::models::{BusinessLogic, CodeElement, Relationship, DocLink, TraceabilityEntry, TraceabilityReport};
use crate::db::schema::CozoDb;
use crate::graph::cache::QueryCache;
use serde::{Deserialize, Serialize};
use std::sync::Arc;
use parking_lot::RwLock;
use tracing::debug;

fn escape_datalog(s: &str) -> String {
    s.replace('\\', "\\\\").replace('"', "\\\"")
}

fn normalize_path(path: &str) -> String {
    path.strip_prefix("./").unwrap_or(path).to_string()
}

#[derive(Clone)]
pub struct GraphEngine {
    db: CozoDb,
    cache: QueryCache,
}

impl GraphEngine {
    pub fn new(db: CozoDb) -> Self {
        Self {
            db,
            cache: QueryCache::new(300, 1000),
        }
    }

    #[allow(dead_code)]
    pub fn with_cache(db: CozoDb, cache: QueryCache) -> Self {
        Self {
            db,
            cache,
        }
    }

    pub fn with_persistence(db: CozoDb) -> Self {
        let db_arc = Arc::new(db);
        let cache = QueryCache::with_persistence(db_arc.clone(), 300, 1000);
        Self {
            db: (*db_arc).clone(),
            cache,
        }
    }

    pub fn db(&self) -> &CozoDb {
        &self.db
    }

    pub fn find_element(
        &self,
        qualified_name: &str,
    ) -> Result<Option<CodeElement>, Box<dyn std::error::Error>> {
        let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], qualified_name = $qn"#;
        let mut params = std::collections::BTreeMap::new();
        params.insert("qn".to_string(), serde_json::Value::String(qualified_name.to_string()));
        let result = self.db.run_script(query, params)?;
        let rows = result.rows;

        if rows.is_empty() {
            return Ok(None);
        }

        let row = &rows[0];
        let parent_qualified = row[7].as_str().map(String::from);
        let cluster_id = row[8].as_str().map(String::from);
        let cluster_label = row[9].as_str().map(String::from);
        let metadata_str = row[10].as_str().unwrap_or("{}");
        
        Ok(Some(CodeElement {
            qualified_name: row[0].as_str().unwrap_or("").to_string(),
            element_type: row[1].as_str().unwrap_or("").to_string(),
            name: row[2].as_str().unwrap_or("").to_string(),
            file_path: row[3].as_str().unwrap_or("").to_string(),
            line_start: row[4].as_i64().unwrap_or(0) as u32,
            line_end: row[5].as_i64().unwrap_or(0) as u32,
            language: row[6].as_str().unwrap_or("").to_string(),
            parent_qualified,
            cluster_id,
            cluster_label,
            metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
            ..Default::default()
        }))
    }

    #[allow(dead_code)]
    pub fn find_element_by_name(
        &self,
        name: &str,
    ) -> Result<Option<CodeElement>, Box<dyn std::error::Error>> {
        let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], name = $nm"#;
        let mut params = std::collections::BTreeMap::new();
        params.insert("nm".to_string(), serde_json::Value::String(name.to_string()));
        let result = self.db.run_script(query, params)?;
        let rows = result.rows;

        if rows.is_empty() {
            return Ok(None);
        }

        let row = &rows[0];
        let parent_qualified = row[7].as_str().map(String::from);
        let cluster_id = row[8].as_str().map(String::from);
        let cluster_label = row[9].as_str().map(String::from);
        let metadata_str = row[10].as_str().unwrap_or("{}");
        
        Ok(Some(CodeElement {
            qualified_name: row[0].as_str().unwrap_or("").to_string(),
            element_type: row[1].as_str().unwrap_or("").to_string(),
            name: row[2].as_str().unwrap_or("").to_string(),
            file_path: row[3].as_str().unwrap_or("").to_string(),
            line_start: row[4].as_i64().unwrap_or(0) as u32,
            line_end: row[5].as_i64().unwrap_or(0) as u32,
            language: row[6].as_str().unwrap_or("").to_string(),
            parent_qualified,
            cluster_id,
            cluster_label,
            metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
            ..Default::default()
        }))
    }

    pub fn get_dependencies(
        &self,
        file_path: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let normalized = normalize_path(file_path);
        let escaped_normalized = escape_datalog(&normalized);

        let cache = self.cache.clone();
        let cache_key = normalized.clone();

        let cached_qns = crate::runtime::run_blocking(async { cache.get_dependencies(&cache_key).await });

        if let Some(cached_qns) = cached_qns {
            let mut elements = Vec::new();
            for qn in &cached_qns {
                if let Some(elem) = self.find_element(qn)? {
                    elements.push(elem);
                }
            }
            if !elements.is_empty() {
                tracing::debug!("get_dependencies cache hit for {}", file_path);
                return Ok(elements);
            }
        }

        let query = r#"?[target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata], (source_qualified = $sq1 or source_qualified = $sq2), rel_type = "imports""#;
        let mut params = std::collections::BTreeMap::new();
        params.insert("sq1".to_string(), serde_json::Value::String(normalized.clone()));
        params.insert("sq2".to_string(), serde_json::Value::String(format!("./{}", normalized)));

        let result = self.db.run_script(query, params)?;
        let rows = result.rows;

        let target_qns: Vec<String> = rows
            .iter()
            .map(|row| row[0].as_str().unwrap_or("").to_string())
            .filter(|s| !s.is_empty())
            .collect();

        let mut elements = Vec::new();
        for qn in &target_qns {
            if let Some(elem) = self.find_element(qn)? {
                elements.push(elem);
            }
        }

        if !elements.is_empty() {
            let qns: Vec<String> = elements.iter().map(|e| e.qualified_name.clone()).collect();
            let db_path = normalize_path(file_path);
            let cache = self.cache.clone();
            crate::runtime::get_runtime().spawn(async move {
                cache.set_dependencies(db_path, qns).await;
            });
        }

        Ok(elements)
    }

    pub fn get_relationships(
        &self,
        source: &str,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let normalized = normalize_path(source);
        let query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata], (source_qualified = $sq1 or source_qualified = $sq2)"#;
        let mut params = std::collections::BTreeMap::new();
        params.insert("sq1".to_string(), serde_json::Value::String(normalized.clone()));
        params.insert("sq2".to_string(), serde_json::Value::String(format!("./{}", normalized)));

        let result = self.db.run_script(query, params)?;
        let rows = result.rows;

        let relationships: Vec<Relationship> = rows
            .iter()
            .map(|row| {
                let metadata_str = row[4].as_str().unwrap_or("{}");
                Relationship {
                    id: None,
                    source_qualified: row[0].as_str().unwrap_or("").to_string(),
                    target_qualified: row[1].as_str().unwrap_or("").to_string(),
                    rel_type: row[2].as_str().unwrap_or("").to_string(),
                    confidence: row[3].as_f64().unwrap_or(1.0),
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                }
            })
            .collect();

        Ok(relationships)
    }

    pub fn get_relationships_for_target(
        &self,
        target: &str,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let normalized = normalize_path(target);
        let escaped_normalized = escape_datalog(&normalized);

        let cache = self.cache.clone();
        let cache_key = normalized.clone();

        let cached_source_qns = crate::runtime::run_blocking(async { cache.get_dependents(&cache_key).await });

        if let Some(cached_source_qns) = cached_source_qns {
            if !cached_source_qns.is_empty() {
                tracing::debug!("get_relationships_for_target cache hit for {}", target);
                let relationships: Vec<Relationship> = cached_source_qns
                    .iter()
                    .map(|source_qn| Relationship {
                        id: None,
                        source_qualified: source_qn.clone(),
                        target_qualified: target.to_string(),
                        rel_type: "imports".to_string(),
                        confidence: 1.0,
                        metadata: serde_json::json!({}),
                    })
                    .collect();
                return Ok(relationships);
            }
        }

        let query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata], (target_qualified = $tq1 or target_qualified = $tq2)"#;
        let mut params = std::collections::BTreeMap::new();
        params.insert("tq1".to_string(), serde_json::Value::String(normalized.clone()));
        params.insert("tq2".to_string(), serde_json::Value::String(format!("./{}", normalized)));

        let result = self.db.run_script(query, params)?;
        let rows = result.rows;

        let relationships: Vec<Relationship> = rows
            .iter()
            .map(|row| {
                let metadata_str = row[4].as_str().unwrap_or("{}");
                Relationship {
                    id: None,
                    source_qualified: row[0].as_str().unwrap_or("").to_string(),
                    target_qualified: row[1].as_str().unwrap_or("").to_string(),
                    rel_type: row[2].as_str().unwrap_or("").to_string(),
                    confidence: row[3].as_f64().unwrap_or(1.0),
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                }
            })
            .collect();

        if !relationships.is_empty() {
            let qns: Vec<String> = relationships.iter().map(|r| r.target_qualified.clone()).collect();
            let cache = self.cache.clone();
            let t = target.to_string();
            crate::runtime::get_runtime().spawn(async move {
                cache.set_dependents(t, qns).await;
            });
        }

        Ok(relationships)
    }

    pub fn get_dependents(
        &self,
        target: &str,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        self.get_relationships_for_target(target)
    }

    pub fn run_raw_query(
        &self,
        query: &str,
        params: std::collections::BTreeMap<String, serde_json::Value>,
    ) -> Result<cozo::NamedRows, Box<dyn std::error::Error + Send + Sync>> {
        self.db.run_script(query, params).map_err(|e| {
            let msg = e.to_string();
            Box::new(std::io::Error::new(std::io::ErrorKind::Other, msg)) as Box<dyn std::error::Error + Send + Sync>
        })
    }

    pub fn all_elements(&self) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata]"#;

        let result = self.db.run_script(query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].as_str().map(String::from);
                let cluster_id = row[8].as_str().map(String::from);
                let cluster_label = row[9].as_str().map(String::from);
                let metadata_str = row[10].as_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].as_str().unwrap_or("").to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    name: row[2].as_str().unwrap_or("").to_string(),
                    file_path: row[3].as_str().unwrap_or("").to_string(),
                    line_start: row[4].as_i64().unwrap_or(0) as u32,
                    line_end: row[5].as_i64().unwrap_or(0) as u32,
                    language: row[6].as_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        Ok(elements)
    }

    pub fn all_relationships(&self) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata]"#;

        let result = self.db.run_script(query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let relationships: Vec<Relationship> = rows
            .iter()
            .map(|row| {
                let metadata_str = row[4].as_str().unwrap_or("{}");
                Relationship {
                    id: None,
                    source_qualified: row[0].as_str().unwrap_or("").to_string(),
                    target_qualified: row[1].as_str().unwrap_or("").to_string(),
                    rel_type: row[2].as_str().unwrap_or("").to_string(),
                    confidence: row[3].as_f64().unwrap_or(1.0),
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                }
            })
            .collect();

        Ok(relationships)
    }

    #[allow(dead_code)]
    pub fn get_children(
        &self,
        parent_qualified: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], parent_qualified = $pq"#;
        let mut params = std::collections::BTreeMap::new();
        params.insert("pq".to_string(), serde_json::Value::String(parent_qualified.to_string()));

        let result = self.db.run_script(query, params)?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].as_str().map(String::from);
                let cluster_id = row[8].as_str().map(String::from);
                let cluster_label = row[9].as_str().map(String::from);
                let metadata_str = row[10].as_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].as_str().unwrap_or("").to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    name: row[2].as_str().unwrap_or("").to_string(),
                    file_path: row[3].as_str().unwrap_or("").to_string(),
                    line_start: row[4].as_i64().unwrap_or(0) as u32,
                    line_end: row[5].as_i64().unwrap_or(0) as u32,
                    language: row[6].as_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        Ok(elements)
    }

    pub fn get_annotation(
        &self,
        element_qualified: &str,
    ) -> Result<Option<BusinessLogic>, Box<dyn std::error::Error>> {
        let query = r#"?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id], element_qualified = $eq"#;
        let mut params = std::collections::BTreeMap::new();
        params.insert("eq".to_string(), serde_json::Value::String(element_qualified.to_string()));

        let result = self.db.run_script(query, params)?;
        let rows = result.rows;

        if rows.is_empty() {
            return Ok(None);
        }

        let row = &rows[0];
        Ok(Some(BusinessLogic {
            id: None,
            element_qualified: row[0].as_str().unwrap_or("").to_string(),
            description: row[1].as_str().unwrap_or("").to_string(),
            user_story_id: row[2].as_str().map(String::from),
            feature_id: row[3].as_str().map(String::from),
        }))
    }

    #[allow(dead_code)]
    pub fn search_annotations(
        &self,
        query_str: &str,
    ) -> Result<Vec<BusinessLogic>, Box<dyn std::error::Error>> {
        let safe_pattern = escape_datalog(&query_str.to_lowercase());
        let query = format!(
            r#"?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id], regex_matches(lowercase(description), ".*{safe_pattern}.*")"#,
            safe_pattern = safe_pattern
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let annotations: Vec<BusinessLogic> = rows
            .iter()
            .map(|row| BusinessLogic {
                id: None,
                element_qualified: row[0].as_str().unwrap_or("").to_string(),
                description: row[1].as_str().unwrap_or("").to_string(),
                user_story_id: row[2].as_str().map(String::from),
                feature_id: row[3].as_str().map(String::from),
            })
            .collect();

        Ok(annotations)
    }

    pub fn all_annotations(&self) -> Result<Vec<BusinessLogic>, Box<dyn std::error::Error>> {
        let query = r#"?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id]"#;

        let result = self.db.run_script(query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let annotations: Vec<BusinessLogic> = rows
            .iter()
            .map(|row| BusinessLogic {
                id: None,
                element_qualified: row[0].as_str().unwrap_or("").to_string(),
                description: row[1].as_str().unwrap_or("").to_string(),
                user_story_id: row[2].as_str().map(String::from),
                feature_id: row[3].as_str().map(String::from),
            })
            .collect();

        Ok(annotations)
    }

    pub fn get_documented_by(&self, element_qualified: &str) -> Result<Vec<DocLink>, Box<dyn std::error::Error>> {
        let normalized = normalize_path(element_qualified);
        let query = r#"?[source_qualified, target_qualified, rel_type, metadata] := *relationships[source_qualified, target_qualified, rel_type, metadata], (source_qualified = $sq1 or source_qualified = $sq2), rel_type = "documented_by""#;
        let mut params = std::collections::BTreeMap::new();
        params.insert("sq1".to_string(), serde_json::Value::String(normalized.clone()));
        params.insert("sq2".to_string(), serde_json::Value::String(format!("./{}", normalized)));

        let result = self.db.run_script(query, params)?;
        let rows = result.rows;

        let doc_links: Vec<DocLink> = rows
            .iter()
            .filter_map(|row| {
                let doc_qualified = row[1].as_str().unwrap_or("").to_string();
                let _rel_type = row[2].as_str().unwrap_or("");
                let metadata_str = row.get(3).and_then(|v| v.as_str()).unwrap_or("{}");
                let metadata: serde_json::Value = serde_json::from_str(metadata_str).ok()?;

                let doc_title = metadata.get("title")
                    .and_then(|v| v.as_str())
                    .unwrap_or("Untitled")
                    .to_string();
                let context = metadata.get("context")
                    .and_then(|v| v.as_str())
                    .map(String::from);

                Some(DocLink {
                    doc_qualified,
                    doc_title,
                    context,
                })
            })
            .collect();

        Ok(doc_links)
    }

    pub fn get_traceability_report(&self, element_qualified: &str) -> Result<TraceabilityReport, Box<dyn std::error::Error>> {
        let bl = self.get_annotation(element_qualified)?;
        let doc_links = self.get_documented_by(element_qualified)?;

        let entry = TraceabilityEntry {
            element_qualified: element_qualified.to_string(),
            description: bl.as_ref().map(|b| b.description.clone()).unwrap_or_default(),
            user_story_id: bl.as_ref().and_then(|b| b.user_story_id.clone()),
            feature_id: bl.as_ref().and_then(|b| b.feature_id.clone()),
            doc_links,
        };

        Ok(TraceabilityReport {
            element_qualified: element_qualified.to_string(),
            entries: vec![entry],
            count: 1,
        })
    }

    pub fn get_code_for_requirement(&self, requirement_id: &str) -> Result<Vec<TraceabilityEntry>, Box<dyn std::error::Error>> {
        let bl_entries = self.get_business_logic_by_user_story(requirement_id)?;

        let mut entries = Vec::new();
        for bl in bl_entries {
            let doc_links = self.get_documented_by(&bl.element_qualified)?;

            entries.push(TraceabilityEntry {
                element_qualified: bl.element_qualified,
                description: bl.description,
                user_story_id: bl.user_story_id,
                feature_id: bl.feature_id,
                doc_links,
            });
        }

        Ok(entries)
    }

    pub fn get_business_logic_by_user_story(&self, user_story_id: &str) -> Result<Vec<BusinessLogic>, Box<dyn std::error::Error>> {
        let query = r#"?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id], user_story_id = $uid"#;
        let mut params = std::collections::BTreeMap::new();
        params.insert("uid".to_string(), serde_json::Value::String(user_story_id.to_string()));

        let result = self.db.run_script(query, params)?;
        let rows = result.rows;

        let business_logic: Vec<BusinessLogic> = rows
            .iter()
            .map(|row| {
                BusinessLogic {
                    id: None,
                    element_qualified: row[0].as_str().unwrap_or("").to_string(),
                    description: row[1].as_str().unwrap_or("").to_string(),
                    user_story_id: row[2].as_str().map(String::from),
                    feature_id: row[3].as_str().map(String::from),
                }
            })
            .collect();

        Ok(business_logic)
    }

    pub fn insert_elements(
        &self,
        elements: &[CodeElement],
    ) -> Result<(), Box<dyn std::error::Error>> {
        if elements.is_empty() {
            return Ok(());
        }

        let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] <- $batch_data :put code_elements { qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata }"#;

        let batch_data: Vec<serde_json::Value> = elements.iter().map(|element| {
            let metadata_str = serde_json::to_string(&element.metadata).unwrap_or_else(|_| "{}".to_string());
            serde_json::json!([
                element.qualified_name.clone(),
                element.element_type.clone(),
                element.name.clone(),
                element.file_path.clone(),
                element.line_start as i64,
                element.line_end as i64,
                element.language.clone(),
                element.parent_qualified.clone(),
                element.cluster_id.clone(),
                element.cluster_label.clone(),
                metadata_str
            ])
        }).collect();

        for chunk in batch_data.chunks(1000) {
            let mut params = std::collections::BTreeMap::new();
            params.insert("batch_data".to_string(), serde_json::Value::Array(chunk.to_vec()));
            self.db.run_script(query, params)?;
        }

        let mut unique_files = std::collections::HashSet::new();
        for element in elements {
            unique_files.insert(element.file_path.clone());
        }

        for fp in unique_files {
            let cache = self.cache.clone();
            crate::runtime::get_runtime().spawn(async move {
                cache.invalidate_file(&fp).await;
            });
        }

        Ok(())
    }

    pub fn insert_element(
        &self,
        element: &CodeElement,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let metadata_str = serde_json::to_string(&element.metadata)?;
        let mut params = std::collections::BTreeMap::new();
        params.insert("qn".to_string(), serde_json::Value::String(element.qualified_name.clone()));
        params.insert("et".to_string(), serde_json::Value::String(element.element_type.clone()));
        params.insert("nm".to_string(), serde_json::Value::String(element.name.clone()));
        params.insert("fp".to_string(), serde_json::Value::String(element.file_path.clone()));
        params.insert("ls".to_string(), serde_json::Value::Number(element.line_start.into()));
        params.insert("le".to_string(), serde_json::Value::Number(element.line_end.into()));
        params.insert("lg".to_string(), serde_json::Value::String(element.language.clone()));
        match &element.parent_qualified {
            Some(pq) => params.insert("pq".to_string(), serde_json::Value::String(pq.clone())),
            None => params.insert("pq".to_string(), serde_json::Value::Null),
        };
        match &element.cluster_id {
            Some(cid) => params.insert("cid".to_string(), serde_json::Value::String(cid.clone())),
            None => params.insert("cid".to_string(), serde_json::Value::Null),
        };
        match &element.cluster_label {
            Some(cl) => params.insert("cl".to_string(), serde_json::Value::String(cl.clone())),
            None => params.insert("cl".to_string(), serde_json::Value::Null),
        };
        params.insert("md".to_string(), serde_json::Value::String(metadata_str));

        let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] <- [[ $qn, $et, $nm, $fp, $ls, $le, $lg, $pq, $cid, $cl, $md ]] :put code_elements { qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata }"#;

        self.db.run_script(query, params)?;

        let cache = self.cache.clone();
        let fp = element.file_path.clone();
        crate::runtime::get_runtime().spawn(async move {
                cache.invalidate_file(&fp).await;
            });

        Ok(())
    }

    pub fn update_element_cluster(
        &self,
        qualified_name: &str,
        cluster_id: Option<String>,
        cluster_label: Option<String>,
    ) -> Result<(), Box<dyn std::error::Error>> {
        if let Some(mut element) = self.find_element(qualified_name)? {
            // Remove the specific original element securely
            let query = r#"
                ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] :=
                    *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], qualified_name = $qn
                :rm code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata}
            "#;
            let mut params = std::collections::BTreeMap::new();
            params.insert("qn".to_string(), serde_json::Value::String(qualified_name.to_string()));
            self.db.run_script(query, params)?;

            // Apply new cluster attributes and natively reinsert mapped into caches and DB
            element.cluster_id = cluster_id;
            element.cluster_label = cluster_label;
            self.insert_elements(&[element])?;
        }
        Ok(())
    }

    pub fn insert_relationship(
        &self,
        relationship: &Relationship,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let metadata_str = serde_json::to_string(&relationship.metadata)?;
        let mut params = std::collections::BTreeMap::new();
        params.insert("sq".to_string(), serde_json::Value::String(relationship.source_qualified.clone()));
        params.insert("tq".to_string(), serde_json::Value::String(relationship.target_qualified.clone()));
        params.insert("rt".to_string(), serde_json::Value::String(relationship.rel_type.clone()));
        params.insert("cn".to_string(), serde_json::json!(relationship.confidence));
        params.insert("md".to_string(), serde_json::Value::String(metadata_str));

        let query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] <- [[ $sq, $tq, $rt, $cn, $md ]] :put relationships { source_qualified, target_qualified, rel_type, confidence, metadata }"#;

        self.db.run_script(query, params)?;

        Ok(())
    }

    pub fn insert_relationships(
        &self,
        relationships: &[Relationship],
    ) -> Result<(), Box<dyn std::error::Error>> {
        if relationships.is_empty() {
            return Ok(());
        }

        let query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] <- $batch_data :put relationships { source_qualified, target_qualified, rel_type, confidence, metadata }"#;

        let batch_data: Vec<serde_json::Value> = relationships.iter().map(|rel| {
            let metadata_str = serde_json::to_string(&rel.metadata).unwrap_or_else(|_| "{}".to_string());
            serde_json::json!([
                rel.source_qualified.clone(),
                rel.target_qualified.clone(),
                rel.rel_type.clone(),
                rel.confidence,
                metadata_str
            ])
        }).collect();

        for chunk in batch_data.chunks(1000) {
            let mut params = std::collections::BTreeMap::new();
            params.insert("batch_data".to_string(), serde_json::Value::Array(chunk.to_vec()));
            self.db.run_script(query, params)?;
        }

        let mut unique_sources = std::collections::HashSet::new();
        for rel in relationships {
            unique_sources.insert(rel.source_qualified.clone());
        }

        for source in unique_sources {
            let cache = self.cache.clone();
            crate::runtime::get_runtime().spawn(async move {
                cache.invalidate_file(&source).await;
            });
        }

        Ok(())
    }

    pub fn remove_elements_by_file(
        &self,
        file_path: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let query = r#"
            ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] :=
                *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], file_path = $fp
            :rm code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata}
        "#;
        let mut params = std::collections::BTreeMap::new();
        params.insert("fp".to_string(), serde_json::Value::String(file_path.to_string()));
        
        self.db.run_script(query, params)?;

        let cache = self.cache.clone();
        let fp = file_path.to_string();
        crate::runtime::get_runtime().spawn(async move {
                cache.invalidate_file(&fp).await;
            });
        
        Ok(())
    }

    pub fn remove_relationships_by_source(
        &self,
        source: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let query = r#"
            ?[source_qualified, target_qualified, rel_type, confidence, metadata] :=
                *relationships[source_qualified, target_qualified, rel_type, confidence, metadata], source_qualified = $sq
            :rm relationships {source_qualified, target_qualified, rel_type, confidence, metadata}
        "#;
        let mut params = std::collections::BTreeMap::new();
        params.insert("sq".to_string(), serde_json::Value::String(source.to_string()));
        
        self.db.run_script(query, params)?;

        let cache = self.cache.clone();
        let s = source.to_string();
        crate::runtime::get_runtime().spawn(async move {
                cache.invalidate_file(&s).await;
            });
        
        Ok(())
    }

    pub fn get_elements_by_file(
        &self,
        file_path: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], file_path = $fp"#;
        let mut params = std::collections::BTreeMap::new();
        params.insert("fp".to_string(), serde_json::Value::String(file_path.to_string()));

        let result = self.db.run_script(query, params)?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].as_str().map(String::from);
                let cluster_id = row[8].as_str().map(String::from);
                let cluster_label = row[9].as_str().map(String::from);
                let metadata_str = row[10].as_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].as_str().unwrap_or("").to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    name: row[2].as_str().unwrap_or("").to_string(),
                    file_path: row[3].as_str().unwrap_or("").to_string(),
                    line_start: row[4].as_i64().unwrap_or(0) as u32,
                    line_end: row[5].as_i64().unwrap_or(0) as u32,
                    language: row[6].as_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        Ok(elements)
    }

    pub fn search_by_name(
        &self,
        name: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let safe_name = escape_datalog(&name.to_lowercase());
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], regex_matches(lowercase(name), ".*{safe_name}.*")"#,
            safe_name = safe_name
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].as_str().map(String::from);
                let cluster_id = row[8].as_str().map(String::from);
                let cluster_label = row[9].as_str().map(String::from);
                let metadata_str = row[10].as_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].as_str().unwrap_or("").to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    name: row[2].as_str().unwrap_or("").to_string(),
                    file_path: row[3].as_str().unwrap_or("").to_string(),
                    line_start: row[4].as_i64().unwrap_or(0) as u32,
                    line_end: row[5].as_i64().unwrap_or(0) as u32,
                    language: row[6].as_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        Ok(elements)
    }

    pub fn search_by_type(
        &self,
        element_type: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], element_type = "{}""#,
            element_type
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].as_str().map(String::from);
                let cluster_id = row[8].as_str().map(String::from);
                let cluster_label = row[9].as_str().map(String::from);
                let metadata_str = row[10].as_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].as_str().unwrap_or("").to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    name: row[2].as_str().unwrap_or("").to_string(),
                    file_path: row[3].as_str().unwrap_or("").to_string(),
                    line_start: row[4].as_i64().unwrap_or(0) as u32,
                    line_end: row[5].as_i64().unwrap_or(0) as u32,
                    language: row[6].as_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        Ok(elements)
    }

    pub fn search_by_pattern(
        &self,
        pattern: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := 
            *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], 
            str_includes(lowercase(qualified_name), lowercase($pattern))"#;
            
        let mut params = std::collections::BTreeMap::new();
        params.insert("pattern".to_string(), serde_json::Value::String(pattern.to_string()));

        let result = self.db.run_script(query, params)?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].as_str().map(String::from);
                let cluster_id = row[8].as_str().map(String::from);
                let cluster_label = row[9].as_str().map(String::from);
                let metadata_str = row[10].as_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].as_str().unwrap_or("").to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    name: row[2].as_str().unwrap_or("").to_string(),
                    file_path: row[3].as_str().unwrap_or("").to_string(),
                    line_start: row[4].as_i64().unwrap_or(0) as u32,
                    line_end: row[5].as_i64().unwrap_or(0) as u32,
                    language: row[6].as_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        Ok(elements)
    }

    pub fn search_by_relation_type(
        &self,
        rel_type: &str,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let escaped = escape_datalog(rel_type);
        let query = format!(
            r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata], rel_type = "{}""#,
            escaped
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let relationships: Vec<Relationship> = rows
            .iter()
            .map(|row| {
                let metadata_str = row[4].as_str().unwrap_or("{}");
                Relationship {
                    id: None,
                    source_qualified: row[0].as_str().unwrap_or("").to_string(),
                    target_qualified: row[1].as_str().unwrap_or("").to_string(),
                    rel_type: row[2].as_str().unwrap_or("").to_string(),
                    confidence: row[3].as_f64().unwrap_or(1.0),
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                }
            })
            .collect();

        Ok(relationships)
    }

    pub fn find_oversized_functions(
        &self,
        min_lines: u32,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], element_type = "function", (line_end - line_start + 1) >= {}"#,
            min_lines
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let mut elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].as_str().map(String::from);
                let cluster_id = row[8].as_str().map(String::from);
                let cluster_label = row[9].as_str().map(String::from);
                let metadata_str = row[10].as_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].as_str().unwrap_or("").to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    name: row[2].as_str().unwrap_or("").to_string(),
                    file_path: row[3].as_str().unwrap_or("").to_string(),
                    line_start: row[4].as_i64().unwrap_or(0) as u32,
                    line_end: row[5].as_i64().unwrap_or(0) as u32,
                    language: row[6].as_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        elements.sort_by(|a, b| {
            let a_lines = a.line_end - a.line_start + 1;
            let b_lines = b.line_end - b.line_start + 1;
            b_lines.cmp(&a_lines)
        });

        Ok(elements)
    }

    pub fn find_oversized_functions_by_lang(
        &self,
        min_lines: u32,
        language: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], element_type = "function", language = "{}", (line_end - line_start + 1) >= {}"#,
            language,
            min_lines
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let mut elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].as_str().map(String::from);
                let cluster_id = row[8].as_str().map(String::from);
                let cluster_label = row[9].as_str().map(String::from);
                let metadata_str = row[10].as_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].as_str().unwrap_or("").to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    name: row[2].as_str().unwrap_or("").to_string(),
                    file_path: row[3].as_str().unwrap_or("").to_string(),
                    line_start: row[4].as_i64().unwrap_or(0) as u32,
                    line_end: row[5].as_i64().unwrap_or(0) as u32,
                    language: row[6].as_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        elements.sort_by(|a, b| {
            let a_lines = a.line_end - a.line_start + 1;
            let b_lines = b.line_end - b.line_start + 1;
            b_lines.cmp(&a_lines)
        });

        Ok(elements)
    }

    fn run_element_query(
        &self,
        query: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let result = self.db.run_script(query, Default::default())?;
        Ok(result.rows.iter().map(|row| {
            let parent_qualified = row[7].as_str().map(String::from);
            let cluster_id = row[8].as_str().map(String::from);
            let cluster_label = row[9].as_str().map(String::from);
            let metadata_str = row[10].as_str().unwrap_or("{}");
            CodeElement {
                qualified_name: row[0].as_str().unwrap_or("").to_string(),
                element_type: row[1].as_str().unwrap_or("").to_string(),
                name: row[2].as_str().unwrap_or("").to_string(),
                file_path: row[3].as_str().unwrap_or("").to_string(),
                line_start: row[4].as_i64().unwrap_or(0) as u32,
                line_end: row[5].as_i64().unwrap_or(0) as u32,
                language: row[6].as_str().unwrap_or("").to_string(),
                parent_qualified,
                cluster_id,
                cluster_label,
                metadata: serde_json::from_str(metadata_str)
                    .unwrap_or(serde_json::json!({})),
                ..Default::default()
            }
        }).collect())
    }

    pub fn search_by_name_typed(
        &self,
        name: &str,
        element_type: Option<&str>,
        limit: usize,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let safe_name = escape_datalog(&name.to_lowercase());
        let (filter_clause, has_type_filter) = match element_type {
            Some(t) => (format!(r#", element_type = "{}""#, escape_datalog(t)), true),
            None => (String::new(), false),
        };
        let query = if has_type_filter {
            format!(
                r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata]
                   := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata]{filter_clause},
                  regex_matches(lowercase(name), "{pattern}")
               :limit {limit}"#,
                filter_clause = filter_clause,
                pattern = safe_name,
                limit = limit,
            )
        } else {
            format!(
                r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata]
                   := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata],
                  regex_matches(lowercase(name), "{pattern}")
               :limit {limit}"#,
                pattern = safe_name,
                limit = limit,
            )
        };
        self.run_element_query(&query)
    }

    #[allow(dead_code)]
    pub fn find_elements_by_name_exact(
        &self,
        name: &str,
        element_type: Option<&str>,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let safe_name = escape_datalog(name);
        let type_clause = match element_type {
            Some(t) => format!(r#", element_type = "{}""#, escape_datalog(t)),
            None => String::new(),
        };
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata]
               := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata]{type_clause},
              name = "{name}"
           :limit 20"#,
            type_clause = type_clause,
            name = safe_name,
        );
        self.run_element_query(&query)
    }

    pub fn get_callers(
        &self,
        function_name: &str,
        file_scope: Option<&str>,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let safe_name = escape_datalog(function_name);
        
        let file_filter = match file_scope {
            Some(f) => format!(r#", regex_matches(file_path, ".*{}.*")"#, escape_datalog(f)),
            None => String::new(),
        };

        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] :=
               *relationships[qualified_name, target_qualified, "calls", _, _],
               regex_matches(target_qualified, ".*{function_name}.*"),
               *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata]{file_filter}
               :limit 50"#,
            function_name = safe_name,
            file_filter = file_filter
        );
        self.run_element_query(&query)
    }


    pub fn get_call_graph_bounded(
        &self,
        source_qualified: &str,
        max_depth: u32,
        max_results: usize,
    ) -> Result<Vec<(String, String, u32)>, Box<dyn std::error::Error>> {
        let normalized = normalize_path(source_qualified);
        let safe_src = escape_datalog(&normalized);
        let query = match max_depth {
            1 => format!(
                r#"?[src, tgt, depth] :=
                   *relationships[src, tgt, "calls", _, _],
                   (src = "{}" or src = "./{}"), depth = 1
                   :limit {limit}"#,
                safe_src, safe_src, limit = max_results,
            ),
            2 => format!(
                r#"hop1[src, tgt] := *relationships[src, tgt, "calls", _, _], (src = "{}" or src = "./{}")
                   hop2[src2, tgt2] := hop1[_, src2], *relationships[src2, tgt2, "calls", _, _]
                   ?[src, tgt, depth] := hop1[src, tgt], depth = 1
                   ?[src, tgt, depth] := hop2[src, tgt], depth = 2
                   :limit {limit}"#,
                safe_src, safe_src, limit = max_results,
            ),
            _ => format!(
                r#"hop1[src, tgt] := *relationships[src, tgt, "calls", _, _], (src = "{}" or src = "./{}")
                   hop2[s2, t2] := hop1[_, s2], *relationships[s2, t2, "calls", _, _]
                   hop3[s3, t3] := hop2[_, s3], *relationships[s3, t3, "calls", _, _]
                   ?[src, tgt, depth] := hop1[src, tgt], depth = 1
                   ?[src, tgt, depth] := hop2[src, tgt], depth = 2
                   ?[src, tgt, depth] := hop3[src, tgt], depth = 3
                   :limit {limit}"#,
                safe_src, safe_src, limit = max_results,
            ),
        };

        let result = self.db.run_script(&query, Default::default())?;
        Ok(result.rows.iter().filter_map(|row| {
            Some((
                row[0].as_str()?.to_string(),
                row[1].as_str()?.to_string(),
                row[2].as_i64()? as u32,
            ))
        }).collect())
    }

    pub fn resolve_call_edges(&self) -> Result<usize, Box<dyn std::error::Error>> {
        let query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata], rel_type = "calls""#;
        debug!("Running resolve_call_edges query (filtered at DB level)");
        let result = self.db.run_script(query, std::collections::BTreeMap::new())?;
        
        let unresolved_rows: Vec<_> = result.rows.iter()
            .filter(|row| {
                let target = row[1].as_str().unwrap_or("");
                target.starts_with("__unresolved__")
            })
            .collect();
        
        let total_unresolved = unresolved_rows.len();
        debug!("Found {} unresolved call edges to resolve", total_unresolved);
        
        if total_unresolved == 0 {
            return Ok(0);
        }

        debug!("Loading all functions into memory for fast lookup...");
        let functions_query = r#"?[qualified_name, name, file_path] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], element_type = "function""#;
        let func_result = self.db.run_script(functions_query, std::collections::BTreeMap::new())?;
        
        let mut by_name_and_file: std::collections::HashMap<(String, String), (String, f64)> = std::collections::HashMap::new();
        let mut by_name: std::collections::HashMap<String, (String, f64)> = std::collections::HashMap::new();
        
        for row in &func_result.rows {
            let qn = row[0].as_str().unwrap_or("").to_string();
            let name = row[1].as_str().unwrap_or("").to_string();
            let file_path = row[2].as_str().unwrap_or("").to_string();
            if !qn.is_empty() && !name.is_empty() {
                by_name_and_file.insert((name.clone(), file_path.clone()), (qn.clone(), 1.0));
                if !by_name.contains_key(&name) {
                    by_name.insert(name.clone(), (qn.clone(), 0.7));
                }
            }
        }
        debug!("Loaded {} functions into memory", by_name.len());

        let mut to_insert: Vec<Relationship> = Vec::new();
        let mut to_delete_keys: Vec<[serde_json::Value; 5]> = Vec::new();

        for row in unresolved_rows.iter() {
            let source = row[0].as_str().unwrap_or("").to_string();
            let target_qualified = row[1].as_str().unwrap_or("");
            let meta_str = row[4].as_str().unwrap_or("{}");
            
            let bare_name = target_qualified.trim_start_matches("__unresolved__").to_string();

            let callee_file_hint: Option<String> = serde_json::from_str::<serde_json::Value>(meta_str)
                .ok()
                .and_then(|m| m.get("callee_file_hint").cloned())
                .and_then(|v| v.as_str().map(String::from));

            let target_qn = if let Some(hint) = &callee_file_hint {
                by_name_and_file.get(&(bare_name.clone(), hint.clone()))
                    .map(|(qn, _)| qn.clone())
                    .or_else(|| by_name.get(&bare_name).map(|(qn, _)| qn.clone()))
                    .unwrap_or_else(|| bare_name.clone())
            } else {
                by_name.get(&bare_name)
                    .map(|(qn, _)| qn.clone())
                    .unwrap_or_else(|| bare_name.clone())
            };

            to_delete_keys.push([
                serde_json::Value::String(source.clone()),
                serde_json::Value::String(target_qualified.to_string()),
                serde_json::Value::String("calls".to_string()),
                row[3].clone(),
                row[4].clone(),
            ]);
            to_insert.push(Relationship {
                id: None,
                source_qualified: source,
                target_qualified: target_qn,
                rel_type: "calls".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({}),
            });
        }

        self._batch_delete_unresolved_calls(&to_delete_keys)?;
        self.insert_relationships(&to_insert)?;
        
        debug!("Resolved {} call edges", to_insert.len());

        Ok(to_insert.len())
    }

    fn _batch_delete_unresolved_calls(
        &self,
        keys: &[[serde_json::Value; 5]],
    ) -> Result<(), Box<dyn std::error::Error>> {
        if keys.is_empty() {
            return Ok(());
        }
        for chunk in keys.chunks(1000) {
            let batch_data: Vec<serde_json::Value> = chunk
                .iter()
                .map(|row| serde_json::Value::Array(row.to_vec()))
                .collect();

            let query = r#"
                ?[source_qualified, target_qualified, rel_type, confidence, metadata] <-
                    $batch_data
                :rm relationships {source_qualified, target_qualified, rel_type, confidence, metadata}
            "#;
            let mut params = std::collections::BTreeMap::new();
            params.insert("batch_data".to_string(), serde_json::Value::Array(batch_data));
            self.db.run_script(query, params)?;
        }
        Ok(())
    }

    #[allow(dead_code)]
    fn find_function_by_name_with_confidence(&self, name: &str, file_hint: Option<&str>) -> Result<(Option<String>, f64), Box<dyn std::error::Error>> {
        let safe_name = escape_datalog(name);
        
        if let Some(hint) = file_hint {
            let safe_hint = escape_datalog(hint);
            let query = format!("?[qualified_name, file_path] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], element_type = \"function\", name = \"{}\", file_path = \"{}\" :limit 1", safe_name, safe_hint);
            let result = self.db.run_script(&query, Default::default())?;
            if let Some(row) = result.rows.first() {
                let qn = row[0].as_str().map(String::from);
                let found_file = row[1].as_str().unwrap_or("");
                let confidence = if found_file == hint { 1.0 } else { 0.9 };
                return Ok((qn, confidence));
            }
        }

        let query = format!("?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], element_type = \"function\", name = \"{}\" :limit 1", safe_name);
        let result = self.db.run_script(&query, Default::default())?;
        Ok((result.rows.first().and_then(|row| row[0].as_str().map(String::from)), 0.7))
    }

    fn _delete_relationship(&self, source: &str, target: &str) -> Result<(), Box<dyn std::error::Error>> {
        let query = r#"
            ?[source_qualified, target_qualified, rel_type, confidence, metadata] :=
                *relationships[source_qualified, target_qualified, rel_type, confidence, metadata], source_qualified = $sq, target_qualified = $tq, rel_type = "calls"
            :rm relationships {source_qualified, target_qualified, rel_type, confidence, metadata}
        "#;
        let mut params = std::collections::BTreeMap::new();
        params.insert("sq".to_string(), serde_json::Value::String(source.to_string()));
        params.insert("tq".to_string(), serde_json::Value::String(target.to_string()));
        
        self.db.run_script(query, params)?;
        Ok(())
    }

    pub fn get_service_graph(
        &self,
        current_service: &str,
    ) -> Result<ServiceGraph, Box<dyn std::error::Error>> {
        let query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata], rel_type = "service_calls""#;
        let result = self.db.run_script(query, std::collections::BTreeMap::new())?;

        let mut service_connections: std::collections::HashMap<(String, String), Vec<serde_json::Value>> = std::collections::HashMap::new();
        let mut all_services: std::collections::HashSet<String> = std::collections::HashSet::new();

        all_services.insert(current_service.to_string());

        for row in &result.rows {
            let source = row[0].as_str().unwrap_or("").to_string();
            let target = row[1].as_str().unwrap_or("").to_string();
            let metadata_str = row[4].as_str().unwrap_or("{}");
            let metadata: serde_json::Value = serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({}));

            all_services.insert(source.clone());
            all_services.insert(target.clone());

            let key = (source.clone(), target.clone());
            service_connections.entry(key).or_default().push(metadata);
        }

        let mut nodes: Vec<ServiceNode> = Vec::new();
        let mut edges: Vec<ServiceEdge> = Vec::new();

        let current_lc = current_service.to_lowercase();
        for service in &all_services {
            let connection_count = service_connections.keys()
                .filter(|(s, t)| s == service || t == service)
                .map(|(_, targets)| targets)
                .count();

            let is_current = service.to_lowercase() == current_lc;
            let weight = if is_current { 10.0 } else { 1.0 + (connection_count as f64 * 0.5).min(5.0) };

            nodes.push(ServiceNode {
                id: service.clone(),
                label: service.clone(),
                is_current_service: is_current,
                weight,
                connection_count,
            });
        }

        nodes.sort_by(|a, b| {
            b.weight.partial_cmp(&a.weight).unwrap_or(std::cmp::Ordering::Equal)
        });

        let mut seen_edges: std::collections::HashSet<(String, String)> = std::collections::HashSet::new();
        for ((source, target), metas) in &service_connections {
            if seen_edges.contains(&(source.clone(), target.clone())) {
                continue;
            }
            seen_edges.insert((source.clone(), target.clone()));

            let protocols: std::collections::HashSet<String> = metas.iter()
                .filter_map(|m| m.get("protocol").and_then(|p| p.as_str()).map(String::from))
                .collect();

            edges.push(ServiceEdge {
                id: format!("{}_{}", source, target),
                source_id: source.clone(),
                target_id: target.clone(),
                call_count: metas.len(),
                protocols: protocols.into_iter().collect(),
                rel_type: "service_calls".to_string(),
            });
        }

        let total_connections = edges.len();

        Ok(ServiceGraph {
            nodes,
            edges,
            current_service: current_service.to_string(),
            total_services: all_services.len(),
            total_connections,
        })
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ServiceNode {
    pub id: String,
    pub label: String,
    pub is_current_service: bool,
    pub weight: f64,
    pub connection_count: usize,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ServiceEdge {
    pub id: String,
    pub source_id: String,
    pub target_id: String,
    pub call_count: usize,
    pub protocols: Vec<String>,
    pub rel_type: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ServiceGraph {
    pub nodes: Vec<ServiceNode>,
    pub edges: Vec<ServiceEdge>,
    pub current_service: String,
    pub total_services: usize,
    pub total_connections: usize,
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::schema::init_db;
    use crate::db::models::CodeElement;
    use tempfile::TempDir;

    fn make_test_engine() -> (GraphEngine, TempDir) {
        let tmp = TempDir::new().unwrap();
        let db_path = tmp.path().join("test.db");
        let db = init_db(&db_path).unwrap();
        let engine = GraphEngine::new(db);
        (engine, tmp)
    }

    fn insert_test_element(engine: &GraphEngine, name: &str, element_type: &str) {
        let elem = CodeElement {
            qualified_name: format!("src/test.rs::{}", name),
            element_type: element_type.to_string(),
            name: name.to_string(),
            file_path: "src/test.rs".to_string(),
            line_start: 1,
            line_end: 10,
            language: "rust".to_string(),
            ..Default::default()
        };
        engine.insert_element(&elem).unwrap();
    }

    #[test]
    fn test_search_by_name_finds_exact_match() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "my_function", "function");

        let results = engine.search_by_name("my_function").unwrap();
        assert!(!results.is_empty(), "search_by_name should find elements by exact name");
        assert_eq!(results[0].name, "my_function");
    }

    #[test]
    fn test_search_by_name_case_insensitive() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "MyFunction", "function");

        let results = engine.search_by_name("myfunction").unwrap();
        assert!(!results.is_empty(), "search_by_name should be case-insensitive");
    }

    #[test]
    fn test_search_by_name_partial_match() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "calculate_total", "function");

        let results = engine.search_by_name("calculate").unwrap();
        assert!(!results.is_empty(), "search_by_name should find partial matches");
    }

    #[test]
    fn test_search_by_name_no_match_returns_empty() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "existing_function", "function");

        let results = engine.search_by_name("nonexistent_xyz_abc").unwrap();
        assert!(results.is_empty(), "search_by_name should return empty for no match");
    }

    #[test]
    fn test_run_raw_query_with_empty_params() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "main", "function");

        let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata]"#;
        let result = engine.run_raw_query(query, Default::default());
        assert!(result.is_ok(), "run_raw_query should succeed with valid query");
        let rows = result.unwrap().rows;
        assert!(!rows.is_empty(), "run_raw_query should return inserted elements");
    }

    #[test]
    fn test_run_raw_query_with_params() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "main", "function");

        let query = r#"?[qualified_name, name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], name = $nm"#;
        let mut params = std::collections::BTreeMap::new();
        params.insert("nm".to_string(), serde_json::Value::String("main".to_string()));
        let result = engine.run_raw_query(query, params);
        assert!(result.is_ok(), "run_raw_query should succeed with parameterized query");
        let rows = result.unwrap().rows;
        assert!(!rows.is_empty(), "run_raw_query with params should find element named 'main'");
    }
}
