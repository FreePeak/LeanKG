use crate::db::models::{CodeElement, Relationship};
use regex::Regex;
use std::sync::OnceLock;

static VIEWMODEL_RE: OnceLock<Regex> = OnceLock::new();
static REPOSITORY_INTERFACE_RE: OnceLock<Regex> = OnceLock::new();
static REPOSITORY_IMPL_RE: OnceLock<Regex> = OnceLock::new();
static HILT_VIEWMODEL_RE: OnceLock<Regex> = OnceLock::new();
static INJECT_CONSTRUCTOR_RE: OnceLock<Regex> = OnceLock::new();
static REPOSITORY_FIELD_RE: OnceLock<Regex> = OnceLock::new();

pub struct ViewModelRepositoryExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> ViewModelRepositoryExtractor<'a> {
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

        let viewmodels = self.extract_viewmodels(content);
        for vm in &viewmodels {
            elements.push(vm.clone());
        }

        let repos = self.extract_repositories(content);
        for repo in &repos {
            elements.push(repo.clone());
        }

        let vm_repo_rels = self.create_vm_repo_relationships(&viewmodels, &repos, content);
        relationships.extend(vm_repo_rels);

        (elements, relationships)
    }

    fn extract_viewmodels(&self, content: &str) -> Vec<CodeElement> {
        let mut viewmodels = Vec::new();

        let hilt_vm_re = HILT_VIEWMODEL_RE.get_or_init(|| {
            Regex::new(r"(?s)@HiltViewModel\s*\n?\s*(?:abstract\s+)?class\s+(\w+)").unwrap()
        });

        for cap in hilt_vm_re.captures_iter(content) {
            if let Some(name_match) = cap.get(1) {
                let vm_name = name_match.as_str();
                let qualified_name = format!("{}::ViewModel:{}", self.file_path, vm_name);

                viewmodels.push(CodeElement {
                    qualified_name,
                    element_type: "viewmodel".to_string(),
                    name: vm_name.to_string(),
                    file_path: self.file_path.to_string(),
                    language: "kotlin".to_string(),
                    metadata: serde_json::json!({
                        "vm_class": vm_name,
                        "has_hilt_annotation": true,
                    }),
                    ..Default::default()
                });
            }
        }

        let vm_re = VIEWMODEL_RE.get_or_init(|| {
            Regex::new(r"(?s)(?:abstract\s+)?class\s+(\w+)\s*:\s*(?:androidx\.lifecycle\.ViewModel|ViewModel)\s*[,{]").unwrap()
        });

        for cap in vm_re.captures_iter(content) {
            if let Some(name_match) = cap.get(1) {
                let vm_name = name_match.as_str();

                if viewmodels.iter().any(|v| v.name == vm_name) {
                    continue;
                }

                let qualified_name = format!("{}::ViewModel:{}", self.file_path, vm_name);

                viewmodels.push(CodeElement {
                    qualified_name,
                    element_type: "viewmodel".to_string(),
                    name: vm_name.to_string(),
                    file_path: self.file_path.to_string(),
                    language: "kotlin".to_string(),
                    metadata: serde_json::json!({
                        "vm_class": vm_name,
                        "has_hilt_annotation": false,
                    }),
                    ..Default::default()
                });
            }
        }

        viewmodels
    }

    fn extract_repositories(&self, content: &str) -> Vec<CodeElement> {
        let mut repos = Vec::new();

        let repo_interface_re = REPOSITORY_INTERFACE_RE.get_or_init(|| {
            Regex::new(r"(?s)(?:interface|abstract\s+class)\s+(\w+Repository)").unwrap()
        });

        for cap in repo_interface_re.captures_iter(content) {
            if let Some(name_match) = cap.get(1) {
                let repo_name = name_match.as_str();
                let qualified_name = format!("{}::Repository:{}", self.file_path, repo_name);

                repos.push(CodeElement {
                    qualified_name,
                    element_type: "repository".to_string(),
                    name: repo_name.to_string(),
                    file_path: self.file_path.to_string(),
                    language: "kotlin".to_string(),
                    metadata: serde_json::json!({
                        "repo_interface": repo_name,
                        "repo_type": "interface",
                    }),
                    ..Default::default()
                });
            }
        }

        let repo_impl_re = REPOSITORY_IMPL_RE.get_or_init(|| {
            Regex::new(r"(?s)(?:class|object)\s+(\w+RepositoryImpl?)\s*:\s*(.*?Repository)")
                .unwrap()
        });

        for cap in repo_impl_re.captures_iter(content) {
            if let Some(name_match) = cap.get(1) {
                let impl_name = name_match.as_str();

                if repos.iter().any(|r| r.name == impl_name) {
                    continue;
                }

                let qualified_name = format!("{}::Repository:{}", self.file_path, impl_name);

                repos.push(CodeElement {
                    qualified_name,
                    element_type: "repository_impl".to_string(),
                    name: impl_name.to_string(),
                    file_path: self.file_path.to_string(),
                    language: "kotlin".to_string(),
                    metadata: serde_json::json!({
                        "repo_implementation": impl_name,
                        "implements_interface": cap.get(2).map(|m| m.as_str()).unwrap_or("unknown"),
                        "repo_type": "implementation",
                    }),
                    ..Default::default()
                });
            }
        }

        repos
    }

    fn create_vm_repo_relationships(
        &self,
        viewmodels: &[CodeElement],
        repositories: &[CodeElement],
        content: &str,
    ) -> Vec<Relationship> {
        let mut relationships = Vec::new();

        let inject_re = INJECT_CONSTRUCTOR_RE.get_or_init(|| {
            Regex::new(r"@Inject\s*\n?\s*constructor\s*\(([^)]+)\)").unwrap()
        });

        for vm in viewmodels {
            let vm_name = &vm.name;

            if let Some(cap) = inject_re.captures(content) {
                if let Some(params_match) = cap.get(1) {
                    let params_str = params_match.as_str();

                    for repo in repositories {
                        if params_str.contains(&repo.name)
                            || params_str.contains(&format!(": {}", repo.name))
                            || params_str.contains(&format!(", {}", repo.name))
                        {
                            relationships.push(Relationship {
                                id: None,
                                source_qualified: vm.qualified_name.clone(),
                                target_qualified: repo.qualified_name.clone(),
                                rel_type: "viewmodel_owns_repository".to_string(),
                                confidence: 0.9,
                                metadata: serde_json::json!({
                                    "vm_name": vm_name,
                                    "repo_name": &repo.name,
                                    "injection_type": "constructor",
                                }),
                            });
                        }
                    }
                }
            }

            let field_re = REPOSITORY_FIELD_RE.get_or_init(|| {
                Regex::new(r"(?:private\s+)?(?:val|var)\s+(\w+)\s*:\s*(\w+Repository)").unwrap()
            });

            for cap in field_re.captures_iter(content) {
                if let (Some(_field_name), Some(repo_type)) = (cap.get(1), cap.get(2)) {
                    for repo in repositories {
                        if repo.name == repo_type.as_str() {
                            relationships.push(Relationship {
                                id: None,
                                source_qualified: vm.qualified_name.clone(),
                                target_qualified: repo.qualified_name.clone(),
                                rel_type: "viewmodel_owns_repository".to_string(),
                                confidence: 0.85,
                                metadata: serde_json::json!({
                                    "vm_name": vm_name,
                                    "repo_name": &repo.name,
                                    "injection_type": "property",
                                }),
                            });
                        }
                    }
                }
            }
        }

        relationships
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_viewmodel() {
        let source = r#"
            @HiltViewModel
            class MainViewModel @Inject constructor(
                private val repository: ChannelRepository
            ) : ViewModel() {
                fun loadData() {}
            }
        "#;
        let extractor =
            ViewModelRepositoryExtractor::new(source.as_bytes(), "./ui/MainViewModel.kt");
        let (elements, _) = extractor.extract();

        let viewmodels: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "viewmodel")
            .collect();
        assert_eq!(viewmodels.len(), 1);
        assert_eq!(viewmodels[0].name, "MainViewModel");
    }

    #[test]
    fn test_extract_repository_interface() {
        let source = r#"
            interface ChannelRepository {
                suspend fun getChannels(): List<Channel>
            }
            
            class ChannelRepositoryImpl @Inject constructor(
                private val api: ChannelApi
            ) : ChannelRepository {
                override suspend fun getChannels(): List<Channel> = emptyList()
            }
        "#;
        let extractor =
            ViewModelRepositoryExtractor::new(source.as_bytes(), "./data/ChannelRepo.kt");
        let (elements, _) = extractor.extract();

        let repos: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "repository" || e.element_type == "repository_impl")
            .collect();
        assert!(repos.len() >= 1, "Should find at least 1 repository");
    }

    #[test]
    fn test_vm_repo_relationship() {
        let source = r#"
            @HiltViewModel
            class MainViewModel @Inject constructor(
                private val channelRepo: ChannelRepository
            ) : ViewModel() {
                fun loadData() {}
            }
            
            interface ChannelRepository {
                suspend fun getChannels(): List<Channel>
            }
        "#;
        let extractor =
            ViewModelRepositoryExtractor::new(source.as_bytes(), "./ui/MainViewModel.kt");
        let (_, relationships) = extractor.extract();

        let vm_repo_rels: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "viewmodel_owns_repository")
            .collect();
        assert!(
            !vm_repo_rels.is_empty(),
            "Should find ViewModel -> Repository relationship"
        );
    }
}