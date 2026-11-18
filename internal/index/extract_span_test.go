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
