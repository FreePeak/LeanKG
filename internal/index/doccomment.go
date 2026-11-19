package index

import "strings"

// Leading doc comments (RS-10). Extractors start an element at its
// declaration line, so the comment block directly above it — the most
// natural-language text in a codebase — belonged to no element: neither the
// keyword rung nor the embeddings could see it (a paraphrase of a function's
// own doc comment ranked it 225th of 698). boundContent now starts the
// element's Content at that block (docCommentStart is the one mechanism for
// it); line_start stays the declaration line, so navigation and LSP
// enrichment are unaffected.

// commentStyle is how a language spells line comments.
type commentStyle struct {
	line  []string // line-comment prefixes
	block bool     // C-style /* */ blocks (and their `*` continuation lines)
	attrs []string // attribute / annotation / decorator prefixes that sit between doc and declaration
}

var (
	cStyle    = commentStyle{line: []string{"//"}, block: true, attrs: []string{"@"}}
	hashStyle = commentStyle{line: []string{"#"}, attrs: []string{"@"}}
)

// commentStyles maps the indexer's language tags to their comment syntax.
// Languages absent here capture no doc block.
var commentStyles = map[string]commentStyle{
	"go":   {line: []string{"//"}, block: true},
	"rust": {line: []string{"//"}, block: true, attrs: []string{"#[", "#!["}},
	"c":    cStyle, "cpp": cStyle, "cuda": cStyle, "objc": cStyle, "glsl": cStyle, "hlsl": cStyle,
	"java": cStyle, "kotlin": cStyle, "scala": cStyle, "swift": cStyle, "dart": cStyle,
	"js": cStyle, "jsx": cStyle, "ts": cStyle, "tsx": cStyle, "php": cStyle,
	"solidity": cStyle, "zig": {line: []string{"//"}},
	"csharp":  {line: []string{"//"}, block: true, attrs: []string{"["}},
	"fsharp":  {line: []string{"//"}, block: true, attrs: []string{"[<"}},
	"verilog": cStyle, "systemverilog": cStyle, "qsharp": cStyle,
	"py": hashStyle, "ruby": hashStyle, "perl": hashStyle, "crystal": hashStyle,
	"nim": hashStyle, "powershell": hashStyle,
	"elixir": {line: []string{"#"}, attrs: []string{"@"}},
	"lua":    {line: []string{"--"}}, "sql": {line: []string{"--"}}, "haskell": {line: []string{"--"}},
	"elm": {line: []string{"--"}}, "cypher": {line: []string{"//"}},
	"erlang": {line: []string{"%"}}, "ocaml": {block: true},
}

// isAttrLine reports whether a trimmed line is an attribute, annotation or
// decorator for style. Such a line may sit between a doc block and its
// declaration without ending the block.
func isAttrLine(t string, style commentStyle) bool {
	for _, p := range style.attrs {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}
