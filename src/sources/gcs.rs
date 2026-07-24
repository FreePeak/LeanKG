use super::{ProgressReporter, Source};
use std::path::{Path, PathBuf};
use std::time::Duration;

const GCS_LIST_URL: &str = "https://storage.googleapis.com/storage/v1/b";
const GCS_DOWNLOAD_URL: &str = "https://storage.googleapis.com/storage/v1/b";

/// Fetch source code from a GCS bucket.
///
/// ## Authentication
///
/// Auth is read from (in priority order):
/// 1. `--auth <token>` -- a raw OAuth 2.0 access token.
///    Obtain via: `gcloud auth print-access-token`
/// 2. `GCS_ACCESS_TOKEN` env var -- same as above.
///
/// ponytail: service-account JSON with JWT signing would need `rsa` + `pkcs8`
/// crates. For now, pass a pre-fetched access token. Add when JWT support
/// is required for CI/CD automation.
pub struct GcsSource {
    pub bucket: String,
    pub prefix: String,
    pub auth: Option<String>,
}

impl GcsSource {
    fn resolve_token(&self) -> Result<String, Box<dyn std::error::Error + Send + Sync>> {
        if let Some(auth) = &self.auth {
            if !auth.trim().is_empty() {
                return Ok(auth.clone());
            }
        }
        if let Ok(token) = std::env::var("GCS_ACCESS_TOKEN") {
            return Ok(token);
        }
        Err(
            "GCS source requires auth: pass --auth <access-token> or set GCS_ACCESS_TOKEN env var.\n\
             Obtain a token: gcloud auth print-access-token"
                .into(),
        )
    }

    /// List all objects under the bucket+prefix, returning their names.
    async fn list_objects(
        &self,
        access_token: &str,
    ) -> Result<Vec<String>, Box<dyn std::error::Error + Send + Sync>> {
        let client = reqwest::Client::new();
        let mut objects = Vec::new();
        let mut page_token: Option<String> = None;

        loop {
            let url = format!("{}/{}/o", GCS_LIST_URL, self.bucket);
            let mut query_params: Vec<(&str, &str)> = Vec::new();
            if !self.prefix.is_empty() {
                query_params.push(("prefix", self.prefix.as_str()));
            }
            if let Some(ref token) = page_token {
                query_params.push(("pageToken", token.as_str()));
            }
            query_params.push(("maxResults", "1000"));

            let resp = client
                .get(&url)
                .bearer_auth(access_token)
                .query(&query_params)
                .timeout(Duration::from_secs(30))
                .send()
                .await
                .map_err(|e| format!("GCS list failed: {}", e))?;

            let status = resp.status();
            let body = resp
                .text()
                .await
                .map_err(|e| format!("read GCS list body: {}", e))?;

            if !status.is_success() {
                return Err(format!("GCS list returned {}: {}", status, body).into());
            }

            let parsed: serde_json::Value =
                serde_json::from_str(&body).map_err(|e| format!("GCS list parse: {}", e))?;

            if let Some(items) = parsed["items"].as_array() {
                for item in items {
                    let name = item["name"].as_str().unwrap_or("").to_string();
                    let size = item["size"]
                        .as_str()
                        .and_then(|s| s.parse::<u64>().ok())
                        .unwrap_or(0);
                    // Skip directory placeholders (ending with / and size 0).
                    if name.ends_with('/') && size == 0 {
                        continue;
                    }
                    objects.push(name);
                }
            }

            page_token = parsed["nextPageToken"].as_str().map(|s| s.to_string());
            if page_token.is_none() {
                break;
            }
        }

        Ok(objects)
    }
}

#[async_trait::async_trait]
impl Source for GcsSource {
    async fn sync_to_local(
        &self,
        staging_root: &Path,
        progress: &mut dyn ProgressReporter,
    ) -> Result<PathBuf, Box<dyn std::error::Error + Send + Sync>> {
        let token = self.resolve_token()?;
        progress.report(&format!("listing gs://{}/{} ...", self.bucket, self.prefix));

        let objects = self.list_objects(&token).await?;
        let total = objects.len();
        progress.report(&format!("found {} objects in bucket", total));

        if total == 0 {
            return Err(format!("no objects found in gs://{}/{}", self.bucket, self.prefix).into());
        }

        let dir_name = super::uri_staging_dir(&super::SourceUri::Gcs {
            bucket: self.bucket.clone(),
            prefix: self.prefix.clone(),
        });
        let local_dir = staging_root.join(&dir_name);
        tokio::fs::create_dir_all(&local_dir).await?;

        let client = reqwest::Client::new();
        let mut downloaded = 0usize;
        let mut total_bytes: u64 = 0;
        let max_size = super::max_file_size_bytes();

        for obj_name in &objects {
            // Strip prefix from the object name to get the relative path.
            let relative_path = if self.prefix.is_empty() {
                obj_name.as_str()
            } else {
                obj_name
                    .strip_prefix(&self.prefix)
                    .unwrap_or(obj_name)
                    .trim_start_matches('/')
            };
            let local_path = local_dir.join(relative_path);

            if let Some(parent) = local_path.parent() {
                tokio::fs::create_dir_all(parent).await?;
            }

            let url = format!(
                "{}/{}/o/{}",
                GCS_DOWNLOAD_URL,
                self.bucket,
                percent_encode(obj_name)
            );

            let resp = client
                .get(&url)
                .query(&[("alt", "media")])
                .bearer_auth(&token)
                .timeout(Duration::from_secs(120))
                .send()
                .await
                .map_err(|e| format!("download {} failed: {}", obj_name, e))?;

            let body = resp
                .bytes()
                .await
                .map_err(|e| format!("read {} body: {}", obj_name, e))?;

            // Respect the max file size limit (same as local indexing).
            if body.len() as u64 > max_size {
                progress.report(&format!(
                    "skipping oversized object {} ({} bytes)",
                    obj_name,
                    body.len()
                ));
                continue;
            }

            tokio::fs::write(&local_path, &body).await?;
            downloaded += 1;
            total_bytes += body.len() as u64;

            if downloaded.is_multiple_of(100) || downloaded == total {
                progress.report(&format!(
                    "synced {}/{} objects ({} MiB)",
                    downloaded,
                    total,
                    total_bytes / (1024 * 1024)
                ));
            }
        }

        progress.report(&format!(
            "complete: {} objects, {} MiB -> {}",
            downloaded,
            total_bytes / (1024 * 1024),
            local_dir.display()
        ));

        Ok(local_dir)
    }

    fn name(&self) -> &str {
        "gcs"
    }
}

/// Percent-encode a GCS object name for use in the JSON API URL path.
/// GCS requires `/` to be encoded as `%2F` in the object name portion.
fn percent_encode(input: &str) -> String {
    let mut result = String::with_capacity(input.len());
    for byte in input.bytes() {
        match byte {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                result.push(byte as char);
            }
            b'/' => result.push_str("%2F"),
            _ => {
                result.push_str(&format!("%{:02X}", byte));
            }
        }
    }
    result
}
