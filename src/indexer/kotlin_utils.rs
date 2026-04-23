/// Find the byte offset past the closing `}` of a class/object body starting at `class_start`.
///
/// Walks `content[class_start..]` counting brace depth. Returns `content.len()` if no balanced
/// closing brace is found (e.g. truncated source). Uses `char_indices()` so byte offsets remain
/// valid for subsequent string slicing.
pub fn find_class_body_end(content: &str, class_start: usize) -> usize {
    let after = &content[class_start..];
    let mut depth = 0i32;
    let mut found_open = false;
    for (i, ch) in after.char_indices() {
        match ch {
            '{' => {
                depth += 1;
                found_open = true;
            }
            '}' => {
                depth -= 1;
                if found_open && depth == 0 {
                    return class_start + i + 1;
                }
            }
            _ => {}
        }
    }
    content.len()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_simple_class_body() {
        let content = "class Foo { val x = 1 }";
        let start = content.find("class Foo").unwrap();
        let end = find_class_body_end(content, start);
        assert_eq!(&content[start..end], "class Foo { val x = 1 }");
    }

    #[test]
    fn test_nested_braces() {
        let content = "class Foo { fun bar() { } }";
        let start = content.find("class Foo").unwrap();
        let end = find_class_body_end(content, start);
        assert_eq!(&content[start..end], "class Foo { fun bar() { } }");
    }

    #[test]
    fn test_no_closing_brace() {
        let content = "class Foo { val x = 1";
        let end = find_class_body_end(content, 0);
        assert_eq!(end, content.len());
    }
}
