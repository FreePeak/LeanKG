use crate::db::models::{CodeElement, Relationship};
use regex::Regex;

pub struct XmlLayoutExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

struct WidgetParseResult {
    elements: Vec<CodeElement>,
    relationships: Vec<Relationship>,
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
            let activity_qualified = activity.to_string();

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

        let widgets = Self::extract_widgets(content, self.file_path);
        let mut widget_elements: Vec<CodeElement> = widgets.elements;
        let mut widget_relationships: Vec<Relationship> = widgets.relationships;

        elements.append(&mut widget_elements);
        relationships.append(&mut widget_relationships);

        let on_click_rels = Self::extract_on_click_handlers(content, self.file_path);
        relationships.extend(on_click_rels);

        (elements, relationships)
    }

    fn extract_widgets(content: &str, file_path: &str) -> WidgetParseResult {
        let mut elements = Vec::new();
        let mut relationships = Vec::new();
        let mut widget_index: usize = 0;
        let mut parent_stack: Vec<(String, String)> = Vec::new();

        let widget_re =
            Regex::new(r"<([a-zA-Z][a-zA-Z0-9_.]*)\s[^>]*>|</([a-zA-Z][a-zA-Z0-9_.]*)\s*>")
                .unwrap();
        let id_re = Regex::new(r#"android:id\s*=\s*["']@\+id/([^"']+)["']"#).unwrap();
        let on_click_re = Regex::new(r#"android:onClick\s*=\s*["']([^"']+)["']"#).unwrap();

        let mut processed_ids: std::collections::HashSet<String> = std::collections::HashSet::new();

        for cap in widget_re.captures_iter(content) {
            if let (Some(tag_match), None) = (cap.get(1), cap.get(2)) {
                let tag_name = tag_match.as_str();

                if tag_name.starts_with("xmlns") || tag_name.starts_with("tools:") {
                    continue;
                }

                let full_match = cap.get(0).unwrap();
                let is_self_closing = full_match.as_str().trim().ends_with("/>");
                let match_str = full_match.as_str();

                let view_id = id_re
                    .captures(match_str)
                    .and_then(|c| c.get(1))
                    .map(|m| m.as_str().to_string());

                if let Some(ref id) = view_id {
                    let key = format!("{}:{}", tag_name, id);
                    if processed_ids.contains(&key) {
                        continue;
                    }
                    processed_ids.insert(key);
                }

                let widget_type = Self::normalize_widget_type(tag_name);
                let on_click = on_click_re
                    .captures(match_str)
                    .and_then(|c| c.get(1))
                    .map(|m| m.as_str().to_string());

                widget_index += 1;
                let qualified_name = if let Some(ref id) = view_id {
                    format!("{}/@+id/{}", file_path, id)
                } else {
                    format!("{}/widget[{}]", file_path, widget_index)
                };

                let parent_qualified = parent_stack.last().cloned();

                let mut metadata = serde_json::json!({
                    "widget_type": widget_type.0,
                    "widget_package": widget_type.1,
                    "tag_name": tag_name,
                });

                if let Some(ref id) = view_id {
                    metadata["view_id"] = serde_json::json!(id);
                }
                if let Some(ref handler) = on_click {
                    metadata["on_click_handler"] = serde_json::json!(handler);
                }

                elements.push(CodeElement {
                    qualified_name: qualified_name.clone(),
                    element_type: "android_widget".to_string(),
                    name: view_id
                        .clone()
                        .unwrap_or_else(|| format!("{}_{}", widget_type.0, widget_index)),
                    file_path: file_path.to_string(),
                    language: "android".to_string(),
                    parent_qualified: parent_qualified.map(|(q, _)| q),
                    metadata,
                    ..Default::default()
                });

                relationships.push(Relationship {
                    id: None,
                    source_qualified: file_path.to_string(),
                    target_qualified: qualified_name.clone(),
                    rel_type: "defines_widget".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({
                        "widget_type": widget_type.0,
                    }),
                });

                if !is_self_closing {
                    parent_stack.push((qualified_name.clone(), tag_name.to_string()));
                }
            } else if let (None, Some(closing_tag)) = (cap.get(1), cap.get(2)) {
                let tag_name = closing_tag.as_str();
                if tag_name.starts_with("xmlns") || tag_name.starts_with("tools:") {
                    continue;
                }
                if let Some((_, stack_tag)) = parent_stack.last() {
                    if *stack_tag == tag_name {
                        parent_stack.pop();
                    }
                }
            }
        }

        WidgetParseResult {
            elements,
            relationships,
        }
    }

    fn normalize_widget_type(tag: &str) -> (String, String) {
        let common_widgets: std::collections::HashMap<&str, (&str, &str)> =
            std::collections::HashMap::from([
                ("Button", ("Button", "android.widget")),
                ("ImageButton", ("ImageButton", "android.widget")),
                ("TextView", ("TextView", "android.widget")),
                ("EditText", ("EditText", "android.widget")),
                ("CheckBox", ("CheckBox", "android.widget")),
                ("RadioButton", ("RadioButton", "android.widget")),
                ("ToggleButton", ("ToggleButton", "android.widget")),
                ("Switch", ("Switch", "android.widget")),
                ("SeekBar", ("SeekBar", "android.widget")),
                ("ProgressBar", ("ProgressBar", "android.widget")),
                ("RatingBar", ("RatingBar", "android.widget")),
                ("LinearLayout", ("LinearLayout", "android.widget")),
                ("RelativeLayout", ("RelativeLayout", "android.widget")),
                ("FrameLayout", ("FrameLayout", "android.widget")),
                ("TableLayout", ("TableLayout", "android.widget")),
                ("GridLayout", ("GridLayout", "android.widget")),
                (
                    "ConstraintLayout",
                    ("ConstraintLayout", "androidx.constraintlayout.widget"),
                ),
                (
                    "RecyclerView",
                    ("RecyclerView", "androidx.recyclerview.widget"),
                ),
                ("ListView", ("ListView", "android.widget")),
                ("GridView", ("GridView", "android.widget")),
                ("Spinner", ("Spinner", "android.widget")),
                ("ScrollView", ("ScrollView", "android.widget")),
                (
                    "HorizontalScrollView",
                    ("HorizontalScrollView", "android.widget"),
                ),
                ("WebView", ("WebView", "android.webkit")),
                ("VideoView", ("VideoView", "android.widget")),
                ("ImageView", ("ImageView", "android.widget")),
                ("SurfaceView", ("SurfaceView", "android.view")),
                ("View", ("View", "android.view")),
                ("ViewGroup", ("ViewGroup", "android.view")),
                ("ActionBar", ("ActionBar", "android.app")),
                ("Toolbar", ("Toolbar", "androidx.appcompat.widget")),
                (
                    "TabLayout",
                    ("TabLayout", "com.google.android.material.tabs"),
                ),
                (
                    "BottomNavigationView",
                    (
                        "BottomNavigationView",
                        "com.google.android.material.bottomnavigation",
                    ),
                ),
                (
                    "NavigationView",
                    ("NavigationView", "com.google.android.material.navigation"),
                ),
                (
                    "DrawerLayout",
                    ("DrawerLayout", "androidx.drawerlayout.widget"),
                ),
                (
                    "CoordinatorLayout",
                    ("CoordinatorLayout", "androidx.coordinatorlayout.widget"),
                ),
                (
                    "FloatingActionButton",
                    (
                        "FloatingActionButton",
                        "com.google.android.material.floatingactionbutton",
                    ),
                ),
                (
                    "TextInputLayout",
                    ("TextInputLayout", "com.google.android.material.textfield"),
                ),
                ("CardView", ("CardView", "com.google.android.material.card")),
                ("Chip", ("Chip", "com.google.android.material.chip")),
                (
                    "SwipeRefreshLayout",
                    ("SwipeRefreshLayout", "androidx.swiperefreshlayout.widget"),
                ),
                (
                    "NestedScrollView",
                    ("NestedScrollView", "androidx.core.widget"),
                ),
                ("SlidingDrawer", ("SlidingDrawer", "android.widget")),
                ("TabHost", ("TabHost", "android.widget")),
                (
                    "AutoCompleteTextView",
                    ("AutoCompleteTextView", "android.widget"),
                ),
                (
                    "MultiAutoCompleteTextView",
                    ("MultiAutoCompleteTextView", "android.widget"),
                ),
                ("CheckedTextView", ("CheckedTextView", "android.widget")),
                ("ZoomButton", ("ZoomButton", "android.widget")),
                ("ZoomControls", ("ZoomControls", "android.widget")),
                ("Chronometer", ("Chronometer", "android.widget")),
                ("DigitalClock", ("DigitalClock", "android.widget")),
                ("AnalogClock", ("AnalogClock", "android.widget")),
                ("ViewFlipper", ("ViewFlipper", "android.widget")),
                ("ViewAnimator", ("ViewAnimator", "android.widget")),
                ("ViewSwitcher", ("ViewSwitcher", "android.widget")),
                ("ImageSwitcher", ("ImageSwitcher", "android.widget")),
                ("TextSwitcher", ("TextSwitcher", "android.widget")),
                (
                    "AdapterViewFlipper",
                    ("AdapterViewFlipper", "android.widget"),
                ),
                ("StackView", ("StackView", "android.widget")),
                ("ViewStub", ("ViewStub", "android.view")),
                ("Menu", ("Menu", "android.view")),
                ("MenuItem", ("MenuItem", "android.view")),
                ("PopupWindow", ("PopupWindow", "android.widget")),
                ("Toast", ("Toast", "android.widget")),
                ("AlertDialog", ("AlertDialog", "android.app")),
                ("DatePicker", ("DatePicker", "android.widget")),
                ("TimePicker", ("TimePicker", "android.widget")),
                ("NumberPicker", ("NumberPicker", "android.widget")),
                ("CalendarView", ("CalendarView", "android.widget")),
                ("ProgressDialog", ("ProgressDialog", "android.app")),
            ]);

        if let Some((name, pkg)) = common_widgets.get(tag) {
            return (name.to_string(), pkg.to_string());
        }

        if tag.contains('.') {
            let parts: Vec<&str> = tag.split('.').collect();
            let name = *parts.last().unwrap_or(&tag);
            let pkg = parts[..parts.len() - 1].join(".");
            return (name.to_string(), pkg);
        }

        ("View".to_string(), "android.view".to_string())
    }

    fn extract_on_click_handlers(content: &str, file_path: &str) -> Vec<Relationship> {
        let mut relationships = Vec::new();
        let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();

        let re = Regex::new(r#"android:onClick\s*=\s*["']([^"']+)["']"#).unwrap();
        let id_re = Regex::new(r#"android:id\s*=\s*["']@\+id/([^"']+)["']"#).unwrap();

        let element_re = Regex::new(r"<[a-zA-Z][^>]*>").unwrap();

        for cap in element_re.captures_iter(content) {
            let full_elem = cap.get(0).unwrap().as_str();

            if let Some(on_click_match) = re.captures(full_elem) {
                if let Some(handler) = on_click_match.get(1) {
                    let handler_name = handler.as_str();
                    let key = format!("{}:{}", file_path, handler_name);
                    if seen.contains(&key) {
                        continue;
                    }
                    seen.insert(key);

                    let view_id = id_re
                        .captures(full_elem)
                        .and_then(|c| c.get(1))
                        .map(|m| m.as_str());

                    let source = if let Some(id) = view_id {
                        format!("{}/@+id/{}", file_path, id)
                    } else {
                        file_path.to_string()
                    };

                    relationships.push(Relationship {
                        id: None,
                        source_qualified: source,
                        target_qualified: handler_name.to_string(),
                        rel_type: "on_click_handler".to_string(),
                        confidence: 0.9,
                        metadata: serde_json::json!({
                            "handler_name": handler_name,
                        }),
                    });
                }
            }
        }

        relationships
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

    #[test]
    fn test_extract_widgets() {
        let source = br#"
<LinearLayout>
    <Button android:id="@+id/submit_button" />
    <ImageButton android:id="@+id/icon_button" />
    <TextView android:id="@+id/title" />
    <RecyclerView android:id="@+id/list_view" />
</LinearLayout>"#;
        let extractor = XmlLayoutExtractor::new(source.as_slice(), "res/layout/activity_main.xml");
        let (elements, relationships) = extractor.extract();

        let widgets: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_widget")
            .collect();
        assert_eq!(widgets.len(), 4, "Should extract 4 widgets");

        let button = widgets.iter().find(|w| w.name == "submit_button").unwrap();
        assert_eq!(button.metadata["widget_type"], "Button");

        let imagebutton = widgets.iter().find(|w| w.name == "icon_button").unwrap();
        assert_eq!(imagebutton.metadata["widget_type"], "ImageButton");

        let defines_widget: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "defines_widget")
            .collect();
        assert_eq!(
            defines_widget.len(),
            4,
            "Should have 4 defines_widget relationships"
        );
    }

    #[test]
    fn test_extract_widgets_with_on_click() {
        let source = br#"
<LinearLayout>
    <Button
        android:id="@+id/submit_button"
        android:onClick="onSubmitClicked" />
    <Button
        android:id="@+id/cancel_button"
        android:onClick="onCancelClicked" />
</LinearLayout>"#;
        let extractor = XmlLayoutExtractor::new(source.as_slice(), "res/layout/activity_main.xml");
        let (elements, relationships) = extractor.extract();

        let on_click_rels: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "on_click_handler")
            .collect();
        assert_eq!(on_click_rels.len(), 2, "Should have 2 onClick handlers");

        let submit_handler = on_click_rels
            .iter()
            .find(|r| r.source_qualified.contains("submit_button"))
            .unwrap();
        assert_eq!(submit_handler.target_qualified, "onSubmitClicked");
    }

    #[test]
    fn test_extract_nested_widgets() {
        let source = br#"
<LinearLayout android:id="@+id/parent_layout">
    <LinearLayout android:id="@+id/nested_layout">
        <Button android:id="@+id/nested_button" />
    </LinearLayout>
</LinearLayout>"#;
        let extractor = XmlLayoutExtractor::new(source.as_slice(), "res/layout/activity_main.xml");
        let (elements, relationships) = extractor.extract();

        let widgets: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_widget")
            .collect();
        assert_eq!(widgets.len(), 3, "Should extract 3 widgets");
    }

    #[test]
    fn test_extract_constraint_layout_widgets() {
        let source = br#"<?xml version="1.0" encoding="utf-8"?>
<androidx.constraintlayout.widget.ConstraintLayout
    xmlns:android="http://schemas.android.com/apk/res/android"
    android:layout_width="match_parent"
    android:layout_height="match_parent">
    <Button
        android:id="@+id/btn_confirm"
        android:layout_width="wrap_content"
        android:layout_height="wrap_content"
        android:text="Confirm" />
    <ImageButton
        android:id="@+id/btn_icon"
        android:layout_width="48dp"
        android:layout_height="48dp" />
</androidx.constraintlayout.widget.ConstraintLayout>"#;
        let extractor = XmlLayoutExtractor::new(source.as_slice(), "res/layout/main.xml");
        let (elements, relationships) = extractor.extract();

        let widgets: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "android_widget")
            .collect();
        assert_eq!(
            widgets.len(),
            3,
            "Should extract 3 widgets (ConstraintLayout + Button + ImageButton)"
        );

        let button = widgets.iter().find(|w| w.name == "btn_confirm").unwrap();
        assert_eq!(button.metadata["widget_type"], "Button");

        let imagebutton = widgets.iter().find(|w| w.name == "btn_icon").unwrap();
        assert_eq!(imagebutton.metadata["widget_type"], "ImageButton");
    }
}
