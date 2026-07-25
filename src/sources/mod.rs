pub mod gcs;
pub mod git;
pub mod local;

use std::path::{Path, PathBuf};

#[async_trait::async_trait]
pub trait ProgressReporter: Send {
    fn report(&mut self, message: &str);
}

/// A simple CLI progress reporter that prints to stderr.
pub struct CliProgress;

impl ProgressReporter for CliProgress {
    fn report(&mut self, message: &str) {
        eprintln!("[source] {}", message);
    }
}

#[async_trait::async_trait]
pub trait Source: Send + Sync {
    /// Sync remote content to a local staging directory.
    /// Returns the path to the synced local directory.
    async fn sync_to_local(
        &self,
        staging_root: &Path,
        progress: &mut dyn ProgressReporter,
    ) -> Result<PathBuf, Box<dyn std::error::Error + Send + Sync>>;

    /// Human-readable label for logging.
    fn name(&self) -> &str;
}

/// Parsed representation of a `--source` URI.
#[derive(Debug, Clone, PartialEq)]
pub enum SourceUri {
    Local {
        path: String,
    },
    Gcs {
        bucket: String,
        prefix: String,
    },
    S3 {
        bucket: String,
        prefix: String,
    },
    Git {
        url: String,
    },
    Sftp {
        user: String,
        host: String,
        port: u16,
        path: String,
    },
    GoogleDrive {
        folder_id: String,
    },
}

#[derive(Debug, thiserror::Error)]
pub enum SourceError {
    #[error("unsupported source URI scheme: {0}")]
    UnsupportedScheme(String),
    #[error("invalid source URI: {0}")]
    InvalidUri(String),
    #[error("auth required but not provided for source type: {0}")]
    AuthRequired(String),
}

/// Parse a source URI string into a `SourceUri`.
///
/// ## Supported schemes
///
/// | Pattern | Variant |
/// |---------|---------|
/// | `./path` or `/abs/path` (no scheme) | `Local` |
/// | `gs://bucket` or `gs://bucket/prefix` | `Gcs` |
/// | `s3://bucket/prefix` | `S3` |
/// | `git+https://host/owner/repo.git` or `git+ssh://...` | `Git` |
/// | `sftp://user@host:port/path` | `Sftp` |
/// | `gdrive://folder-id` | `GoogleDrive` |
pub fn parse_source_uri(uri: &str) -> Result<SourceUri, SourceError> {
    if let Some(rest) = uri.strip_prefix("gs://") {
        let (bucket, prefix) = split_bucket_prefix(rest);
        Ok(SourceUri::Gcs { bucket, prefix })
    } else if let Some(rest) = uri.strip_prefix("s3://") {
        let (bucket, prefix) = split_bucket_prefix(rest);
        Ok(SourceUri::S3 { bucket, prefix })
    } else if let Some(rest) = uri.strip_prefix("git+") {
        if rest.is_empty() {
            return Err(SourceError::InvalidUri(uri.to_string()));
        }
        Ok(SourceUri::Git {
            url: rest.to_string(),
        })
    } else if let Some(rest) = uri.strip_prefix("sftp://") {
        let (user_host_port, path) = match rest.find('/') {
            Some(idx) => (&rest[..idx], rest[idx..].to_string()),
            None => (rest, "/".to_string()),
        };
        let (user, host_port) = match user_host_port.find('@') {
            Some(idx) => (&user_host_port[..idx], &user_host_port[idx + 1..]),
            None => return Err(SourceError::InvalidUri(uri.to_string())),
        };
        let (host, port) = if let Some(idx) = host_port.find(':') {
            let p: u16 = host_port[idx + 1..]
                .parse()
                .map_err(|_| SourceError::InvalidUri(uri.to_string()))?;
            (host_port[..idx].to_string(), p)
        } else {
            (host_port.to_string(), 22u16)
        };
        Ok(SourceUri::Sftp {
            user: user.to_string(),
            host,
            port,
            path,
        })
    } else if let Some(folder_id) = uri.strip_prefix("gdrive://") {
        Ok(SourceUri::GoogleDrive {
            folder_id: folder_id.to_string(),
        })
    } else {
        // Local path (no scheme or file://)
        Ok(SourceUri::Local {
            path: uri.to_string(),
        })
    }
}

fn split_bucket_prefix(rest: &str) -> (String, String) {
    match rest.find('/') {
        Some(idx) => (rest[..idx].to_string(), rest[idx + 1..].to_string()),
        None => (rest.to_string(), String::new()),
    }
}

/// Create a `Source` implementation from a parsed URI.
pub struct SourceFactory;

impl SourceFactory {
    pub fn create(
        uri: &SourceUri,
        auth: Option<&str>,
        ref_name: Option<&str>,
    ) -> Result<Box<dyn Source>, SourceError> {
        match uri {
            SourceUri::Local { path } => Ok(Box::new(local::LocalSource { path: path.clone() })),
            SourceUri::Gcs { bucket, prefix } => Ok(Box::new(gcs::GcsSource {
                bucket: bucket.clone(),
                prefix: prefix.clone(),
                auth: auth.map(|s| s.to_string()),
            })),
            SourceUri::S3 {
                bucket: _,
                prefix: _,
            } => {
                // ponytail: S3Source is planned for Phase 5, not yet implemented.
                // For now, return auth-required error until the implementation lands.
                Err(SourceError::UnsupportedScheme(
                    "s3 requires aws-sigv4 dependency (Phase 5)".to_string(),
                ))
            }
            SourceUri::Git { url } => Ok(Box::new(git::GitSource {
                url: url.clone(),
                auth: auth.map(|s| s.to_string()),
                ref_name: ref_name
                    .map(|s| s.to_string())
                    .unwrap_or_else(|| "main".to_string()),
            })),
            SourceUri::Sftp { .. } => {
                // ponytail: SftpSource is Phase 6.
                Err(SourceError::UnsupportedScheme(
                    "sftp requires ssh2 dependency (Phase 6)".to_string(),
                ))
            }
            SourceUri::GoogleDrive { .. } => {
                // ponytail: GoogleDriveSource is Phase 7.
                Err(SourceError::UnsupportedScheme(
                    "gdrive requires OAuth + Drive API (Phase 7)".to_string(),
                ))
            }
        }
    }
}

/// Compute a staging directory name from a URI by replacing non-filesystem-safe chars.
pub fn uri_staging_dir(uri: &SourceUri) -> String {
    let raw = match uri {
        SourceUri::Local { path } => format!("local_{}", path),
        SourceUri::Gcs { bucket, prefix } => {
            if prefix.is_empty() {
                format!("gs_{}", bucket)
            } else {
                format!("gs_{}_{}", bucket, prefix)
            }
        }
        SourceUri::S3 { bucket, prefix } => format!("s3_{}_{}", bucket, prefix),
        SourceUri::Git { url } => format!("git_{}", url),
        SourceUri::Sftp {
            user,
            host,
            port,
            path,
        } => {
            format!("sftp_{}@{}:{}{}", user, host, port, path)
        }
        SourceUri::GoogleDrive { folder_id } => format!("gdrive_{}", folder_id),
    };
    sanitize_dir_name(&raw)
}

fn sanitize_dir_name(raw: &str) -> String {
    raw.chars()
        .map(|c| {
            if c.is_alphanumeric() || c == '-' || c == '_' || c == '.' {
                c
            } else {
                '_'
            }
        })
        .collect()
}

/// Maximum file size for source downloads (same as the indexer default: 2 MiB).
/// Override with `LEANKG_MAX_FILE_SIZE` env var (in bytes).
pub fn max_file_size_bytes() -> u64 {
    std::env::var("LEANKG_MAX_FILE_SIZE")
        .ok()
        .and_then(|s| s.parse().ok())
        .unwrap_or(2 * 1024 * 1024)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_gs_uri_bucket_only() {
        assert_eq!(
            parse_source_uri("gs://my-bucket").unwrap(),
            SourceUri::Gcs {
                bucket: "my-bucket".into(),
                prefix: String::new(),
            }
        );
    }

    #[test]
    fn parse_gs_uri_with_prefix() {
        assert_eq!(
            parse_source_uri("gs://my-bucket/path/to/code").unwrap(),
            SourceUri::Gcs {
                bucket: "my-bucket".into(),
                prefix: "path/to/code".into(),
            }
        );
    }

    #[test]
    fn parse_s3_uri() {
        assert_eq!(
            parse_source_uri("s3://my-bucket/prefix").unwrap(),
            SourceUri::S3 {
                bucket: "my-bucket".into(),
                prefix: "prefix".into(),
            }
        );
    }

    #[test]
    fn parse_git_https() {
        assert_eq!(
            parse_source_uri("git+https://github.com/user/repo.git").unwrap(),
            SourceUri::Git {
                url: "https://github.com/user/repo.git".into(),
            }
        );
    }

    #[test]
    fn parse_git_ssh() {
        assert_eq!(
            parse_source_uri("git+ssh://git@github.com/user/repo.git").unwrap(),
            SourceUri::Git {
                url: "ssh://git@github.com/user/repo.git".into(),
            }
        );
    }

    #[test]
    fn parse_sftp_uri() {
        assert_eq!(
            parse_source_uri("sftp://user@host:2222/path").unwrap(),
            SourceUri::Sftp {
                user: "user".into(),
                host: "host".into(),
                port: 2222,
                path: "/path".into(),
            }
        );
    }

    #[test]
    fn parse_sftp_uri_default_port() {
        assert_eq!(
            parse_source_uri("sftp://user@host/path").unwrap(),
            SourceUri::Sftp {
                user: "user".into(),
                host: "host".into(),
                port: 22,
                path: "/path".into(),
            }
        );
    }

    #[test]
    fn parse_gdrive_uri() {
        assert_eq!(
            parse_source_uri("gdrive://abc123folder").unwrap(),
            SourceUri::GoogleDrive {
                folder_id: "abc123folder".into(),
            }
        );
    }

    #[test]
    fn parse_local_path() {
        assert_eq!(
            parse_source_uri("./my-code").unwrap(),
            SourceUri::Local {
                path: "./my-code".into(),
            }
        );
        assert_eq!(
            parse_source_uri("/abs/path").unwrap(),
            SourceUri::Local {
                path: "/abs/path".into(),
            }
        );
    }

    #[test]
    fn factory_creates_local_source() {
        let uri = SourceUri::Local {
            path: "./src".into(),
        };
        let source = SourceFactory::create(&uri, None, None).unwrap();
        assert_eq!(source.name(), "local");
    }

    #[test]
    fn factory_creates_gcs_source() {
        let uri = SourceUri::Gcs {
            bucket: "bkt".into(),
            prefix: "pre".into(),
        };
        let source = SourceFactory::create(&uri, Some("token"), None).unwrap();
        assert_eq!(source.name(), "gcs");
    }

    #[test]
    fn factory_creates_git_source() {
        let uri = SourceUri::Git {
            url: "https://example.com/repo.git".into(),
        };
        let source = SourceFactory::create(&uri, Some("token"), Some("develop")).unwrap();
        assert_eq!(source.name(), "git");
    }

    #[test]
    fn factory_rejects_unimplemented_sources() {
        assert!(SourceFactory::create(
            &SourceUri::S3 {
                bucket: "b".into(),
                prefix: "p".into()
            },
            None,
            None
        )
        .is_err());
        assert!(SourceFactory::create(
            &SourceUri::Sftp {
                user: "u".into(),
                host: "h".into(),
                port: 22,
                path: "/".into()
            },
            None,
            None
        )
        .is_err());
        assert!(SourceFactory::create(
            &SourceUri::GoogleDrive {
                folder_id: "f".into()
            },
            None,
            None
        )
        .is_err());
    }

    #[test]
    fn uri_staging_dir_sanitizes_special_chars() {
        let uri = SourceUri::Git {
            url: "https://github.com/user/repo.git".into(),
        };
        let dir = uri_staging_dir(&uri);
        assert!(!dir.contains('/'));
        assert!(!dir.contains(':'));
        assert!(dir.starts_with("git_"));
    }

    #[test]
    fn cli_progress_does_not_panic() {
        let mut p = CliProgress;
        p.report("testing 1-2-3");
    }
}
