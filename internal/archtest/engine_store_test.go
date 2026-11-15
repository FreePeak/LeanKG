package archtest

import (
	"go/ast"
	"strings"
	"testing"
)

// TestEngineHasNoStoreMethod fails while core.Engine still hands out its store
// (plan CX-39). Each caller gets the role it needs from its owner instead.
func TestEngineHasNoStoreMethod(t *testing.T) {
	fset, files := scanSources(t, func(string, string) bool { return true })
	var bad []string
	for _, f := range files {
		if f.Dir != "internal/core" {
			continue
		}
		for _, decl := range f.AST.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && funcName(fn) == "Engine.Store" {
				bad = append(bad, fset.Position(fn.Pos()).String())
			}
		}
	}
	if len(bad) > 0 {
		t.Fatalf("core.Engine.Store exists (remove it; hand the caller its role instead):\n%s",
			strings.Join(bad, "\n"))
	}
}

// TestNoEngineStoreCallers fails on any zero-argument .Store() call in any
// non-generated Go file, tests and package main included. Today the only such
// method in the repository is Engine.Store, so the name match is exact; a new
// Store() method with no arguments would fail here too and must be justified.
func TestNoEngineStoreCallers(t *testing.T) {
	fset, files := scanSources(t, func(string, string) bool { return true })
	var bad []string
	for _, f := range files {
		ast.Inspect(f.AST, func(n ast.Node) bool {
			if _, sel, args, ok := asCall(n); ok && sel == "Store" && len(args) == 0 {
				bad = append(bad, fset.Position(n.Pos()).String())
			}
			return true
		})
	}
	if len(bad) > 0 {
		t.Fatalf("%d caller(s) of Engine.Store (pass the role the caller needs):\n%s",
			len(bad), strings.Join(bad, "\n"))
	}
}
