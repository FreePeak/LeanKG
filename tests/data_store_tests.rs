use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;
use std::fs;
use std::path::Path;

#[derive(Debug)]
pub struct DataStoreCheckResult {
    pub elements_checked: usize,
    pub elements_valid: usize,
    pub elements_invalid: Vec<String>,
    pub relationships_checked: usize,
    pub relationships_valid: usize,
    pub relationships_invalid: Vec<String>,
    pub duplicates_found: usize,
    pub duplicate_names: Vec<String>,
}

pub fn check_indexed_elements_exist(
    db_path: &Path,
    source_root: &Path,
) -> Result<DataStoreCheckResult, Box<dyn std::error::Error>> {
    let db = init_db(db_path)?;
    let graph_engine = GraphEngine::new(db);

    let elements = graph_engine.all_elements()?;
    let mut elements_valid = 0;
    let mut elements_invalid = Vec::new();

    for elem in &elements {
        let file_path = source_root.join(&elem.file_path);
        if file_path.exists() {
            let content = fs::read_to_string(&file_path)?;
            let lines: Vec<&str> = content.lines().collect();
            let line_count = lines.len() as u32;

            if elem.line_start >= 1
                && elem.line_end >= elem.line_start
                && elem.line_end <= line_count
            {
                elements_valid += 1;
            } else {
                elements_invalid.push(format!(
                    "{}: line {}-{} exceeds file {} ({} lines)",
                    elem.qualified_name, elem.line_start, elem.line_end, elem.file_path, line_count
                ));
            }
        } else {
            elements_invalid.push(format!(
                "{}: file {} does not exist",
                elem.qualified_name, elem.file_path
            ));
        }
    }

    Ok(DataStoreCheckResult {
        elements_checked: elements.len(),
        elements_valid,
        elements_invalid,
        relationships_checked: 0,
        relationships_valid: 0,
        relationships_invalid: Vec::new(),
        duplicates_found: 0,
        duplicate_names: Vec::new(),
    })
}

pub fn check_no_duplicate_elements(
    db_path: &Path,
) -> Result<DataStoreCheckResult, Box<dyn std::error::Error>> {
    let db = init_db(db_path)?;
    let graph_engine = GraphEngine::new(db);

    let elements = graph_engine.all_elements()?;
    let mut name_counts: std::collections::HashMap<String, usize> =
        std::collections::HashMap::new();

    for elem in &elements {
        *name_counts.entry(elem.qualified_name.clone()).or_insert(0) += 1;
    }

    let duplicate_names: Vec<String> = name_counts
        .iter()
        .filter(|(_, c)| **c > 1)
        .map(|(n, _)| n.clone())
        .collect();

    Ok(DataStoreCheckResult {
        elements_checked: elements.len(),
        elements_valid: elements.len(),
        elements_invalid: Vec::new(),
        relationships_checked: 0,
        relationships_valid: 0,
        relationships_invalid: Vec::new(),
        duplicates_found: duplicate_names.len(),
        duplicate_names,
    })
}

pub fn check_relationship_validity(
    db_path: &Path,
    source_root: &Path,
) -> Result<DataStoreCheckResult, Box<dyn std::error::Error>> {
    let db = init_db(db_path)?;
    let graph_engine = GraphEngine::new(db);

    let relationships = graph_engine.all_relationships()?;
    let mut relationships_valid = 0;
    let mut relationships_invalid = Vec::new();

    for rel in &relationships {
        if rel.rel_type == "calls" {
            let target_file = source_root.join(
                &rel.target_qualified
                    .split("::")
                    .next()
                    .unwrap_or(&rel.target_qualified),
            );
            if target_file.exists() {
                let content = fs::read_to_string(&target_file)?;
                let target_name = rel.target_qualified.split("::").last().unwrap_or("");

                if content.contains(&format!("{}", target_name)) {
                    relationships_valid += 1;
                } else {
                    relationships_invalid.push(format!(
                        "{} -> {} ({}): target function '{}' not found in source",
                        rel.source_qualified, rel.target_qualified, rel.rel_type, target_name
                    ));
                }
            }
        } else {
            relationships_valid += 1;
        }
    }

    Ok(DataStoreCheckResult {
        elements_checked: 0,
        elements_valid: 0,
        elements_invalid: Vec::new(),
        relationships_checked: relationships.len(),
        relationships_valid,
        relationships_invalid,
        duplicates_found: 0,
        duplicate_names: Vec::new(),
    })
}

pub fn generate_data_store_report(result: &DataStoreCheckResult) -> String {
    let mut report = String::from("# Data Store Correctness Report\n\n");

    if result.elements_checked > 0 {
        report.push_str("## Elements\n");
        report.push_str(&format!(
            "- Checked: {}\n- Valid: {}\n- Invalid: {}\n",
            result.elements_checked,
            result.elements_valid,
            result.elements_invalid.len()
        ));
        if !result.elements_invalid.is_empty() {
            report.push_str("### Invalid Elements\n");
            for err in &result.elements_invalid {
                report.push_str(&format!("- {}\n", err));
            }
        }
    }

    if result.relationships_checked > 0 {
        report.push_str("\n## Relationships\n");
        report.push_str(&format!(
            "- Checked: {}\n- Valid: {}\n- Invalid: {}\n",
            result.relationships_checked,
            result.relationships_valid,
            result.relationships_invalid.len()
        ));
        if !result.relationships_invalid.is_empty() {
            report.push_str("### Invalid Relationships\n");
            for err in &result.relationships_invalid {
                report.push_str(&format!("- {}\n", err));
            }
        }
    }

    if result.duplicates_found > 0 {
        report.push_str("\n## Duplicates\n");
        report.push_str(&format!(
            "- Found: {} duplicate qualified names\n",
            result.duplicates_found
        ));
        for name in &result.duplicate_names {
            report.push_str(&format!("- {}\n", name));
        }
    }

    report
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_data_store_result_struct() {
        let result = DataStoreCheckResult {
            elements_checked: 10,
            elements_valid: 8,
            elements_invalid: vec!["elem1".to_string(), "elem2".to_string()],
            relationships_checked: 5,
            relationships_valid: 5,
            relationships_invalid: vec![],
            duplicates_found: 1,
            duplicate_names: vec!["dup::test".to_string()],
        };

        assert_eq!(result.elements_checked, 10);
        assert_eq!(result.elements_valid, 8);
        assert_eq!(result.duplicates_found, 1);
    }

    #[test]
    fn test_generate_report_format() {
        let result = DataStoreCheckResult {
            elements_checked: 100,
            elements_valid: 95,
            elements_invalid: vec!["test::elem".to_string()],
            relationships_checked: 50,
            relationships_valid: 48,
            relationships_invalid: vec!["bad::rel".to_string()],
            duplicates_found: 2,
            duplicate_names: vec!["dup1".to_string(), "dup2".to_string()],
        };

        let report = generate_data_store_report(&result);
        assert!(report.contains("# Data Store Correctness Report"));
        assert!(report.contains("Checked: 100"));
        assert!(report.contains("Invalid: 1"));
        assert!(report.contains("Found: 2"));
    }

    #[test]
    fn test_empty_result_report() {
        let result = DataStoreCheckResult {
            elements_checked: 0,
            elements_valid: 0,
            elements_invalid: vec![],
            relationships_checked: 0,
            relationships_valid: 0,
            relationships_invalid: vec![],
            duplicates_found: 0,
            duplicate_names: vec![],
        };

        let report = generate_data_store_report(&result);
        assert!(report.contains("# Data Store Correctness Report"));
        assert!(report.len() > 30);
    }
}
