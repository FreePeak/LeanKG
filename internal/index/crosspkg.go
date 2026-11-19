package index

import (
	"context"
	"go/token"
	"maps"
	"slices"
	"strings"

	"github.com/FreePeak/LeanKG/internal/gitobj"
)

// Cross-package resolution of Go name seeds (goast.go), resolved by name:
//   - qualified `alias.Name`: the import is mapped to a directory of the run by
//     longest suffix match of its path (no go.mod parsing), then Name is looked
//     up among that directory's exported non-method elements, confidence 0.8;
//   - method `x.Name`: same-package methods named Name, else methods of the
//     directories the file imports, confidence 0.4 (method-by-name);
//   - interface dispatch: when Name is a method some interface in the module
//     declares, the module's methods named Name link at 0.3, but only when there
//     are at most maxDispatchCandidates of them, a stdlib-common name links only
//     with an interface of that name in the caller's own package, and test-file
//     implementers link only from test files.
//
// Element data: directories re-extracted this run are read from a pre-pass over
// their Go files (crossIndex.byName), so a target declared later in the run is
// already known. Directories this run does not touch are read from the store
// (Store.FindExact, filtered to the directories not re-extracted).
//
// Documented ceilings:
//   - FindExact matches case-insensitively and returns at most findExactLimit
//     rows, so an incremental run can miss a target whose name has more matches
//     than that. Such a name is marked capped and gets no dispatch edges.
//   - Interface method names of an unchanged directory are read from the KV
//     record its last extraction wrote. A directory deleted from the tree keeps
//     its record, so its interface names stay in the module-wide set.
//   - A renamed or removed target leaves an edge from an unchanged importer
//     pointing at a name that no longer exists. The orphan sweep in indexDir
//     removes it, because the target is no longer an element.
//   - A symbol newly added to a changed package gets no edge from importers
//     that are not re-extracted in the same run. Their outgoing edges are left
//     as they are, so the edge appears only once the importer changes.
//   - An upgrade that the time budget cuts short leaves the interface-name
//     records of the directories not yet re-extracted empty until the upgrade
//     finishes.
const (
	// findExactLimit is the row cap store.FindExact applies (SQLite and PostgreSQL).
	findExactLimit = 200
	// ifaceKVNamespace holds, per directory, the newline-joined interface method
	// names its Go files declare.
	ifaceKVNamespace = "go-iface-methods"

	qualifiedConf = 0.8
	methodConf    = 0.4
	dispatchConf  = 0.3

	maxDispatchCandidates = 6
)

// stdlibCommonMethods are method names so common across the standard library
// and ordinary code that an interface-dispatch link on them would be a guess.
var stdlibCommonMethods = map[string]bool{
	"Close": true, "String": true, "Error": true, "Read": true, "Write": true,
	"Len": true, "Less": true, "Swap": true, "Get": true, "Set": true, "Add": true,
	"Run": true, "Stop": true, "Start": true, "Do": true, "Next": true, "Done": true,
	"Err": true,
}

// symRef is one Go element of the run, as cross-package resolution sees it.
type symRef struct {
	rel, qn, etype string
}

// nameHits is every Go element of one name across the module.
type nameHits struct {
	refs   []symRef
	capped bool // a store lookup hit findExactLimit, so refs may be incomplete
}

// crossIndex is the module-wide view one index run resolves cross-package seeds
// against. Its maps are filled before the directories are resolved.
type crossIndex struct {
	base     map[string][]string // last path element -> candidate directories
	changed  map[string]bool     // directories this run re-extracts
	partial  bool                // some directory is not re-extracted
	byName   map[string][]symRef // elements of the changed directories (pre-pass)
	ifaces   map[string][]string // directory -> interface method names it declares
	ifaceAll map[string]bool     // union of ifaces
	all      map[string]nameHits // per name, filled by prefetch
}

// newCrossIndex reads the interface records of every directory, then pre-passes
// the Go files of every changed directory: its elements join byName and its
// interface names are recorded. dirCands holds the candidates of every
// directory of the run; changed names the directories with a changed file.
func newCrossIndex(ctx context.Context, st Store, dirCands map[string][]candidate, changed map[string]bool, rd *gitobj.Reader) (*crossIndex, error) {
	cx := &crossIndex{
		base:     map[string][]string{},
		changed:  changed,
		partial:  len(changed) < len(dirCands),
		byName:   map[string][]symRef{},
		ifaces:   map[string][]string{},
		ifaceAll: map[string]bool{},
		all:      map[string]nameHits{},
	}
	if len(changed) == 0 {
		return cx, nil
	}
	dirs := slices.Sorted(maps.Keys(dirCands))
	for _, d := range dirs {
		if d != "." {
			b := baseOf(d)
			cx.base[b] = append(cx.base[b], d)
		}
		v, _, err := st.KVGet(ctx, ifaceKVNamespace, d)
		if err != nil {
			return nil, err
		}
		if v != "" {
			cx.ifaces[d] = strings.Split(v, "\n")
		}
	}
	for _, d := range dirs {
		if !changed[d] {
			continue
		}
		var names []string
		for _, c := range dirCands[d] {
			if c.lang != "go" || SpecialistClaim(c.rel) {
				continue
			}
			src, err := c.content(rd)
			if err != nil {
				return nil, err
			}
			fe, err := extractFileAs(ctx, c.rel, src, c.lang)
			if err != nil {
				return nil, err
			}
			for _, e := range fe.elements {
				cx.byName[e.name] = append(cx.byName[e.name], symRef{rel: c.rel, qn: e.qn, etype: e.etype})
			}
			names = append(names, fe.ifaces...)
		}
		slices.Sort(names)
		names = slices.Compact(names)
		if joined := strings.Join(names, "\n"); joined != strings.Join(cx.ifaces[d], "\n") {
			if err := st.KVSet(ctx, ifaceKVNamespace, d, joined); err != nil {
				return nil, err
			}
		}
		cx.ifaces[d] = names
	}
	for _, names := range cx.ifaces {
		for _, n := range names {
			cx.ifaceAll[n] = true
		}
	}
	return cx, nil
}

// prefetch fills cx.all for every qualified and method seed name of els. Only
// the store is queried, and only when some directory of the run is not
// re-extracted; a run that re-extracts everything answers from byName alone.
func (cx *crossIndex) prefetch(ctx context.Context, st Store, els []indexedElem) error {
	for _, e := range els {
		for _, s := range e.seeds {
			if s.Kind == "ident" {
				continue
			}
			if _, done := cx.all[s.Name]; done {
				continue
			}
			hits := nameHits{refs: slices.Clone(cx.byName[s.Name])}
			if cx.partial {
				rows, err := st.FindExact(ctx, s.Name)
				if err != nil {
					return err
				}
				for _, r := range rows {
					if r.Name != s.Name || r.Language != "go" || cx.changed[dirOf(r.FilePath)] {
						continue
					}
					hits.refs = append(hits.refs, symRef{rel: r.FilePath, qn: r.QualifiedName, etype: r.ElementType})
				}
				hits.capped = len(rows) >= findExactLimit
			}
			cx.all[s.Name] = hits
		}
	}
	return nil
}

// importDirs maps each import name of a file to the directory of the run that
// holds the imported package. An import with no such directory (external or the
// module root) is left out.
func (cx *crossIndex) importDirs(imports map[string]string) map[string]string {
	var out map[string]string
	for name, imp := range imports {
		d := cx.dirForImport(imp)
		if d == "" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[name] = d
	}
	return out
}

// dirForImport returns the longest candidate directory that the import path ends
// with, or "" when none does.
func (cx *crossIndex) dirForImport(imp string) string {
	best := ""
	for _, d := range cx.base[baseOf(imp)] {
		if (imp == d || strings.HasSuffix(imp, "/"+d)) && len(d) > len(best) {
			best = d
		}
	}
	return best
}

// qualifiedTargets are the exported, non-method elements named name in dir.
func (cx *crossIndex) qualifiedTargets(dir, name string) []string {
	if !token.IsExported(name) {
		return nil
	}
	var out []string
	for _, r := range cx.all[name].refs {
		if r.etype != "method" && dirOf(r.rel) == dir {
			out = append(out, r.qn)
		}
	}
	return out
}

// methodsIn are the methods named name declared in one of the directories.
func (cx *crossIndex) methodsIn(name string, dirs map[string]bool) []string {
	var out []string
	for _, r := range cx.all[name].refs {
		if r.etype == "method" && dirs[dirOf(r.rel)] {
			out = append(out, r.qn)
		}
	}
	return out
}

// dispatchTargets are the methods a call through an interface named name may
// reach, or nil when the call is not linked (see the package comment).
func (cx *crossIndex) dispatchTargets(name, file, dir string) []string {
	if !cx.ifaceAll[name] {
		return nil
	}
	if stdlibCommonMethods[name] && !slices.Contains(cx.ifaces[dir], name) {
		return nil
	}
	hits := cx.all[name]
	callerTest := strings.HasSuffix(file, "_test.go")
	var out []string
	for _, r := range hits.refs {
		if r.etype != "method" || (!callerTest && strings.HasSuffix(r.rel, "_test.go")) {
			continue
		}
		out = append(out, r.qn)
	}
	if hits.capped || len(out) == 0 || len(out) > maxDispatchCandidates {
		return nil
	}
	return out
}
