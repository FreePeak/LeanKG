use serde_json::json;
use serde_json::Value;

pub struct ToolRegistry;

impl ToolRegistry {
    /// Hard one-tool cutover (FR-ZCP-03 end-state): exactly one registered
    /// MCP tool. Every capability (all former tool names) rides the envelope
    /// as an optional `verb` argument; omitting `verb` uses the
    /// natural-language router. Verb dispatch is enforced by
    /// `resolve_envelope` at the server boundary.
    pub fn list_tools() -> Vec<ToolDefinition> {
        vec![ToolDefinition {
            name: "leankg_context".to_string(),
            description: "Tier: core. The one LeanKG tool. Ask any question about the indexed codebase — intent is auto-classified (semantic | lexical | impact | graph | files) and served by a capability ladder: L3 vector (ANN + rerank), L2 keyword (trigram fuzzy + ontology), L1 exact (identifier/regex + did-you-mean), L0 cold (guidance + background index). Degrades ranking, never availability; every response carries retrieval {rung, reason, freshness}. Direct capability access: pass `verb` with any former tool name (e.g. \"get_impact_radius\", \"query_graph\", \"mcp_status\") plus its usual arguments; omit `verb` for natural-language routing.".to_string(),
            input_schema: json!({
                "type": "object",
                "properties": {
                    "query": {"type": "string", "description": "Natural-language question, identifier, path, or graph/impact question (router path; omit when using verb)"},
                    "verb": {"type": "string", "description": "Optional direct capability (any former LeanKG tool name, e.g. get_impact_radius, query_graph, search_code, mcp_status); remaining arguments are passed to that capability unchanged"},
                    "intent": {"type": "string", "enum": ["semantic", "lexical", "impact", "graph", "files"], "description": "Optional explicit intent; defaults to auto-classification from the query shape (router path only)"},
                    "limit": {"type": "integer", "default": 20, "description": "Max results (1-50) for the router path"},
                    "full": {"type": "boolean", "default": false, "description": "Router path: file/impact intents return full compressed context instead of a page"},
                    "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                },
                "required": []
            }),
        }]
    }
}

/// FR-ZCP-12 T1: nearest registered tool name for an unknown-tool error —
/// case-insensitive longest-common-substring so `search_cod` suggests
/// `search_code` and `LEAN` suggests `leankg_context`. Falls back to the
/// default router (`leankg_context`) when nothing shares a token.
pub fn nearest_tool_name(unknown: &str) -> String {
    let unknown_lower = unknown.to_lowercase();
    let mut best: Option<(String, usize)> = None;
    for tool in ToolRegistry::list_tools() {
        let name_lower = tool.name.to_lowercase();
        let score = longest_common_substring_len(&unknown_lower, &name_lower);
        if best
            .as_ref()
            .is_none_or(|(_, best_score)| score > *best_score)
        {
            best = Some((tool.name.clone(), score));
        }
    }
    match best {
        Some((name, score)) if score >= 3 => name,
        _ => "leankg_context".to_string(),
    }
}

/// The one registered MCP tool (hard one-tool cutover, FR-ZCP-03 end-state).
pub const ONE_TOOL: &str = "leankg_context";

/// The capability (verb) catalog: every former tool name is now a verb on
/// `leankg_context`. The verb namespace IS the legacy tool namespace, so
/// existing docs/hints that name a tool remain valid as verb references.
pub fn verb_catalog() -> Vec<&'static str> {
    #[allow(unused_mut)] // mut only used under the `embeddings` feature
    let mut verbs: Vec<&'static str> = vec![
        "mcp_init",
        "mcp_index",
        "mcp_index_docs",
        "mcp_install",
        "mcp_status",
        "detect_changes",
        "get_dependencies",
        "get_dependents",
        "get_impact_radius",
        "get_review_context",
        "get_context",
        "ctx_read",
        "explain_node",
        "get_god_nodes",
        "temporal_query",
        "check_consistency",
        "timeline",
        "find_tunnels",
        "resolve_with_lsp",
        "get_cluster_skill",
        "agent_focus",
        "agent_diary_write",
        "agent_diary_read",
        "report_query_outcome",
        "get_team_map",
        "get_overview_context",
        "get_pr_impact",
        "export_graph_snapshot",
        "export_html",
        "query_graph",
        "shortest_path",
        "get_call_graph",
        "search_code",
        "concept_search",
        "semantic_search",
        "generate_doc",
        "find_large_functions",
        "get_tested_by",
        "get_files_for_doc",
        "get_traceability",
        "get_doc_tree",
        "get_code_tree",
        "find_related_docs",
        "get_clusters",
        "run_raw_query",
        "get_service_graph",
        "get_nav_graph",
        "find_route",
        "get_screen_args",
        "get_nav_callers",
        "add_knowledge",
        "update_knowledge",
        "delete_knowledge",
        "search_knowledge",
        "add_annotation",
        "link_element",
        "add_documentation",
        "add_ontology_concept",
        "add_ontology_workflow",
        "delete_ontology_concept",
        "get_upcoming_changes",
        "promote_environment",
        "query_incidents",
        "find_env_conflicts",
        "get_service_context",
        "kg_context",
        "kg_trace_workflow",
        "kg_ontology_status",
        "get_architecture",
        "index_prd",
        "get_feature_flow",
        "get_traceability_matrix",
        "embed_control",
        "ontology_control",
    ];
    #[cfg(feature = "embeddings")]
    {
        verbs.push("kg_semantic_context");
        verbs.push("set_embed_model");
    }
    verbs
}

/// True when `verb` is a dispatchable capability on the one tool.
pub fn is_valid_verb(verb: &str) -> bool {
    verb == ONE_TOOL || verb_catalog().contains(&verb)
}

pub fn resolve_envelope(
    tool_name: &str,
    arguments: &serde_json::Map<String, serde_json::Value>,
) -> Result<(String, serde_json::Map<String, serde_json::Value>), String> {
    if tool_name != ONE_TOOL {
        let nearest = nearest_verb_name(tool_name);
        return Err(crate::errors::render(
            crate::errors::UNKNOWN_TOOL.code,
            &format!(
                "tool '{tool_name}' is not registered — LeanKG exposes exactly one tool \
                 (`{ONE_TOOL}`) and every capability rides it as a verb"
            ),
            &format!(
                "call `{ONE_TOOL}` with {{\"verb\": \"{nearest}\", ...args}}; \
                 or omit `verb` entirely for the natural-language router"
            ),
        ));
    }
    let Some(verb) = arguments
        .get("verb")
        .and_then(|v| v.as_str())
        .filter(|v| !v.is_empty())
    else {
        return Ok((ONE_TOOL.to_string(), arguments.clone()));
    };
    if !is_valid_verb(verb) {
        let nearest = nearest_verb_name(verb);
        return Err(crate::errors::render(
            crate::errors::UNKNOWN_TOOL.code,
            &format!("unknown verb '{verb}' — not a LeanKG capability"),
            &format!(
                "retry with {{\"verb\": \"{nearest}\"}} (nearest capability) or drop \
                 `verb` entirely to use the natural-language router"
            ),
        ));
    }
    let mut inner = arguments.clone();
    inner.remove("verb");
    Ok((verb.to_string(), inner))
}

/// Nearest verb for an unknown capability/tool name — same LCS heuristic as
/// `nearest_tool_name`, over the verb catalog.
pub fn nearest_verb_name(unknown: &str) -> String {
    let unknown_lower = unknown.to_lowercase();
    let mut best: Option<(String, usize)> = None;
    for verb in verb_catalog() {
        let score = longest_common_substring_len(&unknown_lower, &verb.to_lowercase());
        if best.as_ref().is_none_or(|(_, b)| score > *b) {
            best = Some((verb.to_string(), score));
        }
    }
    match best {
        Some((name, score)) if score >= 3 => name,
        _ => ONE_TOOL.to_string(),
    }
}

fn longest_common_substring_len(a: &str, b: &str) -> usize {
    let a: Vec<char> = a.chars().collect();
    let b: Vec<char> = b.chars().collect();
    let mut best = 0;
    let mut dp = vec![0usize; b.len() + 1];
    for i in 1..=a.len() {
        let mut prev_diag = 0;
        for j in 1..=b.len() {
            let tmp = dp[j];
            dp[j] = if a[i - 1] == b[j - 1] {
                prev_diag + 1
            } else {
                0
            };
            best = best.max(dp[j]);
            prev_diag = tmp;
        }
    }
    best
}

#[derive(Debug, Clone)]
pub struct ToolDefinition {
    pub name: String,
    pub description: String,
    pub input_schema: Value,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_list_tools_returns_tools() {
        let tools = ToolRegistry::list_tools();
        assert!(!tools.is_empty());
    }

    #[test]
    fn test_list_tools_is_exactly_one() {
        let tools = ToolRegistry::list_tools();
        assert_eq!(
            tools.len(),
            1,
            "one-tool cutover: exactly one registered tool"
        );
        assert_eq!(tools[0].name, "leankg_context");
    }

    #[test]
    fn test_verb_catalog_covers_core_capabilities() {
        let verbs = verb_catalog();
        for v in [
            "get_dependencies",
            "get_impact_radius",
            "find_related_docs",
            "query_graph",
            "shortest_path",
            "mcp_status",
            "mcp_init",
        ] {
            assert!(
                verbs.contains(&v),
                "capability `{v}` must be a registered verb"
            );
        }
        for removed in [
            "mcp_hello",
            "mcp_impact",
            "get_doc_for_file",
            "find_clones",
            "wake_up",
            "search_by_environment",
        ] {
            assert!(
                !verbs.contains(&removed),
                "removed tool `{removed}` must not be a verb either"
            );
        }
    }

    /// TDD: Verify the 11 redundant/thin-wrapper tools have been removed.
    /// These tools were identified as overlapping with more general tools:
    /// - query_file → search_code (same safe_discover path on mega-graphs)
    /// - find_function → search_code with element_type="function"
    /// - get_callers → get_call_graph with depth=1
    /// - search_annotations → manual filter on full graph load
    /// - get_cluster_context → get_cluster_skill (same detect+load+filter)
    /// - kg_concept_map → kg_context (same ontology query engine)
    /// - get_graph_schema → low-value meta tool
    /// - find_dead_code → low-value analysis tool
    /// - session_recall → session-specific, rarely used
    /// - kg_self_test → internal test tool
    /// - mcp_embed → thin wrapper (call mcp_index + embed_control sequentially)
    #[test]
    fn test_redundant_tools_removed() {
        let tools = ToolRegistry::list_tools();
        let names: Vec<_> = tools.iter().map(|t| t.name.as_str()).collect();
        let removed = [
            "query_file",
            "find_function",
            "get_callers",
            "search_annotations",
            "get_cluster_context",
            "kg_concept_map",
            "get_graph_schema",
            "find_dead_code",
            "session_recall",
            "kg_self_test",
            "mcp_embed",
        ];
        for tool_name in &removed {
            assert!(
                !names.contains(tool_name),
                "redundant tool `{tool_name}` must be removed (replaced by more general tools)"
            );
        }
        // Verify the replacing capabilities survive as verbs.
        for v in [
            "search_code",
            "get_call_graph",
            "get_cluster_skill",
            "kg_context",
            "get_architecture",
        ] {
            assert!(
                verb_catalog().contains(&v),
                "`{v}` must remain a registered verb"
            );
        }
        // One-tool cutover (FR-ZCP-03 end-state): the registry holds exactly
        // one tool regardless of feature flags; the removed names survive
        // only as verbs.
        assert_eq!(
            names.len(),
            1,
            "unexpected tool-count drift (one-tool cutover regression?)"
        );
    }

    #[test]
    fn test_query_graph_is_a_verb() {
        // query_graph lost its dedicated registry row in the one-tool
        // cutover; its schema contract lives in the verb dispatcher, so the
        // pinned surface here is verb-catalog membership + the router's
        // pass-through mechanism.
        assert!(verb_catalog().contains(&"query_graph"));
        let tools = ToolRegistry::list_tools();
        let tool = tools
            .iter()
            .find(|t| t.name == "leankg_context")
            .expect("leankg_context must be registered");
        assert!(
            tool.description.contains("verb"),
            "router description must document the verb mechanism"
        );
    }

    /// FR-ZCP-12 T3 accounting (FR-ZCP-03): every tool description carries
    /// a `Tier:` marker — core (router + essentials), setup, advanced — with
    /// no tool left unmarked and no unknown tier values.
    #[test]
    fn test_all_tools_carry_tier_markers() {
        let tools = ToolRegistry::list_tools();
        assert!(!tools.is_empty());
        let mut counts = std::collections::BTreeMap::new();
        for tool in &tools {
            let desc = &tool.description;
            let tier = desc
                .strip_prefix("Tier: ")
                .and_then(|rest| {
                    let end = rest.find(". ")?;
                    Some(&rest[..end])
                })
                .unwrap_or_else(|| {
                    panic!(
                        "tool {} description missing Tier marker: {}",
                        tool.name, desc
                    )
                });
            assert!(
                matches!(tier, "core" | "setup" | "advanced"),
                "tool {} has unknown tier {:?}",
                tool.name,
                tier
            );
            *counts.entry(tier).or_insert(0usize) += 1;
        }
        // One-tool cutover: the single tool is the router and is core.
        assert_eq!(counts.get("core").copied().unwrap_or(0), 1);
        assert_eq!(counts.get("setup").copied().unwrap_or(0), 0);
        // leankg_context must be marked core.
        let router = tools
            .iter()
            .find(|t| t.name == "leankg_context")
            .expect("leankg_context must be registered");
        assert!(router.description.starts_with("Tier: core. "));
    }

    #[test]
    fn test_tool_definitions_have_schemas() {
        let tools = ToolRegistry::list_tools();
        for tool in &tools {
            assert!(!tool.description.is_empty());
            assert!(tool.input_schema.is_object());
        }
    }

    #[test]
    fn test_verb_catalog_contains_v2_capabilities() {
        let verbs = verb_catalog();
        for v in [
            "query_incidents",
            "find_env_conflicts",
            "get_service_context",
        ] {
            assert!(
                verbs.contains(&v),
                "capability `{v}` must be a registered verb"
            );
        }
    }

    #[test]
    fn test_one_tool_schema_is_valid() {
        let tools = ToolRegistry::list_tools();
        for tool in &tools {
            assert!(!tool.description.is_empty());
            assert!(tool.input_schema.is_object());
            let schema = tool.input_schema.as_object().unwrap();
            assert!(schema.contains_key("type"));
            assert!(schema.contains_key("properties"));
        }
    }

    #[test]
    fn test_semantic_search_is_a_verb() {
        assert!(verb_catalog().contains(&"semantic_search"));
        let tools = ToolRegistry::list_tools();
        let tool = tools.iter().find(|t| t.name == "leankg_context").unwrap();
        assert!(tool.description.contains("capability ladder"));
        let properties = tool.input_schema["properties"].as_object().unwrap();
        assert!(properties.contains_key("query"));
        assert!(properties.contains_key("verb"));
        assert!(properties.contains_key("limit"));
    }
    #[test]
    fn test_semantic_search_description_contract_lives_in_verb_doc() {
        // The per-tool descriptions moved out of the registry in the
        // one-tool cutover; the capability docs now live in the verb
        // dispatcher's handler docs. Pin the mechanism the agent sees:
        assert!(verb_catalog().contains(&"semantic_search"));
        let tools = ToolRegistry::list_tools();
        let tool = tools.iter().find(|t| t.name == "leankg_context").unwrap();
        assert!(
            tool.description.contains("L3 vector"),
            "router description must document the L3 vector rung (the semantic_search path)"
        );
    }

    #[test]
    fn test_one_tool_description_carries_prefer_order() {
        let tools = ToolRegistry::list_tools();
        let tool = tools.iter().find(|t| t.name == "leankg_context").unwrap();
        // The router teaches the ladder + verb mechanism; per-tool prefer-
        // order copy lives in the one description now.
        assert!(tool
            .description
            .contains("Degrades ranking, never availability"));
        assert!(tool.description.contains("verb"));
    }

    #[cfg(feature = "embeddings")]
    #[test]
    fn test_embed_control_is_a_verb() {
        assert!(verb_catalog().contains(&"embed_control"));
    }

    #[cfg(feature = "embeddings")]
    #[test]
    fn test_set_embed_model_is_a_verb() {
        assert!(verb_catalog().contains(&"set_embed_model"));
    }

    #[test]
    fn test_ontology_control_is_a_verb() {
        assert!(verb_catalog().contains(&"ontology_control"));
    }

    #[test]
    fn test_resolve_envelope_contract() {
        use serde_json::map::Map as Args;

        // Legacy tool name → hard refusal with catalog code + nearest verb.
        let err = resolve_envelope("get_impact_radius", &Args::new()).unwrap_err();
        assert!(err.contains("LEANKG_ERROR_UNKNOWN_TOOL"), "{err}");
        assert!(err.contains("verb"), "{err}");

        // Envelope with valid verb → capability + stripped verb key.
        let mut args = Args::new();
        args.insert("verb".into(), serde_json::json!("get_impact_radius"));
        args.insert("file".into(), serde_json::json!("src/main.rs"));
        let (cap, inner) = resolve_envelope("leankg_context", &args).unwrap();
        assert_eq!(cap, "get_impact_radius");
        assert!(!inner.contains_key("verb"));
        assert_eq!(inner["file"], "src/main.rs");

        // No verb → natural-language router path, args unchanged.
        let mut args = Args::new();
        args.insert("query".into(), serde_json::json!("auth flow"));
        let (cap, inner) = resolve_envelope("leankg_context", &args).unwrap();
        assert_eq!(cap, "leankg_context");
        assert_eq!(inner["query"], "auth flow");

        // Unknown verb → refusal with nearest suggestion.
        let mut args = Args::new();
        args.insert("verb".into(), serde_json::json!("get_impract_radius"));
        let err = resolve_envelope("leankg_context", &args).unwrap_err();
        assert!(err.contains("LEANKG_ERROR_UNKNOWN_TOOL"), "{err}");
    }
}
