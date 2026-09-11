//go:build tstree

package langs

// TreeSitterEnabled reports whether the tree-sitter tier is compiled in.
// Built with `-tags tstree` (CGO + bundled grammars).
func TreeSitterEnabled(_ Language) bool { return true }

// astGrepLang maps a language to its ast-grep language id ("" = no mapping).
// ast-grep is a separate binary — availability is probed, not compiled —
// so this mapping is independent of the build tag.
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
		return "objc" // ast-grep id: "objc" (tree-sitter-objc)
	case Dart:
		return "dart"
	}
	return "" // markdown has no AST tier
}
