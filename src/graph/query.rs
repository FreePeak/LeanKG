#![allow(dead_code)]
use crate::db::models::{
    BusinessLogic, CodeElement, DocLink, Relationship, TraceabilityEntry, TraceabilityReport,
};
use crate::db::schema::CozoDb;
use crate::graph::cache::QueryCache;
use std::sync::Arc;
use tokio::sync::RwLock;
use tracing::debug;

fn escape_datalog(s: &str) -> String {
    s.replace('\\', "\\\\").replace('"', "\\\"")
}

#[derive(Clone)]
pub struct GraphEngine {
    db: CozoDb,
    cache: Arc<RwLock<QueryCache>>,
}

impl GraphEngine {
    pub fn new(db: CozoDb) -> Self {
        Self {
            db,
            cache: Arc::new(RwLock::new(QueryCache::new(300, 1000))),
        }
    }

    pub fn with_cache(db: CozoDb, cache: QueryCache) -> Self {
        Self {
            db,
            cache: Arc::new(RwLock::new(cache)),
        }
    }

    pub fn db(&self) -> &CozoDb {
        &self.db
    }

    pub fn find_element(
        &self,
        qualified_name: &str,
    ) -> Result<Option<CodeElement>, Box<dyn std::error::Error>> {
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], qualified_name = "{}""#,
            qualified_name
        );

        let result = self
            .db
            .run_script(&query, std::collections::BTreeMap::new())?;
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

    pub fn find_element_by_name(
        &self,
        name: &str,
    ) -> Result<Option<CodeElement>, Box<dyn std::error::Error>> {
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], name = "{}""#,
            name
        );

        let result = self
            .db
            .run_script(&query, std::collections::BTreeMap::new())?;
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
        let escaped_path = escape_datalog(file_path);
        let query = format!(
            r#"?[target_qualified, rel_type, metadata] := *relationships[source_qualified, target_qualified, rel_type, metadata], source_qualified = "{}", rel_type = "imports""#,
            escaped_path
        );

        let result = self
            .db
            .run_script(&query, std::collections::BTreeMap::new())?;
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
            let db_path = file_path.to_string();
            let cache = self.cache.clone();
            std::thread::spawn(move || {
                let rt = tokio::runtime::Runtime::new().unwrap();
                rt.block_on(async {
                    cache.read().await.set_dependencies(db_path, qns).await;
                });
            });
        }

        Ok(elements)
    }

    pub fn get_relationships(
        &self,
        source: &str,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let escaped = escape_datalog(source);
        let query = format!(
            r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata], source_qualified = "{}""#,
            escaped
        );

        let result = self
            .db
            .run_script(&query, std::collections::BTreeMap::new())?;
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
        let escaped = escape_datalog(target);
        let query = format!(
            r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata], target_qualified = "{}""#,
            escaped
        );

        let result = self
            .db
            .run_script(&query, std::collections::BTreeMap::new())?;
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
            let qns: Vec<String> = relationships
                .iter()
                .map(|r| r.target_qualified.clone())
                .collect();
            let db_target = target.to_string();
            let cache = self.cache.clone();
            std::thread::spawn(move || {
                let rt = tokio::runtime::Runtime::new().unwrap();
                rt.block_on(async {
                    cache.read().await.set_dependents(db_target, qns).await;
                });
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

    pub fn all_elements(&self) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata]"#;

        let result = self
            .db
            .run_script(query, std::collections::BTreeMap::new())?;
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

        let result = self
            .db
            .run_script(query, std::collections::BTreeMap::new())?;
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

    pub fn get_children(
        &self,
        parent_qualified: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], parent_qualified = "{}""#,
            parent_qualified
        );

        let result = self
            .db
            .run_script(&query, std::collections::BTreeMap::new())?;
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
        let query = format!(
            r#"?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id], element_qualified = "{}""#,
            element_qualified
        );

        let result = self
            .db
            .run_script(&query, std::collections::BTreeMap::new())?;
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

    pub fn search_annotations(
        &self,
        query_str: &str,
    ) -> Result<Vec<BusinessLogic>, Box<dyn std::error::Error>> {
        let like_pattern = format!("%{}%", query_str.to_lowercase());

        let query = format!(
            r#"?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id], regex_matches(lowercase(description), "{}")"#,
            like_pattern
        );

        let result = self
            .db
            .run_script(&query, std::collections::BTreeMap::new())?;
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

        let result = self
            .db
            .run_script(query, std::collections::BTreeMap::new())?;
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

    pub fn get_documented_by(
        &self,
        element_qualified: &str,
    ) -> Result<Vec<DocLink>, Box<dyn std::error::Error>> {
        let escaped = escape_datalog(element_qualified);
        let query = format!(
            r#"?[source_qualified, target_qualified, rel_type, metadata] := *relationships[source_qualified, target_qualified, rel_type, metadata], source_qualified = "{}", rel_type = "documented_by""#,
            escaped
        );

        let result = self
            .db
            .run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let doc_links: Vec<DocLink> = rows
            .iter()
            .filter_map(|row| {
                let doc_qualified = row[1].as_str().unwrap_or("").to_string();
                let _rel_type = row[2].as_str().unwrap_or("");
                let metadata_str = row.get(3).and_then(|v| v.as_str()).unwrap_or("{}");
                let metadata: serde_json::Value = serde_json::from_str(metadata_str).ok()?;

                let doc_title = metadata
                    .get("title")
                    .and_then(|v| v.as_str())
                    .unwrap_or("Untitled")
                    .to_string();
                let context = metadata
                    .get("context")
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

    pub fn get_traceability_report(
        &self,
        element_qualified: &str,
    ) -> Result<TraceabilityReport, Box<dyn std::error::Error>> {
        let bl = self.get_annotation(element_qualified)?;
        let doc_links = self.get_documented_by(element_qualified)?;

        let entry = TraceabilityEntry {
            element_qualified: element_qualified.to_string(),
            description: bl
                .as_ref()
                .map(|b| b.description.clone())
                .unwrap_or_default(),
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

    pub fn get_code_for_requirement(
        &self,
        requirement_id: &str,
    ) -> Result<Vec<TraceabilityEntry>, Box<dyn std::error::Error>> {
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

    pub fn get_business_logic_by_user_story(
        &self,
        user_story_id: &str,
    ) -> Result<Vec<BusinessLogic>, Box<dyn std::error::Error>> {
        let query = format!(
            r#"?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id], user_story_id = "{}""#,
            user_story_id
        );

        let result = self
            .db
            .run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let business_logic: Vec<BusinessLogic> = rows
            .iter()
            .map(|row| BusinessLogic {
                id: None,
                element_qualified: row[0].as_str().unwrap_or("").to_string(),
                description: row[1].as_str().unwrap_or("").to_string(),
                user_story_id: row[2].as_str().map(String::from),
                feature_id: row[3].as_str().map(String::from),
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

        let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] <- [[ $qn, $et, $nm, $fp, $ls, $le, $lg, $pq, $cid, $cl, $md ]] :put code_elements { qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata }"#;

        for element in elements {
            let metadata_str = serde_json::to_string(&element.metadata)?;
            let mut params = std::collections::BTreeMap::new();
            params.insert(
                "qn".to_string(),
                serde_json::Value::String(element.qualified_name.clone()),
            );
            params.insert(
                "et".to_string(),
                serde_json::Value::String(element.element_type.clone()),
            );
            params.insert(
                "nm".to_string(),
                serde_json::Value::String(element.name.clone()),
            );
            params.insert(
                "fp".to_string(),
                serde_json::Value::String(element.file_path.clone()),
            );
            params.insert(
                "ls".to_string(),
                serde_json::Value::Number(element.line_start.into()),
            );
            params.insert(
                "le".to_string(),
                serde_json::Value::Number(element.line_end.into()),
            );
            params.insert(
                "lg".to_string(),
                serde_json::Value::String(element.language.clone()),
            );
            match &element.parent_qualified {
                Some(pq) => params.insert("pq".to_string(), serde_json::Value::String(pq.clone())),
                None => params.insert("pq".to_string(), serde_json::Value::Null),
            };
            match &element.cluster_id {
                Some(cid) => {
                    params.insert("cid".to_string(), serde_json::Value::String(cid.clone()))
                }
                None => params.insert("cid".to_string(), serde_json::Value::Null),
            };
            match &element.cluster_label {
                Some(cl) => params.insert("cl".to_string(), serde_json::Value::String(cl.clone())),
                None => params.insert("cl".to_string(), serde_json::Value::Null),
            };
            params.insert("md".to_string(), serde_json::Value::String(metadata_str));

            self.db.run_script(query, params)?;
        }

        if let Some(first) = elements.first() {
            let cache = self.cache.clone();
            let file_path = first.file_path.clone();
            std::thread::spawn(move || {
                let rt = tokio::runtime::Runtime::new().unwrap();
                rt.block_on(async {
                    cache.read().await.invalidate_file(&file_path).await;
                });
            });
        }

        Ok(())
    }

    pub fn insert_element(&self, element: &CodeElement) -> Result<(), Box<dyn std::error::Error>> {
        let metadata_str = serde_json::to_string(&element.metadata)?;
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "qn".to_string(),
            serde_json::Value::String(element.qualified_name.clone()),
        );
        params.insert(
            "et".to_string(),
            serde_json::Value::String(element.element_type.clone()),
        );
        params.insert(
            "nm".to_string(),
            serde_json::Value::String(element.name.clone()),
        );
        params.insert(
            "fp".to_string(),
            serde_json::Value::String(element.file_path.clone()),
        );
        params.insert(
            "ls".to_string(),
            serde_json::Value::Number(element.line_start.into()),
        );
        params.insert(
            "le".to_string(),
            serde_json::Value::Number(element.line_end.into()),
        );
        params.insert(
            "lg".to_string(),
            serde_json::Value::String(element.language.clone()),
        );
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
        let file_path = element.file_path.clone();
        std::thread::spawn(move || {
            let rt = tokio::runtime::Runtime::new().unwrap();
            rt.block_on(async {
                cache.read().await.invalidate_file(&file_path).await;
            });
        });

        Ok(())
    }

    pub fn update_element_cluster(
        &self,
        qualified_name: &str,
        cluster_id: Option<String>,
        cluster_label: Option<String>,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "qn".to_string(),
            serde_json::Value::String(qualified_name.to_string()),
        );
        if let Some(cid) = cluster_id {
            params.insert("cid".to_string(), serde_json::Value::String(cid));
        } else {
            params.insert("cid".to_string(), serde_json::Value::Null);
        }
        if let Some(cl) = cluster_label {
            params.insert("cl".to_string(), serde_json::Value::String(cl));
        } else {
            params.insert("cl".to_string(), serde_json::Value::Null);
        }

        let query =
            r#"*code_elements[qualified_name = $qn] := { cluster_id: $cid, cluster_label: $cl }"#;

        self.db.run_script(query, params)?;

        Ok(())
    }

    pub fn insert_relationship(
        &self,
        relationship: &Relationship,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let metadata_str = serde_json::to_string(&relationship.metadata)?;
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "sq".to_string(),
            serde_json::Value::String(relationship.source_qualified.clone()),
        );
        params.insert(
            "tq".to_string(),
            serde_json::Value::String(relationship.target_qualified.clone()),
        );
        params.insert(
            "rt".to_string(),
            serde_json::Value::String(relationship.rel_type.clone()),
        );
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

        let query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] <- [[ $sq, $tq, $rt, $cn, $md ]] :put relationships { source_qualified, target_qualified, rel_type, confidence, metadata }"#;

        for rel in relationships {
            let metadata_str = serde_json::to_string(&rel.metadata)?;
            let mut params = std::collections::BTreeMap::new();
            params.insert(
                "sq".to_string(),
                serde_json::Value::String(rel.source_qualified.clone()),
            );
            params.insert(
                "tq".to_string(),
                serde_json::Value::String(rel.target_qualified.clone()),
            );
            params.insert(
                "rt".to_string(),
                serde_json::Value::String(rel.rel_type.clone()),
            );
            params.insert("cn".to_string(), serde_json::json!(rel.confidence));
            params.insert("md".to_string(), serde_json::Value::String(metadata_str));

            self.db.run_script(query, params)?;
        }

        if let Some(first) = relationships.first() {
            let cache = self.cache.clone();
            let file_path = first.source_qualified.clone();
            std::thread::spawn(move || {
                let rt = tokio::runtime::Runtime::new().unwrap();
                rt.block_on(async {
                    cache.read().await.invalidate_file(&file_path).await;
                });
            });
        }

        Ok(())
    }

    pub fn remove_elements_by_file(
        &self,
        file_path: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let query = format!(r#":delete code_elements where file_path = "{}""#, file_path);

        self.db
            .run_script(&query, std::collections::BTreeMap::new())?;

        let cache = self.cache.clone();
        let file_path_str = file_path.to_string();
        std::thread::spawn(move || {
            let rt = tokio::runtime::Runtime::new().unwrap();
            rt.block_on(async {
                cache.read().await.invalidate_file(&file_path_str).await;
            });
        });

        Ok(())
    }

    pub fn remove_relationships_by_source(
        &self,
        source: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let query = format!(
            r#":delete relationships where source_qualified = "{}""#,
            source
        );

        self.db
            .run_script(&query, std::collections::BTreeMap::new())?;

        let cache = self.cache.clone();
        let source_str = source.to_string();
        std::thread::spawn(move || {
            let rt = tokio::runtime::Runtime::new().unwrap();
            rt.block_on(async {
                cache.read().await.invalidate_file(&source_str).await;
            });
        });

        Ok(())
    }

    pub fn get_elements_by_file(
        &self,
        file_path: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], file_path = "{}""#,
            file_path
        );

        let result = self
            .db
            .run_script(&query, std::collections::BTreeMap::new())?;
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
        let pattern = format!(".*{}.*", name.to_lowercase());

        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], regex_matches(lowercase(name), "{}")"#,
            pattern
        );

        let result = self
            .db
            .run_script(&query, std::collections::BTreeMap::new())?;
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

        let result = self
            .db
            .run_script(&query, std::collections::BTreeMap::new())?;
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
        let like_pattern = format!("%{}%", pattern);

        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], regex_matches(lowercase(qualified_name), "{}")"#,
            like_pattern
        );

        let result = self
            .db
            .run_script(&query, std::collections::BTreeMap::new())?;
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

        let result = self
            .db
            .run_script(&query, std::collections::BTreeMap::new())?;
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

        let result = self
            .db
            .run_script(&query, std::collections::BTreeMap::new())?;
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
            language, min_lines
        );

        let result = self
            .db
            .run_script(&query, std::collections::BTreeMap::new())?;
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
        Ok(result
            .rows
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
            .collect())
    }

    pub fn search_by_name_typed(
        &self,
        name: &str,
        element_type: Option<&str>,
        limit: usize,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let safe_name = escape_datalog(&name.to_lowercase());
        let type_clause = match element_type {
            Some(t) => format!(r#", element_type = "{}""#, escape_datalog(t)),
            None => String::new(),
        };
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata]
               := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata]{type_clause},
              regex_matches(lowercase(name), "{pattern}")
           :limit {limit}"#,
            type_clause = type_clause,
            pattern = safe_name,
            limit = limit,
        );
        self.run_element_query(&query)
    }

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

    pub fn get_call_graph_bounded(
        &self,
        source_qualified: &str,
        max_depth: u32,
        max_results: usize,
    ) -> Result<Vec<(String, String, u32)>, Box<dyn std::error::Error>> {
        let safe_src = escape_datalog(source_qualified);
        let query = match max_depth {
            1 => format!(
                r#"?[src, tgt, depth] :=
                   *relationships["{src}", tgt, "calls", _],
                   src = "{src}", depth = 1
                   :limit {limit}"#,
                src = safe_src,
                limit = max_results,
            ),
            2 => format!(
                r#"hop1[src, tgt] := *relationships[src, tgt, "calls", _], src = "{src}"
                   hop2[src2, tgt2] := hop1[_, src2], *relationships[src2, tgt2, "calls", _]
                   ?[src, tgt, depth] := hop1[src, tgt], depth = 1
                   ?[src, tgt, depth] := hop2[src, tgt], depth = 2
                   :limit {limit}"#,
                src = safe_src,
                limit = max_results,
            ),
            _ => format!(
                r#"hop1[src, tgt] := *relationships[src, tgt, "calls", _], src = "{src}"
                   hop2[s2, t2] := hop1[_, s2], *relationships[s2, t2, "calls", _]
                   hop3[s3, t3] := hop2[_, s3], *relationships[s3, t3, "calls", _]
                   ?[src, tgt, depth] := hop1[src, tgt], depth = 1
                   ?[src, tgt, depth] := hop2[src, tgt], depth = 2
                   ?[src, tgt, depth] := hop3[src, tgt], depth = 3
                   :limit {limit}"#,
                src = safe_src,
                limit = max_results,
            ),
        };

        let result = self.db.run_script(&query, Default::default())?;
        Ok(result
            .rows
            .iter()
            .filter_map(|row| {
                Some((
                    row[0].as_str()?.to_string(),
                    row[1].as_str()?.to_string(),
                    row[2].as_i64()? as u32,
                ))
            })
            .collect())
    }

    pub fn resolve_call_edges(&self) -> Result<usize, Box<dyn std::error::Error>> {
        let query = r#"?[source_qualified, target_qualified, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata], rel_type = "calls", target_qualified =~ "__unresolved__.*""#;
        debug!("Running resolve_call_edges query (filtered at DB level)");
        let result = self
            .db
            .run_script(query, std::collections::BTreeMap::new())?;
        let total_unresolved = result.rows.len();
        debug!(
            "Found {} unresolved call edges to resolve",
            total_unresolved
        );

        if total_unresolved == 0 {
            return Ok(0);
        }

        let mut resolved = 0;
        let batch_size = 100;
        let mut last_progress = 0;

        for (idx, row) in result.rows.iter().enumerate() {
            let source = row[0].as_str().unwrap_or("").to_string();
            let target_qualified = row[1].as_str().unwrap_or("");
            let meta_str = row[2].as_str().unwrap_or("{}");

            let bare_name = target_qualified.trim_start_matches("__unresolved__");

            let callee_file_hint: Option<String> =
                serde_json::from_str::<serde_json::Value>(meta_str)
                    .ok()
                    .and_then(|m| m.get("callee_file_hint").cloned())
                    .and_then(|v| v.as_str().map(String::from));

            if let (Some(target_qn), confidence) =
                self.find_function_by_name_with_confidence(&bare_name, callee_file_hint.as_deref())?
            {
                self.insert_relationship(&Relationship {
                    id: None,
                    source_qualified: source,
                    target_qualified: target_qn,
                    rel_type: "calls".to_string(),
                    confidence,
                    metadata: serde_json::json!({}),
                })?;
                resolved += 1;
            }

            if idx - last_progress >= batch_size {
                debug!("Progress: {}/{} resolved", resolved, total_unresolved);
                last_progress = idx;
            }
        }

        debug!("Resolved {} call edges", resolved);

        Ok(resolved)
    }

    fn find_function_by_name_with_confidence(
        &self,
        name: &str,
        file_hint: Option<&str>,
    ) -> Result<(Option<String>, f64), Box<dyn std::error::Error>> {
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
        Ok((
            result
                .rows
                .first()
                .and_then(|row| row[0].as_str().map(String::from)),
            0.7,
        ))
    }

    fn delete_relationship(
        &self,
        source: &str,
        target: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let safe_source = escape_datalog(source);
        let safe_target = escape_datalog(target);
        let query = format!(":rm relationships[source_qualified, target_qualified, rel_type, confidence, metadata] := source_qualified = \"{}\", target_qualified = \"{}\"", safe_source, safe_target);
        self.db.run_script(&query, Default::default())?;
        Ok(())
    }
}
