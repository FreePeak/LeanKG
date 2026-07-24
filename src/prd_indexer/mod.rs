/// PRD Markdown Parser
///
/// Parses `docs/prd.md` to extract FR-* (Feature Requirements) and US-* (User Stories)
/// into structured `knowledge_entries` rows with auto-linking to ontology workflows.
use regex::Regex;
use serde::{Deserialize, Serialize};

use crate::db::models::KnowledgeEntry;

/// A parsed feature requirement from the PRD.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PrdRequirement {
    pub id: String,
    pub title: String,
    pub description: String,
    pub priority: String, // "Must Have" | "Should Have" | "Could Have"
    pub focus: String,    // "P0" | "P1" | "P2" | "P3"
    pub user_story_ids: Vec<String>,
    pub related_fr_ids: Vec<String>,
    pub code_paths: Vec<String>, // file paths mentioned in description
}

/// A parsed user story from the PRD.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PrdUserStory {
    pub id: String,
    pub title: String,
    pub description: String,
    pub priority: String,
    pub focus: String,
    pub feature_ids: Vec<String>,
}

/// Result of parsing the PRD markdown.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PrdParseResult {
    pub requirements: Vec<PrdRequirement>,
    pub user_stories: Vec<PrdUserStory>,
    pub errors: Vec<String>,
}

/// Extracts FR-* and US-* entries from PRD markdown text.
pub fn parse_prd_markdown(content: &str) -> PrdParseResult {
    let mut requirements: Vec<PrdRequirement> = Vec::new();
    let mut user_stories: Vec<PrdUserStory> = Vec::new();
    let errors: Vec<String> = Vec::new();

    let fr_re = Regex::new(r"FR-[A-Z0-9-]+").unwrap();
    let us_re = Regex::new(r"US-[A-Z0-9-]+").unwrap();

    // Parse PRD version changelog table rows.
    // Format: | ID | Priority | Focus | Intent |
    // Where ID is like: US-ONT-PROC-01 / FR-ONT-PROC-01..03 / REL-059
    let _table_row_re = Regex::new(
        r"\|\s*([A-Z]+-[A-Z0-9.-]+(?:\s*/\s*[A-Z]+-[A-Z0-9.-]+)*)\s*\|\s*([^|]+)\s*\|\s*\*?\*?(P[0-3])\*?\*?\s*\|\s*(.+?)\s*\|"
    ).unwrap();

    // Also try the simpler format: | ID | Priority | Focus | Summary |
    let simple_row_re = Regex::new(
        r"\|\s*([A-Z]+-[A-Z0-9.-]+(?:\s*\.\.\s*[A-Z]+-[A-Z0-9.-]+)*(?:\s*/\s*[A-Z]+-[A-Z0-9.-]+)*)\s*\|\s*([^|]+)\s*\|\s*\*?\*?(P[0-3])\*?\*?\s*\|\s*(.+?)\s*\|"
    ).unwrap();

    // Extract code paths from descriptions (backtick-quoted paths)
    let code_path_re =
        Regex::new(r"`([a-zA-Z0-9_/.-]+\.[a-zA-Z]+(::[a-zA-Z0-9_]+)?(\([^)]*\))?)`").unwrap();

    // Scan all table rows for FR/US references
    for row_re in [&simple_row_re] {
        for caps in row_re.captures_iter(content) {
            let id_field = caps.get(1).map_or("", |m| m.as_str()).trim().to_string();
            let priority = caps.get(2).map_or("", |m| m.as_str()).trim().to_string();
            let focus = caps.get(3).map_or("", |m| m.as_str()).trim().to_string();
            let description = caps.get(4).map_or("", |m| m.as_str()).trim().to_string();

            // Split compound IDs like "US-ONT-PROC-01 / FR-ONT-PROC-01..03 / REL-059"
            let parts: Vec<&str> = id_field.split('/').map(|s| s.trim()).collect();
            let mut frs_in_row: Vec<String> = Vec::new();
            let mut uss_in_row: Vec<String> = Vec::new();

            for part in &parts {
                let part = part.trim();
                if part.starts_with("FR-") {
                    // Handle ranges like FR-ONT-PROC-01..03
                    frs_in_row.push(part.to_string());
                } else if part.starts_with("US-") {
                    uss_in_row.push(part.to_string());
                }
            }

            // Extract code paths from description
            let code_paths: Vec<String> = code_path_re
                .captures_iter(&description)
                .map(|c| c.get(1).unwrap().as_str().to_string())
                .collect();

            // Create FR entries
            for fr_id in &frs_in_row {
                // Check if we already have this FR
                if requirements.iter().any(|r| r.id == *fr_id) {
                    continue;
                }
                requirements.push(PrdRequirement {
                    id: fr_id.clone(),
                    title: fr_id.clone(), // Title derived from the FR ID
                    description: description.clone(),
                    priority: priority.clone(),
                    focus: focus.clone(),
                    user_story_ids: uss_in_row.clone(),
                    related_fr_ids: frs_in_row.iter().filter(|f| f != &fr_id).cloned().collect(),
                    code_paths: code_paths.clone(),
                });
            }

            // Create US entries
            for us_id in &uss_in_row {
                if user_stories.iter().any(|s| s.id == *us_id) {
                    continue;
                }
                user_stories.push(PrdUserStory {
                    id: us_id.clone(),
                    title: us_id.clone(),
                    description: description.clone(),
                    priority: priority.clone(),
                    focus: focus.clone(),
                    feature_ids: frs_in_row.clone(),
                });
            }
        }
    }

    // Also scan for standalone FR/US mentions outside tables
    // (e.g. in prose like "US-ONT-PROC-01 / FR-ONT-PROC-01")
    for caps in fr_re.captures_iter(content) {
        let fr_id = caps.get(0).unwrap().as_str();
        if !requirements.iter().any(|r| r.id == fr_id) {
            requirements.push(PrdRequirement {
                id: fr_id.to_string(),
                title: fr_id.to_string(),
                description: String::new(),
                priority: String::new(),
                focus: String::new(),
                user_story_ids: Vec::new(),
                related_fr_ids: Vec::new(),
                code_paths: Vec::new(),
            });
        }
    }

    for caps in us_re.captures_iter(content) {
        let us_id = caps.get(0).unwrap().as_str();
        if !user_stories.iter().any(|s| s.id == us_id) {
            user_stories.push(PrdUserStory {
                id: us_id.to_string(),
                title: us_id.to_string(),
                description: String::new(),
                priority: String::new(),
                focus: String::new(),
                feature_ids: Vec::new(),
            });
        }
    }

    PrdParseResult {
        requirements,
        user_stories,
        errors,
    }
}

/// Converts parsed PRD requirements into `KnowledgeEntry` rows.
pub fn requirements_to_knowledge_entries(
    requirements: &[PrdRequirement],
    environment: &str,
) -> Vec<KnowledgeEntry> {
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64;

    requirements
        .iter()
        .map(|req| KnowledgeEntry {
            id: format!("prd-req-{}", req.id),
            knowledge_type: "prd_mapping".to_string(),
            title: format!("{} {}", req.id, req.title),
            content: format!(
                "Priority: {} | Focus: {}\n\nDescription: {}\n\nRelated FRs: {}\n\nRelated US: {}\n\nCode paths: {}",
                req.priority,
                req.focus,
                req.description,
                req.related_fr_ids.join(", "),
                req.user_story_ids.join(", "),
                req.code_paths.join(", ")
            ),
            element_qualified: None,
            user_story_id: if req.user_story_ids.is_empty() {
                None
            } else {
                Some(req.user_story_ids.join(","))
            },
            feature_id: Some(req.id.clone()),
            tags: format!("{},{}", req.priority.replace(' ', "-"), req.focus),
            environment: environment.to_string(),
            branch: None,
            author: "prd_indexer".to_string(),
            created_at: now,
            updated_at: now,
        })
        .collect()
}

/// Converts parsed PRD user stories into `KnowledgeEntry` rows.
pub fn user_stories_to_knowledge_entries(
    stories: &[PrdUserStory],
    environment: &str,
) -> Vec<KnowledgeEntry> {
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64;

    stories
        .iter()
        .map(|story| KnowledgeEntry {
            id: format!("prd-us-{}", story.id),
            knowledge_type: "prd_mapping".to_string(),
            title: format!("{} {}", story.id, story.title),
            content: format!(
                "Priority: {} | Focus: {}\n\nDescription: {}\n\nRelated FRs: {}",
                story.priority,
                story.focus,
                story.description,
                story.feature_ids.join(", ")
            ),
            element_qualified: None,
            user_story_id: Some(story.id.clone()),
            feature_id: if story.feature_ids.is_empty() {
                None
            } else {
                Some(story.feature_ids.join(","))
            },
            tags: format!("{},{}", story.priority.replace(' ', "-"), story.focus),
            environment: environment.to_string(),
            branch: None,
            author: "prd_indexer".to_string(),
            created_at: now,
            updated_at: now,
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_prd_table_rows() {
        let md = r#"
| ID | Priority | Focus | Intent |
|----|----------|-------|--------|
| US-ONT-PROC-01 | Must Have | **P0** | Procedural ontology stays fresh while LeanKG is in use |
| FR-ONT-PROC-01 | Must Have | **P0** | Watch `ontology/workflows.yaml` during MCP/serve |
| US-SURF-01 / FR-SURF-01 / FR-SURF-02 | Must Have | **P1** | Fix `semantic_search` dual-path docstring |
"#;
        let result = parse_prd_markdown(md);
        assert!(result.errors.is_empty(), "errors: {:?}", result.errors);

        // Should find FR-ONT-PROC-01, FR-SURF-01, FR-SURF-02
        let fr_ids: Vec<_> = result.requirements.iter().map(|r| r.id.clone()).collect();
        assert!(
            fr_ids.contains(&"FR-ONT-PROC-01".to_string()),
            "missing FR-ONT-PROC-01 in {:?}",
            fr_ids
        );
        assert!(
            fr_ids.contains(&"FR-SURF-01".to_string()),
            "missing FR-SURF-01 in {:?}",
            fr_ids
        );
        assert!(
            fr_ids.contains(&"FR-SURF-02".to_string()),
            "missing FR-SURF-02 in {:?}",
            fr_ids
        );

        // Should find US-ONT-PROC-01, US-SURF-01
        let us_ids: Vec<_> = result.user_stories.iter().map(|s| s.id.clone()).collect();
        assert!(
            us_ids.contains(&"US-ONT-PROC-01".to_string()),
            "missing US-ONT-PROC-01 in {:?}",
            us_ids
        );
        assert!(
            us_ids.contains(&"US-SURF-01".to_string()),
            "missing US-SURF-01 in {:?}",
            us_ids
        );
    }

    #[test]
    fn test_extract_code_paths() {
        let md = r#"
| US-UI2-11 / FR-UI2-13 / REL-061 | Must Have | **P1** | Default expand page 500; `ui-v2/src/lib/graph-merge.ts` merge; `src/web/handlers.rs::api_graph_expand_service` pagination |
"#;
        let result = parse_prd_markdown(md);
        let fr = result
            .requirements
            .iter()
            .find(|r| r.id == "FR-UI2-13")
            .unwrap();
        assert!(fr
            .code_paths
            .contains(&"ui-v2/src/lib/graph-merge.ts".to_string()));
        assert!(fr
            .code_paths
            .contains(&"src/web/handlers.rs::api_graph_expand_service".to_string()));
    }

    #[test]
    fn test_requirements_to_knowledge_entries() {
        let reqs = vec![PrdRequirement {
            id: "FR-ONT-PROC-01".to_string(),
            title: "FR-ONT-PROC-01".to_string(),
            description: "Watch ontology YAML during MCP/serve".to_string(),
            priority: "Must Have".to_string(),
            focus: "P0".to_string(),
            user_story_ids: vec!["US-ONT-PROC-01".to_string()],
            related_fr_ids: vec!["FR-ONT-PROC-02".to_string()],
            code_paths: vec!["src/ontology/watcher.rs".to_string()],
        }];

        let entries = requirements_to_knowledge_entries(&reqs, "production");
        assert_eq!(entries.len(), 1);
        assert_eq!(entries[0].id, "prd-req-FR-ONT-PROC-01");
        assert_eq!(entries[0].knowledge_type, "prd_mapping");
        assert_eq!(entries[0].feature_id, Some("FR-ONT-PROC-01".to_string()));
    }
}
