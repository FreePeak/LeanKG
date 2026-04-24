//! Cached compiled regex patterns for performance optimization
//!
//! Regex patterns are compiled once and reused across all indexing operations.

use once_cell::sync::Lazy;
use regex::Regex;

// ============================================================================
// Android-related patterns (used in extractor.rs)
// ============================================================================

/// Kotlin synthetic import: `import kotlin.android.synthetic.<layout>.*`
pub static KOTLIN_SYNTHETIC_IMPORT: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"import\s+kotlin\.android\.synthetic\.(\w+)\.\*"#).unwrap());

/// ViewBinding variable declaration: `val/var <Name>Binding = ...`
pub static VIEWBINDING_VAR: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"(\w+Binding)\s+(\w+)\s*="#).unwrap());

/// Property access pattern: `<obj>.<property>`
pub static PROPERTY_ACCESS: Lazy<Regex> = Lazy::new(|| Regex::new(r#"(\w+)\.(\w+)"#).unwrap());

// ============================================================================
// Microservice patterns (used in microservice.rs)
// ============================================================================

/// gRPC client pattern for Kubernetes DNS
pub static GRPC_CLIENT: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"(?m)grpc\.NewClient\s*\(\s*"([^"]+)"[,\s]""#).unwrap());

/// YAML address pattern: `be_<service>_address = ...`
pub static YAML_ADDRESS: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"be_(\w+)_address\s*[=:]\s*["']([^"']+)["']"#).unwrap());

// ============================================================================
// Config file patterns (used in config_extractor.rs)
// ============================================================================

/// Comment lines in config files
pub static CONFIG_COMMENT: Lazy<Regex> = Lazy::new(|| Regex::new(r"(?m)^\s*//.*$").unwrap());

/// Gradle/Cargo dependency section header
pub static DEPENDENCY_SECTION: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"^\[(.*dependencies.*)\]").unwrap());

/// Gradle/Cargo dependency line: `name = version`
pub static DEPENDENCY_LINE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"^([a-zA-Z0-9_\-]+)\s*=\s*(.*)"#).unwrap());

// ============================================================================
// Go module patterns (used in config_extractor.rs)
// ============================================================================

/// Go require block single line
pub static GO_REQUIRE_SINGLE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"^\s*require\s+([^\s]+)\s+(v[^\s]+)").unwrap());

/// Go require block start
pub static GO_REQUIRE_BLOCK_START: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"^\s*require\s*\(\s*$").unwrap());

/// Go require block end
pub static GO_REQUIRE_BLOCK_END: Lazy<Regex> = Lazy::new(|| Regex::new(r"^\s*\)\s*$").unwrap());

/// Go require block dependency line
pub static GO_REQUIRE_LINE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"^\s*([^\s]+)\s+(v[^\s]+)").unwrap());

// ============================================================================
// Terraform patterns (used in terraform.rs)
// ============================================================================

pub static TF_RESOURCE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"(?m)^resource\s+"([^"]+)"\s+"([^"]+)""#).unwrap());

pub static TF_DATA: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"(?m)^data\s+"([^"]+)"\s+"([^"]+)""#).unwrap());

pub static TF_VARIABLE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"(?m)^variable\s+"([^"]+)""#).unwrap());

pub static TF_OUTPUT: Lazy<Regex> = Lazy::new(|| Regex::new(r#"(?m)^output\s+"([^"]+)""#).unwrap());

pub static TF_MODULE: Lazy<Regex> = Lazy::new(|| Regex::new(r#"(?m)^module\s+"([^"]+)""#).unwrap());

pub static TF_PROVIDER: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"(?m)^provider\s+"([^"]+)""#).unwrap());

// ============================================================================
// Android manifest patterns (used in android_manifest.rs)
// ============================================================================

// Note: ANDROID_MANIFEST_TAG uses backreference \1 which is not supported
// by the regex crate - it must be created dynamically in android_manifest.rs

pub static ANDROID_NAME_ATTR: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"android:name\s*=\s*["']([^"']+)["']"#).unwrap());

// ============================================================================
// XML layout patterns (used in xml_layout.rs)
// ============================================================================

/// XML element pattern (self-closing or with content)
pub static XML_ELEMENT: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"<([a-zA-Z][a-zA-Z0-9_.]*)\s[^>]*>|</([a-zA-Z][a-zA-Z0-9_.]*)\s*>").unwrap()
});

/// Android ID reference: `@+id/<name>` or `@id/<name>`
pub static ANDROID_ID: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"android:id\s*=\s*["']@\+id/([^"']+)["']"#).unwrap());

/// Android onClick handler
pub static ANDROID_ONCLICK: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"android:onClick\s*=\s*["']([^"']+)["']"#).unwrap());

/// View ID extraction: `@+id/<name>`
pub static VIEW_ID_PLUS: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"@\+id/([a-zA-Z_][a-zA-Z0-9_]*)").unwrap());

/// View ID reference: `@id/<name>`
pub static VIEW_ID: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"@id/([a-zA-Z_][a-zA-Z0-9_]*)").unwrap());

/// Tools context attribute
pub static TOOLS_CONTEXT: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"tools:context\s*=\s*["']([^"']+)["']"#).unwrap());

/// Class name in layout: `android:name = "<package>.<Class>"`
pub static LAYOUT_CLASS: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"android:name\s*=\s*["']([^"']+\.)([^"']+)["']"#).unwrap());

/// Style reference
pub static STYLE_REF: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"style\s*=\s*["']@style/([^"']+)["']"#).unwrap());

// ============================================================================
// Android resources patterns (used in android_resources.rs)
// ============================================================================

pub static STRING_RESOURCE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"<string\s+name\s*=\s*"([^"]+)"[^>]*>([^<]*)</string>"#).unwrap());

pub static COLOR_RESOURCE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"<color\s+name\s*=\s*"([^"]+)"[^>]*>([^<]*)</color>"#).unwrap());

pub static DIMEN_RESOURCE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"<dimen\s+name\s*=\s*"([^"]+)"[^>]*>([^<]*)</dimen>"#).unwrap());

pub static THEME_RESOURCE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"<theme\s+name\s*=\s*"([^"]+)"[^>]*>[\s\S]*?</theme>"#).unwrap());

pub static BOOL_RESOURCE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"<bool\s+name\s*=\s*"([^"]+)"[^>]*>([^<]*)</bool>"#).unwrap());

pub static INTEGER_RESOURCE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"<integer\s+name\s*=\s*"([^"]+)"[^>]*>([^<]*)</integer>"#).unwrap());

// ============================================================================
// Maven patterns (used in maven_extractor.rs)
// ============================================================================

pub static MAVEN_DEPENDENCY: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"<dependency>([\s\S]*?)</dependency>").unwrap());

// ============================================================================
// Build system patterns (used in mod.rs)
// ============================================================================

/// Gradle include statement: `include("<module>")`
pub static GRADLE_INCLUDE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"include\(["']([^"']+)["']\)"#).unwrap());

/// Maven module declaration: `<module>name</module>`
pub static MAVEN_MODULE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"<module>([^<]+)</module>").unwrap());
