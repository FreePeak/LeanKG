//go:build !tstree

package golden

// skipGoldensUnderTstree returns false in the default (regex) build: the
// goldens run and pin the extraction wire shapes.
func skipGoldensUnderTstree() bool { return false }
