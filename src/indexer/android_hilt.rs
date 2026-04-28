use crate::db::models::{CodeElement, Relationship};
use crate::indexer::kotlin_utils::find_class_body_end;
use regex::Regex;
use std::sync::OnceLock;

static MODULE_RE: OnceLock<Regex> = OnceLock::new();
static PROVIDES_RE: OnceLock<Regex> = OnceLock::new();
static INJECT_RE: OnceLock<Regex> = OnceLock::new();
static PARAM_RE: OnceLock<Regex> = OnceLock::new();
static FIELD_INJECT_RE: OnceLock<Regex> = OnceLock::new();

/// Extractor for Hilt dependency injection patterns from Kotlin files
pub struct AndroidHiltExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> AndroidHiltExtractor<'a> {
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

        // Extract modules
        let modules = self.extract_modules(content);
        for module in &modules {
            elements.push(module.clone());
        }

        // Extract providers
        let (providers, provider_rels) = self.extract_providers(content, &modules);
        for provider in &providers {
            elements.push(provider.clone());
        }
        relationships.extend(provider_rels);

        // Extract injections
        let injection_rels = self.extract_injections(content);
        relationships.extend(injection_rels);

        (elements, relationships)
    }

    fn extract_modules(&self, content: &str) -> Vec<CodeElement> {
        let mut modules = Vec::new();
        let re = MODULE_RE.get_or_init(|| {
            Regex::new(r"(?s)@Module\s*\n?\s*(?:@InstallIn\(.*?\)\s*\n?\s*)?(?:abstract\s+)?(?:class|object)\s+(\w+)").unwrap()
        });

        for cap in re.captures_iter(content) {
            if let Some(name_match) = cap.get(1) {
                let module_name = name_match.as_str();
                let qualified_name = format!("{}::HiltModule:{}", self.file_path, module_name);

                modules.push(CodeElement {
                    qualified_name,
                    element_type: "hilt_module".to_string(),
                    name: module_name.to_string(),
                    file_path: self.file_path.to_string(),
                    language: "kotlin".to_string(),
                    metadata: serde_json::json!({"class_name": module_name}),
                    ..Default::default()
                });
            }
        }

        modules
    }

    fn extract_providers(
        &self,
        content: &str,
        modules: &[CodeElement],
    ) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut providers = Vec::new();
        let mut relationships = Vec::new();

        // Build module body spans by matching each module element by name — avoids zip fragility
        let module_name_re_str = r"(?s)@Module[^{]*?(?:class|object)\s+MODNAME\b";
        let module_spans: Vec<(&CodeElement, usize, usize)> = modules
            .iter()
            .filter_map(|module| {
                let pattern = module_name_re_str.replace("MODNAME", &regex::escape(&module.name));
                Regex::new(&pattern).ok().and_then(|re| {
                    re.find(content).map(|mat| {
                        let end = find_class_body_end(content, mat.start());
                        (module, mat.start(), end)
                    })
                })
            })
            .collect();

        let provides_re = PROVIDES_RE.get_or_init(|| {
            Regex::new(
                r"@Provides\s*\n?(?:@Singleton\s*\n?)?\s*fun\s+(\w+)\s*\([^)]*\)\s*:\s*(\w+)",
            )
            .unwrap()
        });

        for cap in provides_re.captures_iter(content) {
            let method_match = cap.get(1);
            let return_match = cap.get(2);
            if let (Some(method_name), Some(return_type)) = (method_match, return_match) {
                let provider_pos = cap.get(0).map(|m| m.start()).unwrap_or(0);
                let provider_name = method_name.as_str();
                let return_type_name = return_type.as_str();

                let qualified_name = format!("{}::HiltProvider:{}", self.file_path, provider_name);

                providers.push(CodeElement {
                    qualified_name: qualified_name.clone(),
                    element_type: "hilt_provider".to_string(),
                    name: provider_name.to_string(),
                    file_path: self.file_path.to_string(),
                    language: "kotlin".to_string(),
                    metadata: serde_json::json!({
                        "method_name": provider_name,
                        "provides_type": return_type_name,
                    }),
                    ..Default::default()
                });

                // Link to the module whose body contains this provider
                let containing_module = module_spans
                    .iter()
                    .find(|(_, start, end)| provider_pos >= *start && provider_pos < *end);
                if let Some((module, _, _)) = containing_module {
                    relationships.push(Relationship {
                        id: None,
                        source_qualified: module.qualified_name.clone(),
                        target_qualified: qualified_name.clone(),
                        rel_type: "hilt_module_provides".to_string(),
                        confidence: 0.9,
                        metadata: serde_json::json!({}),
                    });
                }

                relationships.push(Relationship {
                    id: None,
                    source_qualified: qualified_name,
                    target_qualified: format!("__type__{}", return_type_name),
                    rel_type: "hilt_provides".to_string(),
                    confidence: 0.9,
                    metadata: serde_json::json!({"provided_type": return_type_name}),
                });
            }
        }

        (providers, relationships)
    }

    fn extract_injections(&self, content: &str) -> Vec<Relationship> {
        let mut relationships = Vec::new();

        let inject_re = INJECT_RE.get_or_init(|| {
            Regex::new(r"class\s+(\w+).*?@Inject\s*\n?\s*constructor\s*\(([^)]+)\)").unwrap()
        });
        let param_re = PARAM_RE.get_or_init(|| Regex::new(r"(\w+)\s*:\s*(\w+)").unwrap());

        for cap in inject_re.captures_iter(content) {
            let class_match = cap.get(1);
            let params_match = cap.get(2);
            if let (Some(class_name), Some(params)) = (class_match, params_match) {
                let class_name_str = class_name.as_str();
                let params_str = params.as_str();

                for param_cap in param_re.captures_iter(params_str) {
                    if let Some(param_type) = param_cap.get(2) {
                        let type_name = param_type.as_str();
                        relationships.push(Relationship {
                            id: None,
                            source_qualified: format!(
                                "{}::__class__{}",
                                self.file_path, class_name_str
                            ),
                            target_qualified: format!("__type__{}", type_name),
                            rel_type: "hilt_injected".to_string(),
                            confidence: 0.8,
                            metadata: serde_json::json!({"injected_type": type_name}),
                        });
                    }
                }
            }
        }

        // Match @Inject field injection
        let field_inject_re = FIELD_INJECT_RE.get_or_init(|| {
            Regex::new(r"@Inject\s*\n?\s*(?:lateinit\s+)?var\s+(\w+)\s*:\s*(\w+)").unwrap()
        });
        for cap in field_inject_re.captures_iter(content) {
            let name_match = cap.get(1);
            let type_match = cap.get(2);
            if let (Some(field_name), Some(field_type)) = (name_match, type_match) {
                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: format!("__type__{}", field_type.as_str()),
                    rel_type: "hilt_field_injected".to_string(),
                    confidence: 0.8,
                    metadata: serde_json::json!({
                        "field_name": field_name.as_str(),
                        "field_type": field_type.as_str(),
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
    fn test_extract_module() {
        let source = r#"
            @Module
            @InstallIn(SingletonComponent::class)
            class AppModule {
                @Provides
                fun provideRepo(): Repository = RepositoryImpl()
            }
        "#;
        let extractor = AndroidHiltExtractor::new(source.as_bytes(), "./test.kt");
        let (elements, _) = extractor.extract();

        let modules: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "hilt_module")
            .collect();
        assert_eq!(modules.len(), 1);
        assert_eq!(modules[0].name, "AppModule");
    }

    #[test]
    fn test_extract_provider() {
        let source = r#"
            @Module
            class AppModule {
                @Provides
                @Singleton
                fun provideDatabase(): TvDatabase = Room.databaseBuilder(...).build()
            }
        "#;
        let extractor = AndroidHiltExtractor::new(source.as_bytes(), "./test.kt");
        let (elements, relationships) = extractor.extract();

        let providers: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "hilt_provider")
            .collect();
        assert!(!providers.is_empty());
        assert_eq!(providers[0].name, "provideDatabase");

        // Check relationship to type
        let provides_rels: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "hilt_provides")
            .collect();
        assert!(!provides_rels.is_empty());
    }

    #[test]
    fn test_extract_inject() {
        let source = r#"
            class ViewModel @Inject constructor(
                private val repository: ChannelRepository
            )
        "#;
        let extractor = AndroidHiltExtractor::new(source.as_bytes(), "./test.kt");
        let (_, relationships) = extractor.extract();

        let injected: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "hilt_injected")
            .collect();
        assert!(!injected.is_empty());
    }
}
