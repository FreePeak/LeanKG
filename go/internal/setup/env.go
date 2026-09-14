// Package setup is the Go port of the Rust server-side setup pipeline
// (src/setup/mod.rs, FR-ZCP-13): resolve a repo list -> clone -> per-repo
// index -> per-repo embed, driven by `leankg setup --clone|--index|--embed|
// --status`.
//
// Differences from Rust (all deliberate, each with a reason):
//
//   - The git runner is internal/sources (sources.CloneRepo /
//     sources.FetchAndCheckout) instead of a private `git` subprocess block:
//     one argv-only, deadline-bounded git implementation serves both the
//     `--source` verb and the setup clone.
//   - The project config is written through internal/projectcfg
//     (FillMissingKeys/merge), the same read-modify-write the other config
//     writers use, so user fields and the project_path anchor survive.
//   - Indexing and embedding are injected (see Pipeline). Rust re-invoked its
//     own binary and discarded the element count (`index_one` returned None
//     always); this port returns the real count and has no subprocess.
//
// The package deliberately does NOT depend on internal/index or
// internal/embed: stage wiring belongs to the caller (cmd/leankg), which keeps
// this package testable with fakes and avoids an import cycle with the CLI.
package setup

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// GitHost is the git host for clone URLs (LEANKG_GIT_HOST, else github.com).
func GitHost() string {
	if v := strings.TrimSpace(os.Getenv("LEANKG_GIT_HOST")); v != "" {
		return v
	}
	return "github.com"
}

// CloneRoot is the default clone root: LEANKG_CLONE_ROOT, else CLONE_ROOT,
// else the process working directory (Rust fell back to "/app" when the cwd
// was unreadable; the Go engine falls back to "." so a container without a
// workdir cannot silently adopt an unrelated directory).
func CloneRoot() string {
	for _, key := range []string{"LEANKG_CLONE_ROOT", "CLONE_ROOT"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// GitRef is the git ref to clone/pull: LEANKG_GIT_REF, else GIT_REF, else
// "main".
func GitRef() string {
	for _, key := range []string{"LEANKG_GIT_REF", "GIT_REF"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return "main"
}

// GitOwner is the owner used for a bare LEANKG_REPOS entry ("repo" ->
// "<host>/<owner>/repo"). Rust defaulted to the literal "user".
func GitOwner() string {
	if v := strings.TrimSpace(os.Getenv("LEANKG_GIT_OWNER")); v != "" {
		return v
	}
	return "user"
}

// GitToken resolves the git access token: GITLAB_TOKEN, then GIT_TOKEN, then
// GITHUB_TOKEN. Empty means no token (the caller decides whether that is
// fatal — clone_repos treats it as such).
func GitToken() string {
	for _, key := range []string{"GITLAB_TOKEN", "GIT_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

// WorkspaceMaxDepth is the LEANKG_WORKSPACE_DIR walk bound (default 3).
func WorkspaceMaxDepth() int {
	raw := strings.TrimSpace(os.Getenv("LEANKG_WORKSPACE_MAX_DEPTH"))
	if raw == "" {
		return 3
	}
	depth, err := strconv.Atoi(raw)
	if err != nil || depth <= 0 {
		return 3
	}
	return depth
}

// ProjectDirs is the LEANKG_PROJECT_DIRS list: comma-separated mounted dirs
// that are indexed as-is (no clone).
func ProjectDirs() []string {
	return splitList(os.Getenv("LEANKG_PROJECT_DIRS"))
}

// ReposEnv is the LEANKG_REPOS list: comma-separated `host/namespace` paths
// (or bare repo names resolved against LEANKG_GIT_OWNER) that drive the clone
// list.
func ReposEnv() []string {
	return splitList(os.Getenv("LEANKG_REPOS"))
}

// EnvName is the LEANKG_ENV value (default "local"). It is carried for CLI
// parity: the Rust pipeline passed it to `leankg index --env`, and while the
// Go engine's index stage has no env concept, the value still travels through
// Options.Env so the caller can tag output with it.
func EnvName() string {
	if v := strings.TrimSpace(os.Getenv("LEANKG_ENV")); v != "" {
		return v
	}
	return "local"
}

func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// DiscoverGitRepos recursively finds every git repo (a dir holding `.git`)
// under root, walking at most maxDepth levels below it. Used when
// LEANKG_WORKSPACE_DIR points at a monorepo of nested git repos: per-project
// index/embed wants each repo as its own project.
//
// Dot-directories plus `target` and `node_modules` are skipped; once a dir
// contains `.git` it is recorded and not descended into (Rust
// setup::discover_git_repos). The result is sorted.
func DiscoverGitRepos(root string, maxDepth int) []string {
	var out []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > maxDepth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") || name == "target" || name == "node_modules" {
				continue
			}
			path := filepath.Join(dir, name)
			if !isDir(path) {
				continue
			}
			if pathExists(filepath.Join(path, ".git")) {
				out = append(out, path)
				continue
			}
			walk(path, depth+1)
		}
	}
	if !isDir(root) {
		return nil
	}
	walk(root, 0)
	sort.Strings(out)
	return out
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
