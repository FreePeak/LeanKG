// Package docindex implements the markdown documentation indexer of the Go
// engine: it walks .md files, splits them at ATX headings into hierarchical
// doc elements and feeds the same index layer (store.Backend) as
// internal/index, reusing its 3-signal change detection.
package docindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Result summarizes one IndexDocs run.
type Result struct {
	Files    int
	Elements int
	Skipped  int
}

// skipDirs are skipped path components during the walk; any dot-prefixed
// directory is skipped as well.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, ".leankg": true,
	"target": true, "dist": true, "build": true,
}

// maxContent bounds a section's stored Content in characters.
const maxContent = 8000

// IndexDocs walks dir for .md files, re-extracts changed/new ones, drops
// deleted ones and returns per-run counters. Change detection mirrors
// internal/index exactly: files whose size+mtime match the stored record are
// skipped without reads; otherwise SHA-256 is confirmed against ContentHash
// and identical content is skipped without any writes (the stored record
// stays as-is per contract, so the next run re-hashes such a file).
//
// ponytail: extraction is regex-free line scanning, not a markdown parser —
// ATX headings inside fenced code blocks are handled, indented/setext
// headings and inline HTML are not (upgrade path: goldmark in a later wave).
func IndexDocs(ctx context.Context, st store.Backend, dir string) (Result, error) {
	var res Result

	prev, err := st.Files()
	if err != nil {
		return res, err
	}
	// Only doc-index records participate: Files() also carries records
	// written by internal/index for code files, which docindex must not
	// touch (they are cleaned up by IndexDir, not here).
	prevByRel := make(map[string]store.FileRecord)
	for _, f := range prev {
		if strings.EqualFold(filepath.Ext(f.Path), ".md") {
			prevByRel[f.Path] = f
		}
	}

	var cands []string // rel paths, slash-normalized
	onDisk := make(map[string]bool)

	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path == dir {
				return nil
			}
			if skipDirs[name] || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !strings.EqualFold(filepath.Ext(name), ".md") {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		onDisk[rel] = true
		cands = append(cands, rel)
		return nil
	})
	if err != nil {
		return res, err
	}

	// Deleted files: previously indexed docs, absent on disk.
	for rel := range prevByRel {
		if onDisk[rel] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if err := st.DeleteByFile(rel); err != nil {
			return res, err
		}
		if err := st.DeleteFileRecord(rel); err != nil {
			return res, err
		}
	}

	for _, rel := range cands {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		if err != nil {
			return res, err
		}
		rec, seen := prevByRel[rel]
		if seen && rec.Size == info.Size() && rec.MtimeNS == info.ModTime().UnixNano() {
			res.Skipped++
			continue
		}
		sum, err := fileSHA256(abs)
		if err != nil {
			return res, err
		}
		if seen && rec.ContentHash == sum {
			res.Skipped++
			continue
		}

		src, err := os.ReadFile(abs)
		if err != nil {
			return res, err
		}
		els, rels := parseSections(rel, string(src))
		if err := st.DeleteByFile(rel); err != nil {
			return res, err
		}
		if err := st.UpsertElements(els); err != nil {
			return res, err
		}
		if err := st.UpsertRelationships(rels); err != nil {
			return res, err
		}
		if err := st.UpsertFiles([]store.FileRecord{{
			Path: rel, Size: info.Size(), MtimeNS: info.ModTime().UnixNano(),
			ContentHash: sum,
		}}); err != nil {
			return res, err
		}
		res.Files++
		res.Elements += len(els)
	}
	return res, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type heading struct {
	level int
	name  string
	line  int // 1-based
}

// parseSections splits content at ATX headings (levels 1-4, outside fenced
// code blocks) into doc elements. Each element's QualifiedName is
// `<rel>#<slug>`; duplicate slugs get a -2, -3, ... suffix. The parent of a
// section is the nearest preceding heading of a shallower level, expressed
// both as ParentQualified and as a contains edge (confidence 1.0).
func parseSections(rel, content string) ([]store.Element, []store.Relationship) {
	lines := strings.Split(content, "\n")

	var heads []heading
	fenced := false
	for i, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if level, name, ok := atxHeading(trimmed); ok {
			heads = append(heads, heading{level: level, name: name, line: i + 1})
		}
	}
	if len(heads) == 0 {
		return nil, nil
	}

	els := make([]store.Element, len(heads))
	used := make(map[string]bool)
	type frame struct {
		level int
		qn    string
	}
	var stack []frame
	var rels []store.Relationship

	for i, h := range heads {
		base := slugify(h.name)
		slug := base
		for n := 2; used[slug]; n++ {
			slug = fmt.Sprintf("%s-%d", base, n)
		}
		used[slug] = true
		qn := rel + "#" + slug

		end := len(lines)
		if i+1 < len(heads) {
			end = heads[i+1].line - 1
		}
		els[i] = store.Element{
			QualifiedName: qn,
			ElementType:   "doc",
			Name:          h.name,
			FilePath:      rel,
			LineStart:     h.line,
			LineEnd:       end,
			Language:      "markdown",
			Content:       truncate(strings.Join(lines[h.line-1:end], "\n")),
		}

		for len(stack) > 0 && stack[len(stack)-1].level >= h.level {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 {
			parent := stack[len(stack)-1].qn
			els[i].ParentQualified = parent
			rels = append(rels, store.Relationship{
				Source: parent, Target: qn, RelType: "contains", Confidence: 1.0,
			})
		}
		stack = append(stack, frame{level: h.level, qn: qn})
	}
	return els, rels
}

// atxHeading parses a trimmed line as an ATX heading of level 1-4
// (CommonMark shape: 1-4 '#' followed by whitespace; trailing '#' runs are
// stripped). Levels 5+ are content, per the `# .. ####` contract.
func atxHeading(line string) (level int, name string, ok bool) {
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level < 1 || level > 4 || level >= len(line) ||
		(line[level] != ' ' && line[level] != '\t') {
		return 0, "", false
	}
	name = strings.TrimSpace(line[level:])
	name = strings.TrimSpace(strings.TrimRight(name, "#"))
	if name == "" {
		return 0, "", false
	}
	return level, name, true
}

// slugify lowercases a heading and turns every non-alphanumeric run into a
// single hyphen, trimming leading/trailing hyphens.
func slugify(name string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevHyphen = false
			continue
		}
		if !prevHyphen && b.Len() > 0 {
			b.WriteByte('-')
			prevHyphen = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "section"
	}
	return s
}

// truncate bounds content at maxContent characters without splitting a rune.
func truncate(s string) string {
	if len(s) <= maxContent {
		return s
	}
	r := []rune(s)
	if len(r) <= maxContent {
		return s
	}
	return string(r[:maxContent])
}
