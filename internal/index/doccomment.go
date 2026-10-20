package index

import "strings"

// Leading doc comments (RS-10). Extractors start an element at its
// declaration line, so the comment block directly above it — the most
// natural-language text in a codebase — belonged to no element: neither the
// keyword rung nor the embeddings could see it (a paraphrase of a function's
// own doc comment ranked it 225th of 698). boundContent now prepends that
// block to the element's Content; line_start stays the declaration line, so
// navigation and LSP enrichment are unaffected.

// maxDocLines bounds the captured block (license headers above the first
// declaration are cut off by the blank line that follows them anyway).
const maxDocLines = 40

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

// isDocLine reports whether a trimmed, non-empty line is part of a leading
// comment/attribute block for style.
func isDocLine(t string, style commentStyle) bool {
	for _, p := range style.line {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	if style.block && (strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") || strings.HasSuffix(t, "*/")) {
		return true
	}
	for _, p := range style.attrs {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// leadingDoc returns the contiguous comment/attribute block directly above
// the 1-based line start, or "" when there is none. A blank line ends it.
func leadingDoc(lines []string, start int, lang string) string {
	style, ok := commentStyles[lang]
	if !ok || start <= 1 || start-1 > len(lines) {
		return ""
	}
	var block []string
	for i := start - 2; i >= 0 && len(block) < maxDocLines; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" || !isDocLine(t, style) {
			break
		}
		block = append(block, lines[i])
	}
	if len(block) == 0 {
		return ""
	}
	// Only attributes, no comment: nothing worth prepending.
	hasComment := false
	for _, l := range block {
		t := strings.TrimSpace(l)
		attr := false
		for _, p := range style.attrs {
			if strings.HasPrefix(t, p) {
				attr = true
				break
			}
		}
		if !attr {
			hasComment = true
			break
		}
	}
	if !hasComment {
		return ""
	}
	for i, j := 0, len(block)-1; i < j; i, j = i+1, j-1 {
		block[i], block[j] = block[j], block[i]
	}
	return strings.Join(block, "\n")
}
