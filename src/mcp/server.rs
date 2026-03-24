use crate::db::init_db;
use crate::graph::GraphEngine;
use crate::mcp::auth::AuthConfig;
use crate::mcp::handler::ToolHandler;
use crate::mcp::protocol::{MCPRequest, MCPResponse};
use crate::mcp::tools::ToolRegistry;
use futures_util::{SinkExt, StreamExt};
use serde_json::json;
use std::net::SocketAddr;
use std::sync::Arc;
use tokio::io::{AsyncBufReadExt, BufReader};
use tokio::net::{TcpListener, TcpStream};
use tokio::sync::RwLock;
use tokio_tungstenite::{
    accept_async,
    tungstenite::{Error, Message},
};

#[derive(Debug, Clone)]
pub struct MCPServer {
    auth_config: Arc<RwLock<AuthConfig>>,
    db_path: std::path::PathBuf,
}

impl MCPServer {
    pub fn new(db_path: std::path::PathBuf) -> Self {
        Self {
            auth_config: Arc::new(RwLock::new(AuthConfig::default())),
            db_path,
        }
    }

    pub fn db_path(&self) -> &std::path::PathBuf {
        &self.db_path
    }

    pub async fn auth_config_read(&self) -> tokio::sync::RwLockReadGuard<'_, AuthConfig> {
        self.auth_config.read().await
    }

    pub async fn serve_websocket(
        self,
        addr: SocketAddr,
    ) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        let listener = TcpListener::bind(addr).await?;
        tracing::info!("MCP WebSocket server listening on {}", addr);

        while let Ok((stream, peer_addr)) = listener.accept().await {
            tracing::debug!("New MCP connection from {}", peer_addr);
            let server = self.clone();
            tokio::spawn(async move {
                if let Err(e) = server.handle_connection(stream).await {
                    tracing::error!("Connection error: {}", e);
                }
            });
        }

        Ok(())
    }

    pub async fn serve_stdio(&self) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        let stdin = BufReader::new(tokio::io::stdin());
        let mut lines = stdin.lines();

        while let Ok(Some(line)) = lines.next_line().await {
            let line = line.trim();
            if line.is_empty() {
                continue;
            }

            if let Ok(request) = serde_json::from_str::<MCPRequest>(&line) {
                let response = self.process_request(request, "stdio").await;
                if let Ok(resp_text) = serde_json::to_string(&response) {
                    let mut out = tokio::io::stdout();
                    use tokio::io::AsyncWriteExt;
                    out.write_all(resp_text.as_bytes()).await?;
                    out.write_all(b"\n").await?;
                    out.flush().await?;
                }
            }
        }

        Ok(())
    }

    async fn handle_connection(&self, stream: TcpStream) -> Result<(), Error> {
        let ws_stream = accept_async(stream).await?;
        let (mut write, mut read) = ws_stream.split();

        let mut authenticated = false;
        let mut client_id = String::new();

        while let Some(msg) = read.next().await {
            let msg = msg?;

            if msg.is_text() || msg.is_binary() {
                let text = msg.to_text().unwrap_or("");

                if !authenticated {
                    if text.starts_with("Bearer ") || text.starts_with("bearer ") {
                        let token = text[7..].trim();
                        let auth = self.auth_config.read().await;
                        if let Some(id) = auth.validate_token(token) {
                            authenticated = true;
                            client_id = id.clone();
                            tracing::info!("MCP client authenticated: {}", client_id);
                            let resp = MCPResponse::success(
                                None,
                                json!({
                                    "authenticated": true,
                                    "client_id": client_id
                                }),
                            );
                            let resp_text = serde_json::to_string(&resp).unwrap_or_default();
                            write.send(Message::Text(resp_text.into())).await?;
                            continue;
                        }
                    }
                    let resp = MCPResponse::error(None, -32001, "Unauthorized".to_string());
                    let resp_text = serde_json::to_string(&resp).unwrap_or_default();
                    write.send(Message::Text(resp_text.into())).await?;
                    continue;
                }

                if let Ok(request) = serde_json::from_str::<MCPRequest>(text) {
                    let response = self.process_request(request, &client_id).await;
                    if let Ok(resp_text) = serde_json::to_string(&response) {
                        write.send(Message::Text(resp_text.into())).await?;
                    }
                }
            }
        }

        Ok(())
    }

    async fn process_request(&self, request: MCPRequest, _client_id: &str) -> MCPResponse {
        let method = &request.method;

        match method.as_str() {
            "initialize" => MCPResponse::success(
                request.id,
                json!({
                    "protocolVersion": "2024-11-05",
                    "serverInfo": {
                        "name": "leankg",
                        "version": "0.1.0"
                    },
                    "capabilities": {
                        "tools": true,
                        "resources": true
                    }
                }),
            ),
            "tools/list" => {
                let tools = ToolRegistry::list_tools();
                let tool_list: Vec<_> = tools
                    .iter()
                    .map(|t| {
                        json!({
                            "name": t.name,
                            "description": t.description,
                            "inputSchema": t.input_schema
                        })
                    })
                    .collect();

                MCPResponse::success(
                    request.id,
                    json!({
                        "tools": tool_list
                    }),
                )
            }
            "tools/call" => {
                if let Some(params) = &request.params {
                    let tool_name = params["name"].as_str().unwrap_or("");
                    let empty_args = json!({});
                    let arguments = params.get("arguments").unwrap_or(&empty_args);

                    match self.execute_tool(tool_name, arguments).await {
                        Ok(result) => MCPResponse::success(
                            request.id,
                            json!({
                                "content": [{
                                    "type": "text",
                                    "text": serde_json::to_string_pretty(&result).unwrap_or_default()
                                }]
                            }),
                        ),
                        Err(e) => MCPResponse::error(
                            request.id,
                            -32603,
                            format!("Tool execution failed: {}", e),
                        ),
                    }
                } else {
                    MCPResponse::error(request.id, -32602, "Invalid params".to_string())
                }
            }
            "ping" => MCPResponse::success(request.id, json!({ "pong": true })),
            _ => MCPResponse::error(request.id, -32601, format!("Method not found: {}", method)),
        }
    }

    async fn execute_tool(
        &self,
        tool_name: &str,
        arguments: &serde_json::Value,
    ) -> Result<serde_json::Value, String> {
        let db = init_db(&self.db_path)
            .await
            .map_err(|e| format!("Database error: {}", e))?;
        let graph_engine = GraphEngine::new(db);
        let handler = ToolHandler::new(graph_engine);
        handler.execute_tool(tool_name, arguments).await
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
        assert!(!server.auth_config.try_read().is_err());
    }
}
