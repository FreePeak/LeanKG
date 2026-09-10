//! Mnemopi-compatible bank naming + scoping matrix (PRD §3.5, installed
//! ground truth: mnemopi/config.ts:128-186, 253-263).

use wyhash::wyhash;

/// 64-bit wyhash of the absolute cwd bytes, rendered base36 — the exact
/// transform OMP's Bun runtime applies (`Bun.hash(absCwd).toString(36)`).
/// Compatibility-critical: banks fragment if the hash differs.
fn wyhash36(abs_cwd: &str) -> String {
    let h = wyhash(abs_cwd.as_bytes(), 0);
    base36(h)
}

fn base36(mut v: u64) -> String {
    const ALPHABET: &[u8] = b"0123456789abcdefghijklmnopqrstuvwxyz";
    if v == 0 {
        return "0".into();
    }
    let mut out = Vec::new();
    while v > 0 {
        out.push(ALPHABET[(v % 36) as usize]);
        v /= 36;
    }
    out.reverse();
    String::from_utf8(out).expect("base36 alphabet is ASCII")
}

/// Bank names are ≤64 chars of [A-Za-z0-9_-] (mnemopi/config.ts:176-186).
pub fn sanitize_bank(name: &str) -> String {
    let mut out: String = name
        .chars()
        .map(|c| {
            if c.is_ascii_alphanumeric() || c == '_' || c == '-' {
                c
            } else {
                '_'
            }
        })
        .collect();
    out.truncate(64);
    if out.is_empty() {
        out.push('_');
    }
    out
}

/// `<basename(cwd)>-<wyhash36(abs cwd)>` — derived from the CWD ONLY, never
/// the git root (upstream bug #2412: git-root resolution fragments banks
/// when a .git appears/disappears).
pub fn mnemopi_bank_name(cwd: &std::path::Path) -> String {
    let abs = std::fs::canonicalize(cwd).unwrap_or_else(|_| cwd.to_path_buf());
    let basename = abs
        .file_name()
        .map(|n| n.to_string_lossy().into_owned())
        .unwrap_or_else(|| abs.display().to_string());
    let h = wyhash36(&abs.display().to_string());
    sanitize_bank(&format!("{basename}-{h}"))
}

/// Scoping matrix (computeMnemopiBankScope): which banks a WRITE targets
/// and which banks a READ merges, per scope mode.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum BankScope {
    /// write + read shared
    Global,
    /// write + read project (default)
    PerProject,
    /// write project; read [project, shared] merged + deduped
    PerProjectTagged,
}

impl BankScope {
    pub fn parse_scope(s: &str) -> Self {
        match s {
            "global" => Self::Global,
            "per-project-tagged" => Self::PerProjectTagged,
            _ => Self::PerProject,
        }
    }

    pub fn write_bank(&self, project_bank: &str, shared_bank: &str) -> String {
        match self {
            Self::Global => shared_bank.to_string(),
            _ => project_bank.to_string(),
        }
    }

    /// Read banks in merge order (project first; tagged mode appends shared).
    pub fn read_banks(&self, project_bank: &str, shared_bank: &str) -> Vec<String> {
        match self {
            Self::Global => vec![shared_bank.to_string()],
            Self::PerProject => vec![project_bank.to_string()],
            Self::PerProjectTagged => vec![project_bank.to_string(), shared_bank.to_string()],
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn bank_name_is_basename_plus_base36_hash() {
        // /tmp/leankg-bank-test (canonicalized) → "leankg-bank-test-<hash>"
        let dir = std::env::temp_dir().join("leankg-bank-test");
        std::fs::create_dir_all(&dir).unwrap();
        let name = mnemopi_bank_name(&dir);
        assert!(name.starts_with("leankg-bank-test-"), "{name}");
        let hash = &name["leankg-bank-test-".len()..];
        assert!(!hash.is_empty() && hash.len() <= 13, "base36 u64: {hash}");
        assert!(
            name.chars()
                .all(|c| c.is_ascii_alphanumeric() || c == '_' || c == '-'),
            "charset: {name}"
        );
        assert!(name.len() <= 64);
    }

    #[test]
    fn bank_name_derived_from_cwd_not_git_root() {
        // Same directory, with and without a nested .git marker: the bank
        // MUST be identical (upstream bug #2412).
        let dir = std::env::temp_dir().join("leankg-bank-git-test");
        std::fs::create_dir_all(dir.join(".git")).unwrap();
        let with = mnemopi_bank_name(&dir);
        std::fs::remove_dir_all(dir.join(".git")).unwrap();
        let without = mnemopi_bank_name(&dir);
        assert_eq!(with, without, "git-root presence must not fragment banks");
    }

    #[test]
    fn sanitize_limits_charset_and_length() {
        let s = sanitize_bank("weird / name!! with unicode: ✓✓ and a very long tail that exceeds sixty-four characters for sure");
        assert!(s.len() <= 64);
        assert!(s
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || c == '_' || c == '-'));
        assert_eq!(sanitize_bank(""), "_");
    }

    #[test]
    fn scoping_matrix_matches_mnemopi() {
        let (p, sh) = ("proj-bank", "shared-bank");
        assert_eq!(BankScope::Global.write_bank(p, sh), "shared-bank");
        assert_eq!(
            BankScope::Global.read_banks(p, sh),
            vec!["shared-bank".to_string()]
        );
        assert_eq!(BankScope::PerProject.write_bank(p, sh), "proj-bank");
        assert_eq!(
            BankScope::PerProject.read_banks(p, sh),
            vec!["proj-bank".to_string()]
        );
        assert_eq!(BankScope::PerProjectTagged.write_bank(p, sh), "proj-bank");
        assert_eq!(
            BankScope::PerProjectTagged.read_banks(p, sh),
            vec!["proj-bank".to_string(), "shared-bank".to_string()]
        );
    }

    #[test]
    fn wyhash36_is_deterministic() {
        assert_eq!(wyhash36("abc"), wyhash36("abc"));
        assert_ne!(wyhash36("abc"), wyhash36("abd"));
    }
}
