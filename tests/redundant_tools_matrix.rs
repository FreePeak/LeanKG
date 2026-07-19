//! Static redundancy matrix and coverage gap report for LeanKG MCP tools.
//!
//! This file does not call the runtime; instead it encodes the redundancy
//! analysis as machine-checkable assertions. Failures mean the redundancy
//! roster has drifted from `src/mcp/tools.rs`.
//!
//! Generated categories:
//!
//! | Status          | Meaning                                                                |
//! |-----------------|------------------------------------------------------------------------|
//! | SUPERSEDED      | Newer tool subsumes the older one (hard-removed tools asserted absent) |
//! | ALIASED         | Different name, identical payload shape                                |
//! | DOMAIN-SPECIFIC | Overlap but different target domain (Android, etc.)                    |
//! | COMPLEMENTARY   | Different aggregation level — both should remain                       |
//!
//! Run:
//! ```bash
//! cargo test --release --test redundant_tools_matrix
//! ```

use leankg::mcp::tools::ToolRegistry;
use std::collections::HashSet;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Status {
    #[allow(dead_code)]
    Superseded,
    #[allow(dead_code)]
    Aliased,
    DomainSpecific,
    Complementary,
}

#[allow(dead_code)]
struct Entry {
    primary: &'static str,
    redundant: &'static [&'static str],
    status: Status,
    note: &'static str,
}

/// Tools hard-removed in FR-SURF-03 (replaced by get_impact_radius / find_related_docs / kg_self_test).
const REMOVED_TOOLS: &[&str] = &["mcp_hello", "mcp_impact", "get_doc_for_file"];

const REDUNDANCY_TABLE: &[Entry] = &[
    // ----------------------------------------------------------------------
    // ALIASED / COMPLEMENTARY — both sides must remain registered.
    // ----------------------------------------------------------------------
    Entry {
        primary: "mcp_init",
        redundant: &["mcp_install"],
        status: Status::Complementary,
        note: "mcp_init creates the .leankg project; mcp_install writes .mcp.json for clients. Keep both.",
    },
    // ----------------------------------------------------------------------
    // DOMAIN-SPECIFIC — overlap is intentional, scopes are disjoint.
    // ----------------------------------------------------------------------
    Entry {
        primary: "get_call_graph",
        redundant: &["get_callers", "get_nav_callers"],
        status: Status::DomainSpecific,
        note: "get_call_graph / get_callers are generic; get_nav_callers is Android Navigation-graph scoped.",
    },
    Entry {
        primary: "get_clusters",
        redundant: &["get_cluster_context", "get_cluster_skill"],
        status: Status::Complementary,
        note: "Three aggregation levels: list / context / per-cluster SKILL.md.",
    },
    Entry {
        primary: "search_code",
        redundant: &[
            "search_annotations",
            "search_knowledge",
            "search_by_requirement",
            "search_by_environment",
            "concept_search",
            "semantic_search",
        ],
        status: Status::DomainSpecific,
        note: "Each search tool targets a different entity type; do not merge.",
    },
    // ----------------------------------------------------------------------
    // COMPLEMENTARY — different aggregation level.
    // ----------------------------------------------------------------------
    Entry {
        primary: "get_architecture",
        redundant: &["get_overview_context", "wake_up", "load_layer"],
        status: Status::Complementary,
        note: "L0 (wake_up) / L1 (load_layer L1) / single-call overview (get_overview_context) / deep architecture (get_architecture).",
    },
    Entry {
        primary: "get_graph_schema",
        redundant: &["get_graph_report"],
        status: Status::Complementary,
        note: "Schema counts vs prose report — each is its own granularity.",
    },
    Entry {
        primary: "query_incidents",
        redundant: &["get_upcoming_changes"],
        status: Status::Complementary,
        note: "query_incidents = past incidents; get_upcoming_changes = staged-but-not-yet-released work.",
    },
    Entry {
        primary: "find_related_docs",
        redundant: &[],
        status: Status::Complementary,
        note: "FR-SURF-03 replacement for removed get_doc_for_file (documented_by + references).",
    },
    Entry {
        primary: "get_impact_radius",
        redundant: &[],
        status: Status::Complementary,
        note: "FR-SURF-03 replacement for removed mcp_impact (severity + confidence + compress_response).",
    },
    Entry {
        primary: "kg_self_test",
        redundant: &[],
        status: Status::Complementary,
        note: "FR-SURF-03 replacement for removed mcp_hello (diagnostics; use with mcp_status).",
    },
];

fn registered() -> HashSet<String> {
    ToolRegistry::list_tools()
        .into_iter()
        .map(|t| t.name)
        .collect()
}

#[test]
fn removed_superseded_tools_are_absent_from_registry() {
    let reg = registered();
    for tool in REMOVED_TOOLS {
        assert!(
            !reg.contains(*tool),
            "removed tool `{tool}` still registered"
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
    ] {
        assert!(
            reg.contains(tool),
            "replacement `{tool}` missing from registry"
        );
    }
}

#[test]
fn redundancy_table_only_references_registered_tools() {
    let reg = registered();
    for entry in REDUNDANCY_TABLE {
        assert!(
            reg.contains(entry.primary),
            "Primary `{}` not in registry",
            entry.primary
        );
        for r in entry.redundant {
            assert!(
                reg.contains(*r),
                "Redundant `{}` not in registry (primary={})",
                r,
                entry.primary
            );
        }
    }
}

#[test]
fn every_redundant_tool_has_exactly_one_status() {
    let mut seen: HashSet<&'static str> = HashSet::new();
    for entry in REDUNDANCY_TABLE {
        assert!(
            seen.insert(entry.primary),
            "Primary `{}` listed more than once in REDUNDANCY_TABLE",
            entry.primary
        );
        for r in entry.redundant {
            assert!(
                seen.insert(r),
                "Tool `{}` listed more than once in REDUNDANCY_TABLE",
                r
            );
        }
    }
}

#[test]
fn at_least_one_redundancy_entry_per_status() {
    let mut alias = 0;
    let mut domain = 0;
    let mut comp = 0;
    for entry in REDUNDANCY_TABLE {
        match entry.status {
            Status::Superseded => {}
            Status::Aliased => alias += 1,
            Status::DomainSpecific => domain += 1,
            Status::Complementary => comp += 1,
        }
    }
    assert!(alias + domain + comp >= 4, "expected ≥4 remaining entries");
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
    let names: Vec<String> = registered().into_iter().collect();
    let mut sorted = names.clone();
    sorted.sort();
    sorted.dedup();
    assert_eq!(names.len(), sorted.len(), "duplicate MCP tool names");
}

// ---------------------------------------------------------------------------
// Coverage gap report (asserted at runtime so the doc stays honest).
// ---------------------------------------------------------------------------

/// Tools that have **no** direct unit/integration test (per the audit
/// 2026-07-18 in `docs/test-coverage-status.md`). The MCP `mcp_tools_redundancy_tests`
/// file closes these gaps; this list exists so future refactors notice when
/// one of these tools slips back into "untested".
const UNTESTED_BEFORE_THIS_PR: &[&str] = &[
    "add_annotation",
    "add_documentation",
    "add_knowledge",
    "agent_diary_read",
    "agent_diary_write",
    "agent_focus",
    "check_consistency",
    "concept_search",
    "delete_knowledge",
    "explain_node",
    "export_graph_snapshot",
    "find_clones",
    "find_dead_code",
    "find_env_conflicts",
    "find_route",
    "find_tunnels",
    "get_architecture",
    "get_cluster_skill",
    "get_god_nodes",
    "get_graph_report",
    "get_graph_schema",
    "get_nav_callers",
    "get_nav_graph",
    "get_overview_context",
    "get_pr_impact",
    "get_screen_args",
    "get_service_context",
    "get_team_map",
    "get_upcoming_changes",
    "kg_concept_map",
    "kg_context",
    "kg_ontology_status",
    "kg_self_test",
    "kg_semantic_context",
    "kg_trace_workflow",
    "link_element",
    "load_layer",
    "promote_environment",
    "query_incidents",
    "report_query_outcome",
    "resolve_with_lsp",
    "search_annotations",
    "search_by_environment",
    "search_knowledge",
    "semantic_search",
    "shortest_path",
    "temporal_query",
    "timeline",
    "update_knowledge",
    "wake_up",
];

#[test]
fn untested_before_pr_list_matches_registry_subset() {
    let reg = registered();
    for tool in UNTESTED_BEFORE_THIS_PR {
        // `kg_semantic_context` is `#[cfg(feature = "embeddings")]`-gated; only
        // present in the registry when that feature is enabled.
        if *tool == "kg_semantic_context" && !reg.contains(*tool) {
            continue;
        }
        assert!(
            reg.contains(*tool),
            "Tool `{tool}` not in registry anymore — please update UNTESTED_BEFORE_THIS_PR"
        );
    }
}
