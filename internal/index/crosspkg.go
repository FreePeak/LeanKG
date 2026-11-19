package index

import (
	"context"
	"go/token"
	"maps"
	"slices"
	"strings"

	"github.com/FreePeak/LeanKG/internal/gitobj"
)

// Cross-package resolution of Go name seeds (goast.go). A method seed x.Name is
// resolved by the static type of x, which the AST tier reads from the syntax
// (goast.go recvOf); the type decides the target:
//   - qualified `alias.Name`: the import is mapped to a directory of the run by
//     longest suffix match of its path (no go.mod parsing), then Name is looked
//     up among that directory's exported non-method elements, confidence 0.8;
//   - typed receiver of a module type T: the method T.Name, at 0.4 (method-by-name).
//     When T has no such method but is an interface of the module that declares
//     Name, the module's methods named Name are its implementers, at 0.3
//     (interface-dispatch). A stdlib or external type links to nothing;
//   - unknown receiver: a call to a method name that exactly one non-test method
//     of the module carries links at 0.3 (unique-name). A stdlib-common name, a
//     name that is a struct field of the caller's package, or a name with a
//     capped lookup links to nothing. A value (not a call) with an unknown
//     receiver links to nothing.
//
// Interface-dispatch implementers: at most maxDispatchCandidates of them, test
// files only from test files. A stdlib-common method name dispatches only through
// an interface of the caller's own package.
//
// Element and declaration data: directories re-extracted this run are read from a
// pre-pass over their Go files (crossIndex.byName and crossIndex.decls), so a
// target declared later in the run is already known. Directories this run does
// not touch are read from the store (Store.FindExact, filtered to the directories
// not re-extracted); their interface method sets come from the KV record their
// last extraction wrote. Struct fields and constructors are read only for the
// caller's own package, which is always re-extracted with the caller.
//
// Documented ceilings:
//   - FindExact matches case-insensitively and returns at most findExactLimit
//     rows, so an incremental run can miss a target whose name has more matches
//     than that. Such a name is marked capped and gets no dispatch or unique-name
//     edges.
//   - A constructor in another package (x := pkg.NewT()) is not typed: the
//     receiver is unknown and falls back to the unique-name rule.
//   - Promoted methods are followed one embedded level in the same package only.
//   - An interface's embedded interfaces are not expanded; their methods are
//     not dispatched.
//   - A renamed or removed target leaves an edge from an unchanged importer
//     pointing at a name that no longer exists. The orphan sweep in indexDir
//     removes it, because the target is no longer an element.
//   - A symbol newly added to a changed package gets no edge from importers
//     that are not re-extracted in the same run. Their outgoing edges are left
//     as they are, so the edge appears only once the importer changes.
//   - An upgrade that the time budget cuts short leaves the interface records of
//     the directories not yet re-extracted empty until the upgrade finishes.
const (
	// findExactLimit is the row cap store.FindExact applies (SQLite and PostgreSQL).
	findExactLimit = 200
	// ifaceKVNamespace holds, per directory, the newline-joined "Type.Method"
	// lines of the interfaces its Go files declare.
	ifaceKVNamespace = "go-iface-methods"

	qualifiedConf = 0.8
	methodConf    = 0.4
	dispatchConf  = 0.3

	maxDispatchCandidates = 6
)

// stdlibCommonMethods are method names so common across the standard library
// and ordinary code that a link on them by name alone would be a guess.
var stdlibCommonMethods = map[string]bool{
	"Close": true, "String": true, "Error": true, "Read": true, "Write": true,
	"Len": true, "Less": true, "Swap": true, "Get": true, "Set": true, "Add": true,
	"Run": true, "Stop": true, "Start": true, "Do": true, "Next": true, "Done": true,
	"Err": true, "Name": true, "Type": true, "Value": true, "Key": true, "ID": true,
	"Init": true, "Reset": true, "Clone": true, "Copy": true, "Keys": true,
	"Values": true, "Parse": true, "Open": true, "Load": true, "Save": true,
	"Handle": true, "Call": true, "New": true, "Fatal": true, "Fatalf": true,
	"Errorf": true, "Log": true, "Logf": true, "Query": true, "Exec": true,
	"Scan": true, "Send": true, "Flush": true, "Wait": true, "Cancel": true,
	"Push": true, "Pop": true, "Apply": true, "Update": true, "Delete": true,
	"Find": true, "Put": true,
}

// methodTarget is one candidate of a method seed.
type methodTarget struct {
	qn, resolution string
	conf           float64
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
	base    map[string][]string // last path element -> candidate directories
	changed map[string]bool     // directories this run re-extracts
	partial bool                // some directory is not re-extracted
	byName  map[string][]symRef // elements of the changed directories (pre-pass)
	decls   map[string]goDecls  // directory -> its type declarations (changed: all files; others: interface sets from KV)
	all     map[string]nameHits // per name, filled by prefetch
}

// newCrossIndex reads the interface records of every directory, then pre-passes
// the Go files of every changed directory: its elements join byName, and its
// declarations replace the record. dirCands holds the candidates of every
// directory of the run; changed names the directories with a changed file.
func newCrossIndex(ctx context.Context, st Store, dirCands map[string][]candidate, changed map[string]bool, rd *gitobj.Reader) (*crossIndex, error) {
	cx := &crossIndex{
		base:    map[string][]string{},
		changed: changed,
		partial: len(changed) < len(dirCands),
		byName:  map[string][]symRef{},
		decls:   map[string]goDecls{},
		all:     map[string]nameHits{},
	}
	if len(changed) == 0 {
		return cx, nil
	}
	dirs := slices.Sorted(maps.Keys(dirCands))
	stored := map[string]string{}
	for _, d := range dirs {
		if d != "." {
			b := baseOf(d)
			cx.base[b] = append(cx.base[b], d)
		}
		v, _, err := st.KVGet(ctx, ifaceKVNamespace, d)
		if err != nil {
			return nil, err
		}
		stored[d] = v
		cx.decls[d] = parseIfaceLines(v)
	}
	for _, d := range dirs {
		if !changed[d] {
			continue
		}
		decls := newGoDecls()
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
			decls.merge(fe.decls)
		}
		cx.decls[d] = decls
		if joined := decls.ifaceLines(); joined != stored[d] {
			if err := st.KVSet(ctx, ifaceKVNamespace, d, joined); err != nil {
				return nil, err
			}
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

// methodTargets resolves one method seed of a file: the typed receiver's method
// or dispatch targets, or the unique-name target of an unknown receiver. A nil
// index (a caller without cross-package data) resolves nothing.
func (cx *crossIndex) methodTargets(file string, s goSeed) []methodTarget {
	if cx == nil {
		return nil
	}
	dir := dirOf(file)
	if t, ok := cx.receiverType(dir, s.Recv); ok {
		return cx.typedTargets(file, dir, t, s.Name)
	}
	if !s.Call {
		return nil
	}
	return cx.uniqueTargets(dir, s.Name)
}

// receiverType is the static type of a receiver, as far as the syntax and the
// caller's package declare it. dir is the caller's directory.
func (cx *crossIndex) receiverType(dir string, r recvRef) (typeRef, bool) {
	decls := cx.decls[dir]
	switch {
	case r.Ctor != "":
		t, ok := decls.ctors[r.Ctor]
		return t, ok
	case r.Field != "":
		if r.Type.Path != "" {
			return typeRef{}, false
		}
		t, ok := decls.structs[r.Type.Name].fields[r.Field]
		return t, ok
	case r.Type.Name != "":
		return r.Type, true
	}
	return typeRef{}, false
}

// typeDir is the directory of the package a type is declared in, or "" for a
// package outside the run. dir is the directory the reference is written in.
func (cx *crossIndex) typeDir(t typeRef, dir string) string {
	if t.Path == "" {
		return dir
	}
	return cx.dirForImport(t.Path)
}

// typedTargets resolves name on a receiver of known type t, written in file of
// directory dir.
func (cx *crossIndex) typedTargets(file, dir string, t typeRef, name string) []methodTarget {
	tdir := cx.typeDir(t, dir)
	if tdir == "" {
		return nil
	}
	if qn, ok := cx.methodOf(tdir, t.Name, name); ok {
		return []methodTarget{{qn: qn, conf: methodConf, resolution: "method-by-name"}}
	}
	decls := cx.decls[tdir]
	if slices.Contains(decls.ifaces[t.Name], name) {
		if stdlibCommonMethods[name] && tdir != dir {
			return nil
		}
		var out []methodTarget
		for _, qn := range cx.implementers(name, file) {
			out = append(out, methodTarget{qn: qn, conf: dispatchConf, resolution: "interface-dispatch"})
		}
		return out
	}
	for _, e := range decls.structs[t.Name].embedded {
		if qn, ok := cx.methodOf(cx.typeDir(e, tdir), e.Name, name); ok {
			return []methodTarget{{qn: qn, conf: methodConf, resolution: "method-by-name"}}
		}
	}
	return nil
}

// methodOf is the method owner.name declared in dir, if the module has one.
func (cx *crossIndex) methodOf(dir, owner, name string) (string, bool) {
	for _, r := range cx.all[name].refs {
		if r.etype == "method" && dirOf(r.rel) == dir && ownerOf(r.qn) == owner {
			return r.qn, true
		}
	}
	return "", false
}

// implementers are the module's methods named name that a call through an
// interface may reach, or nil when the call is not linked (see the package
// comment).
func (cx *crossIndex) implementers(name, file string) []string {
	hits := cx.all[name]
	callerTest := isTestFile(file)
	var out []string
	for _, r := range hits.refs {
		if r.etype != "method" || (!callerTest && isTestFile(r.rel)) {
			continue
		}
		out = append(out, r.qn)
	}
	if hits.capped || len(out) == 0 || len(out) > maxDispatchCandidates {
		return nil
	}
	return out
}

// uniqueTargets links a call on an unknown receiver to the one non-test method
// of the module named name, when the name is not stdlib-common, not a struct
// field of the caller's package, and not capped.
func (cx *crossIndex) uniqueTargets(dir, name string) []methodTarget {
	if stdlibCommonMethods[name] || slices.Contains(cx.decls[dir].fieldNames, name) {
		return nil
	}
	hits := cx.all[name]
	if hits.capped {
		return nil
	}
	var found []string
	for _, r := range hits.refs {
		if r.etype == "method" && !isTestFile(r.rel) {
			found = append(found, r.qn)
		}
	}
	if len(found) != 1 {
		return nil
	}
	return []methodTarget{{qn: found[0], conf: dispatchConf, resolution: "unique-name"}}
}

// ownerOf is the owning type of an element's qualified name ("file.go::T.M" -> "T").
func ownerOf(qn string) string {
	_, after, _ := strings.Cut(qn, "::")
	owner, _, _ := strings.Cut(after, ".")
	return owner
}

func isTestFile(rel string) bool { return strings.HasSuffix(rel, "_test.go") }

// ifaceLines encodes the interface method sets as sorted "Type.Method" lines, the
// form a directory's KV record keeps.
func (d goDecls) ifaceLines() string {
	var lines []string
	for t, ms := range d.ifaces {
		for _, m := range ms {
			lines = append(lines, t+"."+m)
		}
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

// parseIfaceLines reads a directory's KV record back into its interface sets.
func parseIfaceLines(v string) goDecls {
	d := newGoDecls()
	if v == "" {
		return d
	}
	for _, line := range strings.Split(v, "\n") {
		if t, m, ok := strings.Cut(line, "."); ok {
			d.ifaces[t] = append(d.ifaces[t], m)
		}
	}
	return d
}
