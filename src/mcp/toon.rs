use serde_json::Value;

pub fn to_string(value: &Value) -> String {
    let mut output = String::new();
    format_value(value, &mut output, 0, false);
    output.trim().to_string()
}

fn format_value(value: &Value, output: &mut String, indent: usize, is_list_item: bool) {
    let spaces = "  ".repeat(indent);

    match value {
        Value::Null => {
            output.push_str("null\n");
        }
        Value::Bool(b) => {
            output.push_str(&format!("{}\n", b));
        }
        Value::Number(n) => {
            output.push_str(&format!("{}\n", n));
        }
        Value::String(s) => {
            if s.contains('\n') {
                output.push_str("|\n");
                for line in s.lines() {
                    output.push_str(&format!("{}  {}\n", spaces, line));
                }
            } else {
                output.push_str(&format!("{}\n", s));
            }
        }
        Value::Array(arr) => {
            if arr.is_empty() {
                output.push_str("[]\n");
                return;
            }
            if !is_list_item {
                output.push('\n');
            }
            for item in arr {
                output.push_str(&format!("{}- ", if is_list_item { "" } else { &spaces }));
                format_value(item, output, indent + 1, true);
            }
        }
        Value::Object(obj) => {
            if obj.is_empty() {
                output.push_str("{}\n");
                return;
            }
            if !is_list_item {
                output.push('\n');
            }

            let mut first = true;
            // Sort keys to ensure stable output format
            let mut keys: Vec<_> = obj.keys().collect();
            keys.sort();

            for k in keys {
                let v = &obj[k];
                let current_indent = if is_list_item && first { "" } else { &spaces };
                output.push_str(&format!("{}{}: ", current_indent, k));
                if v.is_object() || v.is_array() {
                    format_value(v, output, indent + 1, false);
                } else {
                    format_value(v, output, indent, false);
                }
                first = false;
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn test_toon_format() {
        let val = json!({
            "results": [
                {
                    "name": "foo",
                    "id": 123
                },
                {
                    "name": "bar",
                    "description": "multi\nline\ntext"
                }
            ],
            "total": 2
        });

        let out = to_string(&val);
        assert_eq!(
            out,
            "results: \n  - id: 123\n    name: foo\n  - description: |\n      multi\n      line\n      text\n    name: bar\ntotal: 2"
        );
    }
}
