//! FR-LSP-A: In-process hybrid typed call resolver (Go / TypeScript MVP).
//!
//! No child process, no JSON-RPC. Uses [`TypeRegistry`] built from the
//! current index pass to upgrade CALLS edges to `resolution_method=typed`.
//! Falls back silently when the registry cannot decide (ambiguous /
//! missing), leaving existing `name` / `unresolved` metadata intact.

use super::type_registry::{module_key_for_file, TypeRegistry};
use crate::config::typed_resolve_enabled;
use crate::db::models::Relationship;
use std::path::Path;

/// Result of a successful hybrid resolution.
#[derive(Debug, Clone, PartialEq)]
pub struct TypedHit {
    pub qualified_name: String,
    pub confidence: f64,
}

/// Resolve a bare callee name in the context of a calling file.
///
/// Order (mirrors CBM hybrid tiers at reduced fidelity):
/// 1. Same module/directory exact name
/// 2. Unique project-wide name
/// 3. Type.method when `receiver` is a known type
pub fn resolve_call(
    registry: &TypeRegistry,
    caller_file: &str,
    callee_name: &str,
    receiver: Option<&str>,
) -> Option<TypedHit> {
    if callee_name.is_empty() || callee_name.starts_with("__unresolved__") {
        let bare = callee_name.trim_start_matches("__unresolved__");
        return resolve_call(registry, caller_file, bare, receiver);
    }

    if let Some(rec) = receiver {
        if let Some(hit) = registry.lookup_type_method(rec, callee_name) {
            return Some(TypedHit {
                qualified_name: hit.qualified_name.clone(),
                confidence: 0.97,
            });
        }
    }

    let module = module_key_for_file(caller_file);
    if let Some(hit) = registry.lookup_in_module(&module, callee_name) {
        // Prefer a different file in the same module (cross-file), but
        // same-file hits still count as typed when the registry knows them.
        return Some(TypedHit {
            qualified_name: hit.qualified_name.clone(),
            confidence: if hit.file_path == caller_file {
                0.94
            } else {
                0.98
            },
        });
    }

    if let Some(hit) = registry.lookup_unique_name(callee_name) {
        return Some(TypedHit {
            qualified_name: hit.qualified_name.clone(),
            confidence: 0.92,
        });
    }

    None
}

/// Infer caller file path from a qualified name (`file::fn` or `file::Class::method`).
pub fn caller_file_from_qn(source_qualified: &str) -> String {
    let path = Path::new(source_qualified);
    // Prefer longest known file-like prefix before `::`
    if let Some(idx) = source_qualified.find("::") {
        let prefix = &source_qualified[..idx];
        if prefix.contains('.') || prefix.contains('/') {
            return prefix.to_string();
        }
    }
    path.to_string_lossy().to_string()
}

fn language_from_file(file_path: &str) -> Option<&'static str> {
    let ext = Path::new(file_path)
        .extension()
        .and_then(|e| e.to_str())
        .unwrap_or("");
    match ext {
        "go" => Some("go"),
        "ts" | "tsx" | "js" | "jsx" => Some("typescript"),
        // FR-B06: Python + Rust join the in-process hybrid resolver. The
        // registry indexes every function/class regardless of language, so
        // module (same-folder) and unique-name hits resolve Python defs and
        // Rust fns without spawning pylsp/rust-analyzer.
        "py" => Some("python"),
        "rs" => Some("rust"),
        "swift" => Some("swift"),
        "m" | "mm" => Some("objc"),
        // `.h` may be C or ObjC; hybrid resolve still runs when typed_resolve
        // includes objc and the index tagged the file as objc.
        "h" => Some("objc"),
        _ => None,
    }
}

fn bare_callee(target: &str) -> &str {
    if let Some(rest) = target.strip_prefix("__unresolved__") {
        return rest;
    }
    target.rsplit("::").next().unwrap_or(target)
}

fn receiver_from_metadata(meta: &serde_json::Value) -> Option<&str> {
    meta.get("receiver")
        .and_then(|v| v.as_str())
        .filter(|s| !s.is_empty())
}

/// Apply hybrid typed resolve to CALLS relationships in place.
///
/// Only touches edges whose source file language is enabled by
/// `typed_resolve` (Go / TypeScript / Swift / Objective-C). Returns the
/// number of edges upgraded to `resolution_method=typed`.
pub fn apply_typed_resolve(
    relationships: &mut [Relationship],
    registry: &TypeRegistry,
    typed_resolve: &str,
) -> usize {
    if registry.is_empty() {
        return 0;
    }
    let mut upgraded = 0;
    for rel in relationships.iter_mut() {
        if rel.rel_type != "calls" {
            continue;
        }
        let caller_file = caller_file_from_qn(&rel.source_qualified);
        let Some(lang) = language_from_file(&caller_file) else {
            continue;
        };
        if !typed_resolve_enabled(typed_resolve, lang) {
            continue;
        }

        // Already typed — leave alone.
        if rel
            .metadata
            .get("resolution_method")
            .and_then(|v| v.as_str())
            == Some("typed")
        {
            continue;
        }

        let callee = bare_callee(&rel.target_qualified);
        let receiver = receiver_from_metadata(&rel.metadata);
        let Some(hit) = resolve_call(registry, &caller_file, callee, receiver) else {
            continue;
        };

        // Do not downgrade a higher-confidence same-file name hit that
        // already points at the same QN — still mark typed.
        rel.target_qualified = hit.qualified_name;
        rel.confidence = hit.confidence.max(rel.confidence);
        let mut meta = rel.metadata.clone();
        if !meta.is_object() {
            meta = serde_json::json!({});
        }
        if let Some(obj) = meta.as_object_mut() {
            obj.insert("resolution_method".to_string(), serde_json::json!("typed"));
            obj.insert("is_resolved".to_string(), serde_json::json!(true));
            obj.insert("hybrid_tier".to_string(), serde_json::json!("in_process"));
        }
        rel.metadata = meta;
        upgraded += 1;
    }
    upgraded
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::models::CodeElement;
    use crate::lsp::type_registry::TypeRegistry;

    fn elem(qn: &str, etype: &str, name: &str, file: &str, lang: &str) -> CodeElement {
        CodeElement {
            qualified_name: qn.to_string(),
            element_type: etype.to_string(),
            name: name.to_string(),
            file_path: file.to_string(),
            line_start: 1,
            line_end: 2,
            language: lang.to_string(),
            parent_qualified: None,
            cluster_id: None,
            cluster_label: None,
            metadata: serde_json::json!({}),
            env: "local".to_string(),
        }
    }

    #[test]
    fn go_cross_file_same_package_is_typed() {
        let elements = vec![
            elem("pkg/a.go::Helper", "function", "Helper", "pkg/a.go", "go"),
            elem("pkg/b.go::Main", "function", "Main", "pkg/b.go", "go"),
        ];
        let reg = TypeRegistry::from_elements(&elements);
        let hit = resolve_call(&reg, "pkg/b.go", "Helper", None).unwrap();
        assert_eq!(hit.qualified_name, "pkg/a.go::Helper");
        assert!(hit.confidence >= 0.95);
    }

    #[test]
    fn typescript_unique_export_is_typed() {
        let elements = vec![elem(
            "src/util.ts::format",
            "function",
            "format",
            "src/util.ts",
            "typescript",
        )];
        let reg = TypeRegistry::from_elements(&elements);
        let hit = resolve_call(&reg, "src/app.ts", "format", None).unwrap();
        assert_eq!(hit.qualified_name, "src/util.ts::format");
    }

    #[test]
    fn python_same_module_def_is_typed() {
        // FR-B06: Python joins the in-process hybrid resolver via `py` ext.
        let elements = vec![
            elem(
                "pkg/utils.py::helper",
                "function",
                "helper",
                "pkg/utils.py",
                "python",
            ),
            elem(
                "pkg/app.py::main",
                "function",
                "main",
                "pkg/app.py",
                "python",
            ),
        ];
        let reg = TypeRegistry::from_elements(&elements);
        let hit = resolve_call(&reg, "pkg/app.py", "helper", None).unwrap();
        assert_eq!(hit.qualified_name, "pkg/utils.py::helper");
        assert!(hit.confidence >= 0.95);
    }

    #[test]
    fn rust_same_module_fn_is_typed() {
        // FR-B06: Rust joins via `rs` ext; unique-name cross-file also works.
        let elements = vec![
            elem(
                "src/math.rs::square",
                "function",
                "square",
                "src/math.rs",
                "rust",
            ),
            elem("src/main.rs::run", "function", "run", "src/main.rs", "rust"),
        ];
        let reg = TypeRegistry::from_elements(&elements);
        let hit = resolve_call(&reg, "src/main.rs", "square", None).unwrap();
        assert_eq!(hit.qualified_name, "src/math.rs::square");
        assert!(hit.confidence >= 0.95);
    }

    #[test]
    fn apply_typed_resolve_upgrades_python_and_rust_calls() {
        // FR-B06: `typed_resolve=all` must upgrade py/rs CALLS edges too.
        let elements = vec![
            elem(
                "pkg/utils.py::helper",
                "function",
                "helper",
                "pkg/utils.py",
                "python",
            ),
            elem(
                "pkg/app.py::main",
                "function",
                "main",
                "pkg/app.py",
                "python",
            ),
            elem(
                "src/math.rs::square",
                "function",
                "square",
                "src/math.rs",
                "rust",
            ),
        ];
        let reg = TypeRegistry::from_elements(&elements);
        let mut rels = vec![
            Relationship {
                id: None,
                source_qualified: "pkg/app.py::main".to_string(),
                target_qualified: "__unresolved__helper".to_string(),
                rel_type: "calls".to_string(),
                confidence: 0.5,
                metadata: serde_json::json!({"resolution_method": "unresolved"}),
                env: "local".to_string(),
            },
            Relationship {
                id: None,
                source_qualified: "src/main.rs::run".to_string(),
                target_qualified: "__unresolved__square".to_string(),
                rel_type: "calls".to_string(),
                confidence: 0.5,
                metadata: serde_json::json!({"resolution_method": "unresolved"}),
                env: "local".to_string(),
            },
        ];
        let n = apply_typed_resolve(&mut rels, &reg, "all");
        assert_eq!(n, 2, "python + rust CALLS both upgrade under all");
        for rel in &rels {
            assert_eq!(
                rel.metadata["resolution_method"].as_str(),
                Some("typed"),
                "{}",
                rel.source_qualified
            );
        }
        assert_eq!(
            rels[0].target_qualified, "pkg/utils.py::helper",
            "python target resolves"
        );
        assert_eq!(
            rels[1].target_qualified, "src/math.rs::square",
            "rust target resolves via unique name"
        );
    }

    #[test]
    fn apply_typed_resolve_upgrades_unresolved_calls() {
        let elements = vec![
            elem("pkg/a.go::Helper", "function", "Helper", "pkg/a.go", "go"),
            elem("pkg/b.go::Main", "function", "Main", "pkg/b.go", "go"),
        ];
        let reg = TypeRegistry::from_elements(&elements);
        let mut rels = vec![Relationship {
            id: None,
            source_qualified: "pkg/b.go::Main".to_string(),
            target_qualified: "__unresolved__Helper".to_string(),
            rel_type: "calls".to_string(),
            confidence: 0.5,
            metadata: serde_json::json!({"resolution_method": "unresolved"}),
            env: "local".to_string(),
        }];
        let n = apply_typed_resolve(&mut rels, &reg, "go,ts");
        assert_eq!(n, 1);
        assert_eq!(rels[0].target_qualified, "pkg/a.go::Helper");
        assert_eq!(
            rels[0].metadata["resolution_method"].as_str(),
            Some("typed")
        );
    }

    #[test]
    fn typed_resolve_off_skips_upgrade() {
        let elements = vec![elem(
            "pkg/a.go::Helper",
            "function",
            "Helper",
            "pkg/a.go",
            "go",
        )];
        let reg = TypeRegistry::from_elements(&elements);
        let mut rels = vec![Relationship {
            id: None,
            source_qualified: "pkg/b.go::Main".to_string(),
            target_qualified: "__unresolved__Helper".to_string(),
            rel_type: "calls".to_string(),
            confidence: 0.5,
            metadata: serde_json::json!({"resolution_method": "unresolved"}),
            env: "local".to_string(),
        }];
        assert_eq!(apply_typed_resolve(&mut rels, &reg, "off"), 0);
    }

    #[test]
    fn apply_typed_resolve_upgrades_swift_calls() {
        let elements = vec![
            elem(
                "App/Session.swift::authenticate",
                "method",
                "authenticate",
                "App/Session.swift",
                "swift",
            ),
            elem(
                "App/Session.swift::start",
                "method",
                "start",
                "App/Session.swift",
                "swift",
            ),
        ];
        let reg = TypeRegistry::from_elements(&elements);
        let mut rels = vec![Relationship {
            id: None,
            source_qualified: "App/Session.swift::start".to_string(),
            target_qualified: "App/Session.swift::authenticate".to_string(),
            rel_type: "calls".to_string(),
            confidence: 0.7,
            metadata: serde_json::json!({"resolution_method": "name"}),
            env: "local".to_string(),
        }];
        let n = apply_typed_resolve(&mut rels, &reg, "go,ts,swift,objc");
        assert_eq!(n, 1, "swift CALLS should upgrade when enabled");
        assert_eq!(
            rels[0].metadata["resolution_method"].as_str(),
            Some("typed")
        );
    }

    #[test]
    fn apply_typed_resolve_upgrades_objc_calls() {
        let elements = vec![
            elem("Greeter.m::setup", "method", "setup", "Greeter.m", "objc"),
            elem(
                "Greeter.m::sayHello",
                "method",
                "sayHello",
                "Greeter.m",
                "objc",
            ),
        ];
        let reg = TypeRegistry::from_elements(&elements);
        let mut rels = vec![Relationship {
            id: None,
            source_qualified: "Greeter.m::sayHello".to_string(),
            target_qualified: "Greeter.m::setup".to_string(),
            rel_type: "calls".to_string(),
            confidence: 0.7,
            metadata: serde_json::json!({"resolution_method": "name"}),
            env: "local".to_string(),
        }];
        let n = apply_typed_resolve(&mut rels, &reg, "swift,objc");
        assert_eq!(n, 1);
        assert_eq!(
            rels[0].metadata["resolution_method"].as_str(),
            Some("typed")
        );
    }

    #[test]
    fn never_spawns_process_on_resolve() {
        // Smoke: pure function path — no process handles involved.
        let reg = TypeRegistry::default();
        assert!(resolve_call(&reg, "x.go", "missing", None).is_none());
    }
}
