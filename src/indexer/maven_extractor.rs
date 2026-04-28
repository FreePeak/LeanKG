use crate::db::models::{CodeElement, Relationship};
use regex::Regex;

pub struct MavenExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> MavenExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self { source, file_path }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let content = std::str::from_utf8(self.source).unwrap_or("");
        let mut elements = Vec::new();
        let mut relationships = Vec::new();

        let group_id = Self::extract_tag(content, "groupId").unwrap_or_default();
        let artifact_id = Self::extract_tag(content, "artifactId").unwrap_or_default();
        let version = Self::extract_tag(content, "version").unwrap_or_default();
        let packaging =
            Self::extract_tag(content, "packaging").unwrap_or_else(|| "jar".to_string());

        elements.push(CodeElement {
            qualified_name: format!("__maven_project__{}:{}", group_id, artifact_id),
            element_type: "maven_project".to_string(),
            name: artifact_id.clone(),
            file_path: self.file_path.to_string(),
            language: "maven".to_string(),
            metadata: serde_json::json!({
                "groupId": group_id,
                "artifactId": artifact_id,
                "version": version,
                "packaging": packaging,
            }),
            ..Default::default()
        });

        let dep_re = Regex::new(r"<dependency>([\s\S]*?)</dependency>").unwrap();
        for cap in dep_re.captures_iter(content) {
            let dep_block = &cap[1];
            let dep_group = Self::extract_tag(dep_block, "groupId").unwrap_or_default();
            let dep_artifact = Self::extract_tag(dep_block, "artifactId").unwrap_or_default();
            let dep_version = Self::extract_tag(dep_block, "version");
            let dep_scope = Self::extract_tag(dep_block, "scope");

            if !dep_artifact.is_empty() {
                let dep_id = format!("__dep__{}:{}", dep_group, dep_artifact);
                elements.push(CodeElement {
                    qualified_name: dep_id.clone(),
                    element_type: "dependency".to_string(),
                    name: dep_artifact,
                    file_path: self.file_path.to_string(),
                    language: "maven".to_string(),
                    ..Default::default()
                });

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: dep_id,
                    rel_type: "has_dependency".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({
                        "scope": dep_scope.unwrap_or_else(|| "compile".to_string()),
                        "version": dep_version,
                    }),
                });
            }
        }

        (elements, relationships)
    }

    fn extract_tag(content: &str, tag: &str) -> Option<String> {
        let re = Regex::new(&format!(r"<{}>([^<]+)</{}>", tag, tag)).ok()?;
        re.captures(content)
            .and_then(|cap| cap.get(1))
            .map(|m| m.as_str().trim().to_string())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_maven_dependencies() {
        let source = br#"<?xml version="1.0" encoding="UTF-8"?>
<project>
    <groupId>com.example</groupId>
    <artifactId>my-app</artifactId>
    <version>1.0.0</version>
    <dependencies>
        <dependency>
            <groupId>org.springframework.boot</groupId>
            <artifactId>spring-boot-starter-web</artifactId>
        </dependency>
        <dependency>
            <groupId>org.junit.jupiter</groupId>
            <artifactId>junit-jupiter</artifactId>
            <scope>test</scope>
        </dependency>
    </dependencies>
</project>"#;
        let extractor = MavenExtractor::new(source.as_slice(), "pom.xml");
        let (_, relationships) = extractor.extract();
        let deps: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "has_dependency")
            .collect();
        assert!(
            deps.len() >= 2,
            "Should extract at least 2 Maven dependencies, got {}",
            deps.len()
        );
    }

    #[test]
    fn test_extract_maven_project_coords() {
        let source = br#"<?xml version="1.0"?>
<project>
    <groupId>com.example</groupId>
    <artifactId>my-app</artifactId>
    <version>1.0.0</version>
    <packaging>jar</packaging>
</project>"#;
        let extractor = MavenExtractor::new(source.as_slice(), "pom.xml");
        let (elements, _) = extractor.extract();
        let project: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "maven_project")
            .collect();
        assert!(!project.is_empty(), "Should extract Maven project");
        assert_eq!(project[0].metadata["groupId"], "com.example");
        assert_eq!(project[0].metadata["artifactId"], "my-app");
    }
}
