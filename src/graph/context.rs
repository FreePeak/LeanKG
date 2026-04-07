#![allow(dead_code)]
use crate::db::models::CodeElement;
use crate::graph::GraphEngine;
use serde::{Deserialize, Serialize};
use std::collections::HashSet;

#[allow(dead_code)]
const DEFAULT_MAX_TOKENS: usize = 4000;
const CHARS_PER_TOKEN: usize = 4;

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize)]
pub enum ContextPriority {
    Contained = 1,
    Imported = 2,
    RecentlyChanged = 3,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ContextElement {
    pub element: CodeElement,
    pub priority: ContextPriority,
    pub token_count: usize,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ContextResult {
    pub elements: Vec<ContextElement>,
    pub total_tokens: usize,
    pub max_tokens: usize,
    pub truncated: bool,
}

impl ContextResult {
    pub fn to_prompt(&self) -> String {
        let mut prompt = String::new();
        prompt.push_str("# Code Context\n\n");

        for ctx_elem in &self.elements {
            let elem = &ctx_elem.element;
            prompt.push_str(&format!(
                "## {}: {}\nFile: {}:{}:{}\n",
                elem.element_type,
                elem.qualified_name,
                elem.file_path,
                elem.line_start,
                elem.line_end
            ));

            if let Some(parent) = &elem.parent_qualified {
                prompt.push_str(&format!("Parent: {}\n", parent));
            }

            prompt.push('\n');
        }

        if self.truncated {
            prompt.push_str(&format!(
                "\n*Context truncated: {} tokens total (max: {})*\n",
                self.total_tokens, self.max_tokens
            ));
        }

        prompt
    }
}

pub struct ContextProvider<'a> {
    graph: &'a GraphEngine,
    max_tokens: usize,
}

impl<'a> ContextProvider<'a> {
    #[allow(dead_code)]
    pub fn new(graph: &'a GraphEngine) -> Self {
        Self {
            graph,
            max_tokens: DEFAULT_MAX_TOKENS,
        }
    }

    pub fn with_max_tokens(graph: &'a GraphEngine, max_tokens: usize) -> Self {
        Self { graph, max_tokens }
    }

    pub fn estimate_tokens(text: &str) -> usize {
        text.len().div_ceil(CHARS_PER_TOKEN)
    }

    pub fn element_tokens(element: &CodeElement) -> usize {
        let base = format!(
            "{} {} {} {}",
            element.element_type, element.name, element.qualified_name, element.file_path
        );
        let metadata_len = serde_json::to_string(&element.metadata)
            .map(|s| s.len())
            .unwrap_or(0);
        Self::estimate_tokens(&base) + metadata_len / CHARS_PER_TOKEN
    }

    pub fn get_context_for_file(
        &self,
        file_path: &str,
    ) -> Result<ContextResult, Box<dyn std::error::Error>> {
        let mut context_elements = Vec::new();
        let mut seen_qualified: HashSet<String> = HashSet::new();

        let file_elements = self.graph.get_elements_by_file(file_path)?;
        for elem in file_elements {
            if !seen_qualified.insert(elem.qualified_name.clone()) {
                continue;
            }
            let priority = self.determine_priority(&elem);
            let token_count = Self::element_tokens(&elem);
            context_elements.push(ContextElement {
                element: elem,
                priority,
                token_count,
            });
        }

        let relationships = self.graph.get_relationships(file_path)?;
        for rel in relationships {
            if let Some(element) = self.graph.find_element(&rel.target_qualified)? {
                if !seen_qualified.insert(element.qualified_name.clone()) {
                    continue;
                }
                let priority = match rel.rel_type.as_str() {
                    "imports" => ContextPriority::Imported,
                    "contains" | "defines" => ContextPriority::Contained,
                    _ => ContextPriority::Contained,
                };
                let token_count = Self::element_tokens(&element);
                context_elements.push(ContextElement {
                    element,
                    priority,
                    token_count,
                });
            }
        }

        context_elements.sort_by(|a, b| {
            b.priority
                .cmp(&a.priority)
                .then_with(|| b.token_count.cmp(&a.token_count))
        });

        let mut total_tokens = 0;
        let mut selected_elements = Vec::new();
        let mut truncated = false;

        for elem in context_elements {
            if total_tokens + elem.token_count <= self.max_tokens {
                total_tokens += elem.token_count;
                selected_elements.push(elem);
            } else {
                truncated = true;
                break;
            }
        }

        Ok(ContextResult {
            elements: selected_elements,
            total_tokens,
            max_tokens: self.max_tokens,
            truncated,
        })
    }

    fn _get_child_elements(
        &self,
        parent_qualified: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let result = self.graph.get_children(parent_qualified)?;
        Ok(result)
    }

    fn determine_priority(&self, element: &CodeElement) -> ContextPriority {
        if let Some(changed) = element
            .metadata
            .get("recently_changed")
            .and_then(|v| v.as_bool())
        {
            if changed {
                return ContextPriority::RecentlyChanged;
            }
        }

        if element.metadata.get("tested_by").is_some() {
            return ContextPriority::RecentlyChanged;
        }

        ContextPriority::Contained
    }
}

impl GraphEngine {
    pub fn get_context(
        &self,
        file_path: &str,
        max_tokens: usize,
    ) -> Result<ContextResult, Box<dyn std::error::Error>> {
        let provider = ContextProvider::with_max_tokens(self, max_tokens);
        provider.get_context_for_file(file_path)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_estimate_tokens() {
        assert_eq!(ContextProvider::estimate_tokens("hello"), 2);
        assert_eq!(ContextProvider::estimate_tokens("hello world"), 3);
        assert_eq!(ContextProvider::estimate_tokens(""), 0);
        assert_eq!(ContextProvider::estimate_tokens("0123456789"), 3);
    }

    #[test]
    fn test_priority_ordering() {
        assert!(ContextPriority::RecentlyChanged > ContextPriority::Imported);
        assert!(ContextPriority::Imported > ContextPriority::Contained);
        assert!(ContextPriority::RecentlyChanged > ContextPriority::Contained);
    }

    #[test]
    fn test_element_tokens_calculation() {
        let elem = CodeElement {
            qualified_name: "test.rs::foo".to_string(),
            element_type: "function".to_string(),
            name: "foo".to_string(),
            file_path: "test.rs".to_string(),
            line_start: 1,
            line_end: 10,
            language: "rust".to_string(),
            parent_qualified: None,
            metadata: serde_json::json!({}),
            ..Default::default()
        };

        let tokens = ContextProvider::element_tokens(&elem);
        assert!(tokens > 0, "Should calculate some tokens");
    }

    #[test]
    fn test_context_result_to_prompt_empty() {
        let result = ContextResult {
            elements: vec![],
            total_tokens: 0,
            max_tokens: 4000,
            truncated: false,
        };

        let prompt = result.to_prompt();
        assert!(prompt.contains("# Code Context"));
    }

    #[test]
    fn test_context_result_to_prompt_with_elements() {
        let elem = CodeElement {
            qualified_name: "test.rs::main".to_string(),
            element_type: "function".to_string(),
            name: "main".to_string(),
            file_path: "test.rs".to_string(),
            line_start: 1,
            line_end: 5,
            language: "rust".to_string(),
            parent_qualified: None,
            metadata: serde_json::json!({}),
            ..Default::default()
        };

        let ctx_elem = ContextElement {
            element: elem,
            priority: ContextPriority::RecentlyChanged,
            token_count: 10,
        };

        let result = ContextResult {
            elements: vec![ctx_elem],
            total_tokens: 10,
            max_tokens: 4000,
            truncated: false,
        };

        let prompt = result.to_prompt();
        assert!(prompt.contains("function: test.rs::main"));
        assert!(prompt.contains("File: test.rs:1:5"));
        assert!(!prompt.contains("Context truncated"));
    }

    #[test]
    fn test_context_result_to_prompt_truncated() {
        let elem = CodeElement {
            qualified_name: "test.rs::foo".to_string(),
            element_type: "function".to_string(),
            name: "foo".to_string(),
            file_path: "test.rs".to_string(),
            line_start: 1,
            line_end: 100,
            language: "rust".to_string(),
            parent_qualified: None,
            metadata: serde_json::json!({}),
            ..Default::default()
        };

        let ctx_elem = ContextElement {
            element: elem,
            priority: ContextPriority::Imported,
            token_count: 100,
        };

        let result = ContextResult {
            elements: vec![ctx_elem],
            total_tokens: 100,
            max_tokens: 50,
            truncated: true,
        };

        let prompt = result.to_prompt();
        assert!(prompt.contains("Context truncated"));
        assert!(prompt.contains("100 tokens total"));
        assert!(prompt.contains("max: 50"));
    }
}
