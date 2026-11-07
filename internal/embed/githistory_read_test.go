package embed

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// Git history rows are ordinary elements: the embed plan must pick them up
// with the same header-plus-content text as code, or commit messages would
// never be searchable by meaning.
func TestReadElementsIncludesGitHistoryRows(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertElements([]store.Element{{
		QualifiedName: "git:commit:abc1234def",
		ElementType:   "commit",
		Name:          "abc1234 rotate the signing key",
		Content:       "rotate the signing key\n\nAuthor: t <t@example.com>",
	}}); err != nil {
		t.Fatal(err)
	}
	els, err := readElements(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(els) != 1 {
		t.Fatalf("readElements = %d rows, want the commit row", len(els))
	}
	text := documentText(els[0])
	if !strings.HasPrefix(text, "commit abc1234 rotate the signing key\n") || !strings.Contains(text, "Author: t") {
		t.Fatalf("documentText = %q, want commit header then content", text)
	}
}
