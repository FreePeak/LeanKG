package archtest

import (
	"go/ast"
	"strings"
	"testing"
)

// roleOnlyPackages are the packages that depend on store roles rather than on
// the composed store.Backend (plan section 3.3, rule 2; CX-38). Only the
// composition roots (cmd/*, core.New, store.OpenBackend) may hold a Backend.
var roleOnlyPackages = []string{
	"internal/auth",
	"internal/maintain",
	"internal/embed",
	"internal/orgknowledge",
}

// TestRolePackagesNeverNameBackend fails on any reference to store.Backend in
// the non-test files of the role-only packages: signatures, struct fields,
// locals and composite literals alike. The allowlist is empty by design; a
// package that needs a new store method declares it in its own role interface.
func TestRolePackagesNeverNameBackend(t *testing.T) {
	fset, files := scanLibrary(t)
	var bad []string
	for _, f := range files {
		if !inRolePackage(f.Dir) {
			continue
		}
		ast.Inspect(f.AST, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "store" && sel.Sel.Name == "Backend" {
				bad = append(bad, fset.Position(sel.Pos()).String())
			}
			return true
		})
	}
	if len(bad) > 0 {
		t.Fatalf("store.Backend referenced in role-only packages (declare the narrow role interface instead):\n%s",
			strings.Join(bad, "\n"))
	}
}

// inRolePackage reports whether dir is one of roleOnlyPackages or below it.
func inRolePackage(dir string) bool {
	for _, p := range roleOnlyPackages {
		if dir == p || strings.HasPrefix(dir, p+"/") {
			return true
		}
	}
	return false
}
