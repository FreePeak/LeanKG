package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// routeSourceDirs and routeSourceFiles are the gated listeners' route
// registrations: the REST, ConnectRPC and federation surfaces, the auth
// bootstrap, and the serve wiring that mounts the MCP, REST, RPC and UI
// listeners. The UI and dashboard surfaces are not listed: the UI is public by
// design (decision D1 gates only /api/file, in serve), and the dashboard has its
// own token gate.
var (
	routeSourceDirs  = []string{"internal/auth", "internal/rest", "internal/rpc", "internal/federation"}
	routeSourceFiles = []string{"cmd/leankg/main.go", "cmd/leankg/helpers.go"}
)

// TestRouteRegistrationsDeclareRole walks every route registration in the
// gated surfaces and fails on one that does not declare its access. Each route
// is registered as mux.Handle(pattern, auth.Require(role, h)) or
// mux.Handle(pattern, auth.Public(h)); a bare HandleFunc, or a Handle whose
// handler carries no declaration, is a route with no role (CX-49, default
// deny). Public is the explicit "no role floor" declaration.
func TestRouteRegistrationsDeclareRole(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	var bad []string
	checked := 0
	for _, f := range routeSourceFilesIn(t, root) {
		tree, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		ast.Inspect(tree, func(n ast.Node) bool {
			_, sel, args, ok := asCall(n)
			if !ok || (sel != "Handle" && sel != "HandleFunc") {
				return true
			}
			pos := fset.Position(n.Pos())
			checked++
			if sel == "HandleFunc" {
				bad = append(bad, pos.String()+": HandleFunc registers a bare handler; use Handle(pattern, auth.Require(role, h)) or auth.Public(h)")
				return true
			}
			if len(args) != 2 || !declaresRole(args[1]) {
				bad = append(bad, pos.String()+": route handler does not declare a role (wrap it in auth.Require or auth.Public)")
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no route registrations found: the scan roots are stale")
	}
	if len(bad) > 0 {
		t.Fatalf("route registrations without a declared role:\n%s", strings.Join(bad, "\n"))
	}
}

// declaresRole reports whether expr is a call to Require or Public, qualified
// (auth.Require) or bare inside package auth.
func declaresRole(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		pkg, ok := fn.X.(*ast.Ident)
		return ok && pkg.Name == "auth" && (fn.Sel.Name == "Require" || fn.Sel.Name == "Public")
	case *ast.Ident:
		return fn.Name == "Require" || fn.Name == "Public"
	}
	return false
}

// routeSourceFilesIn lists the non-test Go files of the gated surfaces. A
// directory that does not exist fails the test, so a moved package is noticed.
func routeSourceFilesIn(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	for _, dir := range routeSourceDirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", dir, err)
		}
	}
	for _, f := range routeSourceFiles {
		path := filepath.Join(root, f)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("scan %s: %v", f, err)
		}
		files = append(files, path)
	}
	return files
}
