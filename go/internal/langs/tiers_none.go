//go:build !tstree

package langs

// TreeSitterEnabled reports whether the tree-sitter tier is compiled in.
// Default build: NO (CGO-free constraint). Build with `-tags tstree` to
// enable the CGO grammar tier.
func TreeSitterEnabled(_ Language) bool { return false }

// astGrepLang maps a language to its ast-grep language id ("" = no mapping).
func astGrepLang(_ Language) string { return "" }
