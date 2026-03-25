use crate::db::schema::init_db;
use crate::graph::GraphEngine;
use crate::mcp::auth::AuthConfig;
use crate::mcp::handler::ToolHandler;
use crate::mcp::tools::ToolRegistry;
use crate::mcp::watcher::start_watcher;
use rmcp::handler::server::ServerHandler;
use rmcp::model::{
    CallToolRequestParams, CallToolResult, Content, Implementation, ListToolsResult,
    ProtocolVersion, ServerCapabilities, ServerInfo, Tool,
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
    fn get_info(&self) -> ServerInfo {
        ServerInfo {
            protocol_version: ProtocolVersion::LATEST,
            capabilities: ServerCapabilities::builder()
                .enable_tools()
                .build(),
            server_info: Implementation {
                name: "leankg".to_string(),
                version: "0.1.0".to_string(),
                title: Some("LeanKG".to_string()),
                description: Some("Lightweight knowledge graph for codebase understanding".to_string()),
                ..Default::default()
            },
            instructions: Some("LeanKG - Lightweight knowledge graph for codebase understanding. Use tools to query code elements, dependencies, impact radius, and traceability.".to_string()),
        }
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
