use super::{ProgressReporter, Source};
use std::path::{Path, PathBuf};
use std::process::Command;

/// Clone or pull a git repository into the staging directory.
pub struct GitSource {
    pub url: String,
    pub auth: Option<String>,
    pub ref_name: String,
}

#[async_trait::async_trait]
impl Source for GitSource {
    async fn sync_to_local(
        &self,
        staging_root: &Path,
        progress: &mut dyn ProgressReporter,
    ) -> Result<PathBuf, Box<dyn std::error::Error + Send + Sync>> {
        let dir_name = super::uri_staging_dir(&super::SourceUri::Git {
            url: self.url.clone(),
        });
        let local_dir = staging_root.join(&dir_name);

        let git_dir = local_dir.join(".git");
        if git_dir.exists() {
            progress.report(&format!(
                "git repo exists at {}, pulling {}...",
                local_dir.display(),
                self.ref_name
            ));
            fetch_and_checkout(&local_dir, &self.ref_name, progress)?;
        } else {
            tokio::fs::create_dir_all(&local_dir).await?;
            progress.report(&format!("cloning {} (ref: {})...", self.url, self.ref_name));
            let clone_url = maybe_inject_auth(&self.url, self.auth.as_deref());
            clone_repo(&clone_url, &local_dir, &self.ref_name, progress)?;
        }

        Ok(local_dir)
    }

    fn name(&self) -> &str {
        "git"
    }
}

fn maybe_inject_auth(url: &str, auth: Option<&str>) -> String {
    let Some(token) = auth else {
        return url.to_string();
    };
    // For https URLs, inject the token as userinfo.
    if let Some(rest) = url.strip_prefix("https://") {
        return format!("https://oauth2:{}@{}", token, rest);
    }
    url.to_string()
}

fn clone_repo(
    url: &str,
    dir: &Path,
    ref_name: &str,
    progress: &mut dyn ProgressReporter,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    progress.report(&format!("git clone --depth 1 --branch {} ...", ref_name));

    let mut cmd = Command::new("git");
    cmd.args(["clone", "--depth", "1", "--branch", ref_name, url])
        .arg(dir);

    let output = cmd
        .output()
        .map_err(|e| format!("git clone failed: {}", e))?;

    if !output.status.success() {
        let _stderr = String::from_utf8_lossy(&output.stderr);
        // Fallback: clone default branch then checkout.
        progress.report("shallow clone failed, trying full clone + checkout...");
        let mut cmd2 = Command::new("git");
        cmd2.args(["clone", url]).arg(dir);
        let out2 = cmd2
            .output()
            .map_err(|e| format!("git clone fallback failed: {}", e))?;
        if !out2.status.success() {
            return Err(format!(
                "git clone fallback failed: {}",
                String::from_utf8_lossy(&out2.stderr)
            )
            .into());
        }
    }

    // Ensure we're on the right ref.
    fetch_and_checkout(dir, ref_name, progress)?;
    Ok(())
}

fn fetch_and_checkout(
    repo_dir: &Path,
    ref_name: &str,
    progress: &mut dyn ProgressReporter,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    // Fetch all to pick up new branches/tags.
    let fetch = Command::new("git")
        .current_dir(repo_dir)
        .args(["fetch", "--all", "--prune"])
        .output()
        .map_err(|e| format!("git fetch failed: {}", e))?;

    if !fetch.status.success() {
        return Err(format!(
            "git fetch failed: {}",
            String::from_utf8_lossy(&fetch.stderr)
        )
        .into());
    }

    progress.report(&format!("git checkout {} ...", ref_name));
    let checkout = Command::new("git")
        .current_dir(repo_dir)
        .args(["checkout", ref_name])
        .output()
        .map_err(|e| format!("git checkout failed: {}", e))?;

    if !checkout.status.success() {
        // Try as a remote branch reference.
        let remote_ref = format!("origin/{}", ref_name);
        progress.report(&format!("trying remote ref {}...", remote_ref));
        let co2 = Command::new("git")
            .current_dir(repo_dir)
            .args(["checkout", "-b", ref_name, &remote_ref])
            .output()
            .map_err(|e| format!("git checkout remote ref failed: {}", e))?;

        if !co2.status.success() {
            return Err(format!(
                "git checkout {} failed: {} (tried {}: {})",
                ref_name,
                String::from_utf8_lossy(&checkout.stderr),
                remote_ref,
                String::from_utf8_lossy(&co2.stderr)
            )
            .into());
        }
    }

    // Pull latest if on a branch (not detached HEAD).
    let pull = Command::new("git")
        .current_dir(repo_dir)
        .args(["pull", "--ff-only"])
        .output();

    match pull {
        Ok(o) if o.status.success() => progress.report("pulled latest"),
        Ok(o) => {
            let stderr = String::from_utf8_lossy(&o.stderr);
            progress.report(&format!("git pull skipped ({}): {}", o.status, stderr));
            // Shallow depth may prevent ff-only pull. Attempt to reset to
            // the remote tracking branch to pick up new commits.
            progress.report("attempting fetch --unshallow + reset...");
            let _ = Command::new("git")
                .current_dir(repo_dir)
                .args(["fetch", "--unshallow"])
                .output();
            let rebase = Command::new("git")
                .current_dir(repo_dir)
                .args(["merge", "--ff-only", &format!("origin/{}", ref_name)])
                .output();
            match rebase {
                Ok(r) if r.status.success() => progress.report("fast-forward after unshallow"),
                _ => {
                    progress.report("reset to origin/HEAD as fallback");
                    let reset = Command::new("git")
                        .current_dir(repo_dir)
                        .args(["reset", "--hard", &format!("origin/{}", ref_name)])
                        .output();
                    if let Ok(r) = reset {
                        if r.status.success() {
                            progress.report("reset to origin/HEAD OK");
                        }
                    }
                }
            }
        }
        Err(e) => progress.report(&format!("git pull error (non-fatal): {}", e)),
    }

    Ok(())
}
