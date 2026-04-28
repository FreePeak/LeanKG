use crate::db::models::{CodeElement, Relationship};
use std::path::Path;

pub struct GradleExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> GradleExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self { source, file_path }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let content = std::str::from_utf8(self.source).unwrap_or("");
        let mut elements = Vec::new();
        let mut relationships = Vec::new();

        let file_name = Path::new(self.file_path)
            .file_name()
            .and_then(|n| n.to_str())
            .unwrap_or("");

        elements.push(CodeElement {
            qualified_name: self.file_path.to_string(),
            element_type: "build_file".to_string(),
            name: file_name.to_string(),
            file_path: self.file_path.to_string(),
            language: "gradle".to_string(),
            ..Default::default()
        });

        let mut group = None;
        let mut version = None;
        let mut artifact_id = None;

        let parent = Path::new(self.file_path)
            .parent()
            .and_then(|p| p.file_name())
            .and_then(|n| n.to_str())
            .unwrap_or("unknown");

        for line in content.lines() {
            let trimmed = line.trim();

            if let Some(g) = Self::extract_string_assignment(trimmed, "group") {
                group = Some(g);
            }
            if let Some(v) = Self::extract_string_assignment(trimmed, "version") {
                version = Some(v);
            }
            if let Some(a) = Self::extract_string_assignment(trimmed, "artifactId") {
                artifact_id = Some(a);
            }

            for dep in Self::extract_dependencies(trimmed) {
                let dep_name = dep.clone();
                let dep_id = format!("__dep__{}", dep);

                elements.push(CodeElement {
                    qualified_name: dep_id.clone(),
                    element_type: "dependency".to_string(),
                    name: dep_name,
                    file_path: self.file_path.to_string(),
                    language: "gradle".to_string(),
                    ..Default::default()
                });

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: dep_id,
                    rel_type: "has_dependency".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({
                        "scope": Self::extract_dependency_scope(trimmed),
                    }),
                });
            }

            if let Some(plugin) = Self::extract_plugin(trimmed) {
                let plugin_id = format!("__plugin__{}", plugin);
                elements.push(CodeElement {
                    qualified_name: plugin_id.clone(),
                    element_type: "plugin".to_string(),
                    name: plugin,
                    file_path: self.file_path.to_string(),
                    language: "gradle".to_string(),
                    ..Default::default()
                });
                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: plugin_id,
                    rel_type: "uses_plugin".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({}),
                });
            }
        }

        let project_name = group
            .as_deref()
            .unwrap_or(artifact_id.as_deref().unwrap_or(parent));
        elements.push(CodeElement {
            qualified_name: format!("__gradle_project__{}", project_name),
            element_type: "gradle_project".to_string(),
            name: project_name.to_string(),
            file_path: self.file_path.to_string(),
            language: "gradle".to_string(),
            metadata: serde_json::json!({
                "group": group,
                "version": version,
                "artifact_id": artifact_id,
            }),
            ..Default::default()
        });

        (elements, relationships)
    }

    fn extract_string_assignment(line: &str, key: &str) -> Option<String> {
        let pattern = format!("{} =", key);
        if line.starts_with(&pattern) || line.starts_with(&format!("{}=", key)) {
            let value = line.split('=').nth(1)?;
            let cleaned = value.trim().trim_matches('"').trim();
            if !cleaned.is_empty() {
                return Some(cleaned.to_string());
            }
        }
        None
    }

    fn extract_dependencies(line: &str) -> Vec<String> {
        let mut deps = Vec::new();
        for scope in &[
            "implementation",
            "api",
            "compileOnly",
            "runtimeOnly",
            "testImplementation",
            "testCompileOnly",
            "testRuntimeOnly",
        ] {
            let pattern = format!("{}(", scope);
            if line.contains(&pattern) {
                if let Some(inner) = Self::extract_paren_content(line, scope) {
                    let parts: Vec<&str> = inner.split(':').collect();
                    if parts.len() >= 2 {
                        deps.push(parts[0..2.min(parts.len())].join(":"));
                    } else {
                        deps.push(inner);
                    }
                }
            }
        }
        deps
    }

    fn extract_dependency_scope(line: &str) -> &'static str {
        if line.contains("testImplementation") || line.contains("testCompileOnly") {
            "test"
        } else if line.contains("compileOnly") {
            "compileOnly"
        } else if line.contains("runtimeOnly") {
            "runtime"
        } else {
            "main"
        }
    }

    fn extract_plugin(line: &str) -> Option<String> {
        if line.contains("id(") {
            let inner = Self::extract_paren_content(line, "id")?;
            Some(inner.trim_matches('"').to_string())
        } else if line.contains("kotlin(") {
            let inner = Self::extract_paren_content(line, "kotlin")?;
            Some(format!("kotlin-{}", inner.trim_matches('"')))
        } else {
            None
        }
    }

    fn extract_paren_content(line: &str, prefix: &str) -> Option<String> {
        let pattern = format!("{}(", prefix);
        let start = line.find(&pattern)?;
        let rest = &line[start + pattern.len()..];
        let end = rest.find(')')?;
        Some(rest[..end].to_string())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_gradle_dependencies() {
        let source = br#"
plugins {
    id("org.springframework.boot") version "3.2.0"
    kotlin("jvm") version "1.9.20"
}

dependencies {
    implementation("com.example:core:1.0.0")
    testImplementation("org.junit.jupiter:junit-jupiter:5.10.0")
}
"#;
        let extractor = GradleExtractor::new(source.as_slice(), "build.gradle.kts");
        let (_, relationships) = extractor.extract();

        let deps: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "has_dependency")
            .collect();
        assert!(
            deps.len() >= 2,
            "Should extract at least 2 dependencies, got {}",
            deps.len()
        );
    }

    #[test]
    fn test_extract_gradle_group_artifact() {
        let source = br#"
group = "com.example"
version = "1.0.0"
"#;
        let extractor = GradleExtractor::new(source.as_slice(), "build.gradle.kts");
        let (elements, _) = extractor.extract();
        let project: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "gradle_project")
            .collect();
        assert!(!project.is_empty(), "Should extract project metadata");
    }
}
