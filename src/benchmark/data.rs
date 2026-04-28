use serde::{Deserialize, Serialize};
use std::error::Error;
use std::path::Path;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ParsedContext {
    pub files_referenced: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BenchmarkResult {
    pub total_tokens: u32,
    pub input_tokens: u32,
    pub cached_tokens: u32,
    pub token_percent: f32,
    pub build_time_seconds: f32,
    pub success: bool,
    #[serde(default)]
    pub context: Option<ParsedContext>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PromptTask {
    pub id: String,
    pub prompt: String,
    pub expected: Vec<String>,
    #[serde(default)]
    pub expected_files: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PromptCategory {
    pub name: String,
    pub description: String,
    pub tasks: Vec<PromptTask>,
}

impl PromptCategory {
    pub fn from_yaml(path: &Path) -> Result<Self, Box<dyn Error>> {
        let content = std::fs::read_to_string(path)?;
        let category: PromptCategory = serde_yaml::from_str(&content)?;
        Ok(category)
    }

    pub fn load_all(prompts_dir: &Path) -> Result<Vec<Self>, Box<dyn Error>> {
        let mut categories = Vec::new();
        for entry in std::fs::read_dir(prompts_dir)? {
            let entry = entry?;
            let path = entry.path();
            if path.extension().and_then(|s| s.to_str()) == Some("yaml") {
                categories.push(Self::from_yaml(&path)?);
            }
        }
        Ok(categories)
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OverheadResult {
    pub token_delta: i32,
    pub token_delta_percent: f32,
    pub time_delta: f32,
}

impl BenchmarkResult {
    pub fn overhead(&self, other: &BenchmarkResult) -> OverheadResult {
        OverheadResult {
            token_delta: self.total_tokens as i32 - other.total_tokens as i32,
            token_delta_percent: self.token_percent - other.token_percent,
            time_delta: self.build_time_seconds - other.build_time_seconds,
        }
    }
}
