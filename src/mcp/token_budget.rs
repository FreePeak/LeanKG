use serde_json::Value;

pub struct TokenBudget;

impl TokenBudget {
    /// Rough token count: 1 token ~= 4 chars for JSON
    pub fn count_tokens(value: &Value) -> usize {
        value.to_string().len() / 4
    }

    /// Get max tokens for a given tool name
    pub fn max_tokens_for_tool(tool_name: &str) -> usize {
        match tool_name {
            "get_service_context" => 800,
            "get_impact_radius" => 6000,
            "query_incidents" => 2000,
            "find_env_conflicts" => 2000,
            "trace_call_chain" => 2000,
            "semantic_search" => 2000,
            // FR-SEM-02: ontology-heavy SEM tools get explicit budgets in the
            // sibling kg_* band (2k-4k), not the anonymous 1000 default.
            "concept_search" => 4000,
            "kg_semantic_context" => 4000,
            "kg_context" => 4000,
            "kg_concept_map" => 4000,
            "kg_trace_workflow" => 4000,
            "kg_ontology_status" => 2000,
            "kg_self_test" => 4000,
            "get_clusters" => 4000,
            "get_cluster_context" => 4000,
            "get_doc_tree" => 4000,
            "get_code_tree" => 4000,
            "get_call_graph" => 4000,
            "search_code" => 4000,
            "query_graph" => 2000,
            "query_file" => 4000,
            "get_dependencies" => 2000,
            "get_dependents" => 2000,
            _ => 1000, // default
        }
    }

    /// Truncate a JSON value to fit within max_tokens.
    ///
    /// FR-SEM-01 / US-SEM-01 dual accounting:
    /// - top-level `tokens` = DELIVERED figure (post-trim, so agents know
    ///   what actually entered the context window);
    /// - `_token_budget.actual` = pre-truncation cost (so agents never
    ///   under-budget by 3-4x when `truncated: true`);
    /// - `_token_budget.max` = tool-specific budget (FR-SEM-02), not the
    ///   anonymous default;
    /// - `_token_budget.truncated` = whether anything was trimmed.
    ///
    /// Envelope is attached to every object response (truncated or not) so
    /// the delivered/actual comparison is always available.
    pub fn apply(value: Value, tool_name: &str) -> Value {
        let max_tokens = Self::max_tokens_for_tool(tool_name);
        let current = Self::count_tokens(&value);

        let mut result = value;
        let truncated = if current > max_tokens {
            Self::truncate_value(&mut result, max_tokens)
        } else {
            false
        };
        // Payload-only delivered figure: measured BEFORE the envelope is
        // attached so `tokens`/`actual` never count the accounting keys
        // themselves (FR-SEM-01 honesty).
        let delivered = Self::count_tokens(&result);

        // Add budget metadata
        if let Some(obj) = result.as_object_mut() {
            obj.insert(
                "_token_budget".to_string(),
                serde_json::json!({
                    "max": max_tokens,
                    "actual": current,
                    "truncated": truncated
                }),
            );
            obj.insert("tokens".to_string(), serde_json::json!(delivered));
        }

        result
    }

    fn truncate_value(value: &mut Value, max_tokens: usize) -> bool {
        if Self::count_tokens(value) <= max_tokens {
            return false;
        }

        if value.is_array() {
            // Take ownership of the array
            if let Value::Array(mut arr) = std::mem::replace(value, Value::Null) {
                while !arr.is_empty() {
                    let tmp = Value::Array(arr.clone());
                    if Self::count_tokens(&tmp) <= max_tokens {
                        break;
                    }
                    arr.pop();
                }
                *value = Value::Array(arr);
                return true;
            }
        }

        if value.is_object() {
            if let Value::Object(mut obj) = std::mem::replace(value, Value::Null) {
                let mut truncated = false;
                for child in obj.values_mut() {
                    if child.is_array() || child.is_object() {
                        truncated |= Self::truncate_value(child, max_tokens);
                    }
                }

                let keys_to_remove: Vec<String> = obj
                    .keys()
                    .filter(|k| {
                        !matches!(
                            k.as_str(),
                            "service"
                                | "env"
                                | "query"
                                | "file"
                                | "function"
                                | "element"
                                | "id"
                                | "results"
                                | "incidents"
                                | "conflicts"
                                | "calls"
                                | "called_by"
                                | "open_incidents"
                                | "recent_incidents"
                                | "count"
                        )
                    })
                    .cloned()
                    .collect();
                for key in keys_to_remove {
                    let tmp = Value::Object(obj.clone());
                    if Self::count_tokens(&tmp) <= max_tokens {
                        break;
                    }
                    obj.remove(&key);
                    truncated = true;
                }
                *value = Value::Object(obj);
                return truncated;
            }
        }

        false
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn test_count_tokens() {
        let v = json!({"key": "value"});
        assert!(TokenBudget::count_tokens(&v) > 0);
    }

    #[test]
    fn test_max_tokens_for_tool() {
        assert_eq!(TokenBudget::max_tokens_for_tool("get_service_context"), 800);
        assert_eq!(TokenBudget::max_tokens_for_tool("semantic_search"), 2000);
        assert_eq!(TokenBudget::max_tokens_for_tool("kg_context"), 4000);
        assert_eq!(TokenBudget::max_tokens_for_tool("get_impact_radius"), 6000);
        assert_eq!(TokenBudget::max_tokens_for_tool("unknown_tool"), 1000);
    }

    // FR-SEM-02: ontology-heavy SEM tools must not fall through to the
    // anonymous 1000 default. Sibling `kg_*` tools sit at 2k-4k; these two
    // must match that band so live probes (concept_search actual ~3053,
    // kg_semantic_context actual ~2428) are not silently cut mid-result.
    #[test]
    fn test_max_tokens_for_tool_sem_ontology_tools() {
        let concept = TokenBudget::max_tokens_for_tool("concept_search");
        let kg_sem = TokenBudget::max_tokens_for_tool("kg_semantic_context");
        assert!(
            (2000..=4000).contains(&concept),
            "concept_search budget {concept} must be in 2k-4k (FR-SEM-02)"
        );
        assert!(
            (2000..=4000).contains(&kg_sem),
            "kg_semantic_context budget {kg_sem} must be in 2k-4k (FR-SEM-02)"
        );
        // >= sibling kg_* band, not the anonymous default
        assert_ne!(concept, 1000, "concept_search must not use default 1000");
        assert_ne!(
            kg_sem, 1000,
            "kg_semantic_context must not use default 1000"
        );
    }

    #[test]
    fn test_apply_under_budget() {
        // FR-SEM-01: envelope is attached to every object response so the
        // delivered/actual comparison is always available; untruncated
        // responses carry truncated:false and tokens == actual.
        let v = json!({"small": "data"});
        let result = TokenBudget::apply(v.clone(), "semantic_search");
        let budget = result.get("_token_budget").unwrap();
        assert_eq!(budget["truncated"].as_bool(), Some(false));
        let actual = budget["actual"].as_u64().unwrap() as usize;
        let tokens = result["tokens"].as_u64().unwrap() as usize;
        assert_eq!(actual, tokens, "untruncated: tokens == actual");
        assert!(result.get("small").is_some(), "payload preserved");
    }

    // FR-SEM-01 / US-SEM-01: dual accounting. Top-level `tokens` is the
    // DELIVERED figure (post-trim); `_token_budget.actual` is the
    // pre-truncation cost so agents never under-budget by 3-4x when
    // `truncated: true`. `max` must reflect the tool-specific budget.
    #[test]
    fn test_apply_truncated_dual_accounting_delivered_vs_actual() {
        let v = json!({
            "query": "service lookup",
            "results": vec![json!({"id": "1", "data": "x".repeat(500)}); 30],
            "debug": "x".repeat(5000)
        });
        let result = TokenBudget::apply(v, "concept_search");
        let budget = result.get("_token_budget").unwrap();
        assert_eq!(
            budget["max"].as_u64(),
            Some(4000),
            "max must be tool budget"
        );
        assert!(budget["truncated"].as_bool().unwrap(), "must truncate");
        let actual = budget["actual"].as_u64().unwrap() as usize;
        // Payload-only delivered figure (envelope keys excluded).
        let delivered = {
            let mut payload = result.clone();
            if let Some(obj) = payload.as_object_mut() {
                obj.remove("_token_budget");
                obj.remove("tokens");
            }
            TokenBudget::count_tokens(&payload)
        };
        // actual is the pre-trim cost: delivered must be <= max <= actual
        assert!(
            actual > delivered,
            "actual (pre-trim) {actual} must exceed delivered {delivered}"
        );
        assert!(
            delivered <= 4000,
            "delivered {delivered} must fit the max budget"
        );
        // Top-level `tokens` field == delivered figure (dual accounting key)
        let envelope_tokens = result["tokens"].as_u64().unwrap() as usize;
        assert_eq!(
            envelope_tokens, delivered,
            "top-level tokens must equal delivered count"
        );
    }

    // US-SEM-01 AC: truncated:false => delivered ~= actual.
    #[test]
    fn test_apply_untruncated_delivered_equals_actual() {
        let v = json!({"results": vec![json!({"id": "1"})]});
        let result = TokenBudget::apply(v, "concept_search");
        let budget = result.get("_token_budget").unwrap();
        assert_eq!(budget["truncated"].as_bool(), Some(false));
        let actual = budget["actual"].as_u64().unwrap() as usize;
        // Payload-only delivered figure (envelope keys excluded).
        let delivered = {
            let mut payload = result.clone();
            if let Some(obj) = payload.as_object_mut() {
                obj.remove("_token_budget");
                obj.remove("tokens");
            }
            TokenBudget::count_tokens(&payload)
        };
        assert!(
            (actual as i64 - delivered as i64).abs() <= 1,
            "delivered {delivered} ~= actual {actual} when untruncated"
        );
    }

    #[test]
    fn test_apply_truncate_array() {
        let v = json!({
            "results": vec![json!({"id": "1", "data": "x".repeat(500)}); 20]
        });
        let result = TokenBudget::apply(v, "semantic_search");
        let budget = result.get("_token_budget").unwrap();
        assert!(budget.get("truncated").unwrap().as_bool().unwrap());
        assert!(result.get("results").is_some());
    }

    #[test]
    fn test_apply_preserves_primary_payload_key() {
        let v = json!({
            "query": "service lookup",
            "results": vec![json!({"id": "1", "data": "x".repeat(500)}); 20],
            "debug": "x".repeat(5000)
        });
        let result = TokenBudget::apply(v, "semantic_search");
        assert!(result.get("query").is_some());
        assert!(result.get("results").is_some());
        assert!(result.get("debug").is_none());
    }
}
