package obsidian

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

func TestNotePath(t *testing.T) {
	cases := []struct {
		name string
		el   store.Element
		want string
	}{
		{"qualified name splits on ::", store.Element{QualifiedName: "./src/main.rs::main", ElementType: "function"}, "./src/main.rs/main.md"},
		{"bare colon becomes underscore", store.Element{QualifiedName: "Cargo.toml:dep", ElementType: "dependency"}, "Cargo.toml_dep.md"},
		{"spaces and parens become underscores", store.Element{QualifiedName: "impl Display for Foo (x)", ElementType: "impl"}, "impl_Display_for_Foo__x_.md"},
		{"folder notes get the folder suffix", store.Element{QualifiedName: "./src/api", ElementType: "Folder"}, "./src/api.folder.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NotePath(tc.el); got != tc.want {
				t.Fatalf("NotePath(%q) = %q, want %q", tc.el.QualifiedName, got, tc.want)
			}
		})
	}
}

// seedPushStore mirrors the fixture the Rust push operated over: code elements
// with relationships, an annotation, and paths the export filter must skip.
func seedPushStore(t *testing.T, st store.Backend) {
	t.Helper()
	if err := st.UpsertElements([]store.Element{
		{QualifiedName: "./src/api.rs::Handler", ElementType: "function", Name: "Handler", FilePath: "./src/api.rs", LineStart: 10, LineEnd: 20, Language: "rust"},
		{QualifiedName: "./src/util.rs", ElementType: "File", Name: "util.rs", FilePath: "./src/util.rs", LineStart: 1, LineEnd: 40, Language: "rust"},
		{QualifiedName: "pkg/thing.go::Thing", ElementType: "function", Name: "Thing", FilePath: "pkg/thing.go", LineStart: 1, LineEnd: 3, Language: "go"},
		{QualifiedName: "/repo/node_modules/x.js::X", ElementType: "function", Name: "X", FilePath: "/repo/node_modules/x.js", LineStart: 1, LineEnd: 2, Language: "js"},
		{QualifiedName: "pkg/thing.test.go::TestThing", ElementType: "function", Name: "TestThing", FilePath: "pkg/thing.test.go", LineStart: 1, LineEnd: 2, Language: "go"},
		{QualifiedName: NotePrefix + "notes/kept", ElementType: NoteType, Name: "kept", FilePath: "notes/kept.md", LineStart: 1, LineEnd: 1, Language: "md"},
	}); err != nil {
		t.Fatalf("seed elements: %v", err)
	}
	if err := st.UpsertRelationships([]store.Relationship{
		{Source: "./src/api.rs::Handler", Target: "./src/main.rs::main", RelType: "calls", Confidence: 1},
		{Source: "pkg/thing.go::Thing", Target: "std::fmt::Display", RelType: "imports", Confidence: 1},
		{Source: "pkg/thing.go::Thing", Target: "__unresolved__Helper", RelType: "calls", Confidence: 1},
		{Source: "pkg/thing.go::Thing", Target: "Helper", RelType: "calls", Confidence: 1},
		{Source: "pkg/thing.go::Thing", Target: "./pkg", RelType: "imports", Confidence: 1},
		{Source: "pkg/thing.go::Thing", Target: "./pkg/other.go::Other", RelType: "calls", Confidence: 1},
	}); err != nil {
		t.Fatalf("seed relationships: %v", err)
	}
	if err := st.KVSet(AnnotationNamespace, "./src/api.rs::Handler", "Handles requests"); err != nil {
		t.Fatalf("seed annotation: %v", err)
	}
}

func TestRenderNoteTemplate(t *testing.T) {
	el := store.Element{
		QualifiedName: "./src/api.rs::Handler",
		ElementType:   "function",
		Name:          "Handler",
		FilePath:      "./src/api.rs",
		LineStart:     10,
		LineEnd:       20,
	}
	rels := []store.Relationship{{Source: el.QualifiedName, Target: "./src/main.rs::main", RelType: "calls"}}
	meta := BuildMetadata(el, rels, "Handles requests", time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC))

	const want = `---
leankg_id: ./src/api.rs::Handler
leankg_type: function
leankg_file: ./src/api.rs
leankg_line: 10-20
leankg_relationships:
  - ./src/main.rs::main (calls)
leankg_annotation: "Handles requests"
created: 2026-09-12T10:00:00Z
updated: 2026-09-12T10:00:00Z
---

# Handler

**Type**: function
**File**: ` + "`./src/api.rs`" + `
**Lines**: 10-20

> **Annotation**: Handles requests
**Relationships**:
  - ./src/main.rs::main (calls)

- [[src/main.rs/main]] (calls)
`
	if got := RenderNote(el, meta); got != want {
		t.Fatalf("note content mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderNoteEmptyRelationshipsAndAnnotation(t *testing.T) {
	el := store.Element{
		QualifiedName: "pkg/a.go::A",
		ElementType:   "function",
		Name:          "A",
		FilePath:      "pkg/a.go",
		LineStart:     4,
		LineEnd:       9,
	}
	got := RenderNote(el, BuildMetadata(el, nil, "", time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)))
	for _, want := range []string{
		"leankg_relationships:\n  (none)\n",
		"leankg_annotation: \"\"\n",
		"**Relationships**:\n  (none)\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("note missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "> **Annotation**") || strings.Contains(got, "[[") {
		t.Fatalf("empty metadata must render no annotation block and no links:\n%s", got)
	}
}

func TestBuildMetadataFiltersWikiLinks(t *testing.T) {
	el := store.Element{
		QualifiedName: "pkg/thing.go::Thing",
		ElementType:   "function",
		Name:          "Thing",
		FilePath:      "pkg/thing.go",
	}
	rels := []store.Relationship{
		{Target: "std::fmt::Display", RelType: "imports"},
		{Target: "__unresolved__Helper", RelType: "calls"},
		{Target: "Helper", RelType: "calls"},
		{Target: "./pkg", RelType: "imports"},
		{Target: "./pkg/other.go::Other", RelType: "calls"},
	}
	meta := BuildMetadata(el, rels, "", time.Now())

	wantLinks := []string{
		"- [[pkg.folder]] (imports)",
		"- [[pkg/other.go/Other]] (calls)",
	}
	if len(meta.WikiLinks) != len(wantLinks) {
		t.Fatalf("WikiLinks = %v, want %v", meta.WikiLinks, wantLinks)
	}
	for i, want := range wantLinks {
		if meta.WikiLinks[i] != want {
			t.Fatalf("WikiLinks[%d] = %q, want %q", i, meta.WikiLinks[i], want)
		}
	}
	// The frontmatter relationship list keeps every relationship, filters and all.
	if len(meta.Relationships) != len(rels) {
		t.Fatalf("Relationships = %v, want all %d entries", meta.Relationships, len(rels))
	}
	if meta.Relationships[0] != "std::fmt::Display (imports)" {
		t.Fatalf("Relationships[0] = %q", meta.Relationships[0])
	}
}

func TestBuildMetadataFileParentFolderLink(t *testing.T) {
	nested := BuildMetadata(store.Element{QualifiedName: "./src/util.rs", ElementType: "File", FilePath: "./src/util.rs"}, nil, "", time.Now())
	if len(nested.WikiLinks) != 1 || nested.WikiLinks[0] != "- [[src.folder]] (contained_by)" {
		t.Fatalf("nested file links = %v, want the parent folder link", nested.WikiLinks)
	}
	root := BuildMetadata(store.Element{QualifiedName: "main.rs", ElementType: "File", FilePath: "main.rs"}, nil, "", time.Now())
	if len(root.WikiLinks) != 0 {
		t.Fatalf("root file links = %v, want none (parent is '.')", root.WikiLinks)
	}
}

func TestPushWritesNotesForStoreElements(t *testing.T) {
	st := openStore(t)
	seedPushStore(t, st)
	vault := filepath.Join(t.TempDir(), "vault")
	e := New(vault, st)

	res, err := e.Push()
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	// Handler, the File element, and Thing get notes; the node_modules path,
	// the test file, and the Note element do not.
	if res.Notes != 3 || res.Failed != 0 {
		t.Fatalf("Push = %+v, want 3 notes and no failures", res)
	}
	got := vaultFiles(t, vault)
	sort.Strings(got)
	want := []string{"pkg/thing.go/Thing.md", "src/api.rs/Handler.md", "src/util.rs.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("vault files = %v, want %v", got, want)
	}

	// The pushed note carries the store's annotation and its own stamps.
	content, err := os.ReadFile(filepath.Join(vault, "src", "api.rs", "Handler.md"))
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	meta, _ := splitFrontmatter(string(content))
	for _, key := range []string{"created", "updated"} {
		if v := meta[key]; len(v) != len("2026-09-12T10:00:00Z") || !strings.HasSuffix(v, "Z") {
			t.Fatalf("%s = %q, want an RFC3339 UTC stamp", key, v)
		}
	}
	el := store.Element{
		QualifiedName: "./src/api.rs::Handler",
		ElementType:   "function",
		Name:          "Handler",
		FilePath:      "./src/api.rs",
		LineStart:     10,
		LineEnd:       20,
	}
	rels := []store.Relationship{{Source: el.QualifiedName, Target: "./src/main.rs::main", RelType: "calls"}}
	stamp, err := time.Parse(noteTimestampLayout, meta["created"])
	if err != nil {
		t.Fatalf("created stamp unparsable: %v", err)
	}
	wantContent := RenderNote(el, BuildMetadata(el, rels, "Handles requests", stamp))
	if string(content) != wantContent {
		t.Fatalf("pushed note mismatch\n--- got ---\n%s\n--- want ---\n%s", content, wantContent)
	}

	// Thing links only the vault-shaped targets.
	thing, err := os.ReadFile(filepath.Join(vault, "pkg", "thing.go", "Thing.md"))
	if err != nil {
		t.Fatalf("read Thing note: %v", err)
	}
	// The wiki-link section follows the body relationship list after a blank
	// line. The relationship list itself keeps every relationship (Rust
	// parity); only the links are filtered.
	_, rest, ok := strings.Cut(string(thing), "**Relationships**:\n")
	if !ok {
		t.Fatalf("Thing note has no relationships section:\n%s", thing)
	}
	_, wiki, ok := strings.Cut(rest, "\n\n")
	if !ok {
		t.Fatalf("Thing note has no wiki-link section:\n%s", rest)
	}
	for _, wantLink := range []string{"[[pkg.folder]] (imports)", "[[pkg/other.go/Other]] (calls)"} {
		if !strings.Contains(wiki, wantLink) {
			t.Fatalf("Thing wiki-links missing %q:\n%s", wantLink, wiki)
		}
	}
	for _, filtered := range []string{"std::", "__unresolved__", "[[Helper]]"} {
		if strings.Contains(wiki, filtered) {
			t.Fatalf("Thing wiki-links must drop %q:\n%s", filtered, wiki)
		}
	}
}

func TestPushIsRepeatable(t *testing.T) {
	st := openStore(t)
	seedPushStore(t, st)
	vault := filepath.Join(t.TempDir(), "vault")
	e := New(vault, st)

	if _, err := e.Push(); err != nil {
		t.Fatalf("first Push: %v", err)
	}
	first := vaultFiles(t, vault)
	firstContent := map[string]string{}
	for _, rel := range first {
		b, err := os.ReadFile(filepath.Join(vault, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		firstContent[rel] = withoutStamps(string(b))
	}

	res, err := e.Push()
	if err != nil {
		t.Fatalf("second Push: %v", err)
	}
	if res.Notes != 3 {
		t.Fatalf("second Push notes = %d, want 3", res.Notes)
	}
	second := vaultFiles(t, vault)
	if strings.Join(first, ",") != strings.Join(second, ",") {
		t.Fatalf("second push changed the vault file set: %v -> %v", first, second)
	}
	for _, rel := range second {
		b, err := os.ReadFile(filepath.Join(vault, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if got := withoutStamps(string(b)); got != firstContent[rel] {
			t.Fatalf("note %s differs between pushes\n--- first ---\n%s\n--- second ---\n%s", rel, firstContent[rel], got)
		}
	}
}

// withoutStamps drops the created/updated lines so two renders can be compared
// across the clock.
func withoutStamps(note string) string {
	var kept []string
	for _, line := range strings.Split(note, "\n") {
		if strings.HasPrefix(line, "created: ") || strings.HasPrefix(line, "updated: ") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
