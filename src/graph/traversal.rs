use crate::db::models::CodeElement;
use crate::graph::GraphEngine;
use std::collections::{HashSet, VecDeque};

pub struct ImpactAnalyzer<'a> {
    graph: &'a GraphEngine,
}

impl<'a> ImpactAnalyzer<'a> {
    pub fn new(graph: &'a GraphEngine) -> Self {
        Self { graph }
    }

    pub async fn calculate_impact_radius(
        &self,
        start_file: &str,
        depth: u32,
    ) -> Result<ImpactResult, Box<dyn std::error::Error>> {
        let mut visited = HashSet::new();
        let mut queue = VecDeque::new();
        let mut affected_elements = Vec::new();

        queue.push_back((start_file.to_string(), 0));
        visited.insert(start_file.to_string());

        while let Some((current, current_depth)) = queue.pop_front() {
            if current_depth >= depth {
                continue;
            }

            let relationships = self.graph.get_relationships(&current).await?;

            for rel in relationships {
                if !visited.contains(&rel.target_qualified) {
                    visited.insert(rel.target_qualified.clone());
                    queue.push_back((rel.target_qualified.clone(), current_depth + 1));

                    if let Some(element) = self.graph.find_element(&rel.target_qualified).await? {
                        affected_elements.push(element);
                    }
                }
            }

            let dependents = self.graph.get_dependents(&current).await?;
            for rel in dependents {
                if !visited.contains(&rel.source_qualified) {
                    visited.insert(rel.source_qualified.clone());
                    queue.push_back((rel.source_qualified.clone(), current_depth + 1));

                    if let Some(element) = self.graph.find_element(&rel.source_qualified).await? {
                        affected_elements.push(element);
                    }
                }
            }
        }

        Ok(ImpactResult {
            start_file: start_file.to_string(),
            max_depth: depth,
            affected_elements,
        })
    }
}

#[derive(Debug)]
pub struct ImpactResult {
    pub start_file: String,
    pub max_depth: u32,
    pub affected_elements: Vec<CodeElement>,
}
