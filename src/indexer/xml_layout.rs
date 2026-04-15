use crate::db::models::{CodeElement, Relationship};
use regex::Regex;

pub struct XmlLayoutExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> XmlLayoutExtractor<'a> {
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
            .unwrap_or("");

        elements.push(CodeElement {
            qualified_name: self.file_path.to_string(),
            element_type: "android_layout".to_string(),
            name: file_name.to_string(),
            file_path: self.file_path.to_string(),
            language: "android".to_string(),
            ..Default::default()
        });

        let view_ids: Vec<String> = Self::extract_view_ids(content);
        let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();
        for view_id in view_ids {
            if seen.contains(&view_id) {
                continue;
            }
            seen.insert(view_id.clone());
            let view_id_name = view_id;
            let view_id_qualified = format!("{}/@+id/{}", self.file_path, view_id_name);

            elements.push(CodeElement {
                qualified_name: view_id_qualified.clone(),
                element_type: "android_view_id".to_string(),
                name: view_id_name.to_string(),
                file_path: self.file_path.to_string(),
                language: "android".to_string(),
                metadata: serde_json::json!({
                    "raw_id": format!("@+id/{}", view_id_name),
                }),
                ..Default::default()
            });

            relationships.push(Relationship {
                id: None,
                source_qualified: self.file_path.to_string(),
                target_qualified: view_id_qualified,
                rel_type: "defines_view".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({}),
            });
        }

        for view_ref in Self::extract_view_references(content) {
            let ref_name = view_ref.clone();
            let ref_qualified = format!("{}/@id/{}", self.file_path, ref_name);

            elements.push(CodeElement {
                qualified_name: ref_qualified.clone(),
                element_type: "android_view_reference".to_string(),
                name: ref_name.to_string(),
                file_path: self.file_path.to_string(),
                language: "android".to_string(),
                metadata: serde_json::json!({
                    "raw_reference": view_ref,
                }),
                ..Default::default()
            });

            relationships.push(Relationship {
                id: None,
                source_qualified: self.file_path.to_string(),
                target_qualified: ref_qualified,
                rel_type: "references_view".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({}),
            });
        }

        if let Some(activity) = Self::extract_tools_context(content) {
            let activity_qualified = format!("{}", activity);
            let context_rel = format!("{}/tools:context", self.file_path);

            relationships.push(Relationship {
                id: None,
                source_qualified: context_rel,
                target_qualified: activity_qualified,
                rel_type: "associated_with".to_string(),
                confidence: 0.9,
                metadata: serde_json::json!({
                    "tools_context": activity,
                }),
            });
        }

        for class_ref in Self::extract_class_references(content) {
            relationships.push(Relationship {
                id: None,
                source_qualified: self.file_path.to_string(),
                target_qualified: class_ref,
                rel_type: "references_class".to_string(),
                confidence: 0.8,
                metadata: serde_json::json!({}),
            });
        }

        (elements, relationships)
    }

    fn extract_view_ids(content: &str) -> Vec<String> {
        let re = Regex::new(r"@\+id/([a-zA-Z_][a-zA-Z0-9_]*)").unwrap();
        re.captures_iter(content)
            .filter_map(|cap| cap.get(1))
            .map(|m| m.as_str().to_string())
            .collect()
    }

    fn extract_view_references(content: &str) -> Vec<String> {
        let re = Regex::new(r"@id/([a-zA-Z_][a-zA-Z0-9_]*)").unwrap();
        re.captures_iter(content)
            .filter_map(|cap| cap.get(1))
            .map(|m| m.as_str().to_string())
            .collect()
    }

    fn extract_tools_context(content: &str) -> Option<String> {
        let re = Regex::new(r#"tools:context\s*=\s*["']([^"']+)["']"#).ok()?;
        re.captures(content)
            .and_then(|cap| cap.get(1))
            .map(|m| m.as_str().to_string())
    }

    fn extract_class_references(content: &str) -> Vec<String> {
        let mut refs = Vec::new();

        let class_re = Regex::new(r#"android:name\s*=\s*["']([^"']+\.)([^"']+)["']"#).unwrap();
        for cap in class_re.captures_iter(content) {
            if let (Some(pkg), Some(cls)) = (cap.get(1), cap.get(2)) {
                refs.push(format!("{}{}", pkg.as_str(), cls.as_str()));
            }
        }

        refs
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_view_ids() {
        let source = br#"
<LinearLayout>
    <Button android:id="@+id/submit_button" />
    <EditText android:id="@+id/email_input" />
    <TextView android:id="@+id/welcome_text" />
</LinearLayout>"#;
        let extractor = XmlLayoutExtractor::new(source.as_slice(), "res/layout/activity_main.xml");
        let (elements, relationships) = extractor.extract();

        let ids: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_view_id")
            .collect();
        assert_eq!(ids.len(), 3, "Should extract 3 view IDs");
        assert_eq!(ids[0].name, "submit_button");

        let defs: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "defines_view")
            .collect();
        assert_eq!(defs.len(), 3);
    }

    #[test]
    fn test_extract_view_references() {
        let source = br#"
<ConstraintLayout>
    <TextView android:id="@+id/text1" />
    <TextView android:layout_below="@id/text1" />
    <Button android:layout_toStartOf="@id/text1" />
</ConstraintLayout>"#;
        let extractor = XmlLayoutExtractor::new(source.as_slice(), "res/layout/item.xml");
        let (elements, relationships) = extractor.extract();

        let refs: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_view_reference")
            .collect();
        assert_eq!(refs.len(), 2, "Should extract 2 view references");
    }

    #[test]
    fn test_extract_tools_context() {
        let source = br#"
<LinearLayout
    xmlns:tools="http://schemas.android.com/tools"
    tools:context=".MainActivity">
</LinearLayout>"#;
        let extractor = XmlLayoutExtractor::new(source.as_slice(), "res/layout/activity_main.xml");
        let (_, relationships) = extractor.extract();

        let assoc: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "associated_with")
            .collect();
        assert_eq!(assoc.len(), 1);
        assert_eq!(assoc[0].metadata["tools_context"], ".MainActivity");
    }

    #[test]
    fn test_extract_full_layout() {
        let source = br#"<?xml version="1.0" encoding="utf-8"?>
<androidx.constraintlayout.widget.ConstraintLayout
    xmlns:android="http://schemas.android.com/apk/res/android"
    xmlns:app="http://schemas.android.com/apk/res-auto"
    xmlns:tools="http://schemas.android.com/tools"
    android:layout_width="match_parent"
    android:layout_height="match_parent"
    tools:context=".ui.MainActivity">

    <TextView
        android:id="@+id/title_text"
        android:layout_width="wrap_content"
        android:layout_height="wrap_content"
        android:text="Hello"
        app:layout_constraintTop_toTopOf="parent"
        app:layout_constraintStart_toStartOf="parent" />

    <Button
        android:id="@+id/click_button"
        android:layout_width="wrap_content"
        android:layout_height="wrap_content"
        android:text="Click"
        app:layout_constraintTop_toBottomOf="@id/title_text"
        app:layout_constraintStart_toStartOf="parent" />

</androidx.constraintlayout.widget.ConstraintLayout>"#;
        let extractor = XmlLayoutExtractor::new(source.as_slice(), "res/layout/activity_main.xml");
        let (elements, relationships) = extractor.extract();

        let views: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_view_id")
            .collect();
        assert_eq!(views.len(), 2);

        let assoc: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "associated_with")
            .collect();
        assert_eq!(assoc.len(), 1);
    }

    #[test]
    fn test_extract_no_duplicates() {
        let source = br#"
<LinearLayout>
    <Button android:id="@+id/button" />
    <Button android:id="@+id/button" />
</LinearLayout>"#;
        let extractor = XmlLayoutExtractor::new(source.as_slice(), "res/layout/dup.xml");
        let (elements, _) = extractor.extract();

        let ids: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_view_id")
            .collect();
        assert_eq!(ids.len(), 1, "Should not duplicate view IDs");
    }
}
