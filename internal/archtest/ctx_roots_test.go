package archtest

import (
	"go/ast"
	"testing"
)

// backgroundBudget is the measured count of context.Background() calls per
// directory in non-main packages. It only goes down: a directory whose count
// drops must be lowered in the same diff, and a directory must reach 0 and
// leave the map.
var backgroundBudget = map[string]int{
	"internal/lsp":      1,
	"internal/registry": 1,
	"internal/session":  1,
	"internal/setup":    1,
}

// TestContextRootsRatchet counts context root calls in non-main packages.
// context.Background() is compared with backgroundBudget; context.TODO() must
// be zero in every package (the P3 burn-down is done, so there is no budget).
func TestContextRootsRatchet(t *testing.T) {
	_, files := scanLibrary(t)
	compareBudget(t, "context.Background", countByDir(files, isContextRoot("Background")), backgroundBudget)
	compareBudget(t, "context.TODO", countByDir(files, isContextRoot("TODO")), nil)
}

// isContextRoot matches calls of the form context.<name>().
func isContextRoot(name string) nodeHit {
	return func(f *sourceFile, n ast.Node) bool {
		return isPkgCall(f, n, "context", name)
	}
}
