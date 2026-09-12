package setup

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// NestedGitMaxDepth bounds the nested-repo search below a workspace root
// (Rust indexer::git_workspace::NESTED_GIT_MAX_DEPTH).
const NestedGitMaxDepth = 4

// gitWorkspaceSkipDirs are never descended into while looking for nested
// repos (Rust indexer::git_workspace::SKIP_DIR_NAMES).
var gitWorkspaceSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"target":       true,
	"dist":         true,
	"build":        true,
	".worktrees":   true,
	".claude":      true,
	"browser-data": true,
}

// gitTimeout bounds one `git` invocation (the freshness probe must never hang
// a server start on a network filesystem).
const gitTimeout = 30 * time.Second

// Workspace is a project root probed for git context, implementing
// indexgate.GitProbe. Some roots (a polyrepo mount like /workspace-other) are
// not themselves repositories but contain many nested repos, so both methods
// consider nested repos.
type Workspace struct {
	// Root is the project root to probe.
	Root string
	// HasGitContextResult/LastCommitResult let a caller inject an answer
	// without stat-ing or shelling out (the CLI's doctor paths do this);
	// nil/zero values mean "probe for real".
	HasGitContextResult *bool
	LastCommitResult    *int64
}

// HasGitContext reports whether Root is a git work tree, or contains nested
// git repos (bounded depth).
func (w Workspace) HasGitContext() bool {
	if w.HasGitContextResult != nil {
		return *w.HasGitContextResult
	}
	return IsGitRepoAt(w.Root) || len(DiscoverNestedGitRepos(w.Root)) > 0
}

// LastCommitTime is the newest HEAD commit timestamp (Unix seconds) across
// the root repo or all nested repos. It returns 0 when no repository could be
// read — the gate treats that as "unknown", which is safest when
// require_git_for_auto_index is false and the freshness comparison is the only
// thing standing between a start and a pointless reindex.
func (w Workspace) LastCommitTime() int64 {
	if w.LastCommitResult != nil {
		return *w.LastCommitResult
	}
	if IsGitRepoAt(w.Root) {
		if ts, err := LastCommitTimeAt(w.Root); err == nil {
			return ts
		}
		return 0
	}
	repos := DiscoverNestedGitRepos(w.Root)
	var maxTS int64
	anyOK := false
	for _, repo := range repos {
		ts, err := LastCommitTimeAt(repo)
		if err != nil {
			continue
		}
		anyOK = true
		if ts > maxTS {
			maxTS = ts
		}
	}
	if !anyOK {
		return 0
	}
	return maxTS
}

// IsGitRepoAt reports whether dir is a git work tree: it holds a `.git`
// directory or a `.git` file (worktree / submodule gitfile).
func IsGitRepoAt(dir string) bool {
	if dir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}

// DiscoverNestedGitRepos finds the git repo roots under root (bounded to
// NestedGitMaxDepth). A directory is a repo root when it contains a `.git`
// directory or file; once a repo is found its tree is not walked further.
// When root is itself a repo, the result is exactly [root]. Sorted.
func DiscoverNestedGitRepos(root string) []string {
	if !isDir(root) {
		return nil
	}
	if IsGitRepoAt(root) {
		return []string{root}
	}
	var repos []string
	type frame struct {
		dir   string
		depth int
	}
	stack := []frame{{root, 0}}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur.depth >= NestedGitMaxDepth {
			continue
		}
		entries, err := os.ReadDir(cur.dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if gitWorkspaceSkipDirs[name] {
				continue
			}
			path := filepath.Join(cur.dir, name)
			if pathExists(filepath.Join(path, ".git")) {
				// Nested repo found — record and do not descend.
				repos = append(repos, path)
				continue
			}
			stack = append(stack, frame{path, cur.depth + 1})
		}
	}
	sort.Strings(repos)
	return repos
}

// LastCommitTimeAt reads the HEAD commit timestamp (Unix seconds) with
// `git log -1 --format=%ct`. argv-only, and bounded by gitTimeout so a hostile
// or unreachable path cannot hang the caller.
func LastCommitTimeAt(dir string) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "log", "-1", "--format=%ct")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return 0, errors.New("git log returned no timestamp")
	}
	ts, perr := strconv.ParseInt(raw, 10, 64)
	if perr != nil {
		return 0, perr
	}
	return ts, nil
}
