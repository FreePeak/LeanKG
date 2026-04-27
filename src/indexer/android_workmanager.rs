use crate::db::models::{CodeElement, Relationship};
use regex::Regex;
use std::sync::OnceLock;

static WORKER_CLASS_RE: OnceLock<Regex> = OnceLock::new();
static ONEDTIMEWORK_RE: OnceLock<Regex> = OnceLock::new();
static PERIODICWORK_RE: OnceLock<Regex> = OnceLock::new();
static COROUTINEWORKER_RE: OnceLock<Regex> = OnceLock::new();
static LISTENERWORKER_RE: OnceLock<Regex> = OnceLock::new();

/// Extractor for WorkManager patterns from Kotlin files
pub struct AndroidWorkManagerExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> AndroidWorkManagerExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self { source, file_path }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let content = match std::str::from_utf8(self.source) {
            Ok(s) => s,
            Err(_) => {
                eprintln!("warn: non-UTF-8 content in {}, skipping", self.file_path);
                return (Vec::new(), Vec::new());
            }
        };
        let mut elements = Vec::new();
        let mut relationships = Vec::new();

        // Extract Worker classes
        let workers = self.extract_workers(content);
        for worker in &workers {
            elements.push(worker.clone());
        }

        // Extract OneTimeWorkRequest usages
        let one_time_rels = self.extract_one_time_requests(content, &workers);
        relationships.extend(one_time_rels);

        // Extract PeriodicWorkRequest usages
        let periodic_rels = self.extract_periodic_requests(content, &workers);
        relationships.extend(periodic_rels);

        // Extract CoroutineWorker classes
        let coroutine_workers = self.extract_coroutine_workers(content);
        for cw in &coroutine_workers {
            elements.push(cw.clone());
        }

        // Extract ListenerWorker classes
        let listener_workers = self.extract_listener_workers(content);
        for lw in &listener_workers {
            elements.push(lw.clone());
        }

        (elements, relationships)
    }

    fn extract_workers(&self, content: &str) -> Vec<CodeElement> {
        let mut workers = Vec::new();
        let re = WORKER_CLASS_RE.get_or_init(|| {
            Regex::new(r"(?s)(?:abstract\s+)?class\s+(\w+)\s*\(.*?\)\s*:\s*(?:androidx\.work\.)?(?:Worker|ListenableWorker)")
                .unwrap()
        });

        for cap in re.captures_iter(content) {
            if let Some(name_match) = cap.get(1) {
                let worker_name = name_match.as_str();
                let qualified_name = format!("{}::WorkManager:{}", self.file_path, worker_name);

                workers.push(CodeElement {
                    qualified_name,
                    element_type: "workmanager_worker".to_string(),
                    name: worker_name.to_string(),
                    file_path: self.file_path.to_string(),
                    language: "kotlin".to_string(),
                    metadata: serde_json::json!({
                        "worker_class": worker_name,
                        "worker_type": "Worker",
                    }),
                    ..Default::default()
                });
            }
        }

        workers
    }

    fn extract_coroutine_workers(&self, content: &str) -> Vec<CodeElement> {
        let mut workers = Vec::new();
        let re = COROUTINEWORKER_RE.get_or_init(|| {
            Regex::new(r"(?s)(?:abstract\s+)?class\s+(\w+)\s*\(.*?\)\s*:\s*(?:androidx\.work\.)?CoroutineWorker")
                .unwrap()
        });

        for cap in re.captures_iter(content) {
            if let Some(name_match) = cap.get(1) {
                let worker_name = name_match.as_str();
                // Avoid duplicates with regular Worker extraction
                let qualified_name = format!("{}::WorkManager:{}", self.file_path, worker_name);

                workers.push(CodeElement {
                    qualified_name,
                    element_type: "workmanager_coroutine_worker".to_string(),
                    name: worker_name.to_string(),
                    file_path: self.file_path.to_string(),
                    language: "kotlin".to_string(),
                    metadata: serde_json::json!({
                        "worker_class": worker_name,
                        "worker_type": "CoroutineWorker",
                    }),
                    ..Default::default()
                });
            }
        }

        workers
    }

    fn extract_listener_workers(&self, content: &str) -> Vec<CodeElement> {
        let mut workers = Vec::new();
        let re = LISTENERWORKER_RE.get_or_init(|| {
            Regex::new(r"(?s)(?:abstract\s+)?class\s+(\w+)\s*:\s*(?:androidx\.work\.ListenerWorker|ListenerWorker)")
                .unwrap()
        });

        for cap in re.captures_iter(content) {
            if let Some(name_match) = cap.get(1) {
                let worker_name = name_match.as_str();
                let qualified_name = format!("{}::WorkManager:{}", self.file_path, worker_name);

                workers.push(CodeElement {
                    qualified_name,
                    element_type: "workmanager_listener_worker".to_string(),
                    name: worker_name.to_string(),
                    file_path: self.file_path.to_string(),
                    language: "kotlin".to_string(),
                    metadata: serde_json::json!({
                        "worker_class": worker_name,
                        "worker_type": "ListenerWorker",
                    }),
                    ..Default::default()
                });
            }
        }

        workers
    }

    fn extract_one_time_requests(
        &self,
        content: &str,
        _workers: &[CodeElement],
    ) -> Vec<Relationship> {
        let mut relationships = Vec::new();
        let re = ONEDTIMEWORK_RE
            .get_or_init(|| Regex::new(r"OneTimeWorkRequest(?:Builder)?\s*<(\w+)>").unwrap());

        for cap in re.captures_iter(content) {
            if let Some(worker_match) = cap.get(1) {
                let worker_name = worker_match.as_str();
                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: format!("{}::WorkManager:{}", self.file_path, worker_name),
                    rel_type: "workmanager_works_on".to_string(),
                    confidence: 0.85,
                    metadata: serde_json::json!({
                        "request_type": "OneTimeWorkRequest",
                        "worker_class": worker_name,
                    }),
                });
            }
        }

        relationships
    }

    fn extract_periodic_requests(
        &self,
        content: &str,
        _workers: &[CodeElement],
    ) -> Vec<Relationship> {
        let mut relationships = Vec::new();
        let re = PERIODICWORK_RE
            .get_or_init(|| Regex::new(r"PeriodicWorkRequest(?:Builder)?\s*<(\w+)>").unwrap());

        for cap in re.captures_iter(content) {
            if let Some(worker_match) = cap.get(1) {
                let worker_name = worker_match.as_str();
                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: format!("{}::WorkManager:{}", self.file_path, worker_name),
                    rel_type: "workmanager_works_on".to_string(),
                    confidence: 0.85,
                    metadata: serde_json::json!({
                        "request_type": "PeriodicWorkRequest",
                        "worker_class": worker_name,
                    }),
                });
            }
        }

        relationships
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_worker() {
        let source = r#"
            class SyncWorker(
                context: Context,
                params: WorkerParameters
            ) : Worker(context, params) {
                override fun doWork(): Result {
                    return Result.success()
                }
            }
        "#;
        let extractor =
            AndroidWorkManagerExtractor::new(source.as_bytes(), "./worker/SyncWorker.kt");
        let (elements, _) = extractor.extract();

        let workers: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "workmanager_worker")
            .collect();
        assert_eq!(workers.len(), 1);
        assert_eq!(workers[0].name, "SyncWorker");
    }

    #[test]
    fn test_extract_coroutine_worker() {
        let source = r#"
            class DataFetchWorker(
                context: Context,
                params: WorkerParameters
            ) : CoroutineWorker(context, params) {
                override suspend fun doWork(): Result {
                    return Result.success()
                }
            }
        "#;
        let extractor =
            AndroidWorkManagerExtractor::new(source.as_bytes(), "./worker/DataFetchWorker.kt");
        let (elements, _) = extractor.extract();

        let workers: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "workmanager_coroutine_worker")
            .collect();
        assert_eq!(workers.len(), 1);
        assert_eq!(workers[0].name, "DataFetchWorker");
    }

    #[test]
    fn test_extract_one_time_request() {
        let source = r#"
            val request = OneTimeWorkRequestBuilder<SyncWorker>()
                .build()
        "#;
        let extractor = AndroidWorkManagerExtractor::new(source.as_bytes(), "./app/AppModule.kt");
        let (_, relationships) = extractor.extract();

        let work_rels: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "workmanager_works_on")
            .collect();
        assert!(!work_rels.is_empty());
    }

    #[test]
    fn test_extract_periodic_request() {
        let source = r#"
            val request = PeriodicWorkRequestBuilder<RefreshWorker>(1, TimeUnit.HOURS)
                .build()
        "#;
        let extractor = AndroidWorkManagerExtractor::new(source.as_bytes(), "./app/AppModule.kt");
        let (_, relationships) = extractor.extract();

        let work_rels: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "workmanager_works_on")
            .collect();
        assert!(!work_rels.is_empty());
    }
}
