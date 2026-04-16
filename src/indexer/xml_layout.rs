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

            relationships.push(Relationship {
                id: None,
                source_qualified: self.file_path.to_string(),
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

        self.extract_resource_references(content, &mut elements, &mut relationships);
        self.extract_style_references(content, &mut elements, &mut relationships);

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

    fn extract_resource_references(
        &self,
        content: &str,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        let patterns = [
            (r#"@\s*string\s*/\s*([a-zA-Z_][a-zA-Z0-9_]*)"#, "string"),
            (r#"@\s*color\s*/\s*([a-zA-Z_][a-zA-Z0-9_]*)"#, "color"),
            (r#"@\s*dimen\s*/\s*([a-zA-Z_][a-zA-Z0-9_]*)"#, "dimen"),
            (r#"@\s*drawable\s*/\s*([a-zA-Z_][a-zA-Z0-9_]*)"#, "drawable"),
            (r#"@\s*theme\s*/\s*([a-zA-Z_][a-zA-Z0-9_./]*)"#, "theme"),
            (r#"@\s*bool\s*/\s*([a-zA-Z_][a-zA-Z0-9_]*)"#, "bool"),
            (r#"@\s*integer\s*/\s*([a-zA-Z_][a-zA-Z0-9_]*)"#, "integer"),
            (r#"@\s*array\s*/\s*([a-zA-Z_][a-zA-Z0-9_]*)"#, "array"),
        ];

        let mut seen = std::collections::HashSet::new();

        for (pattern, resource_type) in patterns {
            let re = Regex::new(pattern).unwrap();
            for cap in re.captures_iter(content) {
                if let Some(name) = cap.get(1) {
                    let resource_name = name.as_str().to_string();
                    let key = format!("{}:{}", resource_type, resource_name);
                    if seen.contains(&key) {
                        continue;
                    }
                    seen.insert(key);

                    let qualified_name =
                        format!("{}/@{}/{}", self.file_path, resource_type, resource_name);

                    elements.push(CodeElement {
                        qualified_name: qualified_name.clone(),
                        element_type: format!("android_resource_ref_{}", resource_type),
                        name: resource_name.clone(),
                        file_path: self.file_path.to_string(),
                        language: "android".to_string(),
                        metadata: serde_json::json!({
                            "resource_type": resource_type,
                            "raw_ref": format!("@{}/{}", resource_type, resource_name),
                        }),
                        ..Default::default()
                    });

                    relationships.push(Relationship {
                        id: None,
                        source_qualified: self.file_path.to_string(),
                        target_qualified: qualified_name,
                        rel_type: format!("uses_{}", resource_type),
                        confidence: 1.0,
                        metadata: serde_json::json!({
                            "resource_type": resource_type,
                        }),
                    });
                }
            }
        }
    }

    fn extract_style_references(
        &self,
        content: &str,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        let re = Regex::new(r#"style\s*=\s*["']@style/([^"']+)["']"#).unwrap();
        let mut seen = std::collections::HashSet::new();

        for cap in re.captures_iter(content) {
            if let Some(name) = cap.get(1) {
                let style_name = name.as_str().to_string();
                if seen.contains(&style_name) {
                    continue;
                }
                seen.insert(style_name.clone());

                let qualified_name = format!("{}/@style/{}", self.file_path, style_name);

                elements.push(CodeElement {
                    qualified_name: qualified_name.clone(),
                    element_type: "android_style_reference".to_string(),
                    name: style_name.clone(),
                    file_path: self.file_path.to_string(),
                    language: "android".to_string(),
                    metadata: serde_json::json!({
                        "raw_style": format!("@style/{}", style_name),
                    }),
                    ..Default::default()
                });

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: qualified_name,
                    rel_type: "uses_style".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({}),
                });
            }
        }
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

    #[test]
    fn test_extract_string_references() {
        let source = br#"
<LinearLayout>
    <TextView android:text="@string/app_name" />
    <Button android:text="@string/submit_button" />
    <EditText android:hint="@string/email_hint" />
</LinearLayout>"#;
        let extractor = XmlLayoutExtractor::new(source.as_slice(), "res/layout/login.xml");
        let (elements, relationships) = extractor.extract();

        let string_refs: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_resource_ref_string")
            .collect();
        assert_eq!(string_refs.len(), 3, "Should extract 3 string references");

        let uses_string: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "uses_string")
            .collect();
        assert_eq!(
            uses_string.len(),
            3,
            "Should have 3 uses_string relationships"
        );
    }

    #[test]
    fn test_extract_color_references() {
        let source = br#"
<LinearLayout android:background="@color/primary">
    <TextView android:textColor="@color/text_primary" />
    <Button android:background="@color/button_bg" />
</LinearLayout>"#;
        let extractor = XmlLayoutExtractor::new(source.as_slice(), "res/layout/main.xml");
        let (elements, relationships) = extractor.extract();

        let color_refs: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_resource_ref_color")
            .collect();
        assert_eq!(color_refs.len(), 3, "Should extract 3 color references");

        let uses_color: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "uses_color")
            .collect();
        assert_eq!(
            uses_color.len(),
            3,
            "Should have 3 uses_color relationships"
        );
    }

    #[test]
    fn test_extract_dimen_references() {
        let source = br#"
<LinearLayout>
    <TextView android:padding="@dimen/padding_small" />
    <Button android:layout_margin="@dimen/margin_medium" />
</LinearLayout>"#;
        let extractor = XmlLayoutExtractor::new(source.as_slice(), "res/layout/main.xml");
        let (elements, _) = extractor.extract();

        let dimen_refs: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_resource_ref_dimen")
            .collect();
        assert_eq!(dimen_refs.len(), 2, "Should extract 2 dimen references");
    }

    #[test]
    fn test_extract_drawable_references() {
        let source = br#"
<LinearLayout android:background="@drawable/bg_gradient">
    <ImageView android:src="@drawable/icon_logo" />
    <Button android:background="@drawable/btn_rounded" />
</LinearLayout>"#;
        let extractor = XmlLayoutExtractor::new(source.as_slice(), "res/layout/splash.xml");
        let (elements, _) = extractor.extract();

        let drawable_refs: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_resource_ref_drawable")
            .collect();
        assert_eq!(
            drawable_refs.len(),
            3,
            "Should extract 3 drawable references"
        );
    }

    #[test]
    fn test_extract_style_references() {
        let source = br#"
<LinearLayout>
    <TextView style="@style/AppTheme.TextView" />
    <Button style="@style/AppTheme.Button.Primary" />
</LinearLayout>"#;
        let extractor = XmlLayoutExtractor::new(source.as_slice(), "res/layout/main.xml");
        let (elements, relationships) = extractor.extract();

        let style_refs: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_style_reference")
            .collect();
        assert_eq!(style_refs.len(), 2, "Should extract 2 style references");

        let uses_style: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "uses_style")
            .collect();
        assert_eq!(
            uses_style.len(),
            2,
            "Should have 2 uses_style relationships"
        );
    }

    #[test]
    fn test_extract_mixed_resource_references() {
        let source = br#"<?xml version="1.0" encoding="utf-8"?>
<LinearLayout xmlns:android="http://schemas.android.com/apk/res/android"
    android:layout_width="match_parent"
    android:layout_height="match_parent"
    android:background="@color/background"
    android:padding="@dimen/activity_padding">

    <TextView
        android:id="@+id/title"
        android:text="@string/app_name"
        android:textColor="@color/text_primary"
        android:textSize="@dimen/text_large" />

    <Button
        android:id="@+id/submit"
        android:text="@string/submit"
        android:background="@drawable/btn_primary"
        style="@style/AppTheme.Button" />

</LinearLayout>"#;
        let extractor = XmlLayoutExtractor::new(source.as_slice(), "res/layout/activity_main.xml");
        let (elements, relationships) = extractor.extract();

        let resource_refs: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type.starts_with("android_resource_ref_"))
            .collect();
        assert!(
            resource_refs.len() >= 6,
            "Should extract at least 6 resource references"
        );

        let style_refs: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_style_reference")
            .collect();
        assert_eq!(style_refs.len(), 1, "Should extract 1 style reference");
    }
}
