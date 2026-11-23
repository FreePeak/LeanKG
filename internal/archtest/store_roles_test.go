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

// openBackendSites is the budget of store.OpenBackend calls per non-test file
// outside internal/store and cmd/. Each entry opens and closes its own store
// handle; new sites must be added here deliberately.
var openBackendSites = map[string]int{
	"internal/convo/convo.go":               1,
	"internal/core/portfolio.go":            1,
	"internal/doctor/fleet.go":              1,
	"internal/portfolioreg/portfolioreg.go": 2,
	"internal/portfolioreg/sweep.go":        1,
	"internal/projects/projects.go":         1,
	"internal/refresh/refresh.go":           1,
	"internal/registry/registry.go":         1,
}

// TestBackendOnlyInCompositionRoots fails when a file names store.Backend more
// often than its backendRoots budget, and when a budget is larger than the
// file's actual count (lower the entry).
func TestBackendOnlyInCompositionRoots(t *testing.T) {
	_, files := scanLibrary(t)
	counts := countPerFile(files, isBackendRef)
	compareFileBudget(t, "backendRoots", "store.Backend references",
		"depend on the store roles it calls instead", counts, backendRoots)
}

// TestOpenBackendOnlyAtKnownSites pins the store.OpenBackend call sites outside
// the composition roots to openBackendSites.
func TestOpenBackendOnlyAtKnownSites(t *testing.T) {
	_, files := scanLibrary(t)
	counts := countPerFile(files, func(f *sourceFile, n ast.Node) bool {
		return isPkgCall(f, n, storePkg, "OpenBackend")
	})
	compareFileBudget(t, "openBackendSites", "store.OpenBackend calls",
		"open the store through a role or add the site to openBackendSites deliberately", counts, openBackendSites)
}

// countPerFile counts hits per repo-relative file among the non-test files
// outside cmd/ and internal/store, keeping only files with at least one hit.
func countPerFile(files []sourceFile, hit nodeHit) map[string]int {
	counts := map[string]int{}
	for _, f := range files {
		if underDir(f.Dir, "cmd") || underDir(f.Dir, "internal/store") {
			continue
		}
		hits := 0
		ast.Inspect(f.AST, func(n ast.Node) bool {
			if hit(&f, n) {
				hits++
			}
			return true
		})
		if hits > 0 {
			counts[f.Rel] = hits
		}
	}
	return counts
}

// compareFileBudget applies the two-way ratchet to per-file counts: a file above
// its budget fails with hint, a budget above the file's count must be lowered,
// and a zero count must drop its entry. mapName names the budget map in messages.
func compareFileBudget(t *testing.T, mapName, what, hint string, counts, budget map[string]int) {
	t.Helper()
	for rel, got := range counts {
		if want := budget[rel]; got > want {
			t.Errorf("%s: %d %s (budget %d): %s", rel, got, what, want, hint)
		}
	}
	for rel, want := range budget {
		if got := counts[rel]; got == 0 {
			t.Errorf("remove the %s entry for %s in internal/archtest/store_roles_test.go: it has no %s or no longer exists", mapName, rel, what)
		} else if got < want {
			t.Errorf("lower the %s entry for %s to %d in internal/archtest/store_roles_test.go", mapName, rel, got)
		}
	}
}

// storePkg is the import path of the store package whose calls are pinned.
const storePkg = "github.com/FreePeak/LeanKG/internal/store"

// isBackendRef reports whether n is the selector store.Backend.
func isBackendRef(f *sourceFile, n ast.Node) bool {
	sel, ok := n.(*ast.SelectorExpr)
	return ok && isQualified(f, sel, storePkg, "Backend")
}

// underDir reports whether dir is root or below it.
func underDir(dir, root string) bool {
	return dir == root || strings.HasPrefix(dir, root+"/")
}
