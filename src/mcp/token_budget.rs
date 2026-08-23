use serde_json::{Map, Value};

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
            "kg_context" => 4000,
            "kg_trace_workflow" => 4000,
            "kg_ontology_status" => 2000,
            "get_clusters" => 4000,
            "get_doc_tree" => 4000,
            "get_code_tree" => 4000,
            "get_call_graph" => 4000,
            "search_code" => 4000,
            "query_graph" => 2000,
            "get_dependencies" => 2000,
            "get_dependents" => 2000,
            _ => 1000, // default
        }
    }

    /// Truncate a JSON value to fit within max_tokens
    pub fn apply(value: Value, tool_name: &str) -> Value {
        let max_tokens = Self::max_tokens_for_tool(tool_name);
        let current = Self::count_tokens(&value);
        if current <= max_tokens {
            return value;
        }

        let mut result = value;
        let truncated = Self::truncate_value(&mut result, max_tokens);

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
        }

        result
    }

    fn truncate_value(value: &mut Value, max_tokens: usize) -> bool {
        if Self::count_tokens(value) <= max_tokens {
            return false;
        }

        match value {
            Value::Array(arr) => Self::truncate_array(arr, max_tokens),
            Value::Object(obj) => Self::truncate_object(obj, max_tokens),
            _ => false,
        }
    }

    /// Exact serialized size of one JSON item in compact form (bytes).
    /// serde_json's compact writer escapes deterministically, so
    /// `item.to_string().len()` is exactly the item's contribution.
    fn item_bytes(item: &Value) -> usize {
        item.to_string().len()
    }

    /// Exact serialized size of `"key":value` plus its trailing comma slot.
    fn entry_bytes(key: &str, value: &Value) -> usize {
        let key_str = serde_json::to_string(key).unwrap_or_else(|_| format!("\"{key}\""));
        key_str.len() + 1 + Self::item_bytes(value) + 1
    }

    /// Exact serialized size of a compact JSON object.
    fn object_bytes(obj: &Map<String, Value>) -> usize {
        if obj.is_empty() {
            return 2; // "{}"
        }
        let entries: usize = obj.iter().map(|(k, v)| Self::entry_bytes(k, v)).sum();
        2 + entries - 1 // drop the last comma slot
    }

    /// R2b perf fix: the old loop cloned the array and re-serialized it once
    /// per popped item (O(n²)) — 24k consistency findings or 93k temporal
    /// relationships burned a tokio worker for minutes and wedged the whole
    /// server (N4). Per-item sizes are now summed in ONE pass and the
    /// largest fitting prefix is kept via partition point (O(n)).
    ///
    /// Semantics preserved: keep as many leading items as fit the budget,
    /// report `truncated = true` when items were dropped.
    fn truncate_array(arr: &mut Vec<Value>, max_tokens: usize) -> bool {
        let budget = max_tokens.saturating_mul(4);
        // cum[i] = exact bytes of "[i0,i1,...,ii]"
        let mut cum: Vec<usize> = Vec::with_capacity(arr.len());
        let mut acc = 2usize;
        for item in arr.iter() {
            acc += Self::item_bytes(item) + 1;
            cum.push(acc);
        }
        let keep = cum.partition_point(|&c| c <= budget);
        if keep == arr.len() {
            // Whole array fits on its own — the over-budget part is the
            // parent object; nothing to drop here.
            return false;
        }
        arr.truncate(keep.max(1)); // never hand back a silently emptied payload marker
        true
    }

    /// R2b perf fix: mirror of `truncate_array`. Recursion into children is
    /// unchanged; key removal now tracks the running serialized size instead
    /// of cloning + re-serializing the object per removed key (O(n²) → O(n)).
    /// Same removal order (map iteration order) and same "stop when under
    /// budget" rule; protected keys are never dropped.
    fn truncate_object(obj: &mut Map<String, Value>, max_tokens: usize) -> bool {
        let mut truncated = false;
        for child in obj.values_mut() {
            if child.is_array() || child.is_object() {
                truncated |= Self::truncate_value(child, max_tokens);
            }
        }

        let removable = |k: &str| {
            !matches!(
                k,
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
                    // Primary payload keys of full-scan tools must survive so
                    // the response keeps its shape after truncation:
                    | "findings"
                    | "relationships"
                    | "elements"
            )
        };
        let keys_to_remove: Vec<String> = obj.keys().filter(|k| removable(k)).cloned().collect();

        let mut cur = Self::object_bytes(obj);
        let budget = max_tokens.saturating_mul(4);
        for key in keys_to_remove {
            if cur <= budget {
                break;
            }
            let Some(v) = obj.remove(&key) else {
                continue;
            };
            let remaining = obj.len();
            cur = cur
                .saturating_sub(Self::entry_bytes(&key, &v))
                .saturating_sub(if remaining >= 1 { 1 } else { 0 });
            truncated = true;
        }
        truncated
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

    #[test]
    fn test_apply_under_budget() {
        let v = json!({"small": "data"});
        let result = TokenBudget::apply(v.clone(), "semantic_search");
        assert!(result.get("_token_budget").is_none());
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
