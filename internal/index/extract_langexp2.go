package index

// Regex extractors for the second language-expansion wave (examples/
// coverage for the Rust-era language list): crystal, cuda, cypher, elm,
// erlang, fsharp, glsl, hlsl, nim, ocaml, sql (pgsql/tsql/plsql),
// powershell, qsharp, solidity, systemverilog, verilog, zig.
//
// Source of truth for extensions, per-language element kinds and LSP rows is
// the Rust registries at f7624143^ — src/indexer/lang/registry.rs
// (NodeKinds per language) and src/lsp/registry.rs. Cypher has no Rust row
// (src/graph/export.rs only *emits* Cypher), so its extractor is defined
// here over the graph-query surface: node labels and relationship types.
//
// Wiring: extract.go's switch default already routes unknown tags to
// matchLangExp (extract_langexp.go); that dispatcher's default branch
// delegates here, so extract.go stays untouched and built-ins never reach
// this file.
//
// Element kinds follow the shared taxonomy (function, method, class, type,
// import) with one SQL extension: table/view/procedure/class(package), the
// element types Rust's dedicated sql.rs extractor used for DDL (it also
// emitted per-column elements and REFERENCES relationships; the regex tier
// names tables/views/routines but does not parse column lists).
//
// Documented ceilings (same class as the built-in and wave-1 extractors):
// bodies end at the next element start, multi-line signatures/decorators are
// not resolved, top-level declarations are anchored at column 0 where the
// language's syntax allows indented nested definitions (fsharp, ocaml, elm
// record fields), backtick-quoted Nim operators and escaped Cypher labels
// are not matched, and language-specific noise is bounded by keyword
// filters.

import (
	"regexp"
	"strings"
)

var (
	// Crystal -------------------------------------------------------------
	crRequireRe = regexp.MustCompile(`^\s*require\s+"([^"]+)"`)
	crTypeRe    = regexp.MustCompile(`^\s*(?:abstract\s+)?(?:class|module|struct|enum)\s+([A-Z][\w:]*)`)
	crDefRe     = regexp.MustCompile(`^\s*(?:private\s+|protected\s+)?def\s+(?:self\.)?([A-Za-z_]\w*[?!]?)`)

	// CUDA ----------------------------------------------------------------
	// Kernel/host function definitions carry execution-space qualifiers and
	// an optional `extern "C"` linkage before the return type. Prototypes
	// end in ';' and are skipped (cFuncRe is column-0 anchored and has no
	// leading-whitespace prefix, so `vec_add<<<...>>>` launch lines and
	// assignments never match).
	cuFuncRe = regexp.MustCompile(`^\s*(?:(?:__global__|__device__|__host__|__managed__|extern\s+"C"|static|inline|template\s*<[^>]*>)\s+)*` +
		`[A-Za-z_][\w\s\*<>,:&\[\]]*?[\s\*]([A-Za-z_]\w*)\s*\(`)

	// Cypher --------------------------------------------------------------
	cypLabelRe = regexp.MustCompile(`\(\s*(?:[A-Za-z_]\w*\s*)?:\s*([A-Z][A-Za-z0-9_]*)`)
	cypRelRe   = regexp.MustCompile(`-\[\s*(?:[A-Za-z_]\w*\s*)?:\s*([A-Z][A-Za-z0-9_]*)`)

	// Elm -----------------------------------------------------------------
	elmModuleRe = regexp.MustCompile(`^module\s+([A-Z][\w.]*)`)
	elmImportRe = regexp.MustCompile(`^import\s+(?:qualified\s+)?([A-Z][\w.]*)`)
	elmTypeRe   = regexp.MustCompile(`^type\s+(?:alias\s+)?([A-Z][\w']*)`)
	elmSigRe    = regexp.MustCompile(`^([a-z][\w']*)\s*:`)
	elmDefRe    = regexp.MustCompile(`^([a-z][\w']*)\s+[^=(\s][^=]*=`)

	// Erlang --------------------------------------------------------------
	erlModuleRe  = regexp.MustCompile(`^\s*-\s*module\s*\(\s*([a-z]\w*)\s*\)`)
	erlIncludeRe = regexp.MustCompile(`^\s*-\s*(?:include|include_lib)\s*\(\s*"([^"]+)"`)
	erlImportRe  = regexp.MustCompile(`^\s*-\s*import\s*\(\s*([a-z]\w*)`)
	erlFuncRe    = regexp.MustCompile(`^([a-z]\w*)\s*\([^)]*\)\s*(?:when\b.*?)?(?:->|;)`)

	// F# ------------------------------------------------------------------
	fsOpenRe   = regexp.MustCompile(`^\s*open\s+([A-Za-z_][\w.]*)`)
	fsModuleRe = regexp.MustCompile(`^(?:module|namespace)\s+([A-Za-z_][\w.]*)`)
	fsTypeRe   = regexp.MustCompile(`^type\s+([A-Z][\w']*)`)
	// A `let` with an argument before '=' is a function; `let x = ...` is a
	// value binding and is deliberately skipped.
	fsLetRe = regexp.MustCompile(`^let\s+(?:rec\s+)?(?:mutable\s+)?([a-zA-Z_]\w*)\s+[^=\s]`)

	// HLSL ----------------------------------------------------------------
	hlTypeRe = regexp.MustCompile(`^\s*(?:struct|class)\s+([A-Za-z_]\w*)`)

	// Nim -----------------------------------------------------------------
	nimImportRe   = regexp.MustCompile(`^\s*(?:import|include|from)\s+([\w/]*[\w])`)
	nimCallableRe = regexp.MustCompile(`^\s*(?:proc|func|method|iterator|template|macro|converter)\s+` +
		`([A-Za-z_]\w*)\s*\*?(?:\[[^\]]*\])?\s*[([]`)
	// Type sections declare `Name = object|ref|distinct|enum`, either inline
	// after `type` or indented under it.
	nimTypeRe = regexp.MustCompile(`^\s*(?:type\s+)?([A-Z]\w*)\s*\*?\s*=\s*(?:object|ref|distinct|enum)\b`)

	// OCaml ---------------------------------------------------------------
	ocOpenRe   = regexp.MustCompile(`^\s*open\s+([A-Z][\w.]*)`)
	ocModuleRe = regexp.MustCompile(`^\s*module\s+(?:rec\s+)?([A-Z][\w']*)\s*(?:=|:)`)
	ocClassRe  = regexp.MustCompile(`^\s*class\s+(?:virtual\s+)?(?:\[[^\]]*\]\s*)?([a-z_][\w']*)`)
	ocMethodRe = regexp.MustCompile(`^\s*method\s+([a-z_][\w']*)`)
	ocFuncRe   = regexp.MustCompile(`^let\s+(?:rec\s+)?([a-z_][\w']*)\s+[^=\s]`)
	// Type declarations (Rust's ocaml kinds have no type row; the Go taxonomy
	// has one, and .mli files are mostly type signatures).
	ocTypeRe = regexp.MustCompile(`^\s*type\s+(?:'[a-z]\w*\s+)*([a-z_]\w*)`)

	// SQL (pgsql / tsql / plsql) ------------------------------------------
	sqlTableRe   = regexp.MustCompile(`(?i)^\s*CREATE\s+(?:GLOBAL\s+TEMPORARY\s+|TEMP(?:ORARY)?\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([\w".\[\]$]+)`)
	sqlViewRe    = regexp.MustCompile(`(?i)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?(?:MATERIALIZED\s+)?VIEW\s+([\w".\[\]$]+)`)
	sqlFuncRe    = regexp.MustCompile(`(?i)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?FUNCTION\s+([\w".\[\]$]+)`)
	sqlProcRe    = regexp.MustCompile(`(?i)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?PROC(?:EDURE)?\s+([\w".\[\]$]+)`)
	sqlPackageRe = regexp.MustCompile(`(?i)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?PACKAGE\s+(?:BODY\s+)?([\w".\[\]$]+)`)
	sqlMemberRe  = regexp.MustCompile(`(?i)^\s*(FUNCTION|PROCEDURE|PROC)\s+([\w".\[\]$]+)\s*\(`)

	// PowerShell ----------------------------------------------------------
	psUsingRe = regexp.MustCompile(`^\s*using\s+(?:module|namespace|assembly)\s+([\w./\\-]+)`)
	psClassRe = regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)`)
	psFuncRe  = regexp.MustCompile(`^\s*function\s+([A-Za-z_][\w-]*)`)

	// Q# ------------------------------------------------------------------
	qsOpenRe     = regexp.MustCompile(`^\s*open\s+([A-Za-z_][\w.]*)`)
	qsNsRe       = regexp.MustCompile(`^\s*namespace\s+([A-Za-z_][\w.]*)`)
	qsCallableRe = regexp.MustCompile(`^\s*(?:(?:internal|public|private)\s+)*(?:operation|function)\s+([A-Za-z_]\w*)\s*[(<]`)

	// Solidity ------------------------------------------------------------
	solImportRe   = regexp.MustCompile(`^\s*import\s+(?:[^;"']*?\s+from\s+)?["']([^"']+)["']`)
	solContractRe = regexp.MustCompile(`^\s*(?:abstract\s+)?(?:contract|interface|library)\s+([A-Za-z_]\w*)`)
	solTypeRe     = regexp.MustCompile(`^\s*(?:struct|enum)\s+([A-Za-z_]\w*)`)
	solFuncRe     = regexp.MustCompile(`^\s*function\s+([A-Za-z_]\w*)\s*\(`)
	solCtorRe     = regexp.MustCompile(`^\s*constructor\s*\(`)

	// Verilog / SystemVerilog ---------------------------------------------
	hdlModuleRe = regexp.MustCompile(`^\s*module\s+([A-Za-z_]\w*)`)
	hdlClassRe  = regexp.MustCompile(`^\s*(?:virtual\s+)?class\s+([A-Za-z_]\w*)`)
	hdlFuncRe   = regexp.MustCompile(`^\s*(?:function|task)\s+(?:automatic\s+)?(?:[\w:\[\]\s$*-]+?\s+)?([A-Za-z_]\w*)\s*[(;]`)

	// Zig -----------------------------------------------------------------
	zigImportRe = regexp.MustCompile(`^\s*(?:pub\s+)?const\s+[\w.]+\s*=\s*@import\("([^"]+)"\)`)
	zigTypeRe   = regexp.MustCompile(`^\s*(?:pub\s+)?const\s+([A-Z]\w*)\s*=\s*(?:struct|enum|union|opaque|error)\b`)
	zigFuncRe   = regexp.MustCompile(`^\s*(?:pub\s+)?(?:export\s+)?fn\s+([A-Za-z_]\w*)`)
	zigTestRe   = regexp.MustCompile(`^\s*test\s+"([^"]+)"`)
)

// matchLangExp2 dispatches the wave-2 regex matchers; ok=false for languages
// this file does not own (built-ins and wave-1 ids).
func matchLangExp2(lang string, lines []string) (ms []match, ok bool) {
	switch lang {
	case "crystal":
		ms = matchCrystal(lines)
	case "cuda":
		ms = matchCuda(lines)
	case "cypher":
		ms = matchCypher(lines)
	case "elm":
		ms = matchElm(lines)
	case "erlang":
		ms = matchErlang(lines)
	case "fsharp":
		ms = matchFSharp(lines)
	case "glsl":
		ms = matchGLSL(lines)
	case "hlsl":
		ms = matchHLSL(lines)
	case "nim":
		ms = matchNim(lines)
	case "ocaml":
		ms = matchOCaml(lines)
	case "sql":
		ms = matchSQL(lines)
	case "powershell":
		ms = matchPowerShell(lines)
	case "qsharp":
		ms = matchQSharp(lines)
	case "solidity":
		ms = matchSolidity(lines)
	case "systemverilog", "verilog":
		ms = matchVerilog(lines)
	case "zig":
		ms = matchZig(lines)
	default:
		return nil, false
	}
	return ms, true
}

// dedupeMatches keeps the first hit per (kind, name): Erlang function
// clauses, SQL package spec/body pairs, repeated Cypher labels and Zig's
// per-file `test` blocks repeat the same declaration.
func dedupeMatches(ms []match) []match {
	seen := map[string]bool{}
	out := ms[:0]
	for _, m := range ms {
		key := m.kind + ":" + m.name
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, m)
	}
	return out
}

// matchCrystal extracts require imports, class/module/struct/enum
// declarations and defs (including `def self.`) as methods.
func matchCrystal(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := crRequireRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := crTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := crDefRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "method", name: m[1], line: i + 1})
		}
	}
	return ms
}

// matchCuda extracts __global__/__device__/__host__ kernel and device
// function definitions (prototypes skipped), struct/enum/union plus typedef
// types, and #include imports.
func matchCuda(lines []string) []match {
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
		if m := cuFuncRe.FindStringSubmatch(l); m != nil && !cControlKw[m[1]] &&
			!strings.HasSuffix(strings.TrimRight(l, " \t\r"), ";") {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
		}
	}
	return ms
}

// matchCypher extracts node labels and relationship types as types — the
// graph-query equivalent of declared entities. Comments (`//`) carry no
// parenthesised labels or relationship arrows, so they are never picked up;
// escaped `\`Label\“ identifiers are a documented ceiling.
func matchCypher(lines []string) []match {
	var ms []match
	for i, l := range lines {
		for _, m := range cypLabelRe.FindAllStringSubmatch(l, -1) {
			ms = append(ms, match{kind: "type", name: m[1], line: i + 1})
		}
		for _, m := range cypRelRe.FindAllStringSubmatch(l, -1) {
			ms = append(ms, match{kind: "type", name: m[1], line: i + 1})
		}
	}
	return dedupeMatches(ms)
}

// matchElm extracts the module declaration, imports, `type`/`type alias`
// declarations and top-level functions. Signatures and their defining
// equations collapse into one element (the Haskell treatment); indented
// record fields use the same `name :` shape, so signature matching is
// anchored at column 0.
func matchElm(lines []string) []match {
	var ms []match
	sig := map[string]bool{}
	for i, l := range lines {
		if m := elmModuleRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := elmImportRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := elmTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "type", name: m[1], line: i + 1})
			continue
		}
		if m := elmSigRe.FindStringSubmatch(l); m != nil {
			sig[m[1]] = true
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
			continue
		}
		if m := elmDefRe.FindStringSubmatch(l); m != nil && !sig[m[1]] {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
		}
	}
	return ms
}

// matchErlang extracts the module attribute as the file's container,
// include/include_lib/import attributes, and function clause heads (one
// element per function: clauses share a name and collapse).
func matchErlang(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := erlModuleRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := erlIncludeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := erlImportRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := erlFuncRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
		}
	}
	return dedupeMatches(ms)
}

// matchFSharp extracts open imports, module/namespace declarations, type
// definitions and top-level `let`-bound functions at column 0 (nested
// binding values for a name without parameters are values, not functions).
func matchFSharp(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := fsOpenRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := fsModuleRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := fsTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "type", name: m[1], line: i + 1})
			continue
		}
		if m := fsLetRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
		}
	}
	return ms
}

// matchGLSL extracts shader entry points and helper functions (Rust GLSL
// kinds: functions only — in/out/uniform declarations are not elements).
func matchGLSL(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := cFuncRe.FindStringSubmatch(l); m != nil && !cControlKw[m[2]] &&
			!strings.HasSuffix(strings.TrimRight(l, " \t\r"), ";") {
			ms = append(ms, match{kind: "function", name: m[2], line: i + 1})
		}
	}
	return ms
}

// matchHLSL extracts struct/class declarations, #include imports and
// function definitions (Rust HLSL kinds: function_definition,
// class_specifier/struct_specifier, preproc_include). cbuffer/tbuffer blocks
// and semantic-annotated parameters are not elements.
func matchHLSL(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := cIncludeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := hlTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := cFuncRe.FindStringSubmatch(l); m != nil && !cControlKw[m[2]] &&
			!strings.HasSuffix(strings.TrimRight(l, " \t\r"), ";") {
			ms = append(ms, match{kind: "function", name: m[2], line: i + 1})
		}
	}
	return ms
}

// matchNim extracts import/include/from imports, proc/func/method/iterator/
// template/macro/converter declarations (export marker and generic
// parameters tolerated) and object/ref/distinct/enum type definitions, both
// the inline `type Name = object` form and the indented section form.
func matchNim(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := nimImportRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := nimCallableRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
			continue
		}
		if m := nimTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "type", name: m[1], line: i + 1})
		}
	}
	return ms
}

// matchOCaml extracts open imports, module bindings and class definitions,
// class methods, type declarations, and top-level `let`-bound functions at
// column 0 (functions nested in `module ... = struct` bodies are not
// resolved by the regex tier — a documented ceiling).
func matchOCaml(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := ocOpenRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := ocModuleRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := ocClassRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := ocMethodRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "method", name: m[1], line: i + 1})
			continue
		}
		if m := ocTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "type", name: m[1], line: i + 1})
			continue
		}
		if m := ocFuncRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
		}
	}
	return ms
}

// matchSQL extracts DDL/DML routine declarations for all three dialects:
// tables, views, functions, procedures and PL/SQL packages (the container;
// its spec and body members are extracted as functions/procedures). Names
// are reduced to their leaf identifier, so `public.calc_total` and
// `dbo.GetUser` yield calc_total and GetUser. The PL/SQL spec and body
// declare the same routines twice; the first hit wins.
func matchSQL(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := sqlTableRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "table", name: sqlLeaf(m[1]), line: i + 1})
			continue
		}
		if m := sqlViewRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "view", name: sqlLeaf(m[1]), line: i + 1})
			continue
		}
		if m := sqlFuncRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "function", name: sqlLeaf(m[1]), line: i + 1})
			continue
		}
		if m := sqlProcRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "procedure", name: sqlLeaf(m[1]), line: i + 1})
			continue
		}
		if m := sqlPackageRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: sqlLeaf(m[1]), line: i + 1})
			continue
		}
		if m := sqlMemberRe.FindStringSubmatch(l); m != nil {
			kind := "function"
			if !strings.EqualFold(m[1], "FUNCTION") {
				kind = "procedure"
			}
			ms = append(ms, match{kind: kind, name: sqlLeaf(m[2]), line: i + 1})
		}
	}
	return dedupeMatches(ms)
}

// sqlLeaf reduces a possibly schema-qualified SQL identifier to its final
// identifier component (quoted identifiers lose their quotes).
func sqlLeaf(name string) string {
	name = strings.Trim(name, `"[].`)
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	return strings.Trim(name, `"[]`)
}

// matchPowerShell extracts using module/namespace imports, class
// declarations and functions (PowerShell verb-noun names keep their hyphen).
func matchPowerShell(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := psUsingRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := psClassRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := psFuncRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
		}
	}
	return ms
}

// matchQSharp extracts open imports, namespace declarations and callables
// (operation/function, Rust callable_decl).
func matchQSharp(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := qsOpenRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := qsNsRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := qsCallableRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
		}
	}
	return ms
}

// matchSolidity extracts import directives (plain path, `import {X} from
// "path"`, `import * as X from "path"`), contract/interface/library
// declarations as classes, struct/enum declarations as types, and functions
// plus constructors as methods (an in-contract function qualifies through
// its contract via the shared parent pass).
func matchSolidity(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := solImportRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := solContractRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := solTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "type", name: m[1], line: i + 1})
			continue
		}
		if m := solFuncRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "method", name: m[1], line: i + 1})
			continue
		}
		if solCtorRe.MatchString(l) {
			ms = append(ms, match{kind: "method", name: "constructor", line: i + 1})
		}
	}
	return ms
}

// matchVerilog extracts modules and SystemVerilog classes as containers and
// function/task declarations as functions, including the legacy
// no-port-list form (`function [WIDTH-1:0] mask;`). `extern` and `pure
// virtual` prototypes are skipped: unlike C, a SystemVerilog function
// *definition* header also ends in ';' (its body closes with endfunction).
func matchVerilog(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := hdlModuleRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := hdlClassRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[1], line: i + 1})
			continue
		}
		if m := hdlFuncRe.FindStringSubmatch(l); m != nil && !declIsExtern(l) {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
		}
	}
	return ms
}

// declIsExtern reports prototype-only declarations in HDL/system headers.
func declIsExtern(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "extern ") || strings.HasPrefix(trimmed, "pure virtual ")
}

// matchZig extracts @import bindings, container types declared as
// struct/enum/union/opaque/error, fn declarations (pub/export tolerated) and
// test blocks (Rust Zig kinds: function_declaration/test_declaration,
// struct/enum declarations).
func matchZig(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := zigImportRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "import", name: m[1], line: i + 1})
			continue
		}
		if m := zigTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "type", name: m[1], line: i + 1})
			continue
		}
		if m := zigFuncRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
			continue
		}
		if m := zigTestRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
		}
	}
	return ms
}
