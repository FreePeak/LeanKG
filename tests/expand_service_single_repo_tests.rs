// FR-MG-03 / US-MG-02 integration: single-repo projects are treated as a single
// service — double-clicking the root loads the ENTIRE service tree in one
// `/api/graph/expand-service?path=.` call. Multi-repo layouts must NOT force the
// full dump at the root; nested services only appear when expanded themselves.
//
// Uses the real `leankg` CLI (init + index + serve) against a TempDir project,
// then drives the HTTP endpoint like ui-v2 does (`expandService('.', true)`).

use std::path::Path;
use std::process::{Child, Command, Stdio};
use std::time::Duration;

use tempfile::TempDir;

const BIN: &str = env!("CARGO_BIN_EXE_leankg");

fn write_source(dir: &Path, rel: &str, content: &str) {
    let path = dir.join(rel);
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    std::fs::write(path, content).unwrap();
}

fn run_cli(cwd: &Path, args: &[&str]) -> String {
    let out = Command::new(BIN)
        .current_dir(cwd)
        .args(args)
        .output()
        .unwrap_or_else(|e| panic!("failed to run leankg {:?}: {}", args, e));
    assert!(
        out.status.success(),
        "leankg {:?} failed: stdout={} stderr={}",
        args,
        String::from_utf8_lossy(&out.stdout),
        String::from_utf8_lossy(&out.stderr)
    );
    String::from_utf8_lossy(&out.stdout).into_owned()
}

fn free_port() -> u16 {
    std::net::TcpListener::bind(("127.0.0.1", 0))
        .unwrap()
        .local_addr()
        .unwrap()
        .port()
}

/// `leankg serve --project <root> --port <port>` as a child process.
fn spawn_serve(project_root: &Path, port: u16) -> Child {
    Command::new(BIN)
        .args([
            "serve",
            "--project",
            &project_root.to_string_lossy(),
            "--port",
            &port.to_string(),
        ])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .expect("failed to spawn leankg serve")
}

fn wait_until_ready(port: u16) {
    let client = reqwest::blocking::Client::new();
    for _ in 0..200 {
        if client
            .get(format!(
                "http://127.0.0.1:{}/api/graph/expand-service",
                port
            ))
            .query(&[("path", ".")])
            .send()
            .is_ok()
        {
            return;
        }
        std::thread::sleep(Duration::from_millis(100));
    }
    panic!("leankg serve did not come up on :{}", port);
}

fn expand(port: u16, path: &str, all: bool) -> serde_json::Value {
    let client = reqwest::blocking::Client::new();
    let resp = client
        .get(format!(
            "http://127.0.0.1:{}/api/graph/expand-service",
            port
        ))
        .query(&[("path", path), ("all", if all { "true" } else { "false" })])
        .send()
        .unwrap();
    assert!(
        resp.status().is_success(),
        "expand-service?path={} status {}",
        path,
        resp.status()
    );
    resp.json().unwrap()
}

fn node_paths(json: &serde_json::Value) -> Vec<String> {
    json["data"]["nodes"]
        .as_array()
        .map(|nodes| {
            nodes
                .iter()
                .filter_map(|n| n["properties"]["filePath"].as_str().map(String::from))
                .collect()
        })
        .unwrap_or_default()
}

fn node_names(json: &serde_json::Value) -> Vec<String> {
    json["data"]["nodes"]
        .as_array()
        .map(|nodes| {
            nodes
                .iter()
                .filter_map(|n| n["properties"]["name"].as_str().map(String::from))
                .collect()
        })
        .unwrap_or_default()
}

struct RunningServer {
    child: Child,
    port: u16,
}

impl Drop for RunningServer {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

fn index_and_serve(project_root: &Path) -> RunningServer {
    // `leankg init` writes leankg.yaml so `index`/`serve --project` resolve the root.
    run_cli(project_root, &["init"]);
    run_cli(project_root, &["index", "."]);
    let port = free_port();
    let child = spawn_serve(project_root, port);
    let server = RunningServer { child, port };
    wait_until_ready(port);
    server
}

#[test]
fn single_repo_root_expand_loads_entire_tree_in_one_call() {
    let tmp = TempDir::new().unwrap();
    let root = tmp.path();
    // No .git anywhere -> single-repo layout (FR-MG-03): root = the "service".
    write_source(
        root,
        "src/main.rs",
        "fn main() { let x = helper(); println!(\"{}\", x); }\n",
    );
    write_source(
        root,
        "src/util/helper.rs",
        "pub fn helper() -> u32 { 42 }\n",
    );

    let server = index_and_serve(root);

    // FR-MG-03 forcing: no all=true param needed — single-repo root auto-enables
    // full content. (The ui-v2 root double-click additionally sends all=true.)
    let json = expand(server.port, ".", false);
    assert_eq!(
        json["success"], true,
        "single-repo root expand failed: {}",
        json
    );
    let paths = node_paths(&json);
    let names = node_names(&json);

    // The entire service tree is returned in a single API call: nested folders,
    // files, and functions are all present without further drilling.
    assert!(
        paths.iter().any(|p| p == "./src/util/helper.rs"),
        "nested file ./src/util/helper.rs missing from root expand (paths: {:?})",
        paths
    );
    assert!(
        paths.iter().any(|p| p == "./src/main.rs"),
        "file ./src/main.rs missing from root expand (paths: {:?})",
        paths
    );
    assert!(
        paths.iter().any(|p| p == "./src/util"),
        "nested dir ./src/util missing from root expand (paths: {:?})",
        paths
    );
    assert!(
        names.iter().any(|n| n == "helper"),
        "nested fn 'helper' missing from root expand (names: {:?})",
        names
    );
    assert!(
        names.iter().any(|n| n == "main"),
        "fn 'main' missing from root expand (names: {:?})",
        names
    );
}

#[test]
fn multi_repo_root_expand_does_not_dump_nested_services() {
    let tmp = TempDir::new().unwrap();
    let root = tmp.path();
    // Multi-repo layout: root .git + nested service .git dirs -> root is NOT a service.
    std::fs::create_dir_all(root.join(".git")).unwrap();
    std::fs::create_dir_all(root.join("svc-a").join(".git")).unwrap();
    std::fs::create_dir_all(root.join("svc-b").join(".git")).unwrap();
    write_source(root, "svc-a/src/lib.rs", "pub fn svc_a_fn() -> u32 { 1 }\n");

    let server = index_and_serve(root);

    // Root expansion must NOT auto-force the full all_content dump of nested
    // trees (only single-repo roots get the auto-enable treatment).
    let json = expand(server.port, ".", false);
    assert_eq!(
        json["success"], true,
        "multi-repo root expand failed: {}",
        json
    );
    let root_paths = node_paths(&json);
    assert!(
        !root_paths.iter().any(|p| p == "./svc-a/src/lib.rs"),
        "multi-repo root expand leaked nested service file (paths: {:?})",
        root_paths
    );
    // Root-level elements are still visible.
    assert!(
        root_paths.iter().any(|p| p == "./svc-a"),
        "multi-repo root expand missing root-level dir ./svc-a (paths: {:?})",
        root_paths
    );

    // Expanding the service node itself still loads its full tree (ui-v2 sends all=true).
    let svc = expand(server.port, "./svc-a", true);
    assert_eq!(svc["success"], true, "svc-a expand failed: {}", svc);
    let svc_paths = node_paths(&svc);
    assert!(
        svc_paths.iter().any(|p| p == "./svc-a/src/lib.rs"),
        "svc-a expand missing its file (paths: {:?})",
        svc_paths
    );
}

#[test]
fn single_repo_root_with_only_root_git_still_loads_entire_tree() {
    let tmp = TempDir::new().unwrap();
    let root = tmp.path();
    // A repo checked out normally: only the root has .git -> still single-repo.
    std::fs::create_dir_all(root.join(".git")).unwrap();
    write_source(root, "src/app.rs", "pub fn app_start() -> u8 { 0 }\n");

    let server = index_and_serve(root);

    let json = expand(server.port, ".", true);
    assert_eq!(
        json["success"], true,
        "single-repo (root .git) expand failed: {}",
        json
    );
    let paths = node_paths(&json);
    assert!(
        paths.iter().any(|p| p == "./src/app.rs"),
        "root-git repo expand missing ./src/app.rs (paths: {:?})",
        paths
    );
}
