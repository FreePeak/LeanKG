#![allow(dead_code)]
use serde::{Deserialize, Serialize};

#[allow(dead_code)]
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub enum RelationshipType {
    Imports,
    Calls,
    References,
    DocumentedBy,
    TestedBy,
    Tests,
    Contains,
    Defines,
    Implements,
    Implementations,
    Extends,
    HasMethod,
    HasProperty,
    Accesses,
    MemberOf,
    Decorates,
    Wraps,
    BelongsTo,
    MethodOverrides,
    MethodImplements,
    Queries,
    EntryPointOf,
    StepInProcess,
    ServiceCalls,
    DefinesWidget,
    ContainsChild,
    OnClickHandler,
    BindsView,
    ViewbindingProperty,
    SyntheticBinding,
    AssociatedWith,
    ReferencesClass,
    UsesString,
    UsesColor,
    UsesDimen,
    UsesDrawable,
    UsesStyle,
    Annotates,
    InflatesLayout,
    UsesViewBinding,
    DependsOnModule,
    UsesLibrary,
    NavigatesTo,
    NavAction,
    ProvidesArg,
    RequiresArg,
    DeepLink,
    Presents,
    HiltBindsInterface,
    ViewModelOwnsRepository,
    UsesDispatcher,
    WorkManagerWorksOn,
    LifecycleObserves,
    RoomDaoMethod,
    ViewbindingUsed,
}

impl RelationshipType {
    pub fn as_str(&self) -> &'static str {
        match self {
            RelationshipType::Imports => "imports",
            RelationshipType::Calls => "calls",
            RelationshipType::References => "references",
            RelationshipType::DocumentedBy => "documented_by",
            RelationshipType::TestedBy => "tested_by",
            RelationshipType::Tests => "tests",
            RelationshipType::Contains => "contains",
            RelationshipType::Defines => "defines",
            RelationshipType::Implements => "implements",
            RelationshipType::Implementations => "implementations",
            RelationshipType::Extends => "extends",
            RelationshipType::HasMethod => "has_method",
            RelationshipType::HasProperty => "has_property",
            RelationshipType::Accesses => "accesses",
            RelationshipType::MemberOf => "member_of",
            RelationshipType::Decorates => "decorates",
            RelationshipType::Wraps => "wraps",
            RelationshipType::BelongsTo => "belongs_to",
            RelationshipType::MethodOverrides => "method_overrides",
            RelationshipType::MethodImplements => "method_implements",
            RelationshipType::Queries => "queries",
            RelationshipType::EntryPointOf => "entry_point_of",
            RelationshipType::StepInProcess => "step_in_process",
            RelationshipType::ServiceCalls => "service_calls",
            RelationshipType::DefinesWidget => "defines_widget",
            RelationshipType::ContainsChild => "contains_child",
            RelationshipType::OnClickHandler => "on_click_handler",
            RelationshipType::BindsView => "binds_view",
            RelationshipType::ViewbindingProperty => "viewbinding_property",
            RelationshipType::SyntheticBinding => "synthetic_binding",
            RelationshipType::AssociatedWith => "associated_with",
            RelationshipType::ReferencesClass => "references_class",
            RelationshipType::UsesString => "uses_string",
            RelationshipType::UsesColor => "uses_color",
            RelationshipType::UsesDimen => "uses_dimen",
            RelationshipType::UsesDrawable => "uses_drawable",
            RelationshipType::UsesStyle => "uses_style",
            RelationshipType::Annotates => "annotates",
            RelationshipType::InflatesLayout => "inflates_layout",
            RelationshipType::UsesViewBinding => "uses_viewbinding",
            RelationshipType::DependsOnModule => "depends_on_module",
            RelationshipType::UsesLibrary => "uses_library",
            RelationshipType::NavigatesTo => "navigates_to",
            RelationshipType::NavAction => "nav_action",
            RelationshipType::ProvidesArg => "provides_arg",
            RelationshipType::RequiresArg => "requires_arg",
            RelationshipType::DeepLink => "deep_link",
            RelationshipType::Presents => "presents",
            RelationshipType::HiltBindsInterface => "hilt_binds_interface",
            RelationshipType::ViewModelOwnsRepository => "viewmodel_owns_repository",
            RelationshipType::UsesDispatcher => "uses_dispatcher",
            RelationshipType::WorkManagerWorksOn => "workmanager_works_on",
            RelationshipType::LifecycleObserves => "lifecycle_observes",
            RelationshipType::RoomDaoMethod => "room_dao_method",
            RelationshipType::ViewbindingUsed => "viewbinding_used",
        }
    }

    #[allow(dead_code)]
    #[allow(clippy::should_implement_trait)]
    pub fn from_str(s: &str) -> Option<Self> {
        match s {
            "imports" => Some(RelationshipType::Imports),
            "calls" => Some(RelationshipType::Calls),
            "references" => Some(RelationshipType::References),
            "documented_by" => Some(RelationshipType::DocumentedBy),
            "tested_by" => Some(RelationshipType::TestedBy),
            "tests" => Some(RelationshipType::Tests),
            "contains" => Some(RelationshipType::Contains),
            "defines" => Some(RelationshipType::Defines),
            "implements" => Some(RelationshipType::Implements),
            "implementations" => Some(RelationshipType::Implementations),
            "extends" => Some(RelationshipType::Extends),
            "has_method" => Some(RelationshipType::HasMethod),
            "has_property" => Some(RelationshipType::HasProperty),
            "accesses" => Some(RelationshipType::Accesses),
            "member_of" => Some(RelationshipType::MemberOf),
            "decorates" => Some(RelationshipType::Decorates),
            "wraps" => Some(RelationshipType::Wraps),
            "belongs_to" => Some(RelationshipType::BelongsTo),
            "method_overrides" => Some(RelationshipType::MethodOverrides),
            "method_implements" => Some(RelationshipType::MethodImplements),
            "queries" => Some(RelationshipType::Queries),
            "entry_point_of" => Some(RelationshipType::EntryPointOf),
            "step_in_process" => Some(RelationshipType::StepInProcess),
            "service_calls" => Some(RelationshipType::ServiceCalls),
            "defines_widget" => Some(RelationshipType::DefinesWidget),
            "contains_child" => Some(RelationshipType::ContainsChild),
            "on_click_handler" => Some(RelationshipType::OnClickHandler),
            "binds_view" => Some(RelationshipType::BindsView),
            "viewbinding_property" => Some(RelationshipType::ViewbindingProperty),
            "synthetic_binding" => Some(RelationshipType::SyntheticBinding),
            "associated_with" => Some(RelationshipType::AssociatedWith),
            "references_class" => Some(RelationshipType::ReferencesClass),
            "uses_string" => Some(RelationshipType::UsesString),
            "uses_color" => Some(RelationshipType::UsesColor),
            "uses_dimen" => Some(RelationshipType::UsesDimen),
            "uses_drawable" => Some(RelationshipType::UsesDrawable),
            "uses_style" => Some(RelationshipType::UsesStyle),
            "annotates" => Some(RelationshipType::Annotates),
            "inflates_layout" => Some(RelationshipType::InflatesLayout),
            "uses_viewbinding" => Some(RelationshipType::UsesViewBinding),
            "depends_on_module" => Some(RelationshipType::DependsOnModule),
            "uses_library" => Some(RelationshipType::UsesLibrary),
            "navigates_to" => Some(RelationshipType::NavigatesTo),
            "nav_action" => Some(RelationshipType::NavAction),
            "provides_arg" => Some(RelationshipType::ProvidesArg),
            "requires_arg" => Some(RelationshipType::RequiresArg),
            "deep_link" => Some(RelationshipType::DeepLink),
            "presents" => Some(RelationshipType::Presents),
            "hilt_binds_interface" => Some(RelationshipType::HiltBindsInterface),
            "viewmodel_owns_repository" => Some(RelationshipType::ViewModelOwnsRepository),
            "uses_dispatcher" => Some(RelationshipType::UsesDispatcher),
            "workmanager_works_on" => Some(RelationshipType::WorkManagerWorksOn),
            "lifecycle_observes" => Some(RelationshipType::LifecycleObserves),
            "room_dao_method" => Some(RelationshipType::RoomDaoMethod),
            "viewbinding_used" => Some(RelationshipType::ViewbindingUsed),
            _ => None,
        }
    }
}

impl std::fmt::Display for RelationshipType {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.as_str())
    }
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct CodeElement {
    pub qualified_name: String,
    pub element_type: String,
    pub name: String,
    pub file_path: String,
    pub line_start: u32,
    pub line_end: u32,
    pub language: String,
    pub parent_qualified: Option<String>,
    #[serde(default)]
    pub cluster_id: Option<String>,
    #[serde(default)]
    pub cluster_label: Option<String>,
    #[serde(default)]
    pub metadata: serde_json::Value,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Relationship {
    #[serde(skip)]
    #[allow(dead_code)]
    pub id: Option<String>,
    pub source_qualified: String,
    pub target_qualified: String,
    pub rel_type: String,
    pub confidence: f64,
    pub metadata: serde_json::Value,
}

/// Information about a dependency (import)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DependencyInfo {
    pub target_qualified: String,
    pub confidence: f64,
}

impl Relationship {
    pub fn severity(&self, depth: u32) -> &'static str {
        if depth == 1 && self.confidence >= 0.85 {
            "WILL BREAK"
        } else if depth == 1 && self.confidence >= 0.60 {
            "LIKELY AFFECTED"
        } else {
            "MAY BE AFFECTED"
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BusinessLogic {
    #[serde(skip)]
    #[allow(dead_code)]
    pub id: Option<String>,
    pub element_qualified: String,
    pub description: String,
    pub user_story_id: Option<String>,
    pub feature_id: Option<String>,
}

#[allow(dead_code)]
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BusinessLogicWithDoc {
    pub business_logic: BusinessLogic,
    pub doc_links: Vec<DocLink>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DocLink {
    pub doc_qualified: String,
    pub doc_title: String,
    pub context: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TraceabilityEntry {
    pub element_qualified: String,
    pub description: String,
    pub user_story_id: Option<String>,
    pub feature_id: Option<String>,
    pub doc_links: Vec<DocLink>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TraceabilityReport {
    pub element_qualified: String,
    pub entries: Vec<TraceabilityEntry>,
    pub count: usize,
}

#[allow(dead_code)]
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Document {
    #[serde(skip)]
    pub id: Option<String>,
    pub title: String,
    pub content: String,
    pub file_path: String,
    pub generated_from: Vec<String>,
    pub last_updated: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ContextMetric {
    pub tool_name: String,
    pub timestamp: i64,
    pub project_path: String,
    pub input_tokens: i32,
    pub output_tokens: i32,
    pub output_elements: i32,
    pub execution_time_ms: i32,
    pub baseline_tokens: i32,
    pub baseline_lines_scanned: i32,
    pub tokens_saved: i32,
    pub savings_percent: f64,
    pub correct_elements: Option<i32>,
    pub total_expected: Option<i32>,
    pub f1_score: Option<f64>,
    pub query_pattern: Option<String>,
    pub query_file: Option<String>,
    pub query_depth: Option<i32>,
    pub success: bool,
    #[serde(default)]
    pub is_deleted: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MetricsSummary {
    pub total_invocations: i64,
    pub total_tokens_saved: i64,
    pub average_savings_percent: f64,
    pub average_correctness_percent: f64,
    pub retention_days: i32,
    pub by_tool: Vec<ToolMetrics>,
    pub by_day: Vec<DailyMetrics>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ToolMetrics {
    pub tool_name: String,
    pub calls: i64,
    pub avg_savings_percent: f64,
    pub avg_correctness_percent: f64,
    pub total_saved: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DailyMetrics {
    pub date: String,
    pub calls: i64,
    pub savings: i64,
    pub correctness: f64,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_code_element_creation() {
        let elem = CodeElement {
            qualified_name: "src/main.rs::main".to_string(),
            element_type: "function".to_string(),
            name: "main".to_string(),
            file_path: "src/main.rs".to_string(),
            line_start: 1,
            line_end: 5,
            language: "rust".to_string(),
            parent_qualified: None,
            metadata: serde_json::json!({}),
            ..Default::default()
        };
        assert_eq!(elem.name, "main");
    }

    #[test]
    fn test_relationship_creation() {
        let rel = Relationship {
            id: None,
            source_qualified: "a.go".to_string(),
            target_qualified: "b.go".to_string(),
            rel_type: "imports".to_string(),
            confidence: 1.0,
            metadata: serde_json::json!({}),
        };
        assert_eq!(rel.rel_type, "imports");
        assert_eq!(rel.confidence, 1.0);
    }

    #[test]
    fn test_relationship_type_display() {
        assert_eq!(RelationshipType::Imports.as_str(), "imports");
        assert_eq!(
            RelationshipType::Implementations.as_str(),
            "implementations"
        );
        assert_eq!(format!("{}", RelationshipType::Calls), "calls");
    }

    #[test]
    fn test_relationship_type_from_str() {
        assert_eq!(
            RelationshipType::from_str("imports"),
            Some(RelationshipType::Imports)
        );
        assert_eq!(
            RelationshipType::from_str("implementations"),
            Some(RelationshipType::Implementations)
        );
        assert_eq!(RelationshipType::from_str("unknown"), None);
    }

    #[test]
    fn test_nav_relationship_types() {
        assert_eq!(RelationshipType::NavigatesTo.as_str(), "navigates_to");
        assert_eq!(RelationshipType::NavAction.as_str(), "nav_action");
        assert_eq!(RelationshipType::ProvidesArg.as_str(), "provides_arg");
        assert_eq!(RelationshipType::RequiresArg.as_str(), "requires_arg");
        assert_eq!(RelationshipType::DeepLink.as_str(), "deep_link");
        assert_eq!(RelationshipType::Presents.as_str(), "presents");
        assert_eq!(
            RelationshipType::from_str("navigates_to"),
            Some(RelationshipType::NavigatesTo)
        );
        assert_eq!(
            RelationshipType::from_str("presents"),
            Some(RelationshipType::Presents)
        );
    }
}
