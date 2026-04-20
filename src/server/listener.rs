use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};

pub async fn start_server(
    port: u16,
    db_path: std::path::PathBuf,
) -> Result<(), Box<dyn std::error::Error>> {
    let addr = format!("127.0.0.1:{}", port);
    let listener = TcpListener::bind(&addr).await?;

    tracing::info!("LeanKG MCP server listening on {}", addr);

    loop {
        match listener.accept().await {
            Ok((stream, _)) => {
                let db_path = db_path.clone();
                tokio::spawn(async move {
                    if let Err(e) = handle_connection(stream, db_path).await {
                        tracing::error!("Connection error: {}", e);
                    }
                });
            }
            Err(e) => {
                tracing::error!("Accept error: {}", e);
            }
        }
    }
}

async fn handle_connection(
    mut stream: TcpStream,
    _db_path: std::path::PathBuf,
) -> Result<(), Box<dyn std::error::Error>> {
    loop {
        // Read 4-byte length header
        let mut header = [0u8; 4];
        match stream.read_exact(&mut header).await {
            Ok(_) => {}
            Err(e) if e.kind() == std::io::ErrorKind::UnexpectedEof => {
                return Ok(()); // Client disconnected
            }
            Err(e) => return Err(e.into()),
        }

        let len = u32::from_be_bytes(header) as usize;

        // Read message body
        let mut body = vec![0u8; len];
        stream.read_exact(&mut body).await?;

        let request: serde_json::Value = serde_json::from_slice(&body)?;

        tracing::debug!("Received request: {:?}", request);

        // Process the request (simplified - just echo for now)
        let response = serde_json::json!({
            "jsonrpc": "2.0",
            "id": request.get("id"),
            "result": {
                "protocolVersion": "2024-11-05",
                "capabilities": {},
                "serverInfo": {
                    "name": "LeanKG",
                    "version": "0.15.3"
                }
            }
        });

        let response_bytes = serde_json::to_vec(&response)?;
        let len = response_bytes.len() as u32;

        // Write length header
        stream.write_all(&len.to_be_bytes()).await?;
        // Write response body
        stream.write_all(&response_bytes).await?;
    }
}
