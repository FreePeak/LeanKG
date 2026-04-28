use regex::Regex;
use std::sync::OnceLock;

#[derive(Debug, Clone)]
pub struct Signature {
    pub kind: &'static str,
    pub name: String,
    pub params: String,
    pub return_type: String,
    pub is_async: bool,
    pub is_exported: bool,
    pub indent: usize,
}

impl Signature {
    pub fn to_compact(&self) -> String {
        let export = if self.is_exported { "⊛ " } else { "" };
        let async_prefix = if self.is_async { "async " } else { "" };

        match self.kind {
            "fn" | "method" => {
                let ret = if self.return_type.is_empty() {
                    String::new()
                } else {
                    format!(" → {}", self.return_type)
                };
                let indent = " ".repeat(self.indent);
                format!(
                    "{indent}fn {async_prefix}{export}{}({}){}",
                    self.name, self.params, ret
                )
            }
            "class" | "struct" => format!("cl {export}{}", self.name),
            "interface" | "trait" => format!("if {export}{}", self.name),
            "type" => format!("ty {export}{}", self.name),
            "enum" => format!("en {export}{}", self.name),
            "const" | "let" | "var" => {
                let ty = if self.return_type.is_empty() {
                    String::new()
                } else {
                    format!(":{}", self.return_type)
                };
                format!("val {export}{}{ty}", self.name)
            }
            _ => format!("{} {}", self.kind, self.name),
        }
    }

    pub fn to_tdd(&self) -> String {
        let vis = if self.is_exported { "+" } else { "-" };
        let a = if self.is_async { "~" } else { "" };

        match self.kind {
            "fn" | "method" => {
                let ret = if self.return_type.is_empty() {
                    String::new()
                } else {
                    format!("→{}", compact_type(&self.return_type))
                };
                let params = tdd_params(&self.params);
                let indent = if self.indent > 0 { " " } else { "" };
                format!("{indent}{a}λ{vis}{}({params}){ret}", self.name)
            }
            "class" | "struct" => format!("§{vis}{}", self.name),
            "interface" | "trait" => format!("∂{vis}{}", self.name),
            "type" => format!("τ{vis}{}", self.name),
            "enum" => format!("ε{vis}{}", self.name),
            "const" | "let" | "var" => {
                let ty = if self.return_type.is_empty() {
                    String::new()
                } else {
                    format!(":{}", compact_type(&self.return_type))
                };
                format!("ν{vis}{}{ty}", self.name)
            }
            _ => format!(
                "{}{vis}{}",
                self.kind.chars().next().unwrap_or('?'),
                self.name
            ),
        }
    }
}

static FN_RE: OnceLock<Regex> = OnceLock::new();
static CLASS_RE: OnceLock<Regex> = OnceLock::new();
static IFACE_RE: OnceLock<Regex> = OnceLock::new();
static TYPE_RE: OnceLock<Regex> = OnceLock::new();
static CONST_RE: OnceLock<Regex> = OnceLock::new();
static RUST_FN_RE: OnceLock<Regex> = OnceLock::new();
static RUST_STRUCT_RE: OnceLock<Regex> = OnceLock::new();
static RUST_ENUM_RE: OnceLock<Regex> = OnceLock::new();
static RUST_TRAIT_RE: OnceLock<Regex> = OnceLock::new();
static RUST_IMPL_RE: OnceLock<Regex> = OnceLock::new();

fn fn_re() -> &'static Regex {
    FN_RE.get_or_init(|| {
        Regex::new(r"^(\s*)(export\s+)?(async\s+)?function\s+(\w+)\s*(?:<[^>]*>)?\s*\(([^)]*)\)(?:\s*:\s*([^\{]+))?\s*\{?")
            .unwrap()
    })
}

fn class_re() -> &'static Regex {
    CLASS_RE.get_or_init(|| Regex::new(r"^(\s*)(export\s+)?(abstract\s+)?class\s+(\w+)").unwrap())
}

fn iface_re() -> &'static Regex {
    IFACE_RE.get_or_init(|| Regex::new(r"^(\s*)(export\s+)?interface\s+(\w+)").unwrap())
}

fn type_re() -> &'static Regex {
    TYPE_RE.get_or_init(|| Regex::new(r"^(\s*)(export\s+)?type\s+(\w+)").unwrap())
}

fn const_re() -> &'static Regex {
    CONST_RE.get_or_init(|| {
        Regex::new(r"^(\s*)(export\s+)?(const|let|var)\s+(\w+)(?:\s*:\s*(\w+))?").unwrap()
    })
}

fn rust_fn_re() -> &'static Regex {
    RUST_FN_RE.get_or_init(|| {
        Regex::new(r"^(\s*)(pub\s+)?(async\s+)?fn\s+(\w+)\s*(?:<[^>]*>)?\s*\(([^)]*)\)(?:\s*->\s*([^\{]+))?\s*\{?")
            .unwrap()
    })
}

fn rust_struct_re() -> &'static Regex {
    RUST_STRUCT_RE.get_or_init(|| Regex::new(r"^(\s*)(pub\s+)?struct\s+(\w+)").unwrap())
}

fn rust_enum_re() -> &'static Regex {
    RUST_ENUM_RE.get_or_init(|| Regex::new(r"^(\s*)(pub\s+)?enum\s+(\w+)").unwrap())
}

fn rust_trait_re() -> &'static Regex {
    RUST_TRAIT_RE.get_or_init(|| Regex::new(r"^(\s*)(pub\s+)?trait\s+(\w+)").unwrap())
}

fn rust_impl_re() -> &'static Regex {
    RUST_IMPL_RE.get_or_init(|| Regex::new(r"^(\s*)impl\s+(?:(\w+)\s+for\s+)?(\w+)").unwrap())
}

pub fn extract_signatures(content: &str, file_ext: &str) -> Vec<Signature> {
    match file_ext {
        "rs" => extract_rust_signatures(content),
        "ts" | "tsx" | "js" | "jsx" | "svelte" | "vue" | "mjs" | "cjs" => {
            extract_ts_signatures(content)
        }
        "py" | "pyi" => extract_python_signatures(content),
        "go" => extract_go_signatures(content),
        "java" => extract_java_signatures(content),
        _ => extract_generic_signatures(content),
    }
}

fn extract_ts_signatures(content: &str) -> Vec<Signature> {
    let mut sigs = Vec::new();

    for line in content.lines() {
        let trimmed = line.trim();
        if trimmed.starts_with("//") || trimmed.starts_with("/*") || trimmed.starts_with('*') {
            continue;
        }

        if let Some(caps) = fn_re().captures(line) {
            let indent = caps.get(1).map_or(0, |m| m.as_str().len());
            sigs.push(Signature {
                kind: if indent > 0 { "method" } else { "fn" },
                name: caps[4].to_string(),
                params: compact_params(&caps[5]),
                return_type: caps
                    .get(6)
                    .map_or(String::new(), |m| m.as_str().trim().to_string()),
                is_async: caps.get(3).is_some(),
                is_exported: caps.get(2).is_some(),
                indent: if indent > 0 { 2 } else { 0 },
            });
        } else if let Some(caps) = class_re().captures(line) {
            sigs.push(Signature {
                kind: "class",
                name: caps[4].to_string(),
                params: String::new(),
                return_type: String::new(),
                is_async: false,
                is_exported: caps.get(2).is_some(),
                indent: 0,
            });
        } else if let Some(caps) = iface_re().captures(line) {
            sigs.push(Signature {
                kind: "interface",
                name: caps[3].to_string(),
                params: String::new(),
                return_type: String::new(),
                is_async: false,
                is_exported: caps.get(2).is_some(),
                indent: 0,
            });
        } else if let Some(caps) = type_re().captures(line) {
            sigs.push(Signature {
                kind: "type",
                name: caps[3].to_string(),
                params: String::new(),
                return_type: String::new(),
                is_async: false,
                is_exported: caps.get(2).is_some(),
                indent: 0,
            });
        } else if let Some(caps) = const_re().captures(line) {
            if caps.get(2).is_some() {
                sigs.push(Signature {
                    kind: "const",
                    name: caps[4].to_string(),
                    params: String::new(),
                    return_type: caps
                        .get(5)
                        .map_or(String::new(), |m| m.as_str().to_string()),
                    is_async: false,
                    is_exported: true,
                    indent: 0,
                });
            }
        }
    }

    sigs
}

fn extract_rust_signatures(content: &str) -> Vec<Signature> {
    let mut sigs = Vec::new();

    for line in content.lines() {
        let trimmed = line.trim();
        if trimmed.starts_with("//") || trimmed.starts_with("///") {
            continue;
        }

        if let Some(caps) = rust_fn_re().captures(line) {
            let indent = caps.get(1).map_or(0, |m| m.as_str().len());
            sigs.push(Signature {
                kind: if indent > 0 { "method" } else { "fn" },
                name: caps[4].to_string(),
                params: compact_params(&caps[5]),
                return_type: caps
                    .get(6)
                    .map_or(String::new(), |m| m.as_str().trim().to_string()),
                is_async: caps.get(3).is_some(),
                is_exported: caps.get(2).is_some(),
                indent: if indent > 0 { 2 } else { 0 },
            });
        } else if let Some(caps) = rust_struct_re().captures(line) {
            sigs.push(Signature {
                kind: "struct",
                name: caps[3].to_string(),
                params: String::new(),
                return_type: String::new(),
                is_async: false,
                is_exported: caps.get(2).is_some(),
                indent: 0,
            });
        } else if let Some(caps) = rust_enum_re().captures(line) {
            sigs.push(Signature {
                kind: "enum",
                name: caps[3].to_string(),
                params: String::new(),
                return_type: String::new(),
                is_async: false,
                is_exported: caps.get(2).is_some(),
                indent: 0,
            });
        } else if let Some(caps) = rust_trait_re().captures(line) {
            sigs.push(Signature {
                kind: "trait",
                name: caps[3].to_string(),
                params: String::new(),
                return_type: String::new(),
                is_async: false,
                is_exported: caps.get(2).is_some(),
                indent: 0,
            });
        } else if let Some(caps) = rust_impl_re().captures(line) {
            let trait_name = caps.get(2).map(|m| m.as_str());
            let type_name = &caps[3];
            let name = if let Some(t) = trait_name {
                format!("{t} for {type_name}")
            } else {
                type_name.to_string()
            };
            sigs.push(Signature {
                kind: "class",
                name,
                params: String::new(),
                return_type: String::new(),
                is_async: false,
                is_exported: false,
                indent: 0,
            });
        }
    }

    sigs
}

fn extract_python_signatures(content: &str) -> Vec<Signature> {
    static PY_FN: OnceLock<Regex> = OnceLock::new();
    static PY_CLASS: OnceLock<Regex> = OnceLock::new();

    let mut sigs = Vec::new();
    let py_fn = PY_FN.get_or_init(|| {
        Regex::new(r"^(\s*)(async\s+)?def\s+(\w+)\s*\(([^)]*)\)(?:\s*->\s*([^:]+))?").unwrap()
    });
    let py_class = PY_CLASS.get_or_init(|| Regex::new(r"^(\s*)class\s+(\w+)").unwrap());

    for line in content.lines() {
        if let Some(caps) = py_fn.captures(line) {
            let indent = caps.get(1).map_or(0, |m| m.as_str().len());
            sigs.push(Signature {
                kind: if indent > 0 { "method" } else { "fn" },
                name: caps[3].to_string(),
                params: compact_params(&caps[4]),
                return_type: caps
                    .get(5)
                    .map_or(String::new(), |m| m.as_str().to_string()),
                is_async: caps.get(2).is_some(),
                is_exported: !caps[3].starts_with('_'),
                indent: if indent > 0 { 2 } else { 0 },
            });
        } else if let Some(caps) = py_class.captures(line) {
            sigs.push(Signature {
                kind: "class",
                name: caps[2].to_string(),
                params: String::new(),
                return_type: String::new(),
                is_async: false,
                is_exported: !caps[2].starts_with('_'),
                indent: 0,
            });
        }
    }

    sigs
}

fn extract_go_signatures(content: &str) -> Vec<Signature> {
    static GO_FN: OnceLock<Regex> = OnceLock::new();
    static GO_TYPE: OnceLock<Regex> = OnceLock::new();

    let mut sigs = Vec::new();
    let go_fn = GO_FN.get_or_init(|| Regex::new(r"^func\s+(?:\((\w+)\s+\*?(\w+)\)\s+)?(\w+)\s*\(([^)]*)\)(?:\s*(?:\(([^)]*)\)|([^{]+)))?\s*\{?").unwrap());
    let go_type =
        GO_TYPE.get_or_init(|| Regex::new(r"^type\s+(\w+)\s+(struct|interface)").unwrap());

    for line in content.lines() {
        if let Some(caps) = go_fn.captures(line) {
            let is_method = caps.get(2).is_some();
            sigs.push(Signature {
                kind: if is_method { "method" } else { "fn" },
                name: caps[3].to_string(),
                params: compact_params(&caps[4]),
                return_type: caps
                    .get(5)
                    .or(caps.get(6))
                    .map_or(String::new(), |m| m.as_str().to_string()),
                is_async: false,
                is_exported: caps[3].starts_with(char::is_uppercase),
                indent: if is_method { 2 } else { 0 },
            });
        } else if let Some(caps) = go_type.captures(line) {
            sigs.push(Signature {
                kind: if &caps[2] == "struct" {
                    "struct"
                } else {
                    "interface"
                },
                name: caps[1].to_string(),
                params: String::new(),
                return_type: String::new(),
                is_async: false,
                is_exported: caps[1].starts_with(char::is_uppercase),
                indent: 0,
            });
        }
    }

    sigs
}

fn extract_java_signatures(content: &str) -> Vec<Signature> {
    static JAVA_METHOD: OnceLock<Regex> = OnceLock::new();
    static JAVA_CLASS: OnceLock<Regex> = OnceLock::new();

    let mut sigs = Vec::new();
    let j_method = JAVA_METHOD.get_or_init(|| {
        Regex::new(r"^(\s*)(?:(?:public|private|protected|static|final|native|synchronized|abstract|transient)\s+)*([\w<>\[\]]+)\s+(\w+)\s*\(([^)]*)\)\s*(?:throws\s+[\w,\s]+)?\s*\{?").unwrap()
    });
    let j_class = JAVA_CLASS.get_or_init(|| {
        Regex::new(r"^(\s*)(?:(?:public|private|protected|static|final|abstract)\s+)*(class|interface|enum|record)\s+(\w+)").unwrap()
    });

    for line in content.lines() {
        let trimmed = line.trim();
        if trimmed.starts_with("//")
            || trimmed.starts_with('*')
            || trimmed.starts_with("/*")
            || trimmed.starts_with("import ")
            || trimmed.starts_with("package ")
        {
            continue;
        }

        if let Some(caps) = j_class.captures(line) {
            sigs.push(Signature {
                kind: if &caps[2] == "interface" {
                    "interface"
                } else if &caps[2] == "enum" {
                    "enum"
                } else {
                    "class"
                },
                name: caps[3].to_string(),
                params: String::new(),
                return_type: String::new(),
                is_async: false,
                is_exported: line.contains("public "),
                indent: 0,
            });
        } else if let Some(caps) = j_method.captures(line) {
            let indent = caps.get(1).map_or(0, |m| m.as_str().len());
            // Ignore common keywords that might match method return types like return, new, etc
            let ret = &caps[2];
            if ["return", "new", "throw"].contains(&ret) {
                continue;
            }

            sigs.push(Signature {
                kind: "method",
                name: caps[3].to_string(),
                params: compact_params(&caps[4]),
                return_type: ret.to_string(),
                is_async: false,
                is_exported: line.contains("public "),
                indent: if indent > 0 { 2 } else { 0 },
            });
        }
    }
    sigs
}

pub(crate) fn compact_params(params: &str) -> String {
    if params.trim().is_empty() {
        return String::new();
    }
    params
        .split(',')
        .map(|p| {
            let p = p.trim();
            if let Some((name, ty)) = p.split_once(':') {
                let name = name.trim();
                let ty = ty.trim();
                let short = match ty {
                    "string" | "String" | "&str" | "str" => ":s",
                    "number" | "i32" | "i64" | "u32" | "u64" | "usize" | "f32" | "f64" => ":n",
                    "boolean" | "bool" => ":b",
                    _ => return format!("{name}:{ty}"),
                };
                format!("{name}{short}")
            } else {
                p.to_string()
            }
        })
        .collect::<Vec<_>>()
        .join(", ")
}

fn compact_type(ty: &str) -> String {
    match ty.trim() {
        "String" | "string" | "&str" | "str" => "s".to_string(),
        "bool" | "boolean" => "b".to_string(),
        "i32" | "i64" | "u32" | "u64" | "usize" | "f32" | "f64" | "number" => "n".to_string(),
        "void" | "()" => "∅".to_string(),
        other => {
            if other.starts_with("Vec<") || other.starts_with("Array<") {
                let inner = other
                    .trim_start_matches("Vec<")
                    .trim_start_matches("Array<")
                    .trim_end_matches('>');
                format!("[{}]", compact_type(inner))
            } else if other.starts_with("Option<") || other.starts_with("Maybe<") {
                let inner = other
                    .trim_start_matches("Option<")
                    .trim_start_matches("Maybe<")
                    .trim_end_matches('>');
                format!("?{}", compact_type(inner))
            } else if other.starts_with("Result<") {
                "R".to_string()
            } else if other.starts_with("impl ") {
                other.trim_start_matches("impl ").to_string()
            } else {
                other.to_string()
            }
        }
    }
}

fn tdd_params(params: &str) -> String {
    if params.trim().is_empty() {
        return String::new();
    }
    params
        .split(',')
        .map(|p| {
            let p = p.trim();
            if p.starts_with('&') {
                let rest = p.trim_start_matches("&mut ").trim_start_matches('&');
                if let Some((name, ty)) = rest.split_once(':') {
                    format!("&{}:{}", name.trim(), compact_type(ty))
                } else {
                    p.to_string()
                }
            } else if let Some((name, ty)) = p.split_once(':') {
                format!("{}:{}", name.trim(), compact_type(ty))
            } else if p == "self" || p == "&self" || p == "&mut self" {
                "⊕".to_string()
            } else {
                p.to_string()
            }
        })
        .collect::<Vec<_>>()
        .join(",")
}

fn extract_generic_signatures(content: &str) -> Vec<Signature> {
    static RE_FUNC: OnceLock<Regex> = OnceLock::new();
    static RE_CLASS: OnceLock<Regex> = OnceLock::new();

    let re_func = RE_FUNC.get_or_init(|| {
        Regex::new(r"^\s*(?:(?:public|private|protected|static|async|abstract|virtual|override|final|def|func|fun|fn)\s+)+(\w+)\s*\(").unwrap()
    });
    let re_class = RE_CLASS.get_or_init(|| {
        Regex::new(r"^\s*(?:(?:public|private|protected|abstract|final|sealed|partial)\s+)*(?:class|struct|enum|interface|trait|module|object|record)\s+(\w+)").unwrap()
    });

    let mut sigs = Vec::new();
    for line in content.lines() {
        let trimmed = line.trim();
        if trimmed.is_empty()
            || trimmed.starts_with("//")
            || trimmed.starts_with('#')
            || trimmed.starts_with("/*")
            || trimmed.starts_with('*')
        {
            continue;
        }
        if let Some(caps) = re_class.captures(trimmed) {
            sigs.push(Signature {
                kind: "type",
                name: caps[1].to_string(),
                params: String::new(),
                return_type: String::new(),
                is_async: false,
                is_exported: true,
                indent: 0,
            });
        } else if let Some(caps) = re_func.captures(trimmed) {
            sigs.push(Signature {
                kind: "fn",
                name: caps[1].to_string(),
                params: String::new(),
                return_type: String::new(),
                is_async: trimmed.contains("async"),
                is_exported: true,
                indent: 0,
            });
        }
    }
    sigs
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_rust_extract() {
        let code = r#"
pub struct User {}
pub async fn login(user: String) -> Result<(), Error> {
    println!("hi");
}
        "#;
        let sigs = extract_signatures(code, "rs");
        assert_eq!(sigs.len(), 2);
        assert_eq!(sigs[0].kind, "struct");
        assert_eq!(sigs[0].name, "User");
        assert_eq!(sigs[1].kind, "fn");
        assert_eq!(sigs[1].name, "login");
        assert_eq!(sigs[1].to_tdd(), "~λ+login(user:s)→R");
    }

    #[test]
    fn test_java_extract() {
        let code = r#"
package com.test;
public class JavaApp {
    private void privateHelp(int count) {
        return;
    }
    public static String getApp() {
        return "App";
    }
}
        "#;
        let sigs = extract_signatures(code, "java");
        assert_eq!(sigs.len(), 3);
        assert_eq!(sigs[0].kind, "class");
        assert_eq!(sigs[0].name, "JavaApp");
        assert_eq!(sigs[1].kind, "method");
        assert_eq!(sigs[1].name, "privateHelp");
        assert_eq!(sigs[2].kind, "method");
        assert_eq!(sigs[2].name, "getApp");
    }
}
