use super::{ProgressReporter, Source};
use std::path::{Path, PathBuf};

/// Passthrough source for local filesystem paths.
pub struct LocalSource {
    pub path: String,
}

#[async_trait::async_trait]
impl Source for LocalSource {
    async fn sync_to_local(
        &self,
        _staging_root: &Path,
        progress: &mut dyn ProgressReporter,
    ) -> Result<PathBuf, Box<dyn std::error::Error + Send + Sync>> {
        let p = Path::new(&self.path);
        let resolved = if p.is_absolute() {
            p.to_path_buf()
        } else {
            std::env::current_dir()?.join(p)
        };
        let canonical = std::fs::canonicalize(&resolved).unwrap_or(resolved);
        progress.report(&format!("local source at {}", canonical.display()));
        Ok(canonical)
    }

    fn name(&self) -> &str {
        "local"
    }
}
