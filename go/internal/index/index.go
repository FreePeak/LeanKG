// Package index implements the regex-based incremental indexer of the Go
// engine (docs/go-rewrite-analysis.md; W1 scope).
//
// Documented ceilings (upgrade path: tree-sitter extraction in W2):
//   - Extraction is regex-per-language; bodies end at the next element start
//     (Python indentation-bounded, Markdown until a higher-level heading)
//     rather than brace/indent exactness.
//   - `calls` relationships are recomputed only for files processed in this
//     run, against element names seen in this run; call edges whose source
//     lives in an unchanged file are not refreshed.
//   - Store.DeleteByFile removes relationships sourced by a file's elements;
//     edges pointing into a deleted file from other files survive until those
//     files are re-indexed.
//
// Deleted files: contract UPDATE (owner decision) — store.DeleteFileRecord
// exists and is called, so no stale code_files rows remain.
package index

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/FreePeak/LeanKG/go/internal/langs"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Result summarizes one IndexDir run.
type Result struct {
	SkippedLarge  int // files over maxIndexFileBytes (vendored bundles)
	Files         int
	Elements      int
	Relationships int
	Skipped       int
}

// skipDirs are skipped path components during the walk; any dot-prefixed
// directory is skipped as well.
var skipDirs = map[string]bool{
	".git": true, "target": true, "node_modules": true, "vendor": true,
	".leankg": true, "dist": true, "build": true, ".worktrees": true,
	".worktree": true,
}

// extLang maps an indexable file extension to its language tag
// (extension without the dot).
var extLang = map[string]string{
	".go": "go", ".rs": "rust", ".ts": "ts", ".tsx": "tsx",
	".js": "js", ".jsx": "jsx", ".py": "py", ".md": "md",
	".java": "java", ".kt": "kotlin", ".kts": "kotlin",
	".swift": "swift", ".m": "objc", ".mm": "objc", ".dart": "dart",
}

// maxIndexFileBytes bounds the walker: larger files are counted and skipped
// (minified/vendored bundles are ~1 MB and semantically flat; regex
// extraction on them is slow and useless).
const maxIndexFileBytes = 1 << 20

// extOwner resolves a file extension to its language: through the registry
// when given (lazy activation — only codebase-detected languages own their
// extensions), else the static map (legacy all-on behavior).
type extOwnerFunc func(ext string) (string, bool)

func staticOwner(ext string) (string, bool) {
	l, ok := extLang[ext]
	return l, ok
}

// IndexDir walks dir, re-extracts changed/new files, drops deleted ones and
// returns per-run counters. Files whose size+mtime match the stored record or
// whose SHA-256 matches ContentHash are skipped without any writes.
func IndexDir(ctx context.Context, st store.Backend, dir string) (Result, error) {
	return IndexDirWith(ctx, st, dir, nil)
}

// IndexDirWith runs IndexDir through a language registry: when reg != nil,
// only languages currently ACTIVE for the opened codebase own extensions
// (lazy activation; a .kt file in a Go-only tree is skipped, not indexed),
// and extraction routes by the owner language. reg == nil keeps the legacy
// static behavior.
func IndexDirWith(ctx context.Context, st store.Backend, dir string, reg *langs.Registry) (Result, error) {
	owner := staticOwner
	if reg != nil {
		owner = func(ext string) (string, bool) {
			l, ok := reg.ExtOwner(ext)
			return string(l), ok
		}
	}
	return indexDir(ctx, st, dir, owner)
}

func indexDir(ctx context.Context, st store.Backend, dir string, owner extOwnerFunc) (Result, error) {
	var res Result

	prev, err := st.Files()
	if err != nil {
		return res, err
	}
	prevByRel := make(map[string]store.FileRecord, len(prev))
	for _, f := range prev {
		prevByRel[f.Path] = f
	}

	type candidate struct {
		rel, abs, lang string
		size           int64
		mtimeNS        int64
	}
	var cands []candidate
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
		if !d.Type().IsRegular() {
			return nil
		}
		if _, ok := owner(filepath.Ext(name)); !ok {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Size() > maxIndexFileBytes {
			res.SkippedLarge++ // vendored/minified bundles: not index material
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		onDisk[rel] = true
		lang, _ := owner(filepath.Ext(name))
		cands = append(cands, candidate{
			rel: rel, abs: path, lang: lang,
			size: info.Size(), mtimeNS: info.ModTime().UnixNano(),
		})
		return nil
	})
	if err != nil {
		return res, err
	}

	// Deleted files: previously indexed, absent on disk.
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

	// Changed/new files: 3-signal detection (size+mtime fast path, then
	// SHA-256 confirmation). Extraction and writes are grouped PER DIRECTORY:
	//   (a) memory stays bounded by one package at a time (a 40k-file tree
	//       previously buffered every file's elements before writing);
	//   (b) call targets are scoped to the same package — matching against a
	//       run-global name map linked unrelated same-named elements across
	//       projects (Start/New/String/Error), producing bogus cross-repo
	//       edges on polyrepos.
	// Ceiling: no import/type resolution, so cross-package calls are not
	// linked (upgrade path: tree-sitter symbol tables).
	type changedFile struct {
		rel, abs, lang, hash string
		size                 int64
		mtimeNS              int64
	}
	byDir := map[string][]changedFile{}
	var dirOrder []string
	for _, c := range cands {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		rec, seen := prevByRel[c.rel]
		if seen && rec.Size == c.size && rec.MtimeNS == c.mtimeNS {
			res.Skipped++
			continue
		}
		sum, err := fileSHA256(c.abs)
		if err != nil {
			return res, err
		}
		if seen && rec.ContentHash == sum {
			// Content identical despite size/mtime signal mismatch: skip, no
			// writes (the stored record stays as-is per contract, so the next
			// run re-hashes this file — acceptable).
			res.Skipped++
			continue
		}
		d := dirOf(c.rel)
		if _, ok := byDir[d]; !ok {
			dirOrder = append(dirOrder, d)
		}
		byDir[d] = append(byDir[d], changedFile{
			rel: c.rel, abs: c.abs, lang: c.lang, hash: sum,
			size: c.size, mtimeNS: c.mtimeNS,
		})
	}

	for _, d := range dirOrder {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		files := byDir[d]
		extracted := make([]fileElements, 0, len(files))
		names := map[string][]string{} // package-scoped call targets
		for _, f := range files {
			fe, err := extractFileAs(f.rel, f.abs, f.lang)
			if err != nil {
				return res, err
			}
			extracted = append(extracted, fe)
			for _, e := range fe.elements {
				names[e.name] = append(names[e.name], e.qn)
			}
		}
		for i, fe := range extracted {
			f := files[i]
			if err := st.DeleteByFile(fe.rel); err != nil {
				return res, err
			}
			if err := st.UpsertElements(fe.toStore()); err != nil {
				return res, err
			}
			rels := relationships(fe.elements, names)
			if err := st.UpsertRelationships(rels); err != nil {
				return res, err
			}
			if err := st.UpsertFiles([]store.FileRecord{{
				Path: fe.rel, Size: f.size, MtimeNS: f.mtimeNS,
				ContentHash: f.hash,
			}}); err != nil {
				return res, err
			}
			res.Files++
			res.Elements += len(fe.elements)
			res.Relationships += len(rels)
		}
	}
	return res, nil
}

// dirOf returns the package directory of a repo-relative path ("." when the
// file sits at the root).
func dirOf(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return "."
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
