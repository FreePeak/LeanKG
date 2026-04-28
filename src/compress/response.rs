use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CompressionStats {
    pub original_tokens: usize,
    pub compressed_tokens: usize,
    pub savings_percent: f64,
}

pub struct ResponseCompressor {
    max_elements: usize,
    max_depth: usize,
    compress_enabled: bool,
}

impl Default for ResponseCompressor {
    fn default() -> Self {
        Self::new()
    }
}

impl ResponseCompressor {
    pub fn new() -> Self {
        Self {
            max_elements: 20,
            max_depth: 3,
            compress_enabled: true,
        }
    }

    pub fn with_max_elements(mut self, max: usize) -> Self {
        self.max_elements = max;
        self
    }

    pub fn with_max_depth(mut self, max: usize) -> Self {
        self.max_depth = max;
        self
    }

    pub fn with_compression(mut self, enabled: bool) -> Self {
        self.compress_enabled = enabled;
        self
    }

    pub fn compress_impact_radius(&self, response: &Value) -> Value {
        if !self.compress_enabled {
            return response.clone();
        }

        let elements = response
            .get("elements_with_confidence")
            .and_then(|v| v.as_array())
            .map(|arr| arr.to_vec())
            .unwrap_or_default();

        let total_count = elements.len();
        let top_elements: Vec<Value> = elements.iter().take(self.max_elements).cloned().collect();

        let compressed = serde_json::json!({
            "start_file": response.get("start_file").unwrap_or(&Value::Null),
            "max_depth": response.get("max_depth").unwrap_or(&Value::Null),
            "total_affected": total_count,
            "elements_summary": format!("{} elements total", total_count),
            "top_elements": top_elements,
            "_compression_note": "Use get_impact_radius with compress=true for full results"
        });

        if response.get("elements").is_some() {
            serde_json::json!({
                "start_file": response.get("start_file").unwrap_or(&Value::Null),
                "max_depth": response.get("max_depth").unwrap_or(&Value::Null),
                "total_affected": total_count,
                "top_elements": top_elements,
                "_compression_note": "Use get_impact_radius with compress=true for full results"
            })
        } else {
            compressed
        }
    }

    pub fn compress_call_graph(&self, response: &Value) -> Value {
        if !self.compress_enabled {
            return response.clone();
        }

        let callers = response
            .get("callers")
            .and_then(|v| v.as_array())
            .map(|arr| arr.to_vec());
        let callees = response
            .get("callees")
            .and_then(|v| v.as_array())
            .map(|arr| arr.to_vec());

        let compress_call_list = |list: Option<Vec<Value>>, _label: &str| -> Value {
            match list {
                Some(items) => {
                    let _total = items.len();
                    let top: Vec<Value> = items.iter().take(self.max_elements).cloned().collect();
                    Value::Array(top)
                }
                None => Value::Null,
            }
        };

        serde_json::json!({
            "function": response.get("function").unwrap_or(&Value::Null),
            "caller_count": callers.as_ref().map(|c| c.len()).unwrap_or(0),
            "callee_count": callees.as_ref().map(|c| c.len()).unwrap_or(0),
            "callers": compress_call_list(callers, "callers"),
            "callees": compress_call_list(callees, "callees"),
            "_compression_note": "Use get_call_graph with compress=true for full results"
        })
    }

    pub fn compress_search_code(&self, response: &Value) -> Value {
        if !self.compress_enabled {
            return response.clone();
        }

        let elements = response
            .get("elements")
            .and_then(|v| v.as_array())
            .map(|arr| arr.to_vec())
            .unwrap_or_default();

        let total_count = elements.len();
        let top_elements: Vec<Value> = elements.iter().take(self.max_elements).cloned().collect();

        serde_json::json!({
            "total_matches": total_count,
            "elements_summary": if total_count > self.max_elements {
                format!("Showing {} of {} elements", self.max_elements, total_count)
            } else {
                format!("{} elements", total_count)
            },
            "top_elements": top_elements,
            "_compression_note": "Use search_code with compress=true for full results"
        })
    }

    pub fn compress_search_annotations(&self, response: &Value) -> Value {
        if !self.compress_enabled {
            return response.clone();
        }

        let annotations = response
            .get("annotations")
            .and_then(|v| v.as_array())
            .map(|arr| arr.to_vec())
            .unwrap_or_default();

        let total_count = annotations.len();
        let top: Vec<Value> = annotations
            .iter()
            .take(self.max_elements)
            .cloned()
            .collect();

        serde_json::json!({
            "count": total_count,
            "annotations_summary": if total_count > self.max_elements {
                format!("Showing {} of {} annotations", self.max_elements, total_count)
            } else {
                format!("{} annotations", total_count)
            },
            "annotations": top,
            "_compression_note": "Use search_annotations with compress=true for full results"
        })
    }

    pub fn compress_nav_graph(&self, response: &Value) -> Value {
        if !self.compress_enabled {
            return response.clone();
        }

        let elements = response
            .get("elements")
            .and_then(|v| v.as_array())
            .map(|arr| arr.to_vec())
            .unwrap_or_default();

        let total = elements.len();
        let top: Vec<Value> = elements.iter().take(self.max_elements).cloned().collect();

        serde_json::json!({
            "count": total,
            "elements": top,
            "relationships": response.get("relationships").cloned().unwrap_or(serde_json::json!([])),
            "_compression_note": "Use get_nav_graph with full file path for complete results"
        })
    }

    pub fn compress_dependencies(&self, response: &Value) -> Value {
        if !self.compress_enabled {
            return response.clone();
        }

        let deps = response
            .get("dependencies")
            .and_then(|v| v.as_array())
            .map(|arr| arr.to_vec())
            .unwrap_or_default();

        let total = deps.len();
        let top: Vec<Value> = deps.iter().take(self.max_elements).cloned().collect();

        serde_json::json!({
            "total": total,
            "dependencies": Value::Array(top),
            "_compression_note": if total > self.max_elements {
                "Showing 20 of {}. Use get_dependencies with compress=true for full results".replace("{}", &total.to_string())
            } else {
                "Use get_dependencies with compress=true for full results".to_string()
            }
        })
    }

    pub fn compress_dependents(&self, response: &Value) -> Value {
        if !self.compress_enabled {
            return response.clone();
        }

        let deps = response
            .get("dependents")
            .and_then(|v| v.as_array())
            .map(|arr| arr.to_vec())
            .unwrap_or_default();

        let total = deps.len();
        let top: Vec<Value> = deps.iter().take(self.max_elements).cloned().collect();

        serde_json::json!({
            "total": total,
            "dependents": Value::Array(top),
            "_compression_note": if total > self.max_elements {
                "Showing 20 of {}. Use get_dependents with compress=true for full results".replace("{}", &total.to_string())
            } else {
                "Use get_dependents with compress=true for full results".to_string()
            }
        })
    }

    pub fn compress_context(&self, response: &Value) -> Value {
        if !self.compress_enabled {
            return response.clone();
        }

        let elements = response
            .get("elements")
            .and_then(|v| v.as_array())
            .map(|arr| arr.to_vec())
            .unwrap_or_default();

        let total_count = elements.len();
        let top_elements: Vec<Value> = elements.iter().take(self.max_elements).cloned().collect();

        serde_json::json!({
            "total_elements": total_count,
            "elements_summary": if total_count > self.max_elements {
                format!("Showing {} of {} elements", self.max_elements, total_count)
            } else {
                format!("{} elements", total_count)
            },
            "top_elements": top_elements,
            "file": response.get("file").unwrap_or(&Value::Null),
            "_compression_note": "Use get_context with compress=true for full results"
        })
    }

    pub fn estimate_savings(&self, original: &Value, compressed: &Value) -> CompressionStats {
        let original_str = original.to_string();
        let compressed_str = compressed.to_string();
        let original_tokens = original_str.len() / 4;
        let compressed_tokens = compressed_str.len() / 4;

        let savings_percent = if original_tokens > 0 {
            ((original_tokens - compressed_tokens) as f64 / original_tokens as f64) * 100.0
        } else {
            0.0
        };

        CompressionStats {
            original_tokens,
            compressed_tokens,
            savings_percent,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn test_compress_impact_radius() {
        let compressor = ResponseCompressor::new();
        let response = json!({
            "start_file": "src/main.rs",
            "max_depth": 3,
            "affected": 50,
            "elements": (0..50).map(|i| json!({
                "qualified_name": format!("func{}", i),
                "name": format!("func{}", i),
                "type": "function",
                "file": "src/main.rs"
            })).collect::<Vec<_>>(),
            "elements_with_confidence": (0..50).map(|i| json!({
                "qualified_name": format!("func{}", i),
                "confidence": 0.9,
                "severity": "WILL BREAK",
                "depth": 1
            })).collect::<Vec<_>>()
        });

        let compressed = compressor.compress_impact_radius(&response);
        assert!(compressed.get("total_affected").is_some());
        assert!(compressed.get("top_elements").is_some());
        let top = compressed.get("top_elements").unwrap().as_array().unwrap();
        assert!(top.len() <= 20);
    }

    #[test]
    fn test_compress_search_code() {
        let compressor = ResponseCompressor::new();
        let response = json!({
            "elements": (0..30).map(|i| json!({
                "qualified_name": format!("func{}", i)
            })).collect::<Vec<_>>()
        });

        let compressed = compressor.compress_search_code(&response);
        let top = compressed.get("top_elements").unwrap().as_array().unwrap();
        assert!(top.len() <= 20);
    }

    #[test]
    fn test_estimate_savings() {
        let compressor = ResponseCompressor::new();
        let original = json!({
            "elements": (0..100).map(|i| json!({"name": format!("item{}", i)})).collect::<Vec<_>>()
        });
        let compressed = json!({
            "elements": (0..20).map(|i| json!({"name": format!("item{}", i)})).collect::<Vec<_>>()
        });

        let stats = compressor.estimate_savings(&original, &compressed);
        assert!(stats.savings_percent > 0.0);
    }
}
