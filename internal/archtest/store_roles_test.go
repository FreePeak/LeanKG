package archtest

import (
	"go/ast"
	"strings"
	"testing"
)

// backendRoots is the budget of store.Backend references per non-test file
// outside internal/store and cmd/ (plan section 3.3, rule 2; CX-38). Only the
// composition root core.New may hold the composed Backend; every other file
// depends on the store roles it calls. The map can only shrink.
var backendRoots = map[string]int{"internal/core/core.go": 2}

// TestBackendOnlyInCompositionRoots fails when a file names store.Backend more
// often than its backendRoots budget, and when a budget is larger than the
// file's actual count (lower the entry).
func TestBackendOnlyInCompositionRoots(t *testing.T) {
	_, files := scanLibrary(t)
	counts := map[string]int{}
	for _, f := range files {
		if underDir(f.Dir, "cmd") || underDir(f.Dir, "internal/store") {
			continue
		}
		hits := 0
		ast.Inspect(f.AST, func(n ast.Node) bool {
			if isBackendRef(n) {
				hits++
			}
			return true
		})
		if hits > 0 {
			counts[f.Rel] = hits
		}
	}

	for rel, got := range counts {
		if want := backendRoots[rel]; got > want {
			t.Errorf("%s names store.Backend %d time(s) (budget %d): depend on the store roles it calls instead", rel, got, want)
		}
	}
	for rel, want := range backendRoots {
		if got := counts[rel]; got == 0 {
			t.Errorf("remove the backendRoots entry for %s in internal/archtest/store_roles_test.go: it has no store.Backend references or no longer exists", rel)
		} else if got < want {
			t.Errorf("lower the backendRoots entry for %s to %d in internal/archtest/store_roles_test.go", rel, got)
		}
	}
}

// isBackendRef reports whether n is the selector store.Backend.
func isBackendRef(n ast.Node) bool {
	sel, ok := n.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "store" && sel.Sel.Name == "Backend"
}

// underDir reports whether dir is root or below it.
func underDir(dir, root string) bool {
	return dir == root || strings.HasPrefix(dir, root+"/")
}
