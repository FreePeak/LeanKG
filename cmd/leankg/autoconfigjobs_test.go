package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestAutoConfigRunsOnServeJobs pins the serve half of CX-05: every mounted
// auto-config endpoint runs its jobs on the Jobs that serve owns and joins at
// shutdown. A RegisterAutoConfig call without WithJobs starts a job that
// outlives serve's shutdown, which is the leak CX-05 closes.
func TestAutoConfigRunsOnServeJobs(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	mounts, unwired := 0, []string{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isRestAutoCall(call, "RegisterAutoConfig") {
				return true
			}
			mounts++
			wired := false
			for _, arg := range call.Args {
				if c, ok := arg.(*ast.CallExpr); ok && isRestAutoCall(c, "WithJobs") {
					wired = true
				}
			}
			if !wired {
				unwired = append(unwired, fset.Position(call.Pos()).String())
			}
			return true
		})
	}
	if mounts == 0 {
		t.Fatal("found no RegisterAutoConfig call: the scan is looking in the wrong place")
	}
	if len(unwired) > 0 {
		t.Fatalf("RegisterAutoConfig without restauto.WithJobs (its jobs outlive serve):\n%s", strings.Join(unwired, "\n"))
	}
}

// isRestAutoCall reports whether call is restauto.<name>(...).
func isRestAutoCall(call *ast.CallExpr, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "restauto"
}
