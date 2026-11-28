package ccmemory

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/sessionlink/claudecode"
)

// excludeFileEnv overrides the path of the exclude list.
const excludeFileEnv = "LEANKG_MEMORY_EXCLUDE_FILE"

// maxSlugLen is the longest slug a prefix keeps. Claude Code truncates a project
// directory name to 200 characters and appends a hash. The first 200 characters
// of the slug are a prefix of that name, so matching them still finds the
// project; the hash is not needed.
const maxSlugLen = 200

// excludeList holds the slugs of the projects sync must never read, keep or
// write. A project is excluded when its slug equals one of them or starts with
// one of them, so over-matching is the safe direction.
type excludeList struct {
	prefixes []string
}

// loadExcludeList reads the exclude list from LEANKG_MEMORY_EXCLUDE_FILE, else
// from ~/.config/leankg/memory-exclude. The file holds one project path per
// line: whitespace is stripped, blank lines and # lines are skipped, and ~ is
// expanded. A missing file excludes nothing. Any other read error is returned,
// and the caller must stop: a sync that cannot read the list must not run. A
// line that is not an absolute path, or names the root or the home directory,
// fails the whole list. The error names the file and the line number only.
func loadExcludeList() (excludeList, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return excludeList{}, fmt.Errorf("exclude list: resolve home: %w", err)
	}
	path := os.Getenv(excludeFileEnv)
	if path == "" {
		path = filepath.Join(home, ".config", "leankg", "memory-exclude")
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return excludeList{}, nil
	}
	if err != nil {
		reason := err
		var pe *fs.PathError
		if errors.As(err, &pe) {
			reason = pe.Err // the path error carries the full path; keep the OS reason
		}
		return excludeList{}, fmt.Errorf("%s: %w", filepath.Base(path), reason)
	}
	return parseExcludeList(string(data), home, filepath.Base(path))
}

// parseExcludeList turns the file content into prefixes. Each path gives its
// slugs (see slugForms) in both the raw and the redacted form: stored rows keep
// the slug masked by memory.Redact, as the scope check of a scoped sync does.
// Each form is capped at maxSlugLen after it is computed. A bad line fails the
// whole list; the error names the file and the line number only.
func parseExcludeList(content, home, name string) (excludeList, error) {
	var ex excludeList
	for i, line := range strings.Split(content, "\n") {
		p := strings.TrimSpace(line)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		path, err := excludePath(p, home)
		if err != nil {
			return excludeList{}, fmt.Errorf("%s: line %d: %w", name, i+1, err)
		}
		for _, slug := range slugForms(path) {
			ex.prefixes = append(ex.prefixes, capSlug(slug), capSlug(memory.Redact(slug)))
		}
	}
	return ex, nil
}

// capSlug keeps the first maxSlugLen characters of slug.
func capSlug(slug string) string {
	if len(slug) > maxSlugLen {
		return slug[:maxSlugLen]
	}
	return slug
}

// slugForms returns the slug of path as written and, when it differs, the slug
// of path with its longest existing ancestor symlink-resolved. Claude Code names
// a project by its resolved working directory (on macOS /tmp is /private/tmp),
// so the resolved slug is the directory that holds the memory. A deleted project
// under a symlinked prefix has no directory under its as-written name, so the
// remainder below the existing ancestor is appended to the resolved ancestor.
func slugForms(path string) []string {
	forms := []string{claudecode.Slug(path)}
	if resolved := resolveExisting(path); resolved != path {
		forms = append(forms, claudecode.Slug(resolved))
	}
	return forms
}

// resolveExisting symlink-resolves the longest existing ancestor of path and
// appends the part below it. A path with no resolvable ancestor is returned
// unchanged.
func resolveExisting(path string) string {
	rest := ""
	for dir := path; ; {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return path
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

// excludePath expands ~ in p and returns it cleaned. A relative path matches
// no project, and the root or the home directory matches every project under
// it, so both are refused.
func excludePath(p, home string) (string, error) {
	path := filepath.Clean(expandHome(p, home))
	if !filepath.IsAbs(path) {
		return "", errors.New("not an absolute path")
	}
	if path == "/" || path == filepath.Clean(home) {
		return "", errors.New("names the filesystem root or the home directory")
	}
	return path, nil
}

// expandHome replaces a leading ~ with home.
func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return p
}

// excludes reports whether slug is an excluded project or lies under one.
func (ex excludeList) excludes(slug string) bool {
	for _, p := range ex.prefixes {
		if strings.HasPrefix(slug, p) {
			return true
		}
	}
	return false
}
