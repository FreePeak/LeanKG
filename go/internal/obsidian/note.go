package obsidian

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// noteTimestampLayout is the stamp shape the Rust generator emitted
// ({:04}-{:02}-{:02}T{:02}:{:02}:{:02}Z). The Rust chrono_like_format
// hand-rolled a 365-day/29-day calendar and produced wrong dates; the Go port
// reads the real UTC clock in the same shape.
const noteTimestampLayout = "2006-01-02T15:04:05Z"

// GeneratedNote is one rendered note (Rust GeneratedNote).
type GeneratedNote struct {
	Path      string `json:"path"`       // vault-relative
	ElementID string `json:"element_id"` // element qualified name
}

// NoteMetadata is the note frontmatter the generator renders (Rust
// NoteMetadata).
type NoteMetadata struct {
	LeanKGID         string   `json:"leankg_id"`
	LeanKGType       string   `json:"leankg_type"`
	LeanKGFile       string   `json:"leankg_file"`
	LeanKGLine       string   `json:"leankg_line"`
	LeanKGAnnotation string   `json:"leankg_annotation"`
	Relationships    []string `json:"leankg_relationships"`
	WikiLinks        []string `json:"leankg_relationships_wikilinks"`
	Created          string   `json:"created"`
	Updated          string   `json:"updated"`
}

// NotePath maps an element to its vault-relative note path (Rust
// NoteGenerator::element_to_note_path): "::" becomes a directory separator,
// the other unsafe characters become underscores, and folder elements get a
// .folder suffix so a note file can never shadow a directory.
func NotePath(el store.Element) string {
	safe := strings.NewReplacer("::", "/", ":", "_", " ", "_", "(", "_", ")", "_").
		Replace(el.QualifiedName)
	if el.ElementType == "Folder" {
		return safe + ".folder.md"
	}
	return safe + ".md"
}

// skipExportPath ports the Rust push filter: build artifacts, vendored trees,
// test files and generated files get no note.
func skipExportPath(p string) bool {
	if strings.Contains(p, ".test.") || strings.HasSuffix(p, ".generated.rs") {
		return true
	}
	for _, frag := range []string{"/target/", "/.next/", "/node_modules/", "/dist/"} {
		if strings.Contains(p, frag) {
			return true
		}
	}
	return false
}

// GenerateNote writes the note for one element under the vault (Rust
// NoteGenerator::generate_note): frontmatter, element summary, annotation
// block, relationship list, and filtered [[wiki-links]]. The note is
// rewritten whole on every call, exactly like the Rust push.
func (e *Engine) GenerateNote(el store.Element, rels []store.Relationship, annotation string) (GeneratedNote, error) {
	relPath := NotePath(el)
	full := filepath.Join(e.vault, filepath.FromSlash(relPath))

	// Trust boundary: paths come from indexed content (qualified names). Refuse
	// anything that would escape the vault.
	rel, err := filepath.Rel(e.vault, full)
	if err != nil {
		return GeneratedNote{}, fmt.Errorf("obsidian: note path for %s: %w", el.QualifiedName, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return GeneratedNote{}, fmt.Errorf("obsidian: note path for %s escapes the vault", el.QualifiedName)
	}

	meta := BuildMetadata(el, rels, annotation, time.Now().UTC())
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return GeneratedNote{}, fmt.Errorf("obsidian: note dir for %s: %w", el.QualifiedName, err)
	}
	if err := os.WriteFile(full, []byte(RenderNote(el, meta)), 0o644); err != nil {
		return GeneratedNote{}, fmt.Errorf("obsidian: write note for %s: %w", el.QualifiedName, err)
	}
	return GeneratedNote{Path: relPath, ElementID: el.QualifiedName}, nil
}

// BuildMetadata ports the Rust build_metadata: relationship strings, the
// filtered [[wiki-link]] list (plus the file -> parent-folder "contained_by"
// edge for file elements), and now() stamps.
func BuildMetadata(el store.Element, rels []store.Relationship, annotation string, now time.Time) NoteMetadata {
	relStrings := make([]string, 0, len(rels))
	wikiLinks := make([]string, 0, len(rels))
	for _, r := range rels {
		relStrings = append(relStrings, fmt.Sprintf("%s (%s)", r.Target, r.RelType))
		if link, ok := wikiLink(r); ok {
			wikiLinks = append(wikiLinks, fmt.Sprintf("- %s (%s)", link, r.RelType))
		}
	}

	// File elements link their parent folder so the vault graph gains
	// folder -> file containment edges (Rust parity).
	if el.ElementType == "File" {
		if parent := filepath.ToSlash(filepath.Dir(el.FilePath)); parent != "" && parent != "." {
			wikiLinks = append(wikiLinks, fmt.Sprintf("- [[%s.folder]] (contained_by)", stripDotSlash(parent)))
		}
	}

	stamp := now.Format(noteTimestampLayout)
	return NoteMetadata{
		LeanKGID:         el.QualifiedName,
		LeanKGType:       el.ElementType,
		LeanKGFile:       el.FilePath,
		LeanKGLine:       fmt.Sprintf("%d-%d", el.LineStart, el.LineEnd),
		LeanKGAnnotation: annotation,
		Relationships:    relStrings,
		WikiLinks:        wikiLinks,
		Created:          stamp,
		Updated:          stamp,
	}
}

// wikiLink renders one relationship as a [[wiki-link]] target, mirroring the
// Rust filter chain: unresolved targets, std/core targets, and targets that do
// not look like vault paths are dropped; "./" prefixes are stripped; targets
// without a file extension become .folder links.
func wikiLink(r store.Relationship) (string, bool) {
	target := r.Target
	if strings.HasPrefix(target, "__unresolved__") ||
		strings.HasPrefix(target, "std::") ||
		strings.HasPrefix(target, "core::") {
		return "", false
	}

	if strings.Contains(target, "::") {
		parts := strings.SplitN(target, "::", 3)
		if len(parts) < 2 {
			return "", false
		}
		// Rust path-shape filter: the file segment must contain a '/' after
		// "./" stripping, else the target is not vault-shaped.
		if !strings.Contains(stripDotSlash(parts[0]), "/") {
			return "", false
		}
		// ./src/main.rs::main -> [[src/main.rs/main]]
		return fmt.Sprintf("[[%s/%s]]", stripDotSlash(parts[0]), parts[1]), true
	}

	if !strings.Contains(target, "/") {
		return "", false
	}
	cleaned := stripDotSlash(target)
	// Likely a folder: no extension, or a trailing slash.
	if !strings.Contains(cleaned, ".") || strings.HasSuffix(cleaned, "/") {
		return fmt.Sprintf("[[%s.folder]]", strings.TrimSuffix(cleaned, "/")), true
	}
	return fmt.Sprintf("[[%s]]", cleaned), true
}

// stripDotSlash removes every "./" occurrence (Rust .replace("./", "")).
func stripDotSlash(s string) string { return strings.ReplaceAll(s, "./", "") }

// RenderNote ports the Rust build_note_content template byte for byte:
// frontmatter, title, type/file/lines block, optional annotation blockquote,
// relationship list, and the wiki-link list.
func RenderNote(el store.Element, meta NoteMetadata) string {
	relsText := "  (none)"
	if len(meta.Relationships) > 0 {
		relsText = strings.Join(prefixEach(meta.Relationships, "  - "), "\n")
	}

	annotationBlock := ""
	if meta.LeanKGAnnotation != "" {
		annotationBlock = fmt.Sprintf("\n> **Annotation**: %s\n", meta.LeanKGAnnotation)
	}

	wikiText := ""
	if len(meta.WikiLinks) > 0 {
		wikiText = strings.Join(meta.WikiLinks, "\n")
	}

	return fmt.Sprintf(`---
leankg_id: %s
leankg_type: %s
leankg_file: %s
leankg_line: %s
leankg_relationships:
%s
leankg_annotation: "%s"
created: %s
updated: %s
---

# %s

**Type**: %s
**File**: `+"`%s`"+`
**Lines**: %d-%d
%s**Relationships**:
%s

%s
`,
		meta.LeanKGID,
		meta.LeanKGType,
		meta.LeanKGFile,
		meta.LeanKGLine,
		relsText,
		strings.ReplaceAll(meta.LeanKGAnnotation, `"`, `'`),
		meta.Created,
		meta.Updated,
		el.Name,
		el.ElementType,
		el.FilePath,
		el.LineStart,
		el.LineEnd,
		annotationBlock,
		relsText,
		wikiText,
	)
}

// Push renders a note for every element in the store (Rust SyncEngine::push).
// Export-filtered paths and Note elements (the vault's own mirror image) get
// no note; per-element failures are counted, not fatal, exactly like the Rust
// push that logged and continued.
func (e *Engine) Push() (PushResult, error) {
	if e.st == nil {
		return PushResult{}, errors.New("obsidian: push requires a store")
	}
	elements, err := e.st.Elements()
	if err != nil {
		return PushResult{}, fmt.Errorf("obsidian: list elements: %w", err)
	}

	res := PushResult{}
	for _, el := range elements {
		// Note elements are the vault's own mirror (created by Pull); pushing
		// them back would duplicate every note under note/ paths.
		if el.ElementType == NoteType || skipExportPath(el.FilePath) {
			continue
		}
		rels, err := e.st.Outgoing(el.QualifiedName)
		if err != nil {
			return res, fmt.Errorf("obsidian: relationships of %s: %w", el.QualifiedName, err)
		}
		annotation, _, err := e.st.KVGet(AnnotationNamespace, el.QualifiedName)
		if err != nil {
			return res, fmt.Errorf("obsidian: annotation of %s: %w", el.QualifiedName, err)
		}
		if _, err := e.GenerateNote(el, rels, annotation); err != nil {
			res.Failed++
			continue
		}
		res.Notes++
	}
	return res, nil
}

// PushResult reports one store -> vault export (Rust SyncResult.pushed plus a
// failure count the Rust code only printed).
type PushResult struct {
	Notes  int `json:"notes"`
	Failed int `json:"failed"`
}

func prefixEach(list []string, prefix string) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = prefix + s
	}
	return out
}
