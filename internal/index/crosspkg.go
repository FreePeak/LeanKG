package index

import (
	"context"
	"go/token"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/FreePeak/LeanKG/internal/gitobj"
)

// Cross-package resolution of Go name seeds (goast.go). A method seed x.Name is
// resolved by the static type of x, which the AST tier reads from the syntax
// (goast.go recvOf and callRef); the type decides the target:
//   - qualified `alias.Name`: the import is mapped to a directory of the run by
//     longest suffix match of its path (no go.mod parsing), then Name is looked
//     up among that directory's exported non-method elements, confidence 0.8;
//   - typed receiver of a module type T: the method T.Name, at 0.4 (method-by-name).
//     When T has no such method but is an interface of the module that declares
//     Name, the interface's own spec of Name (the method element it declares or
//     embeds, at 0.3, "interface-spec") and the module's methods named Name are
//     its implementers (at 0.3, "interface-dispatch"). An implementer must have every method of the
//     interface (its own and its embedded ones, as far as the run knows them).
//     A stdlib or external type links to nothing;
//   - unknown receiver: no edge. The unique-name rule (one module method of the
//     name) was measured at 0.80 to 0.90 on seeded hand checks, below the 0.85
//     target, and was dropped.
//
// A receiver's type comes from its declaration or binding (a parameter, a
// receiver, a var, a composite literal, a type assertion), one struct-field
// level, or the result of a call: a same-package function, or a function of an
// imported package in the run (read from its declarations, or from the KV record
// of an unchanged package). A result of a function of a package outside the run
// is an external type and links to nothing.
//
// Interface-dispatch implementers: at most maxDispatchCandidates of them, test
// files only from test files, and a full method set (the types of the run carry
// their method names, so a partial test double is not an implementer). A
// stdlib-common method name dispatches only through an interface of the caller's
// own package.
//
// Element and declaration data: directories re-extracted this run are read from a
// pre-pass over their Go files (crossIndex.byName and crossIndex.decls), so a
// target declared later in the run is already known. Directories this run does
// not touch are read from the store (Store.FindExact, filtered to the directories
// not re-extracted); their declarations (interfaces, method sets, constructor
// results) come from the KV record their last extraction wrote. Struct fields
// are read only for the caller's own package, which is always re-extracted with
// the caller.
//
// Documented ceilings:
//   - FindExact matches case-insensitively and returns at most findExactLimit
//     rows, so an incremental run can miss a target whose name has more matches
//     than that. Such a name is marked capped and gets no dispatch edges.
//   - Promoted methods are followed one embedded level in the same package only,
//     and a type whose methods are promoted is not an implementer of an interface
//     it satisfies that way.
//   - An interface's embedded interfaces are followed to a fixed depth
//     (maxEmbedDepth); an embedded interface outside the run is not expanded.
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
	// declKVNamespace holds, per directory, the newline-joined lines that
	// declLines writes: the interfaces, method sets and constructor results of its Go types.
	declKVNamespace = "go-iface-methods"

	qualifiedConf = 0.8
	methodConf    = 0.4
	dispatchConf  = 0.3

	maxDispatchCandidates = 6
	maxEmbedDepth         = 4
)

// stdlibCommonMethods are method names so common across the standard library
// and ordinary code that an interface-dispatch link on them would be a guess.
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
		v, _, err := st.KVGet(ctx, declKVNamespace, d)
		if err != nil {
			return nil, err
		}
		stored[d] = v
		cx.decls[d] = parseDeclLines(v)
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
		if joined := decls.declLines(); joined != stored[d] {
			if err := st.KVSet(ctx, declKVNamespace, d, joined); err != nil {
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
// or dispatch targets; an unknown receiver has none. A nil
// index (a caller without cross-package data) resolves nothing.
func (cx *crossIndex) methodTargets(file string, s goSeed) []methodTarget {
	if cx == nil {
		return nil
	}
	dir := dirOf(file)
	if t, ok := cx.receiverType(dir, s.Recv); ok {
		return cx.typedTargets(file, dir, t, s.Name)
	}
	return nil
}

// receiverType is the static type of a receiver, as far as the syntax and the
// caller's package declare it. dir is the caller's directory.
func (cx *crossIndex) receiverType(dir string, r recvRef) (typeRef, bool) {
	decls := cx.decls[dir]
	switch {
	case r.Qual != "":
		// A function of an imported package: external when the package is not in
		// the run; otherwise its result, whose unqualified type is in that package.
		dir := cx.dirForImport(r.Qual)
		if dir == "" {
			return typeRef{Path: r.Qual, Name: r.Ctor}, true
		}
		t, ok := cx.resultOf(dir, r.Ctor, r.Index)
		if t.Path == "" {
			t.Path = r.Qual
		}
		return t, ok
	case r.Ctor != "":
		return cx.resultOf(dir, r.Ctor, r.Index)
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

// resultOf is result index of function fn declared in dir, when its type is a
// type name.
func (cx *crossIndex) resultOf(dir, fn string, index int) (typeRef, bool) {
	results := cx.decls[dir].ctors[fn]
	if index >= len(results) || results[index].Name == "" {
		return typeRef{}, false
	}
	return results[index], true
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
	// An interface is checked first: its declared method specs are method
	// elements owned by it, so methodOf would otherwise return a spec as if it
	// were a concrete method and drop the implementers.
	if slices.Contains(cx.ifaceMethods(tdir, t.Name, 0), name) {
		return cx.ifaceTargets(file, dir, tdir, t.Name, name)
	}
	if qn, ok := cx.methodOf(tdir, t.Name, name); ok {
		return []methodTarget{{qn: qn, conf: methodConf, resolution: "method-by-name"}}
	}
	for _, e := range cx.decls[tdir].structs[t.Name].embedded {
		if qn, ok := cx.methodOf(cx.typeDir(e, tdir), e.Name, name); ok {
			return []methodTarget{{qn: qn, conf: methodConf, resolution: "method-by-name"}}
		}
	}
	return nil
}

// ifaceTargets resolves name on a receiver of interface type iface of tdir, written
// in file of directory dir: the spec the interface declares (or embeds), and the
// implementers the run holds for it. Both are links at 0.3.
func (cx *crossIndex) ifaceTargets(file, dir, tdir, iface, name string) []methodTarget {
	// An inline literal is local to its file but has no declared name, so it
	// takes no same-package exemption for a stdlib-common method name.
	if stdlibCommonMethods[name] && (tdir != dir || isIfaceLiteral(iface)) {
		return nil
	}
	var out []methodTarget
	if qn, ok := cx.specOf(tdir, iface, name, 0); ok {
		out = append(out, methodTarget{qn: qn, conf: dispatchConf, resolution: "interface-spec"})
	}
	for _, qn := range cx.implementers(name, file, tdir, iface) {
		out = append(out, methodTarget{qn: qn, conf: dispatchConf, resolution: "interface-dispatch"})
	}
	return out
}

// specOf is the spec element of method name that interface iface of dir declares,
// or that an interface it embeds declares (followed to maxEmbedDepth, as
// ifaceMethods follows embeds).
func (cx *crossIndex) specOf(dir, iface, name string, depth int) (string, bool) {
	decl, ok := cx.decls[dir].ifaces[iface]
	if !ok || depth > maxEmbedDepth {
		return "", false
	}
	if slices.Contains(decl.methods, name) {
		return cx.methodOf(dir, iface, name)
	}
	for _, e := range decl.embeds {
		if qn, ok := cx.specOf(cx.typeDir(e, dir), e.Name, name, depth+1); ok {
			return qn, true
		}
	}
	return "", false
}

// ifaceMethods is the method set of interface name of dir as far as the run knows
// it: its own methods and those of the interfaces it embeds.
func (cx *crossIndex) ifaceMethods(dir, name string, depth int) []string {
	iface, ok := cx.decls[dir].ifaces[name]
	if !ok || depth > maxEmbedDepth {
		return nil
	}
	out := slices.Clone(iface.methods)
	for _, e := range iface.embeds {
		out = append(out, cx.ifaceMethods(cx.typeDir(e, dir), e.Name, depth+1)...)
	}
	return out
}

// hasMethods reports whether type owner of dir declares every method in need.
func (cx *crossIndex) hasMethods(dir, owner string, need []string) bool {
	have := cx.decls[dir].methods[owner]
	for _, m := range need {
		if !slices.Contains(have, m) {
			return false
		}
	}
	return true
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

// implementers are the module's methods named name whose type has the whole
// method set of interface iface (of dir) that a call through it may reach, or nil
// when the call is not linked (see the package comment).
func (cx *crossIndex) implementers(name, file, dir, iface string) []string {
	hits := cx.all[name]
	need := cx.ifaceMethods(dir, iface, 0)
	callerTest := isTestFile(file)
	var out []string
	for _, r := range hits.refs {
		if r.etype != "method" || (!callerTest && isTestFile(r.rel)) || !cx.hasMethods(dirOf(r.rel), ownerOf(r.qn), need) {
			continue
		}
		out = append(out, r.qn)
	}
	if hits.capped || len(out) == 0 || len(out) > maxDispatchCandidates {
		return nil
	}
	return out
}

// ownerOf is the owning type of an element's qualified name ("file.go::T.M" -> "T").
func ownerOf(qn string) string {
	_, after, _ := strings.Cut(qn, "::")
	owner, _, _ := strings.Cut(after, ".")
	return owner
}

func isTestFile(rel string) bool { return strings.HasSuffix(rel, "_test.go") }

// declLines encodes the declarations receiver typing reads as sorted tab-separated
// lines, the form a directory's KV record keeps: "Type\tm\tMethod" for an
// interface method, "Type\te\tImportPath\tName" for an embedded interface (an
// empty path for the package's own), "Type\tk\tMethod" for a method of a concrete
// type and "Func\tr\tIndex\tImportPath\tName" for a named result of a function.
func (d goDecls) declLines() string {
	var lines []string
	for t, iface := range d.ifaces {
		for _, m := range iface.methods {
			lines = append(lines, strings.Join([]string{t, "m", m}, "\t"))
		}
		for _, e := range iface.embeds {
			lines = append(lines, strings.Join([]string{t, "e", e.Path, e.Name}, "\t"))
		}
	}
	for t, names := range d.methods {
		for _, m := range names {
			lines = append(lines, strings.Join([]string{t, "k", m}, "\t"))
		}
	}
	for fn, results := range d.ctors {
		for i, r := range results {
			if r.Name != "" {
				lines = append(lines, strings.Join([]string{fn, "r", strconv.Itoa(i), r.Path, r.Name}, "\t"))
			}
		}
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

// parseDeclLines reads a directory's KV record back into its declarations.
func parseDeclLines(v string) goDecls {
	d := newGoDecls()
	if v == "" {
		return d
	}
	for _, line := range strings.Split(v, "\n") {
		f := strings.Split(line, "\t")
		switch {
		case len(f) == 3 && f[1] == "m":
			i := d.ifaces[f[0]]
			i.methods = append(i.methods, f[2])
			d.ifaces[f[0]] = i
		case len(f) == 4 && f[1] == "e":
			i := d.ifaces[f[0]]
			i.embeds = append(i.embeds, typeRef{Path: f[2], Name: f[3]})
			d.ifaces[f[0]] = i
		case len(f) == 3 && f[1] == "k":
			d.methods[f[0]] = append(d.methods[f[0]], f[2])
		case len(f) == 5 && f[1] == "r":
			idx, err := strconv.Atoi(f[2])
			if err != nil || idx < 0 {
				continue
			}
			for len(d.ctors[f[0]]) <= idx {
				d.ctors[f[0]] = append(d.ctors[f[0]], typeRef{})
			}
			d.ctors[f[0]][idx] = typeRef{Path: f[3], Name: f[4]}
		}
	}
	return d
}
