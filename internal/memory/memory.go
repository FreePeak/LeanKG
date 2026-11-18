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
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/FreePeak/LeanKG/internal/telemetry"
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

	// bankMu serializes bank JSONL writes inside this process: appends and
	// the read-filter-rename rewrites document replace/delete perform must
	// not interleave, or a rewrite drops a row appended mid-scan.
	bankMu sync.Mutex

	// vec is the optional dense recall arm (dense.go); vecMu guards it.
	vecMu sync.Mutex
	vec   *denseArm
	// keepModelIDs names the vector collections that survive the stale
	// sweep (dense.go). Empty means "keep every collection".
	keepModelIDs []string
}

// Open opens (creating if needed) the memory tree for projectDir, or the
// global home tree when global is true. It creates the directories, empty
// MEMORY.md/USER.md, and topics/ if missing.
func Open(ctx context.Context, projectDir string, global bool) (*Memory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	if m.fts, err = openFTS(ctx, m.ftsPath); err != nil {
		return nil, err
	}
	if err := m.ensureIndex(ctx); err != nil {
		m.fts.Close()
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
func (m *Memory) Snapshot(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	mem, err := os.ReadFile(filepath.Join(m.real, "MEMORY.md"))
	if err != nil {
		return "", err
	}
	usr, err := os.ReadFile(filepath.Join(m.real, "USER.md"))
	if err != nil {
		return "", err
	}
	return snapshot(Redact(string(mem)), Redact(string(usr))), nil
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
func (m *Memory) View(ctx context.Context, path string, offsetLine int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	full, _, err := m.resolve(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	text := Redact(string(data))
	if offsetLine <= 1 {
		return strings.TrimSuffix(text, "\n"), nil
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if offsetLine > len(lines) {
		return "", nil
	}
	return strings.Join(lines[offsetLine-1:], "\n"), nil
}

// Create writes path with content, replacing any existing file.
// Exists reports whether path names an existing memory file (false for an
// invalid or escaping path).
func (m *Memory) Exists(path string) bool {
	full, _, err := m.resolve(path)
	if err != nil {
		return false
	}
	_, err = os.Stat(full)
	return err == nil
}

func (m *Memory) Create(ctx context.Context, path, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	full, key, err := m.resolve(path)
	if err != nil {
		return err
	}
	if err := bound(key, content); err != nil {
		return err
	}
	if err := writeMemoryFile(full, content); err != nil {
		return err
	}
	return m.reindex(ctx, key)
}

// Append adds content to an existing memory file, creating it when absent,
// and reindexes the key so the new lines become searchable rows. A raw file
// append would skip the index, which is how a written-but-unfindable file
// happens (found with the reflect mirroring, session/reflect.go MirrorToMemory).
func (m *Memory) Append(ctx context.Context, path, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	full, key, err := m.resolve(path)
	if err != nil {
		return err
	}
	// The bound is on the file as it will be after the append, not on the
	// fragment alone. Only a core file is bounded, so only it is read back.
	var prior []byte
	if isCoreFile(key) {
		prior, _ = os.ReadFile(full) // an absent file reads as empty
	}
	if err := bound(key, string(prior)+content); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(full, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	// Bound() gates the bound; the redaction gate is the stream below, so an
	// append cannot smuggle a pasted key in as text either.
	if _, err := f.WriteString(Redact(content)); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return m.reindex(ctx, key)
}

// StrReplace replaces the single occurrence of old with new. Zero or
// multiple occurrences fail with ErrAmbiguousMatch.
func (m *Memory) StrReplace(ctx context.Context, path, old, new string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
	if err := writeMemoryFile(full, out); err != nil {
		return err
	}
	return m.reindex(ctx, key)
}

// Insert inserts content so that it starts at line insertLine (1-based);
// insertLine 0 (or less) prepends.
func (m *Memory) Insert(ctx context.Context, path, content string, insertLine int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
	if err := writeMemoryFile(full, out); err != nil {
		return err
	}
	return m.reindex(ctx, key)
}

// Delete removes the file and its index rows.
func (m *Memory) Delete(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, key, err := m.resolve(path)
	if err != nil {
		return err
	}
	full := filepath.Join(m.real, filepath.FromSlash(key))
	if err := os.Remove(full); err != nil {
		return err
	}
	return m.deleteRows(ctx, key)
}

// Rename moves a memory file, updating the index rows to the new path.
func (m *Memory) Rename(ctx context.Context, oldPath, newPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
	if err := m.deleteRows(ctx, oldKey); err != nil {
		return err
	}
	return m.reindex(ctx, newKey)
}

func replaceOnce(content, old, new string) (string, error) {
	n := strings.Count(content, old)
	if n != 1 {
		return "", fmt.Errorf("%w: %d occurrences", ErrAmbiguousMatch, n)
	}
	return strings.Replace(content, old, new, 1), nil
}

// bound enforces the core-file byte limit (error-not-truncate). It measures
// the redacted text, the bytes writeMemoryFile puts on disk: redaction can
// lengthen a value, so the raw length is not the file's length.
func bound(key, content string) error {
	if isCoreFile(key) {
		if used := len(Redact(content)); used > CoreFileBytes {
			return ErrOverflow{File: key, Used: used, Limit: CoreFileBytes}
		}
	}
	return nil
}

// isCoreFile reports whether key is a bounded core file (MEMORY.md, USER.md).
func isCoreFile(key string) bool { return key == "MEMORY.md" || key == "USER.md" }

// writeMemoryFile is the ONE place memory bytes reach the filesystem: every
// write verb calls it, so redaction (FR-SMA-06) cannot be skipped by a new
// verb that forgot to call Redact itself. A read path never goes through it,
// so a read never rewrites a file.
func writeMemoryFile(full, content string) error {
	return os.WriteFile(full, []byte(Redact(content)), 0o644)
}

// Redact masks secrets in MEMORY content before it reaches disk (FR-SMA-06).
//
// Every write verb already funnels through bound(), which is why this lives
// here rather than in six call sites: the same lesson an operator writes
// into a memory file is the one a telemetry row masks. Measured live before
// this existed — `import action=memory command=create path=topics/probe.md
// content=<fake keys>` wrote the file and grep found both keys INTACT.
//
// The caller's words are preserved (only the matched value is replaced), so
// a redacted lesson still explains what it was about.
func Redact(content string) string { return telemetry.Redact(content) }

// redactEntry returns e with its content and every string inside its metadata
// masked by Redact. The bank write path applies it once per row, so the JSONL
// file and the FTS index are built from the same redacted text.
func redactEntry(e Entry) Entry {
	e.Content = Redact(e.Content)
	e.Metadata = redactMetadata(e.Metadata)
	return e
}

// redactEntries applies redactEntry to each row before a read returns it, so a
// row written before write-side redaction existed is masked on read too.
// Redaction is idempotent, so a row already redacted is returned unchanged.
func redactEntries(rows []Entry) []Entry {
	for i := range rows {
		rows[i] = redactEntry(rows[i])
	}
	return rows
}

// redactMetadata copies m with every string value redacted. Tags arrive as a
// []string and other values may nest, so the walk covers those shapes too.
func redactMetadata(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = redactValue(v)
	}
	return out
}

func redactValue(v any) any {
	switch x := v.(type) {
	case string:
		return Redact(x)
	case []string:
		out := make([]string, len(x))
		for i, s := range x {
			out[i] = Redact(s)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = redactValue(item)
		}
		return out
	case map[string]any:
		return redactMetadata(x)
	}
	return v
}
