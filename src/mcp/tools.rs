use serde_json::json;
use serde_json::Value;

pub struct ToolRegistry;

impl ToolRegistry {
    /// Three-tool surface (FR-3T-01, v4.4.0): `set` / `get` / `status`.
    /// The verb namespace (legacy tool names) survives as per-tool `action`
    /// values; `get` with no `action` uses the natural-language router.
    pub fn list_tools() -> Vec<ToolDefinition> {
        vec![
            ToolDefinition {
                name: "set".to_string(),
                description: "Tier: core. Import a repository (or a directory of nested repos) into the knowledge graph and manage writes. Actions: `index` (full index of path, default when omitted), `incremental` (delta re-index), `attach` (register an already-indexed repo), `index_docs`, `install` (write client config), `add_knowledge`, `update_knowledge`, `delete_knowledge`, `add_annotation`, `add_documentation`, `link_element`, `add_ontology_concept`, `add_ontology_workflow`, `delete_ontology_concept`, `promote_environment`, `embed` (build HNSW vectors), `set_embed_model`, `agent_diary_write`, `report_query_outcome`, `agent_focus`, `index_prd`, `export_graph_snapshot`, `export_html`, `generate_doc`. Pass action-specific arguments as top-level fields.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "action": {"type": "string", "description": "What to do (default: index the repo/path). Legacy capability names are accepted as actions."},
                        "path": {"type": "string", "description": "Repository or nested directory of repos to import/index"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "get".to_string(),
                description: "Tier: core. Query the knowledge graph with multiple layers — the capability ladder auto-selects: L3 vector (ANN + rerank), L2 keyword (trigram fuzzy + ontology), L1 exact (identifier/regex + did-you-mean), L0 cold (guidance). Degrades ranking, never availability; every response carries retrieval {rung, reason, freshness}. With no `query`/`action`, serves the natural-language router. Direct capability access: pass `action` with any read capability (e.g. \"search_code\", \"get_impact_radius\", \"query_graph\", \"get_architecture\", \"explain_node\", \"kg_context\", \"temporal_query\") plus its usual arguments.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "query": {"type": "string", "description": "Natural-language question, identifier, path, or graph/impact question"},
                        "action": {"type": "string", "description": "Optional direct read capability (any read verb, e.g. search_code, get_impact_radius, query_graph, get_architecture); remaining arguments pass through unchanged"},
                        "layer": {"type": "string", "enum": ["auto", "exact", "keyword", "semantic", "graph"], "description": "Optional layer override; defaults to auto-classification from the query shape"},
                        "limit": {"type": "integer", "default": 20, "description": "Max results (1-50) for the router path"},
                        "full": {"type": "boolean", "default": false, "description": "Router path: return full compressed context instead of a page"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "status".to_string(),
                description: "Tier: core. Knowledge-graph health and inventory: index freshness, element/relationship counts, embedding coverage + model, indexing state (idle/indexing), storage backend (sqlite|postgres), watch/vacuum status. Read-only; safe on cold or missing indexes.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
        ]
    }
}

/// The 3-tool surface (FR-3T-01).
pub const TOOL_SET: &str = "set";
pub const TOOL_GET: &str = "get";
pub const TOOL_STATUS: &str = "status";

/// Route a legacy capability name to its owning tool. Everything not
/// classified as a set-mutation or status verb lands in `get` (reads).
pub fn owning_tool(capability: &str) -> &'static str {
    match capability {
        "mcp_init"
        | "mcp_index"
        | "mcp_index_docs"
        | "mcp_install"
        | "embed_control"
        | "set_embed_model"
        | "add_knowledge"
        | "update_knowledge"
        | "delete_knowledge"
        | "add_annotation"
        | "link_element"
        | "add_documentation"
        | "promote_environment"
        | "add_ontology_concept"
        | "add_ontology_workflow"
        | "delete_ontology_concept"
        | "agent_diary_write"
        | "report_query_outcome"
        | "agent_focus"
        | "index_prd"
        | "export_graph_snapshot"
        | "export_html"
        | "generate_doc" => TOOL_SET,
        "mcp_status" => TOOL_STATUS,
        _ => TOOL_GET,
    }
}

/// Map (tool, action-or-legacy-verb) to the effective capability.
///
/// Resolution order per tool:
/// * `set`:   `action` (default "index") — legacy verbs accepted as actions.
/// * `get`:   `action` if present, else the natural-language router
///   (`leankg_context` capability — the multi-layer ladder).
/// * `status`: always `mcp_status` (its only capability).
///
/// Returns the effective capability name + arguments with routing keys
/// (`action`/`verb`) stripped.
pub fn resolve_3tool(
    tool_name: &str,
    arguments: &serde_json::Map<String, serde_json::Value>,
) -> Result<(String, serde_json::Map<String, serde_json::Value>), String> {
    let mut inner = arguments.clone();
    let capability = match tool_name {
        TOOL_SET => {
            let a = inner
                .get("action")
                .and_then(|v| v.as_str())
                .unwrap_or("mcp_index")
                .to_string();
            let canonical = if is_valid_verb(&a) {
                a
            } else if a == "index" || a == "incremental" || a == "attach" || a == "embed" {
                match a.as_str() {
                    "index" | "attach" => "mcp_index".to_string(),
                    "incremental" => "mcp_index".to_string(),
                    "embed" => "embed_control".to_string(),
                    _ => a,
                }
            } else {
                let nearest = nearest_verb_name(&a);
                return Err(crate::errors::render(
                    crate::errors::UNKNOWN_TOOL.code,
                    &format!("unknown set action '{a}'"),
                    &format!("use action \"{nearest}\" (nearest capability) or list actions in the set tool description"),
                ));
            };
            inner.remove("action");
            canonical
        }
        TOOL_GET => {
            let a = inner
                .get("action")
                .and_then(|v| v.as_str())
                .map(str::to_string);
            inner.remove("action");
            match a {
                Some(a) => {
                    if !is_valid_verb(&a) {
                        let nearest = nearest_verb_name(&a);
                        return Err(crate::errors::render(
                            crate::errors::UNKNOWN_TOOL.code,
                            &format!("unknown get action '{a}'"),
                            &format!("use action \"{nearest}\" (nearest capability) or omit `action` for the multi-layer router"),
                        ));
                    }
                    a
                }
                None => "leankg_context".to_string(), // NL router (multi-layer)
            }
        }
        TOOL_STATUS => {
            inner.remove("action");
            "mcp_status".to_string()
        }
        _ => {
            let nearest = nearest_verb_name(tool_name);
            return Err(crate::errors::render(
                crate::errors::UNKNOWN_TOOL.code,
                &format!(
                    "tool '{tool_name}' is not registered — LeanKG exposes exactly 3 tools: `set`, `get`, `status`"
                ),
                &format!(
                    "call `get` with {{\"query\": ...}} for questions, `set` with {{\"action\": ...}} for imports/writes, `status` for health; nearest capability: {nearest}"
                ),
            ));
        }
    };
    Ok((capability, inner))
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
    fn test_list_tools_is_exactly_three() {
        let tools = ToolRegistry::list_tools();
        assert_eq!(
            tools.iter().map(|t| t.name.as_str()).collect::<Vec<_>>(),
            vec!["set", "get", "status"],
            "3-tool surface: set / get / status"
        );
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
        // v4.4.0: the registry holds exactly the 3-tool surface regardless
        // of feature flags; removed names survive only as actions.
        assert_eq!(
            names.len(),
            3,
            "unexpected tool-count drift (3-tool surface regression?)"
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
        let get = tools
            .iter()
            .find(|t| t.name == "get")
            .expect("get must be registered");
        assert!(
            get.description.contains("action"),
            "get description must document the action mechanism"
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
        // v4.4.0: set/get/status are all core.
        assert_eq!(counts.get("core").copied().unwrap_or(0), 3);
        assert_eq!(counts.get("setup").copied().unwrap_or(0), 0);
        // status must be marked core.
        let status = tools
            .iter()
            .find(|t| t.name == "status")
            .expect("status must be registered");
        assert!(status.description.starts_with("Tier: core. "));
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
        let get = tools.iter().find(|t| t.name == "get").unwrap();
        assert!(get.description.contains("capability ladder"));
        let properties = get.input_schema["properties"].as_object().unwrap();
        assert!(properties.contains_key("query"));
        assert!(properties.contains_key("action"));
        assert!(properties.contains_key("limit"));
    }
    #[test]
    fn test_semantic_search_description_contract_lives_in_verb_doc() {
        // The per-tool descriptions moved out of the registry in the
        // one-tool cutover; the capability docs now live in the verb
        // dispatcher's handler docs. Pin the mechanism the agent sees:
        assert!(verb_catalog().contains(&"semantic_search"));
        let tools = ToolRegistry::list_tools();
        let get = tools.iter().find(|t| t.name == "get").unwrap();
        assert!(
            get.description.contains("L3 vector"),
            "get description must document the L3 vector rung (the semantic_search path)"
        );
    }

    #[test]
    fn test_get_description_carries_prefer_order() {
        let tools = ToolRegistry::list_tools();
        let get = tools.iter().find(|t| t.name == "get").unwrap();
        assert!(get
            .description
            .contains("Degrades ranking, never availability"));
        assert!(get.description.contains("action"));
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
