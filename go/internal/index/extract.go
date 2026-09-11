package index

import (
	"os"
	"regexp"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// match is one raw regex hit before line-range resolution.
type match struct {
	kind   string // "function", "method", "type", "class", "doc"
	name   string
	line   int    // 1-based
	recv   string // Go receiver type
	indent int    // Python indentation; Markdown heading level
}

// indexedElem is an extracted element before conversion to store.Element.
type indexedElem struct {
	name    string
	etype   string // function | method | type | class | doc
	lang    string
	start   int // 1-based
	end     int
	content string
	parent  int // index into the same slice, -1 = none
	qn      string
	recv    string // Go method receiver type
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
	tsFuncRe   = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_]\w*)`)
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
)

// extractFile extracts elements from one file on disk, tagging it with the
// legacy static extension->language map.
func extractFile(rel, abs string) (fileElements, error) {
	return extractFileAs(rel, abs, extLang[extOf(rel)])
}

// extractFileAs extracts elements from one file on disk as the given
// language tag (empty or unknown tags yield no elements).
func extractFileAs(rel, abs, lang string) (fileElements, error) {
	src, err := os.ReadFile(abs)
	if err != nil {
		return fileElements{}, err
	}
	lines := strings.Split(strings.TrimSuffix(string(src), "\n"), "\n")

	// Tree-sitter tier first (tstree build only): richer symbol extraction
	// for the bundled grammars, with real end lines. No grammar for the
	// language ⇒ nil ⇒ regex fallback below.
	if defs, err := tsExtract(src, lang); err == nil && len(defs) > 0 {
		fe := fileElements{rel: rel}
		for _, d := range defs {
			end := d.EndLine
			if end < d.StartLine {
				end = d.StartLine
			}
			fe.elements = append(fe.elements, indexedElem{
				name: d.Name, etype: d.Kind, lang: lang,
				start: d.StartLine, end: end, parent: -1,
			})
		}
		assignParents(fe.elements)
		qualify(fe)
		boundContent(fe.elements, lines)
		return fe, nil
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
		// A section extends to just before the next heading of a strictly
		// higher level, so nested headings stay line-contained in their parent.
		for i, m := range ms {
			ends[i] = len(lines)
			for j := i + 1; j < len(ms); j++ {
				if ms[j].indent < m.indent {
					ends[i] = ms[j].line - 1
					break
				}
			}
			if ends[i] < m.line {
				ends[i] = m.line
			}
		}
	default:
		for i := range ms {
			ends[i] = len(lines)
			if i+1 < len(ms) {
				ends[i] = ms[i+1].line - 1
			}
			if ends[i] < ms[i].line {
				ends[i] = ms[i].line
			}
		}
	}

	fe := fileElements{rel: rel}
	for i, m := range ms {
		fe.elements = append(fe.elements, indexedElem{
			name: m.name, etype: m.kind, lang: lang,
			start: m.line, end: ends[i], parent: -1, recv: m.recv,
		})
	}
	assignParents(fe.elements)
	qualify(fe)
	boundContent(fe.elements, lines)
	return fe, nil
}

// fileElements holds the extraction output for one file.
type fileElements struct {
	rel      string
	elements []indexedElem
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

func matchDart(lines []string) []match {
	var ms []match
	for i, l := range lines {
		if m := dartTypeRe.FindStringSubmatch(l); m != nil {
			ms = append(ms, match{kind: "class", name: m[2], line: i + 1})
			continue
		}
		if m := dartFuncRe.FindStringSubmatch(l); m != nil && !dartKeyword(m[1]) {
			ms = append(ms, match{kind: "function", name: m[1], line: i + 1})
		}
	}
	return ms
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
// qualified names: `<rel>::<name>`, methods `<rel>::<Type>.<name>`.
func qualify(fe fileElements) {
	for i := range fe.elements {
		e := &fe.elements[i]
		if e.etype == "function" && e.lang == "py" && e.parent >= 0 &&
			fe.elements[e.parent].etype == "class" {
			e.etype = "method"
		}
		base := fe.rel + "::"
		if e.etype == "method" {
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
func boundContent(els []indexedElem, lines []string) {
	const maxContent = 8000
	for i := range els {
		s, e := els[i].start, els[i].end
		if s < 1 {
			s = 1
		}
		if e > len(lines) {
			e = len(lines)
		}
		els[i].content = strings.Join(lines[s-1:e], "\n")
		if len(els[i].content) > maxContent {
			els[i].content = els[i].content[:maxContent]
		}
	}
}

func extOf(rel string) string {
	if p := strings.LastIndex(rel, "."); p >= 0 {
		return rel[p:]
	}
	return ""
}
