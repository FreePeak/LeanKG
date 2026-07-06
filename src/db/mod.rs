#![allow(dead_code)]
pub mod keys;
pub mod models;
pub mod schema;
pub mod versioning;

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

    crate::db::schema::run_script(db, query, params)?;

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

    let result = crate::db::schema::run_script(db, query, params)?;
    let rows = result.rows;

    if rows.is_empty() {
        return Ok(None);
    }

    let row = &rows[0];
    let user_story_id = row[2].get_str().map(String::from);
    let feature_id = row[3].get_str().map(String::from);

    Ok(Some(models::BusinessLogic {
        id: None,
        element_qualified: row[0].get_str().unwrap_or("").to_string(),
        description: row[1].get_str().unwrap_or("").to_string(),
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

    crate::db::schema::run_script(db, query, params)?;

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

    crate::db::schema::run_script(db, query, params)?;
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

    let result = crate::db::schema::run_script(db, query, params)?;
    let rows = result.rows;

    let business_logic: Vec<models::BusinessLogic> = rows
        .iter()
        .map(|row| {
            let user_story_id = row[2].get_str().map(String::from);
            let feature_id = row[3].get_str().map(String::from);
            models::BusinessLogic {
                id: None,
                element_qualified: row[0].get_str().unwrap_or("").to_string(),
                description: row[1].get_str().unwrap_or("").to_string(),
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

    let result = crate::db::schema::run_script(db, query, params)?;
    let rows = result.rows;

    let business_logic: Vec<models::BusinessLogic> = rows
        .iter()
        .map(|row| {
            let user_story_id = row[2].get_str().map(String::from);
            let feature_id = row[3].get_str().map(String::from);
            models::BusinessLogic {
                id: None,
                element_qualified: row[0].get_str().unwrap_or("").to_string(),
                description: row[1].get_str().unwrap_or("").to_string(),
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

    let result = crate::db::schema::run_script(db, &query, std::collections::BTreeMap::new())?;
    let rows = result.rows;

    let business_logic: Vec<models::BusinessLogic> = rows
        .iter()
        .map(|row| {
            let user_story_id = row[2].get_str().map(String::from);
            let feature_id = row[3].get_str().map(String::from);
            models::BusinessLogic {
                id: None,
                element_qualified: row[0].get_str().unwrap_or("").to_string(),
                description: row[1].get_str().unwrap_or("").to_string(),
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

    let result = crate::db::schema::run_script(db, query, std::collections::BTreeMap::new())?;
    let rows = result.rows;

    let business_logic: Vec<models::BusinessLogic> = rows
        .iter()
        .map(|row| {
            let user_story_id = row[2].get_str().map(String::from);
            let feature_id = row[3].get_str().map(String::from);
            models::BusinessLogic {
                id: None,
                element_qualified: row[0].get_str().unwrap_or("").to_string(),
                description: row[1].get_str().unwrap_or("").to_string(),
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
    let query = r#"?[target_qualified, rel_type, metadata, confidence] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _], source_qualified = $sq, rel_type = "documented_by""#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "sq".to_string(),
        serde_json::Value::String(element_qualified.to_string()),
    );

    let result = crate::db::schema::run_script(db, query, params)?;
    let rows = result.rows;

    let doc_links: Vec<models::DocLink> = rows
        .iter()
        .filter_map(|row| {
            let _source = row[0].get_str().unwrap_or("");
            let doc_qualified = row[1].get_str().unwrap_or("").to_string();
            let _rel_type = row[2].get_str().unwrap_or("");
            let _confidence = row[3].get_float().unwrap_or(1.0);
            let metadata_str = row.get(4).and_then(|v| v.get_str()).unwrap_or("{}");
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

    let result = crate::db::schema::run_script(db, query, params)?;
    let rows = result.rows;

    let mut entries = Vec::new();
    for row in rows {
        let element_qualified = row[0].get_str().unwrap_or("").to_string();
        let description = row[1].get_str().unwrap_or("").to_string();
        let user_story_id = row[2].get_str().map(String::from);
        let feature_id = row[3].get_str().map(String::from);

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

pub fn record_metric(
    db: &CozoDb,
    metric: &models::ContextMetric,
) -> Result<(), Box<dyn std::error::Error>> {
    let query = r#"?[tool_name, timestamp, project_path, input_tokens, output_tokens, output_elements, execution_time_ms, baseline_tokens, baseline_lines_scanned, tokens_saved, savings_percent, correct_elements, total_expected, f1_score, query_pattern, query_file, query_depth, success, is_deleted] <- [[ $tool, $ts, $path, $in_tok, $out_tok, $out_elem, $exec_ms, $base_tok, $base_lines, $saved, $sav_pct, $correct, $total, $f1, $qpat, $qfile, $qdepth, $success, false ]] :put context_metrics { tool_name, timestamp, project_path, input_tokens, output_tokens, output_elements, execution_time_ms, baseline_tokens, baseline_lines_scanned, tokens_saved, savings_percent, correct_elements, total_expected, f1_score, query_pattern, query_file, query_depth, success, is_deleted }"#;

    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "tool".to_string(),
        serde_json::Value::String(metric.tool_name.clone()),
    );
    params.insert(
        "ts".to_string(),
        serde_json::Value::Number(metric.timestamp.into()),
    );
    params.insert(
        "path".to_string(),
        serde_json::Value::String(metric.project_path.clone()),
    );
    params.insert(
        "in_tok".to_string(),
        serde_json::Value::Number(metric.input_tokens.into()),
    );
    params.insert(
        "out_tok".to_string(),
        serde_json::Value::Number(metric.output_tokens.into()),
    );
    params.insert(
        "out_elem".to_string(),
        serde_json::Value::Number(metric.output_elements.into()),
    );
    params.insert(
        "exec_ms".to_string(),
        serde_json::Value::Number(metric.execution_time_ms.into()),
    );
    params.insert(
        "base_tok".to_string(),
        serde_json::Value::Number(metric.baseline_tokens.into()),
    );
    params.insert(
        "base_lines".to_string(),
        serde_json::Value::Number(metric.baseline_lines_scanned.into()),
    );
    params.insert(
        "saved".to_string(),
        serde_json::Value::Number(metric.tokens_saved.into()),
    );
    params.insert(
        "sav_pct".to_string(),
        serde_json::Value::Number(
            serde_json::Number::from_f64(metric.savings_percent)
                .unwrap_or(serde_json::Number::from(0)),
        ),
    );
    params.insert(
        "correct".to_string(),
        metric
            .correct_elements
            .map(|v| serde_json::Value::Number(v.into()))
            .unwrap_or(serde_json::Value::Null),
    );
    params.insert(
        "total".to_string(),
        metric
            .total_expected
            .map(|v| serde_json::Value::Number(v.into()))
            .unwrap_or(serde_json::Value::Null),
    );
    params.insert(
        "f1".to_string(),
        metric
            .f1_score
            .map(|v| {
                serde_json::Value::Number(
                    serde_json::Number::from_f64(v).unwrap_or(serde_json::Number::from(0)),
                )
            })
            .unwrap_or(serde_json::Value::Null),
    );
    params.insert(
        "qpat".to_string(),
        metric
            .query_pattern
            .clone()
            .map(serde_json::Value::String)
            .unwrap_or(serde_json::Value::Null),
    );
    params.insert(
        "qfile".to_string(),
        metric
            .query_file
            .clone()
            .map(serde_json::Value::String)
            .unwrap_or(serde_json::Value::Null),
    );
    params.insert(
        "qdepth".to_string(),
        metric
            .query_depth
            .map(|v| serde_json::Value::Number(v.into()))
            .unwrap_or(serde_json::Value::Null),
    );
    params.insert(
        "success".to_string(),
        serde_json::Value::Bool(metric.success),
    );

    crate::db::schema::run_script(db, query, params)?;
    Ok(())
}

pub fn get_metrics_summary(
    db: &CozoDb,
    tool_filter: Option<&str>,
    retention_days: i32,
) -> Result<models::MetricsSummary, Box<dyn std::error::Error>> {
    let cutoff_timestamp = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64
        - (retention_days as i64 * 24 * 60 * 60);

    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "cutoff".to_string(),
        serde_json::Value::Number(cutoff_timestamp.into()),
    );

    let query = if let Some(ref tool) = tool_filter {
        params.insert(
            "tool".to_string(),
            serde_json::Value::String(tool.to_string()),
        );
        r#"?[tool_name, timestamp, project_path, input_tokens, output_tokens, output_elements, execution_time_ms, baseline_tokens, baseline_lines_scanned, tokens_saved, savings_percent, correct_elements, total_expected, f1_score, query_pattern, query_file, query_depth, success, is_deleted] := *context_metrics[tool_name, timestamp, project_path, input_tokens, output_tokens, output_elements, execution_time_ms, baseline_tokens, baseline_lines_scanned, tokens_saved, savings_percent, correct_elements, total_expected, f1_score, query_pattern, query_file, query_depth, success, is_deleted], timestamp >= $cutoff, tool_name = $tool, is_deleted = false"#
    } else {
        r#"?[tool_name, timestamp, project_path, input_tokens, output_tokens, output_elements, execution_time_ms, baseline_tokens, baseline_lines_scanned, tokens_saved, savings_percent, correct_elements, total_expected, f1_score, query_pattern, query_file, query_depth, success, is_deleted] := *context_metrics[tool_name, timestamp, project_path, input_tokens, output_tokens, output_elements, execution_time_ms, baseline_tokens, baseline_lines_scanned, tokens_saved, savings_percent, correct_elements, total_expected, f1_score, query_pattern, query_file, query_depth, success, is_deleted], timestamp >= $cutoff, is_deleted = false"#
    };

    let result = crate::db::schema::run_script(db, query, params.clone())?;

    let mut summary = models::MetricsSummary {
        total_invocations: 0,
        total_tokens_saved: 0,
        average_savings_percent: 0.0,
        average_correctness_percent: 0.0,
        retention_days,
        by_tool: Vec::new(),
        by_day: Vec::new(),
    };

    let mut sum_savings_percent = 0.0;
    let mut count_positive_savings = 0i64;
    let mut sum_correctness_percent = 0.0;
    let mut count_has_correctness = 0i64;
    let mut by_tool_map: std::collections::HashMap<String, (i64, i64, f64, f64, i64)> =
        std::collections::HashMap::new();

    for row in &result.rows {
        summary.total_invocations += 1;
        let saved = row[9].get_int().unwrap_or(0);
        let pct = row[10].get_float().unwrap_or(0.0);
        let correct = row[12].get_int().unwrap_or(0);
        let total = row[13].get_int().unwrap_or(0);

        // Only add positive savings to the total
        if saved > 0 {
            summary.total_tokens_saved += saved;
            sum_savings_percent += pct;
            count_positive_savings += 1;
        }

        // Calculate correctness percentage if both correct and total exist
        if total > 0 {
            let correctness = (correct as f64 / total as f64) * 100.0;
            sum_correctness_percent += correctness;
            count_has_correctness += 1;
        }

        let tool_name = row[0].get_str().unwrap_or("unknown").to_string();
        let entry = by_tool_map
            .entry(tool_name.clone())
            .or_insert((0, 0, 0.0, 0.0, 0));
        entry.0 += 1; // calls
        if saved > 0 {
            entry.1 += saved; // total_saved
        }
        entry.2 += pct; // sum_pct
        if total > 0 {
            entry.3 += (correct as f64 / total as f64) * 100.0; // sum_correctness
            entry.4 += 1; // count_has_correctness
        }
    }

    if count_positive_savings > 0 {
        summary.average_savings_percent = sum_savings_percent / count_positive_savings as f64;
    }
    if count_has_correctness > 0 {
        summary.average_correctness_percent =
            sum_correctness_percent / count_has_correctness as f64;
    }

    for (tool_name, (calls, total_saved, sum_pct, sum_correct, count_correct)) in by_tool_map {
        summary.by_tool.push(models::ToolMetrics {
            tool_name,
            calls,
            total_saved,
            avg_savings_percent: if calls > 0 {
                sum_pct / calls as f64
            } else {
                0.0
            },
            avg_correctness_percent: if count_correct > 0 {
                sum_correct / count_correct as f64
            } else {
                0.0
            },
        });
    }

    Ok(summary)
}

pub fn cleanup_old_metrics(
    db: &CozoDb,
    retention_days: i32,
) -> Result<i64, Box<dyn std::error::Error>> {
    let cutoff_timestamp = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64
        - (retention_days as i64 * 24 * 60 * 60);

    let count_query = r#"?[tool_name, timestamp, project_path, input_tokens, output_tokens, output_elements, execution_time_ms, baseline_tokens, baseline_lines_scanned, tokens_saved, savings_percent, correct_elements, total_expected, f1_score, query_pattern, query_file, query_depth, success, is_deleted] := *context_metrics[tool_name, timestamp, project_path, input_tokens, output_tokens, output_elements, execution_time_ms, baseline_tokens, baseline_lines_scanned, tokens_saved, savings_percent, correct_elements, total_expected, f1_score, query_pattern, query_file, query_depth, success, is_deleted], timestamp < $cutoff"#;

    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "cutoff".to_string(),
        serde_json::Value::Number(cutoff_timestamp.into()),
    );

    let count_result = crate::db::schema::run_script(db, count_query, params)?;
    let deleted_count = count_result.rows.len() as i64;

    if deleted_count > 0 {
        let mut delete_params = std::collections::BTreeMap::new();
        delete_params.insert(
            "cutoff".to_string(),
            serde_json::Value::Number(cutoff_timestamp.into()),
        );
        let delete_query = r#":delete context_metrics where timestamp < $cutoff"#;
        if let Err(e) = crate::db::schema::run_script(db, delete_query, delete_params) {
            eprintln!("Warning: cleanup delete failed: {}", e);
        }
    }

    Ok(deleted_count)
}

pub fn reset_metrics(db: &CozoDb) -> Result<i64, Box<dyn std::error::Error>> {
    let count_query = r#"?[tool_name, timestamp, project_path, input_tokens, output_tokens, output_elements, execution_time_ms, baseline_tokens, baseline_lines_scanned, tokens_saved, savings_percent, correct_elements, total_expected, f1_score, query_pattern, query_file, query_depth, success, is_deleted] := *context_metrics[tool_name, timestamp, project_path, input_tokens, output_tokens, output_elements, execution_time_ms, baseline_tokens, baseline_lines_scanned, tokens_saved, savings_percent, correct_elements, total_expected, f1_score, query_pattern, query_file, query_depth, success, is_deleted]"#;

    let count_result = crate::db::schema::run_script(db, count_query, Default::default())?;
    let deleted_count = count_result.rows.len() as i64;
    if deleted_count > 0 {
        let delete_query =
            r#":delete context_metrics where tool_name != "NON_EXISTENT_TOOL_NAME_123456789""#;
        if let Err(e) = crate::db::schema::run_script(db, delete_query, Default::default()) {
            eprintln!("Warning: reset delete failed: {}", e);
        }
    }
    Ok(deleted_count)
}

// ============================================================================
// Knowledge Entries CRUD
// ============================================================================

pub fn create_knowledge_entry(
    db: &CozoDb,
    entry: &models::KnowledgeEntry,
) -> Result<models::KnowledgeEntry, Box<dyn std::error::Error>> {
    let query = r#"?[id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at] <- [[$id, $kt, $title, $content, $eq, $us, $feat, $tags, $env, $branch, $author, $cat, $uat]] :put knowledge_entries {id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at}"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "id".to_string(),
        serde_json::Value::String(entry.id.clone()),
    );
    params.insert(
        "kt".to_string(),
        serde_json::Value::String(entry.knowledge_type.clone()),
    );
    params.insert(
        "title".to_string(),
        serde_json::Value::String(entry.title.clone()),
    );
    params.insert(
        "content".to_string(),
        serde_json::Value::String(entry.content.clone()),
    );
    params.insert(
        "eq".to_string(),
        entry
            .element_qualified
            .as_ref()
            .map(|s| serde_json::Value::String(s.clone()))
            .unwrap_or(serde_json::Value::Null),
    );
    params.insert(
        "us".to_string(),
        entry
            .user_story_id
            .as_ref()
            .map(|s| serde_json::Value::String(s.clone()))
            .unwrap_or(serde_json::Value::Null),
    );
    params.insert(
        "feat".to_string(),
        entry
            .feature_id
            .as_ref()
            .map(|s| serde_json::Value::String(s.clone()))
            .unwrap_or(serde_json::Value::Null),
    );
    params.insert(
        "tags".to_string(),
        serde_json::Value::String(entry.tags.clone()),
    );
    params.insert(
        "env".to_string(),
        serde_json::Value::String(entry.environment.clone()),
    );
    params.insert(
        "branch".to_string(),
        entry
            .branch
            .as_ref()
            .map(|s| serde_json::Value::String(s.clone()))
            .unwrap_or(serde_json::Value::Null),
    );
    params.insert(
        "author".to_string(),
        serde_json::Value::String(entry.author.clone()),
    );
    params.insert(
        "cat".to_string(),
        serde_json::Value::Number(entry.created_at.into()),
    );
    params.insert(
        "uat".to_string(),
        serde_json::Value::Number(entry.updated_at.into()),
    );

    crate::db::schema::run_script(db, query, params)?;
    Ok(entry.clone())
}

pub fn get_knowledge_entry(
    db: &CozoDb,
    id: &str,
) -> Result<Option<models::KnowledgeEntry>, Box<dyn std::error::Error>> {
    let query = r#"?[id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at] := *knowledge_entries[id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at], id = $id"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert("id".to_string(), serde_json::Value::String(id.to_string()));

    let result = crate::db::schema::run_script(db, query, params)?;
    if result.rows.is_empty() {
        return Ok(None);
    }

    Ok(Some(row_to_knowledge_entry(&result.rows[0])))
}

pub fn update_knowledge_entry(
    db: &CozoDb,
    entry: &models::KnowledgeEntry,
) -> Result<models::KnowledgeEntry, Box<dyn std::error::Error>> {
    // :put acts as upsert in CozoDB
    create_knowledge_entry(db, entry)
}

pub fn delete_knowledge_entry(db: &CozoDb, id: &str) -> Result<(), Box<dyn std::error::Error>> {
    let query = r#":delete knowledge_entries where id = $id"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert("id".to_string(), serde_json::Value::String(id.to_string()));
    crate::db::schema::run_script(db, query, params)?;
    Ok(())
}

pub fn search_knowledge(
    db: &CozoDb,
    query_str: &str,
    knowledge_type: Option<&str>,
    environment: Option<&str>,
    limit: usize,
) -> Result<Vec<models::KnowledgeEntry>, Box<dyn std::error::Error>> {
    let regex_pattern = format!(".*{}.*", query_str.to_lowercase());
    let mut conditions = vec![format!(
        "regex_matches(lowercase(title), \"{}\")",
        regex_pattern
    )];
    let mut params = std::collections::BTreeMap::new();

    if let Some(kt) = knowledge_type {
        params.insert("kt".to_string(), serde_json::Value::String(kt.to_string()));
        conditions.push("knowledge_type = $kt".to_string());
    }
    if let Some(env) = environment {
        params.insert(
            "env".to_string(),
            serde_json::Value::String(env.to_string()),
        );
        conditions.push("environment = $env".to_string());
    }

    let where_clause = conditions.join(", ");
    let query = format!(
        r#"?[id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at] := *knowledge_entries[id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at], {} :limit {}"#,
        where_clause, limit
    );

    let result = crate::db::schema::run_script(db, &query, params)?;
    Ok(result
        .rows
        .iter()
        .map(|r| row_to_knowledge_entry(r))
        .collect())
}

pub fn get_knowledge_by_element(
    db: &CozoDb,
    element_qualified: &str,
) -> Result<Vec<models::KnowledgeEntry>, Box<dyn std::error::Error>> {
    let query = r#"?[id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at] := *knowledge_entries[id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at], element_qualified = $eq"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "eq".to_string(),
        serde_json::Value::String(element_qualified.to_string()),
    );

    let result = crate::db::schema::run_script(db, query, params)?;
    Ok(result
        .rows
        .iter()
        .map(|r| row_to_knowledge_entry(r))
        .collect())
}

pub fn get_knowledge_by_feature(
    db: &CozoDb,
    feature_id: &str,
) -> Result<Vec<models::KnowledgeEntry>, Box<dyn std::error::Error>> {
    let query = r#"?[id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at] := *knowledge_entries[id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at], feature_id = $feat"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "feat".to_string(),
        serde_json::Value::String(feature_id.to_string()),
    );

    let result = crate::db::schema::run_script(db, query, params)?;
    Ok(result
        .rows
        .iter()
        .map(|r| row_to_knowledge_entry(r))
        .collect())
}

pub fn get_knowledge_by_environment(
    db: &CozoDb,
    environment: &str,
    limit: usize,
) -> Result<Vec<models::KnowledgeEntry>, Box<dyn std::error::Error>> {
    let query = format!(
        r#"?[id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at] := *knowledge_entries[id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at], environment = $env :limit {}"#,
        limit
    );
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "env".to_string(),
        serde_json::Value::String(environment.to_string()),
    );

    let result = crate::db::schema::run_script(db, &query, params)?;
    Ok(result
        .rows
        .iter()
        .map(|r| row_to_knowledge_entry(r))
        .collect())
}

fn row_to_knowledge_entry(row: &[cozo::DataValue]) -> models::KnowledgeEntry {
    models::KnowledgeEntry {
        id: row[0].get_str().unwrap_or("").to_string(),
        knowledge_type: row[1].get_str().unwrap_or("general").to_string(),
        title: row[2].get_str().unwrap_or("").to_string(),
        content: row[3].get_str().unwrap_or("").to_string(),
        element_qualified: row[4].get_str().map(String::from),
        user_story_id: row[5].get_str().map(String::from),
        feature_id: row[6].get_str().map(String::from),
        tags: row[7].get_str().unwrap_or("[]").to_string(),
        environment: row[8].get_str().unwrap_or("production").to_string(),
        branch: row[9].get_str().map(String::from),
        author: row[10].get_str().unwrap_or("").to_string(),
        created_at: row[11].get_int().unwrap_or(0),
        updated_at: row[12].get_int().unwrap_or(0),
    }
}

// ============================================================================
// Incident CRUD
// ============================================================================

pub fn create_incident(
    db: &CozoDb,
    incident: &models::Incident,
) -> Result<models::Incident, Box<dyn std::error::Error>> {
    validate_incident(incident)?;
    let query = r#"?[id, env, title, severity, occurred_at, resolved_at, root_cause, resolution, affected_services, trigger_pattern, prevention, tags, author, linked_ticket] <- [[$id, $env, $title, $sev, $occ, $res_at, $rc, $res, $svc, $tp, $prev, $tags, $author, $tk]] :put incidents {id, env, title, severity, occurred_at, resolved_at, root_cause, resolution, affected_services, trigger_pattern, prevention, tags, author, linked_ticket}"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "id".to_string(),
        serde_json::Value::String(incident.id.clone()),
    );
    params.insert(
        "env".to_string(),
        serde_json::Value::String(incident.env.clone()),
    );
    params.insert(
        "title".to_string(),
        serde_json::Value::String(incident.title.clone()),
    );
    params.insert(
        "sev".to_string(),
        serde_json::Value::String(incident.severity.clone()),
    );
    params.insert(
        "occ".to_string(),
        serde_json::Value::Number(incident.occurred_at.into()),
    );
    params.insert(
        "res_at".to_string(),
        incident
            .resolved_at
            .map(|v| serde_json::Value::Number(v.into()))
            .unwrap_or(serde_json::Value::Null),
    );
    params.insert(
        "rc".to_string(),
        serde_json::Value::String(incident.root_cause.clone()),
    );
    params.insert(
        "res".to_string(),
        serde_json::Value::String(incident.resolution.clone()),
    );
    params.insert(
        "svc".to_string(),
        serde_json::Value::String(serde_json::to_string(&incident.affected_services)?),
    );
    params.insert(
        "tp".to_string(),
        incident
            .trigger_pattern
            .as_ref()
            .map(|s| serde_json::Value::String(s.clone()))
            .unwrap_or(serde_json::Value::Null),
    );
    params.insert(
        "prev".to_string(),
        incident
            .prevention
            .as_ref()
            .map(|s| serde_json::Value::String(s.clone()))
            .unwrap_or(serde_json::Value::Null),
    );
    params.insert(
        "tags".to_string(),
        serde_json::Value::String(serde_json::to_string(&incident.tags)?),
    );
    params.insert(
        "author".to_string(),
        serde_json::Value::String(incident.author.clone()),
    );
    params.insert(
        "tk".to_string(),
        incident
            .linked_ticket
            .as_ref()
            .map(|s| serde_json::Value::String(s.clone()))
            .unwrap_or(serde_json::Value::Null),
    );

    crate::db::schema::run_script(db, query, params)?;
    Ok(incident.clone())
}

pub fn validate_incident(incident: &models::Incident) -> Result<(), Box<dyn std::error::Error>> {
    if incident.title.trim().is_empty() {
        return Err("Incident title is required".into());
    }
    if !matches!(incident.severity.as_str(), "P0" | "P1" | "P2" | "P3") {
        return Err(format!(
            "Invalid severity '{}': must be P0, P1, P2, or P3",
            incident.severity
        )
        .into());
    }
    if incident.affected_services.is_empty() {
        return Err("At least one affected service is required".into());
    }
    if incident.root_cause.trim().is_empty() {
        return Err("Root cause is required".into());
    }
    if incident.resolution.trim().is_empty() {
        return Err("Resolution is required".into());
    }
    if incident.occurred_at <= 0 {
        return Err("occurred_at timestamp is required".into());
    }
    if incident.author.trim().is_empty() {
        return Err("Author is required".into());
    }
    if let Some(ref ticket) = incident.linked_ticket {
        if ticket.trim().is_empty() {
            return Err("linked_ticket must not be empty if provided".into());
        }
        if !ticket.chars().any(|c| c == '-') && !ticket.starts_with('#') {
            return Err("linked_ticket should include a project prefix (e.g., TICKET-123)".into());
        }
    }
    if let Some(ref resolved_at) = incident.resolved_at {
        if *resolved_at <= 0 {
            return Err("resolved_at must be a positive timestamp".into());
        }
        if *resolved_at < incident.occurred_at {
            return Err("resolved_at must be >= occurred_at".into());
        }
    }
    Ok(())
}

pub fn get_incident(
    db: &CozoDb,
    id: &str,
) -> Result<Option<models::Incident>, Box<dyn std::error::Error>> {
    let query = r#"?[id, env, title, severity, occurred_at, resolved_at, root_cause, resolution, affected_services, trigger_pattern, prevention, tags, author, linked_ticket] := *incidents[id, env, title, severity, occurred_at, resolved_at, root_cause, resolution, affected_services, trigger_pattern, prevention, tags, author, linked_ticket], id = $id"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert("id".to_string(), serde_json::Value::String(id.to_string()));

    let result = crate::db::schema::run_script(db, query, params)?;
    if result.rows.is_empty() {
        return Ok(None);
    }

    Ok(Some(row_to_incident(&result.rows[0])))
}

pub fn update_incident(
    db: &CozoDb,
    incident: &models::Incident,
) -> Result<models::Incident, Box<dyn std::error::Error>> {
    create_incident(db, incident)
}

pub fn delete_incident(db: &CozoDb, id: &str) -> Result<(), Box<dyn std::error::Error>> {
    let query = r#":delete incidents where id = $id"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert("id".to_string(), serde_json::Value::String(id.to_string()));
    crate::db::schema::run_script(db, query, params)?;
    Ok(())
}

pub fn query_incidents(
    db: &CozoDb,
    service: Option<&str>,
    pattern: Option<&str>,
    env: Option<&str>,
    limit: usize,
) -> Result<Vec<models::Incident>, Box<dyn std::error::Error>> {
    let mut conditions = vec![];
    let mut params = std::collections::BTreeMap::new();

    if let Some(svc) = service {
        params.insert(
            "svc".to_string(),
            serde_json::Value::String(format!(".*{}.*", regex::escape(&svc.to_lowercase()))),
        );
        conditions.push("regex_matches(lowercase(affected_services), $svc)".to_string());
    }
    if let Some(pat) = pattern {
        let pattern = format!(".*{}.*", regex::escape(&pat.to_lowercase()));
        params.insert("pat".to_string(), serde_json::Value::String(pattern));
        conditions.push(
            "(regex_matches(lowercase(title), $pat) or regex_matches(lowercase(root_cause), $pat))"
                .to_string(),
        );
    }
    if let Some(e) = env {
        params.insert("env".to_string(), serde_json::Value::String(e.to_string()));
        conditions.push("env = $env".to_string());
    }

    let where_clause = if conditions.is_empty() {
        "".to_string()
    } else {
        format!(", {}", conditions.join(", "))
    };

    let query = format!(
        r#"?[id, env, title, severity, occurred_at, resolved_at, root_cause, resolution, affected_services, trigger_pattern, prevention, tags, author, linked_ticket] := *incidents[id, env, title, severity, occurred_at, resolved_at, root_cause, resolution, affected_services, trigger_pattern, prevention, tags, author, linked_ticket]{} :limit {}"#,
        where_clause, limit
    );

    let result = crate::db::schema::run_script(db, &query, params)?;
    Ok(result.rows.iter().map(|r| row_to_incident(r)).collect())
}

pub fn get_incidents_by_service(
    db: &CozoDb,
    service: &str,
    env: Option<&str>,
    limit: usize,
) -> Result<Vec<models::Incident>, Box<dyn std::error::Error>> {
    query_incidents(db, Some(service), None, env, limit)
}

fn row_to_incident(row: &[cozo::DataValue]) -> models::Incident {
    let affected_services: Vec<String> =
        serde_json::from_str(row[8].get_str().unwrap_or("[]")).unwrap_or_default();
    let tags: Vec<String> =
        serde_json::from_str(row[11].get_str().unwrap_or("[]")).unwrap_or_default();

    models::Incident {
        id: row[0].get_str().unwrap_or("").to_string(),
        env: row[1].get_str().unwrap_or("local").to_string(),
        title: row[2].get_str().unwrap_or("").to_string(),
        severity: row[3].get_str().unwrap_or("").to_string(),
        occurred_at: row[4].get_int().unwrap_or(0),
        resolved_at: row[5].get_int(),
        root_cause: row[6].get_str().unwrap_or("").to_string(),
        resolution: row[7].get_str().unwrap_or("").to_string(),
        affected_services,
        trigger_pattern: row[9].get_str().map(String::from),
        prevention: row[10].get_str().map(String::from),
        tags,
        author: row[12].get_str().unwrap_or("").to_string(),
        linked_ticket: row[13].get_str().map(String::from),
    }
}

// ============================================================================
// Environment-scoped element queries
// ============================================================================

pub fn get_elements_by_env(
    db: &CozoDb,
    env: &str,
    limit: usize,
) -> Result<Vec<models::CodeElement>, Box<dyn std::error::Error>> {
    let tail = if crate::db::schema::run_script(db,
            "?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] :limit 0",
            Default::default(),
        )
        .is_ok()
    {
        ", env, ontology_layer"
    } else {
        ", env"
    };
    let query = format!(
        r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{}], env = $env :limit {}"#,
        tail, limit
    );
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "env".to_string(),
        serde_json::Value::String(env.to_string()),
    );

    let result = crate::db::schema::run_script(db, &query, params)?;
    Ok(result.rows.iter().map(|r| row_to_code_element(r)).collect())
}

pub fn get_relationships_by_env(
    db: &CozoDb,
    env: &str,
    limit: usize,
) -> Result<Vec<models::Relationship>, Box<dyn std::error::Error>> {
    let query = format!(
        r#"?[source_qualified, target_qualified, rel_type, confidence, metadata, env] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, env], env = $env :limit {}"#,
        limit
    );
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "env".to_string(),
        serde_json::Value::String(env.to_string()),
    );

    let result = crate::db::schema::run_script(db, &query, params)?;
    Ok(result.rows.iter().map(|r| row_to_relationship(r)).collect())
}

pub fn get_element_across_envs(
    db: &CozoDb,
    qualified_name: &str,
) -> Result<Vec<(String, models::CodeElement)>, Box<dyn std::error::Error>> {
    let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env], qualified_name = $qn"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "qn".to_string(),
        serde_json::Value::String(qualified_name.to_string()),
    );

    let result = crate::db::schema::run_script(db, query, params)?;
    Ok(result
        .rows
        .iter()
        .map(|row| {
            let env = row[11].get_str().unwrap_or("local").to_string();
            (env, row_to_code_element(row))
        })
        .collect())
}

fn row_to_code_element(row: &[cozo::DataValue]) -> models::CodeElement {
    let parent_qualified = row[7].get_str().map(String::from);
    let cluster_id = row[8].get_str().map(String::from);
    let cluster_label = row[9].get_str().map(String::from);
    let metadata_str = row[10].get_str().unwrap_or("{}");
    models::CodeElement {
        qualified_name: row[0].get_str().unwrap_or("").to_string(),
        element_type: row[1].get_str().unwrap_or("").to_string(),
        name: row[2].get_str().unwrap_or("").to_string(),
        file_path: row[3].get_str().unwrap_or("").to_string(),
        line_start: row[4].get_int().unwrap_or(0) as u32,
        line_end: row[5].get_int().unwrap_or(0) as u32,
        language: row[6].get_str().unwrap_or("").to_string(),
        parent_qualified,
        cluster_id,
        cluster_label,
        metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
        env: row[11].get_str().unwrap_or("local").to_string(),
    }
}

fn row_to_relationship(row: &[cozo::DataValue]) -> models::Relationship {
    let metadata_str = row[4].get_str().unwrap_or("{}");
    models::Relationship {
        id: None,
        source_qualified: row[0].get_str().unwrap_or("").to_string(),
        target_qualified: row[1].get_str().unwrap_or("").to_string(),
        rel_type: row[2].get_str().unwrap_or("").to_string(),
        confidence: row[3].get_float().unwrap_or(1.0),
        metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
        env: row[5].get_str().unwrap_or("local").to_string(),
    }
}

pub fn upsert_service_metadata(db: &CozoDb, svc: &models::ServiceMetadata) -> Result<(), String> {
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64;
    let query = r#"?[service_name, env, team, on_call, repo_url, language, health_endpoint, slo_p99_ms, incident_count, last_incident, tags, version, deploy_envs, created_at, updated_at] <- [[$svc, $env, $team, $oncall, $repo, $lang, $health, $slo, $icount, $lastinc, $tags, $ver, $denvs, $cat, $uat]] :put service_metadata {service_name, env, team, on_call, repo_url, language, health_endpoint, slo_p99_ms, incident_count, last_incident, tags, version, deploy_envs, created_at, updated_at}"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "svc".into(),
        serde_json::Value::String(svc.service_name.clone()),
    );
    params.insert("env".into(), serde_json::Value::String(svc.env.clone()));
    params.insert(
        "team".into(),
        serde_json::Value::String(svc.team.clone().unwrap_or_default()),
    );
    params.insert(
        "oncall".into(),
        serde_json::Value::String(svc.on_call.clone().unwrap_or_default()),
    );
    params.insert(
        "repo".into(),
        serde_json::Value::String(svc.repo_url.clone().unwrap_or_default()),
    );
    params.insert(
        "lang".into(),
        serde_json::Value::String(svc.language.clone().unwrap_or_default()),
    );
    params.insert(
        "health".into(),
        serde_json::Value::String(svc.health_endpoint.clone().unwrap_or_default()),
    );
    params.insert(
        "slo".into(),
        serde_json::Value::Number((svc.slo_p99_ms.unwrap_or(0)).into()),
    );
    params.insert(
        "icount".into(),
        serde_json::Value::Number(svc.incident_count.into()),
    );
    params.insert(
        "lastinc".into(),
        serde_json::Value::Number(svc.last_incident.unwrap_or(0).into()),
    );
    params.insert("tags".into(), serde_json::Value::String(svc.tags.clone()));
    params.insert(
        "ver".into(),
        serde_json::Value::String(svc.version.clone().unwrap_or_default()),
    );
    params.insert(
        "denvs".into(),
        serde_json::Value::String(svc.deploy_envs.clone()),
    );
    params.insert("cat".into(), serde_json::Value::Number(now.into()));
    params.insert("uat".into(), serde_json::Value::Number(now.into()));
    crate::db::schema::run_script(db, query, params)
        .map_err(|e| format!("upsert_service_metadata: {}", e))?;
    Ok(())
}

pub fn get_service_metadata(
    db: &CozoDb,
    service_name: &str,
    env: &str,
) -> Result<Option<models::ServiceMetadata>, String> {
    let query = r#"?[service_name, env, team, on_call, repo_url, language, health_endpoint, slo_p99_ms, incident_count, last_incident, tags, version, deploy_envs, created_at, updated_at] := *service_metadata{service_name, env, team, on_call, repo_url, language, health_endpoint, slo_p99_ms, incident_count, last_incident, tags, version, deploy_envs, created_at, updated_at}, service_name == $svc, env == $env"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "svc".into(),
        serde_json::Value::String(service_name.to_string()),
    );
    params.insert("env".into(), serde_json::Value::String(env.to_string()));
    let result = crate::db::schema::run_script(db, query, params)
        .map_err(|e| format!("get_service_metadata: {}", e))?;
    if result.rows.is_empty() {
        return Ok(None);
    }
    let row = &result.rows[0];
    Ok(Some(models::ServiceMetadata {
        service_name: row[0].get_str().unwrap_or("").to_string(),
        env: row[1].get_str().unwrap_or("local").to_string(),
        team: row[2]
            .get_str()
            .map(|s: &str| s.to_string())
            .filter(|s| !s.is_empty()),
        on_call: row[3]
            .get_str()
            .map(|s: &str| s.to_string())
            .filter(|s| !s.is_empty()),
        repo_url: row[4]
            .get_str()
            .map(|s: &str| s.to_string())
            .filter(|s| !s.is_empty()),
        language: row[5]
            .get_str()
            .map(|s: &str| s.to_string())
            .filter(|s| !s.is_empty()),
        health_endpoint: row[6]
            .get_str()
            .map(|s: &str| s.to_string())
            .filter(|s| !s.is_empty()),
        slo_p99_ms: row[7].get_int().map(|v| v as i32),
        incident_count: row[8].get_int().unwrap_or(0) as i32,
        last_incident: row[9].get_int(),
        tags: row[10].get_str().unwrap_or("").to_string(),
        version: row[11]
            .get_str()
            .map(|s: &str| s.to_string())
            .filter(|s| !s.is_empty()),
        deploy_envs: row[12].get_str().unwrap_or("").to_string(),
        created_at: row[13].get_int().unwrap_or(0),
        updated_at: row[14].get_int().unwrap_or(0),
    }))
}

// ============================================================================
// Team CRUD
// ============================================================================

pub fn create_team(
    db: &CozoDb,
    team: &models::Team,
) -> Result<models::Team, Box<dyn std::error::Error>> {
    let query = r#"?[id, name, description, owner_id, created_at, updated_at, graph_read_users, graph_write_users, members] <- [[$id, $name, $desc, $owner, $cat, $uat, $read_users, $write_users, $members]] :put teams {id, name, description, owner_id, created_at, updated_at, graph_read_users, graph_write_users, members}"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert("id".to_string(), serde_json::Value::String(team.id.clone()));
    params.insert(
        "name".to_string(),
        serde_json::Value::String(team.name.clone()),
    );
    params.insert(
        "desc".to_string(),
        serde_json::Value::String(team.description.clone()),
    );
    params.insert(
        "owner".to_string(),
        serde_json::Value::String(team.owner_id.clone()),
    );
    params.insert(
        "cat".to_string(),
        serde_json::Value::Number(team.created_at.into()),
    );
    params.insert(
        "uat".to_string(),
        serde_json::Value::Number(team.updated_at.into()),
    );
    params.insert(
        "read_users".to_string(),
        serde_json::Value::String(serde_json::to_string(&team.graph_read_users)?),
    );
    params.insert(
        "write_users".to_string(),
        serde_json::Value::String(serde_json::to_string(&team.graph_write_users)?),
    );
    params.insert(
        "members".to_string(),
        serde_json::Value::String(serde_json::to_string(&team.members)?),
    );

    crate::db::schema::run_script(db, query, params)?;
    Ok(team.clone())
}

pub fn get_team(db: &CozoDb, id: &str) -> Result<Option<models::Team>, Box<dyn std::error::Error>> {
    let query = r#"?[id, name, description, owner_id, created_at, updated_at, graph_read_users, graph_write_users, members] := *teams[id, name, description, owner_id, created_at, updated_at, graph_read_users, graph_write_users, members], id = $id"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert("id".to_string(), serde_json::Value::String(id.to_string()));

    let result = crate::db::schema::run_script(db, query, params)?;
    if result.rows.is_empty() {
        return Ok(None);
    }
    Ok(Some(row_to_team(&result.rows[0])))
}

pub fn update_team(
    db: &CozoDb,
    team: &models::Team,
) -> Result<models::Team, Box<dyn std::error::Error>> {
    create_team(db, team)
}

pub fn delete_team(db: &CozoDb, id: &str) -> Result<(), Box<dyn std::error::Error>> {
    let query = r#":delete teams where id = $id"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert("id".to_string(), serde_json::Value::String(id.to_string()));
    crate::db::schema::run_script(db, query, params)?;
    Ok(())
}

pub fn list_teams(db: &CozoDb) -> Result<Vec<models::Team>, Box<dyn std::error::Error>> {
    let query = r#"?[id, name, description, owner_id, created_at, updated_at, graph_read_users, graph_write_users, members] := *teams[id, name, description, owner_id, created_at, updated_at, graph_read_users, graph_write_users, members]"#;
    let result = crate::db::schema::run_script(db, query, Default::default())?;
    Ok(result.rows.iter().map(|r| row_to_team(r)).collect())
}

fn row_to_team(row: &[cozo::DataValue]) -> models::Team {
    let graph_read_users: Vec<String> =
        serde_json::from_str(row[6].get_str().unwrap_or("[]")).unwrap_or_default();
    let graph_write_users: Vec<String> =
        serde_json::from_str(row[7].get_str().unwrap_or("[]")).unwrap_or_default();
    let members: Vec<models::TeamMember> =
        serde_json::from_str(row[8].get_str().unwrap_or("[]")).unwrap_or_default();

    models::Team {
        id: row[0].get_str().unwrap_or("").to_string(),
        name: row[1].get_str().unwrap_or("").to_string(),
        description: row[2].get_str().unwrap_or("").to_string(),
        owner_id: row[3].get_str().unwrap_or("").to_string(),
        created_at: row[4].get_int().unwrap_or(0),
        updated_at: row[5].get_int().unwrap_or(0),
        graph_read_users,
        graph_write_users,
        members,
    }
}

// ============================================================================
// Team Invite CRUD
// ============================================================================

pub fn create_team_invite(
    db: &CozoDb,
    invite: &models::TeamInvite,
) -> Result<models::TeamInvite, Box<dyn std::error::Error>> {
    let query = r#"?[token, team_id, email, role, created_by, created_at, expires_at, accepted, accepted_by] <- [[$token, $tid, $email, $role, $by, $cat, $exp, $acc, $accept]] :put team_invites {token, team_id, email, role, created_by, created_at, expires_at, accepted, accepted_by}"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "token".to_string(),
        serde_json::Value::String(invite.token.clone()),
    );
    params.insert(
        "tid".to_string(),
        serde_json::Value::String(invite.team_id.clone()),
    );
    params.insert(
        "email".to_string(),
        invite
            .email
            .as_ref()
            .map(|s| serde_json::Value::String(s.clone()))
            .unwrap_or(serde_json::Value::Null),
    );
    params.insert(
        "role".to_string(),
        serde_json::Value::String(invite.role.clone()),
    );
    params.insert(
        "by".to_string(),
        serde_json::Value::String(invite.created_by.clone()),
    );
    params.insert(
        "cat".to_string(),
        serde_json::Value::Number(invite.created_at.into()),
    );
    params.insert(
        "exp".to_string(),
        serde_json::Value::Number(invite.expires_at.into()),
    );
    params.insert("acc".to_string(), serde_json::Value::Bool(invite.accepted));
    params.insert(
        "accept".to_string(),
        invite
            .accepted_by
            .as_ref()
            .map(|s| serde_json::Value::String(s.clone()))
            .unwrap_or(serde_json::Value::Null),
    );

    crate::db::schema::run_script(db, query, params)?;
    Ok(invite.clone())
}

pub fn get_team_invite(
    db: &CozoDb,
    token: &str,
) -> Result<Option<models::TeamInvite>, Box<dyn std::error::Error>> {
    let query = r#"?[token, team_id, email, role, created_by, created_at, expires_at, accepted, accepted_by] := *team_invites[token, team_id, email, role, created_by, created_at, expires_at, accepted, accepted_by], token = $token"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "token".to_string(),
        serde_json::Value::String(token.to_string()),
    );

    let result = crate::db::schema::run_script(db, query, params)?;
    if result.rows.is_empty() {
        return Ok(None);
    }
    Ok(Some(row_to_team_invite(&result.rows[0])))
}

pub fn get_team_invites(
    db: &CozoDb,
    team_id: &str,
) -> Result<Vec<models::TeamInvite>, Box<dyn std::error::Error>> {
    let query = r#"?[token, team_id, email, role, created_by, created_at, expires_at, accepted, accepted_by] := *team_invites[token, team_id, email, role, created_by, created_at, expires_at, accepted, accepted_by], team_id = $tid"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "tid".to_string(),
        serde_json::Value::String(team_id.to_string()),
    );

    let result = crate::db::schema::run_script(db, query, params)?;
    Ok(result.rows.iter().map(|r| row_to_team_invite(r)).collect())
}

pub fn accept_team_invite(
    db: &CozoDb,
    token: &str,
    user_id: &str,
) -> Result<models::TeamInvite, Box<dyn std::error::Error>> {
    let invite = get_team_invite(db, token)?.ok_or("Invite not found")?;

    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64;

    if now > invite.expires_at {
        return Err("Invite has expired".into());
    }
    if invite.accepted {
        return Err("Invite already accepted".into());
    }

    let updated_invite = models::TeamInvite {
        accepted: true,
        accepted_by: Some(user_id.to_string()),
        ..invite
    };
    create_team_invite(db, &updated_invite)
}

pub fn delete_team_invite(db: &CozoDb, token: &str) -> Result<(), Box<dyn std::error::Error>> {
    let query = r#":delete team_invites where token = $token"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "token".to_string(),
        serde_json::Value::String(token.to_string()),
    );
    crate::db::schema::run_script(db, query, params)?;
    Ok(())
}

fn row_to_team_invite(row: &[cozo::DataValue]) -> models::TeamInvite {
    models::TeamInvite {
        token: row[0].get_str().unwrap_or("").to_string(),
        team_id: row[1].get_str().unwrap_or("").to_string(),
        email: row[2].get_str().map(String::from),
        role: row[3].get_str().unwrap_or("").to_string(),
        created_by: row[4].get_str().unwrap_or("").to_string(),
        created_at: row[5].get_int().unwrap_or(0),
        expires_at: row[6].get_int().unwrap_or(0),
        accepted: row[7].get_bool().unwrap_or(false),
        accepted_by: row[8].get_str().map(String::from),
    }
}

// ============================================================================
// Permission checking helpers
// ============================================================================

pub fn check_graph_permission(
    db: &CozoDb,
    team_id: &str,
    user_id: &str,
    require_write: bool,
) -> Result<bool, Box<dyn std::error::Error>> {
    let team = get_team(db, team_id)?;
    let team = team.as_ref().ok_or("Team not found")?;

    if team.graph_write_users.contains(&user_id.to_string()) {
        return Ok(true);
    }
    if !require_write && team.graph_read_users.contains(&user_id.to_string()) {
        return Ok(true);
    }
    if team.owner_id == user_id {
        return Ok(true);
    }
    Ok(false)
}

pub fn add_team_member(
    db: &CozoDb,
    team_id: &str,
    user_id: &str,
    role: &str,
) -> Result<models::Team, Box<dyn std::error::Error>> {
    let team = get_team(db, team_id)?.ok_or("Team not found")?;

    let member = models::TeamMember {
        user_id: user_id.to_string(),
        role: role.to_string(),
        joined_at: std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_secs() as i64,
    };

    let mut updated_team = team.clone();
    updated_team.members.push(member);

    if role == "viewer" && !updated_team.graph_read_users.contains(&user_id.to_string()) {
        updated_team.graph_read_users.push(user_id.to_string());
    } else if role != "viewer"
        && !updated_team
            .graph_write_users
            .contains(&user_id.to_string())
    {
        updated_team.graph_write_users.push(user_id.to_string());
    }

    updated_team.updated_at = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64;

    update_team(db, &updated_team)
}

pub fn remove_team_member(
    db: &CozoDb,
    team_id: &str,
    user_id: &str,
) -> Result<models::Team, Box<dyn std::error::Error>> {
    let team = get_team(db, team_id)?.ok_or("Team not found")?;

    let mut updated_team = team.clone();
    updated_team.members.retain(|m| m.user_id != user_id);
    updated_team.graph_read_users.retain(|u| u != user_id);
    updated_team.graph_write_users.retain(|u| u != user_id);
    updated_team.updated_at = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64;

    update_team(db, &updated_team)
}
