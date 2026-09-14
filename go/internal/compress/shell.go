package compress

import (
	"regexp"
	"strings"
)

// CommandCategory classifies a shell command (Rust shell::CommandCategory).
type CommandCategory uint8

const (
	CatGit CommandCategory = iota
	CatDocker
	CatNpm
	CatCargo
	CatKubectl
	CatGitHub
	CatTestRunner
	CatLinter
	CatBuild
	CatPython
	CatAws
	CatDatabase
	CatTerraform
	CatOther
)

// CategoryFromCommand classifies a command string (Rust
// CommandCategory::from_command), including its quirky single-letter
// prefixes and the broad "tf" substring match.
func CategoryFromCommand(cmd string) CommandCategory {
	cmdLower := strings.ToLower(cmd)
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(cmdLower, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("git") || strings.HasPrefix(cmdLower, "g") ||
		has("status", "log", "diff", "commit", "push", "pull", "branch", "checkout"):
		return CatGit
	case has("docker") || strings.HasPrefix(cmdLower, "d"):
		return CatDocker
	case has("npm", "pnpm", "yarn"):
		return CatNpm
	case has("cargo") || strings.HasPrefix(cmdLower, "c"):
		return CatCargo
	case has("kubectl") || strings.HasPrefix(cmdLower, "k"):
		return CatKubectl
	case has("gh ", "github"):
		return CatGitHub
	case has("jest", "vitest", "pytest", "go test", "playwright", "rspec"):
		return CatTestRunner
	case has("eslint", "prettier", "ruff", "clippy"):
		return CatLinter
	case has("tsc", "vite", "next"):
		return CatBuild
	case has("aws"):
		return CatAws
	case has("psql", "mysql"):
		return CatDatabase
	case has("terraform", "tf"):
		return CatTerraform
	case has("python", "pip", "pip3"):
		return CatPython
	default:
		return CatOther
	}
}

// CompressionPattern is one output rewrite rule (Rust shell::CompressionPattern).
type CompressionPattern struct {
	Regex       *regexp.Regexp
	Replacement string
	Description string
}

// ShellCompressor applies category-specific output patterns (Rust
// shell::ShellCompressor).
type ShellCompressor struct {
	patterns map[CommandCategory][]CompressionPattern
}

// NewShellCompressor builds the pattern table (Rust ShellCompressor::new).
func NewShellCompressor() *ShellCompressor {
	must := func(expr, replacement, description string) CompressionPattern {
		return CompressionPattern{Regex: regexp.MustCompile(expr), Replacement: replacement, Description: description}
	}
	patterns := map[CommandCategory][]CompressionPattern{
		CatGit: {
			must(`^On branch .+$`, "→ branch", "Current branch"),
			must(`^Your branch is (up to date|ahead|behind).+$`, "", "Branch sync status"),
			must(`^Changes (not staged|to be committed):$`, "Changes:", "Change section header"),
			must(`^\s+(modified|new file|deleted):\s+`, "  • ", "File change indicator"),
			must(`^commit [a-f0-9]{7,}$`, "commit @", "Commit hash"),
			must(`\d+ files? changed`, "~ files changed", "File count"),
			must(`\d+ insertions?\(\+\)`, "+ lines", "Insertions"),
			must(`\d+ deletions?\(\-\)`, "- lines", "Deletions"),
			must(`Author: .+$`, "Author: @", "Author"),
			must(`Date: .+$`, "Date: @", "Date"),
		},
		CatDocker: {
			must(`^[a-f0-9]{12}$`, "IMAGE_ID", "Docker image ID"),
			must(`^\d+\.\d+\.\d+\s+`, "v", "Version prefix"),
			must(`About a minute ago`, "~1m ago", "Time ago"),
			must(`About an hour ago`, "~1h ago", "Time ago"),
			must(`\d+ hours ago`, "~Nh ago", "Hours ago"),
			must(`Exited \(\d+\)`, "Exited", "Exit status"),
		},
		CatNpm: {
			must(`^\s+├──\s+`, "├─ ", "Tree branch"),
			must(`^\s+└──\s+`, "└─ ", "Tree end"),
			must(`^\+--- .+$`, "+--- ", "Package tree"),
			must(`^added \d+ packages? in .+$`, "packages added", "Packages added"),
		},
		CatCargo: {
			must(`^\s+Compiling .+$`, "  compile", "Compiling"),
			must(`^\s+Finished .+$`, "  done", "Finished"),
			must(`^\s+Running .+$`, "  run", "Running"),
		},
		CatKubectl: {
			must(`NAME\s+READY\s+STATUS`, "NAME  RDY  ST", "K8s header"),
			must(`^\d+/\d+\s+`, "x/y ", "Ready count"),
		},
	}
	return &ShellCompressor{patterns: patterns}
}

// Compress applies the command's category patterns, then drops blank lines
// and caps at 50 output lines (Rust ShellCompressor::compress).
func (c *ShellCompressor) Compress(cmd, output string) string {
	result := output
	if patterns, ok := c.patterns[CategoryFromCommand(cmd)]; ok {
		for _, pattern := range patterns {
			result = pattern.Regex.ReplaceAllLiteralString(result, pattern.Replacement)
		}
	}

	var kept []string
	for _, line := range splitLines(result) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		kept = append(kept, line)
		if len(kept) == 50 {
			break
		}
	}
	return strings.Join(kept, "\n")
}
