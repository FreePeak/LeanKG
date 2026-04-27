use crate::db::models::{CodeElement, Relationship};
use regex::Regex;
use std::sync::OnceLock;

static DISPATCHER_RE: OnceLock<Regex> = OnceLock::new();
static WITH_DISPATCHER_RE: OnceLock<Regex> = OnceLock::new();
static FUNC_CONTEXT_RE: OnceLock<Regex> = OnceLock::new();

/// Extractor for coroutine dispatcher usage patterns in Kotlin files
pub struct CoroutineDispatcherExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> CoroutineDispatcherExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self { source, file_path }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let content = match std::str::from_utf8(self.source) {
            Ok(s) => s,
            Err(_) => {
                eprintln!("warn: non-UTF-8 content in {}, skipping", self.file_path);
                return (Vec::new(), Vec::new());
            }
        };
        let mut elements = Vec::new();
        let mut relationships = Vec::new();

        // Extract Dispatchers.IO, .Main, .Default usage
        let dispatchers = self.extract_dispatcher_usages(content);
        for disp in &dispatchers {
            elements.push(disp.clone());
        }

        // Create relationships for dispatcher usages
        for elem in &dispatchers {
            if let Some(meta) = elem.metadata.as_object() {
                if let Some(disp_type_val) = meta.get("dispatcher") {
                    if let Some(disp_str) = disp_type_val.as_str() {
                        let disp_type = match disp_str {
                            "Dispatchers.IO" => "io",
                            "Dispatchers.Main" => "main",
                            "Dispatchers.Default" => "default",
                            _ => "unknown",
                        };
                        relationships.push(Relationship {
                            id: None,
                            source_qualified: self.file_path.to_string(),
                            target_qualified: elem.qualified_name.clone(),
                            rel_type: "uses_dispatcher".to_string(),
                            confidence: 0.95,
                            metadata: serde_json::json!({
                                "dispatcher_type": disp_type,
                            }),
                        });
                    }
                }
            }
        }

        // Extract withContext dispatcher switches
        let context_switches = self.extract_context_switches(content);
        relationships.extend(context_switches);

        (elements, relationships)
    }

    fn extract_dispatcher_usages(&self, content: &str) -> Vec<CodeElement> {
        let mut elements = Vec::new();
        let re =
            DISPATCHER_RE.get_or_init(|| Regex::new(r"Dispatchers\.(IO|Main|Default)").unwrap());

        struct FuncMarker {
            line: usize,
            text: String,
        }
        let mut func_markers: Vec<FuncMarker> = Vec::new();
        let func_re =
            FUNC_CONTEXT_RE.get_or_init(|| Regex::new(r"(?m)^(fun |class |object )").unwrap());
        for cap in func_re.captures_iter(content) {
            if let Some(m) = cap.get(0) {
                let line_num = content[..m.start()].lines().count();
                func_markers.push(FuncMarker {
                    line: line_num,
                    text: m.as_str().trim().to_string(),
                });
            }
        }

        for cap in re.captures_iter(content) {
            if let Some(match_match) = cap.get(0) {
                let dispatcher_name = match_match.as_str();
                let line_num = content[..match_match.start()].lines().count() as u32;

                let func_context = func_markers
                    .iter()
                    .rev()
                    .find(|m| m.line < line_num as usize)
                    .map(|m| m.text.clone())
                    .unwrap_or_default();

                let elem_name = dispatcher_name.replace('.', "_");
                let qualified_name = format!("{}::Dispatcher:{}", self.file_path, elem_name);

                elements.push(CodeElement {
                    qualified_name,
                    element_type: "coroutine_dispatcher".to_string(),
                    name: elem_name.clone(),
                    file_path: self.file_path.to_string(),
                    line_start: line_num,
                    line_end: line_num,
                    language: "kotlin".to_string(),
                    metadata: serde_json::json!({
                        "dispatcher": dispatcher_name,
                        "context": func_context,
                    }),
                    ..Default::default()
                });
            }
        }

        elements
    }

    fn extract_context_switches(&self, content: &str) -> Vec<Relationship> {
        let mut relationships = Vec::new();
        let re = WITH_DISPATCHER_RE.get_or_init(|| {
            Regex::new(r"withContext\s*\(\s*Dispatchers\.(IO|Main|Default)\s*\)").unwrap()
        });

        let func_re =
            FUNC_CONTEXT_RE.get_or_init(|| Regex::new(r"(?m)^(fun |suspend fun )").unwrap());
        let mut func_markers: Vec<(usize, String)> = Vec::new();
        for cap in func_re.captures_iter(content) {
            if let Some(m) = cap.get(0) {
                let line_num = content[..m.start()].lines().count();
                func_markers.push((line_num, m.as_str().trim().to_string()));
            }
        }

        for cap in re.captures_iter(content) {
            if let Some(match_match) = cap.get(0) {
                let dispatcher_name = match_match.as_str();
                let line_num = content[..match_match.start()].lines().count() as u32;

                let func_context = func_markers
                    .iter()
                    .rev()
                    .find(|(l, _)| *l < line_num as usize)
                    .map(|(_, t)| t.clone())
                    .unwrap_or_default();

                let disp_type = match dispatcher_name {
                    "withContext(Dispatchers.IO)" => "io",
                    "withContext(Dispatchers.Main)" => "main",
                    "withContext(Dispatchers.Default)" => "default",
                    _ => "unknown",
                };

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: format!(
                        "{}::Dispatcher:{}",
                        self.file_path,
                        dispatcher_name.replace('.', "_")
                    ),
                    rel_type: "uses_dispatcher".to_string(),
                    confidence: 0.95,
                    metadata: serde_json::json!({
                        "dispatcher_type": disp_type,
                        "via": "withContext",
                        "context": func_context,
                    }),
                });
            }
        }

        relationships
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_dispatcher_usage() {
        let source = r#"
            suspend fun fetchData(): List<Data> {
                return withContext(Dispatchers.IO) {
                    repository.fetchAll()
                }
            }
            
            fun processOnMain() {
                viewModelScope.launch(Dispatchers.Main) {
                    updateUI()
                }
            }
        "#;
        let extractor = CoroutineDispatcherExtractor::new(source.as_bytes(), "./repo/DataRepo.kt");
        let (elements, relationships) = extractor.extract();

        let dispatchers: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "coroutine_dispatcher")
            .collect();
        assert!(
            dispatchers.len() >= 2,
            "Should find at least 2 dispatcher usages"
        );

        let rels: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "uses_dispatcher")
            .collect();
        assert!(
            !rels.is_empty(),
            "Should have uses_dispatcher relationships"
        );
    }

    #[test]
    fn test_extract_with_context() {
        let source = r#"
            suspend fun loadData(): Result {
                return withContext(Dispatchers.IO) {
                    network.call()
                }
            }
        "#;
        let extractor = CoroutineDispatcherExtractor::new(source.as_bytes(), "./ui/ViewModel.kt");
        let (_, relationships) = extractor.extract();

        let io_rels: Vec<_> = relationships
            .iter()
            .filter(|r| {
                r.rel_type == "uses_dispatcher"
                    && r.metadata.get("dispatcher_type").and_then(|v| v.as_str()) == Some("io")
            })
            .collect();
        assert!(
            !io_rels.is_empty(),
            "Should find IO dispatcher in withContext"
        );
    }
}
