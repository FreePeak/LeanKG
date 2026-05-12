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
            "get_impact_radius" => 600,
            "query_incidents" => 500,
            "find_env_conflicts" => 400,
            "trace_call_chain" => 400,
            "semantic_search" => 300,
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
            // Take ownership of the object
            if let Value::Object(mut obj) = std::mem::replace(value, Value::Null) {
                let keys_to_remove: Vec<String> = obj
                    .keys()
                    .filter(|k| {
                        !matches!(
                            k.as_str(),
                            "service" | "env" | "query" | "file" | "function" | "element" | "id"
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
                }
                *value = Value::Object(obj);
                return true;
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
        assert_eq!(TokenBudget::max_tokens_for_tool("semantic_search"), 300);
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
    }
}
