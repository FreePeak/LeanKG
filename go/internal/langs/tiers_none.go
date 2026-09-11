//go:build !tstree

package langs

// TreeSitterEnabled reports whether the tree-sitter tier is compiled in.
// Default build: NO for every language (CGO-free constraint).
func TreeSitterEnabled(_ Language) bool { return false }

// astGrepLang maps a language to its ast-grep language id ("" = no AST tier).
func astGrepLang(l Language) string {
	switch l {
	case Go:
		return "go"
	case Rust:
		return "rust"
	case TypeScript, TSX:
		return "ts"
	case JavaScript, JSX:
		return "javascript"
	case Python:
		return "python"
	case Java:
		return "java"
	case Kotlin:
		return "kotlin"
	case Swift:
		return "swift"
	case ObjC:
		return "c"
	}
	return "" // dart, markdown: no AST tier
}
