//go:build !tstree

package index

import "testing"

// TestJavaDocProseInBodyIsNotAnEdge: a multi-line /** ... */ block inside a
// method body is prose. Its interior lines name renderWidget, which must not be
// an edge from run; the real call gammaHelper must be. Alpha is bounded by the
// declaration that follows it (the documented ceiling for a class whose members
// follow it), so its span is 1..1.
func TestJavaDocProseInBodyIsNotAnEdge(t *testing.T) {
	src := "public class Alpha {\n    void run() {\n        /**\n         * Calls renderWidget to draw.\n         */\n        gammaHelper();\n    }\n}\n\nclass renderWidget {}\nclass gammaHelper {}\n"
	fe := extractAs(t, "A.java", []byte(src), "java")
	if got := extractedElem(t, fe, "Alpha").end; got != 1 {
		t.Errorf("Alpha end = %d, want 1 (bounded by the next declaration)", got)
	}
	edges := callEdgesFrom(fe, "::run")
	if hasEdgeTo(edges, "::renderWidget") {
		t.Errorf("run edges = %v, a name in a doc comment is prose, not a call", edges)
	}
	if !hasEdgeTo(edges, "::gammaHelper") {
		t.Errorf("run edges = %v, the real call gammaHelper is missing", edges)
	}
}
