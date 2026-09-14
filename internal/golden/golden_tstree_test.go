//go:build tstree

package golden

// TestMain skips the golden comparisons under the tstree build: the goldens
// capture the REGEX extraction shape (heuristic end lines), while the
// tree-sitter tier legitimately computes real block end lines. Each tier is
// validated by its own tests; the default build owns the goldens.
func skipGoldensUnderTstree() bool { return true }
