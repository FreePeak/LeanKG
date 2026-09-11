//go:build tstree

package langs

// TreeSitterEnabled reports whether the tree-sitter tier is compiled in AND a
// grammar is bundled for this language (objc/dart/md have none — they stay on
// the regex tier even under the tag).
func TreeSitterEnabled(l Language) bool {
	switch l {
	case Go, Rust, TypeScript, TSX, JavaScript, JSX, Python, Java, Kotlin, Swift:
		return true
	}
	return false
}

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
		return "c" // objc grammar absent; ast-grep supports c
	case Dart:
		return "" // ast-grep has no dart language
	}
	return "" // markdown has no AST tier
}
