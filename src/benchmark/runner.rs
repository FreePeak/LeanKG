#![allow(dead_code)]
use crate::benchmark::context_parser::ContextParser;
use crate::benchmark::data::BenchmarkResult;
use crate::benchmark::data::ParsedContext;
use std::error::Error;
use std::path::PathBuf;
use std::process::{Command, Output, Stdio};
use std::time::Duration;

fn kilo_config_path() -> PathBuf {
    dirs::config_dir()
        .unwrap_or_else(|| PathBuf::from(".config"))
        .join("kilo-benchmark")
}

const KILO_MCP_WITH_LEANKG: &str = "mcp_settings_with_leankg.json";
const KILO_MCP_WITHOUT_LEANKG: &str = "mcp_settings_without_leankg.json";
const KILO_MCP_SETTINGS: &str = "kilo.json";

trait WaitWithOutputTimeout {
    fn wait_with_output_timeout(self, duration: Duration) -> Result<Output, ()>;
}

impl WaitWithOutputTimeout for std::process::Child {
    fn wait_with_output_timeout(self, duration: Duration) -> Result<Output, ()> {
        use std::thread;
        use std::time::Instant;

        let start = Instant::now();
        let handle =
            thread::spawn(move || self.wait_with_output().expect("wait_with_output failed"));

        loop {
            if handle.is_finished() {
                return handle.join().map_err(|_| ());
            }
            if start.elapsed() >= duration {
                return Err(());
            }
            thread::sleep(Duration::from_millis(100));
        }
    }
}

pub struct BenchmarkRunner {
    output_dir: PathBuf,
    cli: CliTool,
}

#[derive(Clone)]
pub enum CliTool {
    OpenCode,
    Gemini,
    Kilo,
}

impl BenchmarkRunner {
    pub fn new(output_dir: PathBuf, cli: CliTool) -> Self {
        Self { output_dir, cli }
    }

    pub fn run_with_leankg(&self, prompt: &str) -> BenchmarkResult {
        match self.cli {
            CliTool::Kilo => {
                self.switch_mcp_config(true);
                let (mut result, stdout) = self.run_kilo_with_output(prompt);
                let files = ContextParser::parse_file_paths(&stdout);
                result.context = Some(ParsedContext {
                    files_referenced: files,
                });
                result
            }
            CliTool::OpenCode => self.run_opencode(prompt),
            CliTool::Gemini => self.run_gemini(prompt),
        }
    }

    pub fn run_without_leankg(&self, prompt: &str) -> BenchmarkResult {
        match self.cli {
            CliTool::Kilo => {
                self.switch_mcp_config(false);
                let (mut result, stdout) = self.run_kilo_with_output(prompt);
                let files = ContextParser::parse_file_paths(&stdout);
                result.context = Some(ParsedContext {
                    files_referenced: files,
                });
                result
            }
            CliTool::OpenCode => self.run_opencode(prompt),
            CliTool::Gemini => self.run_gemini(prompt),
        }
    }

    fn switch_mcp_config(&self, with_leankg: bool) {
        let config_dir = kilo_config_path();
        let src = if with_leankg {
            config_dir.join(KILO_MCP_WITH_LEANKG)
        } else {
            config_dir.join(KILO_MCP_WITHOUT_LEANKG)
        };
        let dst = config_dir.join(KILO_MCP_SETTINGS);
        let _ = Command::new("cp").arg(src).arg(dst).output();

        self.kill_leankg_processes();
    }

    fn kill_leankg_processes(&self) {
        let _ = Command::new("pkill")
            .arg("-f")
            .arg("leankg.*mcp-stdio")
            .output();
    }

    fn run_kilo_with_output(&self, prompt: &str) -> (BenchmarkResult, String) {
        // Get the parent of kilo-benchmark (which is ~/.config)
        let config_home = kilo_config_path()
            .parent()
            .map(|p| p.to_string_lossy().to_string())
            .unwrap_or_else(|| String::from(".config"));

        let child = Command::new("kilo")
            .arg("run")
            .arg("--format")
            .arg("json")
            .arg("--auto")
            .arg(prompt)
            .env("XDG_CONFIG_HOME", &config_home)
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .expect("Failed to spawn kilo");

        let output = match child.wait_with_output_timeout(Duration::from_secs(120)) {
            Ok(result) => result,
            Err(_) => {
                return (
                    BenchmarkResult {
                        total_tokens: 0,
                        input_tokens: 0,
                        cached_tokens: 0,
                        token_percent: 0.0,
                        build_time_seconds: 120.0,
                        success: false,
                        context: None,
                    },
                    String::new(),
                );
            }
        };

        let stdout = String::from_utf8_lossy(&output.stdout).to_string();
        let result = self.parse_kilo_output(&stdout);

        (result, stdout)
    }

    fn run_kilo(&self, prompt: &str) -> BenchmarkResult {
        let (result, _) = self.run_kilo_with_output(prompt);
        result
    }

    fn parse_kilo_output(&self, stdout: &str) -> BenchmarkResult {
        let mut total_tokens = 0u32;
        let mut input_tokens = 0u32;
        let mut cached_tokens = 0u32;

        for line in stdout.lines() {
            if let Ok(event) = serde_json::from_str::<serde_json::Value>(line) {
                if event.get("type").and_then(|v| v.as_str()) == Some("step_finish") {
                    if let Some(tokens) = event.get("part").and_then(|p| p.get("tokens")) {
                        total_tokens =
                            tokens.get("total").and_then(|v| v.as_u64()).unwrap_or(0) as u32;
                        input_tokens =
                            tokens.get("input").and_then(|v| v.as_u64()).unwrap_or(0) as u32;
                        cached_tokens = tokens
                            .get("cache")
                            .and_then(|c| c.get("read"))
                            .and_then(|v| v.as_u64())
                            .unwrap_or(0) as u32;
                    }
                }
            }
        }

        BenchmarkResult {
            total_tokens,
            input_tokens,
            cached_tokens,
            token_percent: 0.0,
            build_time_seconds: 0.0,
            success: total_tokens > 0,
            context: None,
        }
    }

    fn run_gemini(&self, prompt: &str) -> BenchmarkResult {
        let output = Command::new("sh")
            .arg("-c")
            .arg(format!(
                "echo '' | gemini -p '{}' -o json 2>/dev/null",
                prompt
            ))
            .output()
            .expect("Failed to execute gemini");

        let stdout = String::from_utf8_lossy(&output.stdout);

        self.parse_gemini_output(&stdout)
    }

    fn run_opencode(&self, prompt: &str) -> BenchmarkResult {
        let output = Command::new("opencode")
            .arg("run")
            .arg("--format")
            .arg("json")
            .arg(prompt)
            .output()
            .expect("Failed to execute opencode");

        let stdout = String::from_utf8_lossy(&output.stdout);
        let stderr = String::from_utf8_lossy(&output.stderr);

        self.parse_opencode_output(&stdout, &stderr, prompt)
    }

    fn parse_gemini_output(&self, stdout: &str) -> BenchmarkResult {
        #[derive(serde::Deserialize)]
        struct GeminiStats {
            stats: Option<Stats>,
        }

        #[derive(serde::Deserialize)]
        struct Stats {
            models: serde_json::Value,
        }

        if let Ok(response) = serde_json::from_str::<GeminiStats>(stdout) {
            if let Some(stats) = response.stats {
                if let Some(models) = stats.models.as_object() {
                    if let Some(first_model) = models.values().next() {
                        if let Some(tokens) = first_model.get("tokens") {
                            let total =
                                tokens.get("total").and_then(|v| v.as_u64()).unwrap_or(0) as u32;
                            let input =
                                tokens.get("input").and_then(|v| v.as_u64()).unwrap_or(0) as u32;
                            let cached =
                                tokens.get("cached").and_then(|v| v.as_u64()).unwrap_or(0) as u32;

                            return BenchmarkResult {
                                total_tokens: total,
                                input_tokens: input,
                                cached_tokens: cached,
                                token_percent: 0.0,
                                build_time_seconds: 0.0,
                                success: true,
                                context: None,
                            };
                        }
                    }
                }
            }
        }

        BenchmarkResult {
            total_tokens: 0,
            input_tokens: 0,
            cached_tokens: 0,
            token_percent: 0.0,
            build_time_seconds: 0.0,
            success: false,
            context: None,
        }
    }

    fn parse_opencode_output(&self, stdout: &str, stderr: &str, prompt: &str) -> BenchmarkResult {
        let mut total_tokens = 0u32;
        let mut input_tokens = 0u32;
        let mut cached_tokens = 0u32;

        for line in stdout.lines() {
            if let Ok(event) = serde_json::from_str::<serde_json::Value>(line) {
                if let Some(tokens_obj) = event.get("tokens") {
                    total_tokens = tokens_obj
                        .get("total")
                        .and_then(|v| v.as_u64())
                        .unwrap_or(0) as u32;
                    input_tokens = tokens_obj
                        .get("input")
                        .and_then(|v| v.as_u64())
                        .unwrap_or(0) as u32;
                    cached_tokens = tokens_obj
                        .get("cached")
                        .and_then(|v| v.as_u64())
                        .unwrap_or(0) as u32;
                    break;
                }
                if let Some(tokens_obj) = event.get("usage") {
                    total_tokens = tokens_obj
                        .get("total")
                        .and_then(|v| v.as_u64())
                        .unwrap_or(0) as u32;
                    input_tokens = tokens_obj
                        .get("input")
                        .and_then(|v| v.as_u64())
                        .unwrap_or(0) as u32;
                    cached_tokens = tokens_obj
                        .get("cache_read")
                        .and_then(|v| v.as_u64())
                        .unwrap_or(0) as u32;
                    break;
                }
            }
        }

        if total_tokens == 0 {
            let prompt_chars = prompt.len() as u32;
            input_tokens = (prompt_chars / 4).max(1);
            let output_chars = stdout.len() as u32;
            let output_tokens = output_chars / 4;
            total_tokens = input_tokens.saturating_add(output_tokens);
            cached_tokens = 0;
        }

        let has_content = !stdout.is_empty() || !stderr.is_empty();

        BenchmarkResult {
            total_tokens,
            input_tokens,
            cached_tokens,
            token_percent: 0.0,
            build_time_seconds: 0.0,
            success: has_content && total_tokens > 0,
            context: None,
        }
    }

    pub fn save_result(&self, result: &BenchmarkResult, name: &str) -> Result<(), Box<dyn Error>> {
        let json_path = self.output_dir.join(format!("{}.json", name));

        let json = serde_json::to_string_pretty(result)?;
        std::fs::write(&json_path, json)?;

        Ok(())
    }

    pub fn save_comparison(
        &self,
        with_leankg: &BenchmarkResult,
        without_leankg: &BenchmarkResult,
        name: &str,
    ) -> Result<(), Box<dyn Error>> {
        let overhead = with_leankg.overhead(without_leankg);

        let comparison = serde_json::json!({
            "task": name,
            "with_leankg": with_leankg,
            "without_leankg": without_leankg,
            "overhead": overhead,
        });

        let json_path = self.output_dir.join(format!("{}-comparison.json", name));
        std::fs::write(&json_path, serde_json::to_string_pretty(&comparison)?)?;

        let md_path = self.output_dir.join(format!("{}-comparison.md", name));
        let mut md = format!(
            "# Benchmark Comparison: {}\n\n## With LeanKG\n- Total Tokens: {}\n- Input: {}\n- Cached: {}\n",
            name,
            with_leankg.total_tokens, with_leankg.input_tokens, with_leankg.cached_tokens
        );

        if let Some(ctx) = &with_leankg.context {
            md.push_str("- Files Referenced: ");
            md.push_str(&format!("{:?}\n", ctx.files_referenced));
        }

        md.push_str(&format!(
            "\n## Without LeanKG\n- Total Tokens: {}\n- Input: {}\n- Cached: {}\n",
            without_leankg.total_tokens, without_leankg.input_tokens, without_leankg.cached_tokens
        ));

        if let Some(ctx) = &without_leankg.context {
            md.push_str("- Files Referenced: ");
            md.push_str(&format!("{:?}\n", ctx.files_referenced));
        }

        md.push_str(&format!(
            "\n## Overhead\n- Token Delta: {}\n",
            overhead.token_delta
        ));

        std::fs::write(&md_path, md)?;

        Ok(())
    }
}
