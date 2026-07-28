//! Full MCP tool classification + redundancy matrix (US-SURF audit 2026-07-20).
//!
//! Every registered tool must appear exactly once in [`TOOL_CLASSIFICATION`].
//! Overlap relationships live in [`OVERLAP_TABLE`] (documentation + keep-both).
//! Soft-deprecated tools are hard-removed in FR-SURF-07/08 (2026-07-21).
//!
//! Report: `docs/reports/mcp-tool-redundancy-impact-2026-07-20.md`
//!
//! ```bash
//! cargo test --release --test redundant_tools_matrix
//! ```

use leankg::mcp::tools::ToolRegistry;
use std::collections::HashSet;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Kind {
    /// No meaningful overlap with another MCP tool.
    KeepUnique,
    /// Overlaps another tool at a different aggregation level — keep both.
    Complementary,
    /// Same shape, different domain (e.g. Android nav) — keep both.
    DomainSpecific,
}

struct ClassEntry {
    name: &'static str,
    kind: Kind,
    #[allow(dead_code)]
    note: &'static str,
}

struct OverlapEntry {
    primary: &'static str,
    related: &'static [&'static str],
    #[allow(dead_code)]
    kind: Kind,
    #[allow(dead_code)]
    note: &'static str,
}

/// Tools hard-removed (FR-SURF-03 + find_clones + FR-SURF-07/08 hard-delete 2026-07-21).
const REMOVED_TOOLS: &[&str] = &[
    "mcp_hello",
    "mcp_impact",
    "get_doc_for_file",
    "find_clones",
    "wake_up",
    "search_by_environment",
];

/// Full inventory — must match `ToolRegistry::list_tools()` exactly (one row each).
const TOOL_CLASSIFICATION: &[ClassEntry] = &[
    // --- Ops bootstrap ---
    ClassEntry {
        name: "mcp_init",
        kind: Kind::Complementary,
        note: "Creates .leankg project; pairs with mcp_install.",
    },
    ClassEntry {
        name: "mcp_install",
        kind: Kind::Complementary,
        note: "Writes .mcp.json; pairs with mcp_init.",
    },
    ClassEntry {
        name: "mcp_index",
        kind: Kind::KeepUnique,
        note: "Index codebase.",
    },
    ClassEntry {
        name: "mcp_index_docs",
        kind: Kind::KeepUnique,
        note: "Index documentation for traceability.",
    },
    ClassEntry {
        name: "mcp_status",
        kind: Kind::KeepUnique,
        note: "Index readiness; load-bearing for agents.",
    },
    #[cfg(feature = "embeddings")]
    ClassEntry {
        name: "embed_control",
        kind: Kind::KeepUnique,
        note: "US-EMBED-05 idle-gated day-2 embed toggle.",
    },
    // --- Search / discovery ---
    ClassEntry {
        name: "concept_search",
        kind: Kind::DomainSpecific,
        note: "Prefer first for domain/concept NL queries.",
    },
    ClassEntry {
        name: "semantic_search",
        kind: Kind::DomainSpecific,
        note: "Prefer second; dual-path HNSW or ontology fallback.",
    },
    ClassEntry {
        name: "search_code",
        kind: Kind::DomainSpecific,
        note: "Prefer third; ontology-first paginated name/type search.",
    },
    ClassEntry {
        name: "search_annotations",
        kind: Kind::DomainSpecific,
        note: "Annotation-scoped search.",
    },
    ClassEntry {
        name: "search_knowledge",
        kind: Kind::DomainSpecific,
        note: "Knowledge-base keyword search.",
    },
    ClassEntry {
        name: "search_by_requirement",
        kind: Kind::DomainSpecific,
        note: "Requirement / story traceability search.",
    },
    ClassEntry {
        name: "query_file",
        kind: Kind::KeepUnique,
        note: "Find file by name/pattern.",
    },
    ClassEntry {
        name: "find_function",
        kind: Kind::KeepUnique,
        note: "Locate function definition by name.",
    },
    // --- Semantic / ontology context ---
    ClassEntry {
        name: "kg_context",
        kind: Kind::DomainSpecific,
        note: "Ontology expand without vectors; prefer after kg_semantic_context.",
    },
    #[cfg(feature = "embeddings")]
    ClassEntry {
        name: "kg_semantic_context",
        kind: Kind::DomainSpecific,
        note: "Embeddings + rerank + traverse; prefer after semantic_search.",
    },
    ClassEntry {
        name: "kg_concept_map",
        kind: Kind::KeepUnique,
        note: "Concept neighborhood map.",
    },
    ClassEntry {
        name: "kg_trace_workflow",
        kind: Kind::KeepUnique,
        note: "Ordered procedural workflow trace.",
    },
    ClassEntry {
        name: "kg_ontology_status",
        kind: Kind::KeepUnique,
        note: "Ontology coverage diagnostics.",
    },
    ClassEntry {
        name: "ontology_control",
        kind: Kind::KeepUnique,
        note: "FR-ONT-PROC-03 admin sync/status for ontology YAML.",
    },
    ClassEntry {
        name: "kg_self_test",
        kind: Kind::KeepUnique,
        note: "Ontology tool smoke; replaces removed mcp_hello.",
    },
    // --- File / review context ---
    ClassEntry {
        name: "get_context",
        kind: Kind::Complementary,
        note: "Graph-aware file context; preferred in using-leankg skill.",
    },
    ClassEntry {
        name: "ctx_read",
        kind: Kind::Complementary,
        note: "File read with compression modes; keep — different payload from get_context.",
    },
    ClassEntry {
        name: "orchestrate",
        kind: Kind::Complementary,
        note: "Intent router + cache; distinct from query_graph.",
    },
    ClassEntry {
        name: "query_graph",
        kind: Kind::Complementary,
        note: "NL scoped subgraph / connections; distinct from orchestrate.",
    },
    ClassEntry {
        name: "get_review_context",
        kind: Kind::KeepUnique,
        note: "Focused subgraph + review prompt.",
    },
    ClassEntry {
        name: "get_overview_context",
        kind: Kind::Complementary,
        note: "L0+L1 overview; session-start prefer-order entry.",
    },
    ClassEntry {
        name: "load_layer",
        kind: Kind::Complementary,
        note: "Progressive L0–L3 layers; not a full overview replacement alone.",
    },
    ClassEntry {
        name: "get_architecture",
        kind: Kind::Complementary,
        note: "Deep architecture overview.",
    },
    // --- Graph deps / impact ---
    ClassEntry {
        name: "get_dependencies",
        kind: Kind::KeepUnique,
        note: "Direct imports outbound.",
    },
    ClassEntry {
        name: "get_dependents",
        kind: Kind::KeepUnique,
        note: "Inbound dependents.",
    },
    ClassEntry {
        name: "get_impact_radius",
        kind: Kind::KeepUnique,
        note: "N-hop impact; replaces removed mcp_impact.",
    },
    ClassEntry {
        name: "detect_changes",
        kind: Kind::KeepUnique,
        note: "Working-tree vs index risk analysis.",
    },
    ClassEntry {
        name: "get_pr_impact",
        kind: Kind::KeepUnique,
        note: "PR cluster overlap / severity.",
    },
    ClassEntry {
        name: "shortest_path",
        kind: Kind::KeepUnique,
        note: "BFS shortest path between symbols.",
    },
    ClassEntry {
        name: "explain_node",
        kind: Kind::KeepUnique,
        note: "Single-node dossier.",
    },
    ClassEntry {
        name: "get_god_nodes",
        kind: Kind::Complementary,
        note: "Highest degree nodes; feeds get_graph_report.",
    },
    ClassEntry {
        name: "get_graph_schema",
        kind: Kind::Complementary,
        note: "Type/relationship counts.",
    },
    ClassEntry {
        name: "get_graph_report",
        kind: Kind::Complementary,
        note: "Prose graph report wrapping god nodes + suggestions.",
    },
    // --- Calls ---
    ClassEntry {
        name: "get_callers",
        kind: Kind::DomainSpecific,
        note: "Inbound callers (not a subset of get_call_graph).",
    },
    ClassEntry {
        name: "get_call_graph",
        kind: Kind::DomainSpecific,
        note: "Outbound bounded call chain.",
    },
    ClassEntry {
        name: "get_nav_callers",
        kind: Kind::DomainSpecific,
        note: "Android Navigation inbound.",
    },
    ClassEntry {
        name: "get_nav_graph",
        kind: Kind::DomainSpecific,
        note: "Android Navigation structure.",
    },
    ClassEntry {
        name: "find_route",
        kind: Kind::DomainSpecific,
        note: "Android route resolution.",
    },
    ClassEntry {
        name: "get_screen_args",
        kind: Kind::DomainSpecific,
        note: "Android screen argument list.",
    },
    // --- Clusters / structure ---
    ClassEntry {
        name: "get_clusters",
        kind: Kind::Complementary,
        note: "List clusters.",
    },
    ClassEntry {
        name: "get_cluster_context",
        kind: Kind::Complementary,
        note: "Cluster members + edges.",
    },
    ClassEntry {
        name: "get_cluster_skill",
        kind: Kind::Complementary,
        note: "Per-cluster SKILL.md generation.",
    },
    ClassEntry {
        name: "get_code_tree",
        kind: Kind::KeepUnique,
        note: "Codebase structure tree.",
    },
    ClassEntry {
        name: "find_dead_code",
        kind: Kind::KeepUnique,
        note: "Zero callers + no tests.",
    },
    ClassEntry {
        name: "find_large_functions",
        kind: Kind::KeepUnique,
        note: "Oversized functions by line count.",
    },
    ClassEntry {
        name: "find_tunnels",
        kind: Kind::KeepUnique,
        note: "Cross-domain tunnels.",
    },
    ClassEntry {
        name: "check_consistency",
        kind: Kind::KeepUnique,
        note: "Broken/stale relationships.",
    },
    // --- Docs / traceability ---
    ClassEntry {
        name: "get_doc_structure",
        kind: Kind::Complementary,
        note: "Doc directory list; merge with get_doc_tree pending FR-SURF-06.",
    },
    ClassEntry {
        name: "get_doc_tree",
        kind: Kind::Complementary,
        note: "Doc hierarchy tree; merge with get_doc_structure pending FR-SURF-06.",
    },
    ClassEntry {
        name: "get_files_for_doc",
        kind: Kind::KeepUnique,
        note: "Code refs in a doc file.",
    },
    ClassEntry {
        name: "find_related_docs",
        kind: Kind::KeepUnique,
        note: "Docs for a code change; replaces get_doc_for_file.",
    },
    ClassEntry {
        name: "get_traceability",
        kind: Kind::KeepUnique,
        note: "Full traceability chain.",
    },
    ClassEntry {
        name: "generate_doc",
        kind: Kind::KeepUnique,
        note: "Generate documentation for a file.",
    },
    ClassEntry {
        name: "add_documentation",
        kind: Kind::KeepUnique,
        note: "Index a single documentation file.",
    },
    ClassEntry {
        name: "get_tested_by",
        kind: Kind::KeepUnique,
        note: "Test coverage edges.",
    },
    // --- Knowledge / annotations ---
    ClassEntry {
        name: "add_knowledge",
        kind: Kind::KeepUnique,
        note: "Create knowledge entry.",
    },
    ClassEntry {
        name: "update_knowledge",
        kind: Kind::KeepUnique,
        note: "Update knowledge by ID.",
    },
    ClassEntry {
        name: "delete_knowledge",
        kind: Kind::KeepUnique,
        note: "Delete knowledge by ID.",
    },
    ClassEntry {
        name: "add_annotation",
        kind: Kind::KeepUnique,
        note: "Business-logic annotation.",
    },
    ClassEntry {
        name: "link_element",
        kind: Kind::KeepUnique,
        note: "Link element to story/feature.",
    },
    // --- Env / services / incidents ---
    ClassEntry {
        name: "get_service_graph",
        kind: Kind::KeepUnique,
        note: "Microservice call topology.",
    },
    ClassEntry {
        name: "get_service_context",
        kind: Kind::KeepUnique,
        note: "Service snapshot for an environment.",
    },
    ClassEntry {
        name: "get_team_map",
        kind: Kind::KeepUnique,
        note: "Ownership / on-call map.",
    },
    ClassEntry {
        name: "find_env_conflicts",
        kind: Kind::KeepUnique,
        note: "Cross-env mismatches.",
    },
    ClassEntry {
        name: "query_incidents",
        kind: Kind::Complementary,
        note: "Past incidents.",
    },
    ClassEntry {
        name: "get_upcoming_changes",
        kind: Kind::Complementary,
        note: "Upcoming / not-yet-promoted changes.",
    },
    ClassEntry {
        name: "promote_environment",
        kind: Kind::KeepUnique,
        note: "Promote knowledge/elements across envs.",
    },
    // --- Agent meta / temporal ---
    ClassEntry {
        name: "agent_focus",
        kind: Kind::KeepUnique,
        note: "Persona-filtered subgraph.",
    },
    ClassEntry {
        name: "agent_diary_read",
        kind: Kind::KeepUnique,
        note: "Read agent diary.",
    },
    ClassEntry {
        name: "agent_diary_write",
        kind: Kind::KeepUnique,
        note: "Append agent diary.",
    },
    ClassEntry {
        name: "report_query_outcome",
        kind: Kind::KeepUnique,
        note: "Reflection feedback for ranking.",
    },
    ClassEntry {
        name: "temporal_query",
        kind: Kind::KeepUnique,
        note: "Graph state as-of epoch.",
    },
    ClassEntry {
        name: "timeline",
        kind: Kind::KeepUnique,
        note: "Relationship evolution for an element.",
    },
    // --- Power / export ---
    ClassEntry {
        name: "run_raw_query",
        kind: Kind::KeepUnique,
        note: "Raw Datalog against CozoDB.",
    },
    ClassEntry {
        name: "export_graph_snapshot",
        kind: Kind::KeepUnique,
        note: "Portable JSON snapshot.",
    },
    ClassEntry {
        name: "resolve_with_lsp",
        kind: Kind::KeepUnique,
        note: "External LSP symbol resolve.",
    },
];

/// Documented keep-both overlaps (not a removal queue).
const OVERLAP_TABLE: &[OverlapEntry] = &[
    OverlapEntry {
        primary: "mcp_init",
        related: &["mcp_install"],
        kind: Kind::Complementary,
        note: "Project init vs client .mcp.json writer.",
    },
    OverlapEntry {
        primary: "get_call_graph",
        related: &["get_callers", "get_nav_callers"],
        kind: Kind::DomainSpecific,
        note: "Outbound vs inbound vs Android nav.",
    },
    OverlapEntry {
        primary: "get_clusters",
        related: &["get_cluster_context", "get_cluster_skill"],
        kind: Kind::Complementary,
        note: "List / context / SKILL.md levels.",
    },
    OverlapEntry {
        primary: "search_code",
        related: &[
            "concept_search",
            "semantic_search",
            "search_annotations",
            "search_knowledge",
            "search_by_requirement",
        ],
        kind: Kind::DomainSpecific,
        note: "Prefer-order: concept_search → semantic_search → search_code; others entity-scoped.",
    },
    OverlapEntry {
        primary: "get_architecture",
        related: &["get_overview_context", "load_layer"],
        kind: Kind::Complementary,
        note: "Deep arch vs overview vs progressive layers.",
    },
    OverlapEntry {
        primary: "get_graph_schema",
        related: &["get_graph_report", "get_god_nodes"],
        kind: Kind::Complementary,
        note: "Counts vs prose report vs hotspots.",
    },
    OverlapEntry {
        primary: "query_incidents",
        related: &["get_upcoming_changes"],
        kind: Kind::Complementary,
        note: "Past incidents vs staged upcoming work.",
    },
    OverlapEntry {
        primary: "get_context",
        related: &["ctx_read"],
        kind: Kind::Complementary,
        note: "Graph context vs compression-mode file read — keep both.",
    },
    OverlapEntry {
        primary: "orchestrate",
        related: &["query_graph"],
        kind: Kind::Complementary,
        note: "Intent router vs NL connection subgraph.",
    },
    OverlapEntry {
        primary: "get_doc_structure",
        related: &["get_doc_tree"],
        kind: Kind::Complementary,
        note: "Pending FR-SURF-06 merge after mega-safe pagination.",
    },
    OverlapEntry {
        primary: "kg_context",
        related: &["semantic_search"],
        kind: Kind::DomainSpecific,
        note: "Prefer-order semantic: semantic_search → kg_semantic_context (embeddings) → kg_context.",
    },
];

fn registered() -> HashSet<String> {
    ToolRegistry::list_tools()
        .into_iter()
        .map(|t| t.name)
        .collect()
}

#[test]
fn hard_removed_tools_are_absent_from_registry() {
    let reg = registered();
    for tool in REMOVED_TOOLS {
        assert!(
            !reg.contains(*tool),
            "hard-removed tool `{tool}` still registered"
        );
    }
}

#[test]
fn replacement_tools_remain_registered() {
    let reg = registered();
    for tool in [
        "get_impact_radius",
        "find_related_docs",
        "kg_self_test",
        "mcp_status",
        "get_overview_context",
    ] {
        assert!(
            reg.contains(tool),
            "replacement/load-bearing `{tool}` missing from registry"
        );
    }
    #[cfg(feature = "embeddings")]
    {
        assert!(
            reg.contains("embed_control"),
            "embed_control missing with embeddings feature"
        );
        assert!(
            reg.contains("kg_semantic_context"),
            "kg_semantic_context missing with embeddings feature"
        );
    }
}

#[test]
fn every_registered_tool_has_exactly_one_classification() {
    let reg = registered();
    let mut seen: HashSet<&str> = HashSet::new();
    for entry in TOOL_CLASSIFICATION {
        assert!(
            seen.insert(entry.name),
            "duplicate classification for `{}`",
            entry.name
        );
        assert!(
            reg.contains(entry.name),
            "classified `{}` not in registry — update TOOL_CLASSIFICATION",
            entry.name
        );
    }
    let missing: Vec<_> = reg
        .iter()
        .filter(|n| !seen.contains(n.as_str()))
        .cloned()
        .collect();
    assert!(
        missing.is_empty(),
        "registered tools missing from TOOL_CLASSIFICATION: {missing:?}"
    );
    assert_eq!(
        seen.len(),
        reg.len(),
        "classification count {} != registry {}",
        seen.len(),
        reg.len()
    );
}

#[test]
fn overlap_table_only_references_registered_tools() {
    let reg = registered();
    for entry in OVERLAP_TABLE {
        assert!(
            reg.contains(entry.primary),
            "overlap primary `{}` not in registry",
            entry.primary
        );
        for r in entry.related {
            assert!(
                reg.contains(*r),
                "overlap related `{}` not in registry (primary={})",
                r,
                entry.primary
            );
        }
    }
}

#[test]
fn registry_has_at_least_80_tools() {
    let count = registered().len();
    assert!(
        count >= 80,
        "expected ≥80 registered MCP tools, got {count} — drift?"
    );
}

#[test]
fn registry_has_no_duplicate_tool_names() {
    let names: Vec<String> = ToolRegistry::list_tools()
        .into_iter()
        .map(|t| t.name)
        .collect();
    let unique: HashSet<_> = names.iter().cloned().collect();
    assert_eq!(names.len(), unique.len(), "duplicate MCP tool names");
}
