#![allow(dead_code)]
use std::process::Command;

pub fn get_current_branch() -> Result<String, Box<dyn std::error::Error>> {
    let output = Command::new("git")
        .args(["rev-parse", "--abbrev-ref", "HEAD"])
        .output()?;

    if !output.status.success() {
        return Ok("detached".to_string());
    }
    Ok(String::from_utf8_lossy(&output.stdout).trim().to_string())
}

pub fn get_current_tag() -> Result<Option<String>, Box<dyn std::error::Error>> {
    let output = Command::new("git")
        .args(["describe", "--tags", "--exact-match", "HEAD"])
        .output()?;

    if output.status.success() {
        Ok(Some(
            String::from_utf8_lossy(&output.stdout).trim().to_string(),
        ))
    } else {
        Ok(None)
    }
}

pub fn infer_environment(branch: &str) -> String {
    match branch {
        "main" | "master" => "production".to_string(),
        b if b.starts_with("release/") => "staging".to_string(),
        b if b.starts_with("hotfix/") => "production".to_string(),
        b if b.starts_with("develop") => "dev".to_string(),
        _ => "upcoming".to_string(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_infer_environment() {
        assert_eq!(infer_environment("main"), "production");
        assert_eq!(infer_environment("master"), "production");
        assert_eq!(infer_environment("release/v1.0"), "staging");
        assert_eq!(infer_environment("hotfix/urgent-fix"), "production");
        assert_eq!(infer_environment("develop"), "dev");
        assert_eq!(infer_environment("feature/new-login"), "upcoming");
        assert_eq!(infer_environment("fix/bug-123"), "upcoming");
        assert_eq!(infer_environment("detached"), "upcoming");
    }
}
