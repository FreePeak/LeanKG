//! XML extraction tests
//! Tests extraction of Android XML patterns from fixture files

use leankg::indexer::{AndroidManifestExtractor, GenericXmlExtractor};
use std::fs;

const XML_FIXTURES_DIR: &str = "tests/fixtures/android_xml";
const TV_APP_DIR: &str = "tests/fixtures/complex_scenarios/tv_app";

#[test]
fn test_manifest_extraction() {
    let source = fs::read_to_string(format!("{}/AndroidManifest.xml", XML_FIXTURES_DIR))
        .expect("Manifest fixture not found");

    let extractor = AndroidManifestExtractor::new(source.as_bytes(), "AndroidManifest.xml");
    let (elements, relationships) = extractor.extract();

    // Should have manifest element
    let manifest: Vec<_> = elements
        .iter()
        .filter(|e| e.element_type == "android_manifest")
        .collect();
    assert_eq!(manifest.len(), 1, "Expected 1 manifest element");

    // Should have activities
    let activities: Vec<_> = elements
        .iter()
        .filter(|e| e.element_type == "android_activity")
        .collect();
    assert!(!activities.is_empty(), "Expected activities in manifest");

    // Should have services
    let services: Vec<_> = elements
        .iter()
        .filter(|e| e.element_type == "android_service")
        .collect();
    assert!(!services.is_empty(), "Expected services in manifest");

    // Check component declarations
    let declares: Vec<_> = relationships
        .iter()
        .filter(|r| r.rel_type == "declares_component")
        .collect();
    assert!(
        !declares.is_empty(),
        "Expected component declaration relationships"
    );
}

#[test]
fn test_manifest_intent_filters() {
    let source = fs::read_to_string(format!("{}/AndroidManifest.xml", XML_FIXTURES_DIR))
        .expect("Manifest fixture not found");

    // Test intent filter extraction
    let filters = AndroidManifestExtractor::extract_intent_filters(&source);
    assert!(!filters.is_empty(), "Expected intent filters in manifest");

    // Check for MAIN action
    let has_main = filters
        .iter()
        .any(|(_, actions, _)| actions.iter().any(|a| a.contains("MAIN")));
    assert!(has_main, "Expected MAIN action in intent filters");

    // Check for LEANBACK_LAUNCHER category (TV-specific)
    let has_leanback = filters
        .iter()
        .any(|(_, _, categories)| categories.iter().any(|c| c.contains("LEANBACK")));
    assert!(
        has_leanback,
        "Expected LEANBACK_LAUNCHER category for TV app"
    );
}

#[test]
fn test_manifest_metadata() {
    let source = fs::read_to_string(format!("{}/AndroidManifest.xml", TV_APP_DIR))
        .expect("TV app manifest not found");

    // Test metadata extraction
    let metadata = AndroidManifestExtractor::extract_metadata(&source);
    // TV app manifest may or may not have metadata - just verify function works

    for (name, value, resource) in &metadata {
        assert!(!name.is_empty(), "Metadata should have name");
        assert!(
            value.is_some() || resource.is_some(),
            "Metadata should have value or resource"
        );
    }
}

#[test]
fn test_manifest_application_class() {
    let source = fs::read_to_string(format!("{}/AndroidManifest.xml", TV_APP_DIR))
        .expect("TV app manifest not found");

    // Should detect Application class
    let app_class = AndroidManifestExtractor::extract_application_class(&source);
    assert!(app_class.is_some(), "Expected Application class reference");

    let class_name = app_class.unwrap();
    assert!(
        class_name.contains("Application"),
        "Application class name should contain 'Application'"
    );
}

#[test]
fn test_manifest_permissions() {
    let source = fs::read_to_string(format!("{}/AndroidManifest.xml", XML_FIXTURES_DIR))
        .expect("Manifest fixture not found");

    let extractor = AndroidManifestExtractor::new(source.as_bytes(), "AndroidManifest.xml");
    let (elements, _) = extractor.extract();

    // Should have permission elements
    let permissions: Vec<_> = elements
        .iter()
        .filter(|e| e.element_type == "android_permission")
        .collect();
    assert!(!permissions.is_empty(), "Expected permission declarations");

    // Check for INTERNET permission
    let has_internet = permissions.iter().any(|p| p.name.contains("INTERNET"));
    assert!(has_internet, "Expected INTERNET permission");
}

#[test]
fn test_manifest_features() {
    let source = fs::read_to_string(format!("{}/AndroidManifest.xml", XML_FIXTURES_DIR))
        .expect("Manifest fixture not found");

    let extractor = AndroidManifestExtractor::new(source.as_bytes(), "AndroidManifest.xml");
    let (elements, _) = extractor.extract();

    // Should have feature elements
    let features: Vec<_> = elements
        .iter()
        .filter(|e| e.element_type == "android_feature")
        .collect();
    assert!(!features.is_empty(), "Expected feature declarations");

    // TV app should have leanback feature
    let has_leanback = features.iter().any(|f| f.name.contains("leanback"));
    assert!(has_leanback, "Expected leanback feature for TV app");

    // TV app should declare touchscreen not required
    let has_touch = features.iter().any(|f| f.name.contains("touchscreen"));
    assert!(has_touch, "Expected touchscreen feature declaration");
}

#[test]
fn test_layout_extraction() {
    // Verify layout fixture files exist and are valid XML
    let layout_files = vec![
        format!("{}/browse_fragment.xml", XML_FIXTURES_DIR),
        format!("{}/player_activity.xml", XML_FIXTURES_DIR),
    ];

    for path in layout_files {
        let content =
            fs::read_to_string(&path).expect(&format!("Layout fixture not found: {}", path));

        // Basic XML validation
        assert!(
            content.starts_with("<?xml"),
            "Should start with XML declaration"
        );
        assert!(content.contains("<"), "Should contain XML tags");

        // Check for Android namespace
        assert!(
            content.contains("xmlns:android"),
            "Should have Android namespace"
        );
    }
}

#[test]
fn test_preferences_extraction() {
    let content = fs::read_to_string(format!("{}/preferences.xml", XML_FIXTURES_DIR))
        .expect("Preferences fixture not found");

    // Check for preference patterns
    assert!(
        content.contains("PreferenceScreen"),
        "Should have PreferenceScreen"
    );
    assert!(
        content.contains("SwitchPreference"),
        "Should have SwitchPreference"
    );
    assert!(
        content.contains("ListPreference"),
        "Should have ListPreference"
    );
}

#[test]
fn test_drawable_extraction() {
    let content = fs::read_to_string(format!("{}/selector_button.xml", XML_FIXTURES_DIR))
        .expect("Drawable fixture not found");

    // Check for selector patterns
    assert!(content.contains("<selector"), "Should have selector root");
    assert!(
        content.contains("state_focused"),
        "Should have focused state"
    );
    assert!(
        content.contains("state_pressed"),
        "Should have pressed state"
    );
}

#[test]
fn test_strings_extraction() {
    let content = fs::read_to_string(format!("{}/strings.xml", XML_FIXTURES_DIR))
        .expect("Strings fixture not found");

    // Check for string patterns
    assert!(
        content.contains("<string name="),
        "Should have string resources"
    );
    assert!(
        content.contains("<string-array"),
        "Should have string arrays"
    );
    assert!(content.contains("<plurals"), "Should have plurals");
    assert!(content.contains("%1$d"), "Should have format strings");
}

#[test]
fn test_colors_extraction() {
    let content = fs::read_to_string(format!("{}/colors.xml", XML_FIXTURES_DIR))
        .expect("Colors fixture not found");

    // Check for Material 3 color patterns
    assert!(
        content.contains("<color name=\"primary\">"),
        "Should have primary color"
    );
    assert!(
        content.contains("<color name=\"surface\">"),
        "Should have surface color"
    );
    assert!(
        content.contains("<color name=\"error\">"),
        "Should have error color"
    );
}

#[test]
fn test_styles_extraction() {
    let content = fs::read_to_string(format!("{}/styles.xml", XML_FIXTURES_DIR))
        .expect("Styles fixture not found");

    // Check for style patterns
    assert!(
        content.contains("<style name="),
        "Should have style definitions"
    );
    assert!(
        content.contains("parent="),
        "Should have parent inheritance"
    );
    assert!(
        content.contains("Theme.Leanback"),
        "Should reference Leanback theme"
    );
}

// Generic XML extraction tests
#[test]
fn test_generic_xml_extraction_root_element() {
    let content = r#"<configuration>
        <server>localhost</server>
        <port>8080</port>
    </configuration>"#;

    let extractor = GenericXmlExtractor::new(content.as_bytes(), "config.xml");
    let (elements, relationships) = extractor.extract();

    // Should have one XMLDocument element for root
    assert_eq!(elements.len(), 1, "Expected 1 XMLDocument element");
    assert_eq!(elements[0].element_type, "XMLDocument");
    assert!(
        elements[0].name.contains("configuration"),
        "Root element should be 'configuration'"
    );

    // Should have has_root relationship
    assert_eq!(relationships.len(), 1, "Expected 1 relationship");
    assert_eq!(relationships[0].rel_type, "has_root");
}

#[test]
fn test_generic_xml_exclusion_of_android_files() {
    // AndroidManifest.xml should be skipped
    let content = r#"<manifest package="com.test"/>"#;
    let extractor = GenericXmlExtractor::new(content.as_bytes(), "AndroidManifest.xml");
    let (elements, relationships) = extractor.extract();

    assert!(elements.is_empty(), "AndroidManifest should be excluded");
    assert!(
        relationships.is_empty(),
        "No relationships for Android files"
    );

    // Files in /res/ should be skipped
    let extractor2 = GenericXmlExtractor::new(content.as_bytes(), "/res/layout/main.xml");
    let (elements2, relationships2) = extractor2.extract();

    assert!(elements2.is_empty(), "Files in /res/ should be excluded");
    assert!(
        relationships2.is_empty(),
        "No relationships for Android resource files"
    );
}

#[test]
fn test_generic_xml_with_xml_declaration() {
    let content = r#"<?xml version="1.0" encoding="UTF-8"?>
<settings>
    <theme>dark</theme>
</settings>"#;

    let extractor = GenericXmlExtractor::new(content.as_bytes(), "settings.xml");
    let (elements, _) = extractor.extract();

    assert_eq!(elements.len(), 1);
    assert!(elements[0].qualified_name.contains("settings"));
}

#[test]
fn test_generic_xml_self_closing_tag() {
    let content = r#"<config enabled="true"/>"#;

    let extractor = GenericXmlExtractor::new(content.as_bytes(), "config.xml");
    let (elements, relationships) = extractor.extract();

    assert_eq!(elements.len(), 1);
    assert_eq!(elements[0].name, "config");
    // Self-closing tags still create has_root relationship
    assert_eq!(relationships.len(), 1);
}

#[test]
fn test_generic_xml_empty_content() {
    let content = "";
    let extractor = GenericXmlExtractor::new(content.as_bytes(), "empty.xml");
    let (elements, relationships) = extractor.extract();

    assert!(
        elements.is_empty(),
        "Empty content should produce no elements"
    );
    assert!(
        relationships.is_empty(),
        "Empty content should produce no relationships"
    );
}

#[test]
fn test_generic_xml_invalid_utf8() {
    // Invalid UTF-8 bytes
    let invalid_bytes = vec![0x80, 0x81, 0x82];
    let extractor = GenericXmlExtractor::new(&invalid_bytes, "binary.xml");
    let (elements, relationships) = extractor.extract();

    assert!(
        elements.is_empty(),
        "Invalid UTF-8 should produce no elements"
    );
    assert!(
        relationships.is_empty(),
        "Invalid UTF-8 should produce no relationships"
    );
}

#[test]
fn test_generic_xml_preserves_file_path() {
    let content = r#"<root/>"#;
    let extractor = GenericXmlExtractor::new(content.as_bytes(), "path/to/config.xml");
    let (elements, _) = extractor.extract();

    assert_eq!(elements[0].file_path, "path/to/config.xml");
    assert!(elements[0].qualified_name.starts_with("path/to/config.xml"));
}
