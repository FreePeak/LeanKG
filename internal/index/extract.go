package index

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/FreePeak/LeanKG/internal/store"
)

// maxContent bounds one element's stored content, in characters.
const maxContent = 8000

// match is one raw regex hit before line-range resolution.
type match struct {
	kind   string // "function", "method", "type", "class", "constructor", "constant", "doc"
	name   string
	line   int    // 1-based
	recv   string // Go receiver type
	indent int    // Python indentation; Markdown heading level
}

// indexedElem is an extracted element before conversion to store.Element.
type indexedElem struct {
	name    string
	etype   string // function | method | type | class | constructor | constant | doc
	lang    string
	start   int // 1-based
	end     int
	content string
	docLen  int // bytes of content that are the prepended leading doc block (RS-10)
	parent  int // index into the same slice, -1 = none
	qn      string
	recv    string   // Go method receiver type
	calls   []string // grammar-derived call targets (objc message sends)
}

// Package-level compiled regexes (documented ceiling: regex extraction,
// tree-sitter port is W2).
var (
	// Go
	goFuncRe = regexp.MustCompile(`^func\s+(?:\(([^)]*)\)\s*)?([A-Za-z_]\w*)\s*\(`)
	goTypeRe = regexp.MustCompile(`^type\s+([A-Za-z_]\w*)\s+(?:struct|interface)\b`)
	// Rust
	rsFnRe   = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)`)
	rsTypeRe = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(struct|enum|trait)\s+([A-Za-z_]\w*)`)
	rsImplRe = regexp.MustCompile(`^\s*impl(?:<[^>]*>)?\s+(?:[^{]*\sfor\s+)?([A-Za-z_][\w:]*)`)
	// Python
	pyFuncRe  = regexp.MustCompile(`^(\s*)(?:async\s+)?def\s+([A-Za-z_]\w*)`)
	pyClassRe = regexp.MustCompile(`^(\s*)(?:async\s+)?class\s+([A-Za-z_]\w*)`)
	// TS/JS/JSX/TSX
	tsFuncRe   = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\b\s*\*?\s*([A-Za-z_]\w*)`)
	tsClassRe  = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_]\w*)`)
	tsArrowRe  = regexp.MustCompile(`^\s*(?:export\s+)?const\s+([A-Za-z_]\w*)\s*(?::[^=]+)?=\s*(?:async\s+)?(?:\([^)]*\)|[A-Za-z_]\w*)\s*=>`)
	tsFnExprRe = regexp.MustCompile(`^\s*(?:export\s+)?const\s+([A-Za-z_]\w*)\s*(?::[^=]+)?=\s*(?:async\s+)?function\b`)
	// Markdown ATX headings (levels 1-4)
	mdHeadingRe = regexp.MustCompile(`^(#{1,4})\s+(.*\S)\s*$`)
	// Java
	javaTypeRe   = regexp.MustCompile(`^\s*(?:(?:public|protected|private|static|final|abstract|sealed|strictfp|native|synchronized|default)\s+)*(class|interface|enum|record|@interface)\s+([A-Za-z_]\w*)`)
	javaMethodRe = regexp.MustCompile(`^\s*(?:(?:public|protected|private|static|final|abstract|synchronized|native|default|strictfp)\s+)*(?:(?:<[^>]*>|[\w$<>\[\].]+)\s+)*([A-Za-z_$][\w$]*)\s*\([^;{)]*\)\s*(?:throws [\w,.\s]+)?\{`)
	// Kotlin
	ktTypeRe = regexp.MustCompile(`^\s*(?:(?:public|private|internal|protected|open|final|abstract|sealed|data|value|annotation|inner|enum)\s+)*(class|interface|object)\s+([A-Za-z_]\w*)`)
	ktFuncRe = regexp.MustCompile(`^\s*(?:(?:public|private|internal|protected|open|final|abstract|override|inline|noinline|crossinline|operator|infix|tailrec|external|suspend|expect|actual)\s+)*fun\s+(?:<[^>]*>\s*)?(?:[\w.<>\[\]?]+\s*\.\s*)?([A-Za-z_]\w*)\s*\(`)
	// Swift
	swiftFuncRe = regexp.MustCompile(`^\s*(?:(?:public|private|internal|open|fileprivate|static|class|final|mutating|nonmutating|override|convenience|required|weak|unowned|lazy)\s+)*func\s+([A-Za-z_]\w*)\s*[(<]`)
	swiftTypeRe = regexp.MustCompile(`^\s*(?:(?:public|private|internal|open|fileprivate|final|indirect)\s+)*(struct|class|enum|protocol|actor)\s+([A-Za-z_]\w*)`)
	// Objective-C
	objcTypeRe   = regexp.MustCompile(`^\s*@(interface|implementation|protocol)\s+([A-Za-z_]\w*)`)
	objcMethodRe = regexp.MustCompile(`^\s*([+-])\s*\(([^)]*)\)\s*([A-Za-z_]\w*)`)
	// Dart
	dartTypeRe = regexp.MustCompile(`^\s*(?:(?:abstract|sealed|final|base|interface|mixin|external|required|const|covariant)\s+)*(class|enum|mixin)\s+([A-Za-z_]\w*)`)
	dartFuncRe = regexp.MustCompile(`^\s*(?:[\w$<>,?\s]+\s+)?([A-Za-z_$][\w$]*)\s*\([^;{)]*\)\s*(?:(?:async|sync\s*\*)\s*)?(?:\{|=>)`)
	// Dart constructors: "Factory()", "Factory.named(int x)", "const
	// Factory.zero()", "factory Factory.create() => ...". The class name is
	// UpperCamel in Dart, which keeps ordinary lower-case call statements
	// ("setState(...)") out; a static-method call statement looks identical
	// to a named-constructor declaration, so those stay a documented ceiling
	// of the regex tier (the tstree build is exact).
	dartCtorRe = regexp.MustCompile(`^\s*(?:(?:const|factory|external)\s+)*([A-Z]\w*)(?:\.(\w+))?\s*\(`)
	// dartEnumConstantRe accepts exactly one enum value name.
	dartEnumConstantRe = regexp.MustCompile(`^[A-Za-z_$][\w$]*$`)
)

// extractFileAs extracts elements from one file's content as the given
// language tag (empty or unknown tags yield no elements). The only error is
// ctx's: a cancelled ctx stops the tree-sitter tier and is returned, while a
// grammar that fails to parse falls back to regex.
func extractFileAs(ctx context.Context, rel string, src []byte, lang string) (fileElements, error) {
	lines := strings.Split(strings.TrimSuffix(string(src), "\n"), "\n")

	// Go: the standard-library parser is the definitions tier in every build
	// (CGO-free). A file it rejects falls through to the regex tier below.
	if lang == "go" {
		if defs, imports, ok := goDefinitions(src); ok {
			return finishDefinitions(ctx, rel, src, lines, lang, defs, imports)
		}
	} else {
		// Tree-sitter tier (tstree build only): richer symbol extraction for
		// the bundled grammars, with real end lines. No grammar for the
		// language ⇒ nil ⇒ regex fallback below.
		defs, err := tsExtract(ctx, src, lang)
		if cerr := ctx.Err(); cerr != nil {
			return fileElements{}, cerr
		}
		if err == nil && len(defs) > 0 {
			return finishDefinitions(ctx, rel, src, lines, lang, defs, nil)
		}
	}

	var ms []match
	switch lang {
	case "go":
		ms = matchGo(lines)
	case "rust":
		ms = matchRust(lines)
	case "py":
		ms = matchPython(lines)
	case "ts", "tsx", "js", "jsx":
		ms = matchTS(lines)
	case "md":
		ms = matchMarkdown(lines)
	case "java":
		ms = matchJava(lines)
	case "kotlin":
		ms = matchKotlin(lines)
	case "swift":
		ms = matchSwift(lines)
	case "objc":
		ms = matchObjC(lines)
	case "dart":
		ms = matchDart(lines)
	default:
		// Expanded language set (extract_langexp.go); claims only its own ids
		// and reports ok=false otherwise, so built-ins above are untouched.
		ms, _ = matchLangExp(lang, lines)
	}
	if len(ms) == 0 {
		return fileElements{rel: rel}, nil
	}

	ends := make([]int, len(ms))
	switch lang {
	case "py":
		for i, m := range ms {
			ends[i] = pyBodyEnd(lines, m)
		}
	case "md":
		// A section extends to just before the next heading of the SAME or a
		// HIGHER level. The old rule (the next strictly HIGHER level) let a
		// `##` swallow every section that followed it, so a document's text
		// was stored once per heading that preceded it.
		ends = mdSectionEnds(ms, len(lines))
	default:
		for i := range ms {
			ends[i] = len(lines)
			if i+1 < len(ms) {
				// Stop before the next declaration's doc block, so that block
				// is not absorbed into this element.
				ends[i] = docCommentStart(lines, ms[i+1].line, lang) - 1
				for ends[i] > ms[i].line && strings.TrimSpace(lines[ends[i]-1]) == "" {
					ends[i]--
				}
			}
			if ends[i] < ms[i].line {
				ends[i] = ms[i].line
			}
		}
	}

	defs := make([]indexDef, len(ms))
	for i, m := range ms {
		defs[i] = indexDef{Kind: m.kind, Name: m.name, StartLine: m.line, EndLine: ends[i], Owner: m.recv}
	}
	fe := indexDefsToElements(rel, lang, defs)
	if lang == "md" {
		markdownContent(fe.elements, ms, ends, lines)
		return fe, nil
	}
	boundContent(fe.elements, lines)
	return fe, nil
}

// fileElements holds the extraction output for one file.
type fileElements struct {
	rel      string
	elements []indexedElem
	// imports maps each explicit Go import alias to its import path; nil for
	// other languages and for Go files with no aliased import.
	imports map[string]string
}

// indexDefsToElements is the one place definitions become indexed elements:
// the owning type rides on recv for members (method, constructor, constant),
// the parent is found by line containment and qualified names are assigned.
// Every tier (go parser, tree-sitter, regex) calls it.
func indexDefsToElements(rel, lang string, defs []indexDef) fileElements {
	fe := fileElements{rel: rel, elements: make([]indexedElem, len(defs))}
	for i, d := range defs {
		end := d.EndLine
		if end < d.StartLine {
			end = d.StartLine
		}
		e := indexedElem{
			name: d.Name, etype: d.Kind, lang: lang,
			start: d.StartLine, end: end, parent: -1,
		}
		switch d.Kind {
		case "method", "constructor", "constant":
			// A Go method's owning type is its RECEIVER, which is a sibling of
			// the method rather than an enclosing node, so line-based parent
			// detection cannot find it. A constructor is named after the
			// constructor, and an enum value carries no type. recv carries the
			// owner so qualify() spells "<file>::Type.member" without depending
			// on line-based parent detection (a one-line body has none).
			e.recv = d.Owner
		}
		fe.elements[i] = e
	}
	assignParents(fe.elements)
	qualify(fe)
	uniqueElemQNs(map[string]bool{}, fe.elements)
	return fe
}

// finishDefinitions is the shared tail of the structural tiers (go parser and
// tree-sitter): definitions become elements, content is bounded from the
// source, and grammar-derived call seeds are attached.
func finishDefinitions(ctx context.Context, rel string, src []byte, lines []string, lang string, defs []indexDef, imports map[string]string) (fileElements, error) {
	fe := indexDefsToElements(rel, lang, defs)
	fe.imports = imports
	boundContent(fe.elements, lines)
	// Grammar-derived call sites seed "calls" edges in relationships().
	if err := tsAttributeCalls(ctx, src, lang, fe.elements); err != nil {
		return fileElements{}, err
	}
	return fe, nil
}

// toStore converts extracted elements to store rows.
func (fe fileElements) toStore() []store.Element {
	out := make([]store.Element, len(fe.elements))
	for i, e := range fe.elements {
		out[i] = store.Element{
			QualifiedName: e.qn, ElementType: e.etype, Name: e.name,
			FilePath: fe.rel, LineStart: e.start, LineEnd: e.end,
			Language: e.lang, Content: e.content,
		}
		if e.parent >= 0 {
			out[i].ParentQualified = fe.elements[e.parent].qn
		}
	}
	return out
}

func matchGo(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := goFuncRe.FindStringSubmatch(l); m != nil {
			kind, recv := "function", ""
			if strings.TrimSpace(m[1]) != "" {
				kind, recv = "method", goRecvType(m[1])
			}
			ms = append(ms, match{kind: kind, name: m[2], line: i + 1, recv: recv})
			continue
		}
		if m := goTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "type", name: m[1], line: i + 1})
		}
	}
	return ms
}

// goRecvType pulls the type out of a receiver like "*Srv", "_ Srv", "s *Srv".
func goRecvType(recv string) string {
	f := strings.Fields(strings.TrimSpace(recv))
	return strings.TrimPrefix(f[len(f)-1], "*")
}

func matchRust(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := rsFnRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
			continue
		}
		if m := rsTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "type", name: m[2], line: i + 1})
			continue
		}
		if m := rsImplRe.FindStringSubmatch(l); m != nil {
			name := m[1]
			if p := strings.LastIndex(name, "::"); p >= 0 {
				name = name[p+2:]
			}
			ms = append(ms, match{kind: "type", name: name, line: i + 1})
		}
	}
	return ms
}
func matchPython(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := pyFuncRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "function", name: m[2], line: i + 1, indent: len(m[1])})
			continue
		}
		if m := pyClassRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[2], line: i + 1, indent: len(m[1])})
		}
	}
	return ms
}

// pyBodyEnd returns the last line of an indentation-bounded Python block.
func pyBodyEnd(lines []string, m match) int {
	end := m.line
	for i := m.line; i < len(lines); i++ { // lines[i] is line i+1
		l := lines[i]
		if strings.TrimSpace(l) == "" {
			continue
		}
		if len(l)-len(strings.TrimLeft(l, " \t")) <= m.indent {
			break
		}
		end = i + 1
	}
	return end
}

func matchTS(lines []string) []match {
	var ms []match
	for i, l := range lines {
		switch {
		case tsFuncRe.MatchString(l):
			ms = append(ms, match{kind: "function", name: tsFuncRe.FindStringSubmatch(l)[1], line: i + 1})
		case tsClassRe.MatchString(l):
			ms = append(ms, match{kind: "class", name: tsClassRe.FindStringSubmatch(l)[1], line: i + 1})
		case tsArrowRe.MatchString(l):
			ms = append(ms, match{kind: "function", name: tsArrowRe.FindStringSubmatch(l)[1], line: i + 1})
		case tsFnExprRe.MatchString(l):
			ms = append(ms, match{kind: "function", name: tsFnExprRe.FindStringSubmatch(l)[1], line: i + 1})
		}
	}
	return ms
}

func matchMarkdown(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := mdHeadingRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "doc", name: m[2], line: i + 1, indent: len(m[1])})
		}
	}
	return ms
}

// mdSectionEnds resolves the line range of every markdown heading: a section
// runs from its heading line to just before the next heading of the SAME or a
// HIGHER level, so a section never contains a sibling's body.
//
// The old rule stopped at the next strictly HIGHER level, which made every
// `##` of a document reach past all of its following siblings: on the live
// leankg store the 249 markdown files (3,407,965 B on disk) expanded to
// 14,545,212 B of `doc` content, 4.27x, and the worst file
// (docs/archive/workflow-opencode-agent.md, 19,509 B on disk) expanded to
// 290,340 B, 14.9x.
func mdSectionEnds(ms []match, nlines int) []int {
	ends := make([]int, len(ms))
	for i, m := range ms {
		end := nlines
		for j := i + 1; j < len(ms); j++ {
			if ms[j].indent <= m.indent {
				end = ms[j].line - 1
				break
			}
		}
		if end < m.line {
			end = m.line
		}
		ends[i] = end
	}
	return ends
}

// markdownContent fills the stored text of every markdown element.
//
// The shape: ONE element per heading, and a heading's text is its OWN lines
// plus the TITLES of its descendant headings, never a descendant's body. The
// document's own top-level heading keeps the WHOLE document, so
// document-level summarization keeps working. Every other heading keeps its
// own text, which lands the total at about 2x the source size instead of the
// 4.27x the ancestor-copy shape produced (the same paragraph used to be
// stored once per ancestor).
func markdownContent(els []indexedElem, ms []match, ends []int, lines []string) {
	owner := mdDocOwner(ms)
	for i := range els {
		if i == owner {
			// The document owner holds the whole document, preamble included.
			els[i].start = 1
			els[i].end = len(lines)
			els[i].content = store.ClipUTF8(strings.Join(lines, "\n"), maxContent)
			continue
		}
		keep := mdKeepLines(i, ms, ends)
		parts := make([]string, 0, len(keep))
		for _, ln := range keep {
			parts = append(parts, lines[ln-1])
		}
		els[i].content = store.ClipUTF8(strings.Join(parts, "\n"), maxContent)
	}
}

// mdDocOwner returns the index of the heading that owns the document: the
// first heading of the file's top level (the lowest heading level present).
// A file that starts at `##` has no `#` title, and its first heading plays
// that role, so document-level summarization keeps working for it too.
func mdDocOwner(ms []match) int {
	if len(ms) == 0 {
		return -1
	}
	owner := 0
	for i, m := range ms {
		if m.indent < ms[owner].indent {
			owner = i
		}
	}
	return owner
}

// mdKeepLines returns the lines one markdown heading stores: its own lines
// plus the titles of its descendant headings, never a descendant's body.
func mdKeepLines(i int, ms []match, ends []int) []int {
	start, end := ms[i].line, ends[i]
	keep := make([]int, 0, end-start+1)
	for ln := start; ln <= end; ln++ {
		if ln == start || mdIsDescendantTitle(i, ms, ends, ln) || !mdInDescendantBody(i, ms, ends, ln) {
			keep = append(keep, ln)
		}
	}
	return keep
}

// mdInDescendantBody reports whether the line falls inside the BODY of one of
// the heading's descendants. An ancestor must not store that text a second
// time: the descendant's own element holds it.
func mdInDescendantBody(i int, ms []match, ends []int, line int) bool {
	for j := i + 1; j < len(ms) && ms[j].line <= ends[i]; j++ {
		if line > ms[j].line && line <= ends[j] {
			return true
		}
	}
	return false
}

// mdIsDescendantTitle reports whether the line is the heading line of one of
// the heading's descendants. A title is the outline of the section and is
// kept; a body is not.
func mdIsDescendantTitle(i int, ms []match, ends []int, line int) bool {
	for j := i + 1; j < len(ms) && ms[j].line <= ends[i]; j++ {
		if ms[j].line == line {
			return true
		}
	}
	return false
}

// uniqueQNs gives each qualified name in qns its own spelling, in order, so
// that no two elements of one file share a row in the store (qualified_name is
// UNIQUE, and an upsert silently replaces the earlier row). The first spelling
// of a name is kept; the n-th repeat (n >= 2) becomes "name (n)", taking the
// lowest n whose spelling is not yet used. used holds every spelling taken so
// far and is updated, so a caller can share it across the element batches of
// one file. The scheme is a pure function of file order, so a re-index of an
// unchanged file names every element the same way.
func uniqueQNs(used map[string]bool, qns []string) []string {
	out := make([]string, len(qns))
	for i, q := range qns {
		cand := q
		for n := 2; used[cand]; n++ {
			cand = q + " (" + strconv.Itoa(n) + ")"
		}
		used[cand] = true
		out[i] = cand
	}
	return out
}

// uniqueElemQNs applies uniqueQNs to the qualified names of one file's
// extracted elements.
func uniqueElemQNs(used map[string]bool, els []indexedElem) {
	qns := make([]string, len(els))
	for i := range els {
		qns[i] = els[i].qn
	}
	for i, q := range uniqueQNs(used, qns) {
		els[i].qn = q
	}
}

func matchJava(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := javaTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[2], line: i + 1})
			continue
		}
		if m := javaMethodRe.FindStringSubmatch(l); m != nil && !javaKeyword(m[1]) {
			ms = append(ms, match{kind: "method", name: m[1], line: i + 1})
		}
	}
	return ms
}

// javaKeyword rejects method-shaped Java keywords ("return", "throw", ...).
func javaKeyword(name string) bool {
	switch name {
	case "if", "else", "for", "while", "do", "switch", "catch", "return",
		"throw", "synchronized", "try", "new", "assert":
		return true
	}
	return false
}

func matchKotlin(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := ktTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[2], line: i + 1})
			continue
		}
		if m := ktFuncRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
		}
	}
	return ms
}

func matchSwift(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := swiftTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[2], line: i + 1})
			continue
		}
		if m := swiftFuncRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "method", name: m[1], line: i + 1})
		}
	}
	return ms
}

func matchObjC(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := objcTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[2], line: i + 1})
			continue
		}
		if m := objcMethodRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "method", name: m[3], line: i + 1, recv: m[2]})
		}
	}
	return ms
}

// matchDart extracts dart classes, mixins, enums, functions, constructors
// (default, named, factory and const — named without the class, matching the
// tree-sitter tier) and enum values. Constructors and values carry their
// owning type in recv (see qualify), which keeps their qualified names stable
// whether or not the class or enum body spans several lines.
func matchDart(lines []string) []match {
	var ms []match
	inEnum, enumName := false, ""
	for i, l := range lines {
		if m := dartTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[2], line: i + 1})
			if m[1] == "enum" {
				// Values sit on the declaration line for the one-line form and
				// in the body lines below otherwise, up to the closing brace.
				enumName = m[2]
				ms = append(ms, dartEnumValues(l, i+1, enumName)...)
				inEnum = !strings.Contains(l, "}")
			}
			continue
		}
		if inEnum {
			if vals := dartEnumValues(l, i+1, enumName); len(vals) > 0 {
				ms = append(ms, vals...)
				continue
			}
			if strings.Contains(l, "}") {
				inEnum, enumName = false, ""
			}
			continue
		}
		if m := dartCtorRe.FindStringSubmatch(l); m != nil {
			name := m[2] // named/factory constructor: the part after the dot
			if name == "" {
				name = m[1]
			}
			// The class name rides in recv so a default constructor's qualified
			// name ("<file>::Factory.Factory") cannot collide with the class
			// element's own row, whatever the heuristic end line resolved to.
			ms = append(ms, match{kind: "constructor", name: name, recv: m[1], line: i + 1})
			continue
		}
		if m := dartFuncRe.FindStringSubmatch(l); m != nil && !dartKeyword(m[1]) {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
		}
	}
	return ms
}

// dartEnumValues extracts the value names carried by one line of a dart enum:
// the value list is everything up to the ";" that separates it from the
// members, so a member line (a field, a constructor, a method body) yields
// nothing. The enum's own name rides in recv, giving "<file>::Color.red".
func dartEnumValues(line string, lineNo int, enumName string) []match {
	s := line
	if p := strings.IndexByte(s, '}'); p >= 0 {
		s = s[:p]
	}
	if p := strings.IndexByte(s, '{'); p >= 0 {
		s = s[p+1:]
	}
	if p := strings.IndexByte(s, ';'); p >= 0 {
		s = s[:p]
	}
	var out []match
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if i := strings.IndexAny(part, "(="); i >= 0 { // "ok(1)", "red = 1"
			part = strings.TrimSpace(part[:i])
		}
		if !dartEnumConstantRe.MatchString(part) {
			continue
		}
		out = append(out, match{kind: "constant", name: part, recv: enumName, line: lineNo})
	}
	return out
}

// dartKeyword rejects function-shaped Dart keywords ("if", "for", "while", ...).
func dartKeyword(name string) bool {
	switch name {
	case "if", "else", "for", "while", "switch", "catch", "return", "throw",
		"print", "new", "assert", "do":
		return true
	}
	return false
}

// assignParents sets parent to the innermost enclosing element by line
// containment (strictly earlier start, range covering the child start).
func assignParents(els []indexedElem) {
	for i := range els {
		best := -1
		for j := range els {
			if j == i || els[j].start >= els[i].start || els[j].end < els[i].start {
				continue
			}
			if best == -1 || els[j].start > els[best].start {
				best = j
			}
		}
		els[i].parent = best
	}
}

// qualify assigns element kinds (Python def-in-class becomes a method) and
// qualified names: `<rel>::<name>`, class and enum members
// `<rel>::<Type>.<name>` (methods and constructors; enum values, whose recv
// holds the enum name — named members keep their owning type even when the
// declaring body sits on a single line, where line-based parent detection
// finds no parent).
func qualify(fe fileElements) {
	for i := range fe.elements {
		e := &fe.elements[i]
		if e.etype == "function" && e.lang == "py" && e.parent >= 0 &&
			fe.elements[e.parent].etype == "class" {
			e.etype = "method"
		}
		base := fe.rel + "::"
		if e.etype == "method" || e.etype == "constructor" || e.etype == "constant" {
			typeName := e.recv
			if typeName == "" && e.parent >= 0 {
				pqn := fe.elements[e.parent].qn
				if p := strings.LastIndex(pqn, "::"); p >= 0 {
					typeName = pqn[p+2:]
				}
			}
			if typeName != "" {
				base += typeName + "."
			}
		}
		e.qn = base + e.name
	}
}

// boundContent fills Content from the source lines, bounded 8000 chars.
//
// Content LEADS with the element's own doc comment. `start` is the `func`/
// `type` line, so the `//` block an author wrote ABOVE it was never in the
// store — and that block is the prose a question is a paraphrase of. Measured
// on this repository (wave 9 of the dogfood loop): 2,596 of 4,175 Go elements
// (62%) have a doc comment the engine could not see, and of the 17 labelled
// questions no arm could reach, nine were the doc comment verbatim minus the
// symbol name. Adding it moved the pure-vector top-10 from 36/84 to 71/84 with
// zero regressions (numbers and rationale in internal/index/doccapture_test.go;
// the comment leads rather than trails because the local sidecar truncates to a
// 1000-rune budget and a leading explanation survives it).
//
// The slice GROWS (lines[docStart-1:e]), so a body is never shortened to make
// room, and an element with no comment is byte-identical to what it was.
func boundContent(els []indexedElem, lines []string) {
	for i := range els {
		s, e := els[i].start, els[i].end
		if s < 1 {
			s = 1
		}
		if e > len(lines) {
			e = len(lines)
		}
		if doc := docCommentStart(lines, s, els[i].lang); doc < s {
			// The doc block is the content's leading bytes; relationships()
			// scans past them, so prose in it never becomes an edge.
			if els[i].etype != "doc" {
				els[i].docLen = len(strings.Join(lines[doc-1:s-1], "\n")) + 1
			}
			s = doc
		}
		els[i].content = strings.Join(lines[s-1:e], "\n")
		els[i].content = store.ClipUTF8(els[i].content, maxContent)
	}
}

// docCommentStart walks back from line `start` over a CONTIGUOUS comment block
// directly above it and returns that block's first line (start itself when there
// is none). Attribute or annotation lines for lang (`@Override`, `#[inline]`)
// may sit between the block and the declaration; they are passed over and belong
// to the block. Contiguity is the rule: a blank line or a second declaration ends
// the block, so a comment belonging to the PREVIOUS element is never stolen.
//
// Recognised forms are the ones the indexed languages use: `//`, `///`, `//!`,
// `#`, and a `/* … */` block. Anything else stops the walk.
func docCommentStart(lines []string, start int, lang string) int {
	if start < 2 || start > len(lines) {
		return start
	}
	style := commentStyles[lang]
	// `start` is 1-based, so the line above it is index start-2 in the 0-based
	// slice below.
	i := start - 2
	for i >= 0 && isAttrLine(strings.TrimSpace(lines[i]), style) {
		i--
	}
	if i < 0 {
		return start
	}
	// A /* … */ block that ends on the line above. Having SEEN the terminator we
	// know a block is open, and everything up to its `/*` is that block's body —
	// a block comment cannot contain code, so no per-line filtering is needed or
	// correct here (interior lines are conventionally " * …" but may be
	// anything, and a stricter walk strands the block). A `//` run sitting
	// directly on top of the block belongs to the same documentation unit, and
	// this loop walks up through it naturally.
	if strings.HasSuffix(strings.TrimSpace(lines[i]), "*/") {
		for k := i; k >= 0; k-- {
			if strings.Contains(lines[k], "/*") {
				return k + 1
			}
		}
		// Malformed — no opening delimiter above. Take nothing rather than
		// guess and swallow unrelated lines.
		return start
	}
	// Otherwise a run of single-line comment lines.
	j := i
	for j >= 0 && isDocCommentLine(lines[j]) {
		j--
	}
	if j == i {
		return start
	}
	return j + 2
}

// isDocCommentLine reports whether one source line is a single-line doc comment
// in a language the registry indexes.
func isDocCommentLine(line string) bool {
	t := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(t, "//"), strings.HasPrefix(t, "#"):
		return true
	case strings.HasPrefix(t, "*") && strings.HasSuffix(t, "*/"):
		return true
	}
	return false
}

func extOf(rel string) string {
	if p := strings.LastIndex(rel, "."); p >= 0 {
		return rel[p:]
	}
	return ""
}
