package obsidian

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// fixtureVault writes the user-note fixture: one anchored note with an
// annotation and mixed wiki-links, one plain linked note, one bare note.
func fixtureVault(t *testing.T, vault string) {
	t.Helper()
	if err := New(vault, nil).Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	writeNote(t, vault, "notes/alpha.md", `---
leankg_id: ./src/api.rs::Handler
leankg_type: function
leankg_annotation: "First pass"
---

# Alpha

Links to [[notes/beta]] and [[notes/beta|Beta alias]] and
[[notes/gamma#Heading]] and [[missing/note]].
`)
	writeNote(t, vault, "notes/beta.md", `---
leankg_type: note
---

# Beta

Back to [[notes/alpha]].
`)
	writeNote(t, vault, "notes/gamma.md", "# Gamma\n\nNo frontmatter here.\n")
}

func TestPullSyncsNotesLinksAndAnnotations(t *testing.T) {
	st := openStore(t)
	vault := filepath.Join(t.TempDir(), "vault")
	fixtureVault(t, vault)
	e := New(vault, st)

	res, err := e.Pull()
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if res.Notes != 3 {
		t.Fatalf("Notes = %d, want 3", res.Notes)
	}
	// alpha -> beta (alias collapsed), alpha -> gamma (anchor stripped),
	// alpha -> missing/note, beta -> alpha.
	if res.Links != 4 {
		t.Fatalf("Links = %d, want 4", res.Links)
	}
	if res.Documents != 1 {
		t.Fatalf("Documents = %d, want 1", res.Documents)
	}
	if res.Annotations != 1 || len(res.Conflicts) != 0 {
		t.Fatalf("Annotations = %d, conflicts = %v, want 1 import and none", res.Annotations, res.Conflicts)
	}

	els, err := st.FindExact("note/notes/beta")
	if err != nil {
		t.Fatalf("FindExact: %v", err)
	}
	if len(els) != 1 {
		t.Fatalf("FindExact(note/notes/beta) = %d elements, want 1", len(els))
	}
	beta := els[0]
	if beta.ElementType != NoteType || beta.FilePath != "notes/beta.md" {
		t.Fatalf("beta element = %+v, want a Note with FilePath notes/beta.md", beta)
	}
	if !strings.Contains(beta.Content, "Back to") {
		t.Fatalf("beta content = %q, want the note body", beta.Content)
	}

	els, err = st.FindExact("note/notes/alpha")
	if err != nil {
		t.Fatalf("FindExact: %v", err)
	}
	if len(els) != 1 {
		t.Fatalf("FindExact(note/notes/alpha) = %d elements, want 1", len(els))
	}
	alpha := els[0]
	if alpha.Metadata["leankg_id"] != "./src/api.rs::Handler" {
		t.Fatalf("alpha metadata leankg_id = %v, want the unquoted frontmatter value", alpha.Metadata["leankg_id"])
	}
	if alpha.Metadata["leankg_annotation"] != "First pass" {
		t.Fatalf("alpha metadata annotation = %v, want the unquoted value", alpha.Metadata["leankg_annotation"])
	}

	out, err := st.Outgoing("note/notes/alpha")
	if err != nil {
		t.Fatalf("Outgoing: %v", err)
	}
	var actual []string
	for _, r := range out {
		actual = append(actual, r.Target+"|"+r.RelType)
	}
	sort.Strings(actual)
	want := []string{
		"./src/api.rs::Handler|" + DocumentsRel,
		"note/missing/note|" + LinkRel,
		"note/notes/beta|" + LinkRel,
		"note/notes/gamma|" + LinkRel,
	}
	if strings.Join(actual, ",") != strings.Join(want, ",") {
		t.Fatalf("alpha outgoing = %v, want %v", actual, want)
	}

	ann, ok, err := st.KVGet(AnnotationNamespace, "./src/api.rs::Handler")
	if err != nil || !ok || ann != "First pass" {
		t.Fatalf("KV annotation = (%q, %v, err %v), want \"First pass\"", ann, ok, err)
	}

	// Idempotency: the second pull over the unchanged vault converges — same
	// rows, nothing new, no duplicate elements or relationships.
	res2, err := e.Pull()
	if err != nil {
		t.Fatalf("second Pull: %v", err)
	}
	if res2.Notes != 3 || res2.Links != 4 || res2.Documents != 1 || res2.Annotations != 0 || len(res2.Conflicts) != 0 {
		t.Fatalf("second Pull = %+v, want identical counts and no new annotations", res2)
	}
	if n, err := st.ElementCount(); err != nil || n != 3 {
		t.Fatalf("ElementCount = (%d, %v), want 3", n, err)
	}
	if n, err := st.RelationshipCount(); err != nil || n != 5 {
		t.Fatalf("RelationshipCount = (%d, %v), want 5 (4 links + 1 documents edge)", n, err)
	}
}

func TestPullAnnotationConflictAndEmpty(t *testing.T) {
	st := openStore(t)
	vault := filepath.Join(t.TempDir(), "vault")
	if err := New(vault, st).Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	writeNote(t, vault, "local.md", "---\nleankg_id: Element\nleankg_annotation: \"local\"\n---\n")
	writeNote(t, vault, "cleared.md", "---\nleankg_id: Other\nleankg_annotation: \"\"\n---\n")

	e := New(vault, st)
	res, err := e.Pull()
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if res.Annotations != 1 || len(res.Conflicts) != 0 {
		t.Fatalf("first Pull = %+v, want one import, no conflicts", res)
	}
	// The empty annotation records nothing (Rust created an empty row; it
	// held no information).
	if _, ok, err := st.KVGet(AnnotationNamespace, "Other"); err != nil || ok {
		t.Fatalf("KV for Other = (_, %v, %v), want absent", ok, err)
	}

	// An edited annotation against a stored one is a conflict, and the store
	// keeps its value.
	writeNote(t, vault, "local.md", "---\nleankg_id: Element\nleankg_annotation: \"remote edit\"\n---\n")
	res, err = e.Pull()
	if err != nil {
		t.Fatalf("second Pull: %v", err)
	}
	if res.Annotations != 0 || len(res.Conflicts) != 1 {
		t.Fatalf("second Pull = %+v, want one conflict, no import", res)
	}
	c := res.Conflicts[0]
	if c.ElementID != "Element" || c.LocalAnnotation != "local" || c.RemoteAnnotation != "remote edit" {
		t.Fatalf("conflict = %+v, want Element local/remote edit", c)
	}
	ann, ok, err := st.KVGet(AnnotationNamespace, "Element")
	if err != nil || !ok || ann != "local" {
		t.Fatalf("KV after conflict = (%q, %v, %v), want \"local\"", ann, ok, err)
	}

	// Re-pulling an unchanged divergent note reports the same conflict —
	// resolution is manual, like the Rust pull.
	res, err = e.Pull()
	if err != nil {
		t.Fatalf("third Pull: %v", err)
	}
	if len(res.Conflicts) != 1 {
		t.Fatalf("third Pull conflicts = %v, want the divergence still reported", res.Conflicts)
	}
}

func TestPullMissingVaultIsEmpty(t *testing.T) {
	st := openStore(t)
	e := New(filepath.Join(t.TempDir(), "absent"), st)
	res, err := e.Pull()
	if err != nil {
		t.Fatalf("Pull on missing vault: %v", err)
	}
	if res.Notes != 0 || res.Links != 0 || res.Documents != 0 || res.Annotations != 0 {
		t.Fatalf("Pull on missing vault = %+v, want a zero result", res)
	}
}

func TestLineCount(t *testing.T) {
	cases := []struct {
		content string
		want    int
	}{
		{"", 0},
		{"a", 1},
		{"a\n", 1},
		{"a\nb", 2},
		{"a\nb\n", 2},
	}
	for _, tc := range cases {
		if got := lineCount(tc.content); got != tc.want {
			t.Fatalf("lineCount(%q) = %d, want %d", tc.content, got, tc.want)
		}
	}
}
