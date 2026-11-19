package index

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
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
		fe := extractAs(t, c.file, []byte(c.src), c.lang)
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
	src := "package a\n\nfunc Helper() {}\n\n// Caller explains that Helper is unrelated.\nfunc Caller() {}\n"
	fe := extractAs(t, "a.go", []byte(src), "go")
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

// indexedGraph writes files (relative path -> source) into a fresh directory,
// indexes it into a new SQLite store and returns that store.
func indexedGraph(t *testing.T, files map[string]string) store.Backend {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.OpenBackend(ctx, store.OpenOptions{DBPath: filepath.Join(t.TempDir(), "s.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := IndexDir(ctx, st, dir); err != nil {
		t.Fatalf("IndexDir: %v", err)
	}
	return st
}

// edgeTypes returns the relationship types from src to dst, read back from the
// store's incoming edges of dst.
func edgeTypes(t *testing.T, st store.Backend, src, dst string) []string {
	t.Helper()
	rels, err := st.Incoming(context.Background(), dst)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, r := range rels {
		if r.Source == src {
			types = append(types, r.RelType)
		}
	}
	return types
}

// TestDocMentionIsNotACall mirrors internal/store/existence.go: a function's
// doc comment names another function on its second line. The mention is prose.
func TestDocMentionIsNotACall(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.go": "package a\n\n" +
		"// OpenBackend opens the store for a project.\n" +
		"func OpenBackend() {}\n\n" +
		"// SQLitePath resolves the store path for a project.\n" +
		"// It is the SAME resolution OpenBackend uses.\n" +
		"func SQLitePath() string { return \"\" }\n"})
	if got := edgeTypes(t, st, "a.go::SQLitePath", "a.go::OpenBackend"); slices.Contains(got, "calls") {
		t.Fatalf("doc-comment prose produced a calls edge: %v", got)
	}
}

// TestNextElementDocIsNotInPreviousElement mirrors internal/store/elements.go:
// Files ends where the next declaration's doc block begins, so the doc that
// mentions Writer is not part of Files and creates no calls edge.
func TestNextElementDocIsNotInPreviousElement(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.go": "package a\n\n" +
		"// Files returns all indexed file records.\n" +
		"func Files() []int { return nil }\n\n" +
		"// ReplaceRelations swaps the edges of one file\n" +
		"// (see Writer) in one transaction.\n" +
		"func ReplaceRelations() {}\n\n" +
		"// Writer persists the graph.\n" +
		"type Writer interface {\n\tFiles() []int\n}\n"})
	if got := edgeTypes(t, st, "a.go::Files", "a.go::Writer"); slices.Contains(got, "calls") {
		t.Fatalf("next element's doc comment leaked into Files: calls edge %v", got)
	}
}

// TestInterfaceMethodSpecsAreNotCalls: an interface lists method specs; they
// name methods but do not call them, so the interface gets no calls edge.
func TestInterfaceMethodSpecsAreNotCalls(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.go": "package a\n\n" +
		"// Remove deletes a row.\n" +
		"func Remove() {}\n\n" +
		"// Writer persists the graph.\n" +
		"type Writer interface {\n\tRemove()\n}\n"})
	if got := edgeTypes(t, st, "a.go::Writer", "a.go::Remove"); slices.Contains(got, "calls") {
		t.Fatalf("interface method spec produced a calls edge: %v", got)
	}
}

// TestTSDocMentionIsNotACall: the same prose-in-doc rule for a TypeScript
// block doc comment.
func TestTSDocMentionIsNotACall(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.ts": "/**\n * Saves the row to storage.\n */\n" +
		"export function save() {}\n\n" +
		"/**\n * Loads a row.\n * Mirrors save, the writer.\n */\n" +
		"export function load() {}\n"})
	if got := edgeTypes(t, st, "a.ts::load", "a.ts::save"); slices.Contains(got, "calls") {
		t.Fatalf("TS doc-comment prose produced a calls edge: %v", got)
	}
}

// TestLineCommentInBodyIsNotACall: a line comment inside a function body names
// a function; the comment is not code, so there is no calls edge.
func TestLineCommentInBodyIsNotACall(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.go": "package a\n\n" +
		"func Helper() {}\n\n" +
		"func Quiet() {\n\t// Helper is deliberately not called here.\n}\n"})
	if got := edgeTypes(t, st, "a.go::Quiet", "a.go::Helper"); slices.Contains(got, "calls") {
		t.Fatalf("body line comment produced a calls edge: %v", got)
	}
}

// TestRealCallInBodyStillCalls guards against regressions: a call in a body
// keeps its calls edge.
func TestRealCallInBodyStillCalls(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.go": "package a\n\n" +
		"func Helper() int { return 1 }\n\n" +
		"// Caller invokes the helper.\n" +
		"func Caller() int {\n\treturn Helper()\n}\n"})
	if got := edgeTypes(t, st, "a.go::Caller", "a.go::Helper"); !slices.Contains(got, "calls") {
		t.Fatalf("real call in body lost its calls edge: %v", got)
	}
}
