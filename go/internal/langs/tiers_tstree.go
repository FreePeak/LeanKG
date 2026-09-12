//go:build tstree

package langs

// TreeSitterEnabled reports whether the tree-sitter tier is compiled in AND a
// grammar is bundled for this language. objc and dart use the grammars
// vendored under internal/tstree/{objc,dart}; markdown and the expanded set
// have none — they stay on the regex tier even under the tag.
func TreeSitterEnabled(l Language) bool {
	switch l {
	case Go, Rust, TypeScript, TSX, JavaScript, JSX, Python, Java, Kotlin, Swift, ObjC, Dart:
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
	case C:
		return "c"
	case Cpp:
		return "cpp"
	case CSharp:
		return "cs" // ast-grep names it "cs" (the registry id csharp is not a CLI lang)
	case PHP:
		return "php"
	case Ruby:
		return "ruby"
	case Scala:
		return "scala"
	case Lua:
		return "lua"
	case Solidity:
		return "solidity" // verified: ast-grep 0.45.3 supports solidity (alias "sol")
	case Dart:
		return "" // ast-grep has no dart language
	}
	// markdown, perl, haskell, elixir, crystal, cuda, cypher, elm, erlang,
	// fsharp, glsl, hlsl, nim, ocaml, sql, powershell, qsharp, systemverilog,
	// verilog, zig: no AST tier — probed against the ast-grep CLI (0.45.3):
	// `ast-grep run --lang <id>` rejects every one of them.
	return ""
}
