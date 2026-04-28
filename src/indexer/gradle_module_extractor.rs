use crate::db::models::Relationship;
use regex::Regex;
use std::sync::OnceLock;

static PROJECT_DEP_RE: OnceLock<Regex> = OnceLock::new();
static LIBS_CATALOG_RE: OnceLock<Regex> = OnceLock::new();
static EXTERNAL_DEP_RE: OnceLock<Regex> = OnceLock::new();

/// Extractor for Gradle module dependencies
/// Parses build.gradle.kts for project dependencies and library references
pub struct GradleModuleExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> GradleModuleExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self { source, file_path }
    }

    pub fn extract(&self) -> (Vec<crate::db::models::CodeElement>, Vec<Relationship>) {
        let content = match std::str::from_utf8(self.source) {
            Ok(s) => s,
            Err(_) => return (Vec::new(), Vec::new()),
        };

        let mut relationships = Vec::new();

        // Extract project dependencies
        let project_rels = self.extract_project_deps(content);
        relationships.extend(project_rels);

        // Extract version catalog references
        let catalog_rels = self.extract_version_catalog_refs(content);
        relationships.extend(catalog_rels);

        // Extract external dependencies
        let external_rels = self.extract_external_deps(content);
        relationships.extend(external_rels);

        (Vec::new(), relationships)
    }

    fn extract_project_deps(&self, content: &str) -> Vec<Relationship> {
        let mut relationships = Vec::new();

        // Match implementation(project(":module")) or api(project(":core:network"))
        let re = PROJECT_DEP_RE.get_or_init(|| {
            Regex::new(r#"(?:implementation|api|compileOnly|runtimeOnly|testImplementation)\s*\(\s*project\s*\(\s*"([^"]+)"\s*\)\s*\)"#).unwrap()
        });

        for cap in re.captures_iter(content) {
            if let Some(module_match) = cap.get(1) {
                let module_path = module_match.as_str();
                let module_name = module_path.trim_start_matches(':');

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: format!("module:{}", module_name),
                    rel_type: "depends_on_module".to_string(),
                    confidence: 0.95,
                    metadata: serde_json::json!({
                        "module_path": module_path,
                        "module_name": module_name,
                        "dependency_type": "project",
                    }),
                });
            }
        }

        relationships
    }

    fn extract_version_catalog_refs(&self, content: &str) -> Vec<Relationship> {
        let mut relationships = Vec::new();

        // Match implementation(libs.androidx.room.runtime)
        let re = LIBS_CATALOG_RE.get_or_init(|| {
            Regex::new(r"(?:implementation|api|compileOnly|runtimeOnly|testImplementation)\s*\(\s*libs\.([\w.]+)\s*\)").unwrap()
        });

        for cap in re.captures_iter(content) {
            if let Some(lib_match) = cap.get(1) {
                let lib_ref = lib_match.as_str();

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: format!("catalog:libs.{}", lib_ref),
                    rel_type: "uses_library".to_string(),
                    confidence: 0.90,
                    metadata: serde_json::json!({
                        "catalog_ref": format!("libs.{}", lib_ref),
                        "source": "version_catalog",
                    }),
                });
            }
        }

        relationships
    }

    fn extract_external_deps(&self, content: &str) -> Vec<Relationship> {
        let mut relationships = Vec::new();

        // Match implementation("group:name:version")
        let re = EXTERNAL_DEP_RE.get_or_init(|| {
            Regex::new(r#"(?:implementation|api|compileOnly|runtimeOnly|testImplementation)\s*\(\s*"([^"]+:[^"]+:[^"]+)"\s*\)"#).unwrap()
        });

        for cap in re.captures_iter(content) {
            if let Some(dep_match) = cap.get(1) {
                let dep_string = dep_match.as_str();
                let parts: Vec<&str> = dep_string.split(':').collect();

                if parts.len() == 3 {
                    let group = parts[0];
                    let name = parts[1];
                    let version = parts[2];

                    relationships.push(Relationship {
                        id: None,
                        source_qualified: self.file_path.to_string(),
                        target_qualified: format!("lib:{}:{}", group, name),
                        rel_type: "uses_library".to_string(),
                        confidence: 0.95,
                        metadata: serde_json::json!({
                            "group": group,
                            "name": name,
                            "version": version,
                            "full_coord": dep_string,
                            "source": "external",
                        }),
                    });
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
    fn test_extract_project_deps() {
        let source = r#"
            dependencies {
                implementation(project(":core"))
                api(project(":feature:login"))
                testImplementation(project(":test:common"))
            }
        "#;
        let extractor = GradleModuleExtractor::new(source.as_bytes(), "./app/build.gradle.kts");
        let (_, relationships) = extractor.extract();

        let project_deps: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "depends_on_module")
            .collect();

        assert_eq!(project_deps.len(), 3, "Should find 3 project deps");
        assert!(project_deps
            .iter()
            .any(|r| r.target_qualified.contains("core")));
        assert!(project_deps
            .iter()
            .any(|r| r.target_qualified.contains("feature:login")));
    }

    #[test]
    fn test_extract_version_catalog_refs() {
        let source = r#"
            dependencies {
                implementation(libs.androidx.room.runtime)
                implementation(libs.kotlinx.coroutines.android)
                api(libs.retrofit)
            }
        "#;
        let extractor = GradleModuleExtractor::new(source.as_bytes(), "./app/build.gradle.kts");
        let (_, relationships) = extractor.extract();

        let catalog_refs: Vec<_> = relationships
            .iter()
            .filter(|r| {
                r.rel_type == "uses_library"
                    && r.metadata.get("source").unwrap() == "version_catalog"
            })
            .collect();

        assert!(!catalog_refs.is_empty(), "Should find version catalog refs");
        assert!(catalog_refs
            .iter()
            .any(|r| r.target_qualified.contains("room.runtime")));
    }

    #[test]
    fn test_extract_external_deps() {
        let source = r#"
            dependencies {
                implementation("com.squareup.retrofit2:retrofit:2.9.0")
                implementation("io.coil-kt:coil-compose:2.4.0")
            }
        "#;
        let extractor = GradleModuleExtractor::new(source.as_bytes(), "./app/build.gradle.kts");
        let (_, relationships) = extractor.extract();

        let external: Vec<_> = relationships
            .iter()
            .filter(|r| {
                r.rel_type == "uses_library" && r.metadata.get("source").unwrap() == "external"
            })
            .collect();

        assert_eq!(external.len(), 2, "Should find 2 external deps");
        assert!(external
            .iter()
            .any(|r| r.target_qualified.contains("retrofit")));
    }
}
