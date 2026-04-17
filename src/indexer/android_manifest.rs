use crate::db::models::{CodeElement, Relationship};
use regex::Regex;

pub struct AndroidManifestExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> AndroidManifestExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self { source, file_path }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let content = std::str::from_utf8(self.source).unwrap_or("");
        let mut elements = Vec::new();
        let mut relationships = Vec::new();

        let file_name = std::path::Path::new(self.file_path)
            .file_name()
            .and_then(|n| n.to_str())
            .unwrap_or("AndroidManifest.xml");

        elements.push(CodeElement {
            qualified_name: self.file_path.to_string(),
            element_type: "android_manifest".to_string(),
            name: file_name.to_string(),
            file_path: self.file_path.to_string(),
            language: "android".to_string(),
            ..Default::default()
        });

        let component_tags = [
            ("activity", "android_activity"),
            ("service", "android_service"),
            ("receiver", "android_broadcast_receiver"),
            ("provider", "android_content_provider"),
        ];

        for (tag, elem_type) in &component_tags {
            for cap in Self::extract_tags(content, tag) {
                if let Some(name) = Self::extract_android_name(&cap, tag) {
                    let comp_id = format!("__android__{}__{}", tag, name.replace(['.', '$'], "_"));

                    elements.push(CodeElement {
                        qualified_name: comp_id.clone(),
                        element_type: elem_type.to_string(),
                        name: name.clone(),
                        file_path: self.file_path.to_string(),
                        language: "android".to_string(),
                        metadata: serde_json::json!({
                            "tag": tag,
                        }),
                        ..Default::default()
                    });

                    relationships.push(Relationship {
                        id: None,
                        source_qualified: self.file_path.to_string(),
                        target_qualified: comp_id,
                        rel_type: "declares_component".to_string(),
                        confidence: 1.0,
                        metadata: serde_json::json!({}),
                    });
                }
            }
        }

        if let Some(app_name) = Self::extract_tag_content(content, "application") {
            if let Some(name) = Self::extract_android_name(&app_name, "application") {
                let app_id = format!("__android__application__{}", name.replace(['.', '$'], "_"));
                elements.push(CodeElement {
                    qualified_name: app_id,
                    element_type: "android_application".to_string(),
                    name,
                    file_path: self.file_path.to_string(),
                    language: "android".to_string(),
                    ..Default::default()
                });
            }
        }

        for cap in Self::extract_tags(content, "uses-permission") {
            if let Some(name) = Self::extract_android_name(&cap, "uses-permission") {
                let perm_id = format!("__android__permission__{}", name.replace(['.', ':'], "_"));

                elements.push(CodeElement {
                    qualified_name: perm_id.clone(),
                    element_type: "android_permission".to_string(),
                    name: name.clone(),
                    file_path: self.file_path.to_string(),
                    language: "android".to_string(),
                    ..Default::default()
                });

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: perm_id,
                    rel_type: "requires_permission".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({}),
                });
            }
        }

        for cap in Self::extract_tags(content, "uses-feature") {
            if let Some(name) = Self::extract_android_name(&cap, "uses-feature") {
                let feature_id = format!("__android__feature__{}", name.replace([':', '-'], "_"));

                elements.push(CodeElement {
                    qualified_name: feature_id.clone(),
                    element_type: "android_feature".to_string(),
                    name,
                    file_path: self.file_path.to_string(),
                    language: "android".to_string(),
                    ..Default::default()
                });

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: feature_id,
                    rel_type: "declares_feature".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({}),
                });
            }
        }

        (elements, relationships)
    }

    fn extract_tags(content: &str, tag: &str) -> Vec<String> {
        let re = Regex::new(&format!(r"<{}[\s>]([^>]*)>(?:[^<]*</{}>)?", tag, tag)).unwrap();
        re.captures_iter(content)
            .map(|cap| {
                cap.get(1)
                    .map(|m| m.as_str().to_string())
                    .unwrap_or_default()
            })
            .collect()
    }

    fn extract_tag_content(content: &str, tag: &str) -> Option<String> {
        let re = Regex::new(&format!(r"<{}>([^<]*)</{}>", tag, tag)).ok()?;
        re.captures(content)
            .and_then(|cap| cap.get(1))
            .map(|m| m.as_str().to_string())
    }

    fn extract_android_name(tag_content: &str, _tag_name: &str) -> Option<String> {
        let re = Regex::new(r#"android:name\s*=\s*["']([^"']+)["']"#).ok()?;
        re.captures(tag_content)
            .and_then(|cap| cap.get(1))
            .map(|m| m.as_str().to_string())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_activity() {
        let source = br#"
<manifest xmlns:android="http://schemas.android.com/apk/res/android">
    <application>
        <activity android:name=".MainActivity" />
    </application>
</manifest>"#;
        let extractor = AndroidManifestExtractor::new(source.as_slice(), "AndroidManifest.xml");
        let (elements, _) = extractor.extract();
        let activities: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_activity")
            .collect();
        assert!(!activities.is_empty(), "Should extract activity");
        assert_eq!(activities[0].name, ".MainActivity");
    }

    #[test]
    fn test_extract_service() {
        let source = br#"
<manifest>
    <service android:name=".MyService" android:exported="false" />
</manifest>"#;
        let extractor = AndroidManifestExtractor::new(source.as_slice(), "AndroidManifest.xml");
        let (elements, _) = extractor.extract();
        let services: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_service")
            .collect();
        assert!(!services.is_empty(), "Should extract service");
    }

    #[test]
    fn test_extract_permission() {
        let source = br#"
<manifest>
    <uses-permission android:name="android.permission.INTERNET" />
    <uses-permission android:name="android.permission.ACCESS_FINE_LOCATION" />
</manifest>"#;
        let extractor = AndroidManifestExtractor::new(source.as_slice(), "AndroidManifest.xml");
        let (elements, relationships) = extractor.extract();
        let perms: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_permission")
            .collect();
        assert_eq!(perms.len(), 2, "Should extract 2 permissions");
        let rels: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "requires_permission")
            .collect();
        assert_eq!(rels.len(), 2);
    }

    #[test]
    fn test_extract_full_manifest() {
        let source = br#"<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.example.myapp">

    <uses-permission android:name="android.permission.INTERNET" />

    <application
        android:label="@string/app_name"
        android:theme="@style/AppTheme">
        <activity
            android:name=".MainActivity"
            android:exported="true">
            <intent-filter>
                <action android:name="android.intent.action.MAIN" />
                <category android:name="android.intent.category.LAUNCHER" />
            </intent-filter>
        </activity>
        <service android:name=".BackgroundService" />
    </application>

</manifest>"#;
        let extractor = AndroidManifestExtractor::new(source.as_slice(), "AndroidManifest.xml");
        let (elements, relationships) = extractor.extract();

        let activities: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_activity")
            .collect();
        assert_eq!(activities.len(), 1);

        let services: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_service")
            .collect();
        assert_eq!(services.len(), 1);

        let perms: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_permission")
            .collect();
        assert_eq!(perms.len(), 1);

        let declares: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "declares_component")
            .collect();
        assert_eq!(declares.len(), 2);
    }

    #[test]
    fn test_extract_broadcast_receiver() {
        let source = br#"
<manifest>
    <receiver android:name=".MyReceiver" android:exported="false">
        <intent-filter>
            <action android:name="android.intent.action.BOOT_COMPLETED" />
        </intent-filter>
    </receiver>
</manifest>"#;
        let extractor = AndroidManifestExtractor::new(source.as_slice(), "AndroidManifest.xml");
        let (elements, _) = extractor.extract();
        let receivers: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_broadcast_receiver")
            .collect();
        assert!(!receivers.is_empty(), "Should extract receiver");
    }

    #[test]
    fn test_extract_content_provider() {
        let source = br#"
<manifest>
    <provider
        android:name=".MyContentProvider"
        android:authorities="com.example.provider"
        android:exported="false" />
</manifest>"#;
        let extractor = AndroidManifestExtractor::new(source.as_slice(), "AndroidManifest.xml");
        let (elements, _) = extractor.extract();
        let providers: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_content_provider")
            .collect();
        assert!(!providers.is_empty(), "Should extract provider");
    }
}
