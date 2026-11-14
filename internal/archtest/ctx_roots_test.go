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
	"internal/core":      2,
	"internal/index":     2,
	"internal/langs":     1,
	"internal/lsp":       4,
	"internal/memory":    11,
	"internal/registry":  1,
	"internal/session":   1,
	"internal/setup":     1,
	"internal/store":     1,
	"internal/telemetry": 8,
}

// todoBudget is the measured count of context.TODO() calls per directory in
// non-main packages. It may rise only in store role-migration PRs (plan P2),
// and it must reach 0 by the end of P3.
var todoBudget = map[string]int{}

// TestContextRootsRatchet counts context root calls (Background and TODO) in
// non-main packages and compares each directory with its budget.
func TestContextRootsRatchet(t *testing.T) {
	_, files := scanLibrary(t)
	compareBudget(t, "context.Background", countByDir(files, isContextRoot("Background")), backgroundBudget)
	compareBudget(t, "context.TODO", countByDir(files, isContextRoot("TODO")), todoBudget)
}

// isContextRoot matches calls of the form context.<name>().
func isContextRoot(name string) func(ast.Node) bool {
	return func(n ast.Node) bool {
		return isPkgCall(n, "context", name)
	}
}
