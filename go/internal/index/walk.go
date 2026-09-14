package index

import (
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/langs"
)

// SupportedFiles walks dir with the same discovery rules as the indexer
// (skipDirs + dot-directory skip, extension gate through the registry's
// lazy activation, 1 MiB size cap) and returns the project-root-relative
// slash paths of exactly the files `leankg index` would take. doctor
// --deep uses it as the disk side of the index-freshness comparison, so
// the two must never drift — this shares the indexer's own tables.
func SupportedFiles(dir string, reg *langs.Registry) ([]string, error) {
	if reg == nil {
		reg = langs.DefaultRegistry()
	}
	owner := func(ext string) (string, bool) {
		l, ok := reg.ExtOwner(ext)
		return string(l), ok
	}
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
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
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if _, ok := owner(filepath.Ext(name)); !ok {
			// Keep this gate in lockstep with indexDir: a specialist-claimed
			// file must be visible here too, or doctor --deep reports drift.
			if !SpecialistClaim(rel) {
				return nil
			}
		}
		if info, ierr := d.Info(); ierr == nil && info.Size() > maxIndexFileBytes {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	return out, err
}
