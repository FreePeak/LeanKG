//! FR-LSP-D: Cross-file type / symbol registry.
//!
//! Built from indexed [`CodeElement`]s so the hybrid resolver can
//! resolve CALLS edges across files without spawning an LSP server.
//! Keys mirror CBM's `type_registry` idea at LeanKG scale: name,
//! (module/dir, name), and (type, method).

use crate::db::models::CodeElement;
use std::collections::HashMap;
use std::path::Path;

/// One resolvable symbol in the project graph.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SymbolRef {
    pub qualified_name: String,
    pub file_path: String,
    pub name: String,
    pub kind: SymbolKind,
    /// Owning type/class/struct name when this is a method.
    pub type_name: Option<String>,
    pub language: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SymbolKind {
    Function,
    Method,
    Type,
}

/// Project-wide lookup table for hybrid typed resolve.
#[derive(Debug, Default, Clone)]
pub struct TypeRegistry {
    by_name: HashMap<String, Vec<SymbolRef>>,
    by_module_name: HashMap<(String, String), SymbolRef>,
    by_type_method: HashMap<(String, String), SymbolRef>,
    file_module: HashMap<String, String>,
}

impl TypeRegistry {
    /// Populate the registry from extracted elements (functions,
    /// methods, classes, structs, interfaces).
    pub fn from_elements(elements: &[CodeElement]) -> Self {
        let mut reg = Self::default();
        for elem in elements {
            let module = module_key_for_file(&elem.file_path);
            reg.file_module
                .insert(elem.file_path.clone(), module.clone());

            match elem.element_type.as_str() {
                "function" => {
                    let sym = SymbolRef {
                        qualified_name: elem.qualified_name.clone(),
                        file_path: elem.file_path.clone(),
                        name: elem.name.clone(),
                        kind: SymbolKind::Function,
                        type_name: None,
                        language: elem.language.clone(),
                    };
                    reg.insert_symbol(module, sym);
                }
                "method" | "constructor" => {
                    let type_name = elem
                        .parent_qualified
                        .as_ref()
                        .and_then(|p| p.rsplit("::").next())
                        .map(|s| s.to_string())
                        .filter(|s| !s.is_empty() && s != elem.file_path.as_str());
                    let sym = SymbolRef {
                        qualified_name: elem.qualified_name.clone(),
                        file_path: elem.file_path.clone(),
                        name: elem.name.clone(),
                        kind: SymbolKind::Method,
                        type_name: type_name.clone(),
                        language: elem.language.clone(),
                    };
                    if let Some(ref ty) = type_name {
                        reg.by_type_method
                            .insert((ty.clone(), elem.name.clone()), sym.clone());
                    }
                    reg.insert_symbol(module, sym);
                }
                "class" | "struct" | "interface" | "type" | "enum" => {
                    let sym = SymbolRef {
                        qualified_name: elem.qualified_name.clone(),
                        file_path: elem.file_path.clone(),
                        name: elem.name.clone(),
                        kind: SymbolKind::Type,
                        type_name: None,
                        language: elem.language.clone(),
                    };
                    reg.insert_symbol(module, sym);
                }
                _ => {}
            }
        }
        reg
    }

    fn insert_symbol(&mut self, module: String, sym: SymbolRef) {
        self.by_module_name
            .insert((module, sym.name.clone()), sym.clone());
        self.by_name.entry(sym.name.clone()).or_default().push(sym);
    }

    /// Module / directory key for a file path.
    pub fn module_for_file(&self, file_path: &str) -> Option<&str> {
        self.file_module.get(file_path).map(|s| s.as_str())
    }

    /// Exact (module, name) hit — preferred for same-package Go / same-folder TS.
    pub fn lookup_in_module(&self, module: &str, name: &str) -> Option<&SymbolRef> {
        self.by_module_name
            .get(&(module.to_string(), name.to_string()))
    }

    /// Unique project-wide name match (exactly one candidate).
    pub fn lookup_unique_name(&self, name: &str) -> Option<&SymbolRef> {
        let hits = self.by_name.get(name)?;
        if hits.len() == 1 {
            Some(&hits[0])
        } else {
            None
        }
    }

    /// Method on a known type/class/struct.
    pub fn lookup_type_method(&self, type_name: &str, method: &str) -> Option<&SymbolRef> {
        self.by_type_method
            .get(&(type_name.to_string(), method.to_string()))
    }

    /// Candidates sharing a bare name (may be ambiguous).
    pub fn candidates(&self, name: &str) -> &[SymbolRef] {
        self.by_name.get(name).map(|v| v.as_slice()).unwrap_or(&[])
    }

    pub fn len(&self) -> usize {
        self.by_name.values().map(|v| v.len()).sum()
    }

    pub fn is_empty(&self) -> bool {
        self.by_name.is_empty()
    }
}

/// Directory containing the file, normalized — used as a lightweight
/// "package/module" key when we do not parse `package` / `import`.
pub fn module_key_for_file(file_path: &str) -> String {
    Path::new(file_path)
        .parent()
        .map(|p| p.to_string_lossy().to_string())
        .filter(|s| !s.is_empty())
        .unwrap_or_else(|| ".".to_string())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::models::CodeElement;

    fn elem(
        qn: &str,
        etype: &str,
        name: &str,
        file: &str,
        lang: &str,
        parent: Option<&str>,
    ) -> CodeElement {
        CodeElement {
            qualified_name: qn.to_string(),
            element_type: etype.to_string(),
            name: name.to_string(),
            file_path: file.to_string(),
            line_start: 1,
            line_end: 2,
            language: lang.to_string(),
            parent_qualified: parent.map(|s| s.to_string()),
            cluster_id: None,
            cluster_label: None,
            metadata: serde_json::json!({}),
            env: "local".to_string(),
        }
    }

    #[test]
    fn builds_cross_file_function_index() {
        let elements = vec![
            elem("a.go::Helper", "function", "Helper", "pkg/a.go", "go", None),
            elem("b.go::Main", "function", "Main", "pkg/b.go", "go", None),
        ];
        let reg = TypeRegistry::from_elements(&elements);
        assert_eq!(reg.len(), 2);
        let hit = reg.lookup_in_module("pkg", "Helper").unwrap();
        assert_eq!(hit.qualified_name, "a.go::Helper");
        assert!(reg.lookup_unique_name("Helper").is_some());
    }

    #[test]
    fn indexes_type_methods() {
        let elements = vec![
            elem(
                "svc.ts::UserService",
                "class",
                "UserService",
                "src/svc.ts",
                "typescript",
                None,
            ),
            elem(
                "src/svc.ts::UserService::save",
                "method",
                "save",
                "src/svc.ts",
                "typescript",
                Some("src/svc.ts::UserService"),
            ),
        ];
        let reg = TypeRegistry::from_elements(&elements);
        let m = reg.lookup_type_method("UserService", "save").unwrap();
        assert_eq!(m.qualified_name, "src/svc.ts::UserService::save");
    }

    #[test]
    fn ambiguous_name_is_not_unique() {
        let elements = vec![
            elem("a.go::Run", "function", "Run", "pkg1/a.go", "go", None),
            elem("b.go::Run", "function", "Run", "pkg2/b.go", "go", None),
        ];
        let reg = TypeRegistry::from_elements(&elements);
        assert!(reg.lookup_unique_name("Run").is_none());
        assert_eq!(reg.candidates("Run").len(), 2);
    }
}
