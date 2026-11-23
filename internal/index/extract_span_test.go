package index

import (
	"slices"
	"strings"
	"testing"
)

// extractedElem returns the extracted element with the given name, failing the test
// when it is absent.
func extractedElem(t *testing.T, fe fileElements, name string) indexedElem {
	t.Helper()
	for _, e := range fe.elements {
		if e.name == name {
			return e
		}
	}
	t.Fatalf("element %q not extracted", name)
	return indexedElem{}
}

// callEdgesFrom returns the calls and references edges whose source ends with
// src, as "rel target" strings.
func callEdgesFrom(fe fileElements, src string) []string {
	names := map[string][]string{}
	for _, e := range fe.elements {
		names[e.name] = append(names[e.name], e.qn)
	}
	var out []string
	for _, r := range relationships(fe.elements, names) {
		if (r.RelType == "calls" || r.RelType == "references") && strings.HasSuffix(r.Source, src) {
			out = append(out, r.RelType+" "+r.Target)
		}
	}
	return out
}

// TestElementEndsAtClosingBrace: the comment above AlphaTwo belongs to AlphaTwo.
// Alpha ends at its own closing brace (a.go:7), so that comment is neither part
// of Alpha's span nor a call edge Alpha -> AlphaTwo.
func TestElementEndsAtClosingBrace(t *testing.T) {
	src := "package pkg\n\n// Alpha drives the cross-file calls.\nfunc Alpha() {\n\tbetaHelper()\n\tgammaHelper()\n}\n\n// AlphaTwo calls into c.go.\nfunc AlphaTwo() {\n\tgammaHelper()\n}\n"
	fe := extractAs(t, "a.go", []byte(src), "go")

	alpha := extractedElem(t, fe, "Alpha")
	if alpha.end != 7 {
		t.Errorf("Alpha end = %d, want 7 (its closing brace)", alpha.end)
	}
	if strings.Contains(alpha.content, "AlphaTwo calls into") {
		t.Errorf("Alpha content carries AlphaTwo's doc comment:\n%s", alpha.content)
	}
	for _, edge := range callEdgesFrom(fe, "::Alpha") {
		if strings.HasSuffix(edge, "::AlphaTwo") {
			t.Errorf("Alpha has an edge to AlphaTwo (%s); its only mention is the next declaration's comment", edge)
		}
	}
}

// TestCommentInBodyIsNotAnEdge: a line comment inside a body that names another
// element is prose, not a call or a reference.
func TestCommentInBodyIsNotAnEdge(t *testing.T) {
	src := "package pkg\n\nfunc Alpha() {\n\t// AlphaTwo handles the rest.\n\tbetaHelper()\n}\n\nfunc AlphaTwo() {}\n\nfunc betaHelper() {}\n"
	fe := extractAs(t, "a.go", []byte(src), "go")
	edges := callEdgesFrom(fe, "::Alpha")
	for _, edge := range edges {
		if strings.HasSuffix(edge, "::AlphaTwo") {
			t.Errorf("comment text inside Alpha produced an edge: %s", edge)
		}
	}
	// The real call on the next line must still be there, so the test cannot
	// pass by dropping every edge.
	if !slices.ContainsFunc(edges, func(e string) bool { return strings.HasSuffix(e, "::betaHelper") }) {
		t.Errorf("the real call betaHelper is missing from Alpha's edges: %v", edges)
	}
}

// TestSignatureLineDoesNotEndElement: a signature line whose braces open and
// close on that line (a Go map type, a TS default object, a TS destructured
// parameter) is still the signature. The element runs on to the body's closing
// brace, and the body's call edge is kept.
func TestSignatureLineDoesNotEndElement(t *testing.T) {
	cases := []struct {
		name, fn, rel, lang, src string
		end                      int
	}{
		{"go multi-line signature with a map type", "Alpha", "a.go", "go",
			"package pkg\n\nfunc Alpha(\n\tseen map[string]struct{},\n) {\n\tbetaHelper()\n}\n\nfunc betaHelper() {}\n", 7},
		{"ts default object parameter", "Alpha", "a.ts", "ts",
			"function Alpha(\n  opts = {},\n) {\n  betaHelper();\n}\n\nfunction betaHelper() {}\n", 5},
		{"ts destructured parameter with allman brace", "Widget", "a.ts", "ts",
			"function Widget({ label }: Props)\n{\n  betaHelper(label);\n}\n\nfunction betaHelper() {}\n", 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fe := extractAs(t, c.rel, []byte(c.src), c.lang)
			if got := extractedElem(t, fe, c.fn).end; got != c.end {
				t.Errorf("%s end = %d, want %d (its closing brace)", c.fn, got, c.end)
			}
			want := "calls " + c.rel + "::betaHelper"
			if edges := callEdgesFrom(fe, "::"+c.fn); !slices.Contains(edges, want) {
				t.Errorf("%s edges = %v, want %s", c.fn, edges, want)
			}
		})
	}
}

// TestBraceInLiteralDoesNotEndElement: a lone brace inside a multi-line string
// (a Go raw string, a TS template literal) is text, not a block boundary. The
// element ends at its real closing brace and the call after the literal is kept.
func TestBraceInLiteralDoesNotEndElement(t *testing.T) {
	cases := []struct {
		name, rel, lang, src string
		end                  int
	}{
		{"go raw string", "a.go", "go",
			"package pkg\n\nfunc Alpha() string {\n\traw := `\n}\n`\n\tbetaHelper()\n\treturn raw\n}\n\nfunc betaHelper() string { return \"\" }\n", 9},
		{"ts template literal", "a.ts", "ts",
			"function Alpha() {\n  const s = `\n}\n`;\n  betaHelper();\n}\n\nfunction betaHelper() {}\n", 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fe := extractAs(t, c.rel, []byte(c.src), c.lang)
			if got := extractedElem(t, fe, "Alpha").end; got != c.end {
				t.Errorf("Alpha end = %d, want %d (its closing brace)", got, c.end)
			}
			want := "calls " + c.rel + "::betaHelper"
			if edges := callEdgesFrom(fe, "::Alpha"); !slices.Contains(edges, want) {
				t.Errorf("Alpha edges = %v, want %s", edges, want)
			}
		})
	}
}

// TestOneLineBodyEndsOnItsLine: a declaration whose body opens and closes on its
// own line ends there, so the next declaration is not swallowed.
func TestOneLineBodyEndsOnItsLine(t *testing.T) {
	src := "package pkg\n\nfunc Alpha() { betaHelper() }\n\nfunc betaHelper() {}\n"
	fe := extractAs(t, "a.go", []byte(src), "go")
	if got := extractedElem(t, fe, "Alpha").end; got != 3 {
		t.Errorf("Alpha end = %d, want 3 (its own line)", got)
	}
	if edges := callEdgesFrom(fe, "::Alpha"); !slices.Contains(edges, "calls a.go::betaHelper") {
		t.Errorf("Alpha edges = %v, want calls a.go::betaHelper", edges)
	}
}
