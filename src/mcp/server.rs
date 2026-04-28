#![allow(dead_code)]
use crate::db::schema::init_db;
use crate::graph::GraphEngine;
use crate::mcp::auth::AuthConfig;
use crate::mcp::handler::ToolHandler;
use crate::mcp::tools::ToolRegistry;
use crate::mcp::tracker::WriteTracker;
use crate::mcp::watcher::start_watcher;
use crate::orchestrator::intent::IntentParser;
use axum::{
    body::Body,
    extract::State,
    http::{header, HeaderMap, Method, StatusCode},
    response::Response,
    routing::{get, post},
    Router,
};
// use futures_util::StreamExt;  // Reserved for future streaming support
use parking_lot::RwLock;
use rmcp::handler::server::ServerHandler;
use rmcp::model::{CallToolRequestParams, CallToolResult, Content, ListToolsResult, Tool};
use rmcp::service::{serve_server, RoleServer};
use rmcp::transport::stdio;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::net::SocketAddr;
use std::path::PathBuf;
use std::sync::Arc;
use tokio::sync::RwLock as TokioRwLock;
use tower_http::cors::{Any, CorsLayer};

pub struct MCPServer {
    auth_config: Arc<TokioRwLock<AuthConfig>>,
    db_path: Arc<RwLock<PathBuf>>,
    graph_engine: Arc<parking_lot::Mutex<Option<GraphEngine>>>,
    graph_engine_cache: Arc<RwLock<HashMap<PathBuf, GraphEngine>>>,
    watch_path: Option<PathBuf>,
    write_tracker: Arc<WriteTracker>,
    intent_parser: IntentParser,
}

impl std::fmt::Debug for MCPServer {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("MCPServer")
            .field("db_path", &self.db_path)
            .finish()
    }
}

impl Clone for MCPServer {
    fn clone(&self) -> Self {
        Self {
            auth_config: self.auth_config.clone(),
            db_path: self.db_path.clone(),
            graph_engine: self.graph_engine.clone(),
            graph_engine_cache: self.graph_engine_cache.clone(),
            watch_path: self.watch_path.clone(),
            write_tracker: self.write_tracker.clone(),
            intent_parser: IntentParser::new(),
        }
    }
}

impl MCPServer {
    pub fn new(db_path: std::path::PathBuf) -> Self {
        let effective_db_path = Self::resolve_project_root(db_path);
        Self {
            auth_config: Arc::new(TokioRwLock::new(AuthConfig::default())),
            db_path: Arc::new(RwLock::new(effective_db_path)),
            graph_engine: Arc::new(parking_lot::Mutex::new(None)),
            graph_engine_cache: Arc::new(RwLock::new(HashMap::new())),
            watch_path: None,
            write_tracker: Arc::new(WriteTracker::new()),
            intent_parser: IntentParser::new(),
        }
    }

    pub fn new_with_watch(db_path: std::path::PathBuf, watch_path: std::path::PathBuf) -> Self {
        let effective_db_path = Self::resolve_project_root(db_path);
        Self {
            auth_config: Arc::new(TokioRwLock::new(AuthConfig::default())),
            db_path: Arc::new(RwLock::new(effective_db_path)),
            graph_engine: Arc::new(parking_lot::Mutex::new(None)),
            graph_engine_cache: Arc::new(RwLock::new(HashMap::new())),
            watch_path: Some(watch_path),
            write_tracker: Arc::new(WriteTracker::new()),
            intent_parser: IntentParser::new(),
        }
    }

    /// Read leankg.yaml and resolve project root with fallback chain:
    /// 1. project_path from config (if exists and valid)
    /// 2. project.root relative path resolution
    /// 3. Original db_path as fallback
    fn resolve_project_root(db_path: std::path::PathBuf) -> std::path::PathBuf {
        let config_path = db_path.join("leankg.yaml");
        if !config_path.exists() {
            return db_path;
        }

        let content = match std::fs::read_to_string(&config_path) {
            Ok(c) => c,
            Err(_) => return db_path,
        };

        let config: crate::config::ProjectConfig = match serde_yaml::from_str(&content) {
            Ok(c) => c,
            Err(_) => return db_path,
        };

        // 1. Check project_path first (absolute path stored at init time)
        if let Some(project_path) = config.project.project_path {
            let db_at_path = project_path.join(".leankg");
            if db_at_path.is_dir() {
                tracing::info!(
                    "Using project_path from leankg.yaml: {}",
                    db_at_path.display()
                );
                return db_at_path;
            } else {
                tracing::warn!(
                    "project_path in leankg.yaml points to non-existent directory: {}. Searching for project...",
                    project_path.display()
                );
            }
        }

        // 2. If root is not ".", check if that directory has its own .leankg
        let root = &config.project.root;
        if root.as_os_str() != "." && root.as_os_str() != "" {
            // Resolve root relative to db_path's parent (project root)
            let project_root = db_path.parent().unwrap_or(&db_path);
            let resolved_root = if root.is_absolute() {
                root.clone()
            } else {
                project_root.join(root)
            };

            // Check if root or its parent has .leankg
            let alternative_db = resolved_root.join(".leankg");
            if alternative_db.is_dir() && alternative_db != db_path {
                tracing::info!(
                    "Using project root from leankg.yaml: {}",
                    alternative_db.display()
                );
                return alternative_db;
            }

            // Check parent of resolved root
            if let Some(parent) = resolved_root.parent() {
                let parent_db = parent.join(".leankg");
                if parent_db.is_dir() && parent_db != db_path {
                    tracing::info!(
                        "Using parent project from leankg.yaml: {}",
                        parent_db.display()
                    );
                    return parent_db;
                }
            }
        }

        // 3. Fall back to original db_path
        tracing::debug!("Using default db_path: {}", db_path.display());
        db_path
    }

    pub fn db_path(&self) -> std::sync::Arc<parking_lot::RwLock<std::path::PathBuf>> {
        self.db_path.clone()
    }

    fn get_db_path(&self) -> std::path::PathBuf {
        self.db_path.read().clone()
    }

    fn find_leankg_for_path(path: &str) -> Option<PathBuf> {
        let path = if path.starts_with('/') {
            PathBuf::from(path)
        } else {
            std::env::current_dir().ok()?.join(path)
        };

        for ancestor in path.ancestors() {
            let leankg_path = ancestor.join(".leankg");
            if leankg_path.is_dir() {
                return Some(leankg_path);
            }
            if ancestor.join("leankg.yaml").exists() {
                return Some(leankg_path);
            }
        }
        None
    }

    fn get_graph_engine_for_path(&self, file_path: Option<&String>) -> Result<GraphEngine, String> {
        let project_db_path = if let Some(fp) = file_path {
            if let Some(leankg_path) = Self::find_leankg_for_path(fp.as_str()) {
                tracing::debug!(
                    "Routing query for '{}' to database at {}",
                    fp,
                    leankg_path.display()
                );
                leankg_path
            } else {
                tracing::debug!("No .leankg found for '{}', using default db_path", fp);
                self.get_db_path()
            }
        } else {
            Self::find_leankg_for_path(".").unwrap_or_else(|| self.get_db_path())
        };

        {
            let cache = self.graph_engine_cache.read();
            if let Some(ge) = cache.get(&project_db_path) {
                return Ok(ge.clone());
            }
        }

        let project_db_path = project_db_path
            .canonicalize()
            .or_else(|_| std::env::current_dir().map(|d| d.join(&project_db_path)))
            .map_err(|e| format!("Failed to resolve db path: {}", e))?;

        if !project_db_path.exists() {
            return Err(
                "LeanKG not initialized. No .leankg directory found. Run 'leankg init' first."
                    .to_string(),
            );
        }

        tracing::debug!("Initializing database at: {}", project_db_path.display());
        let db = init_db(&project_db_path).map_err(|e| format!("Database error: {}", e))?;
        let ge = GraphEngine::with_persistence(db);

        {
            let mut cache = self.graph_engine_cache.write();
            cache.insert(project_db_path.clone(), ge.clone());
        }

        Ok(ge)
    }

    pub async fn auth_config_read(&self) -> tokio::sync::RwLockReadGuard<'_, AuthConfig> {
        self.auth_config.read().await
    }

    fn get_graph_engine(&self) -> Result<GraphEngine, String> {
        {
            let guard = self.graph_engine.lock();
            if let Some(ref ge) = *guard {
                return Ok(ge.clone());
            }
        }
        let db_path = self.get_db_path();
        let db_path = db_path
            .canonicalize()
            .or_else(|_| std::env::current_dir().map(|d| d.join(&db_path)))
            .map_err(|e| format!("Failed to resolve db path: {}", e))?;

        if !db_path.exists() {
            return Err(format!(
                "LeanKG not initialized in this directory. Run 'leankg init' first, or ensure a .leankg directory exists at: {}",
                db_path.display()
            ));
        }

        tracing::debug!("Initializing database at: {}", db_path.display());
        let db = init_db(&db_path).map_err(|e| format!("Database error: {}", e))?;
        let ge = GraphEngine::with_persistence(db);
        {
            let mut guard = self.graph_engine.lock();
            *guard = Some(ge.clone());
        }
        Ok(ge)
    }

    pub async fn serve_stdio(&self) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        if let Err(e) = self.auto_init_if_needed().await {
            tracing::warn!(
                "Auto-init skipped: {}. Server will operate in uninitialized state.",
                e
            );
        }

        // Ensure API server is running (starts it if not)
        match self.ensure_api_server_running().await {
            Ok(port) => {
                tracing::info!("API server ready on port {}", port);
            }
            Err(e) => {
                tracing::warn!(
                    "Failed to ensure API server running: {}. Continuing anyway.",
                    e
                );
            }
        }

        if let Some(ref watch_path) = self.watch_path {
            let db_path = self.get_db_path();
            let watch_path = watch_path.clone();
            tokio::spawn(async move {
                let (tx, rx) = tokio::sync::mpsc::channel(100);
                start_watcher(db_path, watch_path, rx).await;
                let _ = tx; // silence unused warning
            });
            tracing::info!(
                "Auto-indexing enabled for {}",
                self.watch_path
                    .as_ref()
                    .unwrap_or(&std::path::PathBuf::from("?"))
                    .display()
            );
        }
        let transport = stdio();
        let _running = serve_server(self.clone(), transport).await?;
        futures_util::future::pending().await
    }

    /// Check if the API server is running on the given port by connecting to it
    async fn is_api_server_running(port: u16) -> bool {
        let addr = SocketAddr::from(([127, 0, 0, 1], port));
        tokio::net::TcpStream::connect(addr).await.is_ok()
    }

    /// Ensure the API server is running, starting it if not
    async fn ensure_api_server_running(
        &self,
    ) -> Result<u16, Box<dyn std::error::Error + Send + Sync>> {
        // Get port from environment or use default 9699
        let requested_port = std::env::var("LEANKG_API_PORT")
            .ok()
            .and_then(|p| p.parse().ok())
            .unwrap_or(9699);

        // First check if API server is already running on the requested/default port
        if Self::is_api_server_running(requested_port).await {
            tracing::info!("API server already running on port {}", requested_port);
            return Ok(requested_port);
        }

        // Find an available port starting from the requested port
        let port = Self::find_available_port(requested_port);

        // Check again if API server is running on the available port
        // (it might have started between our first check and find_available_port)
        if Self::is_api_server_running(port).await {
            tracing::info!("API server already running on port {}", port);
            return Ok(port);
        }

        // Find the current executable path
        let exe_path = std::env::current_exe()?;
        tracing::info!("Starting API server on port {} (exe: {:?})", port, exe_path);

        // Start API server as a background process
        // Run with LEANKG_API_PORT set to communicate the port
        let child = std::process::Command::new(&exe_path)
            .args(["api-serve", "--port", &port.to_string()])
            .env("LEANKG_API_PORT", port.to_string())
            .spawn();

        match child {
            Ok(_child) => {
                tracing::info!("Spawned API server process");
            }
            Err(e) => {
                tracing::warn!("Failed to spawn API server: {}. Continuing anyway.", e);
                return Ok(port);
            }
        }

        // Wait for server to start (check every 100ms for up to 5 seconds)
        for _ in 0..50 {
            tokio::time::sleep(tokio::time::Duration::from_millis(100)).await;
            if Self::is_api_server_running(port).await {
                tracing::info!("API server started on port {}", port);
                return Ok(port);
            }
        }

        tracing::warn!("API server may not be fully started yet on port {}", port);
        Ok(port)
    }

    /// Find an available port starting from the given port, incrementing if taken
    fn find_available_port(start_port: u16) -> u16 {
        let mut port = start_port;
        while port < start_port + 100 {
            // Try to bind to check if port is available
            let addr = SocketAddr::from(([0, 0, 0, 0], port));
            if std::net::TcpListener::bind(addr).is_ok() {
                return port;
            }
            port += 1;
        }
        start_port
    }

    pub async fn serve_http(
        &self,
        port: u16,
        auth_token: Option<String>,
    ) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        if let Err(e) = self.auto_init_if_needed().await {
            tracing::warn!(
                "Auto-init skipped: {}. Server will operate in uninitialized state.",
                e
            );
        }

        if let Some(ref watch_path) = self.watch_path {
            let db_path = self.get_db_path();
            let watch_path = watch_path.clone();
            tokio::spawn(async move {
                let (tx, rx) = tokio::sync::mpsc::channel(100);
                start_watcher(db_path, watch_path, rx).await;
                let _ = tx; // silence unused warning
            });
            tracing::info!(
                "Auto-indexing enabled for {}",
                self.watch_path
                    .as_ref()
                    .unwrap_or(&std::path::PathBuf::from("?"))
                    .display()
            );
        }

        let server = Arc::new(HttpMcpServer {
            mcp_server: self.clone(),
            auth_token,
        });

        let cors = CorsLayer::new()
            .allow_origin(Any)
            .allow_methods([Method::GET, Method::POST, Method::OPTIONS])
            .allow_headers(Any)
            .expose_headers([header::CONTENT_TYPE]);

        let app = Router::new()
            .route("/mcp", post(handle_mcp_request))
            .route("/mcp/stream", get(handle_sse_stream))
            .route("/health", get(health_check))
            .layer(cors)
            .with_state(server);

        let addr = std::net::SocketAddr::from(([0, 0, 0, 0], port));
        let listener = tokio::net::TcpListener::bind(addr).await?;
        tracing::info!("MCP HTTP server listening on http://{}", addr);

        axum::serve(listener, app).await?;

        Ok(())
    }

    async fn auto_init_if_needed(&self) -> Result<(), String> {
        let project_root = self.find_project_root()?;

        let leankg_path = project_root.join(".leankg");
        let leankg_dir_exists = leankg_path.is_dir();
        let leankg_yaml_exists = project_root.join("leankg.yaml").exists();

        if leankg_dir_exists || leankg_yaml_exists {
            if leankg_dir_exists {
                tracing::info!(
                    "LeanKG project already initialized at {}",
                    project_root.display()
                );
                return self.auto_index_if_needed().await;
            } else {
                tracing::warn!(
                    ".leankg exists but is not a directory. Removing and re-initializing..."
                );
                std::fs::remove_file(&leankg_path)
                    .map_err(|e| format!("Failed to remove invalid .leankg file: {}", e))?;
            }
        }

        tracing::info!("LeanKG not found, searching for project root...");

        let test_file = project_root.join(".leankg_write_test");
        if std::fs::write(&test_file, "test").is_err() {
            std::fs::remove_file(test_file).ok();
            return Err(format!(
                "Filesystem at {} is not writable: Read-only file system",
                project_root.display()
            ));
        }
        std::fs::remove_file(test_file).ok();

        std::fs::create_dir_all(&leankg_path)
            .map_err(|e| format!("Failed to create .leankg: {}", e))?;
        let config = crate::config::ProjectConfig::default();
        let config_yaml = serde_yaml::to_string(&config)
            .map_err(|e| format!("Failed to serialize config: {}", e))?;
        std::fs::write(project_root.join(".leankg/leankg.yaml"), config_yaml)
            .map_err(|e| format!("Failed to write config: {}", e))?;

        tracing::info!(
            "Auto-init: Created .leankg/ and leankg.yaml at {}",
            project_root.display()
        );

        let db_path = project_root.join(".leankg");
        tokio::fs::create_dir_all(&db_path)
            .await
            .map_err(|e| format!("Failed to create db path: {}", e))?;

        let db = init_db(&db_path).map_err(|e| format!("Database error: {}", e))?;
        let graph_engine = crate::graph::GraphEngine::new(db);
        let mut parser_manager = crate::indexer::ParserManager::new();
        parser_manager
            .init_parsers()
            .map_err(|e| format!("Parser init error: {}", e))?;

        let root_str = project_root.to_string_lossy().to_string();
        let files = crate::indexer::find_files_sync(&root_str)
            .map_err(|e| format!("Find files error: {}", e))?;
        let mut indexed = 0;

        for file_path in &files {
            if crate::indexer::index_file_sync(&graph_engine, &mut parser_manager, file_path)
                .is_ok()
            {
                indexed += 1;
            }
        }

        tracing::info!("Auto-init: Indexed {} files", indexed);

        if let Err(e) = graph_engine.resolve_call_edges() {
            tracing::warn!("Auto-init: Failed to resolve call edges: {}", e);
        }

        if let Ok(true) = std::path::Path::new("docs").try_exists() {
            if let Ok(doc_result) = crate::doc_indexer::index_docs_directory(
                std::path::Path::new("docs"),
                &graph_engine,
            ) {
                tracing::info!(
                    "Auto-init: Indexed {} documents",
                    doc_result.documents.len()
                );
            }
        }

        {
            let mut db_path_guard = parking_lot::RwLock::write(&self.db_path);
            *db_path_guard = db_path.clone();
        }
        let mut ge_guard = self.graph_engine.lock();
        *ge_guard = Some(graph_engine);

        tracing::info!("Auto-init complete");
        Ok(())
    }

    async fn auto_index_if_needed(&self) -> Result<(), String> {
        let project_root = self.find_project_root()?;
        let config_path = project_root.join(".leankg/leankg.yaml");

        let config = if config_path.exists() {
            let content = std::fs::read_to_string(&config_path)
                .map_err(|e| format!("Failed to read config: {}", e))?;
            serde_yaml::from_str::<crate::config::ProjectConfig>(&content)
                .map_err(|e| format!("Failed to parse config: {}", e))?
        } else {
            crate::config::ProjectConfig::default()
        };

        if !config.mcp.auto_index_on_start {
            tracing::info!("Auto-indexing on start is disabled in config");
            return Ok(());
        }

        let db_path = self.get_db_path();
        let db_file = db_path.join("leankg.db");

        if !db_file.exists() {
            tracing::info!("Database file does not exist, skipping auto-index");
            return Ok(());
        }

        if !crate::indexer::GitAnalyzer::is_git_repo() {
            tracing::info!("Not a git repo, skipping auto-index");
            return Ok(());
        }

        let last_commit_time = match crate::indexer::GitAnalyzer::get_last_commit_time() {
            Ok(t) => t,
            Err(e) => {
                tracing::warn!("Failed to get last commit time: {}", e);
                return Ok(());
            }
        };

        let db_modified = std::fs::metadata(&db_file)
            .and_then(|m| m.modified())
            .map(|t| {
                t.duration_since(std::time::UNIX_EPOCH)
                    .map(|d| d.as_secs() as i64)
                    .unwrap_or(0)
            })
            .unwrap_or(0);

        let threshold_seconds = (config.mcp.auto_index_threshold_minutes * 60) as i64;

        if last_commit_time <= db_modified + threshold_seconds {
            tracing::info!(
                "Index is fresh (last commit: {}, db modified: {}), skipping auto-index",
                last_commit_time,
                db_modified
            );
            return Ok(());
        }

        tracing::info!(
            "Index may be stale (last commit: {}, db modified: {}), running incremental index...",
            last_commit_time,
            db_modified
        );

        let db = init_db(&self.get_db_path()).map_err(|e| format!("Database error: {}", e))?;
        let graph_engine = crate::graph::GraphEngine::new(db);
        let mut parser_manager = crate::indexer::ParserManager::new();
        parser_manager
            .init_parsers()
            .map_err(|e| format!("Parser init error: {}", e))?;

        let root_str = project_root.to_string_lossy().to_string();
        match crate::indexer::incremental_index_sync(&graph_engine, &mut parser_manager, &root_str)
            .await
        {
            Ok(result) => {
                tracing::info!(
                    "Auto-index: Processed {} files ({} elements)",
                    result.total_files_processed,
                    result.elements_indexed
                );
            }
            Err(e) => {
                tracing::warn!("Auto-index failed: {}, falling back to full index", e);
                let files = crate::indexer::find_files_sync(&root_str)
                    .map_err(|fe| format!("Find files error: {}", fe))?;
                let mut indexed = 0;
                for file_path in &files {
                    if crate::indexer::index_file_sync(
                        &graph_engine,
                        &mut parser_manager,
                        file_path,
                    )
                    .is_ok()
                    {
                        indexed += 1;
                    }
                }
                tracing::info!("Auto-index (fallback): Indexed {} files", indexed);
            }
        }

        if let Err(e) = graph_engine.resolve_call_edges() {
            tracing::warn!("Auto-index: Failed to resolve call edges: {}", e);
        }

        if let Ok(true) = project_root.join("docs").try_exists() {
            if let Ok(doc_result) = crate::doc_indexer::index_docs_directory(
                project_root.join("docs").as_path(),
                &graph_engine,
            ) {
                tracing::info!(
                    "Auto-index: Indexed {} documents",
                    doc_result.documents.len()
                );
            }
        }

        tracing::info!("Auto-index complete");

        {
            let mut guard = self.graph_engine.lock();
            *guard = None;
        }

        Ok(())
    }

    async fn trigger_reindex(&self) -> Result<(), String> {
        let project_root = self.find_project_root()?;
        let db = init_db(&self.get_db_path()).map_err(|e| format!("Database error: {}", e))?;
        let graph_engine = crate::graph::GraphEngine::new(db);
        let mut parser_manager = crate::indexer::ParserManager::new();
        parser_manager
            .init_parsers()
            .map_err(|e| format!("Parser init error: {}", e))?;

        let root_str = project_root.to_string_lossy().to_string();
        match crate::indexer::incremental_index_sync(&graph_engine, &mut parser_manager, &root_str)
            .await
        {
            Ok(result) => {
                tracing::info!(
                    "Reindex triggered by external write: {} files processed",
                    result.total_files_processed
                );
            }
            Err(e) => {
                tracing::warn!("Reindex failed: {}", e);
            }
        }

        {
            let mut guard = self.graph_engine.lock();
            *guard = None;
        }
        Ok(())
    }

    fn load_config(
        &self,
        project_root: &std::path::Path,
    ) -> Result<crate::config::ProjectConfig, String> {
        let config_path = project_root.join(".leankg/leankg.yaml");
        if config_path.exists() {
            let content = std::fs::read_to_string(&config_path)
                .map_err(|e| format!("Failed to read config: {}", e))?;
            serde_yaml::from_str::<crate::config::ProjectConfig>(&content)
                .map_err(|e| format!("Failed to parse config: {}", e))
        } else {
            Ok(crate::config::ProjectConfig::default())
        }
    }

    fn find_project_root(&self) -> Result<std::path::PathBuf, String> {
        let current_dir =
            std::env::current_dir().map_err(|e| format!("Failed to get current dir: {}", e))?;

        if current_dir.join(".leankg").exists() || current_dir.join("leankg.yaml").exists() {
            tracing::debug!(
                "Found .leankg/leankg.yaml at current dir: {}",
                current_dir.display()
            );
            return Ok(current_dir);
        }

        if current_dir.join(".git").exists() {
            tracing::debug!("Found .git at current dir: {}", current_dir.display());
            return Ok(current_dir);
        }

        for dir in current_dir.ancestors() {
            if dir.join(".git").exists() {
                tracing::debug!("Found git repo at {}, this is project root", dir.display());
                if dir.join(".leankg").exists() || dir.join("leankg.yaml").exists() {
                    tracing::debug!(
                        "Found .leankg/leankg.yaml in project root: {}",
                        dir.display()
                    );
                    return Ok(dir.to_path_buf());
                }
                tracing::debug!(
                    "No .leankg in project root {}, will need auto-init",
                    dir.display()
                );
                return Ok(dir.to_path_buf());
            }
        }

        for dir in current_dir.ancestors() {
            if dir.join(".leankg").exists() || dir.join("leankg.yaml").exists() {
                tracing::debug!("Found project at {} (parent without .git)", dir.display());
                return Ok(dir.to_path_buf());
            }
        }

        tracing::debug!(
            "No project markers found, using current dir: {}",
            current_dir.display()
        );
        Ok(current_dir)
    }

    fn validate_required_params(
        &self,
        tool_name: &str,
        arguments: &serde_json::Map<String, serde_json::Value>,
    ) -> Option<String> {
        let tools = ToolRegistry::list_tools();
        let tool = tools.iter().find(|t| t.name == tool_name)?;

        let required_params = tool.input_schema.get("required")?.as_array()?;
        for param in required_params {
            let param_name = param.as_str()?;
            if !arguments.contains_key(param_name)
                || arguments.get(param_name).is_none_or(|v| v.is_null())
            {
                return Some(format!(
                    "Missing required parameter '{}' for tool '{}'",
                    param_name, tool_name
                ));
            }
        }
        None
    }

    async fn execute_tool(
        &self,
        tool_name: &str,
        arguments: serde_json::Map<String, serde_json::Value>,
    ) -> Result<serde_json::Value, String> {
        let project_root = self.find_project_root()?;
        tracing::info!(
            "execute_tool called. project_root={}, db_path={}",
            project_root.display(),
            self.get_db_path().display()
        );

        // Validate required parameters before dispatching to handler
        if let Some(err) = self.validate_required_params(tool_name, &arguments) {
            return Err(err);
        }

        if tool_name == "mcp_init" {
            if let Some(path) = arguments.get("path").and_then(|v| v.as_str()) {
                let new_db_path = std::path::PathBuf::from(path);
                {
                    let mut guard = self.graph_engine.lock();
                    *guard = None;
                }
                {
                    let mut db_path_guard = parking_lot::RwLock::write(&self.db_path);
                    *db_path_guard = new_db_path.clone();
                }
                tracing::info!("Updated db_path to {}", new_db_path.display());
            }
        }

        if self.write_tracker.is_dirty() {
            let config = self.load_config(&project_root)?;
            if config.mcp.auto_index_on_db_write {
                tracing::info!("External write detected, triggering incremental reindex...");
                self.trigger_reindex().await?;
                self.write_tracker.clear_dirty();
            }
        }

        let file_path: Option<String> = if tool_name == "orchestrate" {
            // For orchestrate, parse intent to extract target file
            arguments
                .get("intent")
                .and_then(|v| v.as_str())
                .and_then(|intent| {
                    let parsed = self.intent_parser.parse(intent);
                    parsed.target
                })
                .or_else(|| {
                    arguments
                        .get("file")
                        .and_then(|v| v.as_str())
                        .map(String::from)
                })
        } else {
            arguments
                .get("file")
                .and_then(|v| v.as_str())
                .or_else(|| arguments.get("path").and_then(|v| v.as_str()))
                .or_else(|| arguments.get("project").and_then(|v| v.as_str()))
                .map(String::from)
        };

        let graph_engine = self.get_graph_engine_for_path(file_path.as_ref())?;
        let handler = ToolHandler::new(graph_engine, self.get_db_path());
        let args_value = serde_json::Value::Object(arguments);
        let result = handler.execute_tool(tool_name, &args_value).await;

        if tool_name == "mcp_index" {
            let mut guard = self.graph_engine.lock();
            *guard = None;
        }

        result
    }
}

impl ServerHandler for MCPServer {
    fn get_info(&self) -> rmcp::model::ServerInfo {
        rmcp::model::ServerInfo::new(
            rmcp::model::ServerCapabilities::builder()
                .enable_tools()
                .build(),
        )
        .with_server_info(
            rmcp::model::Implementation::new("leankg", env!("CARGO_PKG_VERSION"))
                .with_title("LeanKG")
                .with_description("Lightweight knowledge graph for codebase understanding")
        )
        .with_instructions("LeanKG - Lightweight knowledge graph for codebase understanding. Use tools to query code elements, dependencies, impact radius, and traceability.")
    }

    async fn list_tools(
        &self,
        _params: Option<rmcp::model::PaginatedRequestParams>,
        _context: rmcp::service::RequestContext<RoleServer>,
    ) -> Result<ListToolsResult, rmcp::model::ErrorData> {
        let tools = ToolRegistry::list_tools();
        let rmcp_tools: Vec<Tool> = tools
            .into_iter()
            .map(|t| {
                Tool::new(
                    t.name,
                    t.description,
                    Arc::new(t.input_schema.as_object().cloned().unwrap_or_default()),
                )
            })
            .collect();
        Ok(ListToolsResult::with_all_items(rmcp_tools))
    }

    async fn call_tool(
        &self,
        request: CallToolRequestParams,
        _context: rmcp::service::RequestContext<RoleServer>,
    ) -> Result<CallToolResult, rmcp::model::ErrorData> {
        let tool_name = request.name.as_ref();
        let arguments = request.arguments.unwrap_or_default();

        // Always use TOON format (ignore client's format preference)
        let use_toon = true;

        match self.execute_tool(tool_name, arguments).await {
            Ok(result) => {
                let content_str = if let Some(s) = result.as_str() {
                    // Already purely text (e.g. from context chunk fetch) - preserve as-is
                    s.to_string()
                } else if use_toon {
                    // Use TOON format with Response Format Envelope
                    crate::mcp::toon::wrap_response(tool_name, &result, true)
                } else {
                    // Use JSON format with Response Format Envelope
                    crate::mcp::toon::wrap_response(tool_name, &result, false)
                };

                Ok(CallToolResult::success(vec![Content::text(content_str)]))
            }
            Err(e) => Ok(CallToolResult::error(vec![Content::text(format!(
                "Tool execution failed: {}",
                e
            ))])),
        }
    }
}

// ============================================================================
// HTTP Transport for Remote MCP Server
// ============================================================================

/// HTTP MCP Server state shared across requests
struct HttpMcpServer {
    mcp_server: MCPServer,
    auth_token: Option<String>,
}

/// MCP JSON-RPC request envelope
#[derive(Debug, Serialize, Deserialize)]
struct JsonRpcRequest {
    jsonrpc: String,
    id: serde_json::Value,
    method: String,
    params: Option<serde_json::Value>,
}

/// MCP JSON-RPC response envelope
#[derive(Debug, Serialize)]
struct JsonRpcResponse {
    jsonrpc: String,
    id: serde_json::Value,
    result: Option<serde_json::Value>,
    error: Option<JsonRpcError>,
}

#[derive(Debug, Serialize)]
struct JsonRpcError {
    code: i32,
    message: String,
    data: Option<serde_json::Value>,
}

/// MCP JSON-RPC error codes
mod json_rpc_code {
    pub const PARSE_ERROR: i32 = -32700;
    pub const INVALID_REQUEST: i32 = -32600;
    pub const METHOD_NOT_FOUND: i32 = -32601;
    pub const INVALID_PARAMS: i32 = -32602;
    pub const INTERNAL_ERROR: i32 = -32603;
}

/// Extract bearer token from Authorization header using constant-time comparison
/// to prevent timing attacks on bearer tokens.
fn extract_bearer_token(auth_header: Option<&str>, token: &Option<String>) -> bool {
    if token.is_none() {
        return true; // No auth required
    }
    let token = token.as_ref().unwrap();

    if let Some(auth) = auth_header {
        if let Some(stripped) = auth.strip_prefix("Bearer ") {
            // Use constant-time comparison to prevent timing attacks
            return subtle::ConstantTimeEq::ct_eq(stripped.as_bytes(), token.as_bytes()).into();
        }
    }
    false
}

/// Handle POST /mcp - JSON-RPC request endpoint
async fn handle_mcp_request(
    State(server): State<Arc<HttpMcpServer>>,
    headers: HeaderMap,
    body: String,
) -> Response {
    // Extract Authorization header
    let auth_value = headers
        .get(header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok());

    // Check authentication
    if !extract_bearer_token(auth_value, &server.auth_token) {
        return Response::builder()
            .status(StatusCode::UNAUTHORIZED)
            .body(Body::from(r#"{"error": "Unauthorized"}"#))
            .unwrap();
    }

    // Parse JSON-RPC request
    let request: JsonRpcRequest = match serde_json::from_str(&body) {
        Ok(req) => req,
        Err(e) => {
            let response = JsonRpcResponse {
                jsonrpc: "2.0".to_string(),
                id: serde_json::Value::Null,
                result: None,
                error: Some(JsonRpcError {
                    code: json_rpc_code::PARSE_ERROR,
                    message: format!("Parse error: {}", e),
                    data: None,
                }),
            };
            return Response::builder()
                .status(StatusCode::OK)
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(serde_json::to_string(&response).unwrap()))
                .unwrap();
        }
    };

    // Process the request
    let result = process_jsonrpc_request(&server.mcp_server, &request).await;

    // Build response
    let response = match result {
        Ok(result) => JsonRpcResponse {
            jsonrpc: "2.0".to_string(),
            id: request.id,
            result: Some(result),
            error: None,
        },
        Err(e) => JsonRpcResponse {
            jsonrpc: "2.0".to_string(),
            id: request.id,
            result: None,
            error: Some(JsonRpcError {
                code: json_rpc_code::INTERNAL_ERROR,
                message: e,
                data: None,
            }),
        },
    };

    Response::builder()
        .status(StatusCode::OK)
        .header(header::CONTENT_TYPE, "application/json")
        .body(Body::from(serde_json::to_string(&response).unwrap()))
        .unwrap()
}

/// Process a JSON-RPC request and return the result
async fn process_jsonrpc_request(
    mcp_server: &MCPServer,
    request: &JsonRpcRequest,
) -> Result<serde_json::Value, String> {
    let method = &request.method;
    let params = request.params.as_ref();

    match method.as_str() {
        "initialize" => Ok(serde_json::json!({
            "protocolVersion": "2025-06-18",
            "capabilities": {
                "tools": { "listChanged": true },
                "resources": {}
            },
            "serverInfo": {
                "name": "leankg",
                "version": env!("CARGO_PKG_VERSION")
            }
        })),
        "notifications/initialized" => {
            // Client is done initializing, no response needed
            Ok(serde_json::Value::Null)
        }
        "tools/list" => {
            let tools = ToolRegistry::list_tools();
            let rmcp_tools: Vec<serde_json::Value> = tools
                .into_iter()
                .map(|t| {
                    serde_json::json!({
                        "name": t.name,
                        "description": t.description,
                        "inputSchema": t.input_schema
                    })
                })
                .collect();
            Ok(serde_json::json!({ "tools": rmcp_tools }))
        }
        "tools/call" => {
            let params_obj = params
                .and_then(|p| p.as_object())
                .ok_or("Missing params for tools/call")?;

            let tool_name = params_obj
                .get("name")
                .and_then(|v| v.as_str())
                .ok_or("Missing tool name")?;

            let arguments = params_obj
                .get("arguments")
                .and_then(|v| v.as_object())
                .cloned()
                .unwrap_or_default();

            let result = mcp_server
                .execute_tool(tool_name, arguments)
                .await
                .map_err(|e| e.to_string())?;

            // Format as MCP tool result
            // Tool results are either plain strings (as_str()) or structured JSON
            // that needs to be wrapped in MCP response format
            let content_str = if let Some(s) = result.as_str() {
                s.to_string()
            } else {
                crate::mcp::toon::wrap_response(tool_name, &result, true)
            };

            Ok(serde_json::json!({
                "content": [{ "type": "text", "text": content_str }]
            }))
        }
        _ => Err(format!("Method not found: {}", method)),
    }
}

/// Handle GET /mcp/stream - SSE endpoint for server-initiated messages
async fn handle_sse_stream(
    State(server): State<Arc<HttpMcpServer>>,
    headers: HeaderMap,
) -> Response {
    // Extract Authorization header
    let auth_value = headers
        .get(header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok());

    // Check authentication
    if !extract_bearer_token(auth_value, &server.auth_token) {
        return Response::builder()
            .status(StatusCode::UNAUTHORIZED)
            .body(Body::from(r#"event: error\ndata: Unauthorized\n\n"#))
            .unwrap();
    }

    // For now, return an SSE stream that sends an endpoint message
    // In a full implementation, this would maintain a persistent connection
    // for server-initiated notifications
    let sse_data = "event: endpoint\ndata: /mcp\n\n";

    Response::builder()
        .status(StatusCode::OK)
        .header(header::CONTENT_TYPE, "text/event-stream")
        .header(header::CACHE_CONTROL, "no-cache")
        .header(header::CONNECTION, "keep-alive")
        .body(Body::from(sse_data))
        .unwrap()
}

/// Health check endpoint
async fn health_check() -> Response {
    Response::builder()
        .status(StatusCode::OK)
        .header(header::CONTENT_TYPE, "application/json")
        .body(Body::from(r#"{"status": "ok"}"#))
        .unwrap()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn test_mcp_server_creation() {
        let _server = MCPServer::new(std::path::PathBuf::from(".leankg"));
    }

    #[tokio::test]
    async fn test_mcp_server_new_with_custom_path() {
        let db_path = std::path::PathBuf::from("/custom/path/.leankg");
        let server = MCPServer::new(db_path.clone());
        assert!(server.auth_config.try_read().is_ok());
    }
}
