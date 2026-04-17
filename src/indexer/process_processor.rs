use crate::db::models::{CodeElement, Relationship};
use std::collections::HashMap;

const MIN_TRACE_CONFIDENCE: f64 = 0.5;
const MAX_TRACE_DEPTH: usize = 10;
const MAX_BRANCHING: usize = 4;
const MAX_PROCESSES: usize = 75;
const MIN_STEPS: usize = 3;

#[derive(Debug, Clone)]
pub struct ProcessConfig {
    pub max_trace_depth: usize,
    pub max_branching: usize,
    pub max_processes: usize,
    pub min_steps: usize,
}

impl Default for ProcessConfig {
    fn default() -> Self {
        Self {
            max_trace_depth: MAX_TRACE_DEPTH,
            max_branching: MAX_BRANCHING,
            max_processes: MAX_PROCESSES,
            min_steps: MIN_STEPS,
        }
    }
}

pub struct ProcessDetectionResult {
    pub process_elements: Vec<CodeElement>,
    pub process_relationships: Vec<Relationship>,
}

fn build_calls_graphs(
    relationships: &[Relationship],
) -> (HashMap<String, Vec<String>>, HashMap<String, Vec<String>>) {
    let mut calls = HashMap::new();
    let mut reverse_calls = HashMap::new();

    for rel in relationships {
        if rel.rel_type == "calls" && rel.confidence >= MIN_TRACE_CONFIDENCE {
            calls
                .entry(rel.source_qualified.clone())
                .or_insert_with(Vec::new)
                .push(rel.target_qualified.clone());
            reverse_calls
                .entry(rel.target_qualified.clone())
                .or_insert_with(Vec::new)
                .push(rel.source_qualified.clone());
        }
    }

    (calls, reverse_calls)
}

fn is_test_file(file_path: &str) -> bool {
    // Basic test file heuristic mimicking `isTestFile` from gitnexus
    file_path.contains("/test/")
        || file_path.contains("/tests/")
        || file_path.contains("_test.")
        || file_path.contains(".test.")
        || file_path.contains(".spec.")
}

fn calculate_entry_point_score(name: &str, callers_count: usize, callees_count: usize) -> f64 {
    // Simplified heuristic: prefers functions with few callers and many callees
    let base_score = (callees_count as f64).ln_1p() * 10.0;
    let penalty = ((callers_count as f64) * 2.0).exp2().min(100.0);
    let mut score = base_score / penalty;

    // Boost common entry point names
    let lower_name = name.to_lowercase();
    if lower_name.starts_with("handle")
        || lower_name.starts_with("on")
        || lower_name.ends_with("controller")
        || lower_name == "main"
    {
        score *= 1.5;
    }

    score
}

fn find_entry_points(
    elements: &[CodeElement],
    calls: &HashMap<String, Vec<String>>,
    reverse_calls: &HashMap<String, Vec<String>>,
) -> Vec<String> {
    let mut candidates = Vec::new();

    for el in elements {
        if el.element_type != "function" && el.element_type != "method" {
            continue;
        }

        if is_test_file(&el.file_path) {
            continue;
        }

        let callees = calls.get(&el.qualified_name).map(|v| v.len()).unwrap_or(0);
        if callees == 0 {
            continue; // Must have at least 1 outgoing call
        }

        let callers = reverse_calls
            .get(&el.qualified_name)
            .map(|v| v.len())
            .unwrap_or(0);

        let score = calculate_entry_point_score(&el.name, callers, callees);
        if score > 0.0 {
            candidates.push((el.qualified_name.clone(), score));
        }
    }

    // Sort descending by score
    candidates.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));

    // Limit to prevent explosion
    candidates.into_iter().take(200).map(|(id, _)| id).collect()
}

fn trace_from_entry_point(
    entry_id: &str,
    calls: &HashMap<String, Vec<String>>,
    config: &ProcessConfig,
) -> Vec<Vec<String>> {
    let mut traces = Vec::new();
    let mut queue = Vec::new();
    queue.push((entry_id.to_string(), vec![entry_id.to_string()]));

    while !queue.is_empty() && traces.len() < config.max_branching * 3 {
        let (current_id, path) = queue.remove(0);

        let callees = calls.get(&current_id);
        let callees_len = callees.map(|v| v.len()).unwrap_or(0);

        if callees_len == 0 || path.len() >= config.max_trace_depth {
            if path.len() >= config.min_steps {
                traces.push(path.clone());
            }
        } else {
            let limited_callees: Vec<String> = callees
                .unwrap()
                .iter()
                .take(config.max_branching)
                .cloned()
                .collect();

            let mut added_branch = false;

            for callee_id in limited_callees {
                if !path.contains(&callee_id) {
                    let mut new_path = path.clone();
                    new_path.push(callee_id.clone());
                    queue.push((callee_id, new_path));
                    added_branch = true;
                }
            }

            if !added_branch && path.len() >= config.min_steps {
                traces.push(path);
            }
        }
    }

    traces
}

fn deduplicate_traces(traces: Vec<Vec<String>>) -> Vec<Vec<String>> {
    if traces.is_empty() {
        return Vec::new();
    }

    let mut sorted = traces;
    sorted.sort_by_key(|b| std::cmp::Reverse(b.len()));
    let mut unique: Vec<Vec<String>> = Vec::new();

    for trace in sorted {
        let trace_key = trace.join("->");
        let is_subset = unique.iter().any(|existing| {
            let existing_key = existing.join("->");
            existing_key.contains(&trace_key)
        });

        if !is_subset {
            unique.push(trace);
        }
    }

    unique
}

fn deduplicate_by_endpoints(traces: Vec<Vec<String>>) -> Vec<Vec<String>> {
    if traces.is_empty() {
        return Vec::new();
    }

    let mut by_endpoints = HashMap::new();
    let mut sorted = traces;
    sorted.sort_by_key(|b| std::cmp::Reverse(b.len()));

    for trace in sorted {
        let first = trace.first().unwrap();
        let last = trace.last().unwrap();
        let key = format!("{}::{}", first, last);
        by_endpoints.entry(key).or_insert(trace);
    }

    by_endpoints.into_values().collect()
}

pub fn detect_processes(
    elements: &[CodeElement],
    relationships: &[Relationship],
    config: Option<ProcessConfig>,
) -> ProcessDetectionResult {
    let cfg = config.unwrap_or_default();
    let (calls, reverse_calls) = build_calls_graphs(relationships);

    let element_map: HashMap<String, &CodeElement> = elements
        .iter()
        .map(|e| (e.qualified_name.clone(), e))
        .collect();

    let entry_points = find_entry_points(elements, &calls, &reverse_calls);

    let mut all_traces = Vec::new();
    for entry_id in entry_points.iter() {
        if all_traces.len() >= cfg.max_processes * 2 {
            break;
        }
        let traces = trace_from_entry_point(entry_id, &calls, &cfg);
        for t in traces {
            if t.len() >= cfg.min_steps {
                all_traces.push(t);
            }
        }
    }

    let unique_traces = deduplicate_traces(all_traces);
    let mut endpoint_deduped = deduplicate_by_endpoints(unique_traces);

    endpoint_deduped.sort_by_key(|b| std::cmp::Reverse(b.len()));
    let limited_traces: Vec<Vec<String>> = endpoint_deduped
        .into_iter()
        .take(cfg.max_processes)
        .collect();

    let mut process_elements = Vec::new();
    let mut process_relationships = Vec::new();

    for (idx, trace) in limited_traces.iter().enumerate() {
        let entry_point_id = trace.first().unwrap();
        let terminal_id = trace.last().unwrap();

        let entry_node = element_map.get(entry_point_id);
        let terminal_node = element_map.get(terminal_id);

        let entry_name = entry_node.map(|n| n.name.as_str()).unwrap_or("Unknown");
        let terminal_name = terminal_node.map(|n| n.name.as_str()).unwrap_or("Unknown");

        let heuristic_label = format!(
            "{} \u{2192} {}",
            capitalize(entry_name),
            capitalize(terminal_name)
        );

        let process_id = format!("proc_{}_{}", idx, sanitize_id(entry_name));

        process_elements.push(CodeElement {
            qualified_name: process_id.clone(),
            element_type: "process".to_string(),
            name: heuristic_label.clone(),
            file_path: entry_node.map(|n| n.file_path.clone()).unwrap_or_default(),
            line_start: 0,
            line_end: 0,
            language: "domain".to_string(),
            parent_qualified: None,
            cluster_id: None,
            cluster_label: None,
            metadata: serde_json::json!({
                "stepCount": trace.len(),
                "entryPointId": entry_point_id,
                "terminalId": terminal_id,
                "heuristicLabel": heuristic_label,
            }),
        });

        // Add relationships
        for (step_idx, node_id) in trace.iter().enumerate() {
            process_relationships.push(Relationship {
                id: None,
                source_qualified: node_id.clone(),
                target_qualified: process_id.clone(),
                rel_type: "step_in_process".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({
                    "step": step_idx + 1,
                }),
            });
        }

        // Add entry_point_of for the first element
        process_relationships.push(Relationship {
            id: None,
            source_qualified: entry_point_id.clone(),
            target_qualified: process_id.clone(),
            rel_type: "entry_point_of".to_string(),
            confidence: 1.0,
            metadata: serde_json::json!({}),
        });
    }

    ProcessDetectionResult {
        process_elements,
        process_relationships,
    }
}

fn capitalize(s: &str) -> String {
    let mut c = s.chars();
    match c.next() {
        None => String::new(),
        Some(f) => f.to_uppercase().collect::<String>() + c.as_str(),
    }
}

fn sanitize_id(s: &str) -> String {
    let sanitized: String = s
        .chars()
        .map(|c| if c.is_ascii_alphanumeric() { c } else { '_' })
        .collect();
    sanitized.to_lowercase().chars().take(20).collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn create_rel(source: &str, target: &str) -> Relationship {
        Relationship {
            id: None,
            source_qualified: source.to_string(),
            target_qualified: target.to_string(),
            rel_type: "calls".to_string(),
            confidence: 1.0,
            metadata: serde_json::json!({}),
        }
    }

    fn create_func(name: &str, is_test: bool) -> CodeElement {
        CodeElement {
            qualified_name: name.to_string(),
            element_type: "function".to_string(),
            name: name.to_string(),
            file_path: if is_test {
                "test_file.rs".to_string()
            } else {
                "main.rs".to_string()
            },
            line_start: 0,
            line_end: 0,
            language: "rust".to_string(),
            parent_qualified: None,
            cluster_id: None,
            cluster_label: None,
            metadata: serde_json::json!({}),
        }
    }

    #[test]
    fn test_find_entry_points() {
        let elements = vec![
            create_func("main", false),
            create_func("handle_request", false),
            create_func("util_func", false),
            create_func("test_something", true), // Should be ignored
        ];

        let mut calls = HashMap::new();
        let mut reverse_calls = HashMap::new();

        calls.insert("main".to_string(), vec!["handle_request".to_string()]);
        reverse_calls.insert("handle_request".to_string(), vec!["main".to_string()]);

        calls.insert("handle_request".to_string(), vec!["util_func".to_string()]);
        reverse_calls.insert("util_func".to_string(), vec!["handle_request".to_string()]);

        let entry_points = find_entry_points(&elements, &calls, &reverse_calls);

        assert_eq!(entry_points.len(), 2);
        assert_eq!(entry_points[0], "main"); // "main" is boosted and has 0 callers
    }

    #[test]
    fn test_trace_from_entry_point() {
        let mut calls: HashMap<String, Vec<String>> = HashMap::new();
        calls.insert("a".to_string(), vec!["b".to_string(), "c".to_string()]);
        calls.insert("b".to_string(), vec!["d".to_string()]);
        calls.insert("c".to_string(), vec!["d".to_string()]);

        let cfg = ProcessConfig::default();
        let traces = trace_from_entry_point("a", &calls, &cfg);

        assert_eq!(traces.len(), 2);
        assert!(traces.contains(&vec!["a".to_string(), "b".to_string(), "d".to_string()]));
        assert!(traces.contains(&vec!["a".to_string(), "c".to_string(), "d".to_string()]));
    }

    #[test]
    fn test_deduplicate_traces() {
        let traces = vec![
            vec!["a".to_string(), "b".to_string()],
            vec!["a".to_string(), "b".to_string(), "c".to_string()],
        ];
        let unique = deduplicate_traces(traces);
        assert_eq!(unique.len(), 1);
        assert_eq!(unique[0].len(), 3);
    }

    #[test]
    fn test_detect_processes_end_to_end() {
        let elements = vec![
            create_func("start_process", false),
            create_func("process_step_1", false),
            create_func("process_step_2", false),
            create_func("save_to_db", false),
        ];

        let relationships = vec![
            create_rel("start_process", "process_step_1"),
            create_rel("process_step_1", "process_step_2"),
            create_rel("process_step_2", "save_to_db"),
        ];

        let result = detect_processes(&elements, &relationships, None);

        assert_eq!(result.process_elements.len(), 1);
        let process = &result.process_elements[0];
        assert_eq!(process.element_type, "process");
        assert_eq!(process.name, "Start_process \u{2192} Save_to_db");
        assert_eq!(process.metadata["stepCount"], 4);

        let rel_count = result.process_relationships.len();
        assert_eq!(rel_count, 5);

        let step_rels: Vec<_> = result
            .process_relationships
            .iter()
            .filter(|r| r.rel_type == "step_in_process")
            .collect();
        assert_eq!(step_rels.len(), 4);
    }
}
