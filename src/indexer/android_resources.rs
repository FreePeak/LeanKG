use crate::db::models::{CodeElement, Relationship};
use regex::Regex;

pub struct AndroidResourcesExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> AndroidResourcesExtractor<'a> {
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

        let resource_type = self.detect_resource_type(file_name);

        elements.push(CodeElement {
            qualified_name: self.file_path.to_string(),
            element_type: resource_type.to_string(),
            name: file_name.to_string(),
            file_path: self.file_path.to_string(),
            language: "android".to_string(),
            ..Default::default()
        });

        self.extract_string_resources(content, &mut elements, &mut relationships);
        self.extract_color_resources(content, &mut elements, &mut relationships);
        self.extract_dimen_resources(content, &mut elements, &mut relationships);
        self.extract_style_resources(content, &mut elements, &mut relationships);
        self.extract_theme_resources(content, &mut elements, &mut relationships);
        self.extract_bool_resources(content, &mut elements, &mut relationships);
        self.extract_integer_resources(content, &mut elements, &mut relationships);
        self.extract_array_resources(content, &mut elements, &mut relationships);

        (elements, relationships)
    }

    fn detect_resource_type(&self, file_name: &str) -> &str {
        if file_name.starts_with("strings") {
            "android_strings"
        } else if file_name.starts_with("colors") {
            "android_colors"
        } else if file_name.starts_with("dimens") {
            "android_dimens"
        } else if file_name.starts_with("styles") {
            "android_styles"
        } else if file_name.starts_with("bools") || file_name.starts_with("bool") {
            "android_bools"
        } else if file_name.starts_with("integers") || file_name.starts_with("integer") {
            "android_integers"
        } else if file_name.starts_with("arrays") || file_name.starts_with("plurals") {
            "android_arrays"
        } else {
            "android_values"
        }
    }

    fn extract_string_resources(
        &self,
        content: &str,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        let re = Regex::new(r#"<string\s+name\s*=\s*"([^"]+)"[^>]*>([^<]*)</string>"#).unwrap();
        for cap in re.captures_iter(content) {
            if let (Some(name), Some(value)) = (cap.get(1), cap.get(2)) {
                let string_name = name.as_str().to_string();
                let string_value = value.as_str().trim().to_string();
                let qualified_name = format!("{}/@string/{}", self.file_path, string_name);

                elements.push(CodeElement {
                    qualified_name: qualified_name.clone(),
                    element_type: "android_string".to_string(),
                    name: string_name.clone(),
                    file_path: self.file_path.to_string(),
                    language: "android".to_string(),
                    metadata: serde_json::json!({
                        "value": string_value,
                        "resource_type": "string",
                    }),
                    ..Default::default()
                });

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: qualified_name,
                    rel_type: "defines_string".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({
                        "value": string_value,
                    }),
                });
            }
        }
    }

    fn extract_color_resources(
        &self,
        content: &str,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        let re = Regex::new(r#"<color\s+name\s*=\s*"([^"]+)"[^>]*>([^<]*)</color>"#).unwrap();
        for cap in re.captures_iter(content) {
            if let (Some(name), Some(value)) = (cap.get(1), cap.get(2)) {
                let color_name = name.as_str().to_string();
                let color_value = value.as_str().trim().to_string();
                let qualified_name = format!("{}/@color/{}", self.file_path, color_name);

                elements.push(CodeElement {
                    qualified_name: qualified_name.clone(),
                    element_type: "android_color".to_string(),
                    name: color_name.clone(),
                    file_path: self.file_path.to_string(),
                    language: "android".to_string(),
                    metadata: serde_json::json!({
                        "value": color_value,
                        "resource_type": "color",
                    }),
                    ..Default::default()
                });

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: qualified_name,
                    rel_type: "defines_color".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({
                        "value": color_value,
                    }),
                });
            }
        }
    }

    fn extract_dimen_resources(
        &self,
        content: &str,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        let re = Regex::new(r#"<dimen\s+name\s*=\s*"([^"]+)"[^>]*>([^<]*)</dimen>"#).unwrap();
        for cap in re.captures_iter(content) {
            if let (Some(name), Some(value)) = (cap.get(1), cap.get(2)) {
                let dimen_name = name.as_str().to_string();
                let dimen_value = value.as_str().trim().to_string();
                let qualified_name = format!("{}/@dimen/{}", self.file_path, dimen_name);

                elements.push(CodeElement {
                    qualified_name: qualified_name.clone(),
                    element_type: "android_dimen".to_string(),
                    name: dimen_name.clone(),
                    file_path: self.file_path.to_string(),
                    language: "android".to_string(),
                    metadata: serde_json::json!({
                        "value": dimen_value,
                        "resource_type": "dimen",
                    }),
                    ..Default::default()
                });

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: qualified_name,
                    rel_type: "defines_dimen".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({
                        "value": dimen_value,
                    }),
                });
            }
        }
    }

    fn extract_style_resources(
        &self,
        content: &str,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        let re = Regex::new(
            r#"<style\s+name\s*=\s*"([^"]+)"(?:\s+parent\s*=\s*"([^"]*)")?[^>]*>[\s\S]*?</style>"#,
        )
        .unwrap();
        for cap in re.captures_iter(content) {
            if let Some(name) = cap.get(1) {
                let style_name = name.as_str().to_string();
                let parent = cap.get(2).map(|m| m.as_str().to_string());
                let qualified_name = format!("{}/@style/{}", self.file_path, style_name);

                elements.push(CodeElement {
                    qualified_name: qualified_name.clone(),
                    element_type: "android_style".to_string(),
                    name: style_name.clone(),
                    file_path: self.file_path.to_string(),
                    language: "android".to_string(),
                    metadata: serde_json::json!({
                        "parent": parent,
                        "resource_type": "style",
                    }),
                    ..Default::default()
                });

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: qualified_name.clone(),
                    rel_type: "defines_style".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({}),
                });

                if let Some(parent_name) = parent {
                    if !parent_name.is_empty() {
                        relationships.push(Relationship {
                            id: None,
                            source_qualified: qualified_name.clone(),
                            target_qualified: format!("@style/{}", parent_name),
                            rel_type: "inherits_from".to_string(),
                            confidence: 1.0,
                            metadata: serde_json::json!({
                                "parent": parent_name,
                            }),
                        });
                    }
                }
            }
        }
    }

    fn extract_theme_resources(
        &self,
        content: &str,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        let re = Regex::new(r#"<theme\s+name\s*=\s*"([^"]+)"[^>]*>[\s\S]*?</theme>"#).unwrap();
        for cap in re.captures_iter(content) {
            if let Some(name) = cap.get(1) {
                let theme_name = name.as_str().to_string();
                let qualified_name = format!("{}/@theme/{}", self.file_path, theme_name);

                elements.push(CodeElement {
                    qualified_name: qualified_name.clone(),
                    element_type: "android_theme".to_string(),
                    name: theme_name.clone(),
                    file_path: self.file_path.to_string(),
                    language: "android".to_string(),
                    metadata: serde_json::json!({
                        "resource_type": "theme",
                    }),
                    ..Default::default()
                });

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: qualified_name,
                    rel_type: "defines_theme".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({}),
                });
            }
        }
    }

    fn extract_bool_resources(
        &self,
        content: &str,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        let re = Regex::new(r#"<bool\s+name\s*=\s*"([^"]+)"[^>]*>([^<]*)</bool>"#).unwrap();
        for cap in re.captures_iter(content) {
            if let (Some(name), Some(value)) = (cap.get(1), cap.get(2)) {
                let bool_name = name.as_str().to_string();
                let bool_value = value.as_str().trim().to_string();
                let qualified_name = format!("{}/@bool/{}", self.file_path, bool_name);

                elements.push(CodeElement {
                    qualified_name: qualified_name.clone(),
                    element_type: "android_bool".to_string(),
                    name: bool_name.clone(),
                    file_path: self.file_path.to_string(),
                    language: "android".to_string(),
                    metadata: serde_json::json!({
                        "value": bool_value,
                        "resource_type": "bool",
                    }),
                    ..Default::default()
                });

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: qualified_name,
                    rel_type: "defines_bool".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({
                        "value": bool_value,
                    }),
                });
            }
        }
    }

    fn extract_integer_resources(
        &self,
        content: &str,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        let re = Regex::new(r#"<integer\s+name\s*=\s*"([^"]+)"[^>]*>([^<]*)</integer>"#).unwrap();
        for cap in re.captures_iter(content) {
            if let (Some(name), Some(value)) = (cap.get(1), cap.get(2)) {
                let int_name = name.as_str().to_string();
                let int_value = value.as_str().trim().to_string();
                let qualified_name = format!("{}/@integer/{}", self.file_path, int_name);

                elements.push(CodeElement {
                    qualified_name: qualified_name.clone(),
                    element_type: "android_integer".to_string(),
                    name: int_name.clone(),
                    file_path: self.file_path.to_string(),
                    language: "android".to_string(),
                    metadata: serde_json::json!({
                        "value": int_value,
                        "resource_type": "integer",
                    }),
                    ..Default::default()
                });

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: qualified_name,
                    rel_type: "defines_integer".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({
                        "value": int_value,
                    }),
                });
            }
        }
    }

    fn extract_array_resources(
        &self,
        content: &str,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        let re = Regex::new(r#"<(?:string-array|integer-array|plurals)\s+name\s*=\s*"([^"]+)"[^>]*>[\s\S]*?</(?:string-array|integer-array|plurals)>"#)
            .unwrap();
        for cap in re.captures_iter(content) {
            if let Some(name) = cap.get(1) {
                let array_name = name.as_str().to_string();
                let qualified_name = format!("{}/@array/{}", self.file_path, array_name);

                elements.push(CodeElement {
                    qualified_name: qualified_name.clone(),
                    element_type: "android_array".to_string(),
                    name: array_name.clone(),
                    file_path: self.file_path.to_string(),
                    language: "android".to_string(),
                    metadata: serde_json::json!({
                        "resource_type": "array",
                    }),
                    ..Default::default()
                });

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: qualified_name,
                    rel_type: "defines_array".to_string(),
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
    fn test_extract_string_resources() {
        let source = br#"<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="app_name">My App</string>
    <string name="greeting">Hello World!</string>
    <string name="empty_string"></string>
</resources>"#;
        let extractor = AndroidResourcesExtractor::new(source.as_slice(), "res/values/strings.xml");
        let (elements, relationships) = extractor.extract();

        let strings: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_string")
            .collect();
        assert_eq!(strings.len(), 3, "Should extract 3 string resources");
        assert_eq!(strings[0].name, "app_name");
        assert_eq!(strings[0].metadata["value"], "My App");

        let defs: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "defines_string")
            .collect();
        assert_eq!(defs.len(), 3);
    }

    #[test]
    fn test_extract_color_resources() {
        let source = br#"<?xml version="1.0" encoding="utf-8"?>
<resources>
    <color name="primary">#FF6200EE</color>
    <color name="secondary">#FF03DAC5</color>
</resources>"#;
        let extractor = AndroidResourcesExtractor::new(source.as_slice(), "res/values/colors.xml");
        let (elements, relationships) = extractor.extract();

        let colors: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_color")
            .collect();
        assert_eq!(colors.len(), 2, "Should extract 2 color resources");
        assert_eq!(colors[0].name, "primary");
        assert_eq!(colors[0].metadata["value"], "#FF6200EE");
    }

    #[test]
    fn test_extract_style_with_parent() {
        let source = br#"<?xml version="1.0" encoding="utf-8"?>
<resources>
    <style name="AppTheme" parent="Theme.MaterialComponents.Light.DarkActionBar">
        <item name="colorPrimary">@color/primary</item>
    </style>
    <style name="AppTheme.NoActionBar">
        <item name="windowActionBar">false</item>
    </style>
</resources>"#;
        let extractor = AndroidResourcesExtractor::new(source.as_slice(), "res/values/styles.xml");
        let (elements, relationships) = extractor.extract();

        let styles: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_style")
            .collect();
        assert_eq!(styles.len(), 2, "Should extract 2 style resources");
        assert_eq!(styles[0].name, "AppTheme");
        assert_eq!(
            styles[0].metadata["parent"],
            "Theme.MaterialComponents.Light.DarkActionBar"
        );

        let inherits: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "inherits_from")
            .collect();
        assert_eq!(
            inherits.len(),
            1,
            "Should extract 1 inheritance relationship"
        );
    }

    #[test]
    fn test_extract_dimen_resources() {
        let source = br#"<?xml version="1.0" encoding="utf-8"?>
<resources>
    <dimen name="padding_small">8dp</dimen>
    <dimen name="padding_medium">16dp</dimen>
    <dimen name="text_size_large">24sp</dimen>
</resources>"#;
        let extractor = AndroidResourcesExtractor::new(source.as_slice(), "res/values/dimens.xml");
        let (elements, _) = extractor.extract();

        let dimens: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_dimen")
            .collect();
        assert_eq!(dimens.len(), 3, "Should extract 3 dimen resources");
        assert_eq!(dimens[0].name, "padding_small");
        assert_eq!(dimens[0].metadata["value"], "8dp");
    }

    #[test]
    fn test_extract_bool_resources() {
        let source = br#"<?xml version="1.0" encoding="utf-8"?>
<resources>
    <bool name="is_tablet">false</bool>
    <bool name="enable_logging">true</bool>
</resources>"#;
        let extractor = AndroidResourcesExtractor::new(source.as_slice(), "res/values/bools.xml");
        let (elements, _) = extractor.extract();

        let bools: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_bool")
            .collect();
        assert_eq!(bools.len(), 2, "Should extract 2 bool resources");
    }

    #[test]
    fn test_extract_integer_resources() {
        let source = br#"<?xml version="1.0" encoding="utf-8"?>
<resources>
    <integer name="max_retries">3</integer>
    <integer name="timeout_seconds">30</integer>
</resources>"#;
        let extractor =
            AndroidResourcesExtractor::new(source.as_slice(), "res/values/integers.xml");
        let (elements, _) = extractor.extract();

        let ints: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_integer")
            .collect();
        assert_eq!(ints.len(), 2, "Should extract 2 integer resources");
    }

    #[test]
    fn test_extract_array_resources() {
        let source = br#"<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string-array name="planets">
        <item>Mercury</item>
        <item>Venus</item>
    </string-array>
    <integer-array name="scores">
        <item>100</item>
        <item>200</item>
    </integer-array>
</resources>"#;
        let extractor = AndroidResourcesExtractor::new(source.as_slice(), "res/values/arrays.xml");
        let (elements, _) = extractor.extract();

        let arrays: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_array")
            .collect();
        assert_eq!(arrays.len(), 2, "Should extract 2 array resources");
    }

    #[test]
    fn test_mixed_resources() {
        let source = br#"<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="app_name">My App</string>
    <color name="primary">#FF6200EE</color>
    <dimen name="padding">16dp</dimen>
    <bool name="is_tablet">false</bool>
    <integer name="max_retries">3</integer>
    <style name="AppTheme" parent="Theme.MaterialComponents">
        <item name="colorPrimary">@color/primary</item>
    </style>
</resources>"#;
        let extractor =
            AndroidResourcesExtractor::new(source.as_slice(), "res/values/resources.xml");
        let (elements, relationships) = extractor.extract();

        assert!(elements.len() >= 6, "Should extract at least 6 resources");
        assert!(
            relationships.len() >= 6,
            "Should extract at least 6 relationships"
        );
    }
}
