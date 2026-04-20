use serde_json::Value;
use std::collections::BTreeSet;

/// Response Format Envelope for MCP tool responses
#[derive(Debug, Clone)]
pub struct ResponseEnvelope {
    pub status: String,
    pub tool: String,
    pub format: String,
    pub tokens: usize,
    pub data: String,
}

impl ResponseEnvelope {
    pub fn new(status: &str, tool: &str, format: &str, tokens: usize, data: String) -> Self {
        Self {
            status: status.to_string(),
            tool: tool.to_string(),
            format: format.to_string(),
            tokens,
            data,
        }
    }

    /// Convert envelope to JSON string
    pub fn to_json_string(&self) -> String {
        format!(
            r#"{{
  status: {}
  tool: {}
  format: {}
  tokens: {}
  data:
{}
}}"#,
            self.status,
            self.tool,
            self.format,
            self.tokens,
            indent_lines(&self.data, 4)
        )
    }

    /// Convert envelope to TOON string
    pub fn to_toon_string(&self) -> String {
        format!(
            "status: {}\ntool: {}\nformat: toon\ntokens: {}\ndata:\n{}",
            self.status,
            self.tool,
            self.tokens,
            indent_lines(&self.data, 2)
        )
    }
}

fn indent_lines(s: &str, spaces: usize) -> String {
    let indent = "  ".repeat(spaces);
    s.lines()
        .map(|line| format!("{}{}", indent, line))
        .collect::<Vec<_>>()
        .join("\n")
}

/// Convert a JSON value to TOON format string
/// TOON = Token-Oriented Object Notation
/// Reduces token usage by ~40% by omitting repetitive field names in arrays
pub fn to_toon_string(value: &Value) -> String {
    let mut output = String::new();
    convert_value(value, &mut output, 0, false);
    output
}

/// Main conversion function - converts JSON Value to TOON string
fn convert_value(value: &Value, output: &mut String, indent: usize, _is_array_item: bool) {
    let indent_str = "  ".repeat(indent);

    match value {
        Value::Null => {
            output.push_str("null");
        }
        Value::Bool(b) => {
            output.push_str(if *b { "true" } else { "false" });
        }
        Value::Number(n) => {
            output.push_str(&n.to_string());
        }
        Value::String(s) => {
            // For TOON, strings are usually unquoted unless they contain special chars
            if s.contains('\n') || s.contains('|') {
                // Multi-line - use literal block
                output.push_str("|\n");
                for line in s.lines() {
                    output.push_str(&format!("{}  {}\n", indent_str, line));
                }
            } else if s.contains(',')
                || s.contains(':')
                || s.contains('{')
                || s.contains('}')
                || s.contains('"')
            {
                // Contains special chars - quote it
                output.push_str(&format!("\"{}\"", s.replace('\"', "\\\"")));
            } else if s.is_empty() {
                output.push_str("\"\"");
            } else {
                // Simple string - unquoted for compactness
                output.push_str(s);
            }
        }
        Value::Array(arr) => {
            if arr.is_empty() {
                output.push_str("[]");
                return;
            }

            // Check if this is an array of objects with shared schema
            if let Some((array_name, schema)) = extract_shared_schema(arr) {
                // Use compact TOON array format
                // e.g., "elements[2]{qualified_name,type,language}:"
                output.push_str(&schema.header(&array_name, arr.len()));
                if arr.is_empty() {
                    output.push('\n');
                } else {
                    output.push('\n');
                    for item in arr {
                        output.push_str(&format!("{}{}\n", indent_str, schema.format_item(item)));
                    }
                }
            } else {
                // Fallback to YAML-like format for mixed types
                for item in arr {
                    output.push_str(&format!("{}- ", indent_str));
                    convert_value(item, output, indent + 1, true);
                    output.push('\n');
                }
            }
        }
        Value::Object(obj) => {
            if obj.is_empty() {
                output.push_str("{}\n");
                return;
            }

            // Sort keys for deterministic output
            let keys: BTreeSet<_> = obj.keys().collect();
            let mut first = true;

            for k in keys {
                if !first {
                    output.push('\n');
                }
                first = false;
                let v = &obj[k];
                output.push_str(&format!("{}{}: ", indent_str, k));

                match v {
                    Value::Object(inner_obj) if inner_obj.is_empty() => {
                        output.push_str("{}\n");
                    }
                    Value::Array(inner_arr) if inner_arr.is_empty() => {
                        output.push_str("[]\n");
                    }
                    Value::Object(_) | Value::Array(_) => {
                        output.push('\n');
                        convert_value(v, output, indent + 1, false);
                    }
                    _ => {
                        convert_value(v, output, indent, false);
                        output.push('\n');
                    }
                }
            }
        }
    }
}

/// Extracts a shared schema from an array of objects
/// Returns None if array is empty, has non-objects, or objects have different keys
/// Note: Fields are sorted alphabetically for deterministic schema headers
fn extract_shared_schema(arr: &[Value]) -> Option<(String, Schema)> {
    if arr.is_empty() {
        return None;
    }

    // All items must be objects
    let objects: Vec<&serde_json::Map<String, Value>> =
        arr.iter().filter_map(|v| v.as_object()).collect();

    if objects.len() != arr.len() {
        return None; // Not all items are objects
    }

    // Get the sorted key set from the first object
    // (BTreeSet gives us sorted order for deterministic output)
    let first_obj = objects[0];
    let mut fields: BTreeSet<String> = BTreeSet::new();
    for k in first_obj.keys() {
        fields.insert(k.clone());
    }

    // Check all objects have the same keys
    for obj in &objects[1..] {
        let obj_keys: BTreeSet<String> = obj.keys().cloned().collect();
        if obj_keys != fields {
            return None; // Different key sets
        }
    }

    let fields_vec: Vec<String> = fields.into_iter().collect();

    // Infer a good array name from the fields
    let array_name = infer_array_name(&fields_vec);

    Some((array_name, Schema { fields: fields_vec }))
}

/// Infer a good name for the array based on its fields
fn infer_array_name(fields: &[String]) -> String {
    // Check for cluster-like structure (has label, members, representative_files)
    if fields.iter().any(|f| f == "label")
        && fields.iter().any(|f| f == "members")
        && fields.iter().any(|f| f == "representative_files")
    {
        return "cluster".to_string();
    }

    // Check for code element structure (has qualified_name)
    if fields.iter().any(|f| f == "qualified_name") {
        return "element".to_string();
    }

    // Check for call graph structure (has from and to)
    if fields.iter().any(|f| f == "from") && fields.iter().any(|f| f == "to") {
        return "call".to_string();
    }

    // Check for relationship structure (has source and target)
    if fields.iter().any(|f| f == "source") && fields.iter().any(|f| f == "target") {
        return "rel".to_string();
    }

    // Check for doc structure (has doc and context)
    if fields.iter().any(|f| f == "doc") && fields.iter().any(|f| f == "context") {
        return "doc".to_string();
    }

    // Priority fields that often indicate what the array represents
    let priority_fields = [
        "qualified_name",
        "name",
        "id",
        "type",
        "element",
        "file",
        "function",
        "from",
        "to",
    ];

    for pf in &priority_fields {
        if fields.iter().any(|f| f == pf) {
            // Singularize common plurals
            if pf.ends_with('s') && !pf.ends_with("ss") {
                return pf.trim_end_matches('s').to_string();
            }
            return pf.to_string();
        }
    }

    // Default to first field
    fields
        .first()
        .cloned()
        .unwrap_or_else(|| "item".to_string())
}

/// Represents a TOON schema for array items
#[derive(Debug, Clone)]
struct Schema {
    /// Fields in sorted order for deterministic schema headers
    fields: Vec<String>,
}

impl Schema {
    /// Generate the TOON header for an array with this schema
    /// e.g., "elements[2]{qualified_name,type,language}:"
    fn header(&self, array_name: &str, count: usize) -> String {
        let fields_str = self.fields.join(",");
        format!("{}[{}]{{{}}}:", array_name, count, fields_str)
    }

    /// Format a single item according to this schema
    /// Values are output in the same order as the sorted schema fields
    fn format_item(&self, item: &Value) -> String {
        let obj = item.as_object().unwrap();
        let values: Vec<String> = self
            .fields
            .iter()
            .map(|field| {
                let v = obj.get(field).unwrap();
                value_to_string(v)
            })
            .collect();
        values.join(",")
    }
}

/// Convert a JSON value to its string representation for TOON output
fn value_to_string(v: &Value) -> String {
    match v {
        Value::Null => "null".to_string(),
        Value::Bool(b) => b.to_string(),
        Value::Number(n) => n.to_string(),
        Value::String(s) => {
            // Quote if string contains any special characters
            if s.is_empty()
                || s.contains('\n')
                || s.contains('|')
                || s.contains(',')
                || s.contains(':')
                || s.contains('{')
                || s.contains('}')
                || s.contains('"')
            {
                format!("\"{}\"", s.replace('\"', "\\\""))
            } else {
                s.clone()
            }
        }
        Value::Array(arr) => {
            let items: Vec<String> = arr.iter().map(value_to_string).collect();
            format!("[{}]", items.join(","))
        }
        Value::Object(obj) => {
            let pairs: Vec<String> = obj
                .iter()
                .map(|(k, v)| format!("{}:{}", k, value_to_string(v)))
                .collect();
            format!("{{{}}}", pairs.join(","))
        }
    }
}

/// Token counter - estimates tokens in a string
/// Rough estimate: 1 token ≈ 4 characters
pub fn estimate_tokens(s: &str) -> usize {
    s.len() / 4
}

/// Wrap a JSON response in a Response Format Envelope
pub fn wrap_response(tool_name: &str, response: &Value, use_toon: bool) -> String {
    if use_toon {
        let toon_data = to_toon_string(response);
        let tokens = estimate_tokens(&toon_data);
        let envelope = ResponseEnvelope::new("ok", tool_name, "toon", tokens, toon_data);
        envelope.to_toon_string()
    } else {
        let json_str = serde_json::to_string_pretty(response).unwrap_or_default();
        let tokens = estimate_tokens(&json_str);
        let envelope = ResponseEnvelope::new("ok", tool_name, "json", tokens, json_str);
        envelope.to_json_string()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn test_simple_toon() {
        let val = json!({
            "results": [
                {"qualified_name": "src/main.rs::main", "type": "function"},
                {"qualified_name": "src/lib.rs::init", "type": "function"}
            ],
            "total": 2
        });

        let out = to_toon_string(&val);
        println!("Output:\n{}", out);

        // Fields are sorted alphabetically: qualified_name, type
        // Array is named "element" because objects have qualified_name field
        assert!(
            out.contains("element[2]{qualified_name,type}:"),
            "Schema header not found. Output:\n{}",
            out
        );
        // qualified_name values are quoted because they contain ::
        assert!(
            out.contains("\"src/main.rs::main\",function"),
            "First item not found. Output:\n{}",
            out
        );
        assert!(
            out.contains("\"src/lib.rs::init\",function"),
            "Second item not found. Output:\n{}",
            out
        );
    }

    #[test]
    fn test_toon_with_three_fields() {
        let val = json!({
            "elements": [
                {"qualified_name": "src/main.rs::main", "type": "function", "language": "rust"},
                {"qualified_name": "src/lib.rs::init", "type": "function", "language": "rust"}
            ]
        });

        let out = to_toon_string(&val);
        println!("Output:\n{}", out);

        // Fields sorted alphabetically: language, qualified_name, type
        // Array is named "element" because objects have qualified_name field
        assert!(
            out.contains("element[2]{language,qualified_name,type}:"),
            "Schema header not found. Output:\n{}",
            out
        );
        // Values follow the sorted field order: language, qualified_name (quoted), type
        assert!(
            out.contains("rust,\"src/main.rs::main\",function"),
            "First item not found. Output:\n{}",
            out
        );
    }

    #[test]
    fn test_mixed_array() {
        // Array with non-objects or different schemas falls back to YAML-like
        let val = json!({
            "items": [1, "string", {"key": "value"}]
        });

        let out = to_toon_string(&val);
        println!("Output:\n{}", out);

        // Should use YAML-like format
        assert!(out.contains("- 1"), "Number not found. Output:\n{}", out);
        assert!(
            out.contains("- string"),
            "String not found. Output:\n{}",
            out
        );
    }

    #[test]
    fn test_empty_array() {
        let val = json!({
            "results": []
        });

        let out = to_toon_string(&val);
        println!("Output:\n{}", out);

        assert!(
            out.contains("[]"),
            "Empty array not found. Output:\n{}",
            out
        );
    }

    #[test]
    fn test_response_envelope() {
        let response = json!({
            "results": [
                {"name": "test"}
            ]
        });

        let out = wrap_response("search_code", &response, true);
        println!("Envelope output:\n{}", out);

        assert!(out.contains("status: ok"), "Status not found");
        assert!(out.contains("tool: search_code"), "Tool name not found");
        assert!(out.contains("format: toon"), "Format not found");
        assert!(out.contains("tokens:"), "Tokens not found");
        assert!(out.contains("data:"), "Data not found");
    }

    #[test]
    fn test_nested_object() {
        let val = json!({
            "context": {
                "file": "src/main.rs",
                "elements": [
                    {"name": "main", "type": "function"},
                    {"name": "init", "type": "function"}
                ]
            }
        });

        let out = to_toon_string(&val);
        println!("Output:\n{}", out);

        // Nested array should still use compact format
        // Fields sorted: name, type
        assert!(
            out.contains("name[2]{name,type}:"),
            "Schema header not found. Output:\n{}",
            out
        );
    }

    #[test]
    fn test_impact_radius_style() {
        let val = json!({
            "impact": [
                {"qualified_name": "src/main.rs::main", "type": "function", "severity": "WILL_BREAK", "confidence": 1.0},
                {"qualified_name": "src/lib.rs::init", "type": "function", "severity": "LIKELY_AFFECTED", "confidence": 0.85}
            ]
        });

        let out = to_toon_string(&val);
        println!("Output:\n{}", out);

        // Fields sorted alphabetically: confidence, qualified_name, severity, type
        // Array is named "element" because objects have qualified_name field
        assert!(
            out.contains("element[2]{confidence,qualified_name,severity,type}:"),
            "Schema header not found. Output:\n{}",
            out
        );
        // Values follow sorted field order: confidence, qualified_name, severity, type
        assert!(
            out.contains("1.0,\"src/main.rs::main\",WILL_BREAK,function"),
            "First item not found. Output:\n{}",
            out
        );
    }

    #[test]
    fn test_call_graph_style() {
        let val = json!({
            "calls": [
                {"from": "src/main.rs::main", "to": "src/lib.rs::init", "depth": 1},
                {"from": "src/lib.rs::init", "to": "src/config.rs::load", "depth": 2}
            ]
        });

        let out = to_toon_string(&val);
        println!("Output:\n{}", out);

        // Fields sorted: depth, from, to
        // Array is named "call" because objects have from and to fields
        assert!(
            out.contains("call[2]{depth,from,to}:"),
            "Schema header not found. Output:\n{}",
            out
        );
        // Values follow sorted field order: depth, from, to
        assert!(
            out.contains("1,\"src/main.rs::main\",\"src/lib.rs::init\""),
            "First call not found. Output:\n{}",
            out
        );
    }
}
