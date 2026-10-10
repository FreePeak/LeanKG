package sessionlink

import "strings"

// NormalizeTool maps a client's tool name to a canonical name shared by every
// adapter. LeanKG calls become "leankg.<tool>" (import|query|status) whatever
// the client prefix: mcp__leankg__query (Claude Code), leankg_query
// (opencode), leankg.query / query on a leankg server (pi family). Common
// built-ins collapse to grep, glob, read, edit, write, bash, search.
func NormalizeTool(name string) (norm string, isLeanKG bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, sep := range []string{"mcp__leankg__", "leankg__", "leankg_", "leankg.", "leankg/", "leankg:"} {
		if strings.HasPrefix(n, sep) {
			return "leankg." + strings.TrimPrefix(n, sep), true
		}
	}
	if strings.Contains(n, "leankg") {
		if i := strings.LastIndexAny(n, "_./:"); i >= 0 && i+1 < len(n) {
			return "leankg." + n[i+1:], true
		}
	}
	switch n {
	case "grep", "rg", "ripgrep", "search_files", "grep_search", "codebase_search", "find":
		return "grep", false
	case "glob", "list", "ls", "list_files", "list_dir", "file_search":
		return "glob", false
	case "read", "read_file", "view", "cat", "open_file":
		return "read", false
	case "edit", "multiedit", "str_replace", "apply_patch", "patch", "replace", "str_replace_editor", "edit_file":
		return "edit", false
	case "write", "write_file", "create_file":
		return "write", false
	case "bash", "shell", "exec", "run", "run_command", "terminal":
		return "bash", false
	}
	return n, false
}

// IsDiscovery reports whether a normalized tool is a code-discovery action
// (the fallback signal of DS-17).
func IsDiscovery(norm string) bool {
	switch norm {
	case "grep", "glob", "read", "search":
		return true
	}
	return false
}
