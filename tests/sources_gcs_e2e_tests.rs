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
use std::sync::{Arc, Mutex};
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

#[tokio::test]
async fn gcs_index_from_bucket_populates_graph() {
    if is_explicit_skip() {
        return;
    }
    let _env_guard = ENV_LOCK.lock().unwrap();
    let emulator = match resolve_or_start_emulator() {
        Some(e) => e,
        None => return,
    };
    let addr = emulator.addr.clone();

    let bucket = "leankg-index-bucket";
    let project = "leankg-index";
    create_bucket(&addr, bucket, project).expect("create_bucket");

    // Upload a small multi-file fixture with functions that should be indexed
    upload_object(
        &addr,
        bucket,
        "main.go",
        b"package main\nfunc main() { println(add(1, 2)) }\nfunc add(a, b int) int { return a + b }\n",
    )
    .expect("upload main.go");
    upload_object(
        &addr,
        bucket,
        "lib/math.go",
        b"package lib\nfunc Mul(a, b int) int { return a * b }\n",
    )
    .expect("upload math.go");

    // Create a temp project and sync then index
    let tmp_dir = TempDir::new().expect("tmpdir");
    let db_path = tmp_dir.path().join(".leankg/leankg.db");
    std::fs::create_dir_all(db_path.parent().unwrap()).expect("create .leankg");

    // Initialize DB schema
    let db = leankg::db::schema::init_db(&db_path).expect("init_db");
    let graph_engine = leankg::graph::GraphEngine::new(std::sync::Arc::new(
        leankg::db::backend::CozoBackend::from_concrete(db.clone()),
    ));

    // Sync GCS source to staging dir
    let staging_root = tmp_dir.path().join(".leankg/sources");
    let src = GcsSource {
        bucket: bucket.to_string(),
        prefix: String::new(),
        auth: None,
    };
    let mut progress = CollectingProgress { messages: vec![] };
    let synced = src
        .sync_to_local(&staging_root, &mut progress)
        .await
        .expect("gcs sync");

    // Index the synced directory
    let mut parser_manager = leankg::indexer::ParserManager::new();
    parser_manager.init_parsers().expect("init parsers");

    // Find files and index them
    let files =
        leankg::indexer::find_files_sync(&synced.to_string_lossy()).expect("find_files_sync");
    let indexed_count = leankg::indexer::index_files_parallel(
        &graph_engine,
        &files,
        false, // verbose
    )
    .expect("index files");

    // Assert the index found elements
    assert!(
        indexed_count > 0,
        "should have indexed at least 1 file, got {}",
        indexed_count
    );
    println!("Indexed {} files from GCS bucket", indexed_count);

    // Query the graph for expected functions
    let elements = graph_engine
        .search_by_name_typed("main", None, 10)
        .expect("search main");
    assert!(
        elements.iter().any(|e| e.name == "main"),
        "expected 'main' function; got: {:?}",
        elements
    );

    let mul_elements = graph_engine
        .search_by_name_typed("Mul", None, 10)
        .expect("search Mul");
    assert!(
        mul_elements.iter().any(|e| e.name == "Mul"),
        "expected 'Mul' function; got: {:?}",
        mul_elements
    );

    let we_started_emulator = emulator.guard.is_some();
    drop(emulator);
    if we_started_emulator {
        std::env::remove_var("STORAGE_EMULATOR_HOST");
    }
}

/// REL-SRC-01: CLI seam — `leankg index --source gs://bucket` must populate
/// the graph with elements (functions + File elements) from the bucket
/// contents, using the real CLI binary so the full index code path
/// (source sync → staging → find_files → index_files_parallel) is covered.
#[tokio::test]
async fn cli_index_gcs_source_populates_graph() {
    if is_explicit_skip() {
        return;
    }
    let _env_guard = ENV_LOCK.lock().unwrap();
    let emulator = match resolve_or_start_emulator() {
        Some(e) => e,
        None => return,
    };
    let addr = emulator.addr.clone();

    let bucket = "leankg-cli-index-bucket";
    let project = "leankg-cli-index";
    create_bucket(&addr, bucket, project).expect("create_bucket");
    upload_object(
        &addr,
        bucket,
        "main.go",
        b"package main\nfunc main() { println(add(1, 2)) }\nfunc add(a, b int) int { return a + b }\n",
    )
    .expect("upload main.go");
    upload_object(
        &addr,
        bucket,
        "lib/math.go",
        b"package lib\nfunc Mul(a, b int) int { return a * b }\n",
    )
    .expect("upload math.go");

    let tmp_dir = TempDir::new().expect("tmpdir");
    let bin = env!("CARGO_BIN_EXE_leankg");

    let output = Command::new(bin)
        .args(["index", "--source", &format!("gs://{}", bucket)])
        .current_dir(tmp_dir.path())
        .env("STORAGE_EMULATOR_HOST", &addr)
        .output()
        .expect("run leankg index --source gs://");

    let stdout = String::from_utf8_lossy(&output.stdout).to_string();
    let stderr = String::from_utf8_lossy(&output.stderr).to_string();
    assert!(
        output.status.success(),
        "index --source failed: stdout={} stderr={}",
        stdout,
        stderr
    );
    assert!(
        stdout.contains("Indexed") && stdout.contains("files"),
        "expected completion line, got: {}",
        stdout
    );

    // The CLI wrote the graph into <project>/.leankg/leankg.db
    let db_path = tmp_dir.path().join(".leankg/leankg.db");
    assert!(
        db_path.is_file(),
        "index --source did not create {}",
        db_path.display()
    );
    let db = leankg::db::schema::init_db(&db_path).expect("init_db");
    let graph_engine = leankg::graph::GraphEngine::new(std::sync::Arc::new(
        leankg::db::backend::CozoBackend::from_concrete(db.clone()),
    ));

    // Functions from both uploaded files must be findable.
    let mains = graph_engine
        .search_by_name_typed("main", Some("function"), 10)
        .expect("search main");
    assert!(
        mains.iter().any(|e| e.name == "main"),
        "expected 'main' function after index --source; got: {:?}",
        mains
    );

    let muls = graph_engine
        .search_by_name_typed("Mul", Some("function"), 10)
        .expect("search Mul");
    assert!(
        muls.iter().any(|e| e.name == "Mul"),
        "expected 'Mul' function after index --source; got: {:?}",
        muls
    );

    // File elements for both uploaded objects must be present.
    let main_file = graph_engine
        .search_by_name_typed("main.go", Some("File"), 10)
        .expect("search main.go");
    assert!(
        main_file.iter().any(|e| e.name == "main.go"),
        "expected main.go File element after index --source; got: {:?}",
        main_file
    );

    let math_file = graph_engine
        .search_by_name_typed("math.go", Some("File"), 10)
        .expect("search math.go");
    assert!(
        math_file.iter().any(|e| e.name == "math.go"),
        "expected math.go File element after index --source; got: {:?}",
        math_file
    );

    let we_started_emulator = emulator.guard.is_some();
    drop(emulator);
    if we_started_emulator {
        std::env::remove_var("STORAGE_EMULATOR_HOST");
    }
}

/// REL-SRC-WATCH-01: e2e — seed the graph via `index --source gs://...`,
/// then run `leankg watch --source gs://... --interval 1`; upload a NEW
/// object to the bucket and assert the watcher's next poll re-indexes and
/// makes the new element queryable. Also asserts FR-SRC-WATCH-05 (watch
/// state persisted in .leankg/source_watch_state.json).
#[tokio::test]
async fn cli_watch_gcs_source_reindexes_on_change() {
    if is_explicit_skip() {
        return;
    }
    let _env_guard = ENV_LOCK.lock().unwrap();
    let emulator = match resolve_or_start_emulator() {
        Some(e) => e,
        None => return,
    };
    let addr = emulator.addr.clone();

    let bucket = "leankg-cli-watch-bucket";
    let project = "leankg-cli-watch";
    create_bucket(&addr, bucket, project).expect("create_bucket");
    upload_object(
        &addr,
        bucket,
        "main.go",
        b"package main\nfunc main() { println(add(1, 2)) }\nfunc add(a, b int) int { return a + b }\n",
    )
    .expect("upload main.go");

    let tmp_dir = TempDir::new().expect("tmpdir");
    let bin = env!("CARGO_BIN_EXE_leankg");
    let source_uri = format!("gs://{}", bucket);

    // Seed the graph (also creates <project>/.leankg so `watch` can start).
    let seed_out = Command::new(bin)
        .args(["index", "--source", &source_uri])
        .current_dir(tmp_dir.path())
        .env("STORAGE_EMULATOR_HOST", &addr)
        .output()
        .expect("seed index");
    assert!(
        seed_out.status.success(),
        "seed index failed: {}",
        String::from_utf8_lossy(&seed_out.stderr)
    );

    // Spawn the remote watcher and capture its stdout for markers.
    let mut child = Command::new(bin)
        .args(["watch", "--source", &source_uri, "--interval", "1"])
        .current_dir(tmp_dir.path())
        .env("STORAGE_EMULATOR_HOST", &addr)
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("spawn leankg watch");

    let lines: Arc<Mutex<Vec<String>>> = Arc::new(Mutex::new(Vec::new()));
    let lines_clone = lines.clone();
    let stdout = child.stdout.take().expect("watch stdout");
    let reader = thread::spawn(move || {
        use std::io::BufRead;
        for line in std::io::BufReader::new(stdout).lines() {
            if let Ok(l) = line {
                lines_clone.lock().unwrap().push(l);
            }
        }
    });

    // Wait until `needle` has been observed at least `min_count` times.
    let wait_for = |needle: &str, min_count: usize, timeout: Duration| {
        let deadline = std::time::Instant::now() + timeout;
        loop {
            let count = lines
                .lock()
                .unwrap()
                .iter()
                .filter(|l| l.contains(needle))
                .count();
            if count >= min_count {
                return true;
            }
            if std::time::Instant::now() >= deadline {
                return false;
            }
            thread::sleep(Duration::from_millis(200));
        }
    };

    // First poll: no persisted fingerprint -> change detected -> index once.
    assert!(
        wait_for("[watch] Indexed", 1, Duration::from_secs(30)),
        "first watch poll did not index; captured: {:?}",
        *lines.lock().unwrap()
    );

    // Let the loop persist watch state before mutating the bucket.
    thread::sleep(Duration::from_secs(2));

    // New object -> etag listing changes -> fingerprint changes.
    upload_object(
        &addr,
        bucket,
        "extra/util.go",
        b"package extra\nfunc Extra() int { return 7 }\n",
    )
    .expect("upload util.go");

    // Second poll must detect the change and re-index.
    assert!(
        wait_for("[watch] Indexed", 2, Duration::from_secs(45)),
        "watch did not re-index after bucket change; captured: {:?}",
        *lines.lock().unwrap()
    );

    // Terminate the watcher before opening the DB (single-writer safety).
    let _ = child.kill();
    let _ = child.wait();
    drop(reader);
    thread::sleep(Duration::from_millis(500));

    // The re-index must have added the new element to the graph.
    let db_path = tmp_dir.path().join(".leankg/leankg.db");
    let db = leankg::db::schema::init_db(&db_path).expect("init_db");
    let graph_engine = leankg::graph::GraphEngine::new(std::sync::Arc::new(
        leankg::db::backend::CozoBackend::from_concrete(db.clone()),
    ));
    let extras = graph_engine
        .search_by_name_typed("Extra", Some("function"), 10)
        .expect("search Extra");
    assert!(
        extras.iter().any(|e| e.name == "Extra"),
        "expected 'Extra' function after watch re-index; got: {:?}",
        extras
    );

    // FR-SRC-WATCH-05: watch state persisted across polls.
    let state_path = tmp_dir.path().join(".leankg/source_watch_state.json");
    assert!(state_path.is_file(), "source_watch_state.json missing");
    let state = std::fs::read_to_string(&state_path).unwrap();
    assert!(
        state.contains("fingerprint"),
        "watch state missing fingerprint: {}",
        state
    );

    let we_started_emulator = emulator.guard.is_some();
    drop(emulator);
    if we_started_emulator {
        std::env::remove_var("STORAGE_EMULATOR_HOST");
    }
}
