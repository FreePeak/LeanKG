package compress

import (
	"strings"
	"testing"
)

func TestCategoryFromCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want CommandCategory
	}{
		{"git status", CatGit},
		{"git log --oneline", CatGit},
		{"git diff", CatGit},
		{"docker ps", CatDocker},
		{"docker-compose up", CatDocker},
		{"npm install", CatNpm},
		{"pnpm build", CatNpm},
		{"yarn add left-pad", CatNpm},
		{"cargo test", CatCargo},
		{"cargo build --release", CatCargo},
		{"kubectl get pods", CatKubectl},
		// Faithful quirk: the "g" prefix check wins, so gh commands classify
		// as Git before the GitHub rule can match.
		{"gh pr list", CatGit},
		// CatGitHub is unreachable in the reference ordering: every "github"
		// command contains "git" and every "gh " command starts with "g",
		// both of which match the Git rule first.
		{"jest --coverage", CatTestRunner},
		{"pytest -q", CatTestRunner},
		// Faithful quirk: the single-letter "g" prefix makes any command
		// starting with g match Git before the TestRunner check.
		{"go test ./...", CatGit},
		{"eslint src/", CatLinter},
		{"tsc --noEmit", CatBuild},
		{"aws s3 ls", CatAws},
		{"psql -c 'select 1'", CatDatabase},
		{"terraform plan", CatTerraform},
		{"python script.py", CatPython},
		{"pip3 install foo", CatPython},
		{"make all", CatOther},
		{"echo hello", CatOther},
	}
	for _, tt := range tests {
		if got := CategoryFromCommand(tt.cmd); got != tt.want {
			t.Errorf("CategoryFromCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestShellCompressorGitStatus(t *testing.T) {
	c := NewShellCompressor()
	output := "On branch main\nYour branch is up to date with 'origin/main'.\n\nChanges not staged for commit:\n  modified:   src/main.rs\n  modified:   src/lib.rs"
	compressed := c.Compress("git status", output)
	if !strings.Contains(compressed, "branch") {
		t.Errorf("compressed output should keep the branch line:\n%s", compressed)
	}
	// Blank lines are dropped and order is preserved.
	lines := strings.Split(compressed, "\n")
	if len(lines) != 5 {
		t.Errorf("expected 5 kept lines (blank dropped), got %d:\n%s", len(lines), compressed)
	}
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			t.Errorf("blank lines should be dropped:\n%s", compressed)
		}
	}
}

func TestShellCompressorCapsAtFiftyLines(t *testing.T) {
	c := NewShellCompressor()
	var b strings.Builder
	for i := 0; i < 80; i++ {
		b.WriteString("some output line\n")
	}
	compressed := c.Compress("make all", b.String())
	if got := len(strings.Split(compressed, "\n")); got != 50 {
		t.Errorf("expected 50 lines, got %d", got)
	}
}

func TestShellCompressorKubectlHeader(t *testing.T) {
	c := NewShellCompressor()
	// The header pattern is unanchored, so it applies mid-document.
	output := "pod/app running\nNAME     READY   STATUS\npod/x running"
	compressed := c.Compress("kubectl get pods", output)
	if !strings.Contains(compressed, "NAME  RDY  ST") {
		t.Errorf("k8s header should compress:\n%s", compressed)
	}
}
