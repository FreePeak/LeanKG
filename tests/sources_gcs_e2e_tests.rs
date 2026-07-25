//! End-to-end Docker-backed tests for the GCS source using
//! [`fsouza/fake-gcs-server`](https://github.com/fsouza/fake-gcs-server).
//!
//! These tests require Docker on the host. They auto-skip when:
//! - Docker CLI is missing (`which docker` returns nothing), or
//! - `LEANKG_GCS_E2E=0` is set explicitly.
//!
//! Two modes:
//! - **Local**: when `STORAGE_EMULATOR_HOST` is unset, the test starts the
//!   emulator as a one-shot docker container on a random local port.
//! - **External**: when `STORAGE_EMULATOR_HOST` is set (e.g. the CI service
//!   container, or a developer already running the emulator), the test
//!   reuses that endpoint and skips the Docker boot path. The caller is
//!   responsible for keeping the emulator alive (CI service containers
//!   satisfy this).

use std::io::Read;
use std::net::TcpListener;
use std::process::{Command, Stdio};
use std::sync::Mutex;
use std::thread;
use std::time::Duration;

use leankg::sources::gcs::GcsSource;
use leankg::sources::{ProgressReporter, Source};
use tempfile::TempDir;

static ENV_LOCK: Mutex<()> = Mutex::new(());

struct CollectingProgress {
    messages: Vec<String>,
}

impl ProgressReporter for CollectingProgress {
    fn report(&mut self, message: &str) {
        self.messages.push(message.to_string());
    }
}

fn docker_available() -> bool {
    Command::new("docker")
        .args(["version"])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .map(|s| s.success())
        .unwrap_or(false)
}

fn is_explicit_skip() -> bool {
    std::env::var("LEANKG_GCS_E2E")
        .map(|v| v == "0")
        .unwrap_or(false)
}

fn pick_free_port() -> u16 {
    let listener = TcpListener::bind("127.0.0.1:0").expect("bind ephemeral port");
    let port = listener.local_addr().expect("local_addr").port();
    drop(listener);
    port
}

struct DockerGuard {
    container: String,
}

impl Drop for DockerGuard {
    fn drop(&mut self) {
        let _ = Command::new("docker")
            .args(["rm", "-f", &self.container])
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .status();
    }
}

fn start_fake_gcs_server(host: &str, port: u16) -> Result<DockerGuard, String> {
    let container = format!("leankg-fake-gcs-{}-{}", std::process::id(), port);
    let addr = format!("http://{}:{}", host, port);

    let mut child = Command::new("docker")
        .args([
            "run",
            "-d",
            "--rm",
            "--name",
            &container,
            "-p",
            &format!("{}:{}", port, port),
            "fsouza/fake-gcs-server",
            "-scheme",
            "http",
            "-port",
            &port.to_string(),
            "-external-url",
            &addr,
        ])
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|e| format!("docker run failed to spawn: {}", e))?;

    let mut stdout = String::new();
    child
        .stdout
        .as_mut()
        .unwrap()
        .read_to_string(&mut stdout)
        .map_err(|e| format!("read docker run stdout: {}", e))?;
    let mut stderr = String::new();
    child
        .stderr
        .as_mut()
        .unwrap()
        .read_to_string(&mut stderr)
        .ok();
    let status = child
        .wait()
        .map_err(|e| format!("docker run wait: {}", e))?;
    if !status.success() {
        return Err(format!(
            "docker run failed ({}): stdout={} stderr={}",
            status, stdout, stderr
        ));
    }
    let container_id = stdout.trim().to_string();
    if container_id.is_empty() {
        return Err(format!(
            "docker run returned empty container id: {}",
            stderr
        ));
    }

    let deadline = std::time::Instant::now() + Duration::from_secs(15);
    loop {
        if std::time::Instant::now() >= deadline {
            return Err(format!(
                "fake-gcs-server did not become ready at {} within 15s",
                addr
            ));
        }
        if let Ok(child) = Command::new("curl")
            .args(["-fsS", "--max-time", "2", &format!("{}/storage/v1/b", addr)])
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .status()
        {
            if child.success() {
                break;
            }
        }
        thread::sleep(Duration::from_millis(250));
    }

    Ok(DockerGuard {
        container: container_id,
    })
}

fn create_bucket(addr: &str, bucket: &str, project: &str) -> Result<(), String> {
    let url = format!("{}/storage/v1/b?project={}", addr, project);
    let body = format!("{{\"name\":\"{}\"}}", bucket);
    let status = Command::new("curl")
        .args([
            "-fsS",
            "-X",
            "POST",
            "-H",
            "Content-Type: application/json",
            "-d",
            &body,
            &url,
        ])
        .stdout(Stdio::null())
        .stderr(Stdio::piped())
        .status()
        .map_err(|e| format!("create_bucket curl failed: {}", e))?;
    if !status.success() {
        return Err(format!("create_bucket returned {}", status));
    }
    Ok(())
}

fn upload_object(addr: &str, bucket: &str, name: &str, body: &[u8]) -> Result<(), String> {
    use std::io::Write;
    let url = format!(
        "{}/upload/storage/v1/b/{}/o?uploadType=media&name={}",
        addr,
        bucket,
        name.replace('/', "%2F")
    );
    let mut child = Command::new("curl")
        .args([
            "-fsS",
            "-X",
            "POST",
            "-H",
            "Content-Type: text/plain",
            "--data-binary",
            "@-",
            &url,
        ])
        .stdin(Stdio::piped())
        .stdout(Stdio::null())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|e| format!("upload_object curl spawn: {}", e))?;
    child
        .stdin
        .as_mut()
        .unwrap()
        .write_all(body)
        .map_err(|e| format!("upload_object stdin: {}", e))?;
    let status = child
        .wait()
        .map_err(|e| format!("upload_object wait: {}", e))?;
    if !status.success() {
        return Err(format!("upload_object {} returned {}", name, status));
    }
    Ok(())
}

/// Parse `http://host:port` into `(host, port)`.
fn parse_emulator_addr(addr: &str) -> Option<(String, u16)> {
    let trimmed = addr
        .strip_prefix("http://")
        .or_else(|| addr.strip_prefix("https://"))?;
    let (host_port, _path) = match trimmed.find('/') {
        Some(idx) => (&trimmed[..idx], &trimmed[idx..]),
        None => (trimmed, ""),
    };
    let (host, port_str) = match host_port.rfind(':') {
        Some(idx) => (&host_port[..idx], &host_port[idx + 1..]),
        None => return None,
    };
    if host.is_empty() || port_str.is_empty() {
        return None;
    }
    port_str.parse::<u16>().ok().map(|p| (host.to_string(), p))
}

struct EmulatorHandle {
    /// `http://host:port` address used to talk to fake-gcs-server.
    addr: String,
    /// Cleanup guard. `None` when the emulator is owned externally (CI).
    guard: Option<DockerGuard>,
}

/// Resolve which emulator address the test should hit. Spawns a fresh
/// docker container when `STORAGE_EMULATOR_HOST` is unset.
fn resolve_or_start_emulator() -> Option<EmulatorHandle> {
    if let Ok(existing) = std::env::var("STORAGE_EMULATOR_HOST") {
        let trimmed = existing.trim_end_matches('/').to_string();
        let parsed =
            parse_emulator_addr(&trimmed).expect("STORAGE_EMULATOR_HOST must be http://host:port");
        eprintln!(
            "[e2e] using pre-configured STORAGE_EMULATOR_HOST={}",
            trimmed
        );
        return Some(EmulatorHandle {
            addr: trimmed,
            guard: None,
        });
    }
    if !docker_available() {
        eprintln!("[e2e] docker not available and STORAGE_EMULATOR_HOST unset; skipping");
        return None;
    }
    let host = "127.0.0.1".to_string();
    let port = pick_free_port();
    let addr = format!("http://{}:{}", host, port);
    std::env::set_var("STORAGE_EMULATOR_HOST", &addr);
    match start_fake_gcs_server(&host, port) {
        Ok(g) => {
            eprintln!("[e2e] emulator ready on {}:{}", host, port);
            Some(EmulatorHandle {
                addr,
                guard: Some(g),
            })
        }
        Err(e) => {
            eprintln!("[e2e] skipping gcs e2e: {}", e);
            std::env::remove_var("STORAGE_EMULATOR_HOST");
            None
        }
    }
}

#[tokio::test]
async fn gcs_source_syncs_objects_from_fake_emulator() {
    if is_explicit_skip() {
        eprintln!("LEANKG_GCS_E2E=0 set; skipping");
        return;
    }
    let _env_guard = ENV_LOCK.lock().unwrap();
    let emulator = match resolve_or_start_emulator() {
        Some(e) => e,
        None => return,
    };
    let addr = emulator.addr.clone();

    let bucket = "leankg-e2e-bucket";
    let project = "leankg-e2e";
    create_bucket(&addr, bucket, project).expect("create_bucket");
    upload_object(&addr, bucket, "hello.go", b"package main\nfunc main() {}\n")
        .expect("upload hello.go");
    upload_object(
        &addr,
        bucket,
        "internal/lib.go",
        b"package internal\nfunc Add(a, b int) int { return a + b }\n",
    )
    .expect("upload lib.go");

    let src = GcsSource {
        bucket: bucket.to_string(),
        prefix: String::new(),
        auth: None,
    };
    let staging = TempDir::new().expect("staging tmpdir");
    let mut progress = CollectingProgress { messages: vec![] };

    let synced = src
        .sync_to_local(staging.path(), &mut progress)
        .await
        .expect("sync_to_local");

    assert!(synced.join("hello.go").is_file(), "hello.go missing");
    assert!(
        synced.join("internal/lib.go").is_file(),
        "internal/lib.go missing"
    );

    let hello = std::fs::read_to_string(synced.join("hello.go")).unwrap();
    assert_eq!(hello, "package main\nfunc main() {}\n");

    let lib = std::fs::read_to_string(synced.join("internal/lib.go")).unwrap();
    assert_eq!(
        lib,
        "package internal\nfunc Add(a, b int) int { return a + b }\n"
    );

    assert!(
        progress
            .messages
            .iter()
            .any(|m| m.contains("found 2 objects")),
        "missing expected progress message, got: {:?}",
        progress.messages
    );

    let we_started_emulator = emulator.guard.is_some();
    drop(emulator);
    if we_started_emulator {
        std::env::remove_var("STORAGE_EMULATOR_HOST");
    }
}

#[tokio::test]
async fn gcs_source_with_prefix_filters_objects() {
    if is_explicit_skip() {
        return;
    }
    let _env_guard = ENV_LOCK.lock().unwrap();
    let emulator = match resolve_or_start_emulator() {
        Some(e) => e,
        None => return,
    };
    let addr = emulator.addr.clone();

    let bucket = "leankg-prefix-bucket";
    let project = "leankg-prefix";
    create_bucket(&addr, bucket, project).expect("create_bucket");
    upload_object(&addr, bucket, "keep/a.go", b"package keep\n").expect("upload a");
    upload_object(&addr, bucket, "skip/b.go", b"package skip\n").expect("upload b");

    let src = GcsSource {
        bucket: bucket.to_string(),
        prefix: "keep/".to_string(),
        auth: None,
    };
    let staging = TempDir::new().expect("staging");
    let mut progress = CollectingProgress { messages: vec![] };

    let synced = src
        .sync_to_local(staging.path(), &mut progress)
        .await
        .expect("sync_to_local");

    assert!(synced.join("a.go").is_file(), "keep/a.go missing");
    assert!(
        !synced.join("skip").exists() && !synced.join("skip/b.go").exists(),
        "skip/* should be filtered out"
    );

    let we_started_emulator = emulator.guard.is_some();
    drop(emulator);
    if we_started_emulator {
        std::env::remove_var("STORAGE_EMULATOR_HOST");
    }
}
