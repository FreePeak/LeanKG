//! E2E: FR-LSP-A/C/D — hybrid typed resolve produces resolution_method=typed
//! for Go and TypeScript cross-file calls without spawning an LSP server.

use leankg::db::models::{CodeElement, Relationship};
use leankg::lsp::{apply_typed_resolve, TypeRegistry};
use std::fs;
use std::path::PathBuf;
use std::process::Command;
use std::time::{SystemTime, UNIX_EPOCH};

fn scratch_dir(label: &str) -> PathBuf {
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_nanos();
    let dir = std::env::temp_dir().join(format!("leankg-hybrid-lsp-{label}-{nanos}"));
    let _ = fs::remove_dir_all(&dir);
    fs::create_dir_all(&dir).unwrap();
    dir
}

fn elem(qn: &str, etype: &str, name: &str, file: &str, lang: &str) -> CodeElement {
    CodeElement {
        qualified_name: qn.to_string(),
        element_type: etype.to_string(),
        name: name.to_string(),
        file_path: file.to_string(),
        line_start: 1,
        line_end: 5,
        language: lang.to_string(),
        parent_qualified: None,
        cluster_id: None,
        cluster_label: None,
        metadata: serde_json::json!({}),
        env: "local".to_string(),
    }
}

#[test]
fn e2e_go_cross_file_typed_edges() {
    let elements = vec![
        elem(
            "pkg/helper.go::FormatName",
            "function",
            "FormatName",
            "pkg/helper.go",
            "go",
        ),
        elem("pkg/main.go::main", "function", "main", "pkg/main.go", "go"),
    ];
    let reg = TypeRegistry::from_elements(&elements);
    assert!(reg.len() >= 2);

    let mut rels = vec![Relationship {
        id: None,
        source_qualified: "pkg/main.go::main".to_string(),
        target_qualified: "__unresolved__FormatName".to_string(),
        rel_type: "calls".to_string(),
        confidence: 0.5,
        metadata: serde_json::json!({"resolution_method": "unresolved"}),
        env: "local".to_string(),
    }];

    let n = apply_typed_resolve(&mut rels, &reg, "go,ts");
    assert_eq!(n, 1, "expected one typed upgrade");
    assert_eq!(rels[0].target_qualified, "pkg/helper.go::FormatName");
    assert_eq!(
        rels[0].metadata["resolution_method"].as_str(),
        Some("typed")
    );
    assert_eq!(rels[0].metadata["hybrid_tier"].as_str(), Some("in_process"));
}

#[test]
fn e2e_typescript_cross_module_typed_edges() {
    let elements = vec![
        elem(
            "src/lib/util.ts::formatDate",
            "function",
            "formatDate",
            "src/lib/util.ts",
            "typescript",
        ),
        elem(
            "src/app.ts::boot",
            "function",
            "boot",
            "src/app.ts",
            "typescript",
        ),
    ];
    let reg = TypeRegistry::from_elements(&elements);
    let mut rels = vec![Relationship {
        id: None,
        source_qualified: "src/app.ts::boot".to_string(),
        target_qualified: "__unresolved__formatDate".to_string(),
        rel_type: "calls".to_string(),
        confidence: 0.5,
        metadata: serde_json::json!({"resolution_method": "unresolved"}),
        env: "local".to_string(),
    }];
    let n = apply_typed_resolve(&mut rels, &reg, "go,ts");
    assert_eq!(n, 1);
    assert_eq!(rels[0].target_qualified, "src/lib/util.ts::formatDate");
    assert_eq!(
        rels[0].metadata["resolution_method"].as_str(),
        Some("typed")
    );
}

#[test]
fn e2e_init_with_lsp_writes_prefab_block() {
    let dir = scratch_dir("init");
    let bin = env!("CARGO_BIN_EXE_leankg");
    let status = Command::new(bin)
        .args(["init", "--path", ".leankg", "--with-lsp"])
        .current_dir(&dir)
        .status()
        .expect("run leankg init --with-lsp");
    assert!(status.success(), "init --with-lsp failed");

    let yaml_path = dir.join("leankg.yaml");
    assert!(yaml_path.exists(), "leankg.yaml missing");
    let yaml = fs::read_to_string(&yaml_path).unwrap();
    assert!(
        yaml.contains("typed_resolve:") && yaml.contains("go,ts"),
        "expected typed_resolve: go,ts in yaml:\n{yaml}"
    );
    assert!(
        yaml.contains("lsp:") && yaml.contains("gopls"),
        "expected prefab lsp gopls entry:\n{yaml}"
    );
    assert!(
        yaml.contains("typescript-language-server") || yaml.contains("typescript:"),
        "expected typescript server in prefab:\n{yaml}"
    );
}

#[test]
fn e2e_hybrid_path_does_not_require_gopls_binary() {
    // Prove typed edges without PATH having gopls — pure registry path.
    let elements = vec![elem("svc/a.go::Ping", "function", "Ping", "svc/a.go", "go")];
    let reg = TypeRegistry::from_elements(&elements);
    let mut rels = vec![Relationship {
        id: None,
        source_qualified: "svc/b.go::Run".to_string(),
        target_qualified: "__unresolved__Ping".to_string(),
        rel_type: "calls".to_string(),
        confidence: 0.4,
        metadata: serde_json::json!({}),
        env: "local".to_string(),
    }];
    assert_eq!(apply_typed_resolve(&mut rels, &reg, "all"), 1);
    assert_eq!(rels[0].metadata["resolution_method"], "typed");
}
