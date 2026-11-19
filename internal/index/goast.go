package index

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
)

// goDefinitions parses one Go file with the standard-library parser and
// returns its file-scope definitions and its explicit import aliases (alias ->
// import path). It is CGO-free and used by every build. ok is false when the
// parser rejects the file; the caller then falls back to the regex tier.
//
// Ranges come from AST positions: a declaration starts on its keyword or name
// line and ends on its last token, so a comment or a string that merely looks
// like a declaration is never an element. Methods are owned by their receiver's
// bare type name ("*Set[T]" -> "Set"). Interfaces and structs are one element
// each; their members are not separate elements.
func goDefinitions(src []byte) (defs []indexDef, imports map[string]string, ok bool) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, false
	}
	line := func(p token.Pos) int { return fset.Position(p).Line }
	add := func(kind, name string, start, end token.Pos, owner string) {
		if name == "_" {
			return
		}
		defs = append(defs, indexDef{Kind: kind, Name: name, StartLine: line(start), EndLine: line(end), Owner: owner})
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			kind, owner := "function", ""
			if d.Recv != nil && len(d.Recv.List) > 0 {
				kind, owner = "method", recvTypeName(d.Recv.List[0].Type)
			}
			add(kind, d.Name.Name, d.Pos(), d.End(), owner)
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, s := range d.Specs {
					ts := s.(*ast.TypeSpec)
					add("type", ts.Name.Name, ts.Pos(), ts.End(), "")
				}
			case token.CONST, token.VAR:
				kind := "variable"
				if d.Tok == token.CONST {
					kind = "constant"
				}
				for _, s := range d.Specs {
					vs := s.(*ast.ValueSpec)
					for _, name := range vs.Names {
						add(kind, name.Name, vs.Pos(), vs.End(), "")
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
	return defs, imports, true
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
