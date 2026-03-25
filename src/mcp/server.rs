use crate::db::schema::init_db;
use crate::graph::GraphEngine;
use crate::mcp::auth::AuthConfig;
use crate::mcp::handler::ToolHandler;
use crate::mcp::tools::ToolRegistry;
use crate::mcp::watcher::start_watcher;
use rmcp::handler::server::ServerHandler;
use rmcp::model::{
    CallToolRequestParams, CallToolResult, Content, ListToolsResult, ServerCapabilities,
    ServerInfo, Tool,
};
use rmcp::service::{serve_server, RoleServer};
use rmcp::transport::stdio;
use std::path::PathBuf;
use std::sync::Arc;
use tokio::sync::RwLock;

pub struct MCPServer {
    auth_config: Arc<RwLock<AuthConfig>>,
    db_path: PathBuf,
    graph_engine: Arc<parking_lot::Mutex<Option<GraphEngine>>>,
    watch_path: Option<PathBuf>,
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
            watch_path: self.watch_path.clone(),
        }
    }
}

impl MCPServer {
    pub fn new(db_path: std::path::PathBuf) -> Self {
        Self {
            auth_config: Arc::new(RwLock::new(AuthConfig::default())),
            db_path,
            graph_engine: Arc::new(parking_lot::Mutex::new(None)),
            watch_path: None,
        }
    }

    pub fn new_with_watch(db_path: std::path::PathBuf, watch_path: std::path::PathBuf) -> Self {
        Self {
            auth_config: Arc::new(RwLock::new(AuthConfig::default())),
            db_path,
            graph_engine: Arc::new(parking_lot::Mutex::new(None)),
            watch_path: Some(watch_path),
        }
    }

    pub fn db_path(&self) -> &std::path::PathBuf {
        &self.db_path
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
        let db = init_db(&self.db_path).map_err(|e| format!("Database error: {}", e))?;
        let ge = GraphEngine::new(db);
        {
            let mut guard = self.graph_engine.lock();
            *guard = Some(ge.clone());
        }
        Ok(ge)
    }

    pub async fn serve_stdio(&self) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        self.auto_init_if_needed().await?;

        if let Some(ref watch_path) = self.watch_path {
            let db_path = self.db_path.clone();
            let watch_path = watch_path.clone();
            tokio::spawn(async move {
                let (tx, rx) = tokio::sync::mpsc::channel(100);
                start_watcher(db_path, watch_path, rx).await;
                let _ = tx; // silence unused warning
            });
            tracing::info!("Auto-indexing enabled for {}", self.watch_path.as_ref().unwrap_or(&std::path::PathBuf::from("?")).display());
        }
        let transport = stdio();
        let _running = serve_server(self.clone(), transport).await?;
        futures_util::future::pending().await
    }

    async fn auto_init_if_needed(&self) -> Result<(), String> {
        let current_dir = std::env::current_dir().map_err(|e| format!("Failed to get current dir: {}", e))?;
        let leankg_exists = current_dir.join(".leankg").exists() || current_dir.join("leankg.yaml").exists();

        if leankg_exists {
            tracing::info!("LeanKG project already initialized at {}", current_dir.display());
            return Ok(());
        }

        tracing::info!("LeanKG not found, auto-initializing...");

        std::fs::create_dir_all(".leankg").map_err(|e| format!("Failed to create .leankg: {}", e))?;
        let config = crate::config::ProjectConfig::default();
        let config_yaml = serde_yaml::to_string(&config).map_err(|e| format!("Failed to serialize config: {}", e))?;
        std::fs::write(current_dir.join(".leankg/leankg.yaml"), config_yaml)
            .map_err(|e| format!("Failed to write config: {}", e))?;

        tracing::info!("Auto-init: Created .leankg/ and leankg.yaml");

        let db_path = self.db_path.clone();
        tokio::fs::create_dir_all(&db_path).await.map_err(|e| format!("Failed to create db path: {}", e))?;

        let db = init_db(&db_path).map_err(|e| format!("Database error: {}", e))?;
        let graph_engine = crate::graph::GraphEngine::new(db);
        let mut parser_manager = crate::indexer::ParserManager::new();
        parser_manager.init_parsers().map_err(|e| format!("Parser init error: {}", e))?;

        let files = crate::indexer::find_files_sync(".").map_err(|e| format!("Find files error: {}", e))?;
        let mut indexed = 0;

        for file_path in &files {
            if crate::indexer::index_file_sync(&graph_engine, &mut parser_manager, file_path).is_ok() {
                indexed += 1;
            }
        }

        tracing::info!("Auto-init: Indexed {} files", indexed);

        if let Ok(true) = std::path::Path::new("docs").try_exists() {
            if let Ok(doc_result) = crate::doc_indexer::index_docs_directory(std::path::Path::new("docs"), &graph_engine) {
                tracing::info!("Auto-init: Indexed {} documents", doc_result.documents.len());
            }
        }

        tracing::info!("Auto-init complete");
        Ok(())
    }

    async fn execute_tool(
        &self,
        tool_name: &str,
        arguments: serde_json::Map<String, serde_json::Value>,
    ) -> Result<serde_json::Value, String> {
        let graph_engine = self.get_graph_engine()?;
        let handler = ToolHandler::new(graph_engine);
        let args_value = serde_json::Value::Object(arguments);
        handler.execute_tool(tool_name, &args_value).await
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

        match self.execute_tool(tool_name, arguments).await {
            Ok(result) => Ok(CallToolResult::success(vec![Content::text(
                serde_json::to_string_pretty(&result).unwrap_or_default(),
            )])),
            Err(e) => Ok(CallToolResult::error(vec![Content::text(format!(
                "Tool execution failed: {}",
                e
            ))])),
        }
    }
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
