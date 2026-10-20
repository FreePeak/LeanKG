package index

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/FreePeak/LeanKG/internal/gitobj"
)

// candidate is one file a run may index. A git candidate names its HEAD blob
// and is read only through the run's gitobj.Reader; a walk candidate is read
// from its path on disk.
type candidate struct {
	rel, lang string
	abs       string // walk candidates only
	oid       string // git candidates only: the HEAD blob id
	size      int64
	mtimeNS   int64 // walk candidates only; git candidates carry 0
}

// content returns the bytes a run extracts and hashes for c. Nothing on the
// git path touches the working-tree file.
func (c candidate) content(rd *gitobj.Reader) ([]byte, error) {
	if c.oid != "" {
		return rd.Read(c.oid)
	}
	return os.ReadFile(c.abs)
}

// gitCandidates keeps the HEAD blobs the walk would have kept: the same
// directory rule, the same language gate, and the same size cap.
func gitCandidates(blobs []gitobj.Blob, owner extOwnerFunc, res *Result) []candidate {
	var cands []candidate
	for _, b := range blobs {
		if inSkippedDir(b.Path) {
			continue
		}
		if dotFile(b.Path) {
			continue
		}
		lang, ok := fileLang(b.Path, owner)
		if !ok {
			continue
		}
		if b.Size > maxIndexFileBytes {
			res.SkippedLarge++
			continue
		}
		cands = append(cands, candidate{rel: b.Path, lang: lang, oid: b.OID, size: b.Size})
	}
	return cands
}

// HeadIndexableFiles counts the files an index run over dir would take from
// HEAD: the git candidates under the static owner, size cap applied. ok is false
// when dir is not a repository root with a commit, since that run walks the disk
// instead. The count ignores an activated language registry, so it can exceed
// what a registry-scoped run keeps.
func HeadIndexableFiles(ctx context.Context, dir string) (n int, ok bool, err error) {
	blobs, ok, err := gitobj.HeadBlobs(ctx, dir)
	if err != nil || !ok {
		return 0, ok, err
	}
	var res Result
	return len(gitCandidates(blobs, staticOwner, &res)), true, nil
}

// walkCandidates lists the working-tree files under dir the same way the walk
// always has.
func walkCandidates(ctx context.Context, dir string, owner extOwnerFunc, res *Result) ([]candidate, error) {
	var cands []candidate
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && skippedDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if dotFile(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		lang, ok := fileLang(rel, owner)
		if !ok {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxIndexFileBytes {
			res.SkippedLarge++ // vendored/minified bundles: not index material
			return nil
		}
		cands = append(cands, candidate{
			rel: rel, abs: path, lang: lang,
			size: info.Size(), mtimeNS: info.ModTime().UnixNano(),
		})
		return nil
	})
	return cands, err
}

// skippedDir reports whether the walk prunes a directory of this name: a
// skipDirs entry, or any dot-prefixed directory.
func skippedDir(name string) bool {
	return skipDirs[name] || strings.HasPrefix(name, ".")
}

// dotFile reports whether a FILE is out because its name starts with a dot.
//
// A dot-prefixed FILE is out for the same reason a dot-prefixed directory is:
// the extension gate cannot tell a tracked `.env.example` from a live `.env`,
// so without this every dotfile with a language extension entered the store —
// agent scratch files (`.*.md`), harness configs, and secret-shaped files that
// then get embedded, exported and pushed. Found live on this repository: a
// dot-prefixed PR scratch note answered the query "where is the single flight
// lock taken" at rank 1 on both arms, outranking the source documenting it.
//
// ponytail: name-based, not a denylist of secret filenames — one rule, and a
// denylist is always incomplete. The cost is that a deliberately dot-prefixed
// source file is not indexed; nothing in this repo tracks one, and `git
// ls-files` is the authority if that ever changes.
func dotFile(name string) bool {
	return strings.HasPrefix(name, ".")
}

// inSkippedDir reports whether a repo-relative file path sits under a directory
// the walk would prune. It gives git candidates the walk's directory rule.
func inSkippedDir(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, name := range parts[:len(parts)-1] {
		if skippedDir(name) {
			return true
		}
	}
	return false
}

// fileLang is the language a repo-relative file is indexed as, and whether it
// is a candidate at all: an extension an active language owns, or a file a
// specialist claims (which has no language of its own).
func fileLang(rel string, owner extOwnerFunc) (string, bool) {
	if lang, ok := owner(filepath.Ext(rel)); ok {
		return lang, true
	}
	return "", SpecialistClaim(rel)
}

// gitKeyPrefix marks a ContentHash that names a HEAD blob rather than the
// SHA-256 of the bytes.
const gitKeyPrefix = "git:"

func gitKey(oid string) string { return gitKeyPrefix + oid }

func isGitKey(contentHash string) bool { return strings.HasPrefix(contentHash, gitKeyPrefix) }
