// Package obsidian ports the Rust Obsidian integration
// (src/obsidian/{mod,sync,note_generator,watcher}.rs) onto the Go engine.
//
// Vault layout: <project>/.leankg/obsidian/vault (Rust obsidian::vault_path).
// Operations mirror the Rust CLI verbs:
//
//	Init    create the vault directory and its README
//	Push    store -> vault: one markdown note per code element
//	Pull    vault -> store: notes, [[wiki-links]], annotations
//	Watch   debounced fsnotify loop that pulls on vault edits
//	Status  vault census
//
// Two Rust storage details have no Go counterpart and are re-expressed on the
// store's KV layer instead of new tables:
//
//   - Rust business_logic rows (one annotation per element) become
//     KVSet(AnnotationNamespace, element_qualified_name, annotation).
//   - Rust only ever wrote vault content OUT to notes; the Go port adds the
//     reverse direction so graph and vault converge: each note becomes a Note
//     element, each [[wiki-link]] a "links" relationship, and a note whose
//     frontmatter carries leankg_id gets a "documents" edge to that element.
//     Pull is idempotent: elements are keyed by qualified name, relationships
//     by (source, target, rel_type), so re-syncing an unchanged vault adds
//     nothing.
package obsidian

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

const (
	// NotePrefix namespaces vault notes in the element graph, so a note
	// qualified name can never collide with a code element qualified name
	// (and a note is never mistaken for the code element it documents).
	NotePrefix = "note/"
	// NoteType is the element_type of a note synced from the vault.
	NoteType = "Note"
	// LinkRel is the relationship type of a [[wiki-link]] between notes.
	LinkRel = "links"
	// DocumentsRel anchors a note to the element named by its leankg_id
	// frontmatter field.
	DocumentsRel = "documents"
	// AnnotationNamespace is the store KV namespace holding annotations
	// imported from note frontmatter (Go stand-in for Rust business_logic).
	AnnotationNamespace = "obsidian"
	// DefaultDebounce is the Rust CLI default (--debounce-ms 1000).
	DefaultDebounce = time.Second
	// readmeName is the vault README written by Init. It is vault chrome, not
	// a note: Pull skips it (Rust pull found no leankg_id in it, so its only
	// effect would have been a phantom note).
	readmeName = "README.md"
)

// vaultREADME is the vault README, the Rust SyncEngine::init text verbatim.
const vaultREADME = "# LeanKG Obsidian Vault\n" +
	"\n" +
	"This vault is managed by LeanKG. Notes in `.leankg/obsidian/vault/` are auto-generated from LeanKG's knowledge graph.\n" +
	"\n" +
	"## Sync Commands\n" +
	"\n" +
	"- `leankg obsidian push` - Generate notes from LeanKG database\n" +
	"- `leankg obsidian pull` - Import annotation edits back to LeanKG\n" +
	"- `leankg obsidian watch` - Watch for changes and auto-sync\n" +
	"\n" +
	"## Frontmatter Fields\n" +
	"\n" +
	"- `leankg_id` - Unique identifier for the code element\n" +
	"- `leankg_type` - Element type (function, file, class, etc.)\n" +
	"- `leankg_file` - Source file path\n" +
	"- `leankg_line` - Line range in source file\n" +
	"- `leankg_relationships` - List of related elements\n" +
	"- `leankg_annotation` - Editable annotation description\n" +
	"\n" +
	"## Notes\n" +
	"\n" +
	"- LeanKG is the source of truth\n" +
	"- `push` overwrites `leankg_*` frontmatter fields\n" +
	"- `pull` imports only `leankg_annotation` back to LeanKG\n" +
	"- Your custom notes in note bodies are never overwritten\n"

// VaultPath resolves the vault directory: custom when non-empty, otherwise
// <leankgDir>/obsidian/vault (Rust obsidian::vault_path). leankgDir is the
// project's .leankg directory, which is where the Rust engine kept the vault.
func VaultPath(leankgDir, custom string) string {
	if custom != "" {
		return custom
	}
	return filepath.Join(leankgDir, "obsidian", "vault")
}

// Engine binds one vault to the store it syncs with.
type Engine struct {
	vault string
	st    store.Backend
}

// New returns an engine for vaultPath. st may be nil for the filesystem-only
// operations (Init, Status); Push and Pull require a store.
func New(vaultPath string, st store.Backend) *Engine {
	return &Engine{vault: vaultPath, st: st}
}

// Vault returns the vault directory this engine operates on.
func (e *Engine) Vault() string { return e.vault }

// Init creates the vault directory and writes its README (Rust
// SyncEngine::init). The README is rewritten on every call.
func (e *Engine) Init() error {
	if err := os.MkdirAll(e.vault, 0o755); err != nil {
		return fmt.Errorf("obsidian: create vault: %w", err)
	}
	if err := os.WriteFile(filepath.Join(e.vault, readmeName), []byte(vaultREADME), 0o644); err != nil {
		return fmt.Errorf("obsidian: write vault README: %w", err)
	}
	return nil
}

// VaultStatus is the vault census (Rust VaultStatus).
type VaultStatus struct {
	Initialized bool   `json:"initialized"`
	NoteCount   int    `json:"note_count"`
	LastSync    string `json:"last_sync,omitempty"`
}

// Status reports whether the vault exists and how many markdown files it
// holds. Like the Rust status, the count includes the vault README.
func (e *Engine) Status() (VaultStatus, error) {
	if _, err := os.Stat(e.vault); err != nil {
		// Rust: a missing vault path reports initialized=false, count=0.
		return VaultStatus{}, nil
	}
	notes, err := walkMarkdown(e.vault)
	if err != nil {
		return VaultStatus{}, err
	}
	return VaultStatus{Initialized: true, NoteCount: len(notes)}, nil
}

// walkMarkdown returns every .md file under root as a slash-separated path
// relative to root, sorted so sync results are deterministic. Unreadable
// entries are skipped, matching the Rust recursive walkdir (which dropped
// read errors) rather than failing the whole scan.
func walkMarkdown(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || filepath.Ext(p) != ".md" {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("obsidian: scan vault: %w", err)
	}
	sort.Strings(out)
	return out, nil
}

// readNote reads one vault-relative note and normalizes CRLF so the
// frontmatter fence parsing matches the Rust line iterator.
func (e *Engine) readNote(rel string) (string, error) {
	b, err := os.ReadFile(filepath.Join(e.vault, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n"), nil
}

// splitFrontmatter ports the Rust parse_frontmatter and additionally returns
// the note body: the leading --- fence is scanned to the closing fence, every
// line splits on the FIRST ':' with both sides trimmed, and values keep any
// surrounding quotes. Lines before the opening fence are ignored.
func splitFrontmatter(content string) (meta map[string]string, body string) {
	meta = map[string]string{}
	lines := strings.Split(content, "\n")
	in, end := false, 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			if in {
				end = i + 1
				break
			}
			in = true
			continue
		}
		if !in {
			continue
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			meta[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	if end > 0 {
		return meta, strings.Trim(strings.Join(lines[end:], "\n"), "\n")
	}
	return meta, strings.Trim(content, "\n")
}

// unquote strips one symmetric layer of double quotes, the way the Rust
// annotation reader did with trim_matches('"').
func unquote(s string) string { return strings.Trim(s, `"`) }

// unquotedMeta is the frontmatter as stored in element metadata: same keys,
// values with their surrounding quotes removed.
func unquotedMeta(meta map[string]string) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]any, len(meta))
	for k, v := range meta {
		out[k] = unquote(v)
	}
	return out
}
