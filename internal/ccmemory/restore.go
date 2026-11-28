package ccmemory

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/pathguard"
)

// ErrRestoreIntoRequired means Restore was called without a target directory.
var ErrRestoreIntoRequired = errors.New("ccmemory: restore needs a target directory (--into)")

// errUnsafeName means a row's project or file name would not stay under the target.
var errUnsafeName = errors.New("ccmemory: restore refused a row with an unsafe project or file name")

// RestoreOptions selects the rows to write and the directory to write them under.
type RestoreOptions struct {
	Projects string // Claude Code projects root; Into must not be it or lie inside it
	Into     string // directory to write under; required
	OnlySlug string // limit to one project slug; empty means every project
	Deleted  bool   // also restore rows kept in ArchiveBank
}

// RestoreResult counts the memory files one restore run handled. Written and
// Skipped count memory files. ArchivedIncluded counts the written files that
// came from ArchiveBank. SkippedExcluded counts the stored rows left out
// because their project is on the exclude list.
type RestoreResult struct {
	Written          int `json:"written"`
	Skipped          int `json:"skipped"`
	ArchivedIncluded int `json:"archived_included"`
	SkippedExcluded  int `json:"skipped_excluded"`
}

// restoreRow is one stored row to restore. archived marks a row read from ArchiveBank.
type restoreRow struct {
	key      Key
	row      memory.Entry
	archived bool
}

// restoreFile is one memory file the run creates: its target path, the fields
// its project index lists and the full file text.
type restoreFile struct {
	path        string
	slug        string
	name        string
	description string
	content     string
	archived    bool
}

// Restore rebuilds Claude Code memory files under opts.Into from the cc- banks,
// and from ArchiveBank too with opts.Deleted. Each row becomes
// <Into>/<slug>/memory/<file>, and each project gets a MEMORY.md index of the
// files written for it. An existing file is never overwritten. Every row is
// checked before the first write, so one unsafe row writes nothing.
func Restore(ctx context.Context, mem *memory.Memory, opts RestoreOptions) (RestoreResult, error) {
	if opts.Into == "" {
		return RestoreResult{}, ErrRestoreIntoRequired
	}
	if err := ctx.Err(); err != nil {
		return RestoreResult{}, err
	}
	if err := InstallDenyList(mem.Root()); err != nil {
		return RestoreResult{}, err
	}
	ex, err := loadExcludeList()
	if err != nil {
		return RestoreResult{}, err
	}
	into, err := filepath.Abs(opts.Into)
	if err != nil {
		return RestoreResult{}, err
	}
	if pathguard.Within(realPath(into), realPath(opts.Projects)) {
		return RestoreResult{}, fmt.Errorf("ccmemory: restore target %s is the Claude Code projects directory or inside it; copy the files by hand instead", into)
	}
	rows, err := restoreRows(mem, opts)
	if err != nil {
		return RestoreResult{}, err
	}
	rows, skipped := withoutExcluded(rows, ex)
	files, err := planRestore(into, rows)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := refuseSymlinks(into, files); err != nil {
		return RestoreResult{}, err
	}
	res, err := writeRestore(ctx, files)
	res.SkippedExcluded = skipped
	return res, err
}

// withoutExcluded drops the rows of excluded projects and returns how many it
// dropped. Sync purges those rows, but a row written after the last sync, or an
// archive copy, must not reach the target either.
func withoutExcluded(rows []restoreRow, ex excludeList) ([]restoreRow, int) {
	kept := rows[:0]
	dropped := 0
	for _, r := range rows {
		if ex.excludes(metaString(r.row, "project")) {
			dropped++
			continue
		}
		kept = append(kept, r)
	}
	return kept, dropped
}

// errSymlinkUnderInto means a directory or file the restore would create under
// the target is, or goes through, a symbolic link. Writing through it could
// reach the Claude Code projects tree, so the run stops before the first write.
var errSymlinkUnderInto = errors.New("ccmemory: restore refused: a path under the target is a symbolic link; nothing was written")

// refuseSymlinks checks each planned file, and the MEMORY.md beside it, from
// the target down, one path component at a time. The target itself is not
// checked: the caller chose it.
func refuseSymlinks(into string, files []restoreFile) error {
	for _, f := range files {
		for _, path := range []string{f.path, filepath.Join(filepath.Dir(f.path), memoryIndex)} {
			if err := refuseSymlinkPath(into, path); err != nil {
				return err
			}
		}
	}
	return nil
}

// refuseSymlinkPath Lstats each component of path below into. A component that
// does not exist ends the walk: nothing below it exists either.
func refuseSymlinkPath(into, path string) error {
	rel, err := filepath.Rel(into, path)
	if err != nil {
		return err
	}
	cur := into
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return errSymlinkUnderInto
		}
	}
	return nil
}

// restoreRows reads the rows to restore. Live rows come first, so a live row
// wins over an archive copy of the same file. Within each group rows are newest first.
func restoreRows(mem *memory.Memory, opts RestoreOptions) ([]restoreRow, error) {
	live, err := actualRows(mem, opts.OnlySlug)
	if err != nil {
		return nil, err
	}
	var liveRows []restoreRow
	for key, row := range live {
		liveRows = append(liveRows, restoreRow{key: key, row: row})
	}
	sortNewestFirst(liveRows)
	if !opts.Deleted {
		return liveRows, nil
	}

	archived, _, err := mem.List(ArchiveBank, 0, 0)
	if err != nil {
		return nil, err
	}
	// Rows store the slug masked by memory.Redact, so the scope is matched in that form.
	scope := memory.Redact(opts.OnlySlug)
	var archiveRows []restoreRow
	for _, row := range archived {
		if opts.OnlySlug != "" && metaString(row, "project") != scope {
			continue
		}
		id, _ := row.Metadata["document_id"].(string)
		archiveRows = append(archiveRows, restoreRow{key: Key{Bank: ArchiveBank, DocID: id}, row: row, archived: true})
	}
	sortNewestFirst(archiveRows)
	return append(liveRows, archiveRows...), nil
}

func sortNewestFirst(rows []restoreRow) {
	slices.SortFunc(rows, func(a, b restoreRow) int {
		return cmp.Or(
			cmp.Compare(b.row.Timestamp, a.row.Timestamp),
			strings.Compare(a.key.Bank, b.key.Bank),
			strings.Compare(a.key.DocID, b.key.DocID),
		)
	})
}

// planRestore checks every row and builds the file it will write. It runs
// before the first write, so an unsafe row stops the run with nothing written.
// The error names the bank and document id only, never the body.
func planRestore(into string, rows []restoreRow) ([]restoreFile, error) {
	files := make([]restoreFile, 0, len(rows))
	for _, r := range rows {
		slug, file := metaString(r.row, "project"), metaString(r.row, "file")
		path, err := safeRestorePath(into, slug, file)
		if err != nil {
			return nil, fmt.Errorf("%w (%s, %s)", err, r.key.Bank, r.key.DocID)
		}
		name, description := metaString(r.row, "name"), metaString(r.row, "description")
		files = append(files, restoreFile{
			path:        path,
			slug:        slug,
			name:        name,
			description: description,
			content:     renderMemoryFile(name, description, metaString(r.row, "kind"), storedBody(r.row.Content)),
			archived:    r.archived,
		})
	}
	return files, nil
}

// writeRestore creates each planned file that does not exist yet, then one
// MEMORY.md per project for the files it wrote. A path met twice in this run
// is skipped on its second row, so the first (newest, live first) wins.
func writeRestore(ctx context.Context, files []restoreFile) (RestoreResult, error) {
	var res RestoreResult
	seen := map[string]bool{}
	written := map[string][]restoreFile{}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if seen[f.path] {
			res.Skipped++
			continue
		}
		seen[f.path] = true
		if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
			return res, err
		}
		ok, err := createNew(f.path, f.content)
		if err != nil {
			return res, err
		}
		if !ok {
			res.Skipped++
			continue
		}
		res.Written++
		if f.archived {
			res.ArchivedIncluded++
		}
		written[f.slug] = append(written[f.slug], f)
	}
	for _, group := range written {
		index := filepath.Join(filepath.Dir(group[0].path), memoryIndex)
		// An existing MEMORY.md is kept, as every existing file is.
		if _, err := createNew(index, indexText(group)); err != nil {
			return res, err
		}
	}
	return res, nil
}

// indexText is one project's MEMORY.md: a "- [name](file) - description" line
// per restored file, sorted by file name.
func indexText(files []restoreFile) string {
	slices.SortFunc(files, func(a, b restoreFile) int { return strings.Compare(a.path, b.path) })
	var b strings.Builder
	for _, f := range files {
		fmt.Fprintf(&b, "- [%s](%s) - %s\n", f.name, filepath.Base(f.path), f.description)
	}
	return b.String()
}

// safeRestorePath is the file a row restores to under into. A project or file
// name that is not one plain path element, or that resolves outside into, is refused.
func safeRestorePath(into, slug, file string) (string, error) {
	if !plainName(slug) || !plainName(file) {
		return "", errUnsafeName
	}
	path := filepath.Join(into, slug, "memory", file)
	if !pathguard.Within(path, into) {
		return "", errUnsafeName
	}
	return path, nil
}

// plainName reports whether s is one path element: not empty, not . or .., and without separators.
func plainName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\`)
}

// createNew writes data to path only when nothing exists there. O_EXCL makes
// the create atomic, so an existing file is never truncated or replaced. The
// mode is 0600. It reports false, and writes nothing, when the path exists.
func createNew(path, data string) (bool, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := f.WriteString(data); err != nil {
		_ = f.Close()
		return false, err
	}
	return true, f.Close()
}

// renderMemoryFile is one Claude Code memory file: the frontmatter Discover
// reads, then the body.
func renderMemoryFile(name, description, kind, body string) string {
	return "---\nname: " + frontmatterValue(name) +
		"\ndescription: " + frontmatterValue(description) +
		"\ntype: " + kind + "\n---\n" + body + "\n"
}

// frontmatterValue returns v so that SplitFrontmatter and unquote read it back
// unchanged. unquote strips one pair of surrounding quotes and nothing else, so
// a value that unquote would alter, or that has outer space, is wrapped in one
// more pair of single quotes.
func frontmatterValue(v string) string {
	if unquote(v) != v || strings.TrimSpace(v) != v {
		return "'" + v + "'"
	}
	return v
}

// storedBody is a row's body: the content after its first line, the
// "[kind] name - description" header that rowFor builds.
func storedBody(content string) string {
	_, body, _ := strings.Cut(content, "\n")
	return body
}

// realPath resolves the symlinks of the longest existing prefix of p and appends
// the rest, so a target that does not exist yet still compares with resolved paths.
func realPath(p string) string {
	rest := ""
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}
