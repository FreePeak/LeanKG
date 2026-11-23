package index

import (
	"slices"
	"strings"
	"testing"
)

// hasEdgeTo reports whether one of edges targets the element qualified name
// suffix target (for example "::renderWidget").
func hasEdgeTo(edges []string, target string) bool {
	return slices.ContainsFunc(edges, func(e string) bool { return strings.HasSuffix(e, target) })
}

// TestPythonFloorDivisionKeepsCall: `//` is floor division in Python, not a
// comment, so the call after it is a real call site.
func TestPythonFloorDivisionKeepsCall(t *testing.T) {
	src := "def alpha(a):\n    x = a // 2 + renderWidget(a)\n    return x\n\ndef renderWidget(a):\n    return a\n"
	fe := extractAs(t, "a.py", []byte(src), "py")
	if edges := callEdgesFrom(fe, "::alpha"); !slices.Contains(edges, "calls a.py::renderWidget") {
		t.Errorf("alpha edges = %v, want calls a.py::renderWidget after the floor division", edges)
	}
}

// TestPythonHashWithoutSpaceIsAComment: `#` starts a Python comment even when
// no space follows it, so a name after it is no call.
func TestPythonHashWithoutSpaceIsAComment(t *testing.T) {
	src := "def alpha(a):\n    #renderWidget(a)\n    return betaHelper(a)\n\ndef renderWidget(a):\n    return a\n\ndef betaHelper(a):\n    return a\n"
	fe := extractAs(t, "a.py", []byte(src), "py")
	edges := callEdgesFrom(fe, "::alpha")
	if hasEdgeTo(edges, "::renderWidget") {
		t.Errorf("alpha edges = %v, the name after #renderWidget is a comment, not a call", edges)
	}
	if !hasEdgeTo(edges, "::betaHelper") {
		t.Errorf("alpha edges = %v, the real call betaHelper is missing", edges)
	}
}

// TestJSLineCommentIsNotAnEdge: a `//` comment in a JS body that names another
// element is prose; the real call on the next line is kept.
func TestJSLineCommentIsNotAnEdge(t *testing.T) {
	src := "function alpha() {\n  // renderWidget draws it\n  betaHelper();\n}\n\nfunction renderWidget() {}\nfunction betaHelper() {}\n"
	fe := extractAs(t, "a.js", []byte(src), "js")
	edges := callEdgesFrom(fe, "::alpha")
	if hasEdgeTo(edges, "::renderWidget") {
		t.Errorf("alpha edges = %v, a name in a line comment is no call", edges)
	}
	if !hasEdgeTo(edges, "::betaHelper") {
		t.Errorf("alpha edges = %v, the real call betaHelper is missing", edges)
	}
}

// TestBlockCommentIsNotAnEdge: names inside a single-line and a multi-line
// /* ... */ comment are prose, not calls.
func TestBlockCommentIsNotAnEdge(t *testing.T) {
	src := "package pkg\n\nfunc Alpha() {\n\t/* renderWidget() */\n\t/* renderWidget\n\tcalls */\n\tbetaHelper()\n}\n\nfunc renderWidget() {}\nfunc betaHelper() {}\n"
	fe := extractAs(t, "a.go", []byte(src), "go")
	edges := callEdgesFrom(fe, "::Alpha")
	if hasEdgeTo(edges, "::renderWidget") {
		t.Errorf("Alpha edges = %v, a name in a block comment is no call", edges)
	}
	if !hasEdgeTo(edges, "::betaHelper") {
		t.Errorf("Alpha edges = %v, the real call betaHelper is missing", edges)
	}
}

// TestCallAfterInlineBlockCommentIsKept: a call that follows a block comment on
// the same line is still a call site.
func TestCallAfterInlineBlockCommentIsKept(t *testing.T) {
	src := "package pkg\n\nfunc Alpha() {\n\t/* note */ GammaHelper()\n}\n\nfunc GammaHelper() {}\n"
	fe := extractAs(t, "a.go", []byte(src), "go")
	if edges := callEdgesFrom(fe, "::Alpha"); !hasEdgeTo(edges, "::GammaHelper") {
		t.Errorf("Alpha edges = %v, want the call GammaHelper after the inline block comment", edges)
	}
}
