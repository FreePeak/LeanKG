// Package ccmemory reads Claude Code's per-repository auto-memory files and
// maps them to the documents the central memory store will hold. It is
// read-only: it never writes under the projects root.
package ccmemory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/FreePeak/LeanKG/internal/mdmeta"
	"github.com/FreePeak/LeanKG/internal/pathguard"
)

// maxDocBytes is the largest memory file Discover reads.
const maxDocBytes = 64 << 10

// memoryIndex is Claude Code's index file, which lists the other memories.
const memoryIndex = "MEMORY.md"

// Kind is the memory type from a file's frontmatter `type` key.
type Kind string

const (
	KindUser      Kind = "user"
	KindFeedback  Kind = "feedback"
	KindProject   Kind = "project"
	KindReference Kind = "reference"
	KindUnknown   Kind = "unknown"
)

// Doc is one memory file as read from Claude Code's memory directory.
type Doc struct {
	Slug        string   // directory name under the projects root; identifies the repository
	File        string   // base name of the memory file
	Name        string   // frontmatter name, else the file name without .md
	Description string   // frontmatter description, else the first non-empty body line
	Kind        Kind     // from the frontmatter type key; unknown when absent or not a known type
	Body        string   // content after the frontmatter
	Links       []string // [[wiki-link]] targets in the body, document order, deduplicated
	ModTime     int64    // file modification time, Unix seconds
	SourceHash  string   // left empty here; sync computes it
	FileSHA     string   // hex sha256 of the raw file bytes, set by Discover
}

// Skipped records a file that Discover did not read, and why.
type Skipped struct {
	Path   string
	Reason string
}

// Discovery is the result of scanning the projects root.
type Discovery struct {
	Docs        []Doc
	Skipped     []Skipped
	RootMissing bool
	FailedSlugs []string // projects whose memory directory could not be scanned, sorted by slug
}

// ParseDoc builds a Doc from one memory file's content. Frontmatter is read
// only when the content opens with a --- fence, so a body that merely contains
// rule lines is not mistaken for frontmatter.
func ParseDoc(slug, file string, content []byte, modTime int64) Doc {
	text := string(content)
	meta, body := map[string]string{}, strings.Trim(text, "\n")
	if strings.HasPrefix(strings.TrimLeft(text, "\r\n"), "---") {
		meta, body = mdmeta.SplitFrontmatter(text)
	}
	d := Doc{
		Slug:    slug,
		File:    file,
		Name:    unquote(meta["name"]),
		Kind:    kindOf(unquote(meta["type"])),
		Body:    body,
		Links:   mdmeta.WikiLinks(body),
		ModTime: modTime,
	}
	d.Description = unquote(meta["description"])
	if d.Name == "" {
		d.Name = strings.TrimSuffix(file, ".md")
	}
	if d.Description == "" {
		d.Description = firstLine(body)
	}
	return d
}

// memoryDirOf is the memory directory of one project under projectsRoot.
func memoryDirOf(projectsRoot, slug string) string {
	return filepath.Join(projectsRoot, slug, "memory")
}

// Discover reads every memory document under the projects root root. A memory
// directory is <root>/<slug>/memory; each *.md in it except MEMORY.md is a
// document. Subdirectories are not read. Files that resolve outside root, are
// over 64 KiB, are unreadable or are not valid UTF-8 are recorded in Skipped
// and the scan continues. A memory directory that cannot be stat'ed or listed
// is recorded in Skipped and FailedSlugs. A project with no memory directory
// is neither. A missing root sets RootMissing and is not an error.
func Discover(root string) (Discovery, error) {
	var d Discovery
	slugs, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		d.RootMissing = true
		return d, nil
	}
	if err != nil {
		return d, err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return d, err
	}
	for _, s := range slugs {
		memDir := memoryDirOf(root, s.Name())
		fi, err := os.Stat(memDir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			d.fail(s.Name(), memDir, err)
			continue
		}
		if err != nil || !fi.IsDir() {
			continue // no memory directory for this project
		}
		files, err := os.ReadDir(memDir)
		if err != nil {
			d.fail(s.Name(), memDir, err)
			continue
		}
		for _, f := range files {
			name := f.Name()
			if !strings.HasSuffix(name, ".md") || name == memoryIndex {
				continue
			}
			path := filepath.Join(memDir, name)
			doc, reason := readDoc(s.Name(), path, name, realRoot)
			if reason != "" {
				d.Skipped = append(d.Skipped, Skipped{Path: path, Reason: reason})
				continue
			}
			d.Docs = append(d.Docs, doc)
		}
	}
	return d, nil
}

// fail records that the memory directory of slug could not be scanned. Sync
// keeps that project's rows: a scan that could not see the directory says
// nothing about the documents in it.
func (d *Discovery) fail(slug, memDir string, err error) {
	d.Skipped = append(d.Skipped, Skipped{Path: memDir, Reason: "unreadable directory: " + err.Error()})
	d.FailedSlugs = append(d.FailedSlugs, slug)
}

// readDoc reads one memory file. A non-empty reason means the file is skipped.
func readDoc(slug, path, name, realRoot string) (Doc, string) {
	fi, err := os.Stat(path)
	if err != nil {
		return Doc{}, "unreadable: " + err.Error()
	}
	if !fi.Mode().IsRegular() {
		return Doc{}, "not a regular file"
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Doc{}, "unreadable: " + err.Error()
	}
	if !pathguard.Within(resolved, realRoot) {
		return Doc{}, "symlink resolves outside the projects root"
	}
	if fi.Size() > maxDocBytes {
		return Doc{}, "file is over the 64 KiB limit"
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Doc{}, "unreadable: " + err.Error()
	}
	if !utf8.Valid(content) {
		return Doc{}, "malformed: not valid UTF-8"
	}
	doc := ParseDoc(slug, name, content, fi.ModTime().Unix())
	sum := sha256.Sum256(content)
	doc.FileSHA = hex.EncodeToString(sum[:])
	return doc, ""
}

// kindOf maps a frontmatter type value to a Kind; anything else is unknown.
func kindOf(v string) Kind {
	switch Kind(v) {
	case KindUser, KindFeedback, KindProject, KindReference:
		return Kind(v)
	}
	return KindUnknown
}

// unquote strips one pair of matching surrounding quotes from a YAML scalar.
func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

// firstLine returns the first non-empty line of body, trimmed.
func firstLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}
