#![allow(dead_code)]
pub mod keys;
pub mod models;
pub mod schema;

#[allow(unused_imports)]
pub use models::*;
#[allow(unused_imports)]
pub use schema::*;

pub fn create_business_logic(
    db: &CozoDb,
    element_qualified: &str,
    description: &str,
    user_story_id: Option<&str>,
    feature_id: Option<&str>,
) -> Result<models::BusinessLogic, Box<dyn std::error::Error>> {
    let query = r#"?[element_qualified, description, user_story_id, feature_id] <- [[ $eq, $desc, $us, $feat ]] :put business_logic { element_qualified, description, user_story_id, feature_id }"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "eq".to_string(),
        serde_json::Value::String(element_qualified.to_string()),
    );
    params.insert(
        "desc".to_string(),
        serde_json::Value::String(description.to_string()),
    );
    params.insert(
        "us".to_string(),
        user_story_id
            .map(|s| serde_json::Value::String(s.to_string()))
            .unwrap_or(serde_json::Value::Null),
    );
    params.insert(
        "feat".to_string(),
        feature_id
            .map(|s| serde_json::Value::String(s.to_string()))
            .unwrap_or(serde_json::Value::Null),
    );

    db.run_script(query, params)?;

    Ok(models::BusinessLogic {
        id: None,
        element_qualified: element_qualified.to_string(),
        description: description.to_string(),
        user_story_id: user_story_id.map(String::from),
        feature_id: feature_id.map(String::from),
    })
}

pub fn get_business_logic(
    db: &CozoDb,
    element_qualified: &str,
) -> Result<Option<models::BusinessLogic>, Box<dyn std::error::Error>> {
    let query = r#"?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id], element_qualified = $eq"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "eq".to_string(),
        serde_json::Value::String(element_qualified.to_string()),
    );

    let result = db.run_script(query, params)?;
    let rows = result.rows;

    if rows.is_empty() {
        return Ok(None);
    }

    let row = &rows[0];
    let user_story_id = row[2].as_str().map(String::from);
    let feature_id = row[3].as_str().map(String::from);

    Ok(Some(models::BusinessLogic {
        id: None,
        element_qualified: row[0].as_str().unwrap_or("").to_string(),
        description: row[1].as_str().unwrap_or("").to_string(),
        user_story_id,
        feature_id,
    }))
}

pub fn update_business_logic(
    db: &CozoDb,
    element_qualified: &str,
    description: &str,
    user_story_id: Option<&str>,
    feature_id: Option<&str>,
) -> Result<Option<models::BusinessLogic>, Box<dyn std::error::Error>> {
    let query = r#"?[element_qualified, description, user_story_id, feature_id] <- [[ $eq, $desc, $us, $feat ]] :put business_logic { element_qualified, description, user_story_id, feature_id }"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "eq".to_string(),
        serde_json::Value::String(element_qualified.to_string()),
    );
    params.insert(
        "desc".to_string(),
        serde_json::Value::String(description.to_string()),
    );
    params.insert(
        "us".to_string(),
        user_story_id
            .map(|s| serde_json::Value::String(s.to_string()))
            .unwrap_or(serde_json::Value::Null),
    );
    params.insert(
        "feat".to_string(),
        feature_id
            .map(|s| serde_json::Value::String(s.to_string()))
            .unwrap_or(serde_json::Value::Null),
    );

    db.run_script(query, params)?;

    Ok(Some(models::BusinessLogic {
        id: None,
        element_qualified: element_qualified.to_string(),
        description: description.to_string(),
        user_story_id: user_story_id.map(String::from),
        feature_id: feature_id.map(String::from),
    }))
}

#[allow(dead_code)]
pub fn delete_business_logic(
    db: &CozoDb,
    element_qualified: &str,
) -> Result<(), Box<dyn std::error::Error>> {
    let query = r#":delete business_logic where element_qualified = $eq"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "eq".to_string(),
        serde_json::Value::String(element_qualified.to_string()),
    );

    db.run_script(query, params)?;
    Ok(())
}

pub fn get_by_user_story(
    db: &CozoDb,
    user_story_id: &str,
) -> Result<Vec<models::BusinessLogic>, Box<dyn std::error::Error>> {
    let query = r#"?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id], user_story_id = $us"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "us".to_string(),
        serde_json::Value::String(user_story_id.to_string()),
    );

    let result = db.run_script(query, params)?;
    let rows = result.rows;

    let business_logic: Vec<models::BusinessLogic> = rows
        .iter()
        .map(|row| {
            let user_story_id = row[2].as_str().map(String::from);
            let feature_id = row[3].as_str().map(String::from);
            models::BusinessLogic {
                id: None,
                element_qualified: row[0].as_str().unwrap_or("").to_string(),
                description: row[1].as_str().unwrap_or("").to_string(),
                user_story_id,
                feature_id,
            }
        })
        .collect();

    Ok(business_logic)
}

pub fn get_by_feature(
    db: &CozoDb,
    feature_id: &str,
) -> Result<Vec<models::BusinessLogic>, Box<dyn std::error::Error>> {
    let query = r#"?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id], feature_id = $feat"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "feat".to_string(),
        serde_json::Value::String(feature_id.to_string()),
    );

    let result = db.run_script(query, params)?;
    let rows = result.rows;

    let business_logic: Vec<models::BusinessLogic> = rows
        .iter()
        .map(|row| {
            let user_story_id = row[2].as_str().map(String::from);
            let feature_id = row[3].as_str().map(String::from);
            models::BusinessLogic {
                id: None,
                element_qualified: row[0].as_str().unwrap_or("").to_string(),
                description: row[1].as_str().unwrap_or("").to_string(),
                user_story_id,
                feature_id,
            }
        })
        .collect();

    Ok(business_logic)
}

pub fn search_business_logic(
    db: &CozoDb,
    query_str: &str,
) -> Result<Vec<models::BusinessLogic>, Box<dyn std::error::Error>> {
    let regex_pattern = format!(".*{}.*", query_str.to_lowercase());
    let query = format!(
        r#"?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id], regex_matches(lowercase(description), "{}")"#,
        regex_pattern
    );

    let result = db.run_script(&query, std::collections::BTreeMap::new())?;
    let rows = result.rows;

    let business_logic: Vec<models::BusinessLogic> = rows
        .iter()
        .map(|row| {
            let user_story_id = row[2].as_str().map(String::from);
            let feature_id = row[3].as_str().map(String::from);
            models::BusinessLogic {
                id: None,
                element_qualified: row[0].as_str().unwrap_or("").to_string(),
                description: row[1].as_str().unwrap_or("").to_string(),
                user_story_id,
                feature_id,
            }
        })
        .collect();

    Ok(business_logic)
}

pub fn all_business_logic(
    db: &CozoDb,
) -> Result<Vec<models::BusinessLogic>, Box<dyn std::error::Error>> {
    let query = r#"?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id]"#;

    let result = db.run_script(query, std::collections::BTreeMap::new())?;
    let rows = result.rows;

    let business_logic: Vec<models::BusinessLogic> = rows
        .iter()
        .map(|row| {
            let user_story_id = row[2].as_str().map(String::from);
            let feature_id = row[3].as_str().map(String::from);
            models::BusinessLogic {
                id: None,
                element_qualified: row[0].as_str().unwrap_or("").to_string(),
                description: row[1].as_str().unwrap_or("").to_string(),
                user_story_id,
                feature_id,
            }
        })
        .collect();

    Ok(business_logic)
}

#[allow(dead_code)]
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct FeatureTraceEntry {
    pub element_qualified: String,
    pub description: String,
    pub user_story_id: Option<String>,
}

#[allow(dead_code)]
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct FeatureTraceability {
    pub feature_id: String,
    pub code_elements: Vec<FeatureTraceEntry>,
    pub count: usize,
}

#[allow(dead_code)]
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct UserStoryTraceEntry {
    pub element_qualified: String,
    pub description: String,
    pub feature_id: Option<String>,
}

#[allow(dead_code)]
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct UserStoryTraceability {
    pub user_story_id: String,
    pub code_elements: Vec<UserStoryTraceEntry>,
    pub count: usize,
}

#[allow(dead_code)]
pub fn get_feature_traceability(
    db: &CozoDb,
    feature_id: &str,
) -> Result<FeatureTraceability, Box<dyn std::error::Error>> {
    let elements = get_by_feature(db, feature_id)?;
    let code_elements: Vec<FeatureTraceEntry> = elements
        .into_iter()
        .map(|bl| FeatureTraceEntry {
            element_qualified: bl.element_qualified,
            description: bl.description,
            user_story_id: bl.user_story_id,
        })
        .collect();
    let count = code_elements.len();
    Ok(FeatureTraceability {
        feature_id: feature_id.to_string(),
        code_elements,
        count,
    })
}

#[allow(dead_code)]
pub fn get_user_story_traceability(
    db: &CozoDb,
    user_story_id: &str,
) -> Result<UserStoryTraceability, Box<dyn std::error::Error>> {
    let elements = get_by_user_story(db, user_story_id)?;
    let code_elements: Vec<UserStoryTraceEntry> = elements
        .into_iter()
        .map(|bl| UserStoryTraceEntry {
            element_qualified: bl.element_qualified,
            description: bl.description,
            feature_id: bl.feature_id,
        })
        .collect();
    let count = code_elements.len();
    Ok(UserStoryTraceability {
        user_story_id: user_story_id.to_string(),
        code_elements,
        count,
    })
}

#[allow(dead_code)]
pub fn all_feature_traceability(
    db: &CozoDb,
) -> Result<Vec<FeatureTraceability>, Box<dyn std::error::Error>> {
    let all = all_business_logic(db)?;
    let mut feature_map: std::collections::HashMap<String, Vec<FeatureTraceEntry>> =
        std::collections::HashMap::new();

    for bl in all {
        if let Some(ref fid) = bl.feature_id {
            let entry = FeatureTraceEntry {
                element_qualified: bl.element_qualified.clone(),
                description: bl.description.clone(),
                user_story_id: bl.user_story_id.clone(),
            };
            feature_map.entry(fid.clone()).or_default().push(entry);
        }
    }

    let traces: Vec<FeatureTraceability> = feature_map
        .into_iter()
        .map(|(feature_id, code_elements)| {
            let count = code_elements.len();
            FeatureTraceability {
                feature_id,
                code_elements,
                count,
            }
        })
        .collect();
    Ok(traces)
}

#[allow(dead_code)]
pub fn all_user_story_traceability(
    db: &CozoDb,
) -> Result<Vec<UserStoryTraceability>, Box<dyn std::error::Error>> {
    let all = all_business_logic(db)?;
    let mut story_map: std::collections::HashMap<String, Vec<UserStoryTraceEntry>> =
        std::collections::HashMap::new();

    for bl in all {
        if let Some(ref sid) = bl.user_story_id {
            let entry = UserStoryTraceEntry {
                element_qualified: bl.element_qualified.clone(),
                description: bl.description.clone(),
                feature_id: bl.feature_id.clone(),
            };
            story_map.entry(sid.clone()).or_default().push(entry);
        }
    }

    let traces: Vec<UserStoryTraceability> = story_map
        .into_iter()
        .map(|(user_story_id, code_elements)| {
            let count = code_elements.len();
            UserStoryTraceability {
                user_story_id,
                code_elements,
                count,
            }
        })
        .collect();
    Ok(traces)
}

#[allow(dead_code)]
pub fn find_by_business_domain(
    db: &CozoDb,
    domain: &str,
) -> Result<Vec<models::BusinessLogic>, Box<dyn std::error::Error>> {
    search_business_logic(db, domain)
}

#[allow(dead_code)]
pub fn get_documented_by(
    db: &CozoDb,
    element_qualified: &str,
) -> Result<Vec<models::DocLink>, Box<dyn std::error::Error>> {
    let query = r#"?[target_qualified, rel_type, metadata] := *relationships[source_qualified, target_qualified, rel_type, metadata], source_qualified = $sq, rel_type = "documented_by""#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "sq".to_string(),
        serde_json::Value::String(element_qualified.to_string()),
    );

    let result = db.run_script(query, params)?;
    let rows = result.rows;

    let doc_links: Vec<models::DocLink> = rows
        .iter()
        .filter_map(|row| {
            let doc_qualified = row[0].as_str().unwrap_or("").to_string();
            let _rel_type = row[1].as_str().unwrap_or("");
            let metadata_str = row.get(2).and_then(|v| v.as_str()).unwrap_or("{}");
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

            Some(models::DocLink {
                doc_qualified,
                doc_title,
                context,
            })
        })
        .collect();

    Ok(doc_links)
}

#[allow(dead_code)]
pub fn get_traceability_report(
    db: &CozoDb,
    element_qualified: &str,
) -> Result<models::TraceabilityReport, Box<dyn std::error::Error>> {
    let bl = get_business_logic(db, element_qualified)?;
    let doc_links = get_documented_by(db, element_qualified)?;

    let entry = models::TraceabilityEntry {
        element_qualified: element_qualified.to_string(),
        description: bl
            .as_ref()
            .map(|b| b.description.clone())
            .unwrap_or_default(),
        user_story_id: bl.as_ref().and_then(|b| b.user_story_id.clone()),
        feature_id: bl.as_ref().and_then(|b| b.feature_id.clone()),
        doc_links,
    };

    Ok(models::TraceabilityReport {
        element_qualified: element_qualified.to_string(),
        entries: vec![entry],
        count: 1,
    })
}

#[allow(dead_code)]
pub fn get_code_for_requirement(
    db: &CozoDb,
    requirement_id: &str,
) -> Result<Vec<models::TraceabilityEntry>, Box<dyn std::error::Error>> {
    let query = r#"?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id], user_story_id = $us"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "us".to_string(),
        serde_json::Value::String(requirement_id.to_string()),
    );

    let result = db.run_script(query, params)?;
    let rows = result.rows;

    let mut entries = Vec::new();
    for row in rows {
        let element_qualified = row[0].as_str().unwrap_or("").to_string();
        let description = row[1].as_str().unwrap_or("").to_string();
        let user_story_id = row[2].as_str().map(String::from);
        let feature_id = row[3].as_str().map(String::from);

        let doc_links = get_documented_by(db, &element_qualified)?;

        entries.push(models::TraceabilityEntry {
            element_qualified,
            description,
            user_story_id,
            feature_id,
            doc_links,
        });
    }

    Ok(entries)
}
