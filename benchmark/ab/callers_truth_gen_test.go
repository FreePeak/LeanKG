package ab_test

// Generator for the callers ground truth, benchmark/ab/testdata/callers_truth.json.
//
// Regenerate the truth alone (the LeanKG indexer is not involved):
//
//	LEANKG_CALLERS_TRUTH_UPDATE=1 go test ./benchmark/ab -run TestCallersTruthGenerate -count=1
//
// The scorer's rebaseline regenerates first, then measures and writes the
// floors and known_misses (one command after the product changes):
//
//	LEANKG_CALLERS_TRUTH_UPDATE=1 LEANKG_CALLERS_SCORE=1 go test ./benchmark/ab -run TestCallers -count=1 -v
//
// The generator reads the committed tree at HEAD (pinnedCorpus, git archive),
// parses every package with go/parser, type-checks it with go/types under each
// build configuration in callersBuildConfigs and applies the rules that
// callersTruthRules writes into the truth file. The truth is the union over the
// configurations. The symbols (id, qualified_name, class) and the defects come
// from the existing truth file; known_misses and floors belong to the scorer
// and are carried over unchanged.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	callersModule    = "github.com/FreePeak/LeanKG"
	callersGenerator = "benchmark/ab/callers_truth_gen_test.go (go/parser + go/types over HEAD)"
)

// callersBuildConfigs are the build configurations the corpus is checked
// under. The first one is the default build; the others make the tstree tier
// and the linux files visible, so that tagged test files count.
var callersBuildConfigs = []struct {
	goos string
	tags []string
}{
	{goos: "", tags: nil},
	{goos: "", tags: []string{"tstree"}},
	{goos: "linux", tags: nil},
}

// callersTruthRules is the rule set written into the truth file. Each rule
// is applied by the generator below; changing one means changing the code.
var callersTruthRules = []string{
	"Corpus: the tracked files of HEAD (git archive of the commit). Working-tree edits are never read. Each build configuration (default darwin; darwin with the tstree tag; linux) is checked on its own, and the truth is the union of their callers. A .go file joins a configuration when go/build matches it there; testdata/, vendor/, node_modules/ and dot or underscore directories are skipped.",
	"Packages: a directory's non-test files and its in-package _test.go files form one package; its package X_test files form a second package. Module imports resolve to these packages; standard-library imports are type-checked from source; other imports stay unresolved and their selectors make no edges.",
	"Targets: qualified_name file::F is the package-level function or type F; file::T.M is the method M declared on the concrete type T.",
	"Reference (type-reference rule included): an identifier whose go/types use resolves to the target counts wherever it sits: a call, a method or function value, a signature, a field or var type, a composite literal, a conversion or an embedded field. A type is referenced by any use of its name. The definition itself does not count.",
	"Own methods: a reference to a type from a method whose receiver is that type is not a caller of the type (TestCallersOfTypeExcludeOwnMethods, internal/graph/graph_test.go). A method target keeps the references from its own type's methods.",
	"Interface dispatch: for a method target T.M, a reference to a method M declared by an interface (named or a literal) that *T implements counts as a caller of T.M (the graph's interface-dispatch edges).",
	"Enclosing caller: the top-level declaration that holds the reference. A method is Recv.Name (receiver base type, pointer and type parameters stripped); a func, type or var spec is its name (a var spec with several names uses its first). Closures count toward their enclosing declaration.",
	"A reference inside the target's own declaration (recursion) is not a caller.",
	"One entry per (file, func). line is the lowest referencing line in that function and is informational. test is true for _test.go files.",
	"possible: a selector X.M whose name matches a target and whose receiver type does not resolve (no go/types selection, and X is not a package name) is listed per symbol. The scorer reports possible hits and counts neither true positives nor false positives for them.",
}

// truthGen type-checks the corpus under one build configuration.
type truthGen struct {
	root  string
	ctx   build.Context
	fset  *token.FileSet
	std   types.Importer
	dirs  map[string]*corpusDir // slash-form directory relative to root
	pkgs  map[string]*types.Package
	busy  map[string]bool
	infos []*checkedPkg
}

// corpusDir holds the parsed files of one directory, split by package role.
type corpusDir struct {
	name    string
	lib     []*ast.File // non-test files
	inTest  []*ast.File // _test.go files of the same package
	extTest []*ast.File // _test.go files of package name_test
}

type checkedPkg struct {
	pkg   *types.Package
	info  *types.Info
	files []*ast.File
}

// newTruthGen parses every corpus file that the build configuration matches.
func newTruthGen(t testing.TB, root string, goos string, tags []string) *truthGen {
	t.Helper()
	ctx := build.Default
	ctx.BuildTags = tags
	if goos != "" {
		ctx.GOOS = goos
	}
	g := &truthGen{root: root, ctx: ctx, fset: token.NewFileSet(),
		dirs: map[string]*corpusDir{}, pkgs: map[string]*types.Package{}, busy: map[string]bool{}}
	g.std = importer.ForCompiler(g.fset, "source", nil)

	type parsed struct {
		file   *ast.File
		isTest bool
	}
	byDir := map[string][]parsed{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if p != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
				name == "testdata" || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		ok, err := g.ctx.MatchFile(filepath.Dir(p), d.Name())
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		f, err := parser.ParseFile(g.fset, p, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
		rel, err := filepath.Rel(root, filepath.Dir(p))
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		byDir[key] = append(byDir[key], parsed{f, strings.HasSuffix(d.Name(), "_test.go")})
		return nil
	})
	if err != nil {
		t.Fatalf("walk corpus: %v", err)
	}
	for key, files := range byDir {
		cd := &corpusDir{}
		for _, p := range files {
			if !p.isTest {
				cd.name = p.file.Name.Name
				break
			}
		}
		if cd.name == "" {
			for _, p := range files {
				if !strings.HasSuffix(p.file.Name.Name, "_test") {
					cd.name = p.file.Name.Name
					break
				}
			}
		}
		for _, p := range files {
			switch {
			case !p.isTest:
				if p.file.Name.Name == cd.name {
					cd.lib = append(cd.lib, p.file)
				}
			case p.file.Name.Name == cd.name:
				cd.inTest = append(cd.inTest, p.file)
			case p.file.Name.Name == cd.name+"_test":
				cd.extTest = append(cd.extTest, p.file)
			}
		}
		g.dirs[key] = cd
	}
	g.checkAll()
	return g
}

// checkAll type-checks every module package and every external test package,
// so that all declarations and uses exist before the scan. The standard
// library is type-checked from source; cgo is disabled for that step.
func (g *truthGen) checkAll() {
	prevCgo := build.Default.CgoEnabled
	build.Default.CgoEnabled = false
	defer func() { build.Default.CgoEnabled = prevCgo }()

	keys := make([]string, 0, len(g.dirs))
	for k := range g.dirs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		g.fullPackage(k, importPathOf(k))
	}
	for _, k := range keys {
		if cd := g.dirs[k]; len(cd.extTest) > 0 {
			g.check(importPathOf(k)+"_test", cd.extTest)
		}
	}
}

// importPathOf maps a corpus directory to its module import path.
func importPathOf(rel string) string {
	if rel == "." {
		return callersModule
	}
	return callersModule + "/" + rel
}

// Import implements types.Importer for the corpus.
func (g *truthGen) Import(p string) (*types.Package, error) {
	if p == callersModule || strings.HasPrefix(p, callersModule+"/") {
		rel := "."
		if p != callersModule {
			rel = strings.TrimPrefix(p, callersModule+"/")
		}
		return g.fullPackage(rel, p), nil
	}
	if !strings.Contains(strings.SplitN(p, "/", 2)[0], ".") {
		if pkg, err := g.std.Import(p); err == nil {
			return pkg, nil
		}
	}
	return fakePackage(p), nil
}

// fullPackage type-checks a module directory once: its library files plus
// its in-package tests. A dependency cycle yields an empty package.
func (g *truthGen) fullPackage(rel, p string) *types.Package {
	if pkg := g.pkgs[p]; pkg != nil {
		return pkg
	}
	cd := g.dirs[rel]
	if cd == nil || g.busy[p] {
		return fakePackage(p)
	}
	g.busy[p] = true
	files := append(append([]*ast.File{}, cd.lib...), cd.inTest...)
	pkg := g.check(p, files)
	delete(g.busy, p)
	g.pkgs[p] = pkg
	return pkg
}

func (g *truthGen) check(p string, files []*ast.File) *types.Package {
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	conf := types.Config{Importer: g, Error: func(error) {}}
	pkg, _ := conf.Check(p, g.fset, files, info)
	g.infos = append(g.infos, &checkedPkg{pkg: pkg, info: info, files: files})
	return pkg
}

// fakePackage stands in for an import the generator cannot resolve.
func fakePackage(p string) *types.Package {
	pkg := types.NewPackage(p, path.Base(p))
	pkg.MarkComplete()
	return pkg
}

// callersTarget is one truth symbol resolved to its go/types object. A
// symbol that does not exist under a build configuration has a nil obj.
type callersTarget struct {
	obj  types.Object
	name string       // the qualified_name part after "::", e.g. "Store.AuditTail"
	file string       // the declaring file, repo-relative
	recv *types.Named // the receiver type of a method target
}

// resolve finds the object a qualified_name refers to.
func (g *truthGen) resolve(qn string) (callersTarget, error) {
	file, name, ok := strings.Cut(qn, "::")
	if !ok {
		return callersTarget{}, fmt.Errorf("qualified_name %q has no ::", qn)
	}
	dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(file)))
	pkg := g.fullPackage(dir, importPathOf(dir))
	tgt := callersTarget{name: name, file: file}
	recv, meth, isMethod := strings.Cut(name, ".")
	if !isMethod {
		tgt.obj = pkg.Scope().Lookup(name)
	} else if tn, ok := pkg.Scope().Lookup(recv).(*types.TypeName); ok {
		if named, ok := tn.Type().(*types.Named); ok {
			tgt.recv = named
			for i := 0; i < named.NumMethods(); i++ {
				if named.Method(i).Name() == meth {
					tgt.obj = named.Method(i)
				}
			}
		}
	}
	if tgt.obj == nil {
		return tgt, fmt.Errorf("%s: no object for %q", file, name)
	}
	return tgt, nil
}

// callersSite is one (file, func) caller with its lowest line.
type callersSite struct {
	line int
}

// enclosingName is the top-level declaration name of rule 7.
func enclosingName(d ast.Decl) string {
	fd, ok := d.(*ast.FuncDecl)
	if !ok {
		return ""
	}
	if base := recvBase(fd); base != nil {
		return base.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}

// recvBase is the receiver's base type identifier of a method, or nil for a
// function or a method on a non-identifier type.
func recvBase(fd *ast.FuncDecl) *ast.Ident {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return nil
	}
	t := fd.Recv.List[0].Type
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
		case *ast.ParenExpr:
			t = x.X
		case *ast.IndexExpr:
			t = x.X
		case *ast.IndexListExpr:
			t = x.X
		case *ast.Ident:
			return x
		default:
			return nil
		}
	}
}

// generateCallers computes the callers and possible lists of every symbol
// under this build configuration. With required false, a symbol missing from
// this configuration is skipped instead of failing.
func (g *truthGen) generateCallers(symbols []truthSymbol, required bool) (callers, possible []map[callerKey]int, err error) {
	targets := make([]callersTarget, len(symbols))
	direct := map[types.Object][]int{}
	byName := map[string][]int{}
	methods := map[string][]int{}
	for i, s := range symbols {
		tgt, err := g.resolve(s.QualifiedName)
		if err != nil {
			if required {
				return nil, nil, err
			}
			continue
		}
		targets[i] = tgt
		direct[tgt.obj] = append(direct[tgt.obj], i)
		short := s.ownName()
		short = short[strings.LastIndex(short, ".")+1:]
		byName[short] = append(byName[short], i)
		if tgt.recv != nil {
			methods[tgt.obj.Name()] = append(methods[tgt.obj.Name()], i)
		}
	}

	// targetsOf returns the symbols an object refers to: the target itself,
	// and for a method target the interface methods of that name that its
	// receiver type implements (interface dispatch).
	memo := map[types.Object][]int{}
	for obj, idx := range direct {
		memo[obj] = idx
	}
	targetsOf := func(obj types.Object) []int {
		if idx, ok := memo[obj]; ok {
			return idx
		}
		var out []int
		if fn, ok := obj.(*types.Func); ok {
			if sig := fn.Type().(*types.Signature); sig.Recv() != nil {
				if iface, ok := sig.Recv().Type().Underlying().(*types.Interface); ok {
					for _, si := range methods[fn.Name()] {
						if types.Implements(types.NewPointer(targets[si].recv), iface) {
							out = append(out, si)
						}
					}
				}
			}
		}
		memo[obj] = out
		return out
	}

	found := make([]map[callerKey]callersSite, len(symbols))
	maybe := make([]map[callerKey]callersSite, len(symbols))
	for i := range symbols {
		found[i] = map[callerKey]callersSite{}
		maybe[i] = map[callerKey]callersSite{}
	}
	record := func(into []map[callerKey]callersSite, sym int, file, fn string, line int) {
		k := callerKey{File: file, Func: fn}
		if cur, ok := into[sym][k]; !ok || line < cur.line {
			into[sym][k] = callersSite{line: line}
		}
	}
	// own reports whether a reference from (file, enc, recvObj) to symbol si
	// lies inside that symbol's own declaration or, for a type, inside one of
	// the type's own methods.
	own := func(si int, file, enc string, recvObj types.Object) bool {
		if file == targets[si].file && enc == symbols[si].ownName() {
			return true
		}
		_, isType := targets[si].obj.(*types.TypeName)
		return isType && recvObj != nil && recvObj == targets[si].obj
	}

	for _, c := range g.infos {
		info := c.info
		for _, f := range c.files {
			file := g.relFile(f)
			scan := func(node ast.Node, enc string, recvObj types.Object) {
				ast.Inspect(node, func(n ast.Node) bool {
					switch n := n.(type) {
					case *ast.Ident:
						obj := info.Uses[n]
						if obj == nil {
							return true
						}
						line := g.fset.Position(n.Pos()).Line
						for _, si := range targetsOf(obj) {
							if !own(si, file, enc, recvObj) {
								record(found, si, file, enc, line)
							}
						}
					case *ast.SelectorExpr:
						if !g.unresolvedSelector(info, n) {
							return true
						}
						line := g.fset.Position(n.Pos()).Line
						for _, si := range byName[n.Sel.Name] {
							if !own(si, file, enc, recvObj) {
								record(maybe, si, file, enc, line)
							}
						}
					}
					return true
				})
			}
			for _, d := range f.Decls {
				if gd, ok := d.(*ast.GenDecl); ok {
					for _, s := range gd.Specs {
						switch s := s.(type) {
						case *ast.TypeSpec:
							scan(s, s.Name.Name, nil)
						case *ast.ValueSpec:
							if len(s.Names) > 0 {
								scan(s, s.Names[0].Name, nil)
							}
						}
					}
					continue
				}
				fd, ok := d.(*ast.FuncDecl)
				if !ok {
					continue
				}
				var recvObj types.Object
				if base := recvBase(fd); base != nil {
					recvObj = info.Uses[base]
				}
				scan(fd, enclosingName(fd), recvObj)
			}
		}
	}

	callers = make([]map[callerKey]int, len(symbols))
	possible = make([]map[callerKey]int, len(symbols))
	for i := range symbols {
		callers[i] = map[callerKey]int{}
		for k, s := range found[i] {
			callers[i][k] = s.line
		}
		possible[i] = map[callerKey]int{}
		for k, s := range maybe[i] {
			if _, confirmed := callers[i][k]; !confirmed {
				possible[i][k] = s.line
			}
		}
	}
	return callers, possible, nil
}

// relFile is the repo-relative slash path of a parsed file.
func (g *truthGen) relFile(f *ast.File) string {
	name := g.fset.Position(f.Pos()).Filename
	rel, _ := filepath.Rel(g.root, name)
	return filepath.ToSlash(rel)
}

// unresolvedSelector reports whether X.M has no go/types selection and X is
// not a package name (rule: possible callers).
func (g *truthGen) unresolvedSelector(info *types.Info, sel *ast.SelectorExpr) bool {
	if _, ok := info.Selections[sel]; ok {
		return false
	}
	if id, ok := sel.X.(*ast.Ident); ok {
		if _, isPkg := info.Uses[id].(*types.PkgName); isPkg {
			return false
		}
	}
	return true
}

// ownName is the qualified_name part after "::", the enclosing name a
// symbol's own declaration would report.
func (s truthSymbol) ownName() string {
	_, name, _ := strings.Cut(s.QualifiedName, "::")
	return name
}

// generateCallersAllBuilds unions the callers and possible sites over every
// build configuration. A possible site that is a caller in any configuration
// is dropped from possible.
func generateCallersAllBuilds(t testing.TB, root string, symbols []truthSymbol) (callers, possible []map[callerKey]int) {
	t.Helper()
	callers = make([]map[callerKey]int, len(symbols))
	possible = make([]map[callerKey]int, len(symbols))
	for i := range symbols {
		callers[i] = map[callerKey]int{}
		possible[i] = map[callerKey]int{}
	}
	for n, cfg := range callersBuildConfigs {
		g := newTruthGen(t, root, cfg.goos, cfg.tags)
		c, p, err := g.generateCallers(symbols, n == 0)
		if err != nil {
			t.Fatalf("build config %d: %v", n, err)
		}
		for i := range symbols {
			mergeMinLine(callers[i], c[i])
			mergeMinLine(possible[i], p[i])
		}
	}
	for i := range symbols {
		for k := range callers[i] {
			delete(possible[i], k)
		}
	}
	return callers, possible
}

func mergeMinLine(dst, src map[callerKey]int) {
	for k, line := range src {
		if cur, ok := dst[k]; !ok || line < cur {
			dst[k] = line
		}
	}
}

// regenerateCallersTruth rewrites the truth file in place from the pinned
// corpus. It keeps known_misses and floors and returns the new truth and the
// previous one for the caller to report.
func regenerateCallersTruth(t testing.TB, truthPath string) (fresh, previous truthFile) {
	t.Helper()
	previous = loadCallersTruth(t, truthPath)
	corpus, commit := pinnedCorpus(t, "HEAD")
	callers, possible := generateCallersAllBuilds(t, corpus, previous.Symbols)

	fresh = previous
	fresh.SourceCommit = commit
	fresh.Schema = "leankg.callers_truth/v2"
	fresh.Generator = callersGenerator
	fresh.Rules = callersTruthRules
	fresh.Symbols = make([]truthSymbol, len(previous.Symbols))
	for i, s := range previous.Symbols {
		s.Callers = sortedCallers(callers[i])
		s.Possible = sortedCallers(possible[i])
		fresh.Symbols[i] = s
	}

	for _, s := range fresh.Symbols {
		for _, d := range s.Defects {
			if !containsCaller(s.Callers, d.File, d.Func) {
				t.Errorf("defect site %s:%s is not a generated caller of %s; fix the rules or the defect list", d.File, d.Func, s.ID)
			}
		}
	}
	if err := writeCallersTruth(truthPath, fresh); err != nil {
		t.Fatalf("write truth: %v", err)
	}
	return fresh, previous
}

// sortedCallers lists the callers by file and function.
func sortedCallers(m map[callerKey]int) []truthCaller {
	out := make([]truthCaller, 0, len(m))
	for k, line := range m {
		out = append(out, truthCaller{File: k.File, Func: k.Func, Test: strings.HasSuffix(k.File, "_test.go"), Line: line})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Func < out[j].Func
	})
	return out
}

func containsCaller(cs []truthCaller, file, fn string) bool {
	for _, c := range cs {
		if c.File == file && c.Func == fn {
			return true
		}
	}
	return false
}

func loadCallersTruth(t testing.TB, truthPath string) truthFile {
	t.Helper()
	raw, err := os.ReadFile(truthPath)
	if err != nil {
		t.Fatalf("read truth: %v", err)
	}
	var truth truthFile
	if err := json.Unmarshal(raw, &truth); err != nil {
		t.Fatalf("parse truth: %v", err)
	}
	return truth
}

func writeCallersTruth(truthPath string, truth truthFile) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(truth); err != nil {
		return err
	}
	return os.WriteFile(truthPath, buf.Bytes(), 0o644)
}

// TestCallersTruthGenerate regenerates the callers truth from the pinned
// corpus. It logs the entry counts and every added or removed (symbol, file,
// func) triple.
func TestCallersTruthGenerate(t *testing.T) {
	if os.Getenv("LEANKG_CALLERS_TRUTH_UPDATE") != "1" {
		t.Skip("set LEANKG_CALLERS_TRUTH_UPDATE=1 to regenerate callers_truth.json")
	}
	fresh, previous := regenerateCallersTruth(t, callersTruthPath)
	logCallersDiff(t, previous, fresh)
}

// logCallersDiff reports the entry counts and the per-symbol differences.
func logCallersDiff(t testing.TB, previous, fresh truthFile) {
	t.Helper()
	count := func(tr truthFile) (callers, possible int) {
		for _, s := range tr.Symbols {
			callers += len(s.Callers)
			possible += len(s.Possible)
		}
		return
	}
	pc, _ := count(previous)
	fc, fp := count(fresh)
	t.Logf("callers entries: previous %d, generated %d; possible %d", pc, fc, fp)

	prev := map[string]bool{}
	for _, s := range previous.Symbols {
		for _, c := range s.Callers {
			prev[s.ID+" "+c.File+" "+c.Func] = true
		}
	}
	cur := map[string]bool{}
	for _, s := range fresh.Symbols {
		for _, c := range s.Callers {
			key := s.ID + " " + c.File + " " + c.Func
			cur[key] = true
			if !prev[key] {
				t.Logf("added   %s", key)
			}
		}
	}
	for k := range prev {
		if !cur[k] {
			t.Logf("removed %s", k)
		}
	}
}
