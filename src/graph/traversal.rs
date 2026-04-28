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

    pub fn calculate_impact_radius(
        &self,
        start_file: &str,
        depth: u32,
    ) -> Result<ImpactResult, Box<dyn std::error::Error>> {
        self.calculate_impact_radius_with_confidence(start_file, depth, 0.0)
    }

    pub fn calculate_impact_radius_with_confidence(
        &self,
        start_file: &str,
        depth: u32,
        min_confidence: f64,
    ) -> Result<ImpactResult, Box<dyn std::error::Error>> {
        let mut visited = HashSet::new();
        let mut queue = VecDeque::new();
        let mut affected_with_confidence: Vec<AffectedElementWithConfidence> = Vec::new();
        let mut seen_qualified: HashSet<String> = HashSet::new();

        queue.push_back((start_file.to_string(), 0));
        visited.insert(start_file.to_string());

        while let Some((current, current_depth)) = queue.pop_front() {
            if current_depth >= depth {
                continue;
            }

            let relationships = self.graph.get_relationships(&current)?;

            for rel in relationships {
                if rel.confidence < min_confidence {
                    continue;
                }
                let target = &rel.target_qualified;
                if !visited.contains(target) {
                    visited.insert(target.clone());
                    queue.push_back((target.clone(), current_depth + 1));
                }
                if seen_qualified.insert(target.clone()) {
                    if let Ok(Some(element)) = self.graph.find_element(target) {
                        let severity = rel.severity(current_depth + 1).to_string();
                        affected_with_confidence.push(AffectedElementWithConfidence {
                            element,
                            confidence: rel.confidence,
                            severity,
                            depth: current_depth + 1,
                        });
                    }
                }
            }

            let dependents = self.graph.get_dependents(&current)?;
            for rel in dependents {
                if rel.confidence < min_confidence {
                    continue;
                }
                let source = &rel.source_qualified;
                if !visited.contains(source) {
                    visited.insert(source.clone());
                    queue.push_back((source.clone(), current_depth + 1));
                }
                if seen_qualified.insert(source.clone()) {
                    if let Ok(Some(element)) = self.graph.find_element(source) {
                        let severity = rel.severity(current_depth + 1).to_string();
                        affected_with_confidence.push(AffectedElementWithConfidence {
                            element,
                            confidence: rel.confidence,
                            severity,
                            depth: current_depth + 1,
                        });
                    }
                }
            }
        }

        let affected_elements: Vec<CodeElement> = affected_with_confidence
            .iter()
            .map(|a| a.element.clone())
            .collect();

        Ok(ImpactResult {
            start_file: start_file.to_string(),
            max_depth: depth,
            affected_elements,
            affected_with_confidence,
        })
    }
}

#[derive(Debug, Clone)]
pub struct AffectedElementWithConfidence {
    pub element: CodeElement,
    pub confidence: f64,
    pub severity: String,
    pub depth: u32,
}

#[derive(Debug)]
pub struct ImpactResult {
    pub start_file: String,
    pub max_depth: u32,
    pub affected_elements: Vec<CodeElement>,
    pub affected_with_confidence: Vec<AffectedElementWithConfidence>,
}
