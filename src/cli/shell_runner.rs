use crate::compress::LeanKGCompressor;
use std::process::Command;

pub struct ShellRunner {
    compressor: LeanKGCompressor,
    compress_enabled: bool,
}

impl ShellRunner {
    pub fn new(compress_enabled: bool) -> Self {
        Self {
            compressor: LeanKGCompressor::new(),
            compress_enabled,
        }
    }

    pub fn run(&self, program: &str, args: &[&str], description: &str) -> Result<String, String> {
        let output = Command::new(program)
            .args(args)
            .output()
            .map_err(|e| format!("Failed to execute {}: {}", program, e))?;

        let stdout = String::from_utf8_lossy(&output.stdout).to_string();
        let stderr = String::from_utf8_lossy(&output.stderr).to_string();

        if !output.status.success() {
            return Err(format!(
                "{} failed with exit code {:?}\n{}",
                description,
                output.status.code(),
                stderr
            ));
        }

        if self.compress_enabled {
            let cmd_str = format!("{} {}", program, args.join(" "));
            Ok(self.compressor.compress(&cmd_str, &stdout))
        } else {
            Ok(stdout)
        }
    }

    pub fn run_with_stderr(
        &self,
        program: &str,
        args: &[&str],
        description: &str,
    ) -> Result<(String, String), String> {
        let output = Command::new(program)
            .args(args)
            .output()
            .map_err(|e| format!("Failed to execute {}: {}", program, e))?;

        let stdout = String::from_utf8_lossy(&output.stdout).to_string();
        let stderr = String::from_utf8_lossy(&output.stderr).to_string();

        if !output.status.success() {
            return Err(format!(
                "{} failed with exit code {:?}\n{}",
                description,
                output.status.code(),
                stderr
            ));
        }

        if self.compress_enabled {
            let cmd_str = format!("{} {}", program, args.join(" "));
            Ok((self.compressor.compress(&cmd_str, &stdout), stderr))
        } else {
            Ok((stdout, stderr))
        }
    }

    pub fn run_and_compress(&self, cmd: &str, output: &str) -> String {
        if self.compress_enabled {
            self.compressor.compress(cmd, output)
        } else {
            output.to_string()
        }
    }

    pub fn estimate_savings(&self, original: &str, compressed: &str) -> f64 {
        self.compressor.estimate_savings(original, compressed)
    }
}

impl Default for ShellRunner {
    fn default() -> Self {
        Self::new(false)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_shell_runner_without_compress() {
        let runner = ShellRunner::new(false);
        let result = runner.run_with_stderr("echo", &["hello"], "echo hello");
        assert!(result.is_ok());
        let (stdout, _) = result.unwrap();
        assert!(stdout.contains("hello"));
    }

    #[test]
    fn test_run_and_compress_disabled() {
        let runner = ShellRunner::new(false);
        let output = "test output";
        let result = runner.run_and_compress("echo test", output);
        assert_eq!(result, output);
    }

    #[test]
    fn test_run_and_compress_enabled() {
        let runner = ShellRunner::new(true);
        let output = "test output";
        let result = runner.run_and_compress("echo test", output);
        assert_eq!(result, output);
    }
}
