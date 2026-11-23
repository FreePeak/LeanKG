package index

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/FreePeak/LeanKG/internal/gitobj"
)

// Go module boundaries. An import path belongs to a module when it equals the
// module's path or starts with that path followed by "/"; the longest such path
// wins, so a nested module takes its own tree from its parent. The rest of the
// path is a directory under the module's directory, and it is linked only when
// that directory is an indexed package the same module owns (a nested module's
// tree is not the parent's). A local replace maps its module path to the
// replaced directory.
//
// The table comes from the go.mod files that cover the run's Go package
// directories: each directory's own go.mod and its ancestors' up to the root.
// go.mod is not an indexed file, so it is read from disk, or in git-snapshot
// mode from its HEAD blob through the run's blob reader. go.work needs no
// reading, since every module it uses has its own go.mod.
//
// When no go.mod covers a Go package, the table is nil and crosspkg.go resolves
// imports by longest suffix instead (the fallback for fixtures without modules).

const goModFile = "go.mod"

// goModule is one module of the run: its path and the module-relative directory
// it covers.
type goModule struct {
	path, dir string
}

// goModules is the module table of a run. nil means no go.mod covers a Go
// package, so imports resolve by longest suffix.
type goModules []goModule

// loadGoModules reads the go.mod files that cover the directories pkgDirs and
// returns their modules, or nil when there are none. blobs and rd are set in
// git-snapshot mode; otherwise files are read from root on disk.
func loadGoModules(root string, pkgDirs map[string]bool, blobs []gitobj.Blob, rd *gitobj.Reader) (goModules, error) {
	oids := make(map[string]string, len(blobs))
	for _, b := range blobs {
		oids[b.Path] = b.OID
	}
	seen := map[string]bool{}
	var mods goModules
	for _, d := range slices.Sorted(maps.Keys(pkgDirs)) {
		for {
			rel := path.Join(d, goModFile)
			if !seen[rel] {
				seen[rel] = true
				src, found, err := readGoModFile(root, rel, oids, rd)
				if err != nil {
					return nil, err
				}
				if found {
					mods = append(mods, parseGoMod(src, d)...)
				}
			}
			if d == "." {
				break
			}
			d = path.Dir(d)
		}
	}
	return mods, nil
}

// readGoModFile reads the go.mod at the repo-relative path rel. found is false
// when the file does not exist.
func readGoModFile(root, rel string, oids map[string]string, rd *gitobj.Reader) (src []byte, found bool, err error) {
	if rd != nil {
		oid, ok := oids[rel]
		if !ok {
			return nil, false, nil
		}
		src, err = rd.Read(oid)
		return src, err == nil, err
	}
	src, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return src, err == nil, err
}

// parseGoMod returns the module of a go.mod file in modDir (its module line) and
// the local replaces it declares. Other directives are ignored.
func parseGoMod(src []byte, modDir string) goModules {
	var mods goModules
	block := "" // the verb of the open "verb ( ... )" block
	for line := range strings.SplitSeq(string(src), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		f := goModFields(line)
		if len(f) == 0 {
			continue
		}
		if block != "" {
			if f[0] == ")" {
				block = ""
				continue
			}
			f = append([]string{block}, f...)
		} else if len(f) == 2 && f[1] == "(" {
			block = f[0]
			continue
		}
		switch {
		case f[0] == "module" && len(f) >= 2:
			mods = append(mods, goModule{path: f[1], dir: modDir})
		case f[0] == "replace":
			if m, ok := localReplace(f[1:], modDir); ok {
				mods = append(mods, m)
			}
		}
	}
	return mods
}

// goModFields splits a go.mod line into tokens, unquoting the quoted ones.
func goModFields(line string) []string {
	f := strings.Fields(line)
	for i, t := range f {
		if u, err := strconv.Unquote(t); err == nil {
			f[i] = u
		}
	}
	return f
}

// localReplace reads the arguments of a replace directive ("old [version] =>
// target [version]") and returns the module it maps to a directory of the
// repository. A target that is not a relative path names a module version and
// is not local; a target outside the indexed root is not indexed either.
func localReplace(args []string, modDir string) (goModule, bool) {
	at := slices.Index(args, "=>")
	if at < 1 || at+1 >= len(args) {
		return goModule{}, false
	}
	target := args[at+1]
	if !strings.HasPrefix(target, "./") && !strings.HasPrefix(target, "../") {
		return goModule{}, false
	}
	dir := path.Join(modDir, target)
	if dir == ".." || strings.HasPrefix(dir, "../") {
		return goModule{}, false
	}
	return goModule{path: args[0], dir: dir}, true
}

// resolve maps an import path to the directory of a package of the run that the
// module table places it in, or "" when it places none. known is the set of the
// run's package directories.
func (ms goModules) resolve(imp string, known map[string]bool) string {
	var best *goModule
	for i := range ms {
		m := &ms[i]
		if (imp == m.path || strings.HasPrefix(imp, m.path+"/")) && (best == nil || len(m.path) > len(best.path)) {
			best = m
		}
	}
	if best == nil {
		return ""
	}
	dir := path.Join(best.dir, strings.TrimPrefix(strings.TrimPrefix(imp, best.path), "/"))
	if !known[dir] || ms.owner(dir) != best.dir {
		return ""
	}
	return dir
}

// owner is the directory of the module that owns dir: the longest module
// directory that holds it.
func (ms goModules) owner(dir string) string {
	owner := ""
	for _, m := range ms {
		if holdsDir(m.dir, dir) && len(m.dir) > len(owner) {
			owner = m.dir
		}
	}
	return owner
}

// holdsDir reports whether dir is root or sits below it, with "." holding every
// directory.
func holdsDir(root, dir string) bool {
	return root == "." || dir == root || strings.HasPrefix(dir, root+"/")
}
