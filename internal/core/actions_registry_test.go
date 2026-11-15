package core

// Action dispatch is a registry lookup (CX-43). Every action and memory
// command is one entry in a registry var next to its handler, and the
// dispatchers below route through those vars without a switch. The two tests
// here fail while a dispatcher still switches on a case literal, or while a
// registry is missing from the source.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// actionRegistries names each registry var and the vocabulary prefix its keys
// sit under. These are the only action and memory-command tables in core.
var actionRegistries = map[string]string{
	"queryActions":        "query",
	"importActions":       "import",
	"memoryWriteCommands": "import/memory",
	"queryMemoryCommands": "query/memory",
	"memoryReadCommands":  "query/memory",
}

// actionDispatchers are the functions that route a request to a registry. None
// may switch on an action or command literal.
var actionDispatchers = []string{"query", "importAction", "memoryWrite", "queryMemory", "MemoryRead"}

// parseCoreSources parses the non-test Go files of the core package.
func parseCoreSources(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(coreDir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		files = append(files, f)
	}
	return fset, files
}

// findCoreFunc returns the named function declared in the core package.
func findCoreFunc(t *testing.T, files []*ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, f := range files {
		for _, decl := range f.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == name && fd.Body != nil {
				return fd
			}
		}
	}
	t.Fatalf("dispatcher %s not found in %s", name, coreDir)
	return nil
}

// findRegistry returns the composite literal a package-level registry is set
// from, either its var declaration or the assignment in init. It returns nil
// when no source file declares the registry.
func findRegistry(files []*ast.File, name string) *ast.CompositeLit {
	var found *ast.CompositeLit
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.ValueSpec:
				for i, id := range v.Names {
					if id.Name == name && i < len(v.Values) {
						if lit, ok := v.Values[i].(*ast.CompositeLit); ok {
							found = lit
						}
					}
				}
			case *ast.AssignStmt:
				for i, lhs := range v.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name == name && i < len(v.Rhs) {
						if lit, ok := v.Rhs[i].(*ast.CompositeLit); ok {
							found = lit
						}
					}
				}
			}
			return true
		})
	}
	return found
}

// TestActionDispatchHasNoCaseSwitch fails when a dispatcher still routes an
// action or command through a switch: any case clause holding a string literal.
func TestActionDispatchHasNoCaseSwitch(t *testing.T) {
	fset, files := parseCoreSources(t)
	for _, name := range actionDispatchers {
		fd := findCoreFunc(t, files, name)
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			for _, stmt := range sw.Body.List {
				cc, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, e := range cc.List {
					if _, ok := stringLit(e, nil); ok {
						t.Errorf("%s still dispatches through a switch at %s; register the action in its registry instead",
							name, fset.Position(e.Pos()))
					}
				}
			}
			return true
		})
	}
}

// TestActionRegistriesHoldEveryKey requires each registry var to exist with at
// least one entry, and every key it holds to be covered by a golden: the
// registry is the vocabulary, so a key with no golden is an uncovered action.
func TestActionRegistriesHoldEveryKey(t *testing.T) {
	_, files := parseCoreSources(t)
	covered := map[string]bool{}
	for _, c := range actionCases {
		for _, k := range c.covers() {
			covered[k] = true
		}
	}
	for name, prefix := range actionRegistries {
		lit := findRegistry(files, name)
		if lit == nil {
			t.Fatalf("registry %s not found in %s", name, coreDir)
		}
		if len(lit.Elts) == 0 {
			t.Fatalf("registry %s is empty", name)
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				t.Fatalf("registry %s has an entry without a key", name)
			}
			key, ok := stringLit(kv.Key, nil)
			if !ok {
				t.Fatalf("registry %s has a non-literal key", name)
			}
			k := prefix + "/" + displayLit(key)
			if !covered[k] && !envGatedKeys[k] {
				t.Errorf("registry %s dispatches %q with no golden", name, k)
			}
		}
	}
}
