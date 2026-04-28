use crate::db::models::Relationship;
use crate::graph::query::GraphEngine;
use regex::Regex;
use std::error::Error;
use std::sync::Arc;

#[derive(Debug, Clone)]
pub struct Evidence {
    pub element: String,
    pub relationship: Option<String>,
    pub snippet: String,
}

#[derive(Debug, Clone)]
pub struct NlQueryResult {
    pub answer: String,
    pub evidence: Vec<Evidence>,
    pub query_type: String,
    pub confidence: f64,
}

#[derive(Debug, Clone, PartialEq)]
pub enum QueryIntent {
    Dependencies,
    Dependents,
    Calls,
    Impact,
    Coverage,
    Search,
    Unknown,
}

impl QueryIntent {
    fn as_str(&self) -> &'static str {
        match self {
            QueryIntent::Dependencies => "dependency",
            QueryIntent::Dependents => "dependent",
            QueryIntent::Calls => "calls",
            QueryIntent::Impact => "impact",
            QueryIntent::Coverage => "coverage",
            QueryIntent::Search => "search",
            QueryIntent::Unknown => "unknown",
        }
    }
}

pub struct NlQueryEngine {
    graph: Arc<GraphEngine>,
    patterns: Vec<(QueryIntent, Regex)>,
}

impl NlQueryEngine {
    pub fn new(graph: Arc<GraphEngine>) -> Self {
        let patterns = vec![
            (
                QueryIntent::Dependencies,
                Regex::new(r"(?i)\b(what\s+(files\s+)?(imports?|dependences?)\b|(files?\s+)?imports?\b)").unwrap(),
            ),
            (
                QueryIntent::Dependents,
                Regex::new(r"(?i)\b(what\s+(files?\s+)?(dependents?|imported\s+by|used\s+by)\b|(files?\s+)?imported\s+by\b)").unwrap(),
            ),
            (
                QueryIntent::Calls,
                Regex::new(r"(?i)\b(what\s+(does|calls?|invokes?)\b|(\w+\s+)?calls?\b)").unwrap(),
            ),
            (
                QueryIntent::Impact,
                Regex::new(r"(?i)\b(what\s+is\s+affected|impact\s+radius|blast\s+radius|affects?|breaking|change\s+impact)\b").unwrap(),
            ),
            (
                QueryIntent::Coverage,
                Regex::new(r"(?i)\b(test\s+(coverage|covered\s+by)|covered\s+by\s+tests?|tests?\b)").unwrap(),
            ),
            (
                QueryIntent::Search,
                Regex::new(r"(?i)\b(find|search|look\s+for|related|similar)\b").unwrap(),
            ),
        ];
        Self { graph, patterns }
    }

    fn detect_intent(&self, question: &str) -> QueryIntent {
        for (intent, pattern) in &self.patterns {
            if pattern.is_match(question) {
                return intent.clone();
            }
        }
        QueryIntent::Unknown
    }

    fn extract_entity(&self, question: &str) -> Option<String> {
        let entity_patterns = [
            r"imports?\s+(.+?)(?:\?|$|\.)",
            r"dependents?\s+(?:of\s+)?(.+?)(?:\?|$|\.)",
            r"calls?\s+(.+?)(?:\?|$|\.)",
            r"affected\s+(?:if\s+)?(?:I\s+)?change\s+(.+?)(?:\?|$|\.)",
            r"change\s+(.+?)(?:\?|$|\.)",
            r"coverage\s+(?:for\s+)?(.+?)(?:\?|$|\.)",
            r"test(?:s|ing)?\s+(?:for\s+)?(.+?)(?:\?|$|\.)",
            r"for\s+(.+?)(?:\?|$|\.)",
        ];

        for pattern_str in &entity_patterns {
            if let Ok(re) = Regex::new(pattern_str) {
                if let Some(caps) = re.captures(question) {
                    if let Some(entity) = caps.get(1) {
                        let extracted = entity.as_str().trim();
                        if !extracted.is_empty() {
                            return Some(extracted.to_string());
                        }
                    }
                }
            }
        }

        for word in question.split_whitespace() {
            if word.starts_with("src/")
                || word.starts_with("./")
                || word.ends_with(".rs")
                || word.ends_with(".go")
                || word.ends_with(".ts")
                || word.ends_with(".js")
            {
                return Some(word.to_string());
            }
        }
        question.split_whitespace().last().map(|s| s.to_string())
    }

    pub fn query(&self, nl_question: &str) -> Result<NlQueryResult, Box<dyn Error>> {
        let intent = self.detect_intent(nl_question);
        let entity = self.extract_entity(nl_question);

        match intent {
            QueryIntent::Dependencies => self.handle_dependencies(entity.as_deref()),
            QueryIntent::Dependents => self.handle_dependents(entity.as_deref()),
            QueryIntent::Calls => self.handle_calls(entity.as_deref()),
            QueryIntent::Impact => self.handle_impact(entity.as_deref()),
            QueryIntent::Coverage => self.handle_coverage(entity.as_deref()),
            QueryIntent::Search => self.handle_search(entity.as_deref()),
            QueryIntent::Unknown => self.handle_unknown(nl_question),
        }
    }

    fn handle_dependencies(&self, target: Option<&str>) -> Result<NlQueryResult, Box<dyn Error>> {
        match target {
            Some(t) => {
                let elements = self.graph.get_dependencies(t)?;
                let evidence: Vec<Evidence> = elements
                    .iter()
                    .map(|e| Evidence {
                        element: e.qualified_name.clone(),
                        relationship: Some("imports".to_string()),
                        snippet: format!("{}:{} ", e.file_path, e.name),
                    })
                    .collect();

                let answer = if evidence.is_empty() {
                    format!("No dependencies found for '{}'", t)
                } else {
                    let names: Vec<&str> = evidence.iter().map(|e| e.element.as_str()).collect();
                    format!(
                        "Found {} dependencies for '{}': {}",
                        evidence.len(),
                        t,
                        names.join(", ")
                    )
                };

                Ok(NlQueryResult {
                    answer,
                    evidence,
                    query_type: QueryIntent::Dependencies.as_str().to_string(),
                    confidence: 0.9,
                })
            }
            None => Ok(NlQueryResult {
                answer: "Please specify what to find dependencies for.".to_string(),
                evidence: vec![],
                query_type: QueryIntent::Dependencies.as_str().to_string(),
                confidence: 0.0,
            }),
        }
    }

    fn handle_dependents(&self, target: Option<&str>) -> Result<NlQueryResult, Box<dyn Error>> {
        match target {
            Some(t) => {
                let relationships = self.graph.get_dependents(t)?;
                let evidence: Vec<Evidence> = relationships
                    .iter()
                    .map(|r| Evidence {
                        element: r.source_qualified.clone(),
                        relationship: Some(r.rel_type.clone()),
                        snippet: format!("{} -> {}", r.source_qualified, r.target_qualified),
                    })
                    .collect();

                let answer = if evidence.is_empty() {
                    format!("No dependents found for '{}'", t)
                } else {
                    let names: Vec<&str> = evidence.iter().map(|e| e.element.as_str()).collect();
                    format!(
                        "Found {} dependents for '{}': {}",
                        evidence.len(),
                        t,
                        names.join(", ")
                    )
                };

                Ok(NlQueryResult {
                    answer,
                    evidence,
                    query_type: QueryIntent::Dependents.as_str().to_string(),
                    confidence: 0.9,
                })
            }
            None => Ok(NlQueryResult {
                answer: "Please specify what to find dependents for.".to_string(),
                evidence: vec![],
                query_type: QueryIntent::Dependents.as_str().to_string(),
                confidence: 0.0,
            }),
        }
    }

    fn handle_calls(&self, target: Option<&str>) -> Result<NlQueryResult, Box<dyn Error>> {
        match target {
            Some(t) => {
                let relationships = self.graph.get_relationships(t)?;
                let call_rels: Vec<&Relationship> = relationships
                    .iter()
                    .filter(|r| r.rel_type == "calls")
                    .collect();

                let evidence: Vec<Evidence> = call_rels
                    .iter()
                    .map(|r| Evidence {
                        element: r.target_qualified.clone(),
                        relationship: Some("calls".to_string()),
                        snippet: format!("{} calls {}", r.source_qualified, r.target_qualified),
                    })
                    .collect();

                let answer = if evidence.is_empty() {
                    format!("No function calls found for '{}'", t)
                } else {
                    let names: Vec<&str> = evidence.iter().map(|e| e.element.as_str()).collect();
                    format!(
                        "Found {} calls from '{}': {}",
                        evidence.len(),
                        t,
                        names.join(", ")
                    )
                };

                Ok(NlQueryResult {
                    answer,
                    evidence,
                    query_type: QueryIntent::Calls.as_str().to_string(),
                    confidence: 0.9,
                })
            }
            None => Ok(NlQueryResult {
                answer: "Please specify what function to find calls for.".to_string(),
                evidence: vec![],
                query_type: QueryIntent::Calls.as_str().to_string(),
                confidence: 0.0,
            }),
        }
    }

    fn handle_impact(&self, target: Option<&str>) -> Result<NlQueryResult, Box<dyn Error>> {
        match target {
            Some(t) => {
                let relationships = self.graph.get_dependents(t)?;
                let elements = self.graph.find_element(t)?;

                let mut evidence: Vec<Evidence> = relationships
                    .iter()
                    .map(|r| {
                        let severity = r.severity(1);
                        Evidence {
                            element: r.source_qualified.clone(),
                            relationship: Some(format!("{} ({})", r.rel_type, severity)),
                            snippet: format!(
                                "{} -> {} [{}]",
                                r.source_qualified, r.target_qualified, severity
                            ),
                        }
                    })
                    .collect();

                if let Some(elem) = elements {
                    evidence.insert(
                        0,
                        Evidence {
                            element: elem.qualified_name.clone(),
                            relationship: None,
                            snippet: format!(
                                "{} (lines {}-{})",
                                elem.name, elem.line_start, elem.line_end
                            ),
                        },
                    );
                }

                let answer = if evidence.is_empty() {
                    format!("No impact analysis available for '{}'", t)
                } else {
                    let names: Vec<&str> = evidence.iter().map(|e| e.element.as_str()).collect();
                    format!(
                        "Changing '{}' may affect {} elements: {}",
                        t,
                        evidence.len(),
                        names.join(", ")
                    )
                };

                Ok(NlQueryResult {
                    answer,
                    evidence,
                    query_type: QueryIntent::Impact.as_str().to_string(),
                    confidence: 0.85,
                })
            }
            None => Ok(NlQueryResult {
                answer: "Please specify what to analyze impact for.".to_string(),
                evidence: vec![],
                query_type: QueryIntent::Impact.as_str().to_string(),
                confidence: 0.0,
            }),
        }
    }

    fn handle_coverage(&self, target: Option<&str>) -> Result<NlQueryResult, Box<dyn Error>> {
        match target {
            Some(t) => {
                let relationships = self.graph.get_relationships(t)?;
                let test_rels: Vec<&Relationship> = relationships
                    .iter()
                    .filter(|r| r.rel_type == "tested_by" || r.rel_type == "tests")
                    .collect();

                let evidence: Vec<Evidence> = test_rels
                    .iter()
                    .map(|r| Evidence {
                        element: r.target_qualified.clone(),
                        relationship: Some(r.rel_type.clone()),
                        snippet: format!(
                            "{} {} {}",
                            r.source_qualified, r.rel_type, r.target_qualified
                        ),
                    })
                    .collect();

                let answer = if evidence.is_empty() {
                    format!("No test coverage found for '{}'", t)
                } else {
                    let names: Vec<&str> = evidence.iter().map(|e| e.element.as_str()).collect();
                    format!(
                        "'{}' is tested by {} tests: {}",
                        t,
                        evidence.len(),
                        names.join(", ")
                    )
                };

                Ok(NlQueryResult {
                    answer,
                    evidence,
                    query_type: QueryIntent::Coverage.as_str().to_string(),
                    confidence: 0.9,
                })
            }
            None => Ok(NlQueryResult {
                answer: "Please specify what to find test coverage for.".to_string(),
                evidence: vec![],
                query_type: QueryIntent::Coverage.as_str().to_string(),
                confidence: 0.0,
            }),
        }
    }

    fn handle_search(&self, query: Option<&str>) -> Result<NlQueryResult, Box<dyn Error>> {
        match query {
            Some(q) => {
                let elements = self.graph.search_by_name(q)?;
                let evidence: Vec<Evidence> = elements
                    .iter()
                    .take(10)
                    .map(|e| Evidence {
                        element: e.qualified_name.clone(),
                        relationship: Some(e.element_type.clone()),
                        snippet: format!("{}:{} ({})", e.file_path, e.name, e.element_type),
                    })
                    .collect();

                let answer = if evidence.is_empty() {
                    format!("No elements found matching '{}'", q)
                } else {
                    let names: Vec<&str> = evidence.iter().map(|e| e.element.as_str()).collect();
                    format!(
                        "Found {} elements matching '{}': {}",
                        evidence.len(),
                        q,
                        names.join(", ")
                    )
                };

                Ok(NlQueryResult {
                    answer,
                    evidence,
                    query_type: QueryIntent::Search.as_str().to_string(),
                    confidence: 0.85,
                })
            }
            None => Ok(NlQueryResult {
                answer: "Please specify what to search for.".to_string(),
                evidence: vec![],
                query_type: QueryIntent::Search.as_str().to_string(),
                confidence: 0.0,
            }),
        }
    }

    fn handle_unknown(&self, question: &str) -> Result<NlQueryResult, Box<dyn Error>> {
        let entity = self.extract_entity(question);
        if let Some(query) = entity {
            self.handle_search(Some(&query))
        } else {
            Ok(NlQueryResult {
                answer: "I couldn't understand that question. Try asking like:\n\
                    - 'What imports src/main.rs?'\n\
                    - 'What does function X call?'\n\
                    - 'What is affected if I change Y?'\n\
                    - 'Show me test coverage for Z'\n\
                    - 'Find functions related to X'"
                    .to_string(),
                evidence: vec![],
                query_type: QueryIntent::Unknown.as_str().to_string(),
                confidence: 0.0,
            })
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    fn create_mock_engine() -> NlQueryEngine {
        let temp_dir = TempDir::new().expect("Failed to create temp dir");
        let db = crate::db::schema::init_db(temp_dir.path()).expect("Failed to create database");
        let graph = Arc::new(GraphEngine::new(db));
        NlQueryEngine::new(graph)
    }

    #[test]
    fn test_detect_intent_dependencies() {
        let engine = create_mock_engine();
        assert_eq!(
            engine.detect_intent("What imports src/main.rs?"),
            QueryIntent::Dependencies
        );
        assert_eq!(
            engine.detect_intent("files importing parser.rs"),
            QueryIntent::Dependencies
        );
        assert_eq!(
            engine.detect_intent("What dependencies does lib.rs have?"),
            QueryIntent::Dependencies
        );
    }

    #[test]
    fn test_detect_intent_dependents() {
        let engine = create_mock_engine();
        assert_eq!(
            engine.detect_intent("What is imported by utils.rs?"),
            QueryIntent::Dependents
        );
        assert_eq!(
            engine.detect_intent("Who imports this file?"),
            QueryIntent::Dependents
        );
        assert_eq!(
            engine.detect_intent("files depending on main.rs"),
            QueryIntent::Dependents
        );
    }

    #[test]
    fn test_detect_intent_calls() {
        let engine = create_mock_engine();
        assert_eq!(
            engine.detect_intent("What does handle_request call?"),
            QueryIntent::Calls
        );
        assert_eq!(
            engine.detect_intent("calls to process_data"),
            QueryIntent::Calls
        );
    }

    #[test]
    fn test_detect_intent_impact() {
        let engine = create_mock_engine();
        assert_eq!(
            engine.detect_intent("What is affected if I change main.rs?"),
            QueryIntent::Impact
        );
        assert_eq!(
            engine.detect_intent("impact radius of api.rs"),
            QueryIntent::Impact
        );
        assert_eq!(
            engine.detect_intent("what breaks if I modify config?"),
            QueryIntent::Impact
        );
    }

    #[test]
    fn test_detect_intent_coverage() {
        let engine = create_mock_engine();
        assert_eq!(
            engine.detect_intent("Show me test coverage for handler.rs"),
            QueryIntent::Coverage
        );
        assert_eq!(
            engine.detect_intent("what tests cover this function?"),
            QueryIntent::Coverage
        );
    }

    #[test]
    fn test_detect_intent_search() {
        let engine = create_mock_engine();
        assert_eq!(
            engine.detect_intent("Find functions related to parsing"),
            QueryIntent::Search
        );
        assert_eq!(
            engine.detect_intent("search for validator"),
            QueryIntent::Search
        );
    }

    #[test]
    fn test_extract_entity_file_path() {
        let engine = create_mock_engine();
        assert_eq!(
            engine.extract_entity("What imports src/main.rs?"),
            Some("src/main.rs".to_string())
        );
        assert_eq!(
            engine.extract_entity("files importing ./lib.rs"),
            Some("./lib.rs".to_string())
        );
    }

    #[test]
    fn test_extract_entity_function_name() {
        let engine = create_mock_engine();
        assert_eq!(
            engine.extract_entity("What does main call?"),
            Some("main".to_string())
        );
        assert_eq!(
            engine.extract_entity("calls to process_data"),
            Some("process_data".to_string())
        );
    }

    #[test]
    fn test_query_unknown_asks_for_clarification() {
        let engine = create_mock_engine();
        let result = engine.query("blah blah").unwrap();
        assert_eq!(result.query_type, "unknown");
        assert!(result.confidence < 0.1);
    }

    #[test]
    fn test_query_returns_structured_result() {
        let engine = create_mock_engine();
        let result = engine.query("What imports nonexistent.rs?").unwrap();
        assert_eq!(result.query_type, "dependency");
        assert!(result.answer.contains("No dependencies"));
    }

    #[test]
    fn test_nl_query_result_has_required_fields() {
        let result = NlQueryResult {
            answer: "Test answer".to_string(),
            evidence: vec![Evidence {
                element: "test::func".to_string(),
                relationship: Some("calls".to_string()),
                snippet: "test::func()".to_string(),
            }],
            query_type: "test".to_string(),
            confidence: 0.95,
        };
        assert_eq!(result.answer, "Test answer");
        assert_eq!(result.evidence.len(), 1);
        assert_eq!(result.confidence, 0.95);
    }

    #[test]
    fn test_evidence_has_required_fields() {
        let evidence = Evidence {
            element: "module::function".to_string(),
            relationship: Some("imports".to_string()),
            snippet: "use module::function".to_string(),
        };
        assert_eq!(evidence.element, "module::function");
        assert_eq!(evidence.relationship, Some("imports".to_string()));
    }

    #[test]
    fn test_intent_confidence_when_entity_missing() {
        let engine = create_mock_engine();
        let result = engine.query("What imports?").unwrap();
        assert_eq!(result.confidence, 0.0);
        assert!(result.answer.contains("Please specify"));
    }

    #[test]
    fn test_query_pattern_dependency() {
        let engine = create_mock_engine();
        let result = engine.query("What files import src/main.rs?").unwrap();
        assert_eq!(result.query_type, "dependency");
    }

    #[test]
    fn test_query_pattern_unknown_falls_back_to_search() {
        let engine = create_mock_engine();
        let result = engine.query("something unclear").unwrap();
        assert_eq!(result.query_type, "search");
    }

    #[test]
    fn test_entity_extraction_with_period() {
        let engine = create_mock_engine();
        let entity = engine.extract_entity("What imports src/main.rs.");
        assert_eq!(entity, Some("src/main.rs".to_string()));
    }
}
