package index

import (
	"strings"
	"testing"
)

// mdNestedFixture is a nested markdown document: one `#` title, five `##`
// sections, each carrying a `###` subsection.
const mdNestedFixture = `# Guide
Intro paragraph for the guide.

## Install
Run the installer.
It is fast.

### Notes
Note one.
Note two.

## Configure
Set the option.
It is required.

### Notes
Note one.
Note two.

## Verify
Run the check.
It passes.

### Notes
Note one.
Note two.

## Ship
Tag the release.
Push the tag.

### Notes
Note one.
Note two.

## Clean
Remove the temp dir.
It is safe.

### Notes
Note one.
Note two.
`

// mdNestedStoredBefore is the number of content bytes this fixture stored
// before the section-shape fix, measured on origin/main (273af4d): every
// heading held the whole rest of the document, so the same paragraph was
// stored once per ancestor.
const mdNestedStoredBefore = 1627

// TestMarkdownSectionsDoNotStoreDescendantBodies pins the stored shape of a
// markdown file: ONE element per heading, the document's top-level heading
// holding the whole document, and every deeper heading holding its OWN text
// plus the TITLES of its descendants — never a descendant's body.
//
// Before the fix a heading's section spanned all its descendants' text AND
// every descendant stored its own copy of it, so the same paragraph lived in
// the store once per ancestor: the 249 markdown files of the live leankg
// store (3,407,965 B on disk) expanded to 14,545,212 B of `doc` content,
// 4.27x, and docs/archive/workflow-opencode-agent.md (19,509 B on disk)
// expanded to 290,340 B, 14.9x.
func TestMarkdownSectionsDoNotStoreDescendantBodies(t *testing.T) {
	els := extractFileAs("guide.md", []byte(mdNestedFixture), "md").toStore()

	// (a) The element COUNT is unchanged: one element per heading, 11 of them.
	if len(els) != 11 {
		t.Fatalf("element count = %d, want 11 (one per heading)", len(els))
	}

	byName := map[string]string{}
	total := 0
	for _, e := range els {
		byName[e.QualifiedName] = e.Content
		total += len(e.Content)
	}

	// (b) The top-level heading still holds the WHOLE document, so
	// document-level summarization keeps working.
	doc, ok := byName["guide.md::Guide"]
	if !ok {
		t.Fatalf("guide.md::Guide missing; got %v", keysOf(byName))
	}
	if !strings.Contains(doc, "# Guide") || !strings.Contains(doc, "Intro paragraph for the guide.") {
		t.Errorf("guide.md::Guide lost the document head:\n%s", doc)
	}
	if !strings.Contains(doc, "Remove the temp dir.") || !strings.HasSuffix(strings.TrimRight(doc, "\n"), "Note two.") {
		t.Errorf("guide.md::Guide must hold the whole document, got %d bytes:\n%s", len(doc), doc)
	}

	// (c) A `##` holds its own text and the TITLE of its `###` grandchild,
	// but not the grandchild's body.
	section, ok := byName["guide.md::Install"]
	if !ok {
		t.Fatalf("guide.md::Install missing; got %v", keysOf(byName))
	}
	if !strings.Contains(section, "Run the installer.") {
		t.Errorf("guide.md::Install lost its own text:\n%s", section)
	}
	if !strings.Contains(section, "### Notes") {
		t.Errorf("guide.md::Install must keep the title of its descendant:\n%s", section)
	}
	if strings.Contains(section, "Note one.") {
		t.Errorf("guide.md::Install must not hold its `###` grandchild's body:\n%s", section)
	}
	if strings.Contains(section, "Remove the temp dir.") {
		t.Errorf("guide.md::Install must not hold a sibling section's body:\n%s", section)
	}

	// The grandchild keeps its own body.
	if got := byName["guide.md::Notes"]; !strings.Contains(got, "Note one.") {
		t.Errorf("guide.md::Notes must keep its own body:\n%s", got)
	}

	// (d) The stored bytes drop by roughly half: the fixture's own text is
	// stored once per section plus once for the document.
	if total > mdNestedStoredBefore*60/100 {
		t.Errorf("stored bytes = %d, want <= 60%% of the pre-fix %d (a ~50%% drop)", total, mdNestedStoredBefore)
	}
	if total < len(mdNestedFixture) {
		t.Errorf("stored bytes = %d, want at least the source size %d", total, len(mdNestedFixture))
	}
	t.Logf("stored bytes: %d (source %d, pre-fix %d)", total, len(mdNestedFixture), mdNestedStoredBefore)
}

// TestMarkdownRepeatedHeadingsGetDistinctNames pins the identity of a
// markdown element: two headings with the same text must not share a
// qualified name. The store's UNIQUE index on qualified_name would silently
// drop one of them, and the file would hold fewer elements than it has
// headings.
func TestMarkdownRepeatedHeadingsGetDistinctNames(t *testing.T) {
	const src = "# Doc\n\n## Notes\n\nfirst\n\n## Notes\n\nsecond\n"
	els := extractFileAs("doc.md", []byte(src), "md").toStore()
	if len(els) != 3 {
		t.Fatalf("element count = %d, want 3 (one per heading)", len(els))
	}
	seen := map[string]bool{}
	for _, e := range els {
		if seen[e.QualifiedName] {
			t.Fatalf("duplicate qualified name %q: the store would drop one of them", e.QualifiedName)
		}
		seen[e.QualifiedName] = true
	}
	if !seen["doc.md::Notes"] || !seen["doc.md::Notes (2)"] {
		t.Errorf("expected doc.md::Notes and doc.md::Notes (2), got %v", seen)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
