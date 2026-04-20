use crate::db::schema::CozoDb;
use std::collections::BTreeMap;
use std::sync::Arc;
use tokio::sync::mpsc;

pub struct WriteQueue {
    sender: mpsc::Sender<WriteRequest>,
}

#[derive(Debug)]
pub enum WriteRequest {
    Script {
        script: String,
        params: BTreeMap<String, serde_json::Value>,
        response_tx: tokio::sync::oneshot::Sender<Result<cozo::NamedRows, String>>,
    },
    Shutdown,
}

impl WriteQueue {
    pub fn new(db: Arc<CozoDb>) -> Self {
        let (sender, mut receiver) = mpsc::channel::<WriteRequest>(100);
        let db_clone = db.clone();

        // Spawn write worker
        tokio::spawn(async move {
            while let Some(req) = receiver.recv().await {
                match req {
                    WriteRequest::Script {
                        script,
                        params,
                        response_tx,
                    } => {
                        let result = db_clone
                            .run_script(&script, params)
                            .map_err(|e| e.to_string());
                        let _ = response_tx.send(result);
                    }
                    WriteRequest::Shutdown => break,
                }
            }
        });

        Self { sender }
    }

    pub async fn execute(
        &self,
        script: String,
        params: BTreeMap<String, serde_json::Value>,
    ) -> Result<cozo::NamedRows, String> {
        let (tx, rx) = tokio::sync::oneshot::channel();
        self.sender
            .send(WriteRequest::Script {
                script,
                params,
                response_tx: tx,
            })
            .await
            .map_err(|_| "Write queue closed".to_string())?;
        rx.await
            .map_err(|_| "Write queue response cancelled".to_string())?
    }

    pub async fn shutdown(&self) {
        let _ = self.sender.send(WriteRequest::Shutdown).await;
    }
}
