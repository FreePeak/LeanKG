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
	"fmt"
	"path/filepath"
	"strings"

	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/gitobj"
	"github.com/FreePeak/LeanKG/internal/langs"
	"github.com/FreePeak/LeanKG/internal/lsp"
	"github.com/FreePeak/LeanKG/internal/store"
)

// Result summarizes one IndexDir run.
type Result struct {
	SkippedLarge int // files over maxIndexFileBytes (vendored bundles)
	Files        int
	// DeletedFiles counts previously indexed files the reconcile removed
	// because the walk did not see them. Reported (not just performed) because
	// a walk root that is not the store's own root makes every outside file look
	// deleted — the failure mode that cost this repo's index 4,525 elements
	// during dogfooding. A nonzero value on an unchanged tree means the caller
	// pointed the index at the wrong root.
	DeletedFiles  int
	Elements      int
	Relationships int
	Skipped       int
}

// skipDirs are skipped path components during the walk and for git candidates;
// any dot-prefixed directory is skipped as well (skippedDir).
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
	// expanded set (extract_langexp.go)
	".c": "c", ".h": "c",
	".cpp": "cpp", ".cc": "cpp", ".cxx": "cpp", ".hpp": "cpp", ".hh": "cpp", ".hxx": "cpp", ".h++": "cpp",
	".cs":  "csharp",
	".php": "php", ".phtml": "php",
	".rb": "ruby", ".ruby": "ruby", ".rake": "ruby", ".gemspec": "ruby",
	".scala": "scala", ".sc": "scala",
	".pl": "perl", ".pm": "perl", ".t": "perl",
	".lua": "lua",
	".hs":  "haskell", ".lhs": "haskell",
	".ex": "elixir", ".exs": "elixir",
	// language-expansion wave 2 (extract_langexp2.go)
	".cr": "crystal",
	".cu": "cuda", ".cuh": "cuda",
	".cyp": "cypher",
	".elm": "elm",
	".erl": "erlang", ".hrl": "erlang",
	".fs": "fsharp", ".fsi": "fsharp", ".fsx": "fsharp",
	".glsl": "glsl", ".vert": "glsl", ".frag": "glsl", ".geom": "glsl", ".tesc": "glsl", ".tese": "glsl", ".comp": "glsl",
	".hlsl": "hlsl", ".fx": "hlsl", ".fxh": "hlsl", ".hlsli": "hlsl",
	".nim": "nim", ".nims": "nim",
	".ml": "ocaml", ".mli": "ocaml",
	".sql": "sql", ".pls": "sql",
	".ps1": "powershell", ".psm1": "powershell", ".psd1": "powershell",
	".qs":  "qsharp",
	".sol": "solidity",
	".sv":  "systemverilog", ".svh": "systemverilog",
	".v": "verilog", ".vh": "verilog",
	".zig": "zig",
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

// IndexDir indexes dir, re-extracts changed/new files, drops deleted ones and
// returns per-run counters. A git repository root with a commit is indexed from
// HEAD (committed content, change detection by blob ID); any other directory is
// walked from disk. Walked files whose size+mtime match the stored record or
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
	res, err := indexDir(ctx, st, dir, owner)
	if err != nil {
		return res, err
	}
	// Post-index LSP enrichment (Rust lsp/bridge parity): merge server symbols
	// into the stored elements and upgrade typed call edges. Absent servers are
	// recorded as reasons on the result, never errors — a codebase with no LSP
	// server indexes exactly as before.
	// Opt-in, like Rust (indexer.typed_resolve defaults to "off" and the bridge
	// is configured per project): a codebase without leankg.yaml lsp/indexer
	// config indexes exactly as before, spawning nothing. With config present,
	// servers merge their symbols and typed call edges into the store.
	if res.Files > 0 {
		if _, _, configured := lsp.LoadProject(dir); configured {
			er, err := lsp.Enrich(st, dir, activeLangTags(reg))
			if err != nil {
				return res, err
			}
			if er.Enabled {
				res.Elements += er.NewElements
				res.Relationships += er.CallsUpgraded
			}
		}
	}
	return res, nil
}

// Index-root tripwire (RS-02). Stored file paths are relative to the walk
// root and the reconcile below deletes every stored path the walk does not
// see, so walking a DIFFERENT root against an existing store reads the whole
// project as deleted — `import repo <sibling>` replaced a 48-file index with
// one file. The store records the root it was built from and refuses any
// other; RebaseRoot is the deliberate escape for a moved checkout.
const (
	rootKVNamespace = store.IndexKVNamespace
	rootKVKey       = store.RootKVKey
)

// CanonicalRoot is the form a walk root is recorded and compared in:
// absolute and symlink-resolved, so /tmp and /private/tmp agree.
func CanonicalRoot(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	return filepath.Clean(dir)
}

// IsGitKeyed reports whether a stored content hash names a git blob
// ("git:<blob oid>") rather than the SHA-256 of walked bytes.
//
// It is the marker that says "this store was built from the repository's
// objects": only such a record can be compared against a tree, because only
// such a record's content is guaranteed to BE the tree's content. A
// filesystem-scanned store records SHA-256 hashes and must never be pruned
// against a git tree.
func IsGitKeyed(contentHash string) bool {
	return isGitKey(contentHash)
}

// DeleteFiles removes the store content of the given file paths: the
// elements and relationships the files own, and their file records.
//
// It is the shared delete step of the incremental index reconcile (a file the
// walk does not see any more) and of the maintenance reconcile against HEAD
// (a file the tree does not contain any more), so both remove a file the
// same way.
//
// It does NOT touch vectors: vectors are keyed by qualified name, not by
// file, and a vector whose element is gone is an ORPHAN — removed by the
// orphan sweep (store.DeleteOrphanVectors), never by a per-file delete
// (which would also discard the vectors of a changed file's unchanged
// symbols).
func DeleteFiles(ctx context.Context, st store.Backend, paths []string) (int, error) {
	n := 0
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		if err := st.DeleteByFile(p); err != nil {
			return n, err
		}
		if err := st.DeleteFileRecord(p); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// RebaseRoot records dir as the store's index root, replacing any previous
// one (`leankg index --rebase-root` after moving a checkout).
func RebaseRoot(st store.Backend, dir string) error {
	return st.KVSet(rootKVNamespace, rootKVKey, CanonicalRoot(dir))
}

// checkRoot enforces the tripwire: a store with no recorded root (built
// before RS-02, or empty) adopts dir; a store with files and a different
// recorded root refuses the walk before anything is deleted.
func checkRoot(st store.Backend, dir string, hasFiles bool) error {
	want := CanonicalRoot(dir)
	got, ok, err := st.KVGet(rootKVNamespace, rootKVKey)
	if err != nil {
		return err
	}
	if ok && got != "" && got != want && hasFiles {
		return errs.NewError(errs.IndexRootMismatch,
			fmt.Sprintf("this store was indexed from %q; walking %q would delete every element outside it", got, want),
			"index the recorded root, open the other directory as its own project, or run `leankg index --rebase-root <dir>` if the checkout moved")
	}
	if !ok || got != want {
		return st.KVSet(rootKVNamespace, rootKVKey, want)
	}
	return nil
}

// extractorVersion versions what the extractors put into an element
// (content, boundaries, edge rules). Unchanged files are skipped by the
// size+mtime / content-hash signals, so an extractor change would otherwise
// reach a store only as its files happen to change: v2 (RS-10, leading doc
// comments in content; signature-only names as references) never reached
// an existing store at all; v3 (#531, word boundary after the TS/JS
// `function` keyword) drops phantom elements such as `ality` from
// `functionality`. A store recorded under another version re-extracts
// every file once.
const extractorVersion = "3"

const extractorKVKey = "extractor_version"

func indexDir(ctx context.Context, st store.Backend, dir string, owner extOwnerFunc) (res Result, err error) {
	prev, err := st.Files()
	if err != nil {
		return res, err
	}
	prevByRel := make(map[string]store.FileRecord, len(prev))
	for _, f := range prev {
		prevByRel[f.Path] = f
	}
	if err := checkRoot(st, dir, len(prev) > 0); err != nil {
		return res, err
	}
	ver, _, err := st.KVGet(rootKVNamespace, extractorKVKey)
	if err != nil {
		return res, err
	}
	reextract := len(prev) > 0 && ver != extractorVersion

	// A repository root with commits is indexed from HEAD (committed content
	// only, never the working tree). Any other directory walks the disk.
	//
	// The blobs are listed for the commit HEAD names AT THIS MOMENT, not for
	// a moving HEAD: the commit id is recorded alongside them, and the two
	// must describe the same tree (a HEAD that moves between the listing and
	// the record would make the store claim a commit it never read).
	headCommit, blobs, fromGit, err := gitobj.HeadSnapshot(ctx, dir)
	if err != nil {
		return res, err
	}
	var cands []candidate
	var rd *gitobj.Reader // git source only: one cat-file process per run
	if fromGit {
		cands = gitCandidates(blobs, owner, &res)
		if rd, err = gitobj.NewReader(ctx, dir); err != nil {
			return res, err
		}
		defer func() {
			if cerr := rd.Close(); err == nil {
				err = cerr
			}
		}()
	} else if cands, err = walkCandidates(ctx, dir, owner, &res); err != nil {
		return res, err
	}
	onDisk := make(map[string]bool, len(cands))
	for _, c := range cands {
		onDisk[c.rel] = true
	}

	// Deleted files: previously indexed, absent from the source.
	var missing []string
	for rel := range prevByRel {
		if onDisk[rel] {
			continue
		}
		missing = append(missing, rel)
	}
	deleted, err := DeleteFiles(ctx, st, missing)
	if err != nil {
		return res, err
	}
	res.DeletedFiles += deleted

	// Changed/new files. A git candidate is keyed by its blob ID: a stored record
	// with the same key is skipped without reading anything. A walk candidate
	// keeps the 3-signal check (size+mtime, then SHA-256). Extraction and writes
	// are grouped PER DIRECTORY:
	//   (a) memory stays bounded by one package at a time (a 40k-file tree
	//       previously buffered every file's elements before writing);
	//   (b) call targets are scoped to the same package — matching against a
	//       run-global name map linked unrelated same-named elements across
	//       projects (Start/New/String/Error), producing bogus cross-repo
	//       edges on polyrepos.
	// Ceiling: no import/type resolution, so cross-package calls are not
	// linked (upgrade path: tree-sitter symbol tables).
	type changedFile struct {
		candidate
		hash string
	}
	var rekeys []store.FileRecord // walk-written records that match a blob
	byDir := map[string][]changedFile{}
	var dirOrder []string
	for _, c := range cands {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		rec, seen := prevByRel[c.rel]
		trusted := seen && !reextract
		var hash string
		if c.oid != "" {
			hash = gitKey(c.oid)
			if trusted && rec.ContentHash == hash {
				res.Skipped++
				continue
			}
			// Only a walk-written record can hold a SHA-256 of the bytes, so a
			// record already keyed to a blob is changed without reading it.
			if trusted && !isGitKey(rec.ContentHash) {
				content, err := c.content(rd)
				if err != nil {
					return res, err
				}
				if rec.ContentHash == sha256Hex(content) {
					// Same bytes, written by the walk: adopt the blob key, no extraction.
					rekeys = append(rekeys, store.FileRecord{Path: c.rel, Size: c.size, ContentHash: hash})
					res.Skipped++
					continue
				}
			}
		} else {
			if trusted && rec.Size == c.size && rec.MtimeNS == c.mtimeNS {
				res.Skipped++
				continue
			}
			content, err := c.content(rd)
			if err != nil {
				return res, err
			}
			hash = sha256Hex(content)
			if trusted && rec.ContentHash == hash {
				// Content identical despite size/mtime signal mismatch: skip, no
				// writes (the stored record stays as-is per contract, so the next
				// run re-hashes this file — acceptable).
				res.Skipped++
				continue
			}
		}
		d := dirOf(c.rel)
		if _, ok := byDir[d]; !ok {
			dirOrder = append(dirOrder, d)
		}
		byDir[d] = append(byDir[d], changedFile{candidate: c, hash: hash})
	}
	if len(rekeys) > 0 {
		if err := st.UpsertFiles(rekeys); err != nil {
			return res, err
		}
	}

	// pendingFile pairs each extracted file with its own record: a specialist
	// file that writes itself is dropped here, so later files must not shift.
	type pendingFile struct {
		changedFile
		fe  fileElements
		src []byte
	}
	for _, d := range dirOrder {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		files := byDir[d]
		pending := make([]pendingFile, 0, len(files))
		names := map[string][]string{} // package-scoped call targets
		for _, f := range files {
			src, err := f.content(rd)
			if err != nil {
				return res, err
			}
			if handled, err := indexSpecialistFile(st, f.rel, src, f.size, f.mtimeNS, f.hash, &res); err != nil {
				return res, err
			} else if handled {
				continue // the specialist own its extraction AND its writes
			}
			fe := extractFileAs(f.rel, src, f.lang)
			pending = append(pending, pendingFile{changedFile: f, fe: fe, src: src})
			for _, e := range fe.elements {
				names[e.name] = append(names[e.name], e.qn)
			}
		}
		for _, p := range pending {
			f := p.changedFile
			if err := st.DeleteByFile(f.rel); err != nil {
				return res, err
			}
			if err := st.UpsertElements(p.fe.toStore()); err != nil {
				return res, err
			}
			rels := relationships(p.fe.elements, names)
			if err := st.UpsertRelationships(rels); err != nil {
				return res, err
			}
			if err := st.UpsertFiles([]store.FileRecord{{
				Path: f.rel, Size: f.size, MtimeNS: f.mtimeNS,
				ContentHash: f.hash,
			}}); err != nil {
				return res, err
			}
			res.Files++
			res.Elements += len(p.fe.elements)
			res.Relationships += len(rels)
			if f.lang == "kotlin" {
				if err := indexKotlinExtras(st, f.rel, p.src, &res); err != nil {
					return res, err
				}
			}
		}
	}
	// Vectors are keyed by qualified name and DeleteByFile does not own them,
	// so every deleted file or removed symbol left a vector behind (RS-05).
	// Sweep once per run that changed anything, instead of cascading inside
	// DeleteByFile, which would also discard the vectors of a changed file's
	// UNCHANGED symbols and force them to re-embed.
	if res.DeletedFiles > 0 || res.Files > 0 {
		if _, err := st.DeleteOrphanVectors(); err != nil {
			return res, fmt.Errorf("index: reclaim orphan vectors: %w", err)
		}
	}
	if ver != extractorVersion {
		if err := st.KVSet(rootKVNamespace, extractorKVKey, extractorVersion); err != nil {
			return res, err
		}
	}
	// Record the commit this store now mirrors, so a reader can tell whether
	// the TREE moved since the index ran (store.HeadSync). The freshness
	// label cannot: it compares two counters inside the same database.
	//
	// The recorded id is the commit whose blobs this run READ — not HEAD read
	// again afterwards — so the record and the content describe one tree. A
	// filesystem-scanned store records the empty id: it was not built from a
	// git tree, and claiming one would be a lie the reconcile would act on.
	if err := store.SetHead(st, headCommit); err != nil {
		return res, err
	}
	return res, nil
}

// activeLangTags lists registry-active language ids for LSP enrichment; nil
// (legacy static path) lets the enrich pass consider every language present.
func activeLangTags(reg *langs.Registry) []string {
	if reg == nil {
		return nil
	}
	active := reg.Active()
	out := make([]string, 0, len(active))
	for _, l := range active {
		out = append(out, string(l))
	}
	return out
}

// dirOf returns the package directory of a repo-relative path ("." when the
// file sits at the root).
func dirOf(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return "."
}

// sha256Hex is the content hash a walk candidate is stored and compared under.
func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
