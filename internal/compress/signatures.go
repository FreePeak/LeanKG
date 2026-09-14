package compress

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Signature is one extracted code declaration (Rust signatures::Signature).
type Signature struct {
	Kind       string // fn | method | class | struct | interface | trait | type | enum | const
	Name       string
	Params     string
	ReturnType string
	IsAsync    bool
	IsExported bool
	Indent     int
}

// ToCompact renders the map-mode one-liner (Rust Signature::to_compact).
func (s Signature) ToCompact() string {
	export := ""
	if s.IsExported {
		export = "⊛ "
	}
	asyncPrefix := ""
	if s.IsAsync {
		asyncPrefix = "async "
	}
	switch s.Kind {
	case "fn", "method":
		ret := ""
		if s.ReturnType != "" {
			ret = " → " + s.ReturnType
		}
		indent := strings.Repeat(" ", s.Indent)
		return fmt.Sprintf("%sfn %s%s%s(%s)%s", indent, asyncPrefix, export, s.Name, s.Params, ret)
	case "class", "struct":
		return fmt.Sprintf("cl %s%s", export, s.Name)
	case "interface", "trait":
		return fmt.Sprintf("if %s%s", export, s.Name)
	case "type":
		return fmt.Sprintf("ty %s%s", export, s.Name)
	case "enum":
		return fmt.Sprintf("en %s%s", export, s.Name)
	case "const", "let", "var":
		ty := ""
		if s.ReturnType != "" {
			ty = ":" + s.ReturnType
		}
		return fmt.Sprintf("val %s%s%s", export, s.Name, ty)
	default:
		return fmt.Sprintf("%s %s", s.Kind, s.Name)
	}
}

// ToTDD renders the TDD-mode symbol line (Rust Signature::to_tdd).
func (s Signature) ToTDD() string {
	vis := "-"
	if s.IsExported {
		vis = "+"
	}
	a := ""
	if s.IsAsync {
		a = "~"
	}
	switch s.Kind {
	case "fn", "method":
		ret := ""
		if s.ReturnType != "" {
			ret = "→" + compactType(s.ReturnType)
		}
		params := tddParams(s.Params)
		indent := ""
		if s.Indent > 0 {
			indent = " "
		}
		return fmt.Sprintf("%s%sλ%s%s(%s)%s", indent, a, vis, s.Name, params, ret)
	case "class", "struct":
		return fmt.Sprintf("§%s%s", vis, s.Name)
	case "interface", "trait":
		return fmt.Sprintf("∂%s%s", vis, s.Name)
	case "type":
		return fmt.Sprintf("τ%s%s", vis, s.Name)
	case "enum":
		return fmt.Sprintf("ε%s%s", vis, s.Name)
	case "const", "let", "var":
		ty := ""
		if s.ReturnType != "" {
			ty = ":" + compactType(s.ReturnType)
		}
		return fmt.Sprintf("ν%s%s%s", vis, s.Name, ty)
	default:
		first := "?"
		if r, _ := utf8.DecodeRuneInString(s.Kind); r != utf8.RuneError {
			first = string(r)
		}
		return fmt.Sprintf("%s%s%s", first, vis, s.Name)
	}
}

var (
	tsFnRe       = regexp.MustCompile(`^(\s*)(export\s+)?(async\s+)?function\s+(\w+)\s*(?:<[^>]*>)?\s*\(([^)]*)\)(?:\s*:\s*([^\{]+))?\s*\{?`)
	tsClassRe    = regexp.MustCompile(`^(\s*)(export\s+)?(abstract\s+)?class\s+(\w+)`)
	tsIfaceRe    = regexp.MustCompile(`^(\s*)(export\s+)?interface\s+(\w+)`)
	tsTypeRe     = regexp.MustCompile(`^(\s*)(export\s+)?type\s+(\w+)`)
	tsConstRe    = regexp.MustCompile(`^(\s*)(export\s+)?(const|let|var)\s+(\w+)(?:\s*:\s*(\w+))?`)
	rsFnRe       = regexp.MustCompile(`^(\s*)(pub\s+)?(async\s+)?fn\s+(\w+)\s*(?:<[^>]*>)?\s*\(([^)]*)\)(?:\s*->\s*([^\{]+))?\s*\{?`)
	rsStructRe   = regexp.MustCompile(`^(\s*)(pub\s+)?struct\s+(\w+)`)
	rsEnumRe     = regexp.MustCompile(`^(\s*)(pub\s+)?enum\s+(\w+)`)
	rsTraitRe    = regexp.MustCompile(`^(\s*)(pub\s+)?trait\s+(\w+)`)
	rsImplRe     = regexp.MustCompile(`^(\s*)impl\s+(?:(\w+)\s+for\s+)?(\w+)`)
	pyFnRe       = regexp.MustCompile(`^(\s*)(async\s+)?def\s+(\w+)\s*\(([^)]*)\)(?:\s*->\s*([^:]+))?`)
	pyClassRe    = regexp.MustCompile(`^(\s*)class\s+(\w+)`)
	goFnRe       = regexp.MustCompile(`^func\s+(?:\((\w+)\s+\*?(\w+)\)\s+)?(\w+)\s*\(([^)]*)\)(?:\s*(?:\(([^)]*)\)|([^{]+)))?\s*\{?`)
	goTypeRe     = regexp.MustCompile(`^type\s+(\w+)\s+(struct|interface)`)
	javaMethodRe = regexp.MustCompile(`^(\s*)(?:(?:public|private|protected|static|final|native|synchronized|abstract|transient)\s+)*([\w<>\[\]]+)\s+(\w+)\s*\(([^)]*)\)\s*(?:throws\s+[\w,\s]+)?\s*\{?`)
	javaClassRe  = regexp.MustCompile(`^(\s*)(?:(?:public|private|protected|static|final|abstract)\s+)*(class|interface|enum|record)\s+(\w+)`)
	genFuncRe    = regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|async|abstract|virtual|override|final|def|func|fun|fn)\s+)+(\w+)\s*\(`)
	genClassRe   = regexp.MustCompile(`^\s*(?:(?:public|private|protected|abstract|final|sealed|partial)\s+)*(?:class|struct|enum|interface|trait|module|object|record)\s+(\w+)`)
)

// ExtractSignatures extracts declarations for a source file by extension
// (Rust signatures::extract_signatures).
func ExtractSignatures(content, fileExt string) []Signature {
	switch fileExt {
	case "rs":
		return extractRustSignatures(content)
	case "ts", "tsx", "js", "jsx", "svelte", "vue", "mjs", "cjs":
		return extractTSSignatures(content)
	case "py", "pyi":
		return extractPythonSignatures(content)
	case "go":
		return extractGoSignatures(content)
	case "java":
		return extractJavaSignatures(content)
	default:
		return extractGenericSignatures(content)
	}
}

func extractTSSignatures(content string) []Signature {
	var sigs []Signature
	for _, line := range splitLines(content) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		if m := tsFnRe.FindStringSubmatch(line); m != nil {
			indent := len(m[1])
			sigs = append(sigs, Signature{
				Kind:       kindForIndent(indent),
				Name:       m[4],
				Params:     compactParams(m[5]),
				ReturnType: strings.TrimSpace(m[6]),
				IsAsync:    m[3] != "",
				IsExported: m[2] != "",
				Indent:     indentFor(indent),
			})
		} else if m := tsClassRe.FindStringSubmatch(line); m != nil {
			sigs = append(sigs, Signature{Kind: "class", Name: m[4], IsExported: m[2] != ""})
		} else if m := tsIfaceRe.FindStringSubmatch(line); m != nil {
			sigs = append(sigs, Signature{Kind: "interface", Name: m[3], IsExported: m[2] != ""})
		} else if m := tsTypeRe.FindStringSubmatch(line); m != nil {
			sigs = append(sigs, Signature{Kind: "type", Name: m[3], IsExported: m[2] != ""})
		} else if m := tsConstRe.FindStringSubmatch(line); m != nil {
			if m[2] != "" {
				sigs = append(sigs, Signature{Kind: "const", Name: m[4], ReturnType: m[5], IsExported: true})
			}
		}
	}
	return sigs
}

func extractRustSignatures(content string) []Signature {
	var sigs []Signature
	for _, line := range splitLines(content) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if m := rsFnRe.FindStringSubmatch(line); m != nil {
			indent := len(m[1])
			sigs = append(sigs, Signature{
				Kind:       kindForIndent(indent),
				Name:       m[4],
				Params:     compactParams(m[5]),
				ReturnType: strings.TrimSpace(m[6]),
				IsAsync:    m[3] != "",
				IsExported: m[2] != "",
				Indent:     indentFor(indent),
			})
		} else if m := rsStructRe.FindStringSubmatch(line); m != nil {
			sigs = append(sigs, Signature{Kind: "struct", Name: m[3], IsExported: m[2] != ""})
		} else if m := rsEnumRe.FindStringSubmatch(line); m != nil {
			sigs = append(sigs, Signature{Kind: "enum", Name: m[3], IsExported: m[2] != ""})
		} else if m := rsTraitRe.FindStringSubmatch(line); m != nil {
			sigs = append(sigs, Signature{Kind: "trait", Name: m[3], IsExported: m[2] != ""})
		} else if m := rsImplRe.FindStringSubmatch(line); m != nil {
			name := m[3]
			if m[2] != "" {
				name = m[2] + " for " + m[3]
			}
			sigs = append(sigs, Signature{Kind: "class", Name: name})
		}
	}
	return sigs
}

func extractPythonSignatures(content string) []Signature {
	var sigs []Signature
	for _, line := range splitLines(content) {
		if m := pyFnRe.FindStringSubmatch(line); m != nil {
			indent := len(m[1])
			sigs = append(sigs, Signature{
				Kind:       kindForIndent(indent),
				Name:       m[3],
				Params:     compactParams(m[4]),
				ReturnType: m[5],
				IsAsync:    m[2] != "",
				IsExported: !strings.HasPrefix(m[3], "_"),
				Indent:     indentFor(indent),
			})
		} else if m := pyClassRe.FindStringSubmatch(line); m != nil {
			sigs = append(sigs, Signature{Kind: "class", Name: m[2], IsExported: !strings.HasPrefix(m[2], "_")})
		}
	}
	return sigs
}

func extractGoSignatures(content string) []Signature {
	var sigs []Signature
	for _, line := range splitLines(content) {
		if m := goFnRe.FindStringSubmatch(line); m != nil {
			isMethod := m[2] != ""
			returnType := m[5]
			if returnType == "" {
				returnType = m[6]
			}
			kind := "fn"
			indent := 0
			if isMethod {
				kind = "method"
				indent = 2
			}
			sigs = append(sigs, Signature{
				Kind:       kind,
				Name:       m[3],
				Params:     compactParams(m[4]),
				ReturnType: returnType,
				IsExported: startsUpper(m[3]),
				Indent:     indent,
			})
		} else if m := goTypeRe.FindStringSubmatch(line); m != nil {
			kind := "interface"
			if m[2] == "struct" {
				kind = "struct"
			}
			sigs = append(sigs, Signature{Kind: kind, Name: m[1], IsExported: startsUpper(m[1])})
		}
	}
	return sigs
}

func extractJavaSignatures(content string) []Signature {
	var sigs []Signature
	for _, line := range splitLines(content) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") ||
			strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "import ") ||
			strings.HasPrefix(trimmed, "package ") {
			continue
		}
		if m := javaClassRe.FindStringSubmatch(line); m != nil {
			kind := "class"
			switch m[2] {
			case "interface":
				kind = "interface"
			case "enum":
				kind = "enum"
			}
			sigs = append(sigs, Signature{Kind: kind, Name: m[3], IsExported: strings.Contains(line, "public ")})
		} else if m := javaMethodRe.FindStringSubmatch(line); m != nil {
			indent := len(m[1])
			ret := m[2]
			if ret == "return" || ret == "new" || ret == "throw" {
				continue
			}
			sigs = append(sigs, Signature{
				Kind:       "method",
				Name:       m[3],
				Params:     compactParams(m[4]),
				ReturnType: ret,
				IsExported: strings.Contains(line, "public "),
				Indent:     indentFor(indent),
			})
		}
	}
	return sigs
}

func extractGenericSignatures(content string) []Signature {
	var sigs []Signature
	for _, line := range splitLines(content) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		if m := genClassRe.FindStringSubmatch(trimmed); m != nil {
			sigs = append(sigs, Signature{Kind: "type", Name: m[1], IsExported: true})
		} else if m := genFuncRe.FindStringSubmatch(trimmed); m != nil {
			sigs = append(sigs, Signature{Kind: "fn", Name: m[1], IsAsync: strings.Contains(trimmed, "async"), IsExported: true})
		}
	}
	return sigs
}

func kindForIndent(indent int) string {
	if indent > 0 {
		return "method"
	}
	return "fn"
}

func indentFor(indent int) int {
	if indent > 0 {
		return 2
	}
	return 0
}

func startsUpper(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}

// compactParams shortens "name: Type" params to common type aliases (Rust
// signatures::compact_params).
func compactParams(params string) string {
	if strings.TrimSpace(params) == "" {
		return ""
	}
	parts := strings.Split(params, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		name, ty, ok := strings.Cut(p, ":")
		if !ok {
			out = append(out, p)
			continue
		}
		name, ty = strings.TrimSpace(name), strings.TrimSpace(ty)
		short := ""
		switch ty {
		case "string", "String", "&str", "str":
			short = ":s"
		case "number", "i32", "i64", "u32", "u64", "usize", "f32", "f64":
			short = ":n"
		case "boolean", "bool":
			short = ":b"
		default:
			out = append(out, name+":"+ty)
			continue
		}
		out = append(out, name+short)
	}
	return strings.Join(out, ", ")
}

// compactType shortens a type expression (Rust signatures::compact_type).
func compactType(ty string) string {
	t := strings.TrimSpace(ty)
	switch t {
	case "String", "string", "&str", "str":
		return "s"
	case "bool", "boolean":
		return "b"
	case "i32", "i64", "u32", "u64", "usize", "f32", "f64", "number":
		return "n"
	case "void", "()":
		return "∅"
	}
	switch {
	case strings.HasPrefix(t, "Vec<") || strings.HasPrefix(t, "Array<"):
		inner := trimStartAll(trimStartAll(t, "Vec<"), "Array<")
		inner = trimEndAll(inner, ">")
		return "[" + compactType(inner) + "]"
	case strings.HasPrefix(t, "Option<") || strings.HasPrefix(t, "Maybe<"):
		inner := trimStartAll(trimStartAll(t, "Option<"), "Maybe<")
		inner = trimEndAll(inner, ">")
		return "?" + compactType(inner)
	case strings.HasPrefix(t, "Result<"):
		return "R"
	case strings.HasPrefix(t, "impl "):
		return trimStartAll(t, "impl ")
	default:
		return t
	}
}

// trimStartAll removes every leading occurrence of prefix (Rust
// str::trim_start_matches semantics).
func trimStartAll(s, prefix string) string {
	for strings.HasPrefix(s, prefix) {
		s = s[len(prefix):]
	}
	return s
}

// trimEndAll removes every trailing occurrence of suffix (Rust
// str::trim_end_matches semantics).
func trimEndAll(s, suffix string) string {
	for strings.HasSuffix(s, suffix) {
		s = s[:len(s)-len(suffix)]
	}
	return s
}

// tddParams shortens params for TDD mode (Rust signatures::tdd_params).
func tddParams(params string) string {
	if strings.TrimSpace(params) == "" {
		return ""
	}
	parts := strings.Split(params, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "&") {
			rest := trimStartAll(trimStartAll(p, "&mut "), "&")
			if name, ty, ok := strings.Cut(rest, ":"); ok {
				out = append(out, "&"+strings.TrimSpace(name)+":"+compactType(ty))
				continue
			}
			out = append(out, p)
			continue
		}
		if name, ty, ok := strings.Cut(p, ":"); ok {
			out = append(out, strings.TrimSpace(name)+":"+compactType(ty))
			continue
		}
		if p == "self" {
			out = append(out, "⊕")
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, ",")
}
