pub mod note_generator;
pub mod sync;
pub mod watcher;

#[allow(unused_imports)]
pub use note_generator::{NoteGenerator, NoteMetadata};
#[allow(unused_imports)]
pub use sync::{SyncEngine, SyncResult};
pub use watcher::ObsidianWatcher;

use std::path::{Path, PathBuf};

pub fn vault_path(leankg_path: &Path, custom_path: Option<&str>) -> PathBuf {
    if let Some(path) = custom_path {
        PathBuf::from(path)
    } else {
        leankg_path.join("obsidian").join("vault")
    }
}
