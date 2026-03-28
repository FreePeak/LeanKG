#![allow(dead_code)]
use notify::{Config, Event, RecommendedWatcher, RecursiveMode, Watcher};
use std::path::{Path, PathBuf};
use std::sync::mpsc::{channel, Receiver};
use std::time::Duration;
use tokio::sync::mpsc as tokio_mpsc;

#[derive(Debug, Clone)]
pub struct FileChange {
    pub path: PathBuf,
    pub kind: FileChangeKind,
}

#[derive(Debug, Clone)]
pub enum FileChangeKind {
    Created,
    Modified,
    Deleted,
}

impl From<&notify::EventKind> for FileChangeKind {
    fn from(kind: &notify::EventKind) -> Self {
        match kind {
            notify::EventKind::Create(_) => FileChangeKind::Created,
            notify::EventKind::Modify(_) => FileChangeKind::Modified,
            notify::EventKind::Remove(_) => FileChangeKind::Deleted,
            _ => FileChangeKind::Modified,
        }
    }
}

pub struct FileWatcher {
    watcher: RecommendedWatcher,
    watch_path: PathBuf,
    rx: Receiver<Result<Event, notify::Error>>,
}

impl FileWatcher {
    pub fn new<P: AsRef<Path>>(path: P) -> Result<Self, notify::Error> {
        let (tx, rx) = channel();

        let watcher = RecommendedWatcher::new(
            move |res: Result<Event, notify::Error>| {
                if let Ok(event) = res {
                    let _ = tx.send(Ok(event));
                }
            },
            Config::default().with_poll_interval(Duration::from_secs(2)),
        )?;

        let mut watcher = watcher;
        watcher.watch(path.as_ref(), RecursiveMode::Recursive)?;

        Ok(Self {
            watcher,
            watch_path: path.as_ref().to_path_buf(),
            rx,
        })
    }

    #[allow(dead_code)]
    pub fn watch_path(&self) -> &Path {
        &self.watch_path
    }

    #[allow(dead_code)]
    pub fn try_recv_event(&self) -> Option<Event> {
        self.rx.try_recv().ok().and_then(|r| r.ok())
    }

    pub fn recv_event(&self) -> Option<Event> {
        self.rx.recv().ok().and_then(|r| r.ok())
    }

    pub fn into_async(self, tx: tokio_mpsc::Sender<FileChange>) -> AsyncFileWatcher {
        AsyncFileWatcher {
            inner: self,
            tokio_tx: tx,
        }
    }
}

pub struct AsyncFileWatcher {
    inner: FileWatcher,
    tokio_tx: tokio_mpsc::Sender<FileChange>,
}

impl AsyncFileWatcher {
    pub async fn run(self) {
        loop {
            if let Some(event) = self.inner.recv_event() {
                for path in event.paths {
                    let change = FileChange {
                        path: path.clone(),
                        kind: FileChangeKind::from(&event.kind),
                    };
                    let _ = self.tokio_tx.send(change).await;
                }
            }
            tokio::time::sleep(Duration::from_millis(100)).await;
        }
    }
}

impl Drop for FileWatcher {
    fn drop(&mut self) {
        let _ = self.watcher.unwatch(self.watch_path.as_path());
    }
}
