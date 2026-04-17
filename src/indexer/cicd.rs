use crate::db::models::{CodeElement, Relationship};
use serde_yaml::Value;

pub struct CicdYamlExtractor {
    source: Vec<u8>,
    file_path: String,
}

#[derive(Debug)]
enum CicdPlatform {
    GitHubActions,
    GitLabCI,
    AzurePipelines,
    Unknown,
}

impl CicdYamlExtractor {
    pub fn new(source: &[u8], file_path: &str) -> Self {
        Self {
            source: source.to_vec(),
            file_path: file_path.to_string(),
        }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut elements = Vec::new();
        let content = String::from_utf8_lossy(&self.source);
        let source_str = content.as_ref();

        let yaml: Value = match serde_yaml::from_str(source_str) {
            Ok(v) => v,
            Err(_) => return (elements, Vec::new()),
        };

        let platform = self.detect_platform(&yaml);

        let line_count = source_str.lines().count() as u32;
        elements.push(CodeElement {
            qualified_name: self.file_path.clone(),
            element_type: "cicd".to_string(),
            name: self
                .file_path
                .rsplit('/')
                .next()
                .unwrap_or(&self.file_path)
                .to_string(),
            file_path: self.file_path.clone(),
            line_start: 1,
            line_end: line_count,
            language: "yaml".to_string(),
            parent_qualified: None,
            metadata: serde_json::json!({
                "ci_platform": format!("{:?}", platform).to_lowercase(),
            }),
            ..Default::default()
        });

        match platform {
            CicdPlatform::GitHubActions => self.extract_github_actions(&yaml, &mut elements),
            CicdPlatform::GitLabCI => self.extract_gitlab_ci(&yaml, &mut elements),
            CicdPlatform::AzurePipelines => self.extract_azure_pipelines(&yaml, &mut elements),
            CicdPlatform::Unknown => {}
        }

        (elements, Vec::new())
    }

    fn detect_platform(&self, yaml: &Value) -> CicdPlatform {
        if let Some(obj) = yaml.as_mapping() {
            for key in obj.keys() {
                if let Some(k) = key.as_str() {
                    if k == "on" || k == "jobs" {
                        return CicdPlatform::GitHubActions;
                    }
                    if k == "stages" {
                        return CicdPlatform::GitLabCI;
                    }
                    if k == "pool" || k == "trigger" {
                        return CicdPlatform::AzurePipelines;
                    }
                }
            }
        }
        CicdPlatform::Unknown
    }

    fn extract_github_actions(&self, yaml: &Value, elements: &mut Vec<CodeElement>) {
        if let Some(jobs) = yaml.get("jobs").and_then(|v| v.as_mapping()) {
            for (job_name, job_details) in jobs {
                let job_name_str = job_name.as_str().unwrap_or("unnamed");
                let qualified_name = format!("{}::{}", self.file_path, job_name_str);

                let mut metadata = serde_json::json!({
                    "name": job_name_str,
                });

                if let Some(job_obj) = job_details.as_mapping() {
                    if let Some(steps) = job_obj.get("steps").and_then(|v| v.as_sequence()) {
                        metadata["step_count"] = serde_json::json!(steps.len());
                    }
                    if let Some(runs_on) = job_obj.get("runs-on") {
                        metadata["runs_on"] = serde_json::json!(runs_on);
                    }
                }

                elements.push(CodeElement {
                    qualified_name: qualified_name.clone(),
                    element_type: "cicd_job".to_string(),
                    name: job_name_str.to_string(),
                    file_path: self.file_path.clone(),
                    line_start: 1,
                    line_end: 1,
                    language: "yaml".to_string(),
                    parent_qualified: Some(self.file_path.clone()),
                    metadata,
                    ..Default::default()
                });

                if let Some(job_obj) = job_details.as_mapping() {
                    if let Some(steps) = job_obj.get("steps").and_then(|v| v.as_sequence()) {
                        for (idx, step) in steps.iter().enumerate() {
                            let step_name = step
                                .get("name")
                                .and_then(|v| v.as_str())
                                .unwrap_or("unnamed_step");
                            let step_qualified = format!("{}::step_{}", qualified_name, idx);

                            let mut step_metadata = serde_json::json!({
                                "name": step_name,
                            });

                            if let Some(cmd) = step.get("run") {
                                step_metadata["command"] = serde_json::json!(cmd);
                            }
                            if let Some(img) = step.get("uses") {
                                step_metadata["image"] = serde_json::json!(img);
                            }

                            elements.push(CodeElement {
                                qualified_name: step_qualified,
                                element_type: "cicd_step".to_string(),
                                name: step_name.to_string(),
                                file_path: self.file_path.clone(),
                                line_start: 1,
                                line_end: 1,
                                language: "yaml".to_string(),
                                parent_qualified: Some(qualified_name.clone()),
                                metadata: step_metadata,
                                ..Default::default()
                            });
                        }
                    }
                }
            }
        }
    }

    fn extract_gitlab_ci(&self, yaml: &Value, elements: &mut Vec<CodeElement>) {
        if let Some(stages) = yaml.get("stages").and_then(|v| v.as_sequence()) {
            for (stage_idx, stage) in stages.iter().enumerate() {
                let stage_name = stage.as_str().unwrap_or("unnamed_stage");
                let stage_qualified = format!("{}::stage_{}", self.file_path, stage_idx);

                elements.push(CodeElement {
                    qualified_name: stage_qualified.clone(),
                    element_type: "cicd_job".to_string(),
                    name: stage_name.to_string(),
                    file_path: self.file_path.clone(),
                    line_start: 1,
                    line_end: 1,
                    language: "yaml".to_string(),
                    parent_qualified: Some(self.file_path.clone()),
                    metadata: serde_json::json!({
                        "name": stage_name,
                        "stage_index": stage_idx,
                    }),
                    ..Default::default()
                });
            }
        }

        for (job_name, job_details) in
            yaml.as_mapping()
                .iter()
                .flat_map(|m| m.iter())
                .filter(|(k, _)| {
                    if let Some(s) = k.as_str() {
                        ![
                            "stages",
                            "image",
                            "services",
                            "variables",
                            "before_script",
                            "after_script",
                            "cache",
                        ]
                        .contains(&s)
                    } else {
                        true
                    }
                })
        {
            let job_name_str = job_name.as_str().unwrap_or("unnamed");
            if job_name_str == "stages" {
                continue;
            }

            let qualified_name = format!("{}::{}", self.file_path, job_name_str);
            let mut metadata = serde_json::json!({
                "name": job_name_str,
            });

            if let Some(job_obj) = job_details.as_mapping() {
                if let Some(script) = job_obj.get("script") {
                    metadata["script"] = serde_json::json!(script);
                }
                if let Some(stage) = job_obj.get("stage") {
                    metadata["stage"] = serde_json::json!(stage);
                }
            }

            elements.push(CodeElement {
                qualified_name,
                element_type: "cicd_job".to_string(),
                name: job_name_str.to_string(),
                file_path: self.file_path.clone(),
                line_start: 1,
                line_end: 1,
                language: "yaml".to_string(),
                parent_qualified: Some(self.file_path.clone()),
                metadata,
                ..Default::default()
            });
        }
    }

    fn extract_azure_pipelines(&self, yaml: &Value, elements: &mut Vec<CodeElement>) {
        if let Some(stages) = yaml.get("stages").and_then(|v| v.as_sequence()) {
            for (stage_idx, stage) in stages.iter().enumerate() {
                let stage_name = stage
                    .get("stage")
                    .and_then(|v| v.get("name"))
                    .and_then(|v| v.as_str())
                    .unwrap_or("unnamed_stage");

                let stage_qualified = format!("{}::{}", self.file_path, stage_name);

                elements.push(CodeElement {
                    qualified_name: stage_qualified.clone(),
                    element_type: "cicd_job".to_string(),
                    name: stage_name.to_string(),
                    file_path: self.file_path.clone(),
                    line_start: 1,
                    line_end: 1,
                    language: "yaml".to_string(),
                    parent_qualified: Some(self.file_path.clone()),
                    metadata: serde_json::json!({
                        "name": stage_name,
                        "stage_index": stage_idx,
                    }),
                    ..Default::default()
                });

                if let Some(jobs) = stage
                    .get("stage")
                    .and_then(|v| v.get("jobs"))
                    .and_then(|v| v.as_sequence())
                {
                    for (job_idx, job) in jobs.iter().enumerate() {
                        let job_name = job
                            .get("job")
                            .and_then(|v| v.get("name"))
                            .and_then(|v| v.as_str())
                            .unwrap_or("unnamed_job");

                        let job_qualified = format!("{}::job_{}", stage_qualified, job_idx);

                        elements.push(CodeElement {
                            qualified_name: job_qualified.clone(),
                            element_type: "cicd_job".to_string(),
                            name: job_name.to_string(),
                            file_path: self.file_path.clone(),
                            line_start: 1,
                            line_end: 1,
                            language: "yaml".to_string(),
                            parent_qualified: Some(stage_qualified.clone()),
                            metadata: serde_json::json!({
                                "name": job_name,
                            }),
                            ..Default::default()
                        });

                        if let Some(steps) = job
                            .get("job")
                            .and_then(|v| v.get("steps"))
                            .and_then(|v| v.as_sequence())
                        {
                            for (step_idx, step) in steps.iter().enumerate() {
                                let step_name = step
                                    .get("task")
                                    .and_then(|v| v.get("displayName"))
                                    .and_then(|v| v.as_str())
                                    .unwrap_or("unnamed_step");

                                let step_qualified =
                                    format!("{}::step_{}", job_qualified, step_idx);

                                elements.push(CodeElement {
                                    qualified_name: step_qualified,
                                    element_type: "cicd_step".to_string(),
                                    name: step_name.to_string(),
                                    file_path: self.file_path.clone(),
                                    line_start: 1,
                                    line_end: 1,
                                    language: "yaml".to_string(),
                                    parent_qualified: Some(job_qualified.clone()),
                                    metadata: serde_json::json!({
                                        "name": step_name,
                                    }),
                                    ..Default::default()
                                });
                            }
                        }
                    }
                }
            }
        } else if let Some(jobs) = yaml.get("jobs").and_then(|v| v.as_sequence()) {
            for job in jobs.iter() {
                let job_name = job
                    .get("name")
                    .or_else(|| job.get("job"))
                    .and_then(|v| v.as_str())
                    .unwrap_or("unnamed_job");

                let job_qualified = format!("{}::{}", self.file_path, job_name);

                elements.push(CodeElement {
                    qualified_name: job_qualified.clone(),
                    element_type: "cicd_job".to_string(),
                    name: job_name.to_string(),
                    file_path: self.file_path.clone(),
                    line_start: 1,
                    line_end: 1,
                    language: "yaml".to_string(),
                    parent_qualified: Some(self.file_path.clone()),
                    metadata: serde_json::json!({
                        "name": job_name,
                    }),
                    ..Default::default()
                });

                if let Some(steps) = job.get("steps").and_then(|v| v.as_sequence()) {
                    for (step_idx, step) in steps.iter().enumerate() {
                        let step_name = step
                            .get("task")
                            .and_then(|v| v.get("displayName"))
                            .and_then(|v| v.as_str())
                            .unwrap_or("unnamed_step");

                        let step_qualified = format!("{}::step_{}", job_qualified, step_idx);

                        elements.push(CodeElement {
                            qualified_name: step_qualified,
                            element_type: "cicd_step".to_string(),
                            name: step_name.to_string(),
                            file_path: self.file_path.clone(),
                            line_start: 1,
                            line_end: 1,
                            language: "yaml".to_string(),
                            parent_qualified: Some(job_qualified.clone()),
                            metadata: serde_json::json!({
                                "name": step_name,
                            }),
                            ..Default::default()
                        });
                    }
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_github_actions() {
        let source = br#"
name: CI

on:
  push:
    branches: [ main ]
  pull_request:

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - name: Run tests
        run: make test
  deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Deploy
        run: make deploy
"#;
        let extractor = CicdYamlExtractor::new(source, ".github/workflows/ci.yml");
        let (elements, _) = extractor.extract();

        let cicd_files: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "cicd")
            .collect();
        assert_eq!(cicd_files.len(), 1);

        let jobs: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "cicd_job")
            .collect();
        assert_eq!(jobs.len(), 2);

        let steps: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "cicd_step")
            .collect();
        assert_eq!(steps.len(), 3);
    }

    #[test]
    fn test_extract_gitlab_ci() {
        let source = br#"
stages:
  - build
  - test
  - deploy

build:
  stage: build
  script:
    - make build

test:
  stage: test
  script:
    - make test

deploy:
  stage: deploy
  script:
    - make deploy
"#;
        let extractor = CicdYamlExtractor::new(source, ".gitlab-ci.yml");
        let (elements, _) = extractor.extract();

        let cicd_files: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "cicd")
            .collect();
        assert_eq!(cicd_files.len(), 1);

        let jobs: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "cicd_job")
            .collect();
        assert_eq!(jobs.len(), 6);
    }

    #[test]
    fn test_extract_azure_pipelines() {
        let source = br#"
trigger:
  - main

pool:
  vmImage: 'ubuntu-latest'

jobs:
  - job: Build
    steps:
      - task: CmdLine@1
        displayName: 'Build'
        inputs:
          command: line
"#;
        let extractor = CicdYamlExtractor::new(source, "azure-pipelines.yml");
        let (elements, _) = extractor.extract();

        let cicd_files: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "cicd")
            .collect();
        assert_eq!(cicd_files.len(), 1);
    }
}
