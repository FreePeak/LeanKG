package index

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// goSeed is one name a Go element uses, read from the AST rather than from its
// text. Kinds:
//   - "ident": a bare identifier resolved in the same package (a function, type,
//     const or var; never a method, which a bare identifier cannot name);
//   - "qualified": alias.Name where alias is an import name (cross-package;
//     resolved by qualifiedEdges from the file's qualified seeds);
//   - "method": x.Name, called (Call true) or taken as a value (a call argument,
//     an assignment or a composite-literal element). Recv is what the syntax
//     says of x's type; crosspkg.go resolves the name against it.
//
// A selector that is neither called nor a value (a field read) makes no seed.
//
// Call is true for a call site (the CallExpr's function), false for a plain
// reference (a type in a signature, a composite literal's type, a value use).
type goSeed struct {
	Kind  string
	Alias string // qualified only
	Name  string
	Line  int
	Call  bool
	Recv  recvRef // method only
}

// typeRef names a Go type as the source writes it. Path is the import path of
// its package ("" for the declaring file's own package), Name the bare type name.
type typeRef struct {
	Path, Name string
}

// recvRef says what the syntax gives of a receiver's static type. The zero value
// is an unknown receiver. Otherwise exactly one field is set:
//   - Type: the receiver has this type (a parameter, a receiver, a declared
//     variable, a composite literal);
//   - Type and Field: the receiver is the field Field of a value of Type (r.field);
//   - Ctor: the receiver is the result of a call to the same-package function Ctor.
type recvRef struct {
	Type  typeRef
	Field string
	Ctor  string
}

// goDecls are the type declarations of a Go package (or one file of it) that
// receiver typing reads. Per file, they come from goDefinitions; crosspkg.go
// merges the files of a directory.
type goDecls struct {
	structs    map[string]goStruct // struct type name -> its fields and embedded types
	ifaces     map[string][]string // interface type name -> the method names it declares
	ctors      map[string]typeRef  // function name -> its first result type
	fieldNames []string            // every struct field name, embedded fields included
}

// goStruct is one struct type as receiver typing sees it.
type goStruct struct {
	fields   map[string]typeRef // named fields whose type is a (qualified) type name
	embedded []typeRef          // embedded types, whose methods are promoted
}

func newGoDecls() goDecls {
	return goDecls{structs: map[string]goStruct{}, ifaces: map[string][]string{}, ctors: map[string]typeRef{}}
}

// merge adds the declarations of another file of the same package.
func (d *goDecls) merge(o goDecls) {
	maps.Copy(d.structs, o.structs)
	maps.Copy(d.ifaces, o.ifaces)
	maps.Copy(d.ctors, o.ctors)
	d.fieldNames = append(d.fieldNames, o.fieldNames...)
}

// goDefinitions parses one Go file with the standard-library parser and
// returns its file-scope definitions, the name seeds of each definition (seeds[i]
// belongs to defs[i]), its import names (name -> import path, an unaliased
// import under its default name) and the type declarations receiver typing
// reads. It is CGO-free and used by every build. ok is false when the parser
// rejects the file; the caller then falls back to the regex tier.
//
// Ranges come from AST positions: a declaration starts on its keyword or name
// line and ends on its last token, so a comment or a string that merely looks
// like a declaration is never an element. Methods are owned by their receiver's
// bare type name ("*Set[T]" -> "Set"). Interfaces and structs are one element
// each; their members are not separate elements.
func goDefinitions(src []byte) (defs []indexDef, seeds [][]goSeed, imports map[string]string, decls goDecls, ok bool) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, nil, goDecls{}, false
	}
	imports = importPaths(f)
	w := &seedWalker{fset: fset, imports: imports}
	decls = newGoDecls()
	line := func(p token.Pos) int { return fset.Position(p).Line }
	add := func(kind, name string, start, end token.Pos, owner string, refs []goSeed) {
		if name == "_" {
			return
		}
		defs = append(defs, indexDef{Kind: kind, Name: name, StartLine: line(start), EndLine: line(end), Owner: owner})
		seeds = append(seeds, refs)
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			kind, owner := "function", ""
			if d.Recv != nil && len(d.Recv.List) > 0 {
				kind, owner = "method", bareTypeName(d.Recv.List[0].Type)
			} else if res := d.Type.Results; res != nil && len(res.List) > 0 {
				w.tparams = tparamsOf(d)
				if t, ok := w.typeOf(res.List[0].Type); ok {
					decls.ctors[d.Name.Name] = t
				}
			}
			add(kind, d.Name.Name, d.Pos(), d.End(), owner, w.seeds(d))
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, s := range d.Specs {
					ts := s.(*ast.TypeSpec)
					add("type", ts.Name.Name, ts.Pos(), ts.End(), "", w.seeds(ts))
					w.tparams = tparamsOf(ts)
					w.typeDecl(&decls, ts)
				}
			case token.CONST, token.VAR:
				kind := "variable"
				if d.Tok == token.CONST {
					kind = "constant"
				}
				for _, s := range d.Specs {
					vs := s.(*ast.ValueSpec)
					refs := w.seeds(vs)
					for _, name := range vs.Names {
						add(kind, name.Name, vs.Pos(), vs.End(), "", refs)
					}
				}
			}
		}
	}
	return defs, seeds, imports, decls, true
}

// typeDecl records what receiver typing reads from one type declaration. w.tparams
// must hold the declaration's type parameters.
func (w *seedWalker) typeDecl(decls *goDecls, ts *ast.TypeSpec) {
	switch t := ts.Type.(type) {
	case *ast.InterfaceType:
		decls.ifaces[ts.Name.Name] = interfaceMethodNames(t)
	case *ast.StructType:
		s := goStruct{fields: map[string]typeRef{}}
		for _, f := range t.Fields.List {
			ft, typed := w.typeOf(f.Type)
			if len(f.Names) == 0 {
				decls.fieldNames = append(decls.fieldNames, bareTypeName(f.Type))
				if typed {
					s.embedded = append(s.embedded, ft)
				}
				continue
			}
			for _, n := range f.Names {
				decls.fieldNames = append(decls.fieldNames, n.Name)
				if typed {
					s.fields[n.Name] = ft
				}
			}
		}
		decls.structs[ts.Name.Name] = s
	}
}

// interfaceMethodNames returns the method names an interface type declares.
// Embedded interfaces and type-set terms have no name of their own and are
// skipped.
func interfaceMethodNames(it *ast.InterfaceType) []string {
	if it.Methods == nil {
		return nil
	}
	var out []string
	for _, f := range it.Methods.List {
		for _, n := range f.Names {
			out = append(out, n.Name)
		}
	}
	return out
}

// qualifiedOf collects the qualified seeds of already-qualified elements (their
// qualified names are set), the per-file result the cross-package resolver reads.
func qualifiedOf(els []indexedElem) []qualifiedSeed {
	var out []qualifiedSeed
	for _, e := range els {
		for _, s := range e.seeds {
			if s.Kind == "qualified" {
				out = append(out, qualifiedSeed{Source: e.qn, Alias: s.Alias, Name: s.Name, Line: s.Line, Call: s.Call})
			}
		}
	}
	return out
}

// importPaths maps each import name of a file to its import path: the explicit
// alias, or else the default package name, which is the last path element (a
// trailing major-version element "v2" is skipped). Dot and blank imports are not
// names. The default name is a guess: a package whose name differs from its
// path element is not recognised, and its selectors become method seeds (a
// documented ceiling; no type information is read).
func importPaths(f *ast.File) map[string]string {
	paths := map[string]string{}
	for _, s := range f.Imports {
		path, err := strconv.Unquote(s.Path.Value)
		if err != nil {
			continue
		}
		name := defaultPackageName(path)
		if s.Name != nil {
			name = s.Name.Name
		}
		if name != "" && name != "_" && name != "." {
			paths[name] = path
		}
	}
	return paths
}

// defaultPackageName guesses the package name of an import path.
func defaultPackageName(path string) string {
	elems := strings.Split(path, "/")
	name := elems[len(elems)-1]
	if isMajorVersion(name) && len(elems) > 1 {
		name = elems[len(elems)-2]
	}
	return name
}

// isMajorVersion reports whether a path element is a module major version (v2).
func isMajorVersion(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, c := range s[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// seedWalker collects the name seeds of one declaration. Scope is a
// conservative name check, not a resolver: every name declared anywhere in the
// declaration (parameters, results, receiver, type parameters, := and var
// declarations, range and type-switch binds, including those of nested closures)
// is local, so no use of that name is a package-level seed. A local also carries
// the receiver type its bindings give it (scope), and a name bound twice to
// different types is untyped. The known limit: a package-level name used before
// a same-named local is declared in the same function is missed.
// Dot imports are not tracked, so their members surface as unresolved bare
// identifiers.
type seedWalker struct {
	fset    *token.FileSet
	imports map[string]string  // import name -> import path
	env     map[string]recvRef // names the declaration declares -> their receiver type (zero when unknown)
	tparams map[string]bool    // type parameter names, which shadow package types
	out     []goSeed
}

// seeds returns the seeds of one declaration root (a FuncDecl, TypeSpec or
// ValueSpec), in source order. The declaration's own name is never a seed.
func (w *seedWalker) seeds(root ast.Node) []goSeed {
	w.tparams = tparamsOf(root)
	w.env = map[string]recvRef{}
	w.scope(root)
	w.out = nil
	w.walk(root)
	slices.SortStableFunc(w.out, func(a, b goSeed) int { return a.Line - b.Line })
	return w.out
}

// scope binds every name the declaration declares inside itself: parameters,
// results and the receiver (typed by their declared type), := and var declarations
// (typed when recvOf can tell), and range, type-switch, select and local type
// binds (untyped). A name bound twice to different receivers becomes untyped.
func (w *seedWalker) scope(root ast.Node) {
	bind := func(name string, r recvRef) {
		if name == "_" {
			return
		}
		if old, seen := w.env[name]; seen && old != r {
			r = recvRef{}
		}
		w.env[name] = r
	}
	bindFields := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			var r recvRef
			if t, ok := w.typeOf(f.Type); ok {
				r = recvRef{Type: t}
			}
			for _, n := range f.Names {
				bind(n.Name, r)
			}
		}
	}
	bindExpr := func(e ast.Expr) {
		if id, ok := e.(*ast.Ident); ok {
			bind(id.Name, recvRef{})
		}
	}
	ast.Inspect(root, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			bindFields(x.Recv)
		case *ast.FuncType:
			bindFields(x.Params)
			bindFields(x.Results)
		case *ast.AssignStmt:
			if x.Tok != token.DEFINE {
				break
			}
			for i, l := range x.Lhs {
				id, ok := l.(*ast.Ident)
				if !ok {
					continue
				}
				var r recvRef
				if len(x.Lhs) == len(x.Rhs) {
					r = w.recvOf(x.Rhs[i])
				}
				bind(id.Name, r)
			}
		case *ast.RangeStmt:
			if x.Tok == token.DEFINE {
				bindExpr(x.Key)
				bindExpr(x.Value)
			}
		case *ast.DeclStmt:
			gd, ok := x.Decl.(*ast.GenDecl)
			if !ok {
				break
			}
			for _, s := range gd.Specs {
				switch sp := s.(type) {
				case *ast.ValueSpec:
					for i, id := range sp.Names {
						var r recvRef
						switch {
						case sp.Type != nil:
							if t, ok := w.typeOf(sp.Type); ok {
								r = recvRef{Type: t}
							}
						case len(sp.Values) == len(sp.Names):
							r = w.recvOf(sp.Values[i])
						}
						bind(id.Name, r)
					}
				case *ast.TypeSpec:
					bind(sp.Name.Name, recvRef{})
				}
			}
		}
		return true
	})
}

// recvOf reads what the syntax gives of an expression's static type: a name the
// declaration binds, a composite literal, a field (one level) of a bound name,
// or the result of a call to a function the declaration does not bind. Anything
// else is unknown.
func (w *seedWalker) recvOf(e ast.Expr) recvRef {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return w.recvOf(x.X)
	case *ast.StarExpr:
		return w.recvOf(x.X)
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			return w.recvOf(x.X)
		}
	case *ast.CompositeLit:
		if t, ok := w.typeOf(x.Type); ok {
			return recvRef{Type: t}
		}
	case *ast.Ident:
		return w.env[x.Name]
	case *ast.SelectorExpr:
		if base, ok := x.X.(*ast.Ident); ok {
			if r := w.env[base.Name]; r.Field == "" && r.Ctor == "" && r.Type.Name != "" {
				return recvRef{Type: r.Type, Field: x.Sel.Name}
			}
		}
	case *ast.CallExpr:
		if id, ok := x.Fun.(*ast.Ident); ok {
			if _, declared := w.env[id.Name]; !declared {
				return recvRef{Ctor: id.Name}
			}
		}
	}
	return recvRef{}
}

// typeOf reads a type expression: a type name, a qualified type name whose
// qualifier is an import of the file, or either behind pointers, parentheses and
// type arguments. A type parameter is not a type name.
func (w *seedWalker) typeOf(e ast.Expr) (typeRef, bool) {
	switch x := e.(type) {
	case *ast.StarExpr:
		return w.typeOf(x.X)
	case *ast.ParenExpr:
		return w.typeOf(x.X)
	case *ast.IndexExpr:
		return w.typeOf(x.X)
	case *ast.IndexListExpr:
		return w.typeOf(x.X)
	case *ast.Ident:
		return typeRef{Name: x.Name}, !w.tparams[x.Name]
	case *ast.SelectorExpr:
		if base, ok := x.X.(*ast.Ident); ok {
			if path, imported := w.imports[base.Name]; imported {
				return typeRef{Path: path, Name: x.Sel.Name}, true
			}
		}
	}
	return typeRef{}, false
}

// walk visits n; a nil or typed-nil node is skipped.
func (w *seedWalker) walk(n ast.Node) {
	if n == nil {
		return
	}
	if v := reflect.ValueOf(n); v.Kind() == reflect.Pointer && v.IsNil() {
		return
	}
	ast.Inspect(n, w.visit)
}

// visit handles the node kinds whose children are not all uses, or whose uses
// depend on position; the rest are walked by ast.Inspect.
func (w *seedWalker) visit(n ast.Node) bool {
	switch x := n.(type) {
	case *ast.FuncDecl:
		// The name is the element's own declaration, not a use.
		w.walk(x.Recv)
		w.walk(x.Type)
		w.walk(x.Body)
		return false
	case *ast.TypeSpec:
		w.walk(x.TypeParams)
		w.walk(x.Type)
		return false
	case *ast.ValueSpec:
		w.walk(x.Type)
		for _, v := range x.Values {
			w.value(v)
		}
		return false
	case *ast.Field:
		// Field and parameter names are declarations; the type is a use.
		w.walk(x.Type)
		return false
	case *ast.KeyValueExpr:
		// A bare key in a composite literal names a struct field, not a use.
		if _, bare := x.Key.(*ast.Ident); !bare {
			w.walk(x.Key)
		}
		w.value(x.Value)
		return false
	case *ast.CompositeLit:
		w.walk(x.Type)
		for _, e := range x.Elts {
			w.value(e)
		}
		return false
	case *ast.AssignStmt:
		for _, l := range x.Lhs {
			w.walk(l)
		}
		for _, r := range x.Rhs {
			if x.Tok == token.ASSIGN || x.Tok == token.DEFINE {
				w.value(r)
			} else {
				w.walk(r) // an operator assignment reads its operand
			}
		}
		return false
	case *ast.LabeledStmt:
		w.walk(x.Stmt)
		return false
	case *ast.BranchStmt:
		return false // a goto/break/continue label is not a use
	case *ast.CallExpr:
		w.callee(x.Fun)
		for _, a := range x.Args {
			w.value(a)
		}
		return false
	case *ast.SelectorExpr:
		w.selector(x, false, false)
		return false
	case *ast.Ident:
		w.ident(x, false)
		return false
	}
	return true
}

// callee records the use of a called expression.
func (w *seedWalker) callee(e ast.Expr) {
	switch f := e.(type) {
	case *ast.ParenExpr:
		w.callee(f.X)
	case *ast.Ident:
		w.ident(f, true)
	case *ast.SelectorExpr:
		w.selector(f, true, false)
	default:
		w.walk(e)
	}
}

// value walks an expression passed or stored as a value: a call argument, an
// assignment's right side or a composite-literal element. A selector there may
// be a method value, so it is a seed; a field value is resolved away later.
func (w *seedWalker) value(e ast.Expr) {
	p := e
	for {
		paren, ok := p.(*ast.ParenExpr)
		if !ok {
			break
		}
		p = paren.X
	}
	if sel, ok := p.(*ast.SelectorExpr); ok {
		w.selector(sel, false, true)
		return
	}
	w.walk(e)
}

// selector records the seeds of a selector used as a call (call), as a value
// (value), or as neither. A qualified name is a seed whatever its use. A method
// is a seed only when called or taken as a value, and a plain selector is a
// field read, which makes no seed. The receiver is walked either way.
func (w *seedWalker) selector(sel *ast.SelectorExpr, call, value bool) {
	if alias, ok := w.qualifiedAlias(sel); ok {
		w.add(goSeed{Kind: "qualified", Alias: alias, Name: sel.Sel.Name, Line: w.line(sel.Sel.Pos()), Call: call})
		return
	}
	if call || value {
		w.add(goSeed{Kind: "method", Name: sel.Sel.Name, Line: w.line(sel.Sel.Pos()), Call: call, Recv: w.recvOf(sel.X)})
	}
	w.walk(sel.X)
}

// qualifiedAlias reports whether sel is alias.Name for an import alias the
// declaration does not bind to a local of its own.
func (w *seedWalker) qualifiedAlias(sel *ast.SelectorExpr) (string, bool) {
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	if _, imported := w.imports[id.Name]; !imported {
		return "", false
	}
	if _, declared := w.env[id.Name]; declared {
		return "", false
	}
	return id.Name, true
}

// ident records a bare identifier use unless the declaration declares it.
func (w *seedWalker) ident(id *ast.Ident, call bool) {
	if _, declared := w.env[id.Name]; id.Name == "_" || declared {
		return
	}
	w.add(goSeed{Kind: "ident", Name: id.Name, Line: w.line(id.Pos()), Call: call})
}

func (w *seedWalker) add(s goSeed) { w.out = append(w.out, s) }

func (w *seedWalker) line(p token.Pos) int { return w.fset.Position(p).Line }

// tparamsOf returns the type parameter names a declaration declares: those of
// its function types and type specs, and the receiver's type arguments.
func tparamsOf(root ast.Node) map[string]bool {
	out := map[string]bool{}
	add := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			for _, n := range f.Names {
				out[n.Name] = true
			}
		}
	}
	ast.Inspect(root, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			if x.Recv != nil && len(x.Recv.List) > 0 {
				addTypeArgs(out, x.Recv.List[0].Type)
			}
		case *ast.FuncType:
			add(x.TypeParams)
		case *ast.TypeSpec:
			add(x.TypeParams)
		}
		return true
	})
	return out
}

// addTypeArgs adds the names of a receiver's type arguments ("*Set[T]" -> T).
func addTypeArgs(out map[string]bool, e ast.Expr) {
	switch t := e.(type) {
	case *ast.StarExpr:
		addTypeArgs(out, t.X)
	case *ast.ParenExpr:
		addTypeArgs(out, t.X)
	case *ast.IndexExpr:
		if id, ok := t.Index.(*ast.Ident); ok {
			out[id.Name] = true
		}
	case *ast.IndexListExpr:
		for _, ix := range t.Indices {
			if id, ok := ix.(*ast.Ident); ok {
				out[id.Name] = true
			}
		}
	}
}

// bareTypeName returns the bare type name of a type expression: pointers,
// parentheses, type-parameter lists and package qualifiers are stripped
// ("*Set[T]" -> "Set", "*sync.Mutex" -> "Mutex").
func bareTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return bareTypeName(t.X)
	case *ast.ParenExpr:
		return bareTypeName(t.X)
	case *ast.IndexExpr:
		return bareTypeName(t.X)
	case *ast.IndexListExpr:
		return bareTypeName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.Ident:
		return t.Name
	}
	return ""
}
