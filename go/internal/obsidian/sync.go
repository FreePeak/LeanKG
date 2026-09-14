package obsidian

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/langs"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Conflict is one annotation divergence found by Pull: the store holds one
// annotation and the note frontmatter another (Rust ConflictInfo). Conflicts
// are reported, never auto-merged — the stored value wins until a human
// resolves the divergence.
type Conflict struct {
	ElementID        string `json:"element_id"`
	LocalAnnotation  string `json:"local_annotation"`
	RemoteAnnotation string `json:"remote_annotation"`
}

// PullResult reports one vault -> store sync.
type PullResult struct {
	Notes       int        `json:"notes"`
	Links       int        `json:"links"`
	Documents   int        `json:"documents"`
	Annotations int        `json:"annotations"`
	Conflicts   []Conflict `json:"conflicts,omitempty"`
}

// wikiLinkRE matches [[target]], [[target|alias]], [[target#heading]], and the
// inner target of ![[embed]].
var wikiLinkRE = regexp.MustCompile(`\[\[([^\[\]]+)\]\]`)

// Pull syncs the vault into the store: one Note element per markdown note, one
// LinkRel relationship per [[wiki-link]], one DocumentsRel edge per leankg_id
// anchor, and annotation import (the Rust SyncEngine::pull flow) through the KV
// annotation namespace.
//
// Idempotent by construction: elements upsert on qualified name, relationships
// on (source, target, rel_type), so a second Pull over an unchanged vault
// writes the same rows and adds nothing new.
func (e *Engine) Pull() (PullResult, error) {
	if e.st == nil {
		return PullResult{}, errors.New("obsidian: pull requires a store")
	}
	notes, err := walkMarkdown(e.vault)
	if err != nil {
		return PullResult{}, err
	}

	var (
		res  PullResult
		els  []store.Element
		rels []store.Relationship
	)
	seen := map[string]bool{} // source, target, rel_type emitted by this sync

	for _, note := range notes {
		// The vault README is chrome written by Init, not a note: Rust's pull
		// found no leankg_id in it, so its only effect would be a phantom node.
		if note == readmeName {
			continue
		}
		content, err := e.readNote(note)
		if err != nil {
			continue // note vanished between scan and read
		}
		meta, body := splitFrontmatter(content)
		qn := NotePrefix + strings.TrimSuffix(note, ".md")

		els = append(els, store.Element{
			QualifiedName: qn,
			ElementType:   NoteType,
			Name:          strings.TrimSuffix(path.Base(note), ".md"),
			FilePath:      note,
			LineStart:     1,
			LineEnd:       lineCount(content),
			Language:      string(langs.Markdown),
			Content:       body,
			Metadata:      unquotedMeta(meta),
		})

		for _, link := range wikiLinks(content) {
			if addRelationship(&rels, seen, qn, NotePrefix+link, LinkRel) {
				res.Links++
			}
		}
		if id := meta["leankg_id"]; id != "" {
			if addRelationship(&rels, seen, qn, id, DocumentsRel) {
				res.Documents++
			}
		}
		if ann, ok := readAnnotationLine(content); ok && ann != "" && meta["leankg_id"] != "" {
			imported, conflict, err := e.importAnnotation(meta["leankg_id"], ann)
			if err != nil {
				return res, err
			}
			if imported {
				res.Annotations++
			}
			if conflict != nil {
				res.Conflicts = append(res.Conflicts, *conflict)
			}
		}
	}

	if err := e.st.UpsertElements(els); err != nil {
		return res, err
	}
	if err := e.st.UpsertRelationships(rels); err != nil {
		return res, err
	}
	res.Notes = len(els)
	return res, nil
}

// addRelationship appends a relationship unless this sync already emitted the
// same (source, target, rel_type) triple.
func addRelationship(rels *[]store.Relationship, seen map[string]bool, source, target, relType string) bool {
	key := source + "\x1f" + target + "\x1f" + relType
	if seen[key] {
		return false
	}
	seen[key] = true
	*rels = append(*rels, store.Relationship{
		Source:     source,
		Target:     target,
		RelType:    relType,
		Confidence: 1.0,
	})
	return true
}

// importAnnotation ports the Rust pull annotation flow: a note annotation is
// imported only when the element has no annotation yet; a differing stored
// annotation is reported as a conflict and left untouched.
//
// Empty annotations are skipped. Rust created an empty business_logic row for
// every generated note (the template writes leankg_annotation: ""), which
// records nothing; skipping loses no information and keeps the store clean.
func (e *Engine) importAnnotation(elementID, annotation string) (bool, *Conflict, error) {
	existing, found, err := e.st.KVGet(AnnotationNamespace, elementID)
	if err != nil {
		return false, nil, fmt.Errorf("obsidian: read annotation for %s: %w", elementID, err)
	}
	switch {
	case !found:
		if err := e.st.KVSet(AnnotationNamespace, elementID, annotation); err != nil {
			return false, nil, fmt.Errorf("obsidian: import annotation for %s: %w", elementID, err)
		}
		return true, nil, nil
	case existing != annotation:
		return false, &Conflict{
			ElementID:        elementID,
			LocalAnnotation:  existing,
			RemoteAnnotation: annotation,
		}, nil
	}
	return false, nil, nil
}

// wikiLinks extracts [[target]] targets from note text, stripping Obsidian
// aliases (|label) and heading or block anchors (#heading). Targets keep
// document order with duplicates removed.
//
// ponytail: links inside fenced code blocks are picked up too; a
// fenced-region pre-pass is the upgrade path if that ever matters.
func wikiLinks(content string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range wikiLinkRE.FindAllStringSubmatch(content, -1) {
		target := m[1]
		if i := strings.IndexByte(target, '|'); i >= 0 {
			target = target[:i]
		}
		if i := strings.IndexByte(target, '#'); i >= 0 {
			target = target[:i]
		}
		target = strings.TrimSpace(target)
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	return out
}

// readAnnotationLine ports the Rust read_existing_annotation: the first line
// starting with "leankg_annotation:" wins, with surrounding quotes stripped.
// The bool distinguishes "no annotation line" from "empty annotation".
func readAnnotationLine(content string) (string, bool) {
	for _, line := range strings.Split(content, "\n") {
		if rest, ok := strings.CutPrefix(line, "leankg_annotation:"); ok {
			return unquote(strings.TrimSpace(rest)), true
		}
	}
	return "", false
}

// lineCount is the 1-based line count of a note (element line range).
func lineCount(content string) int {
	if content == "" {
		return 0
	}
	n := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		n++
	}
	return n
}
