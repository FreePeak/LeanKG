use crate::obsidian::sync::SyncEngine;
use notify::{Config, Event, RecommendedWatcher, RecursiveMode, Watcher};
use std::path::Path;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::mpsc;

pub struct ObsidianWatcher {
    engine: Arc<SyncEngine>,
    debounce_ms: u64,
}

impl ObsidianWatcher {
    pub fn new(engine: Arc<SyncEngine>, debounce_ms: u64) -> Self {
        Self { engine, debounce_ms }
    }

    pub async fn watch(&self, vault_path: &str) -> Result<(), ObsidianError> {
        let (tx, mut rx) = mpsc::channel::<Event>(100);
        
        let tx_clone = tx.clone();
        let watch_path = vault_path.to_string();

        std::thread::spawn(move || {
            let mut watcher = RecommendedWatcher::new(
                move |res: Result<Event, notify::Error>| {
                    if let Ok(event) = res {
                        let _ = tx_clone.blocking_send(event);
                    }
                },
                Config::default().with_poll_interval(Duration::from_secs(1)),
            ).unwrap();

            watcher.watch(Path::new(&watch_path), RecursiveMode::Recursive).unwrap();

            loop {
                std::thread::sleep(Duration::from_secs(1));
            }
        });

        let mut last_event = std::time::Instant::now();
        let debounce = Duration::from_millis(self.debounce_ms);

        while let Some(event) = rx.recv().await {
            if last_event.elapsed() < debounce {
                continue;
            }
            last_event = std::time::Instant::now();

            if self.should_sync_event(&event) {
                println!("Detected change in: {:?}", event.paths);
                if let Err(e) = self.engine.pull().await {
                    eprintln!("Pull failed: {}", e);
                }
            }
        }

        Ok(())
    }

    fn should_sync_event(&self, event: &Event) -> bool {
        use notify::EventKind;
        
        matches!(
            event.kind,
            EventKind::Create(_) | EventKind::Modify(_) | EventKind::Remove(_)
        )
    }
}

#[derive(Debug, thiserror::Error)]
pub enum ObsidianError {
    #[error("IO error: {0}")]
    IoError(String),
    #[error("Watch error: {0}")]
    WatchError(String),
}
