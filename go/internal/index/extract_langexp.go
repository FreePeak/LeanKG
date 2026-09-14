package index

// Regex extractors for the expanded language set ported from the Rust
// indexer registry (src/indexer/lang/registry.rs at f7624143^): c, cpp,
// csharp, php, ruby, scala, perl, lua, haskell, elixir.
//
// This file is standalone on purpose: extract.go's dispatch switch and
// index.go's extLang map are integrator-owned, so wiring happens through
// one line in extractFileAs's switch default:
//
//	default:
//		ms = matchLangExp(lang, lines)
//
// Element kinds follow the Go engine taxonomy: functions, methods (with a
// receiver/parent type for qualified names), classes/types, and imports
// (kind "import", name = imported module or path) — the Rust reference
// extracted imports per language, so the regex tier does too.
//
// Documented ceilings (same class as the built-in extractors): bodies end
// at the next element start, multi-line return types / decorators are not
// resolved, and language-specific noise is bounded by keyword filters.

import (
	"regexp"
	"strings"
)

// C -------------------------------------------------------------------------

var (
	cFuncRe = regexp.MustCompile(`^([A-Za-z_][\w\s\*]*[\s\*])([A-Za-z_]\w*)\s*\(`)
	cTypeRe = regexp.MustCompile(`^(struct|enum|union)\s+([A-Za-z_]\w*)`)
	// typedef aliases: the name is the last identifier before the semicolon
	// (`typedef struct Point PointT;` yields PointT, not Point — the struct
	// itself is already matched at its own line).
	cTypedefRe = regexp.MustCompile(`^typedef\s+[^;]*[\s\*]([A-Za-z_]\w*)\s*;`)
	cIncludeRe = regexp.MustCompile(`^\s*#\s*include\s*[<"]([^">]+)[">]`)
	cControlKw = map[string]bool{"if": true, "else": true, "while": true,
		"for": true, "switch": true, "return": true, "sizeof": true, "do": true}

	cppClassRe = regexp.MustCompile(`^\s*(?:template\s*<[^>]*>\s*)?(?:class|struct)\s+([A-Za-z_]\w*)`)
	cppUsingRe = regexp.MustCompile(`^\s*using\s+(?:namespace\s+)?([A-Za-z_][\w:]*)`)

	csTypeRe   = regexp.MustCompile(`^\s*(?:(?:public|private|protected|internal|static|sealed|abstract|partial|readonly|ref|unsafe)\s+)*(?:class|interface|struct|record|enum)\s+([A-Za-z_]\w*)`)
	csMethodRe = regexp.MustCompile(`^\s*(?:(?:public|private|protected|internal|static|sealed|abstract|override|virtual|async|unsafe|extern|new|partial)\s+)*(?:(?:[\w<>\[\],.]+)\s+)?([A-Za-z_]\w*)\s*\([^;{)]*\)\s*(?:\{|=>)`)
	csUsingRe  = regexp.MustCompile(`^\s*using\s+(?:static\s+)?([A-Za-z_][\w.]*)\s*;`)

	phpFuncRe    = regexp.MustCompile(`^\s*function\s+(?:&\s*)?([A-Za-z_]\w*)\s*\(`)
	phpMethodRe  = regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|final|abstract)\s+)+function\s+(?:&\s*)?([A-Za-z_]\w*)\s*\(`)
	phpTypeRe    = regexp.MustCompile(`^\s*(?:abstract\s+|final\s+|readonly\s+)*(?:class|interface|trait|enum)\s+([A-Za-z_]\w*)`)
	phpUseRe     = regexp.MustCompile(`^\s*use\s+([A-Za-z_\\][\w\\]*)`)
	phpRequireRe = regexp.MustCompile(`^\s*(?:require|require_once)\s*\(?\s*['"]([^'"]+)['"]`)

	rbDefRe     = regexp.MustCompile(`^\s*def\s+(?:self\.)?([A-Za-z_]\w*[?!]?)`)
	rbTypeRe    = regexp.MustCompile(`^\s*(?:class|module)\s+([A-Z][\w:]*)`)
	rbRequireRe = regexp.MustCompile(`^\s*require(?:_relative)?\s+['"]([^'"]+)['"]`)

	scalaTypeRe   = regexp.MustCompile(`^\s*(?:(?:abstract|sealed|final|case|implicit|lazy|open|transparent|private|protected)\s+)*(?:class|object|trait|enum)\s+([A-Za-z_]\w*)`)
	scalaFuncRe   = regexp.MustCompile(`^\s*def\s+([A-Za-z_]\w*)`)
	scalaImportRe = regexp.MustCompile(`^\s*import\s+([\w.{}*]+)`)

	plSubRe     = regexp.MustCompile(`^\s*sub\s+([A-Za-z_]\w*)`)
	plPackageRe = regexp.MustCompile(`^\s*package\s+([A-Za-z_][\w:]*)`)
	plUseRe     = regexp.MustCompile(`^\s*use\s+([A-Za-z_][\w:]*)`)
	plRequireRe = regexp.MustCompile(`^\s*require\s+([A-Za-z_][\w:]*)`)

	luaFuncRe    = regexp.MustCompile(`^\s*function\s+([\w.]+)\s*\(`)
	luaColonRe   = regexp.MustCompile(`^\s*function\s+([\w.]+)\s*:\s*([\w]+)\s*\(`)
	luaLocalRe   = regexp.MustCompile(`^\s*local\s+function\s+([A-Za-z_]\w*)`)
	luaRequireRe = regexp.MustCompile(`^\s*(?:local\s+)?(?:[A-Za-z_][\w.]*\s*=\s*)?require\s*\(?\s*['"]([^'"]+)['"]`)

	hsSigRe    = regexp.MustCompile(`^\s*([a-z_][\w']*(?:\s*,\s*[a-z_][\w']*)*)\s*::`)
	hsTypeRe   = regexp.MustCompile(`^(?:data|newtype|type)\s+(?:family\s+)?([A-Z][\w']*)`)
	hsClassRe  = regexp.MustCompile(`^class\s+(?:\([^)]*\)\s*=>\s*)?([A-Z][\w']*)`)
	hsImportRe = regexp.MustCompile(`^import\s+(?:qualified\s+)?([A-Z][\w.]+)`)
	hsEqRe     = regexp.MustCompile(`^([a-z_][\w']*)\s+[^=]+=\s`)

	exDefRe    = regexp.MustCompile(`^\s*def(?:p|macro|macrop|guard)?\s+([a-z_][A-Za-z0-9_]*[?!]?)`)
	exModuleRe = regexp.MustCompile(`^\s*def(?:module|protocol|impl)\s+([\w.]+)`)
	exImportRe = regexp.MustCompile(`^\s*(?:require|import|alias|use)\s+([A-Za-z_][\w.!?]*)`)
)

// matchLangExp dispatches the expanded-language regex matchers; ok=false for
// languages this file does not own (the built-in switch handles those).
func matchLangExp(lang string, lines []string) (ms []match, ok bool) {
	switch lang {
	case "c":
		ms = matchC(lines)
	case "cpp":
		ms = matchCpp(lines)
	case "csharp":
		ms = matchCSharp(lines)
	case "php":
		ms = matchPHP(lines)
	case "ruby":
		ms = matchRuby(lines)
	case "scala":
		ms = matchScala(lines)
	case "perl":
		ms = matchPerl(lines)
	case "lua":
		ms = matchLua(lines)
	case "haskell":
		ms = matchHaskell(lines)
	case "elixir":
		ms = matchElixir(lines)
	default:
		// Language-expansion wave 2 (extract_langexp2.go): one delegated
		// hop keeps extract.go's call site untouched.
		return matchLangExp2(lang, lines)
	}
	return ms, true
}

// matchC extracts functions (return type + name + params at column 0; a
// trailing ';' is a prototype, not a definition), struct/enum/union types,
// typedef aliases, and #include imports.
func matchC(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := cIncludeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := cTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "type", name: m[2], line: i + 1})
			continue
		}
		if m := cTypedefRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "type", name: m[1], line: i + 1})
			continue
		}
		if m := cFuncRe.FindStringSubmatch(l); m != nil && !cControlKw[m[2]] &&
			!strings.HasSuffix(strings.TrimRight(l, " \t\r"), ";") {
			ms = append(ms, match{kind: "function", name: m[2], line: i + 1})
		}
	}
	return ms
}

// matchCpp is matchC plus class/struct declarations and using-declarations.
func matchCpp(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := cIncludeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := cppUsingRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := cppClassRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := cTypedefRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "type", name: m[1], line: i + 1})
			continue
		}
		if m := cFuncRe.FindStringSubmatch(l); m != nil && !cControlKw[m[2]] &&
			!strings.HasSuffix(strings.TrimRight(l, " \t\r"), ";") {
			ms = append(ms, match{kind: "function", name: m[2], line: i + 1})
		}
	}
	return ms
}

// matchCSharp extracts classes/interfaces/structs/records/enums, methods with
// a body, and using-directives (only the `using X;` statement form —
// `using var x = ...` and resource acquisition are skipped).
func matchCSharp(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := csUsingRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := csTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := csMethodRe.FindStringSubmatch(l); m != nil && !javaKeyword(m[1]) {
			ms = append(ms, match{kind: "method", name: m[1], line: i + 1})
		}
	}
	return ms
}

// matchPHP extracts plain functions, visibility-modified methods,
// class/interface/trait/enum declarations, use-imports and require* imports.
// Ceiling: a class-internal trait `use Greets;` is indistinguishable from a
// file-level use-import in the regex tier and is reported as an import.
func matchPHP(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := phpRequireRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := phpUseRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := phpTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := phpMethodRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "method", name: m[1], line: i + 1})
			continue
		}
		if m := phpFuncRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
		}
	}
	return ms
}

// matchRuby extracts defs as methods (Rust kinds: method, singleton_method),
// CamelCase class/module declarations, and require/require_relative imports.
func matchRuby(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := rbRequireRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := rbTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := rbDefRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "method", name: m[1], line: i + 1})
		}
	}
	return ms
}

// matchScala extracts class/object/trait/enum declarations, defs as methods
// (methods inside containers qualify as Type.name, mirroring the Java
// extractor), and import statements.
func matchScala(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := scalaImportRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := scalaTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := scalaFuncRe.FindStringSubmatch(l); m != nil && !javaKeyword(m[1]) {
			ms = append(ms, match{kind: "method", name: m[1], line: i + 1})
		}
	}
	return ms
}

// matchPerl extracts package declarations as classes, subs as functions, and
// use/require module imports.
func matchPerl(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := plUseRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := plRequireRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := plPackageRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := plSubRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
		}
	}
	return ms
}

// matchLua extracts global/dotted functions (`function M.setup`), colon
// methods (`function M:get` — receiver carried for qualified names), local
// functions, and require imports.
func matchLua(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := luaRequireRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := luaColonRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "method", name: m[2], line: i + 1, recv: m[1]})
			continue
		}
		if m := luaFuncRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
			continue
		}
		if m := luaLocalRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
		}
	}
	return ms
}

// matchHaskell extracts top-level type signatures (including multi-name
// `f, g :: ...`) as functions, data/newtype/type declarations as types,
// class declarations, import statements, and bare equations for functions
// without a signature. Equations whose name already matched a signature are
// dropped (same qualified name, one element).
func matchHaskell(lines []string) []match {
	var ms []match
	seen := map[string]bool{}
	for i, l := range lines {
		if m := hsImportRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := hsTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "type", name: m[1], line: i + 1})
			continue
		}
		if m := hsClassRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := hsSigRe.FindStringSubmatch(l); m != nil {
			for _, n := range strings.Split(m[1], ",") {
				n = strings.TrimSpace(n)
				if n == "" {
					continue
				}
				ms = append(ms, match{kind: "function", name: n, line: i + 1})
				seen[n] = true
			}
			continue
		}
		if m := hsEqRe.FindStringSubmatch(l); m != nil && !seen[m[1]] {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
			seen[m[1]] = true
		}
	}
	return ms
}

// matchElixir extracts defmodule/defprotocol/defimpl as classes, def/defp/
// defmacro*/defguard as functions, and require/import/alias/use as imports.
func matchElixir(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := exImportRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := exModuleRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := exDefRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
		}
	}
	return ms
}
