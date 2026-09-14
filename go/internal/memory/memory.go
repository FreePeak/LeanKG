// Package memory implements the full-markdown memory layer (issue #369):
// a project-anchored tree of plain Markdown files (MEMORY.md, USER.md,
// topics/*.md) with Hermes-style bounded core files, unique-substring
// replacement, an FTS5 side index for search, and a mnemopi-compatible
// JSONL bank adapter (banks.go).
//
// Layout under root (<project>/.leankg/memory, or ~/.leankg/memory when
// global):
//
//	MEMORY.md, USER.md   core files, bounded at CoreFileBytes
//	topics/              unbounded topic notes
//	banks/<bank>.jsonl   mnemopi-compat transcript banks
//	index.db             FTS5 side index (own sqlite file, not the store)
package memory

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CoreFileBytes is the Hermes bound for MEMORY.md and USER.md. Writes that
// would push a core file past this size fail with ErrOverflow rather than
// truncating.
const CoreFileBytes = 2200

// Errors returned by the memory layer.
var (
	// ErrOutsideRoot means the path is absolute, contains a ".." component,
	// or escapes root through a symlink.
	ErrOutsideRoot = errors.New("memory: path outside memory root")
	// ErrInvalidPath means the path is not one of the valid targets
	// (MEMORY.md, USER.md, topics/<name>.md).
	ErrInvalidPath = errors.New("memory: invalid memory path")
	// ErrAmbiguousMatch means a replacement substring did not occur exactly
	// once in the target file (Hermes semantics: zero or multiple matches
	// are both refused).
	ErrAmbiguousMatch = errors.New("memory: replacement is not unique")
)

// ErrOverflow reports a write that would push a bounded core file past its
// byte limit. The write is refused, never truncated.
type ErrOverflow struct {
	File  string
	Used  int
	Limit int
}

func (e ErrOverflow) Error() string {
	return fmt.Sprintf("memory: %s would be %d bytes, over the %d-byte limit", e.File, e.Used, e.Limit)
}

// Memory is a handle to one project's memory tree.
type Memory struct {
	root    string // logical root
	real    string // symlink-resolved root
	fts     *sql.DB
	ftsPath string
}

// Open opens (creating if needed) the memory tree for projectDir, or the
// global home tree when global is true. It creates the directories, empty
// MEMORY.md/USER.md, and topics/ if missing.
func Open(projectDir string, global bool) (*Memory, error) {
	var root string
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("memory: resolve home: %w", err)
		}
		root = filepath.Join(home, ".leankg", "memory")
	} else {
		if projectDir == "" {
			return nil, fmt.Errorf("memory: empty project dir")
		}
		root = filepath.Join(projectDir, ".leankg", "memory")
	}
	if err := os.MkdirAll(filepath.Join(root, "topics"), 0o755); err != nil {
		return nil, fmt.Errorf("memory: create root: %w", err)
	}
	for _, core := range []string{"MEMORY.md", "USER.md"} {
		p := filepath.Join(root, core)
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(p, nil, 0o644); err != nil {
				return nil, fmt.Errorf("memory: create %s: %w", core, err)
			}
		}
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("memory: resolve root: %w", err)
	}
	m := &Memory{root: root, real: real, ftsPath: filepath.Join(root, "index.db")}
	if m.fts, err = openFTS(m.ftsPath); err != nil {
		return nil, err
	}
	return m, nil
}

// Root returns the memory root directory.
func (m *Memory) Root() string { return m.root }

// Close closes the FTS side index. The Markdown files are plain files and
// need no closing.
func (m *Memory) Close() error { return m.fts.Close() }

// resolve validates rel against the path rules and returns the absolute file
// path to operate on (symlink-resolved when the file itself is a symlink)
// plus the logical relative path used as the FTS key.
func (m *Memory) resolve(rel string) (full string, key string, err error) {
	if rel == "" {
		return "", "", fmt.Errorf("%w: empty path", ErrInvalidPath)
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(filepath.ToSlash(rel), "/") {
		return "", "", fmt.Errorf("%w: absolute path %q", ErrOutsideRoot, rel)
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == ".." {
			return "", "", fmt.Errorf("%w: %q climbs out of root", ErrOutsideRoot, rel)
		}
	}
	clean := filepath.Clean(rel)
	name := strings.TrimPrefix(clean, "topics/")
	switch {
	case clean == "MEMORY.md" || clean == "USER.md":
	case strings.HasPrefix(clean, "topics/") && strings.HasSuffix(name, ".md") &&
		len(name) > 3 && !strings.Contains(name, "/"):
	default:
		return "", "", fmt.Errorf("%w: %q (valid: MEMORY.md, USER.md, topics/<name>.md)", ErrInvalidPath, rel)
	}

	key = clean
	full = filepath.Join(m.real, clean)
	// Symlink escape check. The parent directory (root or topics/) always
	// exists, so EvalSymlinks on it is safe; the file itself may not exist
	// yet (Create) or may be a symlink.
	dir, err := filepath.EvalSymlinks(filepath.Dir(full))
	if err != nil || !m.within(dir) {
		return "", "", fmt.Errorf("%w: %q resolves outside root", ErrOutsideRoot, rel)
	}
	full = filepath.Join(dir, filepath.Base(full))
	if fi, lerr := os.Lstat(full); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		resolved, serr := filepath.EvalSymlinks(full)
		if serr != nil || !m.within(resolved) {
			// Dangling symlink or escape: refuse rather than create through it.
			return "", "", fmt.Errorf("%w: %q is a symlink outside root", ErrOutsideRoot, rel)
		}
		full = resolved
	}
	return full, key, nil
}

func (m *Memory) within(p string) bool {
	return p == m.real || strings.HasPrefix(p, m.real+string(os.PathSeparator))
}

// Snapshot returns the session-start frozen core: a usage header line
// followed by the full MEMORY.md and USER.md contents under section
// headings. An empty core file contributes just its heading.
func (m *Memory) Snapshot() (string, error) {
	mem, err := os.ReadFile(filepath.Join(m.real, "MEMORY.md"))
	if err != nil {
		return "", err
	}
	usr, err := os.ReadFile(filepath.Join(m.real, "USER.md"))
	if err != nil {
		return "", err
	}
	return snapshot(string(mem), string(usr)), nil
}

func snapshot(mem, usr string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- leankg-memory usage: MEMORY.md %d%% (%d/%d) USER.md %d%% (%d/%d) -->\n",
		len(mem)*100/CoreFileBytes, len(mem), CoreFileBytes,
		len(usr)*100/CoreFileBytes, len(usr), CoreFileBytes)
	b.WriteString("# MEMORY.md\n")
	b.WriteString(mem)
	if mem != "" && !strings.HasSuffix(mem, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("# USER.md\n")
	b.WriteString(usr)
	if usr != "" && !strings.HasSuffix(usr, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// View returns the file starting at line offsetLine (1-based; 0 or less
// returns the whole file). The trailing newline of the last line is not
// included.
func (m *Memory) View(path string, offsetLine int) (string, error) {
	full, _, err := m.resolve(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	if offsetLine <= 1 {
		return strings.TrimSuffix(string(data), "\n"), nil
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if offsetLine > len(lines) {
		return "", nil
	}
	return strings.Join(lines[offsetLine-1:], "\n"), nil
}

// Create writes path with content, replacing any existing file.
func (m *Memory) Create(path, content string) error {
	full, key, err := m.resolve(path)
	if err != nil {
		return err
	}
	if err := bound(key, content); err != nil {
		return err
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return err
	}
	return m.reindex(key)
}

// StrReplace replaces the single occurrence of old with new. Zero or
// multiple occurrences fail with ErrAmbiguousMatch.
func (m *Memory) StrReplace(path, old, new string) error {
	full, key, err := m.resolve(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return err
	}
	out, err := replaceOnce(string(data), old, new)
	if err != nil {
		return err
	}
	if err := bound(key, out); err != nil {
		return err
	}
	if err := os.WriteFile(full, []byte(out), 0o644); err != nil {
		return err
	}
	return m.reindex(key)
}

// Insert inserts content so that it starts at line insertLine (1-based);
// insertLine 0 (or less) prepends.
func (m *Memory) Insert(path, content string, insertLine int) error {
	full, key, err := m.resolve(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return err
	}
	existing := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(existing) == 1 && existing[0] == "" {
		existing = nil // empty file
	}
	inserted := strings.Split(content, "\n")
	at := insertLine - 1
	if at < 0 {
		at = 0
	}
	if at > len(existing) {
		at = len(existing)
	}
	lines := append(existing[:at], append(inserted, existing[at:]...)...)
	out := strings.Join(lines, "\n") + "\n"
	if err := bound(key, out); err != nil {
		return err
	}
	if err := os.WriteFile(full, []byte(out), 0o644); err != nil {
		return err
	}
	return m.reindex(key)
}

// Delete removes the file and its index rows.
func (m *Memory) Delete(path string) error {
	_, key, err := m.resolve(path)
	if err != nil {
		return err
	}
	full := filepath.Join(m.real, filepath.FromSlash(key))
	if err := os.Remove(full); err != nil {
		return err
	}
	return m.deleteRows(key)
}

// Rename moves a memory file, updating the index rows to the new path.
func (m *Memory) Rename(oldPath, newPath string) error {
	_, oldKey, err := m.resolve(oldPath)
	if err != nil {
		return err
	}
	newFull, newKey, err := m.resolve(newPath)
	if err != nil {
		return err
	}
	if oldKey == newKey {
		return nil
	}
	if err := os.Rename(filepath.Join(m.real, filepath.FromSlash(oldKey)), newFull); err != nil {
		return err
	}
	if err := m.deleteRows(oldKey); err != nil {
		return err
	}
	return m.reindex(newKey)
}

func replaceOnce(content, old, new string) (string, error) {
	n := strings.Count(content, old)
	if n != 1 {
		return "", fmt.Errorf("%w: %d occurrences", ErrAmbiguousMatch, n)
	}
	return strings.Replace(content, old, new, 1), nil
}

// bound enforces the core-file byte limit (error-not-truncate).
func bound(key, content string) error {
	if key == "MEMORY.md" || key == "USER.md" {
		if len(content) > CoreFileBytes {
			return ErrOverflow{File: key, Used: len(content), Limit: CoreFileBytes}
		}
	}
	return nil
}
