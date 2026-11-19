package index

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// goSeed is one name a Go element uses, read from the AST rather than from its
// text. Kinds:
//   - "ident": a bare identifier resolved in the same package (a function, type,
//     const or var; never a method, which a bare identifier cannot name);
//   - "qualified": alias.Name where alias is an import name (cross-package; the
//     next resolver consumes these, relationships() does not);
//   - "method": x.Name(...) where x is not an import name, resolved to the
//     same-package methods named Name.
//
// Call is true for a call site (the CallExpr's function), false for a plain
// reference (a type in a signature, a composite literal's type, a value use).
type goSeed struct {
	Kind  string
	Alias string // qualified only
	Name  string
	Line  int
	Call  bool
}

// goDefinitions parses one Go file with the standard-library parser and
// returns its file-scope definitions, the name seeds of each definition (seeds[i]
// belongs to defs[i]) and its explicit import aliases (alias -> import path). It
// is CGO-free and used by every build. ok is false when the parser rejects the
// file; the caller then falls back to the regex tier.
//
// Ranges come from AST positions: a declaration starts on its keyword or name
// line and ends on its last token, so a comment or a string that merely looks
// like a declaration is never an element. Methods are owned by their receiver's
// bare type name ("*Set[T]" -> "Set"). Interfaces and structs are one element
// each; their members are not separate elements.
func goDefinitions(src []byte) (defs []indexDef, seeds [][]goSeed, imports map[string]string, ok bool) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, nil, false
	}
	w := &seedWalker{fset: fset, imports: importNames(f)}
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
				kind, owner = "method", recvTypeName(d.Recv.List[0].Type)
			}
			add(kind, d.Name.Name, d.Pos(), d.End(), owner, w.seeds(d))
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, s := range d.Specs {
					ts := s.(*ast.TypeSpec)
					add("type", ts.Name.Name, ts.Pos(), ts.End(), "", w.seeds(ts))
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
			case token.IMPORT:
				for _, s := range d.Specs {
					is := s.(*ast.ImportSpec)
					if is.Name == nil || is.Name.Name == "_" || is.Name.Name == "." {
						continue
					}
					path, err := strconv.Unquote(is.Path.Value)
					if err != nil {
						continue
					}
					if imports == nil {
						imports = map[string]string{}
					}
					imports[is.Name.Name] = path
				}
			}
		}
	}
	return defs, seeds, imports, true
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

// importNames returns the names a file's imports are reached by: the explicit
// alias, or else the default package name, which is the last path element
// (a trailing major-version element "v2" is skipped). Dot and blank imports
// are not names. The default name is a guess: a package whose name differs
// from its path element is not recognised, and its selectors become method
// seeds (a documented ceiling; no type information is read).
func importNames(f *ast.File) map[string]bool {
	names := map[string]bool{}
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
			names[name] = true
		}
	}
	return names
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
// suppresses every use of that name in the declaration. The known limit: a
// package-level name used before a same-named local is declared in the same
// function is missed. Dot imports are not tracked, so their members surface
// as unresolved bare identifiers.
type seedWalker struct {
	fset    *token.FileSet
	imports map[string]bool
	locals  map[string]bool
	out     []goSeed
}

// seeds returns the seeds of one declaration root (a FuncDecl, TypeSpec or
// ValueSpec), in source order. The declaration's own name is never a seed.
func (w *seedWalker) seeds(root ast.Node) []goSeed {
	w.locals = declaredLocals(root)
	w.out = nil
	w.walk(root)
	slices.SortStableFunc(w.out, func(a, b goSeed) int { return a.Line - b.Line })
	return w.out
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

// visit handles the node kinds whose children are not all uses; the rest are
// walked by ast.Inspect.
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
			w.walk(v)
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
		w.walk(x.Value)
		return false
	case *ast.LabeledStmt:
		w.walk(x.Stmt)
		return false
	case *ast.BranchStmt:
		return false // a goto/break/continue label is not a use
	case *ast.CallExpr:
		w.use(x.Fun, true)
		for _, a := range x.Args {
			w.walk(a)
		}
		return false
	case *ast.SelectorExpr:
		w.use(x, false)
		return false
	case *ast.Ident:
		w.ident(x, false)
		return false
	}
	return true
}

// use records a use of a callee or selector. call marks a CallExpr's function.
func (w *seedWalker) use(e ast.Expr, call bool) {
	switch f := e.(type) {
	case *ast.ParenExpr:
		w.use(f.X, call)
	case *ast.Ident:
		w.ident(f, call)
	case *ast.SelectorExpr:
		x, isIdent := f.X.(*ast.Ident)
		switch {
		case isIdent && w.imports[x.Name] && !w.locals[x.Name]:
			w.add(goSeed{Kind: "qualified", Alias: x.Name, Name: f.Sel.Name, Line: w.line(f.Sel.Pos()), Call: call})
		case call:
			w.add(goSeed{Kind: "method", Name: f.Sel.Name, Line: w.line(f.Sel.Pos()), Call: true})
			w.walk(f.X)
		default:
			w.walk(f.X)
		}
	default:
		w.walk(e)
	}
}

// ident records a bare identifier use unless the declaration declares it.
func (w *seedWalker) ident(id *ast.Ident, call bool) {
	if id.Name == "_" || w.locals[id.Name] {
		return
	}
	w.add(goSeed{Kind: "ident", Name: id.Name, Line: w.line(id.Pos()), Call: call})
}

func (w *seedWalker) add(s goSeed) { w.out = append(w.out, s) }

func (w *seedWalker) line(p token.Pos) int { return w.fset.Position(p).Line }

// declaredLocals returns every name a declaration declares inside itself.
// Package-level names are not included: only a FuncDecl's receiver and
// parameters, function types' parameters and results, type parameters, :=
// and range binds, and local var, const and type declarations.
func declaredLocals(root ast.Node) map[string]bool {
	locals := map[string]bool{}
	addFields := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			for _, n := range f.Names {
				locals[n.Name] = true
			}
		}
	}
	addIdent := func(e ast.Expr) {
		if id, ok := e.(*ast.Ident); ok {
			locals[id.Name] = true
		}
	}
	ast.Inspect(root, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			addFields(x.Recv)
		case *ast.FuncType:
			addFields(x.TypeParams)
			addFields(x.Params)
			addFields(x.Results)
		case *ast.TypeSpec:
			addFields(x.TypeParams)
		case *ast.AssignStmt:
			if x.Tok == token.DEFINE {
				for _, e := range x.Lhs {
					addIdent(e)
				}
			}
		case *ast.RangeStmt:
			if x.Tok == token.DEFINE {
				addIdent(x.Key)
				addIdent(x.Value)
			}
		case *ast.DeclStmt:
			if gd, ok := x.Decl.(*ast.GenDecl); ok {
				for _, s := range gd.Specs {
					switch sp := s.(type) {
					case *ast.ValueSpec:
						for _, id := range sp.Names {
							locals[id.Name] = true
						}
					case *ast.TypeSpec:
						locals[sp.Name.Name] = true
					}
				}
			}
		}
		return true
	})
	return locals
}

// recvTypeName returns the bare receiver type name: pointers, parentheses and
// type-parameter lists are stripped ("*Set[T]" -> "Set").
func recvTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvTypeName(t.X)
	case *ast.ParenExpr:
		return recvTypeName(t.X)
	case *ast.IndexExpr:
		return recvTypeName(t.X)
	case *ast.IndexListExpr:
		return recvTypeName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return ""
}
