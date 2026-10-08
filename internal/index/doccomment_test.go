package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLeadingDocCaptured pins RS-10: the comment block directly above a
// declaration becomes part of the element's content; a blank line ends it,
// attributes ride along, line_start stays the declaration line, and prose in
// a doc comment never becomes a call edge.
func TestLeadingDocCaptured(t *testing.T) {
	cases := []struct {
		lang, file, src, name, wantDoc, notDoc string
	}{
		{"go", "a.go", "package a\n\n// license header\n\n// Rotate refreshes the staging secrets.\n// It runs quarterly.\nfunc Rotate() {}\n", "Rotate", "refreshes the staging secrets", "license header"},
		{"rust", "a.rs", "/// Parses the frame.\n#[inline]\npub fn parse() {}\n", "parse", "Parses the frame", ""},
		{"py", "a.py", "# Loads the config file.\n@cache\ndef load():\n    pass\n", "load", "Loads the config file", ""},
		{"ts", "a.ts", "/**\n * Renders the widget.\n */\nexport function render() {}\n", "render", "Renders the widget", ""},
		{"java", "A.java", "class A {\n  /** Saves the row. */\n  @Override\n  public void save() {\n  }\n}\n", "save", "Saves the row", ""},
	}
	for _, c := range cases {
		dir := t.TempDir()
		path := filepath.Join(dir, c.file)
		if err := os.WriteFile(path, []byte(c.src), 0o644); err != nil {
			t.Fatal(err)
		}
		fe, err := extractFileAs(c.file, path, c.lang)
		if err != nil {
			t.Fatal(err)
		}
		var found *indexedElem
		for i := range fe.elements {
			if fe.elements[i].name == c.name {
				found = &fe.elements[i]
			}
		}
		if found == nil {
			t.Fatalf("%s: element %q not extracted", c.lang, c.name)
		}
		if !strings.Contains(found.content, c.wantDoc) {
			t.Errorf("%s: content lacks doc %q:\n%s", c.lang, c.wantDoc, found.content)
		}
		if c.notDoc != "" && strings.Contains(found.content, c.notDoc) {
			t.Errorf("%s: content captured past a blank line (%q)", c.lang, c.notDoc)
		}
		// line_start is the declaration — or, under the tree-sitter tier, the
		// attribute/annotation the grammar folds into the declaration node —
		// never a comment line.
		declLine := strings.TrimSpace(strings.Split(c.src, "\n")[found.start-1])
		isAttr := strings.HasPrefix(declLine, "@") || strings.HasPrefix(declLine, "#[")
		if !strings.Contains(declLine, c.name) && !isAttr {
			t.Errorf("%s: line_start %d is %q, want the declaration line", c.lang, found.start, declLine)
		}
	}
}

func TestDocCommentProseIsNotACallEdge(t *testing.T) {
	dir := t.TempDir()
	src := "package a\n\nfunc Helper() {}\n\n// Caller explains that Helper is unrelated.\nfunc Caller() {}\n"
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	fe, err := extractFileAs("a.go", path, "go")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string][]string{}
	for _, e := range fe.elements {
		names[e.name] = append(names[e.name], e.qn)
	}
	for _, r := range relationships(fe.elements, names) {
		if r.RelType == "calls" && strings.HasSuffix(r.Source, "Caller") && strings.HasSuffix(r.Target, "Helper") {
			t.Fatalf("doc-comment prose produced a call edge: %+v", r)
		}
	}
}
