package web

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestWebReadsGraphThroughCore keeps the web package on core's seam: no
// non-test file in internal/web imports internal/store, and no code calls
// Engine.Store().
func TestWebReadsGraphThroughCore(t *testing.T) {
	const storePkg = "github.com/FreePeak/LeanKG/internal/store"
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) == storePkg {
				t.Errorf("%s imports %s; read the graph through core (core.Engine)", name, storePkg)
			}
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Store" && len(call.Args) == 0 {
					t.Errorf("%s: %s calls Store() at %s; use the core read instead",
						name, fn.Name.Name, fset.Position(call.Pos()))
				}
				return true
			})
		}
	}
}
